import { Empty, Surface } from "@cloudflare/kumo";
import { DesktopTowerIcon } from "@phosphor-icons/react";

interface WorkspaceStatePanelProps {
  message: string;
}

export function WorkspaceStatePanel({ message }: WorkspaceStatePanelProps) {
  return (
    <Surface className="flex min-h-[440px] flex-1 items-center justify-center rounded-lg p-6">
      <div className="flex w-full max-w-[560px] flex-col items-center gap-4">
        <Empty
          icon={<DesktopTowerIcon size={42} className="text-kumo-inactive" />}
          title={message}
        />
      </div>
    </Surface>
  );
}
