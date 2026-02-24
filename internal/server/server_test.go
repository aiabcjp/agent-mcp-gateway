package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gomcpserver "github.com/mark3labs/mcp-go/server"

	"qa-mcp-gateway/internal/auth"
	"qa-mcp-gateway/internal/config"
)

// ---------------------------------------------------------------------------
// Mock implementations
// ---------------------------------------------------------------------------

type mockAuthenticator struct {
	claims *auth.Claims
	err    error
}

func (m *mockAuthenticator) VerifyToken(_ context.Context, _ string) (*auth.Claims, error) {
	return m.claims, m.err
}

// ---------------------------------------------------------------------------
// Health endpoint tests
// ---------------------------------------------------------------------------

func TestHealthHandler_ReturnsOK(t *testing.T) {
	s := &Server{
		cfg:    &config.Config{},
		logger: slog.Default(),
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	s.healthHandler(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if result["status"] != "ok" {
		t.Errorf("expected status=ok, got %q", result["status"])
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
}

// ---------------------------------------------------------------------------
// Auth middleware tests
// ---------------------------------------------------------------------------

func TestAuthMiddleware_MissingHeader(t *testing.T) {
	logger := slog.Default()
	authn := &mockAuthenticator{}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := authMiddleware(authn, logger, next)

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_InvalidFormat(t *testing.T) {
	logger := slog.Default()
	authn := &mockAuthenticator{}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := authMiddleware(authn, logger, next)

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_EmptyToken(t *testing.T) {
	logger := slog.Default()
	authn := &mockAuthenticator{}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := authMiddleware(authn, logger, next)

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	logger := slog.Default()
	authn := &mockAuthenticator{
		err: fmt.Errorf("invalid token"),
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := authMiddleware(authn, logger, next)

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer bad-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	logger := slog.Default()
	claims := &auth.Claims{
		Subject: "user1",
		Email:   "user1@example.com",
		Groups:  []string{"qa"},
		Expiry:  time.Now().Add(time.Hour),
	}
	authn := &mockAuthenticator{claims: claims}

	var gotClaims *auth.Claims
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := auth.ClaimsFromContext(r.Context())
		if ok {
			gotClaims = c
		}
		w.WriteHeader(http.StatusOK)
	})

	handler := authMiddleware(authn, logger, next)

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if gotClaims == nil {
		t.Fatal("expected claims in context")
	}
	if gotClaims.Subject != "user1" {
		t.Errorf("expected subject=user1, got %q", gotClaims.Subject)
	}
	if gotClaims.Email != "user1@example.com" {
		t.Errorf("expected email=user1@example.com, got %q", gotClaims.Email)
	}
}

// ---------------------------------------------------------------------------
// Recovery middleware tests
// ---------------------------------------------------------------------------

func TestRecoveryMiddleware_CatchesPanic(t *testing.T) {
	logger := slog.Default()
	panickingHandler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("something went wrong")
	})

	handler := recoveryMiddleware(logger, panickingHandler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
}

func TestRecoveryMiddleware_PassesNormalRequests(t *testing.T) {
	logger := slog.Default()
	normalHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	handler := recoveryMiddleware(logger, normalHandler)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Logging middleware tests
// ---------------------------------------------------------------------------

func TestLoggingMiddleware_RecordsStatus(t *testing.T) {
	logger := slog.Default()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	handler := loggingMiddleware(logger, inner)

	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", rec.Code)
	}
}

func TestLoggingMiddleware_DefaultStatus(t *testing.T) {
	logger := slog.Default()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Writing body without explicit WriteHeader defaults to 200.
		_, _ = w.Write([]byte("ok"))
	})

	handler := loggingMiddleware(logger, inner)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Bootstrap handler tests
// ---------------------------------------------------------------------------

func TestBootstrapHandler_ReturnsJSON(t *testing.T) {
	s := &Server{
		cfg: &config.Config{
			Clients: config.ClientsConfig{
				Version: config.VersionConfig{
					Latest:      "1.2.0",
					Minimum:     "1.0.0",
					DownloadURL: "https://example.com/download",
				},
			},
		},
		logger: slog.Default(),
	}

	// Inject claims into context to simulate authenticated request.
	claims := &auth.Claims{
		Subject: "user1",
		Email:   "user1@example.com",
		Groups:  []string{"qa"},
		Expiry:  time.Now().Add(time.Hour),
	}
	ctx := auth.ContextWithClaims(context.Background(), claims)

	req := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	s.bootstrapHandler(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var body struct {
		Resources []json.RawMessage    `json:"resources"`
		Version   config.VersionConfig `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if body.Version.Latest != "1.2.0" {
		t.Errorf("expected version.latest=1.2.0, got %q", body.Version.Latest)
	}
	if body.Version.Minimum != "1.0.0" {
		t.Errorf("expected version.minimum=1.0.0, got %q", body.Version.Minimum)
	}
}

func TestBootstrapHandler_Unauthorized(t *testing.T) {
	s := &Server{
		cfg:    &config.Config{},
		logger: slog.Default(),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
	rec := httptest.NewRecorder()

	s.bootstrapHandler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// New() constructor tests
// ---------------------------------------------------------------------------

func TestNew_MinimalConfig(t *testing.T) {
	logger := slog.Default()
	cfg := &config.Config{}

	authn := &mockAuthenticator{}
	mcpSrv := gomcpserver.NewMCPServer("test", "0.0.1")

	s := New(cfg, mcpSrv, authn, nil, logger)
	if s == nil {
		t.Fatal("expected non-nil Server")
	}
	if s.httpServer == nil {
		t.Fatal("expected non-nil httpServer")
	}
	// Default listen address should be :8080.
	if s.httpServer.Addr != ":8080" {
		t.Errorf("expected default addr :8080, got %q", s.httpServer.Addr)
	}
	// No TLS config for minimal config.
	if s.httpServer.TLSConfig != nil {
		t.Error("expected nil TLSConfig for minimal config")
	}
}

func TestNew_WithTLSAutoConfig(t *testing.T) {
	logger := slog.Default()
	cfg := &config.Config{
		Server: config.ServerConfig{
			TLS:    "auto",
			Domain: "example.com",
		},
	}

	authn := &mockAuthenticator{}
	mcpSrv := gomcpserver.NewMCPServer("test", "0.0.1")

	s := New(cfg, mcpSrv, authn, nil, logger)
	if s == nil {
		t.Fatal("expected non-nil Server")
	}
	if s.httpServer.TLSConfig == nil {
		t.Fatal("expected TLSConfig to be set for TLS auto mode")
	}
}

func TestNew_CustomListenAddress(t *testing.T) {
	logger := slog.Default()
	cfg := &config.Config{
		Server: config.ServerConfig{
			Listen: "127.0.0.1:9090",
		},
	}

	authn := &mockAuthenticator{}
	mcpSrv := gomcpserver.NewMCPServer("test", "0.0.1")

	s := New(cfg, mcpSrv, authn, nil, logger)
	if s == nil {
		t.Fatal("expected non-nil Server")
	}
	if s.httpServer.Addr != "127.0.0.1:9090" {
		t.Errorf("expected addr 127.0.0.1:9090, got %q", s.httpServer.Addr)
	}
}

// ---------------------------------------------------------------------------
// Start and Shutdown lifecycle test
// ---------------------------------------------------------------------------

func TestStartAndShutdown_Lifecycle(t *testing.T) {
	logger := slog.Default()
	claims := &auth.Claims{
		Subject: "user1",
		Email:   "user1@example.com",
		Groups:  []string{"qa"},
		Expiry:  time.Now().Add(time.Hour),
	}
	authn := &mockAuthenticator{claims: claims}
	cfg := &config.Config{
		Server: config.ServerConfig{
			Listen: "127.0.0.1:0",
		},
	}

	mcpSrv := gomcpserver.NewMCPServer("test", "0.0.1")
	s := New(cfg, mcpSrv, authn, nil, logger)

	// Use a real listener to get an available port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find available port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	// Override the server addr.
	s.httpServer.Addr = addr

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Start(ctx)
	}()

	// Give server time to start.
	time.Sleep(100 * time.Millisecond)

	// Send a health check request.
	resp, err := http.Get("http://" + addr + "/health")
	if err != nil {
		t.Fatalf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 from health, got %d", resp.StatusCode)
	}

	// Shutdown by cancelling context.
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("unexpected error from Start: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down within timeout")
	}
}

func TestShutdown_Direct(t *testing.T) {
	logger := slog.Default()
	cfg := &config.Config{
		Server: config.ServerConfig{
			Listen: "127.0.0.1:0",
		},
	}
	authn := &mockAuthenticator{}
	mcpSrv := gomcpserver.NewMCPServer("test", "0.0.1")
	s := New(cfg, mcpSrv, authn, nil, logger)

	// Calling Shutdown on a server that hasn't started should not panic.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	err := s.Shutdown(ctx)
	if err != nil {
		t.Fatalf("unexpected error from Shutdown: %v", err)
	}
}

// ---------------------------------------------------------------------------
// statusRecorder tests
// ---------------------------------------------------------------------------

func TestStatusRecorder_WriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := &statusRecorder{
		ResponseWriter: rec,
		statusCode:     http.StatusOK,
	}

	sr.WriteHeader(http.StatusNotFound)
	if sr.statusCode != http.StatusNotFound {
		t.Errorf("expected statusCode 404, got %d", sr.statusCode)
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected underlying recorder code 404, got %d", rec.Code)
	}
}

func TestStatusRecorder_WriteHeaderMultiple(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := &statusRecorder{
		ResponseWriter: rec,
		statusCode:     http.StatusOK,
	}

	sr.WriteHeader(http.StatusCreated)
	if sr.statusCode != http.StatusCreated {
		t.Errorf("expected statusCode 201, got %d", sr.statusCode)
	}

	// Second call - statusCode should update (our recorder tracks it).
	sr.WriteHeader(http.StatusBadRequest)
	if sr.statusCode != http.StatusBadRequest {
		t.Errorf("expected statusCode 400 after second WriteHeader, got %d", sr.statusCode)
	}
}

func TestStatusRecorder_DefaultStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := &statusRecorder{
		ResponseWriter: rec,
		statusCode:     http.StatusOK,
	}

	// Without calling WriteHeader, default should be 200.
	if sr.statusCode != http.StatusOK {
		t.Errorf("expected default statusCode 200, got %d", sr.statusCode)
	}
}

// ---------------------------------------------------------------------------
// Bootstrap handler with different HTTP methods
// ---------------------------------------------------------------------------

func TestBootstrapHandler_POST(t *testing.T) {
	s := &Server{
		cfg: &config.Config{
			Clients: config.ClientsConfig{
				Version: config.VersionConfig{
					Latest:  "1.0.0",
					Minimum: "0.9.0",
				},
			},
		},
		logger: slog.Default(),
	}

	claims := &auth.Claims{
		Subject: "user1",
		Email:   "user1@example.com",
		Groups:  []string{"qa"},
		Expiry:  time.Now().Add(time.Hour),
	}
	ctx := auth.ContextWithClaims(context.Background(), claims)

	// POST method should still work (handler doesn't restrict methods).
	req := httptest.NewRequest(http.MethodPost, "/api/bootstrap", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	s.bootstrapHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for POST bootstrap, got %d", rec.Code)
	}

	// Verify response is valid JSON.
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if _, ok := body["version"]; !ok {
		t.Error("expected version key in bootstrap response")
	}
	if _, ok := body["resources"]; !ok {
		t.Error("expected resources key in bootstrap response")
	}
}

func TestBootstrapHandler_ContentType(t *testing.T) {
	s := &Server{
		cfg:    &config.Config{},
		logger: slog.Default(),
	}

	claims := &auth.Claims{
		Subject: "user1",
		Email:   "user1@example.com",
		Groups:  []string{"qa"},
		Expiry:  time.Now().Add(time.Hour),
	}
	ctx := auth.ContextWithClaims(context.Background(), claims)

	req := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	s.bootstrapHandler(rec, req)

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
}

// ---------------------------------------------------------------------------
// Health endpoint accessible without auth (integration test via full server)
// ---------------------------------------------------------------------------

func TestHealthEndpoint_NoAuthRequired(t *testing.T) {
	logger := slog.Default()
	authn := &mockAuthenticator{
		err: fmt.Errorf("invalid token"),
	}
	cfg := &config.Config{}

	mcpSrv := gomcpserver.NewMCPServer("test", "0.0.1")
	s := New(cfg, mcpSrv, authn, nil, logger)

	// Use a real listener to get an available port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find available port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	s.httpServer.Addr = addr

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Start(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	// Health endpoint should be accessible without any Authorization header.
	resp, err := http.Get("http://" + addr + "/health")
	if err != nil {
		t.Fatalf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 from /health without auth, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if result["status"] != "ok" {
		t.Errorf("expected status=ok, got %q", result["status"])
	}

	cancel()
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down")
	}
}

// ---------------------------------------------------------------------------
// Integration-style test: full middleware chain
// ---------------------------------------------------------------------------

func TestFullMiddlewareChain(t *testing.T) {
	logger := slog.Default()
	claims := &auth.Claims{
		Subject: "user1",
		Email:   "user1@example.com",
		Groups:  []string{"qa"},
		Expiry:  time.Now().Add(time.Hour),
	}
	authn := &mockAuthenticator{claims: claims}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := auth.ClaimsFromContext(r.Context())
		if !ok {
			http.Error(w, "no claims", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"user":"%s"}`, c.Email)
	})

	handler := recoveryMiddleware(logger,
		loggingMiddleware(logger,
			authMiddleware(authn, logger, inner)))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var result map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if result["user"] != "user1@example.com" {
		t.Errorf("expected user=user1@example.com, got %q", result["user"])
	}
}
