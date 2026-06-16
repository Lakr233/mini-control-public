import Foundation

public enum WorkerConfigNetworkError: Error, CustomStringConvertible {
    case invalidServerAddress(String)

    public var description: String {
        switch self {
        case let .invalidServerAddress(address):
            "Invalid server address: \(address)"
        }
    }
}

public extension WorkerConfig {
    func httpBaseURL() throws -> URL {
        try baseURL(
            for: useHTTPS ? "https" : "http", explicitPort: httpPort > 0 ? httpPort : nil,
            defaultPort: 8080,
        )
    }

    func websocketURL(path: String, queryItems: [URLQueryItem] = []) throws -> URL {
        var components = try baseComponents(for: useHTTPS ? "wss" : "ws")
        components.port = httpPort > 0 ? httpPort : (components.port ?? 8080)
        components.path = path
        components.queryItems = queryItems.isEmpty ? nil : queryItems
        guard let url = components.url else {
            throw WorkerConfigNetworkError.invalidServerAddress(serverAddress)
        }
        return url
    }

    func healthCheckURL() throws -> URL {
        try httpBaseURL().appending(path: "api/v1/health")
    }

    private func baseURL(for scheme: String, explicitPort: Int?, defaultPort: Int) throws -> URL {
        var components = try baseComponents(for: scheme)
        components.port = explicitPort ?? (components.port ?? defaultPort)
        components.path = ""
        components.query = nil
        components.fragment = nil
        guard let url = components.url else {
            throw WorkerConfigNetworkError.invalidServerAddress(serverAddress)
        }
        return url
    }

    private func baseComponents(for scheme: String) throws -> URLComponents {
        let rawAddress: String =
            if serverAddress.contains("://") {
                serverAddress
            } else {
                "\(scheme)://\(serverAddress)"
            }

        guard var components = URLComponents(string: rawAddress), components.host != nil else {
            throw WorkerConfigNetworkError.invalidServerAddress(serverAddress)
        }

        components.scheme = scheme
        return components
    }
}
