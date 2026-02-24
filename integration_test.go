// Package integration_test provides end-to-end integration tests for the
// agent-gateway. It starts a real HTTP server with mock auth and exercises
// the MCP protocol flow including authentication, tool discovery, and tool
// execution.
package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"agent-gateway/internal/auth"
	"agent-gateway/internal/config"
	"agent-gateway/internal/mcp"
	"agent-gateway/internal/metering"
	"agent-gateway/internal/resources"

	gomcpserver "github.com/mark3labs/mcp-go/server"
)

// staticAuthenticator is a simple token-based authenticator for tests.
type staticAuthenticator struct {
	tokens map[string]*auth.Claims
}

func (a *staticAuthenticator) VerifyToken(_ context.Context, token string) (*auth.Claims, error) {
	if c, ok := a.tokens[token]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("invalid token")
}

func newTestAuthenticator() *staticAuthenticator {
	return &staticAuthenticator{
		tokens: map[string]*auth.Claims{
			"sk-test-admin": {
				Subject: "admin-user",
				Email:   "admin@test.com",
				Groups:  []string{"admin"},
			},
			"sk-test-readonly": {
				Subject: "readonly-user",
				Email:   "readonly@test.com",
				Groups:  []string{"readonly"},
			},
		},
	}
}

// noopMeter is a no-op metering implementation for tests.
type noopMeter struct{}

func (m *noopMeter) Record(_ context.Context, _ *metering.UsageEntry) error { return nil }
func (m *noopMeter) GetUsage(_ context.Context, _ string, _, _ time.Time) (*metering.UsageStats, error) {
	return &metering.UsageStats{
		TotalRequests: 42,
		TotalBytes:    1024,
		ByResource:    map[string]int64{"mock-redis": 30, "mock-mysql": 12},
		ByOperation:   map[string]int64{"get": 20, "select": 12, "info": 10},
	}, nil
}
func (m *noopMeter) Close() error { return nil }

// testConfig returns a minimal config for integration testing.
func testConfig() *config.Config {
	return &config.Config{
		Server: config.ServerConfig{
			Listen: "127.0.0.1:0",
		},
		Auth: config.AuthConfig{
			RBAC: []config.RBACRule{
				{
					Group:       "admin",
					Resources:   []string{"*"},
					Permissions: []string{"read", "write"},
				},
				{
					Group:       "readonly",
					Resources:   []string{"mock-redis"},
					Permissions: []string{"read"},
				},
			},
		},
		Resources: map[string]config.ResourceConfig{
			"mock-redis": {
				Type:        "redis",
				Host:        "127.0.0.1:16379",
				Description: "Mock Redis for testing",
				ReadOnly:    true,
				AllowedOps:  []string{"get", "set", "keys", "info"},
			},
			"mock-mysql": {
				Type:        "mysql",
				DSN:         "test:test@tcp(127.0.0.1:13306)/testdb",
				Description: "Mock MySQL for testing",
				ReadOnly:    true,
				AllowedOps:  []string{"select", "show"},
			},
		},
		Clients: config.ClientsConfig{
			Version: config.VersionConfig{
				Latest:      "1.0.0",
				Minimum:     "0.9.0",
				DownloadURL: "https://example.com/downloads/",
			},
		},
	}
}

// startTestServer creates and starts a full integration test server.
func startTestServer(t *testing.T) (baseURL string, cleanup func()) {
	t.Helper()

	cfg := testConfig()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	authn := newTestAuthenticator()
	authz := auth.NewRBACAuthorizer(cfg.Auth.RBAC)
	meter := &noopMeter{}

	// Resource manager with a custom dialer that always fails (no real DBs).
	failDial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, fmt.Errorf("no real backend in tests")
	}
	mgr, err := resources.NewManager(cfg.Resources, failDial, logger)
	if err != nil {
		t.Fatalf("failed to create resource manager: %v", err)
	}

	mcpSrv := mcp.NewServer("agent-gateway-test", "0.0.0-test", mgr, authz, meter, logger)

	mux := http.NewServeMux()
	streamableServer := gomcpserver.NewStreamableHTTPServer(mcpSrv)

	authedMCP := testAuthMiddleware(authn, streamableServer)
	mux.Handle("/mcp", authedMCP)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.Handle("/api/bootstrap", testAuthMiddleware(authn, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"resources": cfg.Resources,
			"client_version": map[string]string{
				"latest":  cfg.Clients.Version.Latest,
				"minimum": cfg.Clients.Version.Minimum,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)

	return fmt.Sprintf("http://%s", ln.Addr().String()), func() {
		srv.Close()
		ln.Close()
	}
}

func testAuthMiddleware(authn *staticAuthenticator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" {
			http.Error(w, `{"error":"missing authorization"}`, http.StatusUnauthorized)
			return
		}
		claims, err := authn.VerifyToken(r.Context(), token)
		if err != nil {
			http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
			return
		}
		ctx := auth.ContextWithClaims(r.Context(), claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// mcpRequest is a helper that sends an MCP JSON-RPC request.
func mcpRequest(t *testing.T, baseURL, token, sessionID string, id int, method string, params map[string]any) (int, string, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	req, _ := http.NewRequest("POST", baseURL+"/mcp", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("request %s failed: %v", method, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(respBody), resp.Header.Get("Mcp-Session-Id")
}

// mcpInit initializes an MCP session and returns the session ID.
func mcpInit(t *testing.T, baseURL, token string) string {
	t.Helper()
	_, _, sid := mcpRequest(t, baseURL, token, "", 1, "initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":   map[string]any{},
		"clientInfo":     map[string]string{"name": "test", "version": "1.0.0"},
	})
	return sid
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestInteg_HealthEndpoint(t *testing.T) {
	baseURL, cleanup := startTestServer(t)
	defer cleanup()

	resp, err := http.Get(baseURL + "/health")
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", body)
	}
}

func TestInteg_AuthRejection(t *testing.T) {
	baseURL, cleanup := startTestServer(t)
	defer cleanup()

	tests := []struct {
		name   string
		token  string
		status int
	}{
		{"no token", "", 401},
		{"invalid token", "sk-wrong-token", 401},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", baseURL+"/mcp", strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != tt.status {
				t.Fatalf("expected %d, got %d", tt.status, resp.StatusCode)
			}
		})
	}
}

func TestInteg_BootstrapEndpoint(t *testing.T) {
	baseURL, cleanup := startTestServer(t)
	defer cleanup()

	req, _ := http.NewRequest("GET", baseURL+"/api/bootstrap", nil)
	req.Header.Set("Authorization", "Bearer sk-test-admin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/bootstrap failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)

	cv, ok := result["client_version"].(map[string]any)
	if !ok {
		t.Fatal("client_version missing")
	}
	if cv["latest"] != "1.0.0" || cv["minimum"] != "0.9.0" {
		t.Fatalf("unexpected version info: %v", cv)
	}
}

func TestInteg_MCPInitialize(t *testing.T) {
	baseURL, cleanup := startTestServer(t)
	defer cleanup()

	status, body, _ := mcpRequest(t, baseURL, "sk-test-admin", "", 1, "initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":   map[string]any{},
		"clientInfo":     map[string]string{"name": "test", "version": "1.0.0"},
	})

	if status != 200 {
		t.Fatalf("expected 200, got %d: %s", status, body)
	}
	if !strings.Contains(body, "agent-gateway-test") {
		t.Fatalf("expected server name in response, got: %s", body)
	}
}

func TestInteg_MCPToolsList(t *testing.T) {
	baseURL, cleanup := startTestServer(t)
	defer cleanup()

	sid := mcpInit(t, baseURL, "sk-test-admin")

	_, body, _ := mcpRequest(t, baseURL, "sk-test-admin", sid, 2, "tools/list", map[string]any{})

	expectedTools := []string{"list_resources", "redis_query", "mongo_query", "mysql_query", "es_search", "get_usage"}
	for _, tool := range expectedTools {
		if !strings.Contains(body, tool) {
			t.Errorf("expected tool %q in response, got: %s", tool, body)
		}
	}
}

func TestInteg_CallListResources_Admin(t *testing.T) {
	baseURL, cleanup := startTestServer(t)
	defer cleanup()

	sid := mcpInit(t, baseURL, "sk-test-admin")

	_, body, _ := mcpRequest(t, baseURL, "sk-test-admin", sid, 2, "tools/call", map[string]any{
		"name":      "list_resources",
		"arguments": map[string]any{},
	})

	// Admin should see both resources.
	if !strings.Contains(body, "mock-redis") || !strings.Contains(body, "mock-mysql") {
		t.Fatalf("admin should see all resources, got: %s", body)
	}
}

func TestInteg_CallListResources_Readonly(t *testing.T) {
	baseURL, cleanup := startTestServer(t)
	defer cleanup()

	sid := mcpInit(t, baseURL, "sk-test-readonly")

	_, body, _ := mcpRequest(t, baseURL, "sk-test-readonly", sid, 2, "tools/call", map[string]any{
		"name":      "list_resources",
		"arguments": map[string]any{},
	})

	// Readonly user should only see mock-redis.
	if !strings.Contains(body, "mock-redis") {
		t.Fatalf("readonly user should see mock-redis, got: %s", body)
	}
	if strings.Contains(body, "mock-mysql") {
		t.Fatalf("readonly user should NOT see mock-mysql, got: %s", body)
	}
}

func TestInteg_RBAC_ForbiddenResource(t *testing.T) {
	baseURL, cleanup := startTestServer(t)
	defer cleanup()

	sid := mcpInit(t, baseURL, "sk-test-readonly")

	_, body, _ := mcpRequest(t, baseURL, "sk-test-readonly", sid, 2, "tools/call", map[string]any{
		"name": "mysql_query",
		"arguments": map[string]any{
			"resource":  "mock-mysql",
			"operation": "select",
			"query":     "SELECT 1",
		},
	})

	if !strings.Contains(body, "forbidden") {
		t.Fatalf("expected forbidden for mock-mysql access by readonly user, got: %s", body)
	}
}

func TestInteg_GetUsage(t *testing.T) {
	baseURL, cleanup := startTestServer(t)
	defer cleanup()

	sid := mcpInit(t, baseURL, "sk-test-admin")

	_, body, _ := mcpRequest(t, baseURL, "sk-test-admin", sid, 2, "tools/call", map[string]any{
		"name":      "get_usage",
		"arguments": map[string]any{"days": 7},
	})

	if !strings.Contains(body, "total_requests") {
		t.Fatalf("expected usage stats in response, got: %s", body)
	}
}

func TestInteg_NonexistentResource(t *testing.T) {
	baseURL, cleanup := startTestServer(t)
	defer cleanup()

	sid := mcpInit(t, baseURL, "sk-test-admin")

	_, body, _ := mcpRequest(t, baseURL, "sk-test-admin", sid, 2, "tools/call", map[string]any{
		"name": "redis_query",
		"arguments": map[string]any{
			"resource":  "nonexistent",
			"operation": "get",
			"key":       "test",
		},
	})

	if !strings.Contains(body, "not found") {
		t.Fatalf("expected 'not found' error, got: %s", body)
	}
}

func TestInteg_ConcurrentRequests(t *testing.T) {
	baseURL, cleanup := startTestServer(t)
	defer cleanup()

	const n = 20
	errCh := make(chan error, n)

	for i := 0; i < n; i++ {
		go func(id int) {
			sid := mcpInit(t, baseURL, "sk-test-admin")
			status, body, _ := mcpRequest(t, baseURL, "sk-test-admin", sid, 2, "tools/list", map[string]any{})
			if status != 200 {
				errCh <- fmt.Errorf("goroutine %d: status %d, body: %s", id, status, body)
				return
			}
			if !strings.Contains(body, "list_resources") {
				errCh <- fmt.Errorf("goroutine %d: missing tools in response", id)
				return
			}
			errCh <- nil
		}(i)
	}

	for i := 0; i < n; i++ {
		if err := <-errCh; err != nil {
			t.Error(err)
		}
	}
}
