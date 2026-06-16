export interface VNCCredentials {
  username: string;
  password: string;
}

export const DEFAULT_VM_VNC_CREDENTIALS: VNCCredentials = {
  username: "admin",
  password: "admin",
};
