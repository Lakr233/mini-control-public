package rest

import (
	"context"
	"net/http"
	"strings"

	"github.com/coder/websocket"
	"github.com/Lakr233/minis/server/internal/db"
	"github.com/Lakr233/minis/server/internal/model"
	"github.com/Lakr233/minis/server/internal/proxy"
)

func (h *Handler) handleCreateProxySession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Target   string `json:"target"`    // "vm" or "host"
		VMID     string `json:"vm_id"`     // required for target=vm
		WorkerID string `json:"worker_id"` // required for target=host
		Port     int    `json:"port"`
	}
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Port <= 0 {
		writeError(w, http.StatusBadRequest, "port is required")
		return
	}
	if !h.proxy.AllowedPorts[req.Port] {
		writeError(w, http.StatusForbidden, "port not allowed")
		return
	}

	if _, ok := currentMember(r); ok {
		if req.Target == string(proxy.ProxyTargetHost) {
			writeError(w, http.StatusForbidden, "host access is not available for members")
			return
		}
		workstation, resolveErr := h.resolveMemberWorkstation(r, strings.TrimSpace(req.VMID))
		if resolveErr != nil {
			resolveErr.write(w)
			return
		}
		if workstation.PowerState != model.WorkstationPowerStateRunning {
			writeError(w, http.StatusConflict, "workstation not running")
			return
		}

		session := h.proxy.Create(proxy.ProxyTargetVM, workstation.ID, workstation.WorkerID, req.Port)
		writeJSON(w, http.StatusCreated, map[string]any{
			"session_id": session.ID,
			"token":      session.Token,
		})
		return
	}

	target, workerID, vmID, resolveErr := h.resolveProxyTarget(r.Context(), req.Target, req.VMID, req.WorkerID)
	if resolveErr != nil {
		resolveErr.write(w)
		return
	}

	session := h.proxy.Create(target, vmID, workerID, req.Port)

	writeJSON(w, http.StatusCreated, map[string]any{
		"session_id": session.ID,
		"token":      session.Token,
	})
}

func (h *Handler) resolveProxyTarget(ctx context.Context, target, vmID, workerID string) (proxy.ProxyTarget, string, string, *apiError) {
	if target == "" {
		target = string(proxy.ProxyTargetVM)
	}

	switch proxy.ProxyTarget(target) {
	case proxy.ProxyTargetHost:
		if workerID == "" {
			return "", "", "", &apiError{http.StatusBadRequest, "worker_id is required for host target"}
		}
		return proxy.ProxyTargetHost, workerID, "", nil

	case proxy.ProxyTargetVM:
		if vmID == "" {
			return "", "", "", &apiError{http.StatusBadRequest, "vm_id is required for vm target"}
		}
		workstation, err := h.db.GetWorkstationByID(ctx, vmID)
		if err == nil {
			if workstation.PowerState != model.WorkstationPowerStateRunning {
				return "", "", "", &apiError{http.StatusConflict, "workstation not running"}
			}
			return proxy.ProxyTargetVM, workstation.WorkerID, workstation.ID, nil
		}
		if !db.IsNotFound(err) {
			h.logger.Error("get workstation", "workstation_id", vmID, "error", err)
			return "", "", "", &apiError{http.StatusInternalServerError, "failed to load workstation"}
		}
		vm, err := h.db.GetVM(ctx, vmID)
		if err != nil {
			return "", "", "", &apiError{http.StatusNotFound, "vm not found"}
		}
		if vm.State != model.VMStateReady && vm.State != model.VMStateBusy && vm.State != model.VMStateBootstrapping {
			return "", "", "", &apiError{http.StatusConflict, "vm not available"}
		}
		return proxy.ProxyTargetVM, vm.WorkerID, vmID, nil

	default:
		return "", "", "", &apiError{http.StatusBadRequest, "target must be 'vm' or 'host'"}
	}
}

func (h *Handler) handlePendingProxySessions(w http.ResponseWriter, r *http.Request) {
	worker, ok := requireCurrentWorker(w, r)
	if !ok {
		return
	}
	if requestedWorkerID := r.URL.Query().Get("worker_id"); requestedWorkerID != "" && requestedWorkerID != worker.ID {
		writeError(w, http.StatusForbidden, "worker_id mismatch")
		return
	}

	workerID := worker.ID

	sessions := h.proxy.GetPending(workerID)
	type pendingResp struct {
		SessionID string `json:"session_id"`
		Target    string `json:"target"`
		VMID      string `json:"vm_id,omitempty"`
		Port      int    `json:"port"`
		Token     string `json:"token"`
	}
	result := make([]pendingResp, 0, len(sessions))
	for _, s := range sessions {
		if s.Target == proxy.ProxyTargetVM && !h.pendingSessionBelongsToWorker(r.Context(), s, workerID) {
			h.logger.Info("dropping stale pending proxy session", "session_id", s.ID, "worker_id", workerID, "vm_id", s.VMID)
			h.proxy.Close(s.ID)
			continue
		}
		result = append(result, pendingResp{
			SessionID: s.ID,
			Target:    string(s.Target),
			VMID:      s.VMID,
			Port:      s.Port,
			Token:     s.Token,
		})
	}
	writeJSON(w, http.StatusOK, result)
}

// pendingSessionBelongsToWorker reports whether the VM behind a pending
// session is still assigned to the polling worker, checking workstations
// first and falling back to the legacy vms table.
func (h *Handler) pendingSessionBelongsToWorker(ctx context.Context, s *proxy.ProxySession, workerID string) bool {
	workstation, err := h.db.GetWorkstationByID(ctx, s.VMID)
	if err == nil {
		return workstation.WorkerID == workerID
	}
	if !db.IsNotFound(err) {
		return false
	}
	vm, err := h.db.GetVM(ctx, s.VMID)
	return err == nil && vm != nil && vm.WorkerID == workerID
}

func (h *Handler) handleProxyWebSocket(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session")
	role := r.URL.Query().Get("role")
	token := r.URL.Query().Get("token")

	if sessionID == "" || role == "" || token == "" {
		writeError(w, http.StatusBadRequest, "missing parameters")
		return
	}
	if role != "client" && role != "worker" {
		writeError(w, http.StatusBadRequest, "invalid role")
		return
	}
	if role == "client" && !h.matchesBrowserHost(r) {
		http.NotFound(w, r)
		return
	}
	if role == "worker" && !h.matchesWorkerHost(r) {
		http.NotFound(w, r)
		return
	}

	session := h.proxy.Get(sessionID)
	if session == nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if !tokensMatch(session.Token, token) {
		writeError(w, http.StatusUnauthorized, "invalid token")
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		h.logger.Error("websocket accept", "error", err)
		return
	}
	conn.SetReadLimit(maxProxyMessageBytes)

	bothReady := h.proxy.SetConn(sessionID, role, conn)

	h.logger.Info("proxy websocket connected", "session_id", sessionID, "role", role)

	if bothReady {
		// This goroutine runs the relay; the other side is already waiting
		proxy.Relay(r.Context(), session, h.logger)
		h.proxy.Close(sessionID)
		return
	}

	// Wait for the other side or session close
	select {
	case <-session.Done():
	case <-r.Context().Done():
		h.proxy.Close(sessionID)
	}
}
