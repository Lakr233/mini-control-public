import Foundation
import Logging

/// MiniControlAPIClient talks to the mini-control server over HTTPS REST endpoints.
public actor MiniControlAPIClient {
    private let config: WorkerConfig
    private let logger: Logger
    private let baseURL: URL?
    private let urlSession: URLSession

    /// Connection state
    private var workerID: String = ""
    private var authToken: String
    private let workerVersion: String

    public init(config: WorkerConfig, workerVersion: String = "0.0.0", logger: Logger) {
        self.workerVersion = workerVersion
        self.config = config
        self.logger = logger
        authToken = config.preferredAuthToken()
        baseURL = try? config.httpBaseURL()
        let delegate = CertPinningDelegate(fingerprint: config.tlsCertFingerprint)
        urlSession = URLSession(configuration: .default, delegate: delegate, delegateQueue: nil)
    }

    deinit {
        urlSession.invalidateAndCancel()
    }

    // MARK: - Public Interface

    /// Register with the server. Returns registration info.
    public func register(
        hostname: String,
        hardwareUUID: String,
        cpuCores: UInt32,
        memoryBytes: UInt64,
        tartVersion: String,
    ) async throws -> RegistrationResult {
        logger.info("Registering with server", metadata: ["server": "\(config.serverAddress)"])

        let body: [String: Any] = [
            "hostname": hostname,
            "hardware_uuid": hardwareUUID,
            "cpu_cores": cpuCores,
            "memory_bytes": memoryBytes,
            "tart_version": tartVersion,
            "worker_version": workerVersion,
            "worker_token": authToken,
        ]

        let (data, response) = try await postJSON(path: "/api/v1/workers/register", body: body)
        guard response.statusCode == 200 else {
            let responseBody = String(data: data, encoding: .utf8) ?? ""
            logger.error(
                "Worker registration failed",
                metadata: [
                    "status": "\(response.statusCode)",
                    "body": "\(responseBody)",
                ],
            )
            throw APIClientError.httpStatus(response.statusCode, responseBody)
        }
        let result = try JSONDecoder().decode(RegistrationResult.self, from: data)

        workerID = result.workerID
        if let issuedWorkerToken = result.workerToken, !issuedWorkerToken.isEmpty {
            authToken = issuedWorkerToken
        }

        logger.info(
            "Registered",
            metadata: [
                "worker_id": "\(workerID)",
                "pool_size": "\(result.poolSize)",
                "base_image": "\(result.baseImage)",
            ],
        )

        return result
    }

    /// Send a heartbeat to the server.
    public func sendHeartbeat(workstationStatuses: [WorkstationHeartbeatStatus]) async
        -> HeartbeatResponse
    {
        let body = HeartbeatPayload(workstationStatuses: workstationStatuses)

        do {
            let (data, response) = try await postCodable(
                path: "/api/v1/workers/\(workerID)/heartbeat", body: body,
            )
            if response.statusCode != 200 {
                logger.warning(
                    "Heartbeat failed",
                    metadata: [
                        "worker_id": "\(workerID)",
                        "status": "\(response.statusCode)",
                        "workstation_count": "\(workstationStatuses.count)",
                    ],
                )
                return HeartbeatResponse(status: "error", desiredWorkstations: [])
            }
            logger.debug(
                "Heartbeat succeeded",
                metadata: [
                    "worker_id": "\(workerID)",
                    "workstation_count": "\(workstationStatuses.count)",
                ],
            )
            return try JSONDecoder().decode(HeartbeatResponse.self, from: data)
        } catch {
            logger.warning(
                "Heartbeat error",
                metadata: [
                    "worker_id": "\(workerID)",
                    "workstation_count": "\(workstationStatuses.count)",
                    "error": "\(error)",
                ],
            )
            return HeartbeatResponse(status: "error", desiredWorkstations: [])
        }
    }

    /// Report VM state change to the server.
    public func reportVMState(vmID: String, state: String, ipAddress: String?, errorMessage: String?)
        async
    {
        var body: [String: Any] = [
            "worker_id": workerID,
            "state": state,
        ]
        if let ip = ipAddress { body["ip_address"] = ip }
        if let err = errorMessage { body["error_message"] = err }

        do {
            try await request(method: "PUT", path: "/api/v1/vms/\(vmID)/state", jsonObject: body)
        } catch {
            logger.warning("Report VM state failed", metadata: ["vm_id": "\(vmID)", "error": "\(error)"])
        }
    }

    /// Poll for pending proxy sessions.
    public func pollForProxySessions() async -> [PendingProxySession] {
        do {
            let (data, response) = try await getJSON(
                path: "/api/v1/proxy/sessions/pending?worker_id=\(workerID)",
            )
            if response.statusCode == 200 {
                return try JSONDecoder().decode([PendingProxySession].self, from: data)
            }
        } catch {
            // No sessions or error
        }
        return []
    }

    /// Get the HTTP base URL for constructing WebSocket URLs.
    public func getBaseURL() -> String {
        baseURL?.absoluteString ?? ""
    }

    // MARK: - HTTP Helpers

    @discardableResult
    private func postJSON(path: String, body: [String: Any]) async throws -> (Data, HTTPURLResponse) {
        try await request(method: "POST", path: path, jsonObject: body)
    }

    @discardableResult
    private func postCodable(path: String, body: some Encodable) async throws -> (
        Data, HTTPURLResponse,
    ) {
        let url = try requestURL(path: path)
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("Bearer \(authToken)", forHTTPHeaderField: "Authorization")
        req.httpBody = try JSONEncoder().encode(body)
        return try await perform(req)
    }

    private func getJSON(path: String) async throws -> (Data, HTTPURLResponse) {
        let url = try requestURL(path: path)
        var req = URLRequest(url: url)
        req.setValue("Bearer \(authToken)", forHTTPHeaderField: "Authorization")
        return try await perform(req)
    }

    @discardableResult
    private func request(method: String, path: String, jsonObject: [String: Any]) async throws -> (
        Data, HTTPURLResponse,
    ) {
        let url = try requestURL(path: path)
        var req = URLRequest(url: url)
        req.httpMethod = method
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("Bearer \(authToken)", forHTTPHeaderField: "Authorization")
        req.httpBody = try JSONSerialization.data(withJSONObject: jsonObject)
        return try await perform(req)
    }

    private func perform(_ request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        let (data, response) = try await urlSession.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            throw APIClientError.invalidResponse
        }
        return (data, httpResponse)
    }

    private func requestURL(path: String) throws -> URL {
        guard let baseURL else {
            throw WorkerConfigNetworkError.invalidServerAddress(config.serverAddress)
        }
        guard let url = URL(string: path, relativeTo: baseURL) else {
            throw WorkerConfigNetworkError.invalidServerAddress(config.serverAddress)
        }
        return url.absoluteURL
    }
}

// MARK: - Data Types

public struct RegistrationResult: Codable, Sendable {
    public let workerID: String
    public let poolSize: Int
    public let baseImage: String
    public let workerToken: String?

    enum CodingKeys: String, CodingKey {
        case workerID = "worker_id"
        case poolSize = "pool_size"
        case baseImage = "base_image"
        case workerToken = "worker_token"
    }
}

public struct WorkstationHeartbeatStatus: Codable, Sendable {
    public let workstationID: String
    public let powerState: String
    public let ipAddress: String?
    public let lastError: String?

    public init(workstationID: String, powerState: String, ipAddress: String?, lastError: String?) {
        self.workstationID = workstationID
        self.powerState = powerState
        self.ipAddress = ipAddress
        self.lastError = lastError
    }

    enum CodingKeys: String, CodingKey {
        case workstationID = "workstation_id"
        case powerState = "power_state"
        case ipAddress = "ip_address"
        case lastError = "last_error"
    }
}

public struct HeartbeatPayload: Codable, Sendable {
    public let workstationStatuses: [WorkstationHeartbeatStatus]

    enum CodingKeys: String, CodingKey {
        case workstationStatuses = "workstation_statuses"
    }
}

public struct PendingProxySession: Codable, Sendable {
    public let sessionID: String
    public let target: String // "vm" or "host"
    public let vmID: String? // only for target=vm
    public let port: Int
    public let token: String

    enum CodingKeys: String, CodingKey {
        case sessionID = "session_id"
        case target
        case vmID = "vm_id"
        case port
        case token
    }
}

public struct HeartbeatResponse: Codable, Sendable {
    public let status: String
    public let desiredWorkstations: [DesiredWorkstation]

    public init(status: String, desiredWorkstations: [DesiredWorkstation]) {
        self.status = status
        self.desiredWorkstations = desiredWorkstations
    }

    public var isAuthoritative: Bool {
        status == "ok"
    }

    enum CodingKeys: String, CodingKey {
        case status
        case desiredWorkstations = "desired_workstations"
    }
}

public enum APIClientError: Error, CustomStringConvertible, LocalizedError {
    case invalidURL
    case invalidResponse
    case registrationFailed
    case httpStatus(Int, String)

    public var description: String {
        switch self {
        case .invalidURL:
            return "invalid server URL"
        case .invalidResponse:
            return "server returned a non-HTTP response"
        case .registrationFailed:
            return "worker registration failed"
        case let .httpStatus(status, body):
            let trimmedBody = body.trimmingCharacters(in: .whitespacesAndNewlines)
            if trimmedBody.isEmpty {
                return "server returned HTTP \(status)"
            }
            return "server returned HTTP \(status): \(trimmedBody)"
        }
    }

    public var errorDescription: String? {
        description
    }
}
