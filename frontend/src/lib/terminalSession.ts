const terminalSessionStorageKeyPrefix = "mini-control.terminal-session.";

function storageKey(workstationID: string) {
  return `${terminalSessionStorageKeyPrefix}${workstationID}`;
}

export function loadTerminalSessionID(workstationID?: string) {
  if (!workstationID || typeof window === "undefined") {
    return null;
  }

  try {
    return window.sessionStorage.getItem(storageKey(workstationID));
  } catch {
    return null;
  }
}

export function saveTerminalSessionID(workstationID: string, sessionID: string) {
  if (typeof window === "undefined") {
    return;
  }

  try {
    window.sessionStorage.setItem(storageKey(workstationID), sessionID);
  } catch {
    // Ignore storage failures and keep the terminal usable.
  }
}

export function clearTerminalSessionID(workstationID?: string) {
  if (!workstationID || typeof window === "undefined") {
    return;
  }

  try {
    window.sessionStorage.removeItem(storageKey(workstationID));
  } catch {
    // Ignore storage failures and keep the terminal usable.
  }
}
