package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"
	"time"

	gomcp "github.com/mark3labs/mcp-go/mcp"

	"qa-mcp-gateway/internal/auth"
	"qa-mcp-gateway/internal/metering"
	"qa-mcp-gateway/internal/resources"
)

// ---------------------------------------------------------------------------
// Mock implementations
// ---------------------------------------------------------------------------

// mockResource implements resources.Resource for testing.
type mockResource struct {
	typ        string
	desc       string
	allowedOps []string
	execResult any
	execErr    error
}

func (m *mockResource) Type() string                { return m.typ }
func (m *mockResource) Description() string         { return m.desc }
func (m *mockResource) AllowedOps() []string        { return m.allowedOps }
func (m *mockResource) Close() error                { return nil }
func (m *mockResource) Execute(_ context.Context, _ string, _ map[string]any) (any, error) {
	return m.execResult, m.execErr
}

// mockAuthorizer implements auth.Authorizer for testing.
type mockAuthorizer struct {
	allowed   bool
	resources []string
}

func (m *mockAuthorizer) Check(_ *auth.Claims, _ string, _ string) bool {
	return m.allowed
}

func (m *mockAuthorizer) AllowedResources(_ *auth.Claims) []string {
	return m.resources
}

// mockMeter implements metering.Meter for testing.
type mockMeter struct {
	recorded []*metering.UsageEntry
	stats    *metering.UsageStats
	err      error
}

func (m *mockMeter) Record(_ context.Context, entry *metering.UsageEntry) error {
	m.recorded = append(m.recorded, entry)
	return m.err
}

func (m *mockMeter) GetUsage(_ context.Context, _ string, _, _ time.Time) (*metering.UsageStats, error) {
	return m.stats, m.err
}

func (m *mockMeter) Close() error { return nil }

// mockManager wraps a map of resources to simulate a resources.Manager.
// Since we cannot easily construct a Manager with injected resources (the
// constructor requires real resource configs), we test handler functions
// directly using the unexported helpers.
type mockManager struct {
	res map[string]*mockResource
}

func (m *mockManager) Get(name string) (resources.Resource, bool) {
	r, ok := m.res[name]
	if !ok {
		return nil, false
	}
	return r, true
}

func (m *mockManager) List() map[string]resources.ResourceInfo {
	result := make(map[string]resources.ResourceInfo, len(m.res))
	for name, r := range m.res {
		result[name] = resources.ResourceInfo{
			Name:        name,
			Type:        r.Type(),
			Description: r.Description(),
			AllowedOps:  r.AllowedOps(),
		}
	}
	return result
}

// resourceGetter is an interface matching the subset of resources.Manager
// methods that the handlers use.
type resourceGetter interface {
	Get(name string) (resources.Resource, bool)
	List() map[string]resources.ResourceInfo
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestNewServer_NotNil(t *testing.T) {
	logger := slog.Default()
	mgr, err := resources.NewManager(nil, nil, logger)
	if err != nil {
		t.Fatalf("unexpected error creating empty manager: %v", err)
	}
	defer mgr.Close()

	authz := &mockAuthorizer{allowed: true, resources: []string{"*"}}
	meter := &mockMeter{stats: &metering.UsageStats{}}

	srv := NewServer("test", "0.0.1", mgr, authz, meter, logger)
	if srv == nil {
		t.Fatal("expected non-nil MCP server")
	}
}

func TestListResourcesHandler_Authorized(t *testing.T) {
	logger := slog.Default()

	mgr, err := resources.NewManager(nil, nil, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer mgr.Close()

	authz := &mockAuthorizer{allowed: true, resources: []string{"*"}}
	handler := makeListResourcesHandler(mgr, authz, logger)

	// Create context with claims.
	claims := &auth.Claims{
		Subject: "user1",
		Email:   "user1@example.com",
		Groups:  []string{"qa"},
		Expiry:  time.Now().Add(time.Hour),
	}
	ctx := auth.ContextWithClaims(context.Background(), claims)

	req := gomcp.CallToolRequest{}
	req.Params.Name = "list_resources"

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
}

func TestListResourcesHandler_Unauthorized(t *testing.T) {
	logger := slog.Default()

	mgr, err := resources.NewManager(nil, nil, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer mgr.Close()

	authz := &mockAuthorizer{allowed: false, resources: nil}
	handler := makeListResourcesHandler(mgr, authz, logger)

	// No claims in context.
	ctx := context.Background()
	req := gomcp.CallToolRequest{}
	req.Params.Name = "list_resources"

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Fatal("expected error result for unauthorized request")
	}
}

func TestQueryHandler_Authorized(t *testing.T) {
	logger := slog.Default()

	mgr, err := resources.NewManager(nil, nil, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer mgr.Close()

	authz := &mockAuthorizer{allowed: true, resources: []string{"test-redis"}}
	meter := &mockMeter{stats: &metering.UsageStats{}}

	handler := makeQueryHandler(mgr, authz, meter, logger)

	claims := &auth.Claims{
		Subject: "user1",
		Email:   "user1@example.com",
		Groups:  []string{"qa"},
		Expiry:  time.Now().Add(time.Hour),
	}
	ctx := auth.ContextWithClaims(context.Background(), claims)

	// The resource doesn't actually exist in the empty manager, so we expect
	// a "not found" error from the handler (not a Go error).
	req := gomcp.CallToolRequest{}
	req.Params.Name = "redis_query"
	req.Params.Arguments = map[string]any{
		"resource":  "test-redis",
		"operation": "get",
		"key":       "mykey",
	}

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// The resource isn't registered, so we expect a tool-level error.
	if !result.IsError {
		t.Fatal("expected tool error for missing resource")
	}
}

func TestQueryHandler_Unauthorized_NoClaims(t *testing.T) {
	logger := slog.Default()

	mgr, err := resources.NewManager(nil, nil, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer mgr.Close()

	authz := &mockAuthorizer{allowed: false}
	meter := &mockMeter{}

	handler := makeQueryHandler(mgr, authz, meter, logger)

	ctx := context.Background()
	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"resource":  "test-redis",
		"operation": "get",
	}

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for missing claims")
	}
}

func TestQueryHandler_Unauthorized_Forbidden(t *testing.T) {
	logger := slog.Default()

	mgr, err := resources.NewManager(nil, nil, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer mgr.Close()

	authz := &mockAuthorizer{allowed: false}
	meter := &mockMeter{}

	handler := makeQueryHandler(mgr, authz, meter, logger)

	claims := &auth.Claims{
		Subject: "user1",
		Email:   "user1@example.com",
		Groups:  []string{"readonly"},
		Expiry:  time.Now().Add(time.Hour),
	}
	ctx := auth.ContextWithClaims(context.Background(), claims)

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"resource":  "test-redis",
		"operation": "get",
	}

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for forbidden access")
	}
}

func TestQueryHandler_MissingResource(t *testing.T) {
	logger := slog.Default()

	mgr, err := resources.NewManager(nil, nil, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer mgr.Close()

	authz := &mockAuthorizer{allowed: true}
	meter := &mockMeter{}

	handler := makeQueryHandler(mgr, authz, meter, logger)

	claims := &auth.Claims{Subject: "u", Email: "u@e.com", Groups: []string{"qa"}}
	ctx := auth.ContextWithClaims(context.Background(), claims)

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"resource":  "",
		"operation": "get",
	}

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for empty resource name")
	}
}

func TestQueryHandler_MissingOperation(t *testing.T) {
	logger := slog.Default()

	mgr, err := resources.NewManager(nil, nil, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer mgr.Close()

	authz := &mockAuthorizer{allowed: true}
	meter := &mockMeter{}

	handler := makeQueryHandler(mgr, authz, meter, logger)

	claims := &auth.Claims{Subject: "u", Email: "u@e.com", Groups: []string{"qa"}}
	ctx := auth.ContextWithClaims(context.Background(), claims)

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"resource": "test-redis",
	}

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing operation")
	}
}

func TestGetUsageHandler_Success(t *testing.T) {
	logger := slog.Default()

	stats := &metering.UsageStats{
		TotalRequests: 42,
		TotalBytes:    1024,
		ByResource:    map[string]int64{"redis": 30, "mongo": 12},
		ByOperation:   map[string]int64{"get": 40, "set": 2},
	}
	meter := &mockMeter{stats: stats}

	handler := makeGetUsageHandler(meter, logger)

	claims := &auth.Claims{
		Subject: "user1",
		Email:   "user1@example.com",
		Groups:  []string{"qa"},
		Expiry:  time.Now().Add(time.Hour),
	}
	ctx := auth.ContextWithClaims(context.Background(), claims)

	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"days": float64(7),
	}

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}

	// Verify the result contains expected data.
	var gotStats metering.UsageStats
	textContent, ok := result.Content[0].(gomcp.TextContent)
	if !ok {
		t.Fatal("expected TextContent in result")
	}
	if err := json.Unmarshal([]byte(textContent.Text), &gotStats); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if gotStats.TotalRequests != 42 {
		t.Errorf("expected TotalRequests=42, got %d", gotStats.TotalRequests)
	}
}

func TestGetUsageHandler_NoClaims(t *testing.T) {
	logger := slog.Default()
	meter := &mockMeter{stats: &metering.UsageStats{}}

	handler := makeGetUsageHandler(meter, logger)

	ctx := context.Background()
	req := gomcp.CallToolRequest{}

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing claims")
	}
}

func TestGetUsageHandler_NilMeter(t *testing.T) {
	logger := slog.Default()

	handler := makeGetUsageHandler(nil, logger)

	claims := &auth.Claims{Subject: "u", Email: "u@e.com", Groups: []string{"qa"}}
	ctx := auth.ContextWithClaims(context.Background(), claims)

	req := gomcp.CallToolRequest{}
	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when metering is disabled")
	}
}

func TestGetUsageHandler_MeterError(t *testing.T) {
	logger := slog.Default()
	meter := &mockMeter{err: fmt.Errorf("db connection failed")}

	handler := makeGetUsageHandler(meter, logger)

	claims := &auth.Claims{Subject: "u", Email: "u@e.com", Groups: []string{"qa"}}
	ctx := auth.ContextWithClaims(context.Background(), claims)

	req := gomcp.CallToolRequest{}
	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when meter fails")
	}
}

func TestGetUsageHandler_DefaultDays(t *testing.T) {
	logger := slog.Default()
	stats := &metering.UsageStats{
		TotalRequests: 0,
		TotalBytes:    0,
		ByResource:    map[string]int64{},
		ByOperation:   map[string]int64{},
	}
	meter := &mockMeter{stats: stats}

	handler := makeGetUsageHandler(meter, logger)

	claims := &auth.Claims{Subject: "u", Email: "u@e.com", Groups: []string{"qa"}}
	ctx := auth.ContextWithClaims(context.Background(), claims)

	// No days parameter provided — should default to 30.
	req := gomcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
}
