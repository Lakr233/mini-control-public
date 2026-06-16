package proxy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

type SessionState string

const (
	SessionPending SessionState = "pending"
	SessionActive  SessionState = "active"
	SessionClosed  SessionState = "closed"
)

type ProxyTarget string

const (
	ProxyTargetVM   ProxyTarget = "vm"
	ProxyTargetHost ProxyTarget = "host"
)

type ProxySession struct {
	ID        string       `json:"session_id"`
	Target    ProxyTarget  `json:"target"` // "vm" or "host"
	VMID      string       `json:"vm_id"`  // only for target=vm
	WorkerID  string       `json:"worker_id"`
	Port      int          `json:"port"`
	Token     string       `json:"token"`
	State     SessionState `json:"state"`
	CreatedAt time.Time    `json:"created_at"`

	mu         sync.Mutex
	clientConn *websocket.Conn
	workerConn *websocket.Conn
	ready      chan struct{} // closed when both sides connect
	done       chan struct{} // closed when session ends
}

type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]*ProxySession
	logger   *slog.Logger

	// Allowed ports for proxying
	AllowedPorts map[int]bool
}

func NewSessionManager(logger *slog.Logger) *SessionManager {
	return &SessionManager{
		sessions:     make(map[string]*ProxySession),
		logger:       logger,
		AllowedPorts: map[int]bool{5900: true, 22: true},
	}
}

func (sm *SessionManager) Create(target ProxyTarget, vmID, workerID string, port int) *ProxySession {
	tokenBytes := make([]byte, 32)
	rand.Read(tokenBytes)

	s := &ProxySession{
		ID:        uuid.NewString(),
		Target:    target,
		VMID:      vmID,
		WorkerID:  workerID,
		Port:      port,
		Token:     hex.EncodeToString(tokenBytes),
		State:     SessionPending,
		CreatedAt: time.Now(),
		ready:     make(chan struct{}),
		done:      make(chan struct{}),
	}

	sm.mu.Lock()
	sm.sessions[s.ID] = s
	sm.mu.Unlock()

	sm.logger.Info("proxy session created", "session_id", s.ID, "target", string(target), "vm_id", vmID, "worker_id", workerID, "port", port)
	return s
}

func (sm *SessionManager) Get(sessionID string) *ProxySession {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.sessions[sessionID]
}

func (sm *SessionManager) GetPending(workerID string) []*ProxySession {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	var result []*ProxySession
	for _, s := range sm.sessions {
		if s.WorkerID == workerID && s.State == SessionPending {
			result = append(result, s)
		}
	}
	return result
}

// SetConn stores a WebSocket connection for the given role and returns true if both sides are now connected.
func (sm *SessionManager) SetConn(sessionID, role string, conn *websocket.Conn) bool {
	s := sm.Get(sessionID)
	if s == nil {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	switch role {
	case "client":
		s.clientConn = conn
	case "worker":
		s.workerConn = conn
	}

	if s.clientConn != nil && s.workerConn != nil {
		s.State = SessionActive
		select {
		case <-s.ready:
		default:
			close(s.ready)
		}
		return true
	}
	return false
}

func (sm *SessionManager) Close(sessionID string) {
	sm.mu.Lock()
	s := sm.sessions[sessionID]
	if s != nil {
		delete(sm.sessions, sessionID)
	}
	sm.mu.Unlock()

	if s == nil {
		return
	}

	s.mu.Lock()
	s.State = SessionClosed
	if s.clientConn != nil {
		s.clientConn.CloseNow()
	}
	if s.workerConn != nil {
		s.workerConn.CloseNow()
	}
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	s.mu.Unlock()

	sm.logger.Info("proxy session closed", "session_id", sessionID)
}

func (sm *SessionManager) CloseByWorker(workerID string) int {
	sm.mu.Lock()
	var sessionIDs []string
	for id, s := range sm.sessions {
		if s.WorkerID == workerID {
			sessionIDs = append(sessionIDs, id)
		}
	}
	sm.mu.Unlock()

	for _, id := range sessionIDs {
		sm.Close(id)
	}
	return len(sessionIDs)
}

func (sm *SessionManager) CloseByVM(vmID string) int {
	sm.mu.Lock()
	var sessionIDs []string
	for id, s := range sm.sessions {
		if s.VMID == vmID {
			sessionIDs = append(sessionIDs, id)
		}
	}
	sm.mu.Unlock()

	for _, id := range sessionIDs {
		sm.Close(id)
	}
	return len(sessionIDs)
}

const (
	reapInterval         = 10 * time.Second
	pendingSessionMaxAge = 60 * time.Second
)

// StartReaper periodically cleans up sessions that never became active.
func (sm *SessionManager) StartReaper(ctx context.Context) {
	ticker := time.NewTicker(reapInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sm.reap()
		}
	}
}

// Done returns a channel that's closed when the session ends.
func (s *ProxySession) Done() <-chan struct{} {
	return s.done
}

func (sm *SessionManager) reap() {
	sm.mu.Lock()
	var stale []string
	now := time.Now()
	for id, s := range sm.sessions {
		if s.State == SessionPending && now.Sub(s.CreatedAt) > pendingSessionMaxAge {
			stale = append(stale, id)
		}
	}
	sm.mu.Unlock()

	for _, id := range stale {
		sm.logger.Info("reaping stale proxy session", "session_id", id)
		sm.Close(id)
	}
}
