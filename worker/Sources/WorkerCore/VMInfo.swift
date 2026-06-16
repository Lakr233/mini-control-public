import Foundation

/// Power state reported for a workstation VM. Raw values are the wire
/// protocol shared with the server's workstation power states.
public enum VMState: String, Sendable {
    case starting
    case running
    case stopping
    case stopped
    case deleting
    case deleted
    case error
}

/// Point-in-time snapshot of a workstation VM, used for heartbeats and the
/// host overlay display.
public struct VMInfo: Sendable {
    public let vmID: String
    public let slot: Int
    public let state: VMState
    public let ipAddress: String?
    public let stateChangedAt: Date
    public let lastError: String?
}
