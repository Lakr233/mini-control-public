import Foundation
import Yams

public struct WorkerConfig: Sendable {
    public var serverAddress: String
    public var workerToken: String
    public var enrollmentToken: String
    public var baseImage: String
    public var poolSize: Int
    public var sshUsername: String
    public var sshPassword: String
    public var httpPort: Int
    public var logLevel: String
    public var useHTTPS: Bool
    public var tlsCertFingerprint: String
    public var hostKeychainPassword: String

    public init(
        serverAddress: String = "localhost:9090",
        workerToken: String = "",
        enrollmentToken: String = "",
        baseImage: String = "minictl-tahoe-base",
        poolSize: Int = 2,
        sshUsername: String = "admin",
        sshPassword: String = "admin",
        httpPort: Int = 8080,
        logLevel: String = "info",
        useHTTPS: Bool = true,
        tlsCertFingerprint: String = "",
        hostKeychainPassword: String = "",
    ) {
        self.serverAddress = serverAddress
        self.workerToken = workerToken
        self.enrollmentToken = enrollmentToken
        self.baseImage = baseImage
        self.poolSize = poolSize
        self.sshUsername = sshUsername
        self.sshPassword = sshPassword
        self.httpPort = httpPort
        self.logLevel = logLevel
        self.useHTTPS = useHTTPS
        self.tlsCertFingerprint = tlsCertFingerprint
        self.hostKeychainPassword = hostKeychainPassword
    }

    public static func load(from path: String) throws -> WorkerConfig {
        let data = try String(contentsOfFile: path, encoding: .utf8)
        guard let dict = try Yams.load(yaml: data) as? [String: Any] else {
            throw ConfigError.invalidFormat
        }

        let rawWorkerToken = dict["worker_token"] as? String ?? ""
        let enrollmentToken =
            dict["enrollment_token"] as? String
                ?? (rawWorkerToken.hasPrefix("wkt_") ? "" : rawWorkerToken)
        let workerToken = rawWorkerToken.hasPrefix("wkt_") ? rawWorkerToken : ""

        return WorkerConfig(
            serverAddress: dict["server_address"] as? String ?? "localhost:9090",
            workerToken: workerToken,
            enrollmentToken: enrollmentToken,
            baseImage: dict["base_image"] as? String ?? "minictl-tahoe-base",
            poolSize: dict["pool_size"] as? Int ?? 2,
            sshUsername: dict["ssh_username"] as? String ?? "admin",
            sshPassword: dict["ssh_password"] as? String ?? "admin",
            httpPort: dict["http_port"] as? Int ?? 8080,
            logLevel: dict["log_level"] as? String ?? "info",
            useHTTPS: dict["use_https"] as? Bool ?? true,
            tlsCertFingerprint: dict["tls_cert_fingerprint"] as? String ?? "",
            hostKeychainPassword: dict["host_keychain_password"] as? String ?? "",
        )
    }

    public func preferredAuthToken() -> String {
        if !workerToken.isEmpty {
            return workerToken
        }
        return enrollmentToken
    }

    public func save(to path: String) throws {
        let yaml = """
        server_address: \(Self.yamlQuote(serverAddress))
        worker_token: \(Self.yamlQuote(workerToken))
        enrollment_token: \(Self.yamlQuote(enrollmentToken))
        base_image: \(Self.yamlQuote(baseImage))
        pool_size: \(poolSize)
        ssh_username: \(Self.yamlQuote(sshUsername))
        ssh_password: \(Self.yamlQuote(sshPassword))
        http_port: \(httpPort)
        log_level: \(Self.yamlQuote(logLevel))
        use_https: \(useHTTPS ? "true" : "false")
        tls_cert_fingerprint: \(Self.yamlQuote(tlsCertFingerprint))
        host_keychain_password: \(Self.yamlQuote(hostKeychainPassword))
        """
        try yaml.write(toFile: path, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: path)
    }

    private static func yamlQuote(_ value: String) -> String {
        "'\(value.replacingOccurrences(of: "'", with: "''"))'"
    }

    enum ConfigError: Error {
        case invalidFormat
    }
}
