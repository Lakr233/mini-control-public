import Foundation
import Logging

private let bootstrapRetryInterval: Duration = .seconds(5)

/// Keeps the GUI overlay LaunchAgent loaded for the logged-in user session,
/// retrying until the GUI session becomes available.
public struct OverlaySessionBootstrapper {
    private let overlayUser: String
    private let overlayPlistPath: String
    private let logger: Logger
    private var lastGUIUnavailableReason: String?

    public init(overlayUser: String, overlayPlistPath: String, logger: Logger) {
        self.overlayUser = overlayUser
        self.overlayPlistPath = overlayPlistPath
        self.logger = logger
        lastGUIUnavailableReason = nil
    }

    public mutating func run() async throws {
        let uid = try await resolveUID(user: overlayUser)
        logger.info(
            "Overlay bootstrapper starting", metadata: ["user": "\(overlayUser)", "uid": "\(uid)"],
        )
        while !Task.isCancelled {
            do {
                if let unavailableReason = try await guiUnavailableReason(uid: uid) {
                    if unavailableReason != lastGUIUnavailableReason {
                        logger.info(
                            "GUI session unavailable for overlay",
                            metadata: [
                                "user": "\(overlayUser)",
                                "uid": "\(uid)",
                                "reason": "\(unavailableReason)",
                            ],
                        )
                        lastGUIUnavailableReason = unavailableReason
                    }
                } else {
                    lastGUIUnavailableReason = nil
                    try await ensureOverlayLoaded(uid: uid)
                }
            } catch {
                logger.warning("Overlay bootstrap iteration failed", metadata: ["error": "\(error)"])
            }
            try? await Task.sleep(for: bootstrapRetryInterval)
        }
    }

    private func resolveUID(user: String) async throws -> Int {
        let result = try await ProcessRunner.run(
            executableURL: URL(fileURLWithPath: "/usr/bin/id"),
            arguments: ["-u", user],
        )
        guard result.terminationStatus == 0,
              let text = String(data: result.stdout, encoding: .utf8)?.trimmingCharacters(
                  in: .whitespacesAndNewlines,
              ),
              let uid = Int(text)
        else {
            throw OverlayBootstrapError.userLookupFailed(user)
        }
        return uid
    }

    private func guiUnavailableReason(uid: Int) async throws -> String? {
        let consoleResult = try await ProcessRunner.run(
            executableURL: URL(fileURLWithPath: "/usr/bin/stat"),
            arguments: ["-f", "%Su", "/dev/console"],
        )
        let consoleUser =
            String(data: consoleResult.stdout, encoding: .utf8)?
                .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        if consoleUser != overlayUser {
            return "console user is '\(consoleUser.isEmpty ? "unknown" : consoleUser)'"
        }

        let result = try await ProcessRunner.run(
            executableURL: URL(fileURLWithPath: "/bin/launchctl"),
            arguments: ["print", "gui/\(uid)"],
        )
        if result.terminationStatus != 0 {
            let stderr =
                String(data: result.stderr, encoding: .utf8)?
                    .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            return stderr.isEmpty ? "gui/\(uid) domain unavailable" : stderr
        }
        return nil
    }

    private func ensureOverlayLoaded(uid: Int) async throws {
        let label = "gui/\(uid)/com.minicontrol.overlay"
        let printResult = try await ProcessRunner.run(
            executableURL: URL(fileURLWithPath: "/bin/launchctl"),
            arguments: ["print", label],
        )
        if printResult.terminationStatus != 0 {
            logger.info("Bootstrapping GUI overlay", metadata: ["uid": "\(uid)"])
            let bootstrapResult = try await ProcessRunner.run(
                executableURL: URL(fileURLWithPath: "/bin/launchctl"),
                arguments: ["bootstrap", "gui/\(uid)", overlayPlistPath],
            )
            if bootstrapResult.terminationStatus != 0 {
                logger.warning(
                    "GUI overlay bootstrap failed",
                    metadata: [
                        "uid": "\(uid)",
                        "status": "\(bootstrapResult.terminationStatus)",
                        "stderr": "\(String(data: bootstrapResult.stderr, encoding: .utf8) ?? "")",
                    ],
                )
            }
        }
    }
}

public enum OverlayBootstrapError: Error, CustomStringConvertible {
    case userLookupFailed(String)

    public var description: String {
        switch self {
        case let .userLookupFailed(user):
            "failed to resolve overlay user: \(user)"
        }
    }
}
