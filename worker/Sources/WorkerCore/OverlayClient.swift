import Darwin
import Foundation
import Logging

private let receiveBufferSize = 4096
private let socketTimeout = timeval(tv_sec: 3, tv_usec: 0)
private let snapshotPollInterval: Duration = .seconds(2)

public enum OverlayClientSource {
    case socket(String)
    case stateFile(String)

    var description: String {
        switch self {
        case let .socket(path):
            "socket:\(path)"
        case let .stateFile(path):
            "state-file:\(path)"
        }
    }
}

public enum OverlayHTTPClient {
    public static func fetchSnapshot(socketPath: String) async throws -> OverlaySnapshot {
        try await Task.detached(priority: .userInitiated) {
            try fetchSnapshotSync(socketPath: socketPath)
        }.value
    }

    private static func fetchSnapshotSync(socketPath: String) throws -> OverlaySnapshot {
        let fd = socket(AF_UNIX, Int32(SOCK_STREAM), 0)
        guard fd >= 0 else {
            throw OverlayBridgeError.socketCreationFailed(errno)
        }
        defer { close(fd) }

        var timeout = socketTimeout
        setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &timeout, socklen_t(MemoryLayout<timeval>.size))
        setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &timeout, socklen_t(MemoryLayout<timeval>.size))

        var (addr, addrLen) = try UnixSocketAddress.make(path: socketPath)
        let result = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                connect(fd, $0, addrLen)
            }
        }
        guard result == 0 else {
            throw OverlayBridgeError.connectFailed(errno, socketPath)
        }

        let request = "GET /state HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
        _ = request.withCString { ptr in
            send(fd, ptr, strlen(ptr), 0)
        }

        var data = Data()
        var buffer = [UInt8](repeating: 0, count: receiveBufferSize)
        while true {
            let received = recv(fd, &buffer, buffer.count, 0)
            if received <= 0 {
                break
            }
            data.append(buffer, count: received)
        }

        guard let headerRange = data.range(of: Data("\r\n\r\n".utf8)) else {
            throw OverlayBridgeError.missingBody
        }
        let headerData = data[..<headerRange.lowerBound]
        guard let headerString = String(data: headerData, encoding: .utf8),
              headerString.hasPrefix("HTTP/1.1 200")
        else {
            throw OverlayBridgeError.badHTTPResponse
        }
        let body = data[headerRange.upperBound...]
        return try JSONDecoder().decode(OverlaySnapshot.self, from: body)
    }
}

public enum OverlayStateFileClient {
    public static func fetchSnapshot(stateFilePath: String) async throws -> OverlaySnapshot {
        try await Task.detached(priority: .userInitiated) {
            let data = try Data(contentsOf: URL(fileURLWithPath: stateFilePath))
            return try JSONDecoder().decode(OverlaySnapshot.self, from: data)
        }.value
    }
}

@MainActor
public final class OverlayClientCoordinator {
    private let source: OverlayClientSource
    private let logger: Logger
    private let overlay: LockScreenOverlay
    private var running = true

    public init(source: OverlayClientSource, logger: Logger) {
        self.source = source
        self.logger = logger
        overlay = LockScreenOverlay(logger: logger)
    }

    public func run() async {
        overlay.start()
        while running, !Task.isCancelled {
            do {
                let snapshot: OverlaySnapshot =
                    switch source {
                    case let .socket(path):
                        try await OverlayHTTPClient.fetchSnapshot(socketPath: path)
                    case let .stateFile(path):
                        try await OverlayStateFileClient.fetchSnapshot(stateFilePath: path)
                    }
                overlay.render(snapshot: snapshot)
            } catch {
                overlay.renderConnectionError("overlay unavailable: \(error)")
                logger.warning(
                    "Overlay client poll failed",
                    metadata: [
                        "source": "\(source.description)",
                        "error": "\(error)",
                    ],
                )
            }
            try? await Task.sleep(for: snapshotPollInterval)
        }
        overlay.stop()
    }

    public func stop() {
        running = false
        overlay.stop()
    }
}
