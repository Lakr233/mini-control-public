import { useEffect, useState } from "react";
import { Banner } from "@cloudflare/kumo";
import { WarningCircleIcon } from "@phosphor-icons/react";
import {
  WorkspaceHeader,
  type WorkspaceView,
  type WorkstationAction,
} from "./components/WorkspaceHeader";
import { WorkspaceStatePanel } from "./components/WorkspaceStatePanel";
import { WorkspaceViewport } from "./components/WorkspaceViewport";
import {
  claimMyWorkstation,
  getCurrentMember,
  getMyWorkstations,
  listBrowserWorkers,
  releaseMyWorkstation,
  type Member,
  type WorkstationInfo,
  type WorkerInfo,
} from "./lib/api";
import { buildMachineSummaries } from "./lib/machines";
import {
  describeWorkstationState,
  shouldPollWorkstation,
} from "./lib/workstation";

const workspaceViewStorageKey = "mini-control.workspace-view";

export default function App() {
  const [member, setMember] = useState<Member | null>(null);
  const [workstations, setWorkstations] = useState<WorkstationInfo[]>([]);
  const [workers, setWorkers] = useState<WorkerInfo[]>([]);
  const [selectedMachineID, setSelectedMachineID] = useState<string>();
  const [activeView, setActiveView] = useState<WorkspaceView>(() =>
    loadStoredWorkspaceView(),
  );
  const [loading, setLoading] = useState(true);
  const [pendingAction, setPendingAction] = useState<WorkstationAction>();
  const [error, setError] = useState<string>();

  useEffect(() => {
    void refresh({ initial: true });
  }, []);

  useEffect(() => {
    saveWorkspaceView(activeView);
  }, [activeView]);

  const machines = buildMachineSummaries(workstations);
  const machineIDs = machines.map((machine) => machine.id).join("|");
  const pollingSignature = workstations
    .map((workstation) =>
      [
        workstation.id,
        workstation.power_state,
        workstation.desired_power_state,
      ].join(":"),
    )
    .join("|");
  const selectedMachine =
    machines.find((machine) => machine.id === selectedMachineID) ?? machines[0];
  const selectedWorkstation = selectedMachine?.workstation;

  useEffect(() => {
    if (
      selectedMachineID &&
      machines.some((machine) => machine.id === selectedMachineID)
    ) {
      return;
    }

    const preferredMachineID = workstations[0]?.id ?? machines[0]?.id;
    if (preferredMachineID && preferredMachineID !== selectedMachineID) {
      setSelectedMachineID(preferredMachineID);
    }
  }, [machineIDs, selectedMachineID, workstations]);

  useEffect(() => {
    if (!workstations.some((workstation) => shouldPollWorkstation(workstation))) {
      return;
    }

    const intervalID = window.setInterval(() => {
      void refresh();
    }, 4000);

    return () => {
      window.clearInterval(intervalID);
    };
  }, [pollingSignature, workstations]);

  const initialPending =
    loading && member === null && workstations.length === 0 && workers.length === 0 && !error;
  const isRunning = selectedWorkstation?.power_state === "running";
  const subtitle = buildHeaderSubtitle(member, workstations.length, initialPending);

  async function refresh(options?: { initial?: boolean }) {
    try {
      if (options?.initial) {
        setLoading(true);
      }
      setError(undefined);

      const [memberResult, workstationsResult, workersResult] =
        await Promise.allSettled([
          getCurrentMember(),
          getMyWorkstations(),
          listBrowserWorkers(),
        ]);

      if (memberResult.status === "fulfilled") {
        setMember(memberResult.value);
      }

      if (workstationsResult.status === "fulfilled") {
        setWorkstations(workstationsResult.value);
      }

      if (workersResult.status === "fulfilled") {
        setWorkers(workersResult.value);
      }

      const nextErrors = [
        workstationsResult.status === "rejected"
          ? toErrorMessage(
              workstationsResult.reason,
              "Failed to load workstations",
            )
          : undefined,
        workersResult.status === "rejected"
          ? toErrorMessage(
              workersResult.reason,
              "Failed to load machine inventory",
            )
          : undefined,
      ].filter(Boolean);
      setError(nextErrors.length > 0 ? nextErrors.join(" / ") : undefined);
    } catch (caught) {
      setError(
        caught instanceof Error ? caught.message : "Failed to load dashboard",
      );
    } finally {
      setLoading(false);
    }
  }

  async function runAction(
    action: WorkstationAction,
    task: () => Promise<WorkstationInfo>,
  ) {
    try {
      setPendingAction(action);
      setError(undefined);
      const workstation = await task();
      setSelectedMachineID(workstation.id);
      void refresh();
    } catch (caught) {
      setError(
        caught instanceof Error ? caught.message : `Failed to ${action}`,
      );
    } finally {
      setPendingAction(undefined);
    }
  }

  function selectView(view: WorkspaceView) {
    if (document.activeElement instanceof HTMLElement) {
      document.activeElement.blur();
    }
    setActiveView(view);
  }

  return (
    <div className="flex h-screen min-h-screen flex-col overflow-hidden bg-kumo-overlay text-kumo-default">
      <WorkspaceHeader
        title="Mac Fleet"
        subtitle={subtitle}
        machines={machines}
        selectedMachineID={selectedMachine?.id}
        activeView={activeView}
        showViewControls={Boolean(selectedWorkstation && isRunning)}
        canClaim={!initialPending}
        canReleaseSelected={
          Boolean(selectedWorkstation) &&
          !initialPending &&
          selectedWorkstation?.power_state !== "deleting"
        }
        pendingAction={pendingAction}
        onSelectMachine={setSelectedMachineID}
        onSelectView={selectView}
        onClaim={() => {
          if (pendingAction) {
            return;
          }
          if (!window.confirm("Claim a new workstation?")) {
            return;
          }
          void runAction("claim", () => claimMyWorkstation());
        }}
        onRelease={() => {
          if (!selectedWorkstation || !selectedMachine || pendingAction) {
            return;
          }
          if (
            !window.confirm(
              `Release ${selectedMachine.label}? This removes the claimed workstation.`,
            )
          ) {
            return;
          }
          void runAction("release", () =>
            releaseMyWorkstation(selectedWorkstation.id),
          );
        }}
      />

      <main className="flex min-h-0 min-w-0 flex-1 flex-col gap-4 p-4">
        {error ? (
          <Banner
            variant="error"
            icon={<WarningCircleIcon weight="fill" />}
            title="Request failed"
            description={error}
          />
        ) : null}

        {initialPending ? (
          <WorkspaceStatePanel
            message="Loading your claimed machines."
          />
        ) : !selectedMachine ? (
          <WorkspaceStatePanel
            message="No claimed machines yet. Claim one to enable terminal and screen."
          />
        ) : !isRunning ? (
          <WorkspaceStatePanel
            message={describeWorkstationState(selectedWorkstation)}
          />
        ) : (
          <WorkspaceViewport
            activeView={activeView}
            workstationID={selectedWorkstation.id}
          />
        )}
      </main>
    </div>
  );
}

function buildHeaderSubtitle(
  member: Member | null,
  workstationCount: number,
  initialPending: boolean,
) {
  if (initialPending) {
    return "Loading machine inventory...";
  }

  const identity = member?.display_name || member?.email || "Unknown member";
  if (workstationCount === 0) {
    return `${identity} · no claimed workstations`;
  }

  return `${identity} · ${workstationCount} claimed ${workstationCount === 1 ? "workstation" : "workstations"}`;
}

function toErrorMessage(caught: unknown, fallback: string) {
  return caught instanceof Error ? caught.message : fallback;
}

function loadStoredWorkspaceView(): WorkspaceView {
  if (typeof window === "undefined") {
    return "screen";
  }

  try {
    const stored = window.localStorage.getItem(workspaceViewStorageKey);
    if (stored === "screen" || stored === "terminal") {
      return stored;
    }
  } catch {
    // Ignore storage failures and fall back to the default view.
  }

  return "screen";
}

function saveWorkspaceView(view: WorkspaceView) {
  if (typeof window === "undefined") {
    return;
  }

  try {
    window.localStorage.setItem(workspaceViewStorageKey, view);
  } catch {
    // Ignore storage failures and keep the app usable.
  }
}
