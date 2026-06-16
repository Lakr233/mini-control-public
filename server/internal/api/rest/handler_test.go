package rest

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func newAuthOnlyHandler(cfg HandlerConfig) *Handler {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return &Handler{
		logger: logger,
		config: cfg,
	}
}

func TestBearerToken(t *testing.T) {
	tests := []struct {
		header   string
		expected string
	}{
		{header: "Bearer abc123", expected: "abc123"},
		{header: "Bearer  abc123 ", expected: "abc123"},
		{header: "bearer abc123", expected: ""},
		{header: "Basic abc123", expected: ""},
		{header: "", expected: ""},
	}

	for _, tt := range tests {
		if got := bearerToken(tt.header); got != tt.expected {
			t.Fatalf("bearerToken(%q) = %q, want %q", tt.header, got, tt.expected)
		}
	}
}

func TestTokensMatch(t *testing.T) {
	if !tokensMatch("abc", "abc") {
		t.Fatal("expected identical tokens to match")
	}
	if tokensMatch("abc", "def") {
		t.Fatal("expected different tokens to not match")
	}
	if tokensMatch("", "abc") {
		t.Fatal("expected empty expected token to fail")
	}
}

func TestEnrollmentTokenUsesDedicatedValue(t *testing.T) {
	handler := newAuthOnlyHandler(HandlerConfig{EnrollmentToken: "enrollment-token"})
	if got := handler.enrollmentToken(); got != "enrollment-token" {
		t.Fatalf("enrollmentToken() = %q, want dedicated token", got)
	}
}

func TestRequireAdminAuth_ValidDedicatedToken(t *testing.T) {
	handler := newAuthOnlyHandler(HandlerConfig{AdminToken: "admin-secret"})
	called := false
	wrapped := handler.requireAdminAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	rr := httptest.NewRecorder()
	wrapped(rr, req)

	if !called {
		t.Fatal("expected admin handler to run")
	}
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
}

func TestRequireEnrollmentAuth_RejectsWrongToken(t *testing.T) {
	handler := newAuthOnlyHandler(HandlerConfig{EnrollmentToken: "enroll-secret"})
	wrapped := handler.requireEnrollmentAuth(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not run with invalid enrollment token")
	})

	req := httptest.NewRequest(http.MethodPost, "/enroll", nil)
	req.Header.Set("Authorization", "Bearer nope")
	rr := httptest.NewRecorder()
	wrapped(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestHandleHealth(t *testing.T) {
	handler := newAuthOnlyHandler(HandlerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rr := httptest.NewRecorder()

	handler.handleHealth(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status body = %q, want ok", body["status"])
	}
}

func TestRequireCurrentWorker_ReturnsUnauthorizedWithoutContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/workers/test", nil)
	rr := httptest.NewRecorder()

	if _, ok := requireCurrentWorker(rr, req); ok {
		t.Fatal("expected missing worker context to fail")
	}
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestRequireWorkerPathMatch_RejectsMismatchedPath(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workers/worker-b/heartbeat", nil)
	req.SetPathValue("id", "worker-b")
	req = req.WithContext(context.WithValue(req.Context(), workerAuthContextKey{}, authenticatedWorker{
		ID:           "worker-a",
		HardwareUUID: "hw-a",
	}))
	rr := httptest.NewRecorder()

	if _, ok := requireWorkerPathMatch(rr, req); ok {
		t.Fatal("expected mismatched path worker ID to fail")
	}
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}
}
