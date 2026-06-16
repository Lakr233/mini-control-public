import { Button, Text } from "@cloudflare/kumo";
import {
  DesktopTowerIcon,
  MonitorIcon,
  TerminalWindowIcon,
} from "@phosphor-icons/react";
import type { MachineSummary } from "../lib/machines";

export type WorkspaceView = "screen" | "terminal";

export type WorkstationAction = "claim" | "release";

interface WorkspaceHeaderProps {
  title: string;
  subtitle: string;
  machines: MachineSummary[];
  selectedMachineID?: string;
  activeView: WorkspaceView;
  showViewControls: boolean;
  canClaim: boolean;
  canReleaseSelected: boolean;
  pendingAction?: WorkstationAction;
  onSelectMachine: (machineID: string) => void;
  onSelectView: (view: WorkspaceView) => void;
  onClaim: () => void;
  onRelease: () => void;
}

export function WorkspaceHeader({
  title,
  subtitle,
  machines,
  selectedMachineID,
  activeView,
  showViewControls,
  canClaim,
  canReleaseSelected,
  pendingAction,
  onSelectMachine,
  onSelectView,
  onClaim,
  onRelease,
}: WorkspaceHeaderProps) {
  return (
    <header className="flex h-16 shrink-0 items-center justify-between gap-4 border-b border-kumo-line bg-kumo-elevated px-5">
      <div className="flex min-w-0 items-center gap-4">
        <div className="flex size-9 shrink-0 items-center justify-center rounded-md bg-kumo-brand text-kumo-inverse">
          <DesktopTowerIcon size={16} weight="fill" />
        </div>

        <div className="min-w-0">
          <div className="truncate">
            <Text variant="heading3" as="h1">
              {title}
            </Text>
          </div>
          <div className="truncate">
            <Text variant="secondary" size="xs" as="div">
              {subtitle}
            </Text>
          </div>
        </div>

        {machines.length > 0 ? (
          <div className="min-w-0 max-w-[420px] overflow-x-auto">
            <div className="flex min-w-max items-center gap-1.5 rounded-md bg-kumo-recessed p-1.5">
              {machines.map((machine) => {
                const isActive = machine.id === selectedMachineID;

                return (
                  <Button
                    key={machine.id}
                    variant={isActive ? "secondary" : "ghost"}
                    size="sm"
                    onClick={() => onSelectMachine(machine.id)}
                  >
                    {isActive ? machine.label : machine.number}
                  </Button>
                );
              })}
            </div>
          </div>
        ) : null}
      </div>

      <div className="flex shrink-0 items-center gap-3">
        {showViewControls ? (
          <div className="flex items-center gap-1.5 rounded-md bg-kumo-recessed p-1.5">
            <Button
              variant={activeView === "terminal" ? "secondary" : "ghost"}
              size="sm"
              icon={TerminalWindowIcon}
              onClick={() => onSelectView("terminal")}
            >
              Terminal
            </Button>
            <Button
              variant={activeView === "screen" ? "secondary" : "ghost"}
              size="sm"
              icon={MonitorIcon}
              onClick={() => onSelectView("screen")}
            >
              Screen
            </Button>
          </div>
        ) : null}

        {canClaim ? (
          <Button
            variant="secondary"
            size="sm"
            loading={pendingAction === "claim"}
            onClick={onClaim}
          >
            Claim
          </Button>
        ) : null}

        {canReleaseSelected ? (
          <Button
            variant="destructive"
            size="sm"
            loading={pendingAction === "release"}
            onClick={onRelease}
          >
            Release
          </Button>
        ) : null}
      </div>
    </header>
  );
}
