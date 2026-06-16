package rest

import (
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/Lakr233/minis/server/internal/db"
	"github.com/Lakr233/minis/server/internal/proxy"
	"github.com/Lakr233/minis/server/internal/release"
)

const (
	maxJSONBodyBytes     = 1 << 20
	maxProxyMessageBytes = 8 << 20
)

type HandlerConfig struct {
	AdminToken               string
	EnrollmentToken          string
	PoolSize                 int
	BaseImage                string
	HTTPAddr                 string
	TLSEnabled               bool
	WorkerPublicHost         string
	BrowserPublicHost        string
	AccessTeamDomain         string
	AccessAUD                string
	AccessIssuerURL          string
	AccessJWKSURL            string
	AccessAllowedEmailDomain string
}

type Handler struct {
	db            *db.DB
	logger        *slog.Logger
	installScript string
	config        HandlerConfig
	proxy         *proxy.SessionManager
	terminals     *terminalSessionManager
	releases      *release.Store
	access        *AccessVerifier
}

func NewHandler(database *db.DB, installScript string, proxyMgr *proxy.SessionManager, releases *release.Store, cfg HandlerConfig, logger *slog.Logger) (*Handler, error) {
	access, err := NewAccessVerifier(cfg, logger)
	if err != nil {
		return nil, err
	}
	return &Handler{
		db:            database,
		logger:        logger,
		installScript: installScript,
		config:        cfg,
		proxy:         proxyMgr,
		terminals:     newTerminalSessionManager(logger),
		releases:      releases,
		access:        access,
	}, nil
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// Public endpoints (no auth)
	mux.HandleFunc("GET /install.sh", h.requireWorkerHost(h.handleInstallScript))
	mux.HandleFunc("GET /api/v1/health", h.handleHealth)
	mux.HandleFunc("GET /api/v1/me", h.requireBrowserHost(h.requireAccessAuth(h.handleMe)))
	mux.HandleFunc("GET /api/v1/my-workstations", h.requireBrowserHost(h.requireAccessAuth(h.handleListMyWorkstations)))
	mux.HandleFunc("GET /api/v1/my-workstation", h.requireBrowserHost(h.requireAccessAuth(h.handleGetMyWorkstation)))
	mux.HandleFunc("POST /api/v1/my-workstation/claim", h.requireBrowserHost(h.requireAccessAuth(h.handleClaimMyWorkstation)))
	mux.HandleFunc("POST /api/v1/my-workstation/release", h.requireBrowserHost(h.requireAccessAuth(h.handleReleaseMyWorkstation)))
	mux.HandleFunc("GET /api/v1/browser/workers", h.requireBrowserHost(h.requireAccessAuth(h.handleListWorkers)))
	mux.HandleFunc("POST /api/v1/browser/proxy-sessions", h.requireBrowserHost(h.requireAccessAuth(h.handleCreateProxySession)))
	mux.HandleFunc("GET /api/v1/browser/terminal/ws", h.requireBrowserHost(h.requireAccessAuth(h.handleBrowserTerminalWebSocket)))

	// Admin endpoints
	mux.HandleFunc("GET /api/v1/workers", h.requireBrowserHost(h.requireAdminAuth(h.handleListWorkers)))

	// Proxy endpoints
	mux.HandleFunc("POST /api/v1/proxy/sessions", h.requireBrowserHost(h.requireAdminAuth(h.handleCreateProxySession)))
	mux.HandleFunc("GET /api/v1/proxy/sessions/pending", h.requireWorkerHost(h.requireWorkerAuth(h.handlePendingProxySessions)))
	mux.HandleFunc("GET /api/v1/proxy/ws", h.handleProxyWebSocket) // token auth via query param

	// Release management
	mux.HandleFunc("POST /api/v1/releases/upload", h.requireWorkerHost(h.requireAdminAuth(h.handleReleaseUpload)))
	mux.HandleFunc("PUT /api/v1/releases/upload/{version}", h.requireWorkerHost(h.requireAdminAuth(h.handleReleaseUploadBinary)))
	mux.HandleFunc("GET /api/v1/releases/latest", h.requireWorkerHost(h.handleReleaseLatest))
	mux.HandleFunc("GET /api/v1/releases/download/{version}", h.requireWorkerHost(h.handleReleaseDownload))

	// Host setup
	mux.HandleFunc("POST /api/v1/hostname/assign", h.requireWorkerHost(h.requireEnrollmentAuth(h.handleAssignHostname)))

	// Worker REST endpoints (same auth)
	mux.HandleFunc("POST /api/v1/workers/register", h.requireWorkerHost(h.handleWorkerRegister))
	mux.HandleFunc("POST /api/v1/workers/{id}/heartbeat", h.requireWorkerHost(h.requireWorkerAuth(h.handleWorkerHeartbeat)))
	mux.HandleFunc("PUT /api/v1/vms/{id}/state", h.requireWorkerHost(h.requireWorkerAuth(h.handleVMState)))
}

func (h *Handler) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	script := h.installScript
	script = strings.Replace(script, "__SERVER_ADDR__", h.workerScriptAddress(r), 1)
	w.Write([]byte(script))
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) handleMe(w http.ResponseWriter, r *http.Request) {
	member, ok := currentMember(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	writeJSON(w, http.StatusOK, member)
}

// ─── Shared Helpers ───

// apiError pairs an HTTP status with a client-safe message, so resource
// resolution helpers can hand the response decision back to the handler.
type apiError struct {
	status  int
	message string
}

func (e *apiError) write(w http.ResponseWriter) {
	writeError(w, e.status, e.message)
}

func bearerToken(header string) string {
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return ""
	}
	return strings.TrimSpace(token)
}

func tokensMatch(expected, actual string) bool {
	if expected == "" || actual == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) == 1
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func parsePositiveQueryInt(r *http.Request, key string, fallback int) int {
	value := strings.TrimSpace(r.URL.Query().Get(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func requireCurrentWorker(w http.ResponseWriter, r *http.Request) (authenticatedWorker, bool) {
	worker, ok := currentWorker(r)
	if ok {
		return worker, true
	}
	writeError(w, http.StatusUnauthorized, "unauthorized")
	return authenticatedWorker{}, false
}

func requireWorkerPathMatch(w http.ResponseWriter, r *http.Request) (authenticatedWorker, bool) {
	worker, ok := requireCurrentWorker(w, r)
	if !ok {
		return authenticatedWorker{}, false
	}
	if pathWorkerID := r.PathValue("id"); pathWorkerID == worker.ID {
		return worker, true
	}
	writeError(w, http.StatusForbidden, "worker_id mismatch")
	return authenticatedWorker{}, false
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(dst)
}
