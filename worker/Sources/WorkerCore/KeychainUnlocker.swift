import Foundation
import Logging

/// Unlocks the macOS login keychain for Tart VM support on macOS 15+ (Sequoia).
/// Virtualization.Framework requires an unlocked login keychain to run VMs.
public struct KeychainUnlocker: Sendable {
    private let logger: Logger

    public init(logger: Logger) {
        self.logger = logger
    }

    /// Unlock the login keychain using `security unlock-keychain`.
    /// Resolves the user's actual login keychain path first because newer macOS
    /// releases often use `login.keychain-db` instead of `login.keychain`.
    /// Idempotent: re-unlocking an already-unlocked keychain is a no-op.
    public func ensureKeychainUnlocked(password: String) async throws {
        let candidates = try await keychainCandidates()
        var failures: [String] = []
        var lastStatus: Int32 = 1

        for keychainPath in candidates {
            let result = try await ProcessRunner.run(
                executableURL: URL(fileURLWithPath: "/usr/bin/security"),
                arguments: ["unlock-keychain", "-p", password, keychainPath],
            )

            if result.terminationStatus == 0 {
                logger.info("Keychain unlocked", metadata: ["path": "\(keychainPath)"])
                await setDefaultKeychainIfPossible(path: keychainPath)
                return
            }

            lastStatus = result.terminationStatus
            let output =
                String(data: result.stdout + result.stderr, encoding: .utf8)?
                    .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            let detail = output.isEmpty ? "no output" : output
            failures.append("\(keychainPath) [exit \(result.terminationStatus)]: \(detail)")
        }

        throw KeychainError.unlockFailed(
            status: lastStatus,
            output: failures.joined(separator: " | "),
        )
    }

    private func setDefaultKeychainIfPossible(path: String) async {
        let result = try? await ProcessRunner.run(
            executableURL: URL(fileURLWithPath: "/usr/bin/security"),
            arguments: ["default-keychain", "-d", "user", "-s", path],
        )
        guard let result, result.terminationStatus != 0 else { return }

        let output =
            String(data: result.stdout + result.stderr, encoding: .utf8)?
                .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        logger.warning(
            "Failed to set default keychain",
            metadata: [
                "path": "\(path)",
                "status": "\(result.terminationStatus)",
                "output": "\(output)",
            ],
        )
    }

    private func keychainCandidates() async throws -> [String] {
        let homeDirectory = FileManager.default.homeDirectoryForCurrentUser.path
        var outputs: [String] = []

        for arguments in [["login-keychain", "-d", "user"], ["default-keychain", "-d", "user"]] {
            let result = try await ProcessRunner.run(
                executableURL: URL(fileURLWithPath: "/usr/bin/security"),
                arguments: arguments,
            )
            if let output = String(data: result.stdout + result.stderr, encoding: .utf8),
               !output.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            {
                outputs.append(output)
            }
        }

        return Self.defaultKeychainCandidates(homeDirectory: homeDirectory, commandOutputs: outputs)
    }

    static func defaultKeychainCandidates(homeDirectory: String, commandOutputs: [String]) -> [String] {
        var candidates: [String] = []

        for output in commandOutputs {
            if let parsed = parseKeychainPath(output) {
                candidates.append(parsed)
            }
        }

        candidates.append("\(homeDirectory)/Library/Keychains/login.keychain-db")
        candidates.append("\(homeDirectory)/Library/Keychains/login.keychain")
        candidates.append("login.keychain-db")
        candidates.append("login.keychain")

        var seen = Set<String>()
        return candidates.filter { candidate in
            guard !candidate.isEmpty, !seen.contains(candidate) else { return false }
            seen.insert(candidate)
            return true
        }
    }

    static func parseKeychainPath(_ output: String) -> String? {
        let trimmed = output.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }

        if let firstQuote = trimmed.firstIndex(of: "\""),
           let lastQuote = trimmed.lastIndex(of: "\""),
           firstQuote < lastQuote
        {
            let start = trimmed.index(after: firstQuote)
            return String(trimmed[start ..< lastQuote])
        }

        return trimmed
    }
}

public enum KeychainError: Error, CustomStringConvertible {
    case unlockFailed(status: Int32, output: String)

    public var description: String {
        switch self {
        case let .unlockFailed(status, output):
            "security unlock-keychain failed (exit \(status)): \(output)"
        }
    }
}
