import Darwin
import Foundation
import Logging

private let receiveBufferSize = 4096
private let maxRequestBytes = 16384
private let listenBacklog: Int32 = 16

/// Minimal HTTP server on a unix socket that serves the overlay snapshot to
/// the GUI overlay client running in the logged-in user session.
public final class OverlayHTTPServer: @unchecked Sendable {
    private let socketPath: String
    private let stateStore: OverlayStateStore
    private let logger: Logger
    private let queue = DispatchQueue(label: "mini-control.overlay.http")

    private var listenFD: Int32 = -1
    private var source: DispatchSourceRead?

    public init(socketPath: String, stateStore: OverlayStateStore, logger: Logger) {
        self.socketPath = socketPath
        self.stateStore = stateStore
        self.logger = logger
    }

    deinit {
        stop()
    }

    public func start() throws {
        stop()

        let socketDir = (socketPath as NSString).deletingLastPathComponent
        try FileManager.default.createDirectory(atPath: socketDir, withIntermediateDirectories: true)
        _ = socketPath.withCString { unlink($0) }

        listenFD = socket(AF_UNIX, Int32(SOCK_STREAM), 0)
        guard listenFD >= 0 else {
            throw OverlayBridgeError.socketCreationFailed(errno)
        }

        var value: Int32 = 1
        setsockopt(listenFD, SOL_SOCKET, SO_REUSEADDR, &value, socklen_t(MemoryLayout<Int32>.size))

        var addr: sockaddr_un
        let addrLen: socklen_t
        do {
            (addr, addrLen) = try UnixSocketAddress.make(path: socketPath)
        } catch {
            close(listenFD)
            listenFD = -1
            throw error
        }

        let bindResult = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                bind(listenFD, $0, addrLen)
            }
        }
        guard bindResult == 0 else {
            let code = errno
            close(listenFD)
            listenFD = -1
            throw OverlayBridgeError.bindFailed(code, socketPath)
        }

        _ = socketPath.withCString { chmod($0, mode_t(0o600)) }

        guard listen(listenFD, listenBacklog) == 0 else {
            let code = errno
            close(listenFD)
            listenFD = -1
            throw OverlayBridgeError.listenFailed(code)
        }

        let source = DispatchSource.makeReadSource(fileDescriptor: listenFD, queue: queue)
        source.setEventHandler { [weak self] in
            self?.acceptConnections()
        }
        source.setCancelHandler { [weak self] in
            guard let self else { return }
            if listenFD >= 0 {
                close(listenFD)
                listenFD = -1
            }
        }
        self.source = source
        source.resume()
        logger.info("Overlay HTTP server started", metadata: ["socket": "\(socketPath)"])
    }

    public func stop() {
        source?.cancel()
        source = nil
        if listenFD >= 0 {
            close(listenFD)
            listenFD = -1
        }
        _ = socketPath.withCString { unlink($0) }
    }

    private func acceptConnections() {
        while true {
            let clientFD = accept(listenFD, nil, nil)
            if clientFD < 0 {
                if errno == EAGAIN || errno == EWOULDBLOCK {
                    return
                }
                logger.warning("Overlay accept failed", metadata: ["errno": "\(errno)"])
                return
            }
            Task.detached { [weak self] in
                await self?.handleClient(clientFD)
            }
        }
    }

    private func handleClient(_ clientFD: Int32) async {
        defer { close(clientFD) }

        guard let request = readRequest(from: clientFD) else {
            return
        }
        let requestLine = request.split(separator: "\n", maxSplits: 1).first.map(String.init) ?? ""

        if requestLine.hasPrefix("GET /state ") {
            let snapshot = await stateStore.snapshot()
            do {
                let responseBody = try JSONEncoder().encode(snapshot)
                writeResponse(status: "200 OK", body: responseBody, to: clientFD)
            } catch {
                writeResponse(status: "500 Internal Server Error", body: Data("{}".utf8), to: clientFD)
            }
            return
        }

        writeResponse(status: "404 Not Found", body: Data("{}".utf8), to: clientFD)
    }

    private func readRequest(from clientFD: Int32) -> String? {
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: receiveBufferSize)

        while data.count < maxRequestBytes {
            let received = recv(clientFD, &buffer, buffer.count, 0)
            if received < 0 {
                return nil
            }
            if received == 0 {
                break
            }
            data.append(buffer, count: received)
            if data.range(of: Data("\r\n\r\n".utf8)) != nil {
                break
            }
        }

        return String(data: data, encoding: .utf8)
    }

    private func writeResponse(status: String, body: Data, to clientFD: Int32) {
        let headers =
            "HTTP/1.1 \(status)\r\nContent-Type: application/json\r\nContent-Length: \(body.count)\r\nConnection: close\r\n\r\n"
        _ = headers.withCString { ptr in
            send(clientFD, ptr, strlen(ptr), 0)
        }
        body.withUnsafeBytes { bytes in
            guard let base = bytes.baseAddress else { return }
            _ = send(clientFD, base, bytes.count, 0)
        }
    }
}
