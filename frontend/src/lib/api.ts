export interface Member {
  id: string;
  access_sub: string;
  email: string;
  display_name: string;
  role: string;
  created_at: string;
  updated_at: string;
  last_seen_at: string;
}

export interface VMInfo {
  id: string;
  worker_id: string;
  state: string;
  ip_address?: string;
  state_since: string;
  created_at: string;
}

export interface WorkerInfo {
  id: string;
  hostname: string;
  hardware_uuid: string;
  cpu_cores: number;
  memory_bytes: number;
  tart_version: string;
  worker_version: string;
  pool_size: number;
  base_image: string;
  status: string;
  registered_at: string;
  last_heartbeat?: string;
  vms: VMInfo[];
}

export interface ProxySession {
  session_id: string;
  token: string;
}

export type WorkstationPowerState =
  | "starting"
  | "running"
  | "stopping"
  | "stopped"
  | "deleting"
  | "deleted"
  | "error";

export type WorkstationDesiredPowerState = "running" | "stopped" | "deleted";

export interface WorkstationInfo {
  id: string;
  member_id: string;
  worker_id: string;
  slot: number;
  vm_name: string;
  power_state: WorkstationPowerState;
  desired_power_state: WorkstationDesiredPowerState;
  ip_address: string;
  last_error: string;
  created_at: string;
  updated_at: string;
}

export interface CreateProxySessionRequest {
  target: "vm" | "host";
  vm_id?: string;
  worker_id?: string;
  port: number;
}

export class APIError extends Error {
  status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "APIError";
    this.status = status;
  }
}

async function request<T>(input: string, init?: RequestInit): Promise<T> {
  const response = await fetch(input, {
    credentials: "include",
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers ?? {}),
    },
  });

  const text = await response.text();

  if (!response.ok) {
    let message = text || `Request failed with status ${response.status}`;
    if (text) {
      try {
        const parsed = JSON.parse(text) as { error?: string; message?: string };
        message = parsed.error ?? parsed.message ?? message;
      } catch {
        // Keep the plain response body as the error message.
      }
    }
    throw new APIError(response.status, message);
  }

  if (text.length === 0) {
    return undefined as T;
  }

  return JSON.parse(text) as T;
}

export function getCurrentMember() {
  return request<Member>("/api/v1/me");
}

export function getMyWorkstations() {
  return request<WorkstationInfo[]>("/api/v1/my-workstations");
}

export function claimMyWorkstation(workerID?: string) {
  return request<WorkstationInfo>("/api/v1/my-workstation/claim", {
    method: "POST",
    body: JSON.stringify(workerID ? { worker_id: workerID } : {}),
  });
}

export function releaseMyWorkstation(workstationID?: string) {
  return request<WorkstationInfo>("/api/v1/my-workstation/release", {
    method: "POST",
    body: JSON.stringify(workstationID ? { workstation_id: workstationID } : {}),
  });
}

export function listBrowserWorkers() {
  return request<WorkerInfo[]>("/api/v1/browser/workers");
}

export function createBrowserProxySession(payload: CreateProxySessionRequest) {
  return request<ProxySession>("/api/v1/browser/proxy-sessions", {
    method: "POST",
    body: JSON.stringify(payload),
  });
}

export function buildProxyWebSocketURL(session: ProxySession) {
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${protocol}//${window.location.host}/api/v1/proxy/ws?session=${encodeURIComponent(
    session.session_id,
  )}&role=client&token=${encodeURIComponent(session.token)}`;
}

export function buildTerminalWebSocketURL(
  cols: number,
  rows: number,
  sessionID?: string,
  workstationID?: string,
) {
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  const params = new URLSearchParams({
    cols: String(cols),
    rows: String(rows),
  });
  if (sessionID) {
    params.set("session", sessionID);
  }
  if (workstationID) {
    params.set("workstation_id", workstationID);
  }
  return `${protocol}//${window.location.host}/api/v1/browser/terminal/ws?${params.toString()}`;
}
