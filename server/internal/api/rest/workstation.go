package rest

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Lakr233/minis/server/internal/db"
	"github.com/Lakr233/minis/server/internal/model"
)

func (h *Handler) handleListMyWorkstations(w http.ResponseWriter, r *http.Request) {
	member, ok := currentMember(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	workstations, err := h.db.ListWorkstationsByMemberID(r.Context(), member.ID)
	if err != nil {
		h.logger.Error("list my workstations", "member_id", member.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list workstations")
		return
	}
	if workstations == nil {
		workstations = []model.Workstation{}
	}

	writeJSON(w, http.StatusOK, workstations)
}

func (h *Handler) handleGetMyWorkstation(w http.ResponseWriter, r *http.Request) {
	member, ok := currentMember(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	requestedWorkstationID := strings.TrimSpace(r.URL.Query().Get("workstation_id"))
	workstation, resolveErr := h.resolveMemberWorkstation(r, requestedWorkstationID)
	if resolveErr != nil {
		resolveErr.write(w)
		return
	}

	workstation, err := h.db.EnsureWorkstationRunningByIDForMember(r.Context(), member.ID, workstation.ID)
	if err != nil {
		if db.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "workstation not found")
			return
		}
		h.logger.Error("get my workstation", "member_id", member.ID, "workstation_id", workstation.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load workstation")
		return
	}

	writeJSON(w, http.StatusOK, workstation)
}

func (h *Handler) handleClaimMyWorkstation(w http.ResponseWriter, r *http.Request) {
	member, ok := currentMember(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req struct {
		WorkerID string `json:"worker_id"`
	}
	if r.Body != nil {
		if err := decodeJSONBody(w, r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
	}

	workstation, created, err := h.db.ClaimWorkstation(r.Context(), member.ID, strings.TrimSpace(req.WorkerID))
	if err != nil {
		status := http.StatusInternalServerError
		message := "failed to claim workstation"
		if isClaimConflict(err) {
			status = http.StatusConflict
			message = err.Error()
		}
		h.logger.Error("claim workstation", "member_id", member.ID, "worker_id", req.WorkerID, "error", err)
		writeError(w, status, message)
		return
	}

	if created {
		writeJSON(w, http.StatusCreated, workstation)
		return
	}
	writeJSON(w, http.StatusOK, workstation)
}

func isClaimConflict(err error) bool {
	return errors.Is(err, db.ErrWorkstationBeingDeleted) ||
		errors.Is(err, db.ErrNoActiveWorkers) ||
		errors.Is(err, db.ErrNoSlotsAvailable) ||
		errors.Is(err, db.ErrTargetWorkerFull) ||
		errors.Is(err, db.ErrTargetWorkerUnavailable)
}

func (h *Handler) handleStartMyWorkstation(w http.ResponseWriter, r *http.Request) {
	h.handleMyWorkstationDesiredState(w, r, model.WorkstationDesiredPowerStateRunning)
}

func (h *Handler) handleStopMyWorkstation(w http.ResponseWriter, r *http.Request) {
	h.handleMyWorkstationDesiredState(w, r, model.WorkstationDesiredPowerStateStopped)
}

func (h *Handler) handleReleaseMyWorkstation(w http.ResponseWriter, r *http.Request) {
	h.handleMyWorkstationDesiredState(w, r, model.WorkstationDesiredPowerStateDeleted)
}

func (h *Handler) handleMyWorkstationDesiredState(w http.ResponseWriter, r *http.Request, desired model.WorkstationDesiredPowerState) {
	member, ok := currentMember(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req struct {
		WorkstationID string `json:"workstation_id"`
	}
	if r.Body != nil {
		if err := decodeJSONBody(w, r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
	}

	workstation, resolveErr := h.resolveMemberWorkstation(r, strings.TrimSpace(req.WorkstationID))
	if resolveErr != nil {
		resolveErr.write(w)
		return
	}

	workstation, err := h.db.UpdateWorkstationDesiredPowerStateByID(r.Context(), member.ID, workstation.ID, desired)
	if err != nil {
		if db.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "workstation not found")
			return
		}
		if errors.Is(err, db.ErrWorkstationBeingDeleted) {
			writeError(w, http.StatusConflict, "workstation is being deleted")
			return
		}
		h.logger.Error("update workstation desired power state", "member_id", member.ID, "workstation_id", workstation.ID, "desired_power_state", desired, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update workstation")
		return
	}

	writeJSON(w, http.StatusOK, workstation)
}

func (h *Handler) resolveMemberWorkstation(r *http.Request, requestedWorkstationID string) (*model.Workstation, *apiError) {
	member, ok := currentMember(r)
	if !ok {
		return nil, &apiError{http.StatusUnauthorized, "unauthorized"}
	}

	if requestedWorkstationID != "" {
		workstation, err := h.db.GetWorkstationByIDForMember(r.Context(), member.ID, requestedWorkstationID)
		if err != nil {
			if db.IsNotFound(err) {
				return nil, &apiError{http.StatusNotFound, "workstation not found"}
			}
			h.logger.Error("resolve member workstation by id", "member_id", member.ID, "workstation_id", requestedWorkstationID, "error", err)
			return nil, &apiError{http.StatusInternalServerError, "failed to load workstation"}
		}
		return workstation, nil
	}

	workstations, err := h.db.ListWorkstationsByMemberID(r.Context(), member.ID)
	if err != nil {
		h.logger.Error("resolve member workstations", "member_id", member.ID, "error", err)
		return nil, &apiError{http.StatusInternalServerError, "failed to load workstation"}
	}
	if len(workstations) == 0 {
		return nil, &apiError{http.StatusNotFound, "workstation not found"}
	}
	if len(workstations) > 1 {
		return nil, &apiError{http.StatusBadRequest, "workstation_id is required when multiple workstations exist"}
	}
	return &workstations[0], nil
}
