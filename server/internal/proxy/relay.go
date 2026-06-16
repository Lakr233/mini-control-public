package proxy

import (
	"context"
	"io"
	"log/slog"

	"github.com/coder/websocket"
)

// Relay copies binary WebSocket frames between client and worker until one side closes.
func Relay(ctx context.Context, session *ProxySession, logger *slog.Logger) {
	// Wait for both sides to connect
	select {
	case <-session.ready:
	case <-ctx.Done():
		return
	}

	session.mu.Lock()
	client := session.clientConn
	worker := session.workerConn
	session.mu.Unlock()

	if client == nil || worker == nil {
		return
	}

	logger.Info("proxy relay started", "session_id", session.ID, "vm_id", session.VMID, "port", session.Port)

	type relayResult struct {
		direction string
		err       error
	}

	errc := make(chan relayResult, 2)

	// client → worker
	go func() {
		errc <- relayResult{
			direction: "client_to_worker",
			err:       copyWS(ctx, worker, client),
		}
	}()

	// worker → client
	go func() {
		errc <- relayResult{
			direction: "worker_to_client",
			err:       copyWS(ctx, client, worker),
		}
	}()

	// Wait for first error (one side closed)
	result := <-errc

	logger.Info(
		"proxy relay ended",
		"session_id", session.ID,
		"direction", result.direction,
		"error", result.err,
	)
}

func copyWS(ctx context.Context, dst, src *websocket.Conn) error {
	for {
		msgType, reader, err := src.Reader(ctx)
		if err != nil {
			return err
		}

		writer, err := dst.Writer(ctx, msgType)
		if err != nil {
			return err
		}

		if _, err := io.Copy(writer, reader); err != nil {
			return err
		}

		if err := writer.Close(); err != nil {
			return err
		}
	}
}
