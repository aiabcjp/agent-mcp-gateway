// Package server provides the HTTP server that wraps the MCP server with
// authentication, logging, and recovery middleware.
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	gomcpserver "github.com/mark3labs/mcp-go/server"
	"golang.org/x/crypto/acme/autocert"

	"agent-mcp-gateway/internal/auth"
	"agent-mcp-gateway/internal/config"
	"agent-mcp-gateway/internal/metering"
	"agent-mcp-gateway/internal/resources"
)

// Server wraps an HTTP server with middleware for MCP gateway operation.
type Server struct {
	httpServer *http.Server
	cfg        *config.Config
	authn      auth.Authenticator
	meter      metering.Meter
	logger     *slog.Logger
}

// New creates a new Server with the given configuration and dependencies. It
// sets up HTTP routing and middleware, mounting the MCP streamable HTTP handler
// behind authentication.
func New(
	cfg *config.Config,
	mcpHandler *gomcpserver.MCPServer,
	authn auth.Authenticator,
	meter metering.Meter,
	logger *slog.Logger,
) *Server {
	s := &Server{
		cfg:    cfg,
		authn:  authn,
		meter:  meter,
		logger: logger,
	}

	mux := http.NewServeMux()

	// Create the streamable HTTP handler from the MCP server.
	streamableServer := gomcpserver.NewStreamableHTTPServer(mcpHandler)

	// Wrap the MCP handler in auth middleware.
	authedMCP := authMiddleware(authn, logger, streamableServer)

	// Mount routes.
	mux.Handle("/mcp", authedMCP)
	mux.HandleFunc("/health", s.healthHandler)
	mux.Handle("/api/bootstrap", authMiddleware(authn, logger, http.HandlerFunc(s.bootstrapHandler)))

	// Build the full middleware chain: recovery -> logging -> mux.
	handler := recoveryMiddleware(logger, loggingMiddleware(logger, mux))

	listen := cfg.Server.Listen
	if listen == "" {
		listen = ":8080"
	}

	s.httpServer = &http.Server{
		Addr:              listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Configure TLS if requested.
	if cfg.Server.TLS == "auto" && cfg.Server.Domain != "" {
		m := &autocert.Manager{
			Cache:      autocert.DirCache("cert-cache"),
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(cfg.Server.Domain),
		}
		s.httpServer.TLSConfig = m.TLSConfig()
	}

	return s
}

// Start starts the HTTP server in a goroutine and blocks until ctx is
// cancelled, at which point it initiates a graceful shutdown.
func (s *Server) Start(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		s.logger.Info("starting server", "addr", s.httpServer.Addr)
		var err error
		if s.httpServer.TLSConfig != nil {
			err = s.httpServer.ListenAndServeTLS("", "")
		} else {
			err = s.httpServer.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		s.logger.Info("shutting down server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return s.httpServer.Shutdown(shutdownCtx)
	}
}

// Shutdown gracefully shuts down the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// bootstrapHandler returns client configuration including available resources
// and version information.
func (s *Server) bootstrapHandler(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.ClaimsFromContext(r.Context())
	if !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	_ = claims // Claims are present — the user is authenticated.

	type bootstrapResponse struct {
		Resources []resources.ResourceInfo `json:"resources"`
		Version   config.VersionConfig     `json:"version"`
	}

	resp := bootstrapResponse{
		Resources: []resources.ResourceInfo{},
		Version:   s.cfg.Clients.Version,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.logger.Error("failed to encode bootstrap response", "error", err)
	}
}

// healthHandler returns a simple health check response.
func (s *Server) healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
