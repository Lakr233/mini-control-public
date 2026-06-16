import Foundation
import Logging

public struct DesiredWorkstation: Codable, Sendable {
    public let id: String
    public let vmName: String
    public let slot: Int
    public let desiredPowerState: String

    enum CodingKeys: String, CodingKey {
        case id
        case vmName = "vm_name"
        case slot
        case desiredPowerState = "desired_power_state"
    }
}

public actor WorkstationRuntime {
    public let workstationID: String
    public let vmName: String
    public let slot: Int

    private let tart: TartDriver
    private let config: WorkerConfig
    private let logger: Logger

    private var desiredPowerState: String
    private var state: VMState = .stopped
    private var ipAddress: String?
    private var stateChangedAt: Date = .init()
    private var vmProcess: Process?
    private var lastError: String?

    public init(desired: DesiredWorkstation, tart: TartDriver, config: WorkerConfig, logger: Logger) {
        workstationID = desired.id
        vmName = desired.vmName
        slot = desired.slot
        desiredPowerState = desired.desiredPowerState
        self.tart = tart
        self.config = config
        self.logger = logger
    }

    public func apply(desired: DesiredWorkstation) {
        desiredPowerState = desired.desiredPowerState
    }

    public func markForDeletion() {
        desiredPowerState = "deleted"
    }

    public func reconcile() async {
        switch desiredPowerState {
        case "running":
            await ensureRunning()
        case "stopped":
            await ensureStopped()
        case "deleted":
            await ensureDeleted()
        default:
            setState(.error, error: "unknown desired power state \(desiredPowerState)")
        }
    }

    public func info() -> VMInfo {
        VMInfo(
            vmID: workstationID,
            slot: slot,
            state: state,
            ipAddress: ipAddress,
            stateChangedAt: stateChangedAt,
            lastError: lastError,
        )
    }

    private func ensureRunning() async {
        setState(.starting, error: nil)

        do {
            if await !(tart.exists(name: vmName)) {
                try await tart.clone(source: config.baseImage, name: vmName)
                try await tart.configure(name: vmName, vmCount: config.poolSize)
            }

            if let ip = try await currentIPAddress(timeout: .seconds(5)) {
                ipAddress = ip
                setState(.running, error: nil)
                return
            }

            if vmProcess != nil {
                return
            }

            try await tart.configure(name: vmName, vmCount: config.poolSize)
            vmProcess = try await tart.start(name: vmName)
            let ip = try await tart.ip(name: vmName, timeout: .seconds(60))
            ipAddress = ip
            setState(.running, error: nil)
        } catch {
            setState(.error, error: String(describing: error))
        }
    }

    private func ensureStopped() async {
        setState(.stopping, error: nil)

        do {
            if await tart.exists(name: vmName) {
                try await tart.stopIfRunning(name: vmName)
            }
            vmProcess = nil
            ipAddress = nil
            setState(.stopped, error: nil)
        } catch {
            setState(.error, error: String(describing: error))
        }
    }

    private func ensureDeleted() async {
        setState(.deleting, error: nil)

        do {
            if await tart.exists(name: vmName) {
                try await tart.stopIfRunning(name: vmName)
                try await tart.deleteIfExists(name: vmName)
            }
            vmProcess = nil
            ipAddress = nil
            setState(.deleted, error: nil)
        } catch {
            setState(.error, error: String(describing: error))
        }
    }

    private func currentIPAddress(timeout: Duration) async throws -> String? {
        guard await tart.exists(name: vmName) else { return nil }
        do {
            let ip = try await tart.ip(name: vmName, timeout: timeout)
            let trimmed = ip.trimmingCharacters(in: .whitespacesAndNewlines)
            return trimmed.isEmpty ? nil : trimmed
        } catch {
            return nil
        }
    }

    private func setState(_ newState: VMState, error: String?) {
        state = newState
        stateChangedAt = Date()
        lastError = error
    }
}

public actor WorkstationManager {
    private let config: WorkerConfig
    private let tart: TartDriver
    private let logger: Logger

    private var runtimes: [String: WorkstationRuntime] = [:]
    private var desiredIDs: Set<String> = []

    public init(config: WorkerConfig, tart: TartDriver, logger: Logger) {
        self.config = config
        self.tart = tart
        self.logger = logger
    }

    public func applyDesiredWorkstations(
        _ desiredWorkstations: [DesiredWorkstation],
        authoritative: Bool = true,
    ) async {
        guard authoritative else {
            logger.warning("Skipping non-authoritative desired workstation update")
            return
        }

        desiredIDs = Set(desiredWorkstations.map(\.id))

        for desired in desiredWorkstations {
            if let runtime = runtimes[desired.id] {
                await runtime.apply(desired: desired)
            } else {
                runtimes[desired.id] = WorkstationRuntime(
                    desired: desired,
                    tart: tart,
                    config: config,
                    logger: logger,
                )
            }
        }

        for (workstationID, runtime) in runtimes where !desiredIDs.contains(workstationID) {
            await runtime.markForDeletion()
        }
    }

    public func reconcile() async {
        for runtime in runtimes.values {
            await runtime.reconcile()
        }

        for (workstationID, runtime) in runtimes {
            if desiredIDs.contains(workstationID) {
                continue
            }
            let info = await runtime.info()
            if info.state == .deleted {
                runtimes.removeValue(forKey: workstationID)
            }
        }
    }

    public func allVMInfo() async -> [VMInfo] {
        var infos: [VMInfo] = []
        for (_, runtime) in runtimes.sorted(by: { $0.key < $1.key }) {
            await infos.append(runtime.info())
        }
        return infos
    }

    public func findVM(byID vmID: String) async -> VMInfo? {
        guard let runtime = runtimes[vmID] else { return nil }
        return await runtime.info()
    }

    public func shutdown() async {
        logger.info("WorkstationManager shutting down")
    }
}
