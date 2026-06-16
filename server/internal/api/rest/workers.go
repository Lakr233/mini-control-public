package rest

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/Lakr233/minis/server/internal/db"
	"github.com/Lakr233/minis/server/internal/model"
)

type WorkerRegisterRequest struct {
	Hostname      string `json:"hostname"`
	HardwareUUID  string `json:"hardware_uuid"`
	CPUCores      int    `json:"cpu_cores"`
	MemoryBytes   int64  `json:"memory_bytes"`
	TartVersion   string `json:"tart_version"`
	WorkerVersion string `json:"worker_version"`
	WorkerToken   string `json:"worker_token"`
}

func (h *Handler) handleWorkerRegister(w http.ResponseWriter, r *http.Request) {
	var req WorkerRegisterRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Hostname == "" {
		writeError(w, http.StatusBadRequest, "hostname is required")
		return
	}
	if req.HardwareUUID == "" {
		writeError(w, http.StatusBadRequest, "hardware_uuid is required")
		return
	}
	if req.WorkerToken == "" {
		writeError(w, http.StatusUnauthorized, "invalid token")
		return
	}

	workerID := model.GenerateWorkerID(req.HardwareUUID)
	shouldIssueWorkerToken, err := h.shouldIssueWorkerToken(r.Context(), req.HardwareUUID, req.WorkerToken)
	if err != nil {
		if db.IsNotFound(err) {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		h.logger.Error("authenticate worker registration", "hardware_uuid", req.HardwareUUID, "error", err)
		writeError(w, http.StatusInternalServerError, "registration failed")
		return
	}

	worker := &model.Worker{
		ID:            workerID,
		Hostname:      req.Hostname,
		HardwareUUID:  req.HardwareUUID,
		CPUCores:      req.CPUCores,
		MemoryBytes:   req.MemoryBytes,
		TartVersion:   req.TartVersion,
		WorkerVersion: req.WorkerVersion,
		PoolSize:      h.config.PoolSize,
		BaseImage:     h.config.BaseImage,
	}

	if err := h.db.UpsertWorker(r.Context(), worker); err != nil {
		h.logger.Error("register worker", "error", err)
		writeError(w, http.StatusInternalServerError, "registration failed")
		return
	}
	if closed := h.proxy.CloseByWorker(workerID); closed > 0 {
		h.logger.Info("closed stale proxy sessions on register", "worker_id", workerID, "count", closed)
	}

	h.logger.Info("worker registered via REST", "worker_id", workerID, "hostname", req.Hostname)

	resp := map[string]any{
		"worker_id":  workerID,
		"pool_size":  h.config.PoolSize,
		"base_image": h.config.BaseImage,
	}
	if shouldIssueWorkerToken {
		issuedToken, err := h.db.IssueWorkerToken(r.Context(), workerID)
		if err != nil {
			h.logger.Error("issue worker token", "worker_id", workerID, "error", err)
			writeError(w, http.StatusInternalServerError, "registration failed")
			return
		}
		resp["worker_token"] = issuedToken
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleWorkerHeartbeat(w http.ResponseWriter, r *http.Request) {
	worker, ok := requireWorkerPathMatch(w, r)
	if !ok {
		return
	}

	workerID := worker.ID
	if err := h.db.UpdateHeartbeat(r.Context(), workerID); err != nil {
		h.logger.Error("heartbeat", "worker_id", workerID, "error", err)
	}

	var body struct {
		WorkstationStatuses []struct {
			WorkstationID string `json:"workstation_id"`
			PowerState    string `json:"power_state"`
			IPAddress     string `json:"ip_address"`
			LastError     string `json:"last_error"`
		} `json:"workstation_statuses"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		h.logger.Warn("heartbeat decode failed", "worker_id", workerID, "error", err)
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	for _, ws := range body.WorkstationStatuses {
		if err := h.db.UpdateWorkstationStatus(r.Context(), workerID, db.WorkstationStatusReport{
			WorkstationID: ws.WorkstationID,
			PowerState:    model.WorkstationPowerState(ws.PowerState),
			IPAddress:     ws.IPAddress,
			LastError:     ws.LastError,
		}); err != nil {
			h.logger.Error("update workstation in heartbeat", "workstation_id", ws.WorkstationID, "error", err)
		}
	}

	desired, err := h.db.ListDesiredWorkstationsByWorker(r.Context(), workerID)
	if err != nil {
		h.logger.Error("list desired workstations", "worker_id", workerID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list desired workstations")
		return
	}

	type desiredWorkstation struct {
		ID                string `json:"id"`
		VMName            string `json:"vm_name"`
		Slot              int    `json:"slot"`
		DesiredPowerState string `json:"desired_power_state"`
	}
	resp := struct {
		Status              string               `json:"status"`
		DesiredWorkstations []desiredWorkstation `json:"desired_workstations"`
	}{
		Status:              "ok",
		DesiredWorkstations: make([]desiredWorkstation, 0, len(desired)),
	}
	for _, item := range desired {
		resp.DesiredWorkstations = append(resp.DesiredWorkstations, desiredWorkstation{
			ID:                item.ID,
			VMName:            item.VMName,
			Slot:              item.Slot,
			DesiredPowerState: string(item.DesiredPowerState),
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleVMState(w http.ResponseWriter, r *http.Request) {
	worker, ok := requireCurrentWorker(w, r)
	if !ok {
		return
	}

	vmID := r.PathValue("id")
	var body struct {
		WorkerID     string `json:"worker_id"`
		State        string `json:"state"`
		IPAddress    string `json:"ip_address"`
		ErrorMessage string `json:"error_message"`
	}
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.WorkerID != "" && body.WorkerID != worker.ID {
		writeError(w, http.StatusForbidden, "worker_id mismatch")
		return
	}

	workstation, err := h.db.GetWorkstationByID(r.Context(), vmID)
	if err != nil && !db.IsNotFound(err) {
		h.logger.Error("get workstation", "workstation_id", vmID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed")
		return
	}
	if err == nil && workstation.WorkerID != worker.ID {
		writeError(w, http.StatusForbidden, "workstation does not belong to worker")
		return
	}

	if err := h.db.UpdateWorkstationStatus(r.Context(), worker.ID, db.WorkstationStatusReport{
		WorkstationID: vmID,
		PowerState:    model.WorkstationPowerState(body.State),
		IPAddress:     body.IPAddress,
		LastError:     body.ErrorMessage,
	}); err != nil {
		h.logger.Error("update workstation state", "workstation_id", vmID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleAssignHostname(w http.ResponseWriter, r *http.Request) {
	var req struct {
		HardwareUUID string `json:"hardware_uuid"`
	}
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.HardwareUUID == "" {
		writeError(w, http.StatusBadRequest, "hardware_uuid is required")
		return
	}

	hostname, err := h.db.AssignHostname(r.Context(), req.HardwareUUID)
	if err != nil {
		h.logger.Error("assign hostname", "hardware_uuid", req.HardwareUUID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to assign hostname")
		return
	}
	h.logger.Info("hostname assigned", "hostname", hostname, "hardware_uuid", req.HardwareUUID)
	writeJSON(w, http.StatusOK, map[string]string{"hostname": hostname})
}

func (h *Handler) handleListWorkers(w http.ResponseWriter, r *http.Request) {
	workers, err := h.db.ListWorkers(r.Context())
	if err != nil {
		h.logger.Error("list workers", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list workers")
		return
	}
	if workers == nil {
		workers = []model.Worker{}
	}

	allWorkstations, err := h.db.ListAllWorkstations(r.Context())
	if err != nil {
		h.logger.Error("list all workstations", "error", err)
		allWorkstations = []model.Workstation{}
	}

	if member, ok := currentMember(r); ok {
		filtered := allWorkstations[:0]
		for _, workstation := range allWorkstations {
			if workstation.MemberID == member.ID {
				filtered = append(filtered, workstation)
			}
		}
		allWorkstations = filtered
	}

	type browserVM struct {
		ID         string    `json:"id"`
		WorkerID   string    `json:"worker_id"`
		State      string    `json:"state"`
		IPAddress  string    `json:"ip_address,omitempty"`
		StateSince time.Time `json:"state_since"`
		CreatedAt  time.Time `json:"created_at"`
	}
	type workerWithVMs struct {
		model.Worker
		VMs []browserVM `json:"vms"`
	}

	vmsByWorker := make(map[string][]browserVM)
	for _, workstation := range allWorkstations {
		vmsByWorker[workstation.WorkerID] = append(vmsByWorker[workstation.WorkerID], browserVM{
			ID:         workstation.ID,
			WorkerID:   workstation.WorkerID,
			State:      string(workstation.PowerState),
			IPAddress:  workstation.IPAddress,
			StateSince: workstation.UpdatedAt,
			CreatedAt:  workstation.CreatedAt,
		})
	}

	result := make([]workerWithVMs, 0, len(workers))
	for _, w := range workers {
		vms := vmsByWorker[w.ID]
		if vms == nil {
			vms = []browserVM{}
		}
		result = append(result, workerWithVMs{Worker: w, VMs: vms})
	}

	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) shouldIssueWorkerToken(ctx context.Context, hardwareUUID string, presentedToken string) (bool, error) {
	existingWorker, err := h.db.GetWorkerByHardwareUUID(ctx, hardwareUUID)
	if err != nil {
		if !db.IsNotFound(err) {
			return false, err
		}
		if tokensMatch(h.enrollmentToken(), presentedToken) {
			return true, nil
		}
		return false, pgx.ErrNoRows
	}

	if existingWorker.AuthTokenHash != nil && *existingWorker.AuthTokenHash != "" &&
		existingWorker.AuthTokenRevokedAt == nil &&
		tokensMatch(*existingWorker.AuthTokenHash, db.HashWorkerToken(presentedToken)) {
		return false, nil
	}
	if tokensMatch(h.enrollmentToken(), presentedToken) {
		return true, nil
	}
	return false, pgx.ErrNoRows
}
