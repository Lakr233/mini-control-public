package rest

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/Lakr233/minis/server/internal/model"
	"github.com/Lakr233/minis/server/internal/proxy"
	"golang.org/x/crypto/ssh"
)

const (
	defaultTerminalCols = 120
	defaultTerminalRows = 36
	terminalUserVM      = "admin"
	terminalPassVM      = "admin"
	terminalUserHost    = "launchctl"
	terminalPassHost    = "change-me"
)

type terminalClientMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

type terminalServerMessage struct {
	Type      string `json:"type"`
	Message   string `json:"message,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Recovered bool   `json:"recovered,omitempty"`
}

func (h *Handler) handleBrowserTerminalWebSocket(w http.ResponseWriter, r *http.Request) {
	member, ok := currentMember(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	cols := parsePositiveQueryInt(r, "cols", defaultTerminalCols)
	rows := parsePositiveQueryInt(r, "rows", defaultTerminalRows)
	sessionID := strings.TrimSpace(r.URL.Query().Get("session"))
	requestedWorkstationID := strings.TrimSpace(r.URL.Query().Get("workstation_id"))

	browserConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
	if err != nil {
		h.logger.Error("accept browser terminal websocket", "error", err)
		return
	}
	defer browserConn.CloseNow()
	browserConn.SetReadLimit(maxJSONBodyBytes)

	var session *terminalSession
	recovered := false
	if sessionID != "" {
		session = h.terminals.Get(sessionID)
		if session == nil {
			_ = browserConn.Close(websocket.StatusPolicyViolation, "terminal session not found")
			return
		}
		if session.memberID != member.ID {
			_ = browserConn.Close(websocket.StatusPolicyViolation, "terminal session unauthorized")
			return
		}
		recovered = true
	} else {
		workstation, resolveErr := h.resolveMemberWorkstation(r, requestedWorkstationID)
		if resolveErr != nil {
			_ = browserConn.Close(websocket.StatusPolicyViolation, resolveErr.message)
			return
		}
		if workstation.PowerState != model.WorkstationPowerStateRunning {
			_ = browserConn.Close(websocket.StatusPolicyViolation, "workstation not running")
			return
		}

		targetUser, targetPassword := terminalCredentials(proxy.ProxyTargetVM)
		proxySession := h.proxy.Create(proxy.ProxyTargetVM, workstation.ID, workstation.WorkerID, 22)

		sessionCtx, cancel := context.WithCancel(context.Background())
		proxyConn, err := h.dialInternalProxyWebSocket(sessionCtx, proxySession)
		if err != nil {
			cancel()
			h.proxy.Close(proxySession.ID)
			h.logger.Error("dial internal proxy websocket", "session_id", proxySession.ID, "error", err)
			_ = browserConn.Close(websocket.StatusInternalError, "relay connection failed")
			return
		}

		sshConn := websocket.NetConn(sessionCtx, proxyConn, websocket.MessageBinary)
		sshClient, sshSession, sshInput, sshStdout, sshStderr, err := startTerminalSSHSession(
			sshConn,
			targetUser,
			targetPassword,
			cols,
			rows,
		)
		if err != nil {
			cancel()
			sshConn.Close()
			proxyConn.CloseNow()
			h.proxy.Close(proxySession.ID)
			h.logger.Error(
				"start ssh session",
				"target", "vm",
				"workstation_id", workstation.ID,
				"worker_id", workstation.WorkerID,
				"error", err,
			)
			_ = browserConn.Close(websocket.StatusInternalError, "ssh connection failed")
			return
		}

		session = h.terminals.Create(
			member.ID,
			workstation.ID,
			workstation.WorkerID,
			h.logger,
			sshClient,
			sshSession,
			sshInput,
			proxyConn,
			func() {
				cancel()
				sshConn.Close()
				h.proxy.Close(proxySession.ID)
			},
			sshStdout,
			sshStderr,
		)
	}

	err = session.Attach(browserConn, cols, rows, recovered)
	if err == nil ||
		errors.Is(err, context.Canceled) ||
		websocket.CloseStatus(err) == websocket.StatusNormalClosure ||
		websocket.CloseStatus(err) == websocket.StatusGoingAway ||
		errors.Is(err, io.EOF) {
		return
	}

	h.logger.Warn("terminal websocket ended with error", "session_id", session.id, "error", err)
	_ = browserConn.Close(websocket.StatusInternalError, "terminal error")
}

func terminalCredentials(target proxy.ProxyTarget) (string, string) {
	if target == proxy.ProxyTargetHost {
		return terminalUserHost, terminalPassHost
	}
	return terminalUserVM, terminalPassVM
}

func (h *Handler) dialInternalProxyWebSocket(ctx context.Context, session *proxy.ProxySession) (*websocket.Conn, error) {
	internalURL, err := h.internalWebSocketURL("/api/v1/proxy/ws?session=" + url.QueryEscape(session.ID) + "&role=client&token=" + url.QueryEscape(session.Token))
	if err != nil {
		return nil, err
	}

	options := h.internalProxyDialOptions()
	if h.config.TLSEnabled {
		options.HTTPClient = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}
	}

	dialCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(dialCtx, internalURL, options)
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(maxProxyMessageBytes)
	return conn, nil
}

func (h *Handler) internalProxyDialOptions() *websocket.DialOptions {
	options := &websocket.DialOptions{HTTPHeader: http.Header{}}
	if browserHost := normalizeHost(h.config.BrowserPublicHost); browserHost != "" {
		options.Host = browserHost
		options.HTTPHeader.Set("X-Forwarded-Host", browserHost)
	}
	return options
}

func (h *Handler) internalWebSocketURL(path string) (string, error) {
	hostPort, err := normalizeLoopbackHTTPAddr(h.config.HTTPAddr)
	if err != nil {
		return "", err
	}

	scheme := "ws"
	if h.config.TLSEnabled {
		scheme = "wss"
	}

	return scheme + "://" + hostPort + path, nil
}

func normalizeLoopbackHTTPAddr(addr string) (string, error) {
	if strings.TrimSpace(addr) == "" {
		return "", errors.New("http addr is not configured")
	}
	if strings.HasPrefix(addr, ":") {
		return "127.0.0.1" + addr, nil
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port), nil
}

func startTerminalSSHSession(conn net.Conn, user, password string, cols, rows int) (*ssh.Client, *ssh.Session, io.WriteCloser, io.Reader, io.Reader, error) {
	clientConn, chans, reqs, err := ssh.NewClientConn(conn, "127.0.0.1:22", &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	})
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	client := ssh.NewClient(clientConn, chans, reqs)
	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, nil, nil, nil, nil, err
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		client.Close()
		return nil, nil, nil, nil, nil, err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		session.Close()
		client.Close()
		return nil, nil, nil, nil, nil, err
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		client.Close()
		return nil, nil, nil, nil, nil, err
	}

	if err := session.RequestPty("xterm-256color", rows, cols, ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}); err != nil {
		session.Close()
		client.Close()
		return nil, nil, nil, nil, nil, err
	}
	if err := session.Shell(); err != nil {
		session.Close()
		client.Close()
		return nil, nil, nil, nil, nil, err
	}

	return client, session, stdin, stdout, stderr, nil
}
