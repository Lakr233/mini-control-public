package rest

import (
	"context"
	"net/http"
	"strings"

	"github.com/Lakr233/minis/server/internal/db"
	"github.com/Lakr233/minis/server/internal/model"
)

type workerAuthContextKey struct{}
type memberAuthContextKey struct{}

type authenticatedWorker struct {
	ID           string
	HardwareUUID string
}

type authenticatedMember = model.Member

func (h *Handler) requireAdminAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !tokensMatch(h.adminToken(), bearerToken(r.Header.Get("Authorization"))) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

func (h *Handler) requireEnrollmentAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !tokensMatch(h.enrollmentToken(), bearerToken(r.Header.Get("Authorization"))) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

func (h *Handler) requireWorkerAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r.Header.Get("Authorization"))
		worker, err := h.db.AuthenticateWorkerToken(r.Context(), token)
		if err != nil {
			if !db.IsNotFound(err) {
				h.logger.Error("authenticate worker token", "error", err)
			}
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		if err := h.db.TouchWorkerTokenUsage(r.Context(), worker.ID); err != nil {
			h.logger.Warn("touch worker token usage", "worker_id", worker.ID, "error", err)
		}

		ctx := context.WithValue(r.Context(), workerAuthContextKey{}, authenticatedWorker{
			ID:           worker.ID,
			HardwareUUID: worker.HardwareUUID,
		})
		next(w, r.WithContext(ctx))
	}
}

func (h *Handler) requireAccessAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.access == nil {
			writeError(w, http.StatusServiceUnavailable, "access auth not configured")
			return
		}

		token := strings.TrimSpace(r.Header.Get(accessJWTHeader))
		if token == "" {
			if cookie, err := r.Cookie("CF_Authorization"); err == nil {
				token = strings.TrimSpace(cookie.Value)
			}
		}
		if token == "" {
			writeError(w, http.StatusUnauthorized, "missing access token")
			return
		}

		claims, err := h.access.Verify(r.Context(), token)
		if err != nil {
			h.logger.Warn("verify access token", "error", err)
			writeError(w, http.StatusUnauthorized, "invalid access token")
			return
		}

		member, err := h.db.UpsertMemberFromAccess(r.Context(), claims.Subject, claims.Email)
		if err != nil {
			h.logger.Error("upsert member from access", "sub", claims.Subject, "error", err)
			writeError(w, http.StatusInternalServerError, "failed to load member")
			return
		}

		ctx := context.WithValue(r.Context(), memberAuthContextKey{}, *member)
		next(w, r.WithContext(ctx))
	}
}

func currentWorker(r *http.Request) (authenticatedWorker, bool) {
	worker, ok := r.Context().Value(workerAuthContextKey{}).(authenticatedWorker)
	return worker, ok
}

func currentMember(r *http.Request) (authenticatedMember, bool) {
	member, ok := r.Context().Value(memberAuthContextKey{}).(authenticatedMember)
	return member, ok
}

func (h *Handler) adminToken() string {
	return h.config.AdminToken
}

func (h *Handler) enrollmentToken() string {
	return h.config.EnrollmentToken
}
