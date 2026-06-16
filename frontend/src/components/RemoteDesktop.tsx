import { useEffect, useRef, useState } from "react";
import { Empty, Surface } from "@cloudflare/kumo";
import { MonitorIcon } from "@phosphor-icons/react";
import { VncScreen, type VncScreenHandle } from "react-vnc";
import {
  buildProxyWebSocketURL,
  createBrowserProxySession,
} from "../lib/api";
import { DEFAULT_VM_VNC_CREDENTIALS } from "../lib/vnc";

const VNC_PORT = 5900;

interface RemoteDesktopProps {
  workstationID?: string;
}

export function RemoteDesktop({ workstationID }: RemoteDesktopProps) {
  const [sessionURL, setSessionURL] = useState<string>();
  const [loadFailed, setLoadFailed] = useState(false);
  const [clipboardActive, setClipboardActive] = useState(false);
  const containerRef = useRef<HTMLDivElement | null>(null);
  const screenRef = useRef<VncScreenHandle | null>(null);

  useEffect(() => {
    if (!workstationID) {
      setSessionURL(undefined);
      setLoadFailed(false);
      setClipboardActive(false);
      return;
    }

    let active = true;

    const createSession = async () => {
      setLoadFailed(false);
      setSessionURL(undefined);
      const session = await createBrowserProxySession({
        target: "vm",
        vm_id: workstationID,
        port: VNC_PORT,
      });
      if (!active) {
        return;
      }
      setSessionURL(buildProxyWebSocketURL(session));
    };

    createSession().catch(() => {
      if (!active) {
        return;
      }
      setLoadFailed(true);
    });

    return () => {
      active = false;
    };
  }, [workstationID]);

  useEffect(() => {
    if (!clipboardActive) {
      return;
    }

    const handleWindowBlur = () => {
      setClipboardActive(false);
    };

    const handleDocumentPointerDown = (event: PointerEvent) => {
      const target = event.target;
      if (target instanceof Node && containerRef.current?.contains(target)) {
        return;
      }
      setClipboardActive(false);
    };

    const handleDocumentPaste = (event: ClipboardEvent) => {
      const text = event.clipboardData?.getData("text/plain");
      if (!text) {
        return;
      }

      event.preventDefault();
      screenRef.current?.clipboardPaste(text);
    };

    window.addEventListener("blur", handleWindowBlur);
    document.addEventListener("pointerdown", handleDocumentPointerDown, true);
    document.addEventListener("paste", handleDocumentPaste, true);

    return () => {
      window.removeEventListener("blur", handleWindowBlur);
      document.removeEventListener(
        "pointerdown",
        handleDocumentPointerDown,
        true,
      );
      document.removeEventListener("paste", handleDocumentPaste, true);
    };
  }, [clipboardActive]);

  const markClipboardActive = () => {
    setClipboardActive(true);
  };

  const handleRemoteClipboard = async (event: CustomEvent<{ text: string }>) => {
    const text = event.detail.text;

    if (
      !clipboardActive ||
      !window.isSecureContext ||
      !navigator.clipboard?.writeText
    ) {
      return;
    }

    try {
      await navigator.clipboard.writeText(text);
    } catch {
      // Ignore clipboard permission failures and keep the VNC session usable.
    }
  };

  if (!workstationID) {
    return (
      <Surface className="flex h-full min-h-[560px] items-center justify-center rounded-lg bg-kumo-overlay p-6">
        <Empty
          icon={<MonitorIcon size={42} className="text-kumo-inactive" />}
          title="Workstation unavailable"
          description="Start your workstation to open a desktop session."
        />
      </Surface>
    );
  }

  if (loadFailed) {
    return (
      <Surface className="flex h-full min-h-[560px] items-center justify-center rounded-lg bg-kumo-overlay p-6">
        <Empty
          icon={<MonitorIcon size={42} className="text-kumo-inactive" />}
          title="Desktop unavailable"
          description="Failed to prepare the desktop session."
        />
      </Surface>
    );
  }

  if (!sessionURL) {
    return <div className="desktop-frame min-h-0 flex-1" />;
  }

  return (
    <div
      ref={containerRef}
      className="desktop-frame min-h-0 flex-1"
      onPointerDownCapture={markClipboardActive}
      onFocusCapture={markClipboardActive}
    >
      <VncScreen
        key={sessionURL}
        ref={screenRef}
        url={sessionURL}
        className="min-h-0 flex-1"
        style={{ width: "100%", height: "100%" }}
        scaleViewport
        resizeSession
        focusOnClick
        background="#0f0f10"
        retryDuration={0}
        onClipboard={handleRemoteClipboard}
        rfbOptions={{
          credentials: { ...DEFAULT_VM_VNC_CREDENTIALS },
        }}
      />
    </div>
  );
}
