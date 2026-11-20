package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"exa/internal/api"
	"exa/internal/auth"
	"exa/internal/db"
	"exa/internal/monitor"
	"exa/internal/notify"
	"exa/web"
)

var version = "0.1.0"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	port := flag.String("port", envOrDefault("PORT", "8080"), "HTTP listen port")
	host := flag.String("host", envOrDefault("HOST", "0.0.0.0"), "HTTP listen host")
	dataDir := flag.String("data-dir", envOrDefault("DATA_DIR", "./data"), "SQLite data directory")
	baseURL := flag.String("base-url", envOrDefault("BASE_URL", ""), "Public base URL")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("exa", version)
		return
	}

	store, err := db.Open(*dataDir)
	if err != nil {
		logger.Error("failed to open database", "err", err)
		os.Exit(1)
	}
	defer func() {
		if err := store.Close(); err != nil {
			logger.Error("failed to close database", "err", err)
		}
	}()

	trimmedBaseURL := strings.TrimRight(strings.TrimSpace(*baseURL), "/")
	if trimmedBaseURL != "" {
		if err := store.SetSetting("base_url", trimmedBaseURL); err != nil {
			logger.Warn("failed to persist base_url setting", "err", err)
		}
	}

	cookieSecure := strings.HasPrefix(strings.ToLower(trimmedBaseURL), "https://")
	if envCS := strings.TrimSpace(os.Getenv("COOKIE_SECURE")); envCS != "" {
		cookieSecure = parseBoolEnv(envCS)
	}
	trustProxy := parseBoolEnv(os.Getenv("TRUST_PROXY"))

	staticFS, err := web.DistFS()
	if err != nil {
		logger.Error("failed to load embedded web assets", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	authSvc := auth.NewService(store)
	authSvc.SetCookieSecure(cookieSecure)
	authSvc.SetTrustProxy(trustProxy)
	notifier := notify.NewDispatcher(store)
	sseHub := api.NewSSEHub()
	manager := monitor.NewManager(store, notifier, sseHub)
	manager.Start(ctx)
	defer manager.Stop()

	handler := api.NewRouter(api.RouterConfig{
		Store:      store,
		Auth:       authSvc,
		Monitors:   manager,
		Notifier:   notifier,
		SSE:        sseHub,
		StaticFS:   staticFS,
		Version:    version,
		StartedAt:  time.Now().UTC(),
		TrustProxy: trustProxy,
	})

	addr := net.JoinHostPort(*host, *port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("starting exa server", "addr", addr, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			logger.Error("http server error", "err", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		logger.Info("shutting down exa server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown error", "err", err)
		}
	}
}

func envOrDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func parseBoolEnv(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
