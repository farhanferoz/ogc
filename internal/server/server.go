// Package server manages the HTTP server lifecycle.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xynogen/ogc/internal/client"
	"github.com/xynogen/ogc/internal/config"
	"github.com/xynogen/ogc/internal/handlers"
	"github.com/xynogen/ogc/internal/metrics"
	"github.com/xynogen/ogc/internal/router"
	"github.com/xynogen/ogc/internal/token"
)

// shutdownTimeout bounds how long a stop waits for in-flight requests to finish.
// Keep it below systemd's TimeoutStopSec so the drain ends before the service
// manager kills the process.
const shutdownTimeout = 40 * time.Second

// Server represents the proxy server.
type Server struct {
	config  *config.Config
	httpSrv *http.Server
	logger  *slog.Logger
}

// NewServer creates a new proxy server.
func NewServer(cfg *config.Config) (*Server, error) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(cfg.Logging.Level),
	}))
	slog.SetDefault(logger)

	// Initialize components.
	tokenCounter, err := token.NewCounter()
	if err != nil {
		return nil, fmt.Errorf("failed to create token counter: %w", err)
	}

	// Create metrics
	metrics := metrics.New()

	upstreamClient := client.NewClient(cfg.Upstream, cfg.APIKey)
	modelRouter := router.NewModelRouter(cfg)
	fallbackHandler := router.NewFallbackHandler(logger, 3, 30*time.Second)

	// Create handlers.
	messagesHandler := handlers.NewMessagesHandler(
		cfg,
		upstreamClient,
		modelRouter,
		fallbackHandler,
		tokenCounter,
		metrics,
	)
	healthHandler := handlers.NewHealthHandler(tokenCounter, fallbackHandler, metrics)

	// Setup router.
	mux := http.NewServeMux()

	// API routes.
	mux.HandleFunc("/v1/messages", messagesHandler.HandleMessages)
	mux.HandleFunc("/v1/messages/count_tokens", healthHandler.HandleCountTokens)
	mux.HandleFunc("/health", healthHandler.HandleHealth)

	// Create HTTP server.
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	httpSrv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}

	return &Server{
		config:  cfg,
		httpSrv: httpSrv,
		logger:  logger,
	}, nil
}

// Start starts the server with graceful shutdown.
func (s *Server) Start() error {
	s.logger.Info("starting ogc proxy",
		"host", s.config.Host,
		"port", s.config.Port,
		"base_url", s.config.Upstream.BaseURL,
		"anthropic_base_url", s.config.Upstream.AnthropicBaseURL,
	)

	ln, err := net.Listen("tcp", s.httpSrv.Addr)
	if err != nil {
		return fmt.Errorf("server failed: %w", err)
	}

	// Graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return s.serve(ctx, ln)
}

// serve handles requests on ln until ctx is done, then waits for in-flight
// requests to finish (up to shutdownTimeout) before returning.
func (s *Server) serve(ctx context.Context, ln net.Listener) error {
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		s.logger.Info("shutting down server...")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := s.httpSrv.Shutdown(shutdownCtx); err != nil {
			s.logger.Error("server shutdown failed", "error", err)
		}
	}()

	if err := s.httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server failed: %w", err)
	}

	// Serve returns as soon as Shutdown begins. Returning before the drain
	// finishes would exit the process and cut off requests still streaming.
	<-shutdownDone

	s.logger.Info("server stopped")
	return nil
}

// WritePID writes the current PID to a file.
func WritePID(path string) error {
	pid := os.Getpid()
	return os.WriteFile(path, []byte(fmt.Sprintf("%d", pid)), 0644)
}

// ReadPID reads the PID from a file.
func ReadPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}

	var pid int
	_, err = fmt.Sscanf(string(data), "%d", &pid)
	return pid, err
}

// parseLogLevel converts a string log level to slog.Level.
func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
