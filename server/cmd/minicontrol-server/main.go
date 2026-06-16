package main

import (
	"context"
	"flag"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Lakr233/minis/server/internal/api/rest"
	"github.com/Lakr233/minis/server/internal/config"
	"github.com/Lakr233/minis/server/internal/db"
	"github.com/Lakr233/minis/server/internal/proxy"
	"github.com/Lakr233/minis/server/internal/release"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Database
	database, err := db.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("connect to database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	// Proxy session manager
	proxyMgr := proxy.NewSessionManager(logger)
	go proxyMgr.StartReaper(ctx)

	// Release store (manual install / distribution)
	var releaseStore *release.Store
	if cfg.BinaryStoragePath != "" {
		var err error
		releaseStore, err = release.NewStore(database, cfg.BinaryStoragePath, cfg.RequiredSigningIdentity, logger)
		if err != nil {
			logger.Error("init release store", "error", err)
			os.Exit(1)
		}
		logger.Info("release store initialized",
			"path", cfg.BinaryStoragePath,
			"required_identity", cfg.RequiredSigningIdentity,
		)
	}

	// REST server
	installScript := ""
	for _, path := range []string{"/etc/minicontrol/install.sh", "install/setup-host.sh"} {
		if data, err := os.ReadFile(path); err == nil {
			installScript = string(data)
			break
		}
	}
	if installScript == "" {
		logger.Warn("install script not found")
	}

	restHandler, err := rest.NewHandler(database, installScript, proxyMgr, releaseStore, rest.HandlerConfig{
		AdminToken:        cfg.AdminToken,
		EnrollmentToken:   cfg.EnrollmentToken,
		PoolSize:          cfg.DefaultPoolSize,
		BaseImage:         cfg.DefaultBaseImage,
		HTTPAddr:          cfg.HTTPAddr,
		TLSEnabled:        cfg.TLSCertFile != "" && cfg.TLSKeyFile != "",
		WorkerPublicHost:  cfg.WorkerPublicHost,
		BrowserPublicHost: cfg.BrowserPublicHost,
		AccessTeamDomain:  cfg.AccessTeamDomain,
		AccessAUD:         cfg.AccessAUD,
		AccessIssuerURL:   cfg.AccessIssuerURL,
		AccessJWKSURL:     cfg.AccessJWKSURL,
		AccessAllowedEmailDomain: cfg.AccessAllowedEmailDomain,
	}, logger)
	if err != nil {
		logger.Error("init rest handler", "error", err)
		os.Exit(1)
	}
	mux := http.NewServeMux()
	restHandler.RegisterRoutes(mux)
	if frontendFS, frontendDir := findFrontendAssets(logger); frontendFS != nil {
		logger.Info("frontend assets enabled", "path", frontendDir)
		mux.Handle("/", rest.NewStaticHandler(frontendFS, cfg.BrowserPublicHost, logger))
	} else {
		logger.Warn("frontend assets not found; root path will not serve web UI")
	}
	httpServer := &http.Server{Addr: cfg.HTTPAddr, Handler: mux}

	// Start all components
	go func() {
		if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
			logger.Info("https server starting", "addr", cfg.HTTPAddr, "cert", cfg.TLSCertFile)
			if err := httpServer.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile); err != http.ErrServerClosed {
				logger.Error("https server", "error", err)
				cancel()
			}
		} else {
			logger.Info("http server starting", "addr", cfg.HTTPAddr)
			if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
				logger.Error("http server", "error", err)
				cancel()
			}
		}
	}()
	logger.Info("minicontrol server started",
		"http_addr", cfg.HTTPAddr,
	)

	<-ctx.Done()

	logger.Info("shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Warn("http shutdown error", "error", err)
	}
	logger.Info("server stopped")
}

func findFrontendAssets(logger *slog.Logger) (fs.FS, string) {
	candidates := []string{
		"/var/lib/minicontrol/frontend",
		"../frontend/dist",
		"frontend/dist",
	}

	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			if logger != nil {
				logger.Debug("found frontend assets", "path", candidate)
			}
			return os.DirFS(candidate), candidate
		}

		absCandidate, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if info, err := os.Stat(absCandidate); err == nil && info.IsDir() {
			if logger != nil {
				logger.Debug("found frontend assets", "path", absCandidate)
			}
			return os.DirFS(absCandidate), absCandidate
		}
	}

	return nil, ""
}
