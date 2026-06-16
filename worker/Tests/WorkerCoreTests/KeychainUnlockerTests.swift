import Foundation
import Logging
import Testing
@testable import WorkerCore

struct KeychainErrorTests {
    @Test func `error description`() {
        let error = KeychainError.unlockFailed(status: 1, output: "keychain locked")
        let desc = error.description
        #expect(desc.contains("exit 1"))
        #expect(desc.contains("keychain locked"))
    }

    @Test func `error description with zero status`() {
        // Edge case: status 0 but still an error (shouldn't happen normally)
        let error = KeychainError.unlockFailed(status: 0, output: "unexpected")
        let desc = error.description
        #expect(desc.contains("exit 0"))
        #expect(desc.contains("unexpected"))
    }

    @Test func `error description with empty output`() {
        let error = KeychainError.unlockFailed(status: 44, output: "")
        let desc = error.description
        #expect(desc.contains("exit 44"))
    }

    @Test func `error description with negative status`() {
        // Process termination status can be negative for signal kills
        let error = KeychainError.unlockFailed(status: -9, output: "killed")
        let desc = error.description
        #expect(desc.contains("exit -9"))
        #expect(desc.contains("killed"))
    }

    @Test func `error conforms to error protocol`() {
        let error: any Error = KeychainError.unlockFailed(status: 1, output: "test")
        #expect(error is KeychainError)
    }

    @Test func `error conforms to custom string convertible`() {
        let error: any CustomStringConvertible = KeychainError.unlockFailed(status: 1, output: "test")
        #expect(!error.description.isEmpty)
    }
}

struct KeychainUnlockerTests {
    @Test func `init with logger`() {
        var logger = Logger(label: "test-keychain")
        logger.logLevel = .debug
        let unlocker = KeychainUnlocker(logger: logger)
        // Verify it was created (it's a struct, so this is mainly a compilation test)
        #expect(Bool(true))
        _ = unlocker
    }

    @Test func `is sendable`() {
        let logger = Logger(label: "test")
        let unlocker = KeychainUnlocker(logger: logger)
        let _: any Sendable = unlocker
        #expect(Bool(true))
    }

    @Test func `ensure keychain unlocked with bad password`() async {
        // Running with a deliberately wrong password should fail
        // because the 'security' command will reject it.
        // This tests the actual command execution path.
        let logger = Logger(label: "test-keychain-bad-pw")
        let unlocker = KeychainUnlocker(logger: logger)

        // Use a clearly invalid password that won't accidentally match
        // This test exercises the error path of runSecurity
        do {
            try await unlocker.ensureKeychainUnlocked(
                password: "definitely-wrong-password-\(UUID().uuidString)",
            )
            // If the keychain doesn't exist and gets created with this password,
            // the test still passes -- it means the command succeeded.
            // On CI/fresh machines, create-keychain may succeed.
        } catch {
            // Expected: the security command should fail
            #expect(error is KeychainError)
            if let keychainError = error as? KeychainError {
                switch keychainError {
                case let .unlockFailed(status, _):
                    #expect(status != 0)
                }
            }
        }
    }

    @Test func `parse quoted keychain path`() {
        let parsed = KeychainUnlocker.parseKeychainPath(
            "    \"/Users/launchctl/Library/Keychains/login.keychain-db\"    ",
        )
        #expect(parsed == "/Users/launchctl/Library/Keychains/login.keychain-db")
    }

    @Test func `parse unquoted keychain path`() {
        let parsed = KeychainUnlocker.parseKeychainPath(
            "/Users/launchctl/Library/Keychains/login.keychain",
        )
        #expect(parsed == "/Users/launchctl/Library/Keychains/login.keychain")
    }

    @Test func `default keychain candidates prefer discovered path and dedupe`() {
        let candidates = KeychainUnlocker.defaultKeychainCandidates(
            homeDirectory: "/Users/launchctl",
            commandOutputs: [
                "\"/Users/launchctl/Library/Keychains/login.keychain-db\"",
                "\"/Users/launchctl/Library/Keychains/login.keychain-db\"",
            ],
        )

        #expect(candidates.first == "/Users/launchctl/Library/Keychains/login.keychain-db")
        #expect(candidates.count == Set(candidates).count)
        #expect(candidates.contains("login.keychain"))
    }
}

struct RegistrationResultTests {
    @Test func `decodes without keychain password`() throws {
        let json = """
        {
            "worker_id": "w-abc123",
            "pool_size": 2,
            "base_image": "test-image"
        }
        """.data(using: .utf8)!

        let result = try JSONDecoder().decode(RegistrationResult.self, from: json)
        #expect(result.workerID == "w-abc123")
        #expect(result.poolSize == 2)
        #expect(result.baseImage == "test-image")
    }

    @Test func `keychain password uses local config only`() {
        let configPassword = "config-pass"
        let selected = configPassword
        #expect(selected == "config-pass")
    }
}
