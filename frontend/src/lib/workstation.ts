import type { WorkstationInfo, WorkstationPowerState } from "./api";

const transitionalStates = new Set<WorkstationPowerState>([
  "starting",
  "stopping",
  "deleting",
]);

export function shouldPollWorkstation(workstation: WorkstationInfo) {
  return (
    workstation.power_state !== workstation.desired_power_state ||
    transitionalStates.has(workstation.power_state)
  );
}

export function describeWorkstationState(workstation: WorkstationInfo) {
  switch (workstation.power_state) {
    case "starting":
      return "Starting workstation. Preparing browser access.";
    case "stopped":
      return "Stopped workstation. Starting it again.";
    case "stopping":
      return "Stopping workstation. Waiting for the transition to finish.";
    case "deleting":
      return "Releasing workstation. Waiting for cleanup.";
    case "error":
      return workstation.last_error
        ? `Workstation error. ${workstation.last_error}`
        : "Workstation error. Retrying automatically.";
    default:
      return "Workstation is not ready yet.";
  }
}
