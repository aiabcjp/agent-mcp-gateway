// Package server provides the HTTP server that wraps the MCP server with
// authentication, logging, and recovery middleware.
package server

import (
	"context"
	"encoding/json"
	"fmt"
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
	mux.HandleFunc("/device", s.deviceVerificationPage)

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

// deviceVerificationPage serves an HTML page for the OAuth 2.0 Device Code flow.
// On headless machines, the CLI shows a URL pointing here. The user opens it on
// any device with a browser and is redirected to the Okta SSO login. This is the
// industry-standard approach used by GitHub CLI, Azure CLI, and AWS SSO.
func (s *Server) deviceVerificationPage(w http.ResponseWriter, r *http.Request) {
	userCode := r.URL.Query().Get("user_code")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	page := `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Agent MCP Gateway — Device Authorization</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
         display: flex; justify-content: center; align-items: center; min-height: 100vh;
         background: #0f172a; color: #e2e8f0; }
  .card { background: #1e293b; border-radius: 12px; padding: 48px; max-width: 480px;
          text-align: center; box-shadow: 0 25px 50px rgba(0,0,0,0.5); }
  h1 { font-size: 24px; margin-bottom: 8px; color: #f8fafc; }
  .subtitle { color: #94a3b8; margin-bottom: 32px; }
  .code-box { background: #0f172a; border: 2px solid #3b82f6; border-radius: 8px;
              padding: 16px 24px; font-size: 32px; font-family: 'SF Mono', Monaco, monospace;
              letter-spacing: 8px; color: #60a5fa; margin: 24px 0; cursor: pointer;
              transition: border-color 0.2s; user-select: all; }
  .code-box:hover { border-color: #60a5fa; }
  .copied { font-size: 14px; color: #22c55e; opacity: 0; transition: opacity 0.3s; }
  .copied.show { opacity: 1; }
  .instructions { color: #94a3b8; font-size: 14px; line-height: 1.8; margin-top: 24px;
                  text-align: left; }
  .instructions li { margin-bottom: 4px; }
  .no-code { color: #94a3b8; font-size: 16px; margin: 24px 0; }
  .footer { margin-top: 32px; color: #475569; font-size: 12px; }
</style>
</head>
<body>
<div class="card">
  <h1>Agent MCP Gateway</h1>
  <p class="subtitle">Device Authorization</p>`

	if userCode != "" {
		page += fmt.Sprintf(`
  <p>Enter this code to authorize your device:</p>
  <div class="code-box" onclick="copyCode()" id="code">%s</div>
  <p class="copied" id="copied">Copied to clipboard!</p>
  <ol class="instructions">
    <li>Copy the code above</li>
    <li>Click "Continue" to sign in with your identity provider</li>
    <li>Paste the code when prompted</li>
    <li>Return to your terminal — it will connect automatically</li>
  </ol>
  <script>
    function copyCode() {
      navigator.clipboard.writeText(document.getElementById('code').textContent.trim());
      var el = document.getElementById('copied');
      el.classList.add('show');
      setTimeout(function(){ el.classList.remove('show'); }, 2000);
    }
  </script>`, userCode)
	} else {
		page += `
  <p class="no-code">Run <code>agent-mcp-gateway connect</code> on your server to get a device code.</p>`
	}

	page += `
  <p class="footer">Agent MCP Gateway — Secure database access for AI agents</p>
</div>
</body>
</html>`

	_, _ = fmt.Fprint(w, page)
}

// healthHandler returns a simple health check response.
func (s *Server) healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
