import Foundation
import IOKit
import IOKit.pwr_mgt
import Logging

/// Top-level coordinator for the worker process.
/// Manages registration, dedicated workstations, heartbeats, and proxy relays.
public actor Worker {
    private var config: WorkerConfig
    private let configPath: String
    private let currentVersion: String
    private let hostname: String
    private let workstations: WorkstationManager
    private let apiClient: MiniControlAPIClient
    private let logger: Logger
    private let overlayStateStore: OverlayStateStore?

    private var workerID: String = ""

    // Keychain management
    private var keychainPassword: String = ""
    private var keychainUnlocked: Bool = false

    // Active proxy relays
    private var activeRelays: [String: Task<Void, Never>] = [:]
    private var finishedRelayIDs: Set<String> = []

    /// Power assertion to prevent system sleep
    private var sleepAssertionID: IOPMAssertionID = .init(0)

    public init(
        config: WorkerConfig,
        configPath: String = "/etc/minicontrol/config.yaml",
        currentVersion: String,
        logger: Logger,
        overlayStateStore: OverlayStateStore? = nil,
    ) {
        self.config = config
        self.configPath = configPath
        self.currentVersion = currentVersion
        self.logger = logger
        self.overlayStateStore = overlayStateStore
        apiClient = MiniControlAPIClient(config: config, workerVersion: currentVersion, logger: logger)
        let hostname = ProcessInfo.processInfo.hostName
        self.hostname = hostname
        let tart = TartDriver(logger: logger)
        workstations = WorkstationManager(
            config: config,
            tart: tart,
            logger: logger,
        )
    }

    /// Main run loop.
    public func run() async throws {
        logger.info(
            "Worker starting",
            metadata: [
                "hostname": "\(hostname)",
                "server": "\(config.serverAddress)",
            ],
        )
        await overlayStateStore?.setHostname(hostname)

        preventSleep()
        await overlayLog("worker v\(currentVersion) starting...")

        // Gather system info
        let hardwareUUID = Self.getHardwareUUID()
        let cpuCores = UInt32(ProcessInfo.processInfo.processorCount)
        let memoryBytes = UInt64(ProcessInfo.processInfo.physicalMemory)
        await overlayLog("cpu=\(cpuCores) mem=\(memoryBytes / 1024 / 1024 / 1024)GB")

        // Register with server
        await overlayLog("registering with \(config.serverAddress)...")
        let registration = try await apiClient.register(
            hostname: hostname,
            hardwareUUID: hardwareUUID,
            cpuCores: cpuCores,
            memoryBytes: memoryBytes,
            tartVersion: "",
        )

        workerID = registration.workerID
        persistIssuedWorkerTokenIfNeeded(registration.workerToken)
        await overlayLog("registered: \(workerID)")

        // Unlock login keychain (required for Tart VMs on macOS 15+)
        keychainPassword = config.hostKeychainPassword
        if !keychainPassword.isEmpty {
            await overlayLog("unlocking keychain...")
            await tryUnlockKeychain()
        }

        await overlayLog("worker ready for on-demand workstations")

        logger.info("Worker ready, entering management loop")
        try await managementLoop()
    }

    private func tryUnlockKeychain() async {
        guard !keychainPassword.isEmpty, !keychainUnlocked else { return }
        let unlocker = KeychainUnlocker(logger: logger)
        do {
            try await unlocker.ensureKeychainUnlocked(password: keychainPassword)
            keychainUnlocked = true
            await overlayLog("keychain unlocked")
        } catch {
            logger.warning("Keychain unlock failed", metadata: ["error": "\(error)"])
            await overlayLog("keychain unlock failed: \(error)")
        }
    }

    private func overlayLog(_ message: String) async {
        logger.info("\(message)")
        await overlayStateStore?.appendLog(message)
    }

    public func displayFatalErrorBeforeExit(_ error: Error, delay: Duration = .seconds(6)) async {
        let message: String =
            if let localizedError = error as? LocalizedError,
            let description = localizedError.errorDescription, !description.isEmpty {
                description
            } else {
                String(describing: error)
            }
        await overlayLog("fatal: \(message)")
        try? await Task.sleep(for: delay)
    }

    /// Graceful shutdown.
    public func shutdown(stopOverlay _: Bool = true) async {
        logger.info("Worker shutting down...")
        allowSleep()
        await workstations.shutdown()
        logger.info("Worker stopped")
    }

    // MARK: - Private

    private func managementLoop() async throws {
        let heartbeatInterval: Duration = .seconds(5)
        let pollInterval: Duration = .seconds(2)
        let clock = ContinuousClock()
        var nextHeartbeat = clock.now

        // Heartbeat and reconciliation loop
        while !Task.isCancelled {
            await workstations.reconcile()

            let vmInfos = await workstations.allVMInfo()
            let runningCount = vmInfos.count(where: { $0.state == .running })
            let startingCount = vmInfos.count(where: { $0.state == .starting })
            logger.info(
                "Workstation status",
                metadata: [
                    "running": "\(runningCount)",
                    "starting": "\(startingCount)",
                    "total": "\(vmInfos.count)",
                ],
            )

            await overlayStateStore?.updateVMInfos(vmInfos, hostname: hostname)

            // Poll for proxy sessions
            await pollProxySessions()

            // Clean up finished relays
            for id in finishedRelayIDs {
                activeRelays.removeValue(forKey: id)
            }
            finishedRelayIDs.removeAll()

            // Retry keychain unlock if not yet successful
            if !keychainUnlocked {
                await tryUnlockKeychain()
            }

            if clock.now >= nextHeartbeat {
                let heartbeatPayload = vmInfos.map { vm in
                    WorkstationHeartbeatStatus(
                        workstationID: vm.vmID,
                        powerState: vm.state.rawValue,
                        ipAddress: vm.ipAddress,
                        lastError: vm.lastError,
                    )
                }
                logger.info(
                    "Sending heartbeat",
                    metadata: [
                        "worker_id": "\(workerID)",
                        "workstation_count": "\(heartbeatPayload.count)",
                        "workstation_ids": "\(heartbeatPayload.map(\.workstationID).joined(separator: ","))",
                    ],
                )
                let heartbeatResponse = await apiClient.sendHeartbeat(workstationStatuses: heartbeatPayload)
                if !heartbeatResponse.isAuthoritative {
                    logger.warning(
                        "Heartbeat response was not authoritative; keeping existing desired workstation state",
                        metadata: [
                            "worker_id": "\(workerID)",
                            "status": "\(heartbeatResponse.status)",
                        ],
                    )
                }
                await workstations.applyDesiredWorkstations(
                    heartbeatResponse.desiredWorkstations,
                    authoritative: heartbeatResponse.isAuthoritative,
                )

                nextHeartbeat = clock.now + heartbeatInterval
            }

            try await Task.sleep(for: pollInterval)
        }
    }

    private func persistIssuedWorkerTokenIfNeeded(_ issuedWorkerToken: String?) {
        guard let issuedWorkerToken, !issuedWorkerToken.isEmpty else {
            return
        }
        if config.workerToken == issuedWorkerToken, config.enrollmentToken.isEmpty {
            return
        }

        config.workerToken = issuedWorkerToken
        config.enrollmentToken = ""
        do {
            try config.save(to: configPath)
            logger.info("Persisted per-worker auth token", metadata: ["config_path": "\(configPath)"])
        } catch {
            logger.error(
                "Failed to persist per-worker auth token",
                metadata: [
                    "config_path": "\(configPath)",
                    "error": "\(error)",
                ],
            )
        }
    }

    // MARK: - Proxy Relay

    private func pollProxySessions() async {
        let sessions = await apiClient.pollForProxySessions()
        for session in sessions {
            guard activeRelays[session.sessionID] == nil else { continue }

            let targetIP: String

            switch session.target {
            case "host":
                // Connect to localhost (the worker IS the host)
                targetIP = "127.0.0.1"

            case "vm":
                guard let vmID = session.vmID else {
                    logger.warning("Proxy: VM session missing vm_id")
                    continue
                }
                guard let info = await workstations.findVM(byID: vmID) else {
                    logger.warning("Proxy: VM not found", metadata: ["vm_id": "\(vmID)"])
                    continue
                }
                guard let ip = info.ipAddress else {
                    logger.warning("Proxy: VM has no IP", metadata: ["vm_id": "\(vmID)"])
                    continue
                }
                targetIP = ip

            default:
                logger.warning("Proxy: unknown target", metadata: ["target": "\(session.target)"])
                continue
            }

            let baseURL = await apiClient.getBaseURL()
            let relay = ProxyRelay(logger: logger, tlsCertFingerprint: config.tlsCertFingerprint)
            let sessionID = session.sessionID
            let token = session.token
            let port = session.port

            logger.info(
                "Starting proxy relay",
                metadata: [
                    "session_id": "\(sessionID)", "target": "\(session.target)",
                    "target_ip": "\(targetIP)", "port": "\(port)",
                ],
            )

            activeRelays[sessionID] = Task { [weak self] in
                await relay.start(
                    serverBaseURL: baseURL,
                    sessionID: sessionID,
                    token: token,
                    vmIP: targetIP,
                    port: port,
                )
                await self?.markRelayFinished(sessionID)
            }
        }
    }

    private func markRelayFinished(_ sessionID: String) {
        finishedRelayIDs.insert(sessionID)
    }

    // MARK: - Sleep Prevention (Caffeine)

    private func preventSleep() {
        let result = IOPMAssertionCreateWithName(
            kIOPMAssertionTypePreventUserIdleSystemSleep as CFString,
            IOPMAssertionLevel(kIOPMAssertionLevelOn),
            "minicontrol-worker: keeping host awake for VM management" as CFString,
            &sleepAssertionID,
        )
        if result == kIOReturnSuccess {
            logger.info("Sleep prevention enabled (assertion \(sleepAssertionID))")
        } else {
            logger.warning("Failed to create sleep prevention assertion: \(result)")
        }
    }

    private func allowSleep() {
        guard sleepAssertionID != 0 else { return }
        let result = IOPMAssertionRelease(sleepAssertionID)
        if result == kIOReturnSuccess {
            logger.info("Sleep prevention released")
        } else {
            logger.warning("Failed to release sleep assertion: \(result)")
        }
        sleepAssertionID = IOPMAssertionID(0)
    }

    // MARK: - Hardware UUID

    /// Retrieve the macOS Hardware UUID (IOPlatformUUID) via IOKit.
    /// Falls back to hostname-based UUID if IOKit call fails.
    private static func getHardwareUUID() -> String {
        let service = IOServiceGetMatchingService(
            kIOMainPortDefault,
            IOServiceMatching("IOPlatformExpertDevice"),
        )
        guard service != IO_OBJECT_NULL else {
            return "unknown-\(ProcessInfo.processInfo.hostName)"
        }
        defer { IOObjectRelease(service) }

        if let uuidCF = IORegistryEntryCreateCFProperty(
            service,
            "IOPlatformUUID" as CFString,
            kCFAllocatorDefault,
            0,
        )?.takeRetainedValue() as? String {
            return uuidCF
        }
        return "unknown-\(ProcessInfo.processInfo.hostName)"
    }
}
