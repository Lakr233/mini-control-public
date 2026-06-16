import Darwin
import Foundation

public enum OverlayBridgeError: Error, CustomStringConvertible {
    case socketCreationFailed(Int32)
    case socketPathTooLong(String)
    case bindFailed(Int32, String)
    case connectFailed(Int32, String)
    case listenFailed(Int32)
    case badHTTPResponse
    case missingBody

    public var description: String {
        switch self {
        case let .socketCreationFailed(code):
            "failed to create overlay socket (errno=\(code))"
        case let .socketPathTooLong(path):
            "overlay socket path too long: \(path)"
        case let .bindFailed(code, path):
            "failed to bind overlay socket \(path) (errno=\(code))"
        case let .connectFailed(code, path):
            "failed to connect to overlay socket \(path) (errno=\(code))"
        case let .listenFailed(code):
            "failed to listen on overlay socket (errno=\(code))"
        case .badHTTPResponse:
            "invalid overlay HTTP response"
        case .missingBody:
            "overlay HTTP response missing body"
        }
    }
}

enum UnixSocketAddress {
    /// Builds a `sockaddr_un` for the given path, throwing when the path
    /// does not fit in `sun_path`. Shared by the overlay server and client.
    static func make(path: String) throws -> (address: sockaddr_un, length: socklen_t) {
        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let pathBytes = path.utf8CString
        guard pathBytes.count <= MemoryLayout.size(ofValue: addr.sun_path) else {
            throw OverlayBridgeError.socketPathTooLong(path)
        }
        let pathStorageSize = MemoryLayout.size(ofValue: addr.sun_path)
        withUnsafeMutablePointer(to: &addr.sun_path) { ptr in
            let raw = UnsafeMutableRawPointer(ptr)
            raw.initializeMemory(as: CChar.self, repeating: 0, count: pathStorageSize)
            _ = pathBytes.withUnsafeBytes { bytes in
                memcpy(raw, bytes.baseAddress!, bytes.count)
            }
        }
        let length = socklen_t(MemoryLayout.size(ofValue: addr.sun_family) + pathBytes.count)
        return (addr, length)
    }
}
