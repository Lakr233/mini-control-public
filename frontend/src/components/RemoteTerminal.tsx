import { useEffect, useRef } from "react";
import { Empty, Surface } from "@cloudflare/kumo";
import { TerminalWindowIcon } from "@phosphor-icons/react";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import { buildTerminalWebSocketURL } from "../lib/api";
import {
  clearTerminalSessionID,
  loadTerminalSessionID,
  saveTerminalSessionID,
} from "../lib/terminalSession";

const RECONNECT_BASE_DELAY_MS = 1000;
const RECONNECT_MAX_DELAY_MS = 5000;
const SESSION_REJECTED_CLOSE_CODE = 1008;

const TERMINAL_THEME = {
  background: "#121212",
  foreground: "#f6f3ed",
  cursor: "#f97316",
  selectionBackground: "rgba(249,115,22,0.28)",
  black: "#121212",
  red: "#ef4444",
  green: "#22c55e",
  yellow: "#f59e0b",
  blue: "#60a5fa",
  magenta: "#f472b6",
  cyan: "#2dd4bf",
  white: "#f6f3ed",
  brightBlack: "#78716c",
  brightRed: "#f87171",
  brightGreen: "#4ade80",
  brightYellow: "#fbbf24",
  brightBlue: "#93c5fd",
  brightMagenta: "#f9a8d4",
  brightCyan: "#5eead4",
  brightWhite: "#ffffff",
} as const;

interface RemoteTerminalProps {
  workstationID?: string;
}

type ControlMessage =
  | { type: "ready"; session_id?: string }
  | { type: "error"; message?: string };

export function RemoteTerminal({ workstationID }: RemoteTerminalProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const sessionIDRef = useRef<string | null>(null);

  useEffect(() => {
    if (!containerRef.current || !workstationID) {
      return;
    }

    sessionIDRef.current = loadTerminalSessionID(workstationID);

    const terminal = new Terminal({
      cursorBlink: true,
      fontFamily: '"Iosevka Term", "SFMono-Regular", ui-monospace, monospace',
      fontSize: 13,
      lineHeight: 1.2,
      theme: TERMINAL_THEME,
    });
    const fitAddon = new FitAddon();
    terminal.loadAddon(fitAddon);
    terminal.open(containerRef.current);

    const socketRef = { current: null as WebSocket | null };
    const reconnectTimerRef = { current: undefined as number | undefined };
    const reconnectAttemptsRef = { current: 0 };
    let disposed = false;

    const sendResize = () => {
      fitAddon.fit();
      if (socketRef.current?.readyState === WebSocket.OPEN) {
        socketRef.current.send(
          JSON.stringify({
            type: "resize",
            cols: Math.max(terminal.cols, 1),
            rows: Math.max(terminal.rows, 1),
          }),
        );
      }
    };

    const scheduleResize = () => {
      window.requestAnimationFrame(() => {
        window.requestAnimationFrame(sendResize);
      });
    };

    const resizeObserver = new ResizeObserver(scheduleResize);
    resizeObserver.observe(containerRef.current);
    window.addEventListener("resize", scheduleResize);

    const inputDisposable = terminal.onData((data) => {
      if (socketRef.current?.readyState !== WebSocket.OPEN) {
        return;
      }
      socketRef.current.send(JSON.stringify({ type: "input", data }));
    });

    const clearReconnectTimer = () => {
      if (reconnectTimerRef.current !== undefined) {
        window.clearTimeout(reconnectTimerRef.current);
        reconnectTimerRef.current = undefined;
      }
    };

    const scheduleReconnect = () => {
      if (disposed || reconnectTimerRef.current !== undefined) {
        return;
      }
      const delay = Math.min(
        RECONNECT_BASE_DELAY_MS * Math.max(1, reconnectAttemptsRef.current),
        RECONNECT_MAX_DELAY_MS,
      );
      reconnectTimerRef.current = window.setTimeout(() => {
        reconnectTimerRef.current = undefined;
        connect();
      }, delay);
    };

    const handleMessage = (event: MessageEvent) => {
      if (typeof event.data === "string") {
        const message = JSON.parse(event.data) as ControlMessage;
        if (message.type === "ready") {
          if (message.session_id) {
            sessionIDRef.current = message.session_id;
            saveTerminalSessionID(workstationID, message.session_id);
          }
          return;
        }
        if (message.type === "error") {
          terminal.writeln(
            `\r\n[mini-control] ${message.message ?? "Terminal error"}\r`,
          );
        }
        return;
      }

      void Promise.resolve(event.data)
        .then((data) => {
          if (data instanceof ArrayBuffer) {
            terminal.write(new Uint8Array(data));
          } else if (data instanceof Blob) {
            return data.arrayBuffer().then((buffer) => {
              terminal.write(new Uint8Array(buffer));
            });
          }
          return undefined;
        })
        .catch(() => {});
    };

    const connect = () => {
      if (disposed) {
        return;
      }

      const attemptedSessionID = sessionIDRef.current;
      const socket = new WebSocket(
        buildTerminalWebSocketURL(
          Math.max(terminal.cols, 1),
          Math.max(terminal.rows, 1),
          attemptedSessionID ?? undefined,
          workstationID,
        ),
      );
      socket.binaryType = "arraybuffer";
      socketRef.current = socket;

      socket.onopen = () => {
        reconnectAttemptsRef.current = 0;
        scheduleResize();
      };
      socket.onmessage = handleMessage;
      socket.onclose = (event) => {
        if (socketRef.current === socket) {
          socketRef.current = null;
        }
        if (disposed) {
          return;
        }

        if (attemptedSessionID && event.code === SESSION_REJECTED_CLOSE_CODE) {
          sessionIDRef.current = null;
          clearTerminalSessionID(workstationID);
          reconnectAttemptsRef.current = 0;
          scheduleReconnect();
          return;
        }

        reconnectAttemptsRef.current += 1;
        scheduleReconnect();
      };
      socket.onerror = () => {
        socket.close();
      };
    };

    connect();

    return () => {
      disposed = true;
      clearReconnectTimer();
      inputDisposable.dispose();
      resizeObserver.disconnect();
      window.removeEventListener("resize", scheduleResize);
      socketRef.current?.close();
      terminal.dispose();
    };
  }, [workstationID]);

  if (!workstationID) {
    return (
      <Surface className="flex h-full min-h-[560px] items-center justify-center rounded-lg bg-kumo-overlay p-6">
        <Empty
          icon={<TerminalWindowIcon size={42} className="text-kumo-inactive" />}
          title="Workstation unavailable"
          description="Start your workstation to open the browser shell."
        />
      </Surface>
    );
  }

  return (
    <div
      className="terminal-shell min-h-0 h-full w-full flex-1"
      ref={containerRef}
    />
  );
}
