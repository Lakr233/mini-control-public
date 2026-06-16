import Foundation
import Testing
@testable import WorkerCore

struct TartDriverTests {
    @Test func `configure arguments use derived cpu 6g memory dynamic disk and 1080p display`() {
        let args = TartDriver.configureArguments(name: "vm-test", targetCPUCount: 4, targetDiskSizeGiB: 96)

        #expect(args == [
            "tart",
            "set",
            "vm-test",
            "--cpu",
            "4",
            "--memory",
            "6144",
            "--display",
            "1920x1080",
            "--disk-size",
            "96",
        ])
    }

    @Test func `configure arguments can leave disk size unchanged`() {
        let args = TartDriver.configureArguments(name: "vm-test", targetCPUCount: 4, targetDiskSizeGiB: nil)

        #expect(args == [
            "tart",
            "set",
            "vm-test",
            "--cpu",
            "4",
            "--memory",
            "6144",
            "--display",
            "1920x1080",
        ])
    }

    @Test func `parse disk size from tart list output`() {
        let output = """
        Source Name                                                                                                         Disk Size Accessed      State
        local  minictl-tahoe-base                                                                                           140  86   3 minutes ago stopped
        local  ws-041f8ba3f8db                                                                                               140  86   1 second ago stopped
        OCI    ghcr.io/cirruslabs/macos-tahoe-xcode:26.4                                                                    140  86   3 minutes ago stopped
        """

        #expect(TartDriver.parseDiskSizeGiBFromList(output: output, name: "ws-041f8ba3f8db") == 140)
        #expect(TartDriver.parseDiskSizeGiBFromList(output: output, name: "missing") == nil)
    }

    @Test func `configure arguments clamp cpu to at least one`() {
        let args = TartDriver.configureArguments(name: "vm-test", targetCPUCount: 0, targetDiskSizeGiB: 96)
        #expect(args[4] == "1")
    }

    @Test func `target cpu count uses physical cores minus one then half floored`() {
        #expect(TartDriver.targetCPUCount(physicalCoreCount: 1) == 1)
        #expect(TartDriver.targetCPUCount(physicalCoreCount: 2) == 1)
        #expect(TartDriver.targetCPUCount(physicalCoreCount: 3) == 1)
        #expect(TartDriver.targetCPUCount(physicalCoreCount: 4) == 1)
        #expect(TartDriver.targetCPUCount(physicalCoreCount: 5) == 2)
        #expect(TartDriver.targetCPUCount(physicalCoreCount: 10) == 4)
        #expect(TartDriver.targetCPUCount(physicalCoreCount: 12) == 5)
    }

    @Test func `target cpu count divides by configured vm count`() {
        #expect(TartDriver.targetCPUCount(physicalCoreCount: 10, vmCount: 1) == 9)
        #expect(TartDriver.targetCPUCount(physicalCoreCount: 10, vmCount: 2) == 4)
        #expect(TartDriver.targetCPUCount(physicalCoreCount: 10, vmCount: 3) == 3)
        #expect(TartDriver.targetCPUCount(physicalCoreCount: 10, vmCount: 4) == 2)
        #expect(TartDriver.targetCPUCount(physicalCoreCount: 10, vmCount: 0) == 9)
    }

    @Test func `target disk size subtracts reserved and divides by vm count`() {
        #expect(TartDriver.targetDiskSizeGiB(totalDiskGiB: 256) == 96)
        #expect(TartDriver.targetDiskSizeGiB(totalDiskGiB: 512) == 224)
        #expect(TartDriver.targetDiskSizeGiB(totalDiskGiB: 1024) == 480)
        #expect(TartDriver.targetDiskSizeGiB(totalDiskGiB: 256, vmCount: 4) == 48)
        #expect(TartDriver.targetDiskSizeGiB(totalDiskGiB: 64) == 1)
        #expect(TartDriver.targetDiskSizeGiB(totalDiskGiB: 0) == 1)
    }

    @Test func `total disk gib returns a plausible host disk size`() {
        let total = TartDriver.totalDiskGiB()
        // Any real Mac should have at least 64 GiB of disk
        #expect(total >= 64)
        // Sanity upper bound: 64 TiB
        #expect(total <= 64 * 1024)
    }

    @Test func `host disk size pipeline produces a positive value`() {
        let perVM = TartDriver.targetDiskSizeGiB(totalDiskGiB: TartDriver.totalDiskGiB())
        #expect(perVM >= 1)
    }

    @Test func `clone profile updates config JSON`() throws {
        let original = """
        {"cpuCountMin":2,"memorySizeMin":8589934592,"hardwareModel":"abc","diskFormat":"raw","arch":"arm64","os":"darwin","version":1,"memorySize":8589934592,"cpuCount":4,"macAddress":"3e:c4:6b:7e:4b:ac","display":{"width":1024,"height":768}}
        """.data(using: .utf8)!

        let updated = try TartDriver.applyingCloneProfile(to: original, targetCPUCount: 4)
        let json = try #require(JSONSerialization.jsonObject(with: updated) as? [String: Any])
        let display = try #require(json["display"] as? [String: Any])
        let memorySizeMin = try #require((json["memorySizeMin"] as? NSNumber)?.int64Value)
        let memorySize = try #require((json["memorySize"] as? NSNumber)?.int64Value)

        #expect((json["cpuCountMin"] as? NSNumber)?.intValue == 4)
        #expect((json["cpuCount"] as? NSNumber)?.intValue == 4)
        #expect(memorySizeMin == Int64(6) * 1024 * 1024 * 1024)
        #expect(memorySize == Int64(6) * 1024 * 1024 * 1024)
        #expect((display["width"] as? NSNumber)?.intValue == 1920)
        #expect((display["height"] as? NSNumber)?.intValue == 1080)
        #expect((json["hardwareModel"] as? String) == "abc")
        #expect((json["macAddress"] as? String) == "3e:c4:6b:7e:4b:ac")
    }
}
