import Darwin
import Foundation
import Logging

/// Thin wrapper around the Tart CLI for VM lifecycle management.
public struct TartDriver: Sendable {
    static let cloneProfileMemoryMiB = 6144
    static let cloneProfileMemoryBytes = NSNumber(value: Int64(cloneProfileMemoryMiB) * 1024 * 1024)
    static let reservedDiskGiB = 64
    static let cloneProfileDisplay = "1920x1080"
    static let cloneProfileDisplayWidth = 1920
    static let cloneProfileDisplayHeight = 1080

    private let logger: Logger

    public init(logger: Logger) {
        self.logger = logger
    }

    /// Clone a base image to create a new VM.
    public func clone(source: String, name: String) async throws {
        logger.info("Cloning VM", metadata: ["source": "\(source)", "name": "\(name)"])
        try await run("tart", "clone", source, name)
    }

    /// Configure VM resources.
    public func configure(name: String, vmCount: Int) async throws {
        let targetCPUCount = Self.hostCPUCount(vmCount: vmCount)
        let targetDiskSizeGiB = Self.hostDiskSizeGiB(vmCount: vmCount)
        let currentDiskSizeGiB = try await currentDiskSizeGiB(name: name)
        let targetDiskSizeArgument =
            currentDiskSizeGiB.map { max($0, targetDiskSizeGiB) } ?? targetDiskSizeGiB
        let diskSizeToSet = currentDiskSizeGiB == targetDiskSizeArgument ? nil : targetDiskSizeArgument
        logger.info(
            "Configuring VM",
            metadata: [
                "name": "\(name)",
                "cpu": "\(targetCPUCount)",
                "memory": "\(Self.cloneProfileMemoryMiB)",
                "vm_count": "\(vmCount)",
                "disk_size_gib": "\(targetDiskSizeGiB)",
                "current_disk_size_gib": "\(currentDiskSizeGiB.map(String.init) ?? "unknown")",
                "set_disk_size_gib": "\(diskSizeToSet.map(String.init) ?? "unchanged")",
                "display": "\(Self.cloneProfileDisplay)",
            ],
        )
        try await exec(
            Self.configureArguments(
                name: name,
                targetCPUCount: targetCPUCount,
                targetDiskSizeGiB: diskSizeToSet,
            ),
        )
        try Self.updateLocalConfig(name: name, targetCPUCount: targetCPUCount)
    }

    /// Start VM in headless mode. Returns the Process handle.
    public func start(name: String) async throws -> Process {
        logger.info("Starting VM", metadata: ["name": "\(name)"])
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/bin/env")
        process.arguments = ["tart", "run", name, "--no-graphics"]
        process.standardOutput = FileHandle.nullDevice
        process.standardError = FileHandle.nullDevice

        // Create new process group so it doesn't die with parent
        process.qualityOfService = .userInitiated

        try process.run()
        return process
    }

    /// Stop a running VM.
    public func stop(name: String) async throws {
        logger.info("Stopping VM", metadata: ["name": "\(name)"])
        try await run("tart", "stop", name)
    }

    /// Delete a VM.
    public func delete(name: String) async throws {
        logger.info("Deleting VM", metadata: ["name": "\(name)"])
        try await run("tart", "delete", name)
    }

    /// List locally cloned Tart VMs.
    public func listLocalVMs() async throws -> [String] {
        let output = try await runOutput("tart", "list")
        return
            output
                .split(whereSeparator: \.isNewline)
                .compactMap { line in
                    let trimmed = line.trimmingCharacters(in: .whitespacesAndNewlines)
                    guard trimmed.hasPrefix("local ") else { return nil }
                    let parts = trimmed.split(whereSeparator: \.isWhitespace)
                    guard parts.count >= 2 else { return nil }
                    return String(parts[1])
                }
    }

    /// Stop a VM if it is still running.
    public func stopIfRunning(name: String) async throws {
        do {
            try await stop(name: name)
        } catch let error as TartError {
            if error.outputContains("is not running") || error.outputContains("not running") {
                return
            }
            throw error
        }
    }

    /// Delete a VM if it still exists.
    public func deleteIfExists(name: String) async throws {
        if await exists(name: name) {
            try await delete(name: name)
        }
    }

    /// Get the IP address of a running VM. Retries internally.
    public func ip(name: String, timeout: Duration = .seconds(60)) async throws -> String {
        let deadline = ContinuousClock.now + timeout
        var lastError: Error?

        while ContinuousClock.now < deadline {
            do {
                let output = try await runOutput("tart", "ip", name, "--wait", "10")
                let ip = output.trimmingCharacters(in: .whitespacesAndNewlines)
                if !ip.isEmpty {
                    logger.info("Got VM IP", metadata: ["name": "\(name)", "ip": "\(ip)"])
                    return ip
                }
            } catch {
                lastError = error
            }
            try await Task.sleep(for: .seconds(2))
        }

        throw TartError.ipTimeout(name: name, underlying: lastError)
    }

    /// Check if a VM exists locally.
    public func exists(name: String) async -> Bool {
        do {
            _ = try await runOutput("tart", "get", name)
            return true
        } catch {
            return false
        }
    }

    // MARK: - Private

    @discardableResult
    private func exec(_ args: [String]) async throws -> String {
        let result = try await ProcessRunner.run(
            executableURL: URL(fileURLWithPath: "/usr/bin/env"),
            arguments: args,
        )

        if result.terminationStatus != 0 {
            let errOutput = String(data: result.stderr, encoding: .utf8) ?? ""
            throw TartError.commandFailed(args: args, status: result.terminationStatus, output: errOutput)
        }

        return String(data: result.stdout, encoding: .utf8) ?? ""
    }

    private func run(_ args: String...) async throws {
        try await exec(Array(args))
    }

    private func runOutput(_ args: String...) async throws -> String {
        try await exec(Array(args))
    }

    static func configureArguments(name: String, targetCPUCount: Int, targetDiskSizeGiB: Int?) -> [String] {
        var args = [
            "tart",
            "set",
            name,
            "--cpu",
            "\(max(1, targetCPUCount))",
            "--memory",
            "\(cloneProfileMemoryMiB)",
            "--display",
            cloneProfileDisplay,
        ]
        if let targetDiskSizeGiB {
            args.append(contentsOf: ["--disk-size", "\(max(1, targetDiskSizeGiB))"])
        }
        return args
    }

    static func applyingCloneProfile(to data: Data, targetCPUCount: Int) throws -> Data {
        guard var json = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw TartConfigError.invalidFormat
        }

        let normalizedTargetCPUCount = NSNumber(value: max(1, targetCPUCount))
        json["cpuCountMin"] = normalizedTargetCPUCount
        json["cpuCount"] = normalizedTargetCPUCount
        json["memorySizeMin"] = cloneProfileMemoryBytes
        json["memorySize"] = cloneProfileMemoryBytes

        var display = json["display"] as? [String: Any] ?? [:]
        display["width"] = NSNumber(value: cloneProfileDisplayWidth)
        display["height"] = NSNumber(value: cloneProfileDisplayHeight)
        json["display"] = display

        return try JSONSerialization.data(withJSONObject: json, options: [.sortedKeys])
    }

    private static func updateLocalConfig(name: String, targetCPUCount: Int) throws {
        let configURL = localConfigURL(for: name)
        let data = try Data(contentsOf: configURL)
        let updatedData = try applyingCloneProfile(to: data, targetCPUCount: targetCPUCount)
        try updatedData.write(to: configURL, options: .atomic)
    }

    /// Best-effort lookup of the VM's current disk size: the local config
    /// file first, then `tart list`. Returns nil when neither source knows.
    private func currentDiskSizeGiB(name: String) async throws -> Int? {
        do {
            if let configDiskSize = try Self.currentDiskSizeGiBFromConfig(name: name) {
                return configDiskSize
            }
        } catch {
            logger.debug(
                "Could not read disk size from local VM config, falling back to tart list",
                metadata: ["name": "\(name)", "error": "\(error)"],
            )
        }

        let output = try await runOutput("tart", "list")
        return Self.parseDiskSizeGiBFromList(output: output, name: name)
    }

    private static let configDiskSizeKeys = ["diskSize", "diskSizeMin", "diskSizeBytes", "diskSizeMinBytes"]

    private static func currentDiskSizeGiBFromConfig(name: String) throws -> Int? {
        let configURL = localConfigURL(for: name)
        let data = try Data(contentsOf: configURL)
        guard let json = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw TartConfigError.invalidFormat
        }

        for key in configDiskSizeKeys {
            guard let bytes = numericValue(json[key]) else { continue }
            return Int((bytes / 1024 / 1024 / 1024).rounded(.up))
        }

        return nil
    }

    static func parseDiskSizeGiBFromList(output: String, name: String) -> Int? {
        for line in output.split(whereSeparator: \.isNewline) {
            let parts = line.split(whereSeparator: \.isWhitespace)
            guard parts.count >= 4, parts[0] == "local", parts[1] == Substring(name) else {
                continue
            }
            return Int(parts[2])
        }
        return nil
    }

    private static func numericValue(_ value: Any?) -> Double? {
        switch value {
        case let number as NSNumber:
            return number.doubleValue
        case let int as Int:
            return Double(int)
        case let double as Double:
            return double
        default:
            return nil
        }
    }

    private static func localConfigURL(for name: String) -> URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".tart", isDirectory: true)
            .appendingPathComponent("vms", isDirectory: true)
            .appendingPathComponent(name, isDirectory: true)
            .appendingPathComponent("config.json", isDirectory: false)
    }

    private static func hostCPUCount(vmCount: Int) -> Int {
        targetCPUCount(physicalCoreCount: physicalCoreCount(), vmCount: vmCount)
    }

    static func targetCPUCount(physicalCoreCount: Int, vmCount: Int = 2) -> Int {
        max(1, (max(1, physicalCoreCount) - 1) / max(1, vmCount))
    }

    private static func hostDiskSizeGiB(vmCount: Int) -> Int {
        targetDiskSizeGiB(totalDiskGiB: totalDiskGiB(), vmCount: vmCount)
    }

    static func targetDiskSizeGiB(totalDiskGiB: Int, vmCount: Int = 2) -> Int {
        max(1, (max(0, totalDiskGiB) - reservedDiskGiB) / max(1, vmCount))
    }

    static func totalDiskGiB() -> Int {
        if let attrs = try? FileManager.default.attributesOfFileSystem(forPath: "/"),
           let totalBytes = attrs[.systemSize] as? Int64
        {
            return Int(totalBytes / (1024 * 1024 * 1024))
        }
        return 256
    }

    private static func physicalCoreCount() -> Int {
        var value: Int32 = 0
        var size = MemoryLayout<Int32>.size
        if sysctlbyname("hw.physicalcpu", &value, &size, nil, 0) == 0, value > 0 {
            return Int(value)
        }
        return max(1, ProcessInfo.processInfo.processorCount)
    }
}

public enum TartError: Error, CustomStringConvertible {
    case commandFailed(args: [String], status: Int32, output: String)
    case ipTimeout(name: String, underlying: Error?)

    func outputContains(_ needle: String) -> Bool {
        switch self {
        case let .commandFailed(_, _, output):
            output.localizedCaseInsensitiveContains(needle)
        case .ipTimeout:
            false
        }
    }

    public var description: String {
        switch self {
        case let .commandFailed(args, status, output):
            "tart command failed (\(args.joined(separator: " "))): exit \(status) - \(output)"
        case let .ipTimeout(name, _):
            "timeout waiting for IP of VM '\(name)'"
        }
    }
}

enum TartConfigError: Error {
    case invalidFormat
}
