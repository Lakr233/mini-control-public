import AppKit
import ArgumentParser
import Foundation
import Logging
import WorkerCore

@main
struct MiniControlWorkerCLI: AsyncParsableCommand {
    static let currentVersion = "0.1.27"

    static let configuration = CommandConfiguration(
        commandName: "minicontrol-worker",
        abstract: "Mini-control worker agent for managing Tart VMs",
        version: currentVersion,
    )

    @Option(name: .long, help: "Path to config file")
    var config: String = "/etc/minicontrol/config.yaml"

    @Option(name: .long, help: "Server address (overrides config)")
    var server: String?

    @Option(name: .long, help: "Worker token (overrides config)")
    var token: String?

    @Option(name: .long, help: "Log level: debug, info, warning, error")
    var logLevel: String?

    @Option(name: .long, help: "Unix socket path used for overlay IPC")
    var overlaySocket: String?

    @Option(name: .long, help: "State file path used for overlay IPC")
    var overlayStateFile: String?

    @Flag(name: .long, help: "Run the GUI overlay client instead of the worker backend")
    var overlayClient: Bool = false

    @Flag(name: .long, help: "Run the overlay GUI-session bootstrap supervisor")
    var overlayBootstrap: Bool = false

    @Option(name: .long, help: "Username whose GUI session should host the overlay")
    var overlayUser: String?

    @Option(
        name: .long, help: "Path to the overlay plist that should be bootstrapped into the GUI session",
    )
    var overlayPlist: String?

    @Flag(name: .long, help: "Run preflight checks and exit (used by auto-updater)")
    var preflightCheck: Bool = false

    func run() async throws {
        // Load config
        var workerConfig: WorkerConfig
        do {
            workerConfig = try WorkerConfig.load(from: config)
        } catch {
            print("CONFIG FAIL: unable to load \(config): \(error)")
            throw ExitCode.failure
        }

        // Apply CLI overrides
        if let server {
            workerConfig.serverAddress = server
        }
        if let token {
            if token.hasPrefix("wkt_") {
                workerConfig.workerToken = token
                workerConfig.enrollmentToken = ""
            } else {
                workerConfig.enrollmentToken = token
                workerConfig.workerToken = ""
            }
        }
        if let level = logLevel {
            workerConfig.logLevel = level
        }

        // Preflight check mode: validate and exit
        if preflightCheck {
            try await runPreflightChecks(config: workerConfig)
            return
        }

        // Setup logging
        let level: Logger.Level =
            switch workerConfig.logLevel {
            case "debug": .debug
            case "warning": .warning
            case "error": .error
            default: .info
            }

        LoggingSystem.bootstrap { label in
            var handler = StreamLogHandler.standardOutput(label: label)
            handler.logLevel = level
            return handler
        }

        var configuredLogger = Logger(label: "minicontrol-worker")
        configuredLogger.logLevel = level
        let logger = configuredLogger

        logger.info("Starting minicontrol-worker v\(Self.currentVersion)")
        if overlayClient {
            try await runOverlayClient(logger: logger)
            return
        }
        if overlayBootstrap {
            try await runOverlayBootstrap(logger: logger)
            return
        }

        logger.info(
            "Config",
            metadata: [
                "server": "\(workerConfig.serverAddress)",
                "base_image": "\(workerConfig.baseImage)",
                "pool_size": "\(workerConfig.poolSize)",
            ],
        )

        let overlayStateStore = OverlayStateStore(
            version: Self.currentVersion,
            persistPath: overlayStateFile,
        )
        var overlayServer: OverlayHTTPServer?
        if let overlaySocket, !overlaySocket.isEmpty {
            let server = OverlayHTTPServer(
                socketPath: overlaySocket, stateStore: overlayStateStore, logger: logger,
            )
            do {
                try server.start()
                overlayServer = server
            } catch {
                logger.warning(
                    "Failed to start overlay HTTP server",
                    metadata: [
                        "socket": "\(overlaySocket)",
                        "error": "\(error)",
                    ],
                )
            }
        }
        let overlayServerRef = overlayServer

        // Create and run worker
        let worker = Worker(
            config: workerConfig,
            configPath: config,
            currentVersion: Self.currentVersion,
            logger: logger,
            overlayStateStore: overlayStateStore,
        )

        // Handle signals for graceful shutdown
        let shutdownTask = makeSignalTask(logger: logger) {
            logger.info("Received shutdown signal")
            await worker.shutdown()
            overlayServerRef?.stop()
        }

        do {
            try await worker.run()
        } catch {
            logger.error("Worker error: \(error)")
            await worker.displayFatalErrorBeforeExit(error)
            await worker.shutdown()
            overlayServerRef?.stop()
            throw error
        }

        shutdownTask.cancel()
        overlayServerRef?.stop()
    }

    // MARK: - Preflight Checks

    private func runOverlayClient(logger: Logger) async throws {
        let source: OverlayClientSource
        if let overlayStateFile, !overlayStateFile.isEmpty {
            source = .stateFile(overlayStateFile)
        } else if let overlaySocket, !overlaySocket.isEmpty {
            source = .socket(overlaySocket)
        } else {
            throw ValidationError(
                "either --overlay-state-file or --overlay-socket is required with --overlay-client",
            )
        }

        _ = await MainActor.run { NSApplication.shared.setActivationPolicy(.accessory) }
        let coordinator = await MainActor.run {
            OverlayClientCoordinator(source: source, logger: logger)
        }

        let shutdownTask = makeSignalTask(logger: logger) {
            logger.info("Stopping overlay client")
            await MainActor.run {
                coordinator.stop()
            }
        }

        await MainActor.run {
            NSApp.activate(ignoringOtherApps: false)
        }
        await coordinator.run()
        shutdownTask.cancel()
    }

    private func runOverlayBootstrap(logger: Logger) async throws {
        guard let overlayUser, !overlayUser.isEmpty else {
            throw ValidationError("--overlay-user is required with --overlay-bootstrap")
        }
        guard let overlayPlist, !overlayPlist.isEmpty else {
            throw ValidationError("--overlay-plist is required with --overlay-bootstrap")
        }

        var bootstrapper = OverlaySessionBootstrapper(
            overlayUser: overlayUser,
            overlayPlistPath: overlayPlist,
            logger: logger,
        )

        let bootstrapTask = Task {
            try await bootstrapper.run()
        }
        let shutdownTask = makeSignalTask(logger: logger) {
            logger.info("Stopping overlay bootstrapper")
            bootstrapTask.cancel()
        }

        do {
            try await bootstrapTask.value
        } catch is CancellationError {}
        shutdownTask.cancel()
    }

    private func makeSignalTask(logger: Logger, onSignal: @escaping @Sendable () async -> Void)
        -> Task<Void, Never>
    {
        let sigintSource = DispatchSource.makeSignalSource(signal: SIGINT)
        let sigtermSource = DispatchSource.makeSignalSource(signal: SIGTERM)
        signal(SIGINT, SIG_IGN)
        signal(SIGTERM, SIG_IGN)

        return Task {
            final class ResumeState: @unchecked Sendable {
                let lock = NSLock()
                var resumed = false
            }
            let state = ResumeState()

            await withCheckedContinuation { continuation in
                let resumeOnce = {
                    state.lock.lock()
                    defer { state.lock.unlock() }
                    guard !state.resumed else { return }
                    state.resumed = true
                    continuation.resume()
                }

                sigintSource.setEventHandler(handler: resumeOnce)
                sigtermSource.setEventHandler(handler: resumeOnce)
                sigintSource.resume()
                sigtermSource.resume()
            }

            logger.info("Signal handler activated")
            await onSignal()
        }
    }

    private func runPreflightChecks(config: WorkerConfig) async throws {
        print("PREFLIGHT: Starting checks...")

        // 1. Config loaded successfully (already done above)
        print("PREFLIGHT: Config loaded OK")

        // 2. Server connectivity
        let healthURL: URL
        do {
            healthURL = try config.healthCheckURL()
        } catch {
            print("PREFLIGHT FAIL: Invalid server address: \(error)")
            throw ExitCode.failure
        }

        var request = URLRequest(url: healthURL)
        request.timeoutInterval = 10
        let session = URLSession(
            configuration: .default,
            delegate: CertPinningDelegate(fingerprint: config.tlsCertFingerprint),
            delegateQueue: nil,
        )

        do {
            let (_, response) = try await session.data(for: request)
            if let http = response as? HTTPURLResponse, http.statusCode == 200 {
                print("PREFLIGHT: Server reachable OK")
            } else {
                print("PREFLIGHT FAIL: Server returned non-200")
                throw ExitCode.failure
            }
        } catch let error where !(error is ExitCode) {
            print("PREFLIGHT FAIL: Server unreachable: \(error)")
            throw ExitCode.failure
        }

        // 3. Check writable directories
        let fm = FileManager.default
        let logDir = fm.homeDirectoryForCurrentUser.appendingPathComponent("Library/Logs").path
        let writableChecks: [(path: String, label: String)] = [
            (logDir, logDir),
            ("/usr/local/bin", "/usr/local/bin"),
        ]
        for check in writableChecks {
            let path = check.path
            if fm.fileExists(atPath: path) {
                guard fm.isWritableFile(atPath: path) else {
                    print("PREFLIGHT FAIL: Directory not writable: \(check.label)")
                    throw ExitCode.failure
                }
                continue
            }

            let parentDir = (path as NSString).deletingLastPathComponent
            guard fm.isWritableFile(atPath: parentDir) else {
                print("PREFLIGHT FAIL: Directory not writable: \(check.label)")
                throw ExitCode.failure
            }
        }
        print("PREFLIGHT: Directory permissions OK")

        print("PREFLIGHT: All checks passed")
    }
}
