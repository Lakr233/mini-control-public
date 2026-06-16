import Foundation
import Logging
import NIOCore
import NIOPosix

/// ProxyRelay manages a single proxy session: WebSocket ↔ TCP relay.
/// Connects to the server via WebSocket and to a VM via TCP, relaying data bidirectionally.
public actor ProxyRelay {
    private let logger: Logger
    private let tlsCertFingerprint: String
    private var wsTask: URLSessionWebSocketTask?
    private var tcpChannel: Channel?
    private var urlSession: URLSession?
    private var tcpToWSContinuation: AsyncStream<Data>.Continuation?
    private var tcpToWSTask: Task<Void, Never>?
    private var isClosed = false

    public init(logger: Logger, tlsCertFingerprint: String = "") {
        self.logger = logger
        self.tlsCertFingerprint = tlsCertFingerprint
    }

    deinit {
        urlSession?.invalidateAndCancel()
    }

    /// Start the relay: connect WebSocket to server, TCP to VM, relay data.
    public func start(
        serverBaseURL: String, sessionID: String, token: String, vmIP: String, port: Int,
    ) async {
        logger.info(
            "Starting proxy relay",
            metadata: [
                "session_id": "\(sessionID)", "vm_ip": "\(vmIP)", "port": "\(port)",
            ],
        )

        // Build WebSocket URL
        guard let wsURL = makeWebSocketURL(baseURL: serverBaseURL, sessionID: sessionID, token: token)
        else {
            logger.error("Invalid WebSocket URL", metadata: ["base_url": "\(serverBaseURL)"])
            return
        }

        // Connect WebSocket with certificate pinning
        let session = URLSession(
            configuration: .default, delegate: CertPinningDelegate(fingerprint: tlsCertFingerprint),
            delegateQueue: nil,
        )
        urlSession = session
        let ws = session.webSocketTask(with: wsURL)
        ws.resume()
        wsTask = ws

        // Connect TCP to VM
        let group = MultiThreadedEventLoopGroup(numberOfThreads: 1)
        var tcpContinuation: AsyncStream<Data>.Continuation?
        let tcpStream = AsyncStream<Data> { continuation in
            tcpContinuation = continuation
        }
        tcpToWSContinuation = tcpContinuation
        let tcpHandler = TCPRelayHandler(output: tcpContinuation, logger: logger)

        do {
            let bootstrap = ClientBootstrap(group: group)
                .channelInitializer { channel in
                    channel.pipeline.addHandler(tcpHandler)
                }
                .connectTimeout(.seconds(10))

            let channel = try await bootstrap.connect(host: vmIP, port: port).get()
            tcpChannel = channel

            logger.info(
                "Proxy relay connected",
                metadata: [
                    "session_id": "\(sessionID)", "vm_ip": "\(vmIP)", "port": "\(port)",
                ],
            )

            tcpToWSTask = Task { [logger] in
                await Self.tcpToWebSocketLoop(
                    ws: ws,
                    logger: logger,
                    sessionID: sessionID,
                    port: port,
                    dataStream: tcpStream,
                )
            }

            // WS → TCP loop (runs until WebSocket closes)
            await wsToTCPLoop(ws: ws, channel: channel, sessionID: sessionID, port: port)

        } catch {
            logger.error(
                "Proxy relay TCP connect failed",
                metadata: [
                    "session_id": "\(sessionID)", "vm_ip": "\(vmIP)", "port": "\(port)", "error": "\(error)",
                ],
            )
        }

        // Cleanup
        await stop()
        try? await group.shutdownGracefully()

        logger.info("Proxy relay ended", metadata: ["session_id": "\(sessionID)"])
    }

    public func stop() async {
        guard !isClosed else { return }
        isClosed = true
        tcpToWSContinuation?.finish()
        tcpToWSContinuation = nil
        tcpToWSTask?.cancel()
        tcpToWSTask = nil
        wsTask?.cancel(with: .normalClosure, reason: nil)
        try? await tcpChannel?.close()
        tcpChannel = nil
        urlSession?.invalidateAndCancel()
        urlSession = nil
    }

    // MARK: - WebSocket → TCP

    private func wsToTCPLoop(
        ws: URLSessionWebSocketTask, channel: Channel, sessionID: String, port: Int,
    ) async {
        while !isClosed {
            do {
                let message = try await ws.receive()
                switch message {
                case let .data(data):
                    logger.debug(
                        "Proxy relay WS->TCP",
                        metadata: [
                            "session_id": "\(sessionID)", "port": "\(port)", "bytes": "\(data.count)",
                        ],
                    )
                    let buffer = channel.allocator.buffer(bytes: data)
                    try await channel.writeAndFlush(buffer)
                case let .string(text):
                    let data = Data(text.utf8)
                    logger.debug(
                        "Proxy relay WS->TCP text frame",
                        metadata: [
                            "session_id": "\(sessionID)", "port": "\(port)", "bytes": "\(data.count)",
                        ],
                    )
                    let buffer = channel.allocator.buffer(bytes: data)
                    try await channel.writeAndFlush(buffer)
                @unknown default:
                    logger.warning(
                        "Proxy relay WS->TCP unknown frame",
                        metadata: [
                            "session_id": "\(sessionID)", "port": "\(port)",
                        ],
                    )
                }
            } catch {
                logger.info(
                    "Proxy relay WS->TCP loop ended",
                    metadata: [
                        "session_id": "\(sessionID)", "port": "\(port)", "error": "\(error)",
                    ],
                )
                break
            }
        }
    }

    private static func tcpToWebSocketLoop(
        ws: URLSessionWebSocketTask,
        logger: Logger,
        sessionID: String,
        port: Int,
        dataStream: AsyncStream<Data>,
    ) async {
        for await data in dataStream {
            do {
                logger.debug(
                    "Proxy relay TCP->WS",
                    metadata: [
                        "session_id": "\(sessionID)", "port": "\(port)", "bytes": "\(data.count)",
                    ],
                )
                try await ws.send(.data(data))
            } catch {
                logger.warning(
                    "Proxy relay TCP->WS send failed",
                    metadata: [
                        "session_id": "\(sessionID)", "port": "\(port)", "error": "\(error)",
                    ],
                )
                return
            }
        }

        logger.info(
            "Proxy relay TCP->WS loop ended",
            metadata: [
                "session_id": "\(sessionID)", "port": "\(port)",
            ],
        )
    }

    private func makeWebSocketURL(baseURL: String, sessionID: String, token: String) -> URL? {
        guard var components = URLComponents(string: baseURL), components.host != nil else {
            return nil
        }
        components.scheme = components.scheme == "https" ? "wss" : "ws"
        components.path = "/api/v1/proxy/ws"
        components.queryItems = [
            URLQueryItem(name: "session", value: sessionID),
            URLQueryItem(name: "role", value: "worker"),
            URLQueryItem(name: "token", value: token),
        ]
        return components.url
    }
}

// MARK: - TCP → WebSocket NIO Handler

/// Reads TCP data and forwards to WebSocket.
final class TCPRelayHandler: ChannelInboundHandler, @unchecked Sendable {
    typealias InboundIn = ByteBuffer

    private let output: AsyncStream<Data>.Continuation?
    private let logger: Logger

    init(output: AsyncStream<Data>.Continuation?, logger: Logger) {
        self.output = output
        self.logger = logger
    }

    func channelRead(context _: ChannelHandlerContext, data: NIOAny) {
        let buffer = unwrapInboundIn(data)
        guard let bytes = buffer.getBytes(at: buffer.readerIndex, length: buffer.readableBytes) else {
            return
        }
        output?.yield(Data(bytes))
    }

    func channelInactive(context _: ChannelHandlerContext) {
        logger.info("TCP relay target disconnected")
        output?.finish()
    }

    func errorCaught(context: ChannelHandlerContext, error: Error) {
        logger.warning("TCP relay error", metadata: ["error": "\(error)"])
        output?.finish()
        context.close(promise: nil)
    }
}
