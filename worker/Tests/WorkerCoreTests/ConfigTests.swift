import Foundation
import Testing
@testable import WorkerCore

struct ConfigTests {
    @Test func `default values`() {
        let config = WorkerConfig()
        #expect(config.serverAddress == "localhost:9090")
        #expect(config.workerToken == "")
        #expect(config.enrollmentToken == "")
        #expect(config.baseImage == "minictl-tahoe-base")
        #expect(config.poolSize == 2)
        #expect(config.sshUsername == "admin")
        #expect(config.sshPassword == "admin")
        #expect(config.httpPort == 8080)
        #expect(config.logLevel == "info")
        #expect(config.useHTTPS == true)
        #expect(config.tlsCertFingerprint == "")
        #expect(config.hostKeychainPassword == "")
    }

    @Test func `custom values`() {
        let config = WorkerConfig(
            serverAddress: "server.example.com:9091",
            workerToken: "my-token",
            enrollmentToken: "enroll-token",
            baseImage: "custom-image",
            poolSize: 4,
            sshUsername: "user",
            sshPassword: "pass",
            httpPort: 443,
            logLevel: "debug",
            useHTTPS: true,
            tlsCertFingerprint: "abc123",
            hostKeychainPassword: "keychain-pass",
        )
        #expect(config.serverAddress == "server.example.com:9091")
        #expect(config.workerToken == "my-token")
        #expect(config.enrollmentToken == "enroll-token")
        #expect(config.baseImage == "custom-image")
        #expect(config.poolSize == 4)
        #expect(config.sshUsername == "user")
        #expect(config.sshPassword == "pass")
        #expect(config.httpPort == 443)
        #expect(config.logLevel == "debug")
        #expect(config.useHTTPS == true)
        #expect(config.tlsCertFingerprint == "abc123")
        #expect(config.hostKeychainPassword == "keychain-pass")
    }

    @Test func `host keychain password default`() {
        let config = WorkerConfig()
        #expect(config.hostKeychainPassword.isEmpty)
    }

    @Test func `load from YAML`() throws {
        let yaml = """
        server_address: "10.0.0.1:9090"
        enrollment_token: "tok123"
        worker_token: "wkt_issued123"
        base_image: "my-image"
        pool_size: 3
        vm_cpu: 6
        vm_memory: 4096
        ssh_username: "testuser"
        ssh_password: "testpass"
        http_port: 8443
        log_level: "debug"
        use_https: true
        tls_cert_fingerprint: "deadbeef"
        host_keychain_password: "kc-secret"
        """

        let tmpDir = FileManager.default.temporaryDirectory
        let tmpFile = tmpDir.appendingPathComponent("test-config-\(UUID().uuidString).yaml")
        try yaml.write(to: tmpFile, atomically: true, encoding: .utf8)
        defer { try? FileManager.default.removeItem(at: tmpFile) }

        let config = try WorkerConfig.load(from: tmpFile.path)
        #expect(config.serverAddress == "10.0.0.1:9090")
        #expect(config.workerToken == "wkt_issued123")
        #expect(config.enrollmentToken == "tok123")
        #expect(config.baseImage == "my-image")
        #expect(config.poolSize == 3)
        #expect(config.sshUsername == "testuser")
        #expect(config.sshPassword == "testpass")
        #expect(config.httpPort == 8443)
        #expect(config.logLevel == "debug")
        #expect(config.useHTTPS == true)
        #expect(config.tlsCertFingerprint == "deadbeef")
        #expect(config.hostKeychainPassword == "kc-secret")
    }

    @Test func `load from YAML missing fields uses defaults`() throws {
        let yaml = """
        server_address: "myserver:9090"
        """

        let tmpDir = FileManager.default.temporaryDirectory
        let tmpFile = tmpDir.appendingPathComponent("test-config-\(UUID().uuidString).yaml")
        try yaml.write(to: tmpFile, atomically: true, encoding: .utf8)
        defer { try? FileManager.default.removeItem(at: tmpFile) }

        let config = try WorkerConfig.load(from: tmpFile.path)
        #expect(config.serverAddress == "myserver:9090")
        #expect(config.workerToken == "")
        #expect(config.enrollmentToken == "")
        #expect(config.poolSize == 2)
        #expect(config.hostKeychainPassword == "")
        #expect(config.useHTTPS == true)
        #expect(config.tlsCertFingerprint == "")
    }

    @Test func `load from legacy YAML treats old worker token as enrollment token`() throws {
        let yaml = """
        server_address: "legacy.example.com:9090"
        worker_token: "legacy-shared-token"
        """

        let tmpDir = FileManager.default.temporaryDirectory
        let tmpFile = tmpDir.appendingPathComponent("test-config-\(UUID().uuidString).yaml")
        try yaml.write(to: tmpFile, atomically: true, encoding: .utf8)
        defer { try? FileManager.default.removeItem(at: tmpFile) }

        let config = try WorkerConfig.load(from: tmpFile.path)
        #expect(config.workerToken == "")
        #expect(config.enrollmentToken == "legacy-shared-token")
    }

    @Test func `load from YAML invalid format`() throws {
        let yaml = "not: [valid: yaml: format"

        let tmpDir = FileManager.default.temporaryDirectory
        let tmpFile = tmpDir.appendingPathComponent("test-config-\(UUID().uuidString).yaml")
        try yaml.write(to: tmpFile, atomically: true, encoding: .utf8)
        defer { try? FileManager.default.removeItem(at: tmpFile) }

        // This should still parse as YAML (it's a mapping), but test truly invalid
        let badYaml = "just a string"
        let tmpFile2 = tmpDir.appendingPathComponent("test-config-\(UUID().uuidString).yaml")
        try badYaml.write(to: tmpFile2, atomically: true, encoding: .utf8)
        defer { try? FileManager.default.removeItem(at: tmpFile2) }

        #expect(throws: (any Error).self) {
            _ = try WorkerConfig.load(from: tmpFile2.path)
        }
    }

    @Test func `load from YAML file not found`() {
        #expect(throws: (any Error).self) {
            _ = try WorkerConfig.load(from: "/nonexistent/path/config.yaml")
        }
    }
}
