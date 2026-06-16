package rest

import (
	"container/ring"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"
)

const (
	terminalHistoryFrames       = 256
	terminalAttachmentQueueSize = 128
	terminalIdleTimeout         = 60 * time.Second
	terminalWriteTimeout        = 10 * time.Second
)

type terminalSessionManager struct {
	mu       sync.Mutex
	sessions map[string]*terminalSession
	logger   *slog.Logger
}

type terminalSession struct {
	id            string
	memberID      string
	workstationID string
	workerID      string
	logger        *slog.Logger

	sshClient  *ssh.Client
	sshSession *ssh.Session
	sshInput   io.WriteCloser
	proxyConn  *websocket.Conn
	cleanup    func()
	remove     func(string)

	ctx    context.Context
	cancel context.CancelFunc

	attachMu  sync.Mutex
	closeOnce sync.Once

	mu         sync.Mutex
	attachment *terminalAttachment
	history    *ring.Ring
	historyLen int
	nextSeq    uint64
	closed     bool
	idleTimer  *time.Timer
}

type terminalHistoryFrame struct {
	seq     uint64
	message websocket.MessageType
	payload []byte
}

type terminalOutboundMessage struct {
	message websocket.MessageType
	payload []byte
}

type terminalAttachment struct {
	conn     *websocket.Conn
	outbound chan terminalOutboundMessage
	done     chan struct{}

	closeOnce sync.Once
}

func newTerminalSessionManager(logger *slog.Logger) *terminalSessionManager {
	return &terminalSessionManager{
		sessions: make(map[string]*terminalSession),
		logger:   logger,
	}
}

func (m *terminalSessionManager) Create(
	memberID string,
	workstationID string,
	workerID string,
	logger *slog.Logger,
	sshClient *ssh.Client,
	sshSession *ssh.Session,
	sshInput io.WriteCloser,
	proxyConn *websocket.Conn,
	cleanup func(),
	stdout io.Reader,
	stderr io.Reader,
) *terminalSession {
	ctx, cancel := context.WithCancel(context.Background())
	session := &terminalSession{
		id:            uuid.NewString(),
		memberID:      memberID,
		workstationID: workstationID,
		workerID:      workerID,
		logger:        logger,
		sshClient:     sshClient,
		sshSession:    sshSession,
		sshInput:      sshInput,
		proxyConn:     proxyConn,
		cleanup:       cleanup,
		ctx:           ctx,
		cancel:        cancel,
	}
	session.remove = m.remove

	m.mu.Lock()
	m.sessions[session.id] = session
	m.mu.Unlock()

	go session.streamOutput(stdout)
	go session.streamOutput(stderr)
	go session.waitForExit()

	return session
}

func (m *terminalSessionManager) Get(sessionID string) *terminalSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[sessionID]
}

func (m *terminalSessionManager) remove(sessionID string) {
	m.mu.Lock()
	delete(m.sessions, sessionID)
	m.mu.Unlock()
}

func (s *terminalSession) Attach(conn *websocket.Conn, cols, rows int, recovered bool) error {
	s.attachMu.Lock()

	if s.isClosed() {
		s.attachMu.Unlock()
		return errors.New("terminal session is closed")
	}

	s.stopIdleTimer()

	nextAttachment := newTerminalAttachment(s.ctx, conn)
	if current := s.currentAttachment(); current != nil {
		current.close(websocket.StatusPolicyViolation, "terminal reattached")
		s.removeAttachment(current)
	}

	if err := nextAttachment.sendControl(
		terminalServerMessage{Type: "ready", SessionID: s.id, Recovered: recovered},
	); err != nil {
		s.attachMu.Unlock()
		nextAttachment.close(websocket.StatusInternalError, "terminal attach failed")
		return err
	}

	replayedSeq := uint64(0)
	for {
		frames, latestSeq := s.snapshotFramesAfter(replayedSeq)
		if len(frames) == 0 {
			s.setAttachment(nextAttachment)
			break
		}
		for _, frame := range frames {
			if err := nextAttachment.send(frame.message, frame.payload); err != nil {
				s.attachMu.Unlock()
				nextAttachment.close(websocket.StatusInternalError, "terminal attach failed")
				return err
			}
		}
		replayedSeq = latestSeq
	}

	s.attachMu.Unlock()

	if rows > 0 && cols > 0 {
		if err := s.sshSession.WindowChange(rows, cols); err != nil {
			s.clearAttachment(nextAttachment)
			nextAttachment.close(websocket.StatusInternalError, "terminal resize failed")
			return err
		}
	}

	err := s.readInput(nextAttachment)
	s.clearAttachment(nextAttachment)

	if err == nil ||
		errors.Is(err, context.Canceled) ||
		websocket.CloseStatus(err) == websocket.StatusNormalClosure ||
		websocket.CloseStatus(err) == websocket.StatusGoingAway {
		return nil
	}
	return err
}

func (s *terminalSession) streamOutput(reader io.Reader) {
	buffer := make([]byte, 32*1024)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			s.publishBinary(buffer[:n])
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) {
			return
		}
		s.close(websocket.StatusInternalError, "Terminal session ended unexpectedly.", err)
		return
	}
}

func (s *terminalSession) waitForExit() {
	err := s.sshSession.Wait()
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) {
		s.close(websocket.StatusInternalError, "Terminal session ended unexpectedly.", err)
		return
	}
	s.close(websocket.StatusNormalClosure, "terminal closed", err)
}

func (s *terminalSession) readInput(attachment *terminalAttachment) error {
	for {
		msgType, payload, err := attachment.conn.Read(s.ctx)
		if err != nil {
			return err
		}
		if msgType != websocket.MessageText {
			continue
		}

		var msg terminalClientMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			return err
		}

		switch msg.Type {
		case "input":
			if msg.Data == "" {
				continue
			}
			if _, err := io.WriteString(s.sshInput, msg.Data); err != nil {
				return err
			}
		case "resize":
			if msg.Cols <= 0 || msg.Rows <= 0 {
				continue
			}
			if err := s.sshSession.WindowChange(msg.Rows, msg.Cols); err != nil {
				return err
			}
		}
	}
}

func (s *terminalSession) publishBinary(payload []byte) {
	copied := append([]byte(nil), payload...)
	frame := terminalHistoryFrame{
		message: websocket.MessageBinary,
		payload: copied,
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}

	s.nextSeq++
	frame.seq = s.nextSeq
	s.appendFrameLocked(frame)
	attachment := s.attachment
	s.mu.Unlock()

	if attachment == nil {
		return
	}

	if err := attachment.send(frame.message, frame.payload); err != nil {
		s.logger.Warn("terminal attachment send failed", "session_id", s.id, "error", err)
		s.clearAttachment(attachment)
	}
}

func (s *terminalSession) appendFrameLocked(frame terminalHistoryFrame) {
	if s.history == nil {
		s.history = ring.New(terminalHistoryFrames)
	}
	s.history.Value = frame
	s.history = s.history.Next()
	if s.historyLen < terminalHistoryFrames {
		s.historyLen++
	}
}

func (s *terminalSession) snapshotFramesAfter(afterSeq uint64) ([]terminalHistoryFrame, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.historyLen == 0 {
		return nil, s.nextSeq
	}

	oldest := s.history.Move(-s.historyLen)
	frames := make([]terminalHistoryFrame, 0, s.historyLen)
	current := oldest
	for i := 0; i < s.historyLen; i++ {
		if frame, ok := current.Value.(terminalHistoryFrame); ok && frame.seq > afterSeq {
			frames = append(frames, frame)
		}
		current = current.Next()
	}
	return frames, s.nextSeq
}

func (s *terminalSession) currentAttachment() *terminalAttachment {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attachment
}

func (s *terminalSession) setAttachment(attachment *terminalAttachment) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attachment = attachment
}

func (s *terminalSession) clearAttachment(target *terminalAttachment) {
	s.mu.Lock()
	if s.attachment == target {
		s.attachment = nil
		s.scheduleIdleTimeoutLocked()
	}
	s.mu.Unlock()

	if target != nil {
		target.close(websocket.StatusGoingAway, "terminal detached")
	}
}

func (s *terminalSession) removeAttachment(target *terminalAttachment) {
	s.mu.Lock()
	if s.attachment == target {
		s.attachment = nil
	}
	s.mu.Unlock()
}

func (s *terminalSession) scheduleIdleTimeoutLocked() {
	if s.closed {
		return
	}
	if s.idleTimer != nil {
		s.idleTimer.Stop()
	}
	s.idleTimer = time.AfterFunc(terminalIdleTimeout, func() {
		s.close(websocket.StatusGoingAway, "terminal session expired", context.DeadlineExceeded)
	})
}

func (s *terminalSession) stopIdleTimer() {
	s.mu.Lock()
	if s.idleTimer != nil {
		s.idleTimer.Stop()
		s.idleTimer = nil
	}
	s.mu.Unlock()
}

func (s *terminalSession) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *terminalSession) close(code websocket.StatusCode, message string, cause error) {
	s.closeOnce.Do(func() {
		s.cancel()

		s.mu.Lock()
		s.closed = true
		attachment := s.attachment
		s.attachment = nil
		if s.idleTimer != nil {
			s.idleTimer.Stop()
			s.idleTimer = nil
		}
		s.mu.Unlock()

		if attachment != nil && code != websocket.StatusNormalClosure {
			_ = attachment.sendControl(terminalServerMessage{Type: "error", Message: message})
		}
		if attachment != nil {
			attachment.close(code, message)
		}
		if s.sshSession != nil {
			_ = s.sshSession.Close()
		}
		if s.sshClient != nil {
			_ = s.sshClient.Close()
		}
		if s.proxyConn != nil {
			s.proxyConn.CloseNow()
		}
		if s.cleanup != nil {
			s.cleanup()
		}
		if s.remove != nil {
			s.remove(s.id)
		}

		if cause != nil && !errors.Is(cause, io.EOF) && !errors.Is(cause, context.Canceled) {
			s.logger.Warn("terminal session closed", "session_id", s.id, "error", cause)
			return
		}
		s.logger.Info("terminal session closed", "session_id", s.id, "reason", message)
	})
}

func newTerminalAttachment(sessionCtx context.Context, conn *websocket.Conn) *terminalAttachment {
	attachment := &terminalAttachment{
		conn:     conn,
		outbound: make(chan terminalOutboundMessage, terminalAttachmentQueueSize),
		done:     make(chan struct{}),
	}

	go func() {
		defer attachment.close(websocket.StatusNormalClosure, "attachment closed")

		for {
			select {
			case <-sessionCtx.Done():
				return
			case <-attachment.done:
				return
			case msg, ok := <-attachment.outbound:
				if !ok {
					return
				}
				writeCtx, cancel := context.WithTimeout(sessionCtx, terminalWriteTimeout)
				err := conn.Write(writeCtx, msg.message, msg.payload)
				cancel()
				if err != nil {
					return
				}
			}
		}
	}()

	return attachment
}

func (a *terminalAttachment) send(message websocket.MessageType, payload []byte) error {
	select {
	case <-a.done:
		return errors.New("terminal attachment closed")
	case a.outbound <- terminalOutboundMessage{message: message, payload: payload}:
		return nil
	default:
		return errors.New("terminal attachment buffer is full")
	}
}

func (a *terminalAttachment) sendControl(message terminalServerMessage) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return a.send(websocket.MessageText, payload)
}

func (a *terminalAttachment) close(code websocket.StatusCode, reason string) {
	a.closeOnce.Do(func() {
		close(a.done)
		_ = a.conn.Close(code, reason)
		a.conn.CloseNow()
	})
}
