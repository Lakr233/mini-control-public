import Foundation

public struct OverlaySnapshot: Codable, Sendable {
    public let hostname: String
    public let version: String
    public let vmInfos: [OverlaySnapshotVM]
    public let logLines: [String]
    public let updatedAt: Date
}

public struct OverlaySnapshotVM: Codable, Sendable {
    public let vmID: String
    public let slot: Int
    public let state: String
    public let ipAddress: String?
}

public actor OverlayStateStore {
    private let version: String
    private let persistPath: String?
    private var hostname: String
    private var vmInfos: [OverlaySnapshotVM] = []
    private var logLines: [String] = []
    private var updatedAt: Date = .init()
    private let maxLogLines: Int

    public init(
        hostname: String = "", version: String, maxLogLines: Int = 20, persistPath: String? = nil,
    ) {
        self.hostname = hostname
        self.version = version
        self.maxLogLines = maxLogLines
        self.persistPath = persistPath
    }

    public func setHostname(_ hostname: String) {
        self.hostname = hostname
        updatedAt = Date()
        persistSnapshotIfNeeded()
    }

    public func appendLog(_ message: String) {
        let ts = Self.timeFormatter.string(from: Date())
        logLines.append("\(ts) \(message)")
        if logLines.count > maxLogLines {
            logLines.removeFirst(logLines.count - maxLogLines)
        }
        updatedAt = Date()
        persistSnapshotIfNeeded()
    }

    public func updateVMInfos(_ infos: [VMInfo], hostname: String) {
        self.hostname = hostname
        vmInfos = infos.map {
            OverlaySnapshotVM(
                vmID: $0.vmID,
                slot: $0.slot,
                state: $0.state.rawValue,
                ipAddress: $0.ipAddress,
            )
        }
        updatedAt = Date()
        persistSnapshotIfNeeded()
    }

    public func snapshot() -> OverlaySnapshot {
        OverlaySnapshot(
            hostname: hostname,
            version: version,
            vmInfos: vmInfos,
            logLines: logLines,
            updatedAt: updatedAt,
        )
    }

    private static let timeFormatter: DateFormatter = {
        let f = DateFormatter()
        f.dateFormat = "HH:mm:ss"
        return f
    }()

    private func persistSnapshotIfNeeded() {
        guard let persistPath, !persistPath.isEmpty else { return }

        let snapshot = snapshot()

        do {
            let targetURL = URL(fileURLWithPath: persistPath)
            let dirURL = targetURL.deletingLastPathComponent()
            try FileManager.default.createDirectory(at: dirURL, withIntermediateDirectories: true)
            let data = try JSONEncoder().encode(snapshot)
            try data.write(to: targetURL, options: .atomic)
            try FileManager.default.setAttributes(
                [.posixPermissions: 0o644], ofItemAtPath: targetURL.path,
            )
        } catch {}
    }
}
