import Foundation
import Testing
@testable import WorkerCore

struct ControlAPIClientDataTypesTests {
    // --- RegistrationResult ---

    @Test func `registration result coding keys`() throws {
        let json = """
        {
            "worker_id": "w-test",
            "pool_size": 4,
            "base_image": "img"
        }
        """.data(using: .utf8)!

        let result = try JSONDecoder().decode(RegistrationResult.self, from: json)
        #expect(result.workerID == "w-test")
        #expect(result.poolSize == 4)
        #expect(result.baseImage == "img")
        #expect(result.workerToken == nil)
    }

    @Test func `registration result decodes issued worker token`() throws {
        let json = """
        {
            "worker_id": "w-test",
            "pool_size": 4,
            "base_image": "img",
            "worker_token": "wkt_issued"
        }
        """.data(using: .utf8)!

        let result = try JSONDecoder().decode(RegistrationResult.self, from: json)
        #expect(result.workerToken == "wkt_issued")
    }

    // --- HeartbeatResponse ---

    @Test func `heartbeat response decode`() throws {
        let json = """
        {
            "status": "ok",
            "desired_workstations": [
                {
                    "id": "ws-1",
                    "vm_name": "ws-abc123",
                    "slot": 0,
                    "desired_power_state": "running"
                }
            ]
        }
        """.data(using: .utf8)!

        let resp = try JSONDecoder().decode(HeartbeatResponse.self, from: json)
        #expect(resp.status == "ok")
        #expect(resp.desiredWorkstations.count == 1)
        #expect(resp.desiredWorkstations[0].id == "ws-1")
    }

    // --- WorkstationHeartbeatStatus ---

    @Test func `workstation heartbeat status encode`() throws {
        let status = WorkstationHeartbeatStatus(
            workstationID: "ws-1",
            powerState: "running",
            ipAddress: "192.168.64.3",
            lastError: "",
        )

        let data = try JSONEncoder().encode(status)
        let decoded = try JSONDecoder().decode(WorkstationHeartbeatStatus.self, from: data)
        #expect(decoded.workstationID == "ws-1")
        #expect(decoded.powerState == "running")
        #expect(decoded.ipAddress == "192.168.64.3")
    }

    // --- PendingProxySession ---

    @Test func `pending proxy session decode VM target`() throws {
        let json = """
        {
            "session_id": "sess-1",
            "target": "vm",
            "vm_id": "vm-1",
            "port": 5900,
            "token": "tok-1"
        }
        """.data(using: .utf8)!

        let session = try JSONDecoder().decode(PendingProxySession.self, from: json)
        #expect(session.sessionID == "sess-1")
        #expect(session.target == "vm")
        #expect(session.vmID == "vm-1")
        #expect(session.port == 5900)
        #expect(session.token == "tok-1")
    }

    @Test func `pending proxy session decode host target`() throws {
        let json = """
        {
            "session_id": "sess-2",
            "target": "host",
            "port": 22,
            "token": "tok-2"
        }
        """.data(using: .utf8)!

        let session = try JSONDecoder().decode(PendingProxySession.self, from: json)
        #expect(session.sessionID == "sess-2")
        #expect(session.target == "host")
        #expect(session.vmID == nil)
        #expect(session.port == 22)
    }

    // --- HeartbeatPayload ---

    @Test func `heartbeat payload encode`() throws {
        let payload = HeartbeatPayload(
            workstationStatuses: [
                WorkstationHeartbeatStatus(
                    workstationID: "ws-1",
                    powerState: "running",
                    ipAddress: "10.0.0.1",
                    lastError: "",
                ),
            ],
        )

        let data = try JSONEncoder().encode(payload)
        let json = try #require(JSONSerialization.jsonObject(with: data) as? [String: Any])

        let statuses = json["workstation_statuses"] as? [[String: Any]]
        #expect(statuses?.count == 1)
    }

    // --- MiniControlAPIClient URL construction ---

    @Test func `client base URL HTTP`() async {
        let config = WorkerConfig(
            serverAddress: "example.com:9090",
            httpPort: 8080,
            useHTTPS: false,
        )
        let client = MiniControlAPIClient(
            config: config,
            logger: .init(label: "test"),
        )
        let baseURL = await client.getBaseURL()
        #expect(baseURL == "http://example.com:8080")
    }

    @Test func `client base URL HTTPS`() async {
        let config = WorkerConfig(
            serverAddress: "secure.example.com:9090",
            httpPort: 443,
            useHTTPS: true,
        )
        let client = MiniControlAPIClient(
            config: config,
            logger: .init(label: "test"),
        )
        let baseURL = await client.getBaseURL()
        #expect(baseURL == "https://secure.example.com:443")
    }

    @Test func `client base URL default port`() async {
        let config = WorkerConfig(
            serverAddress: "host.local:9090",
            httpPort: 0, // should reuse the server address port
            useHTTPS: false,
        )
        let client = MiniControlAPIClient(
            config: config,
            logger: .init(label: "test"),
        )
        let baseURL = await client.getBaseURL()
        #expect(baseURL == "http://host.local:9090")
    }

    @Test func `client base URL preserves IPv6 host`() throws {
        let config = WorkerConfig(
            serverAddress: "[2001:db8::10]:9090",
            httpPort: 8443,
            useHTTPS: true,
        )

        let baseURL = try config.httpBaseURL()
        #expect(baseURL.absoluteString == "https://[2001:db8::10]:8443")
    }

    @Test func `health check URL uses configured HTTP port`() throws {
        let config = WorkerConfig(
            serverAddress: "worker.example.com:9090",
            httpPort: 5496,
            useHTTPS: true,
        )

        let url = try config.healthCheckURL()
        #expect(url.absoluteString == "https://worker.example.com:5496/api/v1/health")
    }
}
