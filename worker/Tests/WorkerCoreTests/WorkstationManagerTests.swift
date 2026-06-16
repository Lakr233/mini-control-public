import Logging
import Testing
@testable import WorkerCore

struct WorkstationManagerTests {
    @Test func `removed desired workstation is deleted during reconcile`() async {
        let manager = WorkstationManager(
            config: WorkerConfig(),
            tart: TartDriver(logger: .init(label: "test")),
            logger: .init(label: "test"),
        )

        await manager.applyDesiredWorkstations([
            DesiredWorkstation(
                id: "ws-1",
                vmName: "ws-1",
                slot: 0,
                desiredPowerState: "running",
            ),
        ])
        #expect(await (manager.allVMInfo()).count == 1)

        await manager.applyDesiredWorkstations([])
        await manager.reconcile()

        #expect(await (manager.allVMInfo()).isEmpty)
    }

    @Test func `non authoritative desired workstation update does not delete existing runtime`() async {
        let manager = WorkstationManager(
            config: WorkerConfig(),
            tart: TartDriver(logger: .init(label: "test")),
            logger: .init(label: "test"),
        )

        await manager.applyDesiredWorkstations([
            DesiredWorkstation(
                id: "ws-1",
                vmName: "ws-1",
                slot: 0,
                desiredPowerState: "running",
            ),
        ])
        #expect(await (manager.allVMInfo()).count == 1)

        await manager.applyDesiredWorkstations([], authoritative: false)
        await manager.reconcile()

        #expect(await (manager.allVMInfo()).count == 1)
    }
}
