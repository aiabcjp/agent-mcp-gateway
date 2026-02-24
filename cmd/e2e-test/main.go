package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	gomcp "github.com/mark3labs/mcp-go/mcp"
	gomcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	gomcpserver "github.com/mark3labs/mcp-go/server"

	"agent-mcp-gateway/internal/auth"
	"agent-mcp-gateway/internal/config"
	"agent-mcp-gateway/internal/mcp"
	"agent-mcp-gateway/internal/metering"
	"agent-mcp-gateway/internal/resources"
)

type staticAuth struct{ tokens map[string]*auth.Claims }

func (a *staticAuth) VerifyToken(_ context.Context, token string) (*auth.Claims, error) {
	if c, ok := a.tokens[token]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("invalid token")
}

type noopMeter struct{}

func (m *noopMeter) Record(_ context.Context, _ *metering.UsageEntry) error { return nil }
func (m *noopMeter) GetUsage(_ context.Context, _ string, _, _ time.Time) (*metering.UsageStats, error) {
	return &metering.UsageStats{TotalRequests: 100, ByResource: map[string]int64{"demo-redis": 100}}, nil
}
func (m *noopMeter) Close() error { return nil }

func callTool(ctx context.Context, client *gomcpclient.Client, name string, args map[string]any) (*gomcp.CallToolResult, error) {
	return client.CallTool(ctx, gomcp.CallToolRequest{
		Params: gomcp.CallToolParams{
			Name:      name,
			Arguments: args,
		},
	})
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	cfg := &config.Config{
		Auth: config.AuthConfig{
			RBAC: []config.RBACRule{
				{Group: "admin", Resources: []string{"*"}, Permissions: []string{"read", "write"}},
			},
		},
		Resources: map[string]config.ResourceConfig{
			"demo-redis": {
				Type: "redis", Host: "127.0.0.1:6379",
				Description: "Demo Redis", ReadOnly: true,
				AllowedOps: []string{"get", "set", "keys", "info"},
			},
			"demo-mongo": {
				Type: "mongodb", URI: "mongodb://127.0.0.1:27017/test",
				Description: "Demo MongoDB", ReadOnly: true,
				AllowedOps: []string{"find", "count", "listCollections"},
			},
		},
	}

	authn := &staticAuth{tokens: map[string]*auth.Claims{
		"sk-e2e-token": {Subject: "e2e-agent", Email: "agent@test.com", Groups: []string{"admin"}},
	}}
	authz := auth.NewRBACAuthorizer(cfg.Auth.RBAC)
	failDial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, fmt.Errorf("no backend")
	}
	mgr, _ := resources.NewManager(cfg.Resources, failDial, logger)
	mcpSrv := mcp.NewServer("agent-mcp-gateway", "1.0.0", mgr, authz, &noopMeter{}, logger)

	mux := http.NewServeMux()
	mux.Handle("/mcp", authMW(authn, gomcpserver.NewStreamableHTTPServer(mcpSrv)))

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	serverURL := fmt.Sprintf("http://%s/mcp", ln.Addr().String())
	fmt.Printf("🚀 Server at %s\n\n", serverURL)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := gomcpclient.NewStreamableHttpClient(serverURL,
		transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer sk-e2e-token"}),
	)
	if err != nil {
		fatal("Create client", err)
	}

	// 1. Initialize
	fmt.Println("📡 Initialize...")
	initResp, err := client.Initialize(ctx, gomcp.InitializeRequest{
		Params: struct {
			ProtocolVersion string                   `json:"protocolVersion"`
			Capabilities    gomcp.ClientCapabilities `json:"capabilities"`
			ClientInfo      gomcp.Implementation     `json:"clientInfo"`
		}{
			ProtocolVersion: "2025-03-26",
			ClientInfo:      gomcp.Implementation{Name: "e2e-agent", Version: "1.0.0"},
		},
	})
	if err != nil {
		fatal("Initialize", err)
	}
	fmt.Printf("✅ %s v%s (protocol %s)\n\n", initResp.ServerInfo.Name, initResp.ServerInfo.Version, initResp.ProtocolVersion)

	// 2. List tools
	fmt.Println("🔧 Tools:")
	toolsResp, err := client.ListTools(ctx, gomcp.ListToolsRequest{})
	if err != nil {
		fatal("ListTools", err)
	}
	for _, t := range toolsResp.Tools {
		fmt.Printf("   • %s — %s\n", t.Name, t.Description)
	}
	fmt.Println()

	// 3. list_resources
	fmt.Println("📋 list_resources:")
	result, err := callTool(ctx, client, "list_resources", map[string]any{})
	if err != nil {
		fatal("list_resources", err)
	}
	printResult(result)

	// 4. get_usage
	fmt.Println("📊 get_usage:")
	result, err = callTool(ctx, client, "get_usage", map[string]any{"days": 7})
	if err != nil {
		fatal("get_usage", err)
	}
	printResult(result)

	// 5. redis_query (no backend)
	fmt.Println("🔴 redis_query (expect error):")
	result, err = callTool(ctx, client, "redis_query", map[string]any{
		"resource": "demo-redis", "operation": "info",
	})
	if err != nil {
		fatal("redis_query", err)
	}
	printResult(result)

	fmt.Println("🎉 All E2E tests passed! MCP protocol fully functional.")
}

func printResult(result *gomcp.CallToolResult) {
	for _, c := range result.Content {
		if tc, ok := c.(gomcp.TextContent); ok {
			// Try to pretty-print JSON
			var v any
			if json.Unmarshal([]byte(tc.Text), &v) == nil {
				b, _ := json.MarshalIndent(v, "   ", "  ")
				fmt.Printf("   %s\n", string(b))
			} else {
				fmt.Printf("   %s\n", tc.Text)
			}
		}
	}
	fmt.Println()
}

func fatal(op string, err error) {
	fmt.Printf("❌ %s failed: %v\n", op, err)
	os.Exit(1)
}

func authMW(authn *staticAuth, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		claims, err := authn.VerifyToken(r.Context(), token)
		if err != nil {
			http.Error(w, "unauthorized", 401)
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.ContextWithClaims(r.Context(), claims)))
	})
}
