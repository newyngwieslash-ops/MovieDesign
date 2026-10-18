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
	"path/filepath"
	"time"

	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/control"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/server"
	"github.com/newyngwieslash-ops/MovieDesign/gateway/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("gateway_stopped", "error", err)
		os.Exit(1)
	}
}

func run() (runErr error) {
	dataDir := flag.String("data-dir", "", "Required application data directory")
	port := flag.Int("port", 8001, "Loopback port; 0 allowed for isolated checks")
	origin := flag.String("origin", "http://tauri.localhost", "Exact permitted WebView origin")
	flag.Parse()
	if *dataDir == "" || *port < 0 || *port > 65535 {
		return errors.New("valid --data-dir and --port required")
	}
	if *origin != "http://tauri.localhost" && *origin != "http://localhost:1420" {
		return errors.New("unsupported origin")
	}
	if err := os.MkdirAll(filepath.Join(*dataDir, "logs"), 0700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(*dataDir, "logs", "gateway.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(logFile, nil)))
	defer func() {
		if runErr != nil {
			slog.Error("gateway_stopped", "error", runErr)
		}
		logFile.Close()
	}()
	// Claim our own port before opening storage; never attach to another service.
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		return fmt.Errorf("bind loopback: %w", err)
	}
	defer listener.Close()
	db, err := store.Open(filepath.Join(*dataDir, "moviedesign.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	workspacePath := filepath.Join(*dataDir, "workspace")
	if err = os.MkdirAll(workspacePath, 0700); err != nil {
		return err
	}
	handler, err := server.New(*origin, db, workspacePath)
	if err != nil {
		return err
	}
	defer handler.Close()
	if bootstrap := os.Getenv("MOVIEDESIGN_AGENT_BOOTSTRAP"); bootstrap != "" {
		if err = handler.StartAgentHost(bootstrap); err != nil {
			_ = os.WriteFile(filepath.Join(bootstrap, "error.txt"), []byte("start_failed"), 0600)
			slog.Error("agent_bootstrap_failed")
		}
	}
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	errorsCh := make(chan error, 2)
	go func() { errorsCh <- httpServer.Serve(listener) }()
	actualPort := listener.Addr().(*net.TCPAddr).Port
	slog.Info("gateway_ready", "port", actualPort)
	go func() { errorsCh <- control.Serve(os.Stdin, os.Stdout, actualPort, handler.Health) }()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	select {
	case err = <-errorsCh:
	case <-ctx.Done():
	}
	handler.BeginShutdown()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if shutdownErr := httpServer.Shutdown(shutdownCtx); shutdownErr != nil {
		_ = httpServer.Close()
		return shutdownErr
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	slog.Info("gateway_shutdown")
	return nil
}
