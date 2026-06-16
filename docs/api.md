# Mini-Control REST API

Base URL: `https://<server>`

## Authentication

Most endpoints require a Bearer token in the `Authorization` header:

```
Authorization: Bearer <admin_token | worker_token>
```

Token comparison uses constant-time algorithm to prevent timing attacks.

Browser-facing member endpoints are authenticated with Cloudflare Access. The origin expects the Access JWT in `Cf-Access-Jwt-Assertion` and validates it against the configured Access audience and issuer.

The browser frontend lives under `frontend/` and uses `pnpm`:

```bash
cd frontend
pnpm install
pnpm dev
pnpm build
```

---

## Public Endpoints

### `GET /api/v1/health`

Health check.

**Response** `200`

```json
{ "status": "ok" }
```

---

## Member Endpoints

### `GET /api/v1/me`

Returns the currently authenticated member from Cloudflare Access.

**Response** `200`

```json
{
  "id": "2b2f8a8f-...",
  "access_sub": "7335d417-61da-459d-899c-0a01c76a2f94",
  "email": "user@example.com",
  "display_name": "user",
  "role": "member",
  "created_at": "2026-03-24T07:10:00Z",
  "updated_at": "2026-03-24T07:10:00Z",
  "last_seen_at": "2026-03-24T07:10:00Z"
}
```

---

### `GET /api/v1/browser/workers`

Returns workers and their reported VMs for the authenticated browser session.

This is the browser-safe equivalent of the old admin inventory call and is intended for the React frontend.

**Response** `200`

```json
[
  {
    "id": "w-abc123",
    "hostname": "mini-01",
    "status": "active",
    "cpu_cores": 10,
    "memory_bytes": 549755813888,
    "vms": [
      {
        "id": "vm-123",
        "worker_id": "w-abc123",
        "state": "ready",
        "ip_address": "192.168.64.5",
        "state_since": "2026-03-24T07:10:00Z",
        "created_at": "2026-03-24T07:05:00Z"
      }
    ]
  }
]
```

---

### `GET /api/v1/my-workstation`

Returns the currently assigned dedicated workstation for the authenticated member.

If the workstation exists but is stopped or errored, the server will mark it to run again before returning it.

**Response** `200`

```json
{
  "id": "ws-123",
  "member_id": "2b2f8a8f-...",
  "worker_id": "w-abc123",
  "slot": 0,
  "vm_name": "ws-abc123def456",
  "power_state": "running",
  "desired_power_state": "running",
  "ip_address": "192.168.64.5",
  "last_error": "",
  "created_at": "2026-03-24T07:05:00Z",
  "updated_at": "2026-03-24T07:10:00Z"
}
```

| Status | Body                                 |
| ------ | ------------------------------------ |
| `404`  | `{"error": "workstation not found"}` |

---

### `POST /api/v1/my-workstation/claim`

Claims a workstation for the authenticated member.

If the member already has a workstation:
- with no request body, the existing workstation is resumed/returned
- with `worker_id`, the workstation is moved to that target worker if it has capacity

**Request**

```json
{
  "worker_id": "w-abc123"
}
```

`worker_id` is optional.

**Response** `200` or `201`

```json
{
  "id": "ws-123",
  "member_id": "2b2f8a8f-...",
  "worker_id": "w-abc123",
  "slot": 1,
  "vm_name": "ws-abc123def456",
  "power_state": "starting",
  "desired_power_state": "running",
  "ip_address": "",
  "last_error": "",
  "created_at": "2026-03-24T07:05:00Z",
  "updated_at": "2026-03-24T07:10:00Z"
}
```

| Status | Body                                                      |
| ------ | --------------------------------------------------------- |
| `409`  | `{"error": "target worker is not available"}`             |
| `409`  | `{"error": "no workstation slots available on target worker"}` |
| `409`  | `{"error": "no workstation slots available"}`             |
| `409`  | `{"error": "workstation is being deleted"}`               |

---

### `POST /api/v1/my-workstation/release`

Marks the member workstation for deletion.

**Response** `200`

---

### `POST /api/v1/browser/proxy-sessions`

Creates a browser-initiated proxy session for either `5900` or `22`.

**Request**

```json
{
  "target": "vm",
  "vm_id": "vm-123",
  "port": 5900
}
```

**Response** `201`

```json
{
  "session_id": "9e4b2f10-...",
  "token": "6c7c4f..."
}
```

The browser then connects to:

```text
/api/v1/proxy/ws?session=<session_id>&role=client&token=<token>
```

---

### `GET /api/v1/browser/terminal/ws`

WebSocket endpoint for browser terminal sessions. This endpoint is authenticated by Cloudflare Access and then bridges SSH on the server side, so the browser only needs `xterm.js`.

**Query params**

| Param       | Required          | Description                      |
| ----------- | ----------------- | -------------------------------- |
| `target`    | no                | `vm` or `host`, defaults to `vm` |
| `vm_id`     | for `target=vm`   | VM identifier                    |
| `worker_id` | for `target=host` | Worker identifier                |
| `cols`      | no                | Initial terminal width           |
| `rows`      | no                | Initial terminal height          |

**Client -> server messages**

```json
{ "type": "input", "data": "ls -la\r" }
{ "type": "resize", "cols": 132, "rows": 40 }
```

**Server -> client messages**

- Binary frames: terminal output bytes
- Text frames:

```json
{ "type": "ready" }
{ "type": "error", "message": "SSH session could not be established." }
```

---

### `GET /install.sh`

Returns the worker setup script as plain text. The script only injects the server
address at serve time; first-time enrollment expects the token to already exist in
`~/mini-control-enroll.key` on the Mac mini (or to be passed via `--token`).

**Response** `200` `text/x-shellscript`

---

### `GET /api/v1/releases/latest`

Returns metadata for the most recently uploaded release.

**Response** `200`

```json
{
  "id": "8306d3eb-...",
  "version": "0.1.0",
  "sha256": "074c4368...",
  "size_bytes": 37443040,
  "signing_identity": "(team: TEAMID, id: MiniControlWorker)",
  "uploaded_at": "2026-03-17T07:45:42Z"
}
```

| Status | Body                                 |
| ------ | ------------------------------------ |
| `404`  | `{"error": "no releases available"}` |

---

### `GET /api/v1/releases/download/{version}`

Downloads a release binary.

**Path params:** `version` — must match `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`

**Response** `200` `application/octet-stream`

Response headers:

| Header                | Example                                   |
| --------------------- | ----------------------------------------- |
| `Content-Disposition` | `attachment; filename=minicontrol-worker` |
| `X-Release-SHA256`    | `074c4368...`                             |
| `X-Release-Version`   | `0.1.0`                                   |

| Status | Body                             |
| ------ | -------------------------------- |
| `400`  | `{"error": "invalid version"}`   |
| `404`  | `{"error": "version not found"}` |

---

## Release Management

### `PUT /api/v1/releases/upload/{version}`

Upload a signed worker binary as a raw request body. Requires auth.

**Content-Type:** `application/octet-stream`

**Path params:** `version` — must match `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`

Server validates the Mach-O code signature against the configured `required_signing_identity`. If the identity is empty, signature validation is skipped.

**Response** `201`

```json
{
  "id": "...",
  "version": "0.1.0",
  "sha256": "074c4368...",
  "size_bytes": 37443040,
  "signing_identity": "(team: TEAMID, id: MiniControlWorker)",
  "uploaded_at": "2026-03-17T07:45:42Z"
}
```

| Status | Body                                             |
| ------ | ------------------------------------------------ |
| `400`  | `{"error": "invalid version"}`                   |
| `400`  | `{"error": "binary body is required"}`           |
| `400`  | `{"error": "<signature validation error>"}`      |
| `409`  | `{"error": "release version already exists"}`    |
| `503`  | `{"error": "release management not configured"}` |

---

## Workers

### `POST /api/v1/workers/register`

Register a worker with the server. Registration uses the request-body token and accepts either:

- `enrollment_token` for first install / re-enrollment
- an issued per-worker `worker_token` for normal restarts

**Request**

```json
{
  "hostname": "mac-1",
  "hardware_uuid": "XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX",
  "cpu_cores": 10,
  "memory_bytes": 17179869184,
  "tart_version": "2.31.0",
  "worker_version": "0.1.0",
  "worker_token": "<token>"
}
```

**Response** `200`

```json
{
  "worker_id": "mac-1-abc123",
  "pool_size": 2,
  "base_image": "minictl-tahoe-base",
  "worker_token": "wkt_..."
}
```

| Status | Body                         |
| ------ | ---------------------------- |
| `401`  | `{"error": "invalid token"}` |

If registration used `enrollment_token`, the server issues a new per-worker `worker_token` in the response.

---

### `GET /api/v1/workers`

List all workers and their VMs. Requires auth.

**Response** `200`

```json
[
  {
    "id": "mac-1-abc123",
    "hostname": "mac-1",
    "hardware_uuid": "...",
    "cpu_cores": 10,
    "memory_bytes": 17179869184,
    "tart_version": "2.31.0",
    "worker_version": "0.1.0",
    "pool_size": 2,
    "base_image": "minictl-tahoe-base",
    "status": "active",
    "registered_at": "2026-03-17T07:00:00Z",
    "last_heartbeat": "2026-03-17T07:50:00Z",
    "vms": [
      {
        "id": "vm-xxxx",
        "worker_id": "mac-1-abc123",
        "state": "ready",
        "ip_address": "192.168.64.5",
        "state_since": "2026-03-17T07:10:00Z",
        "created_at": "2026-03-17T07:05:00Z"
      }
    ]
  }
]
```

---

### `POST /api/v1/workers/{id}/heartbeat`

Worker heartbeat. Updates VM states. Requires auth.

**Request**

```json
{
  "vm_statuses": [
    {
      "vm_id": "vm-xxxx",
      "state": "ready",
      "ip_address": "192.168.64.5"
    }
  ],
  "lan_address": "192.168.1.100"
}
```

**Response** `200`

```json
{
  "status": "ok"
}
```

---

## VMs

### `PUT /api/v1/vms/{id}/state`

Update VM state. Requires auth. If `state` is `"destroying"`, the VM record is deleted.

**Request**

```json
{
  "worker_id": "mac-1-abc123",
  "state": "ready",
  "ip_address": "192.168.64.5",
  "error_message": ""
}
```

**Response** `200`

```json
{ "status": "ok" }
```

---

## Proxy

### `POST /api/v1/proxy/sessions`

Create a TCP proxy session (e.g. for VNC forwarding). Requires auth.

**Request**

```json
{
  "target": "vm",
  "vm_id": "vm-xxxx",
  "port": 5900
}
```

For host proxy:

```json
{
  "target": "host",
  "worker_id": "mac-1-abc123",
  "port": 22
}
```

`target` defaults to `"vm"` if omitted.

**Response** `201`

```json
{
  "session_id": "sess-xxxx",
  "token": "<session-token>"
}
```

| Status | Body                                                 |
| ------ | ---------------------------------------------------- |
| `400`  | `{"error": "port is required"}`                      |
| `400`  | `{"error": "vm_id is required for vm target"}`       |
| `400`  | `{"error": "worker_id is required for host target"}` |
| `403`  | `{"error": "port not allowed"}`                      |
| `404`  | `{"error": "vm not found"}`                          |
| `409`  | `{"error": "vm not available"}`                      |

---

### `GET /api/v1/proxy/sessions/pending`

Get pending proxy sessions for a worker. Requires auth.

**Query params:** `worker_id` (required)

**Response** `200`

```json
[
  {
    "session_id": "sess-xxxx",
    "target": "vm",
    "vm_id": "vm-xxxx",
    "port": 5900,
    "token": "<session-token>"
  }
]
```

---

### `GET /api/v1/proxy/ws`

WebSocket endpoint for proxy data relay. Auth via query parameter.

**Query params:**

| Param     | Required | Values               |
| --------- | -------- | -------------------- |
| `session` | yes      | Session ID           |
| `role`    | yes      | `client` or `worker` |
| `token`   | yes      | Session token        |

Upgrades to WebSocket. Binary frames are relayed between client and worker.

---

## Hostname

### `POST /api/v1/hostname/assign`

Assign a sequential hostname (`mac-N`) to a worker. Requires auth.

**Request**

```json
{
  "hardware_uuid": "XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX"
}
```

**Response** `200`

```json
{
  "hostname": "mac-1"
}
```

---

## Enums

### Worker Status

`active` | `draining` | `offline`

### VM State

`starting` | `bootstrapping` | `ready` | `busy` | `destroying` | `failed`
