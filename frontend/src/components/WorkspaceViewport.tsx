import { lazy, Suspense, useEffect, useState } from "react";
import { Surface } from "@cloudflare/kumo";
import type { WorkspaceView } from "./WorkspaceHeader";

interface WorkspaceViewportProps {
  activeView: WorkspaceView;
  workstationID?: string;
}

const RemoteDesktop = lazy(() =>
  import("./RemoteDesktop").then((module) => ({
    default: module.RemoteDesktop,
  })),
);

const RemoteTerminal = lazy(() =>
  import("./RemoteTerminal").then((module) => ({
    default: module.RemoteTerminal,
  })),
);

export function WorkspaceViewport({
  activeView,
  workstationID,
}: WorkspaceViewportProps) {
  const [loadedViews, setLoadedViews] = useState<Record<WorkspaceView, boolean>>(
    () => ({
      screen: activeView === "screen",
      terminal: activeView === "terminal",
    }),
  );

  useEffect(() => {
    setLoadedViews((current) => {
      if (current[activeView]) {
        return current;
      }
      return { ...current, [activeView]: true };
    });
  }, [activeView]);

  return (
    <Surface className="relative min-h-0 flex-1 overflow-hidden rounded-lg border border-kumo-line">
      <div
        className={
          activeView === "screen"
            ? "absolute inset-0 z-10"
            : "pointer-events-none absolute inset-0 opacity-0"
        }
      >
        {loadedViews.screen ? (
          <Suspense fallback={<div className="desktop-frame min-h-0 flex-1" />}>
            <RemoteDesktop workstationID={workstationID} />
          </Suspense>
        ) : null}
      </div>
      <div
        className={
          activeView === "terminal"
            ? "absolute inset-0 z-10"
            : "pointer-events-none absolute inset-0 opacity-0"
        }
      >
        {loadedViews.terminal ? (
          <Suspense fallback={<div className="terminal-shell min-h-0 h-full w-full flex-1" />}>
            <RemoteTerminal workstationID={workstationID} />
          </Suspense>
        ) : null}
      </div>
    </Surface>
  );
}
