package resources

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"agent-gateway/internal/config"
)

// mockResource is a test double that implements the Resource interface.
type mockResource struct {
	typeName   string
	desc       string
	allowedOps []string
	closeCalled bool
	closeErr    error
	execResult  any
	execErr     error
}

func (m *mockResource) Type() string           { return m.typeName }
func (m *mockResource) Description() string     { return m.desc }
func (m *mockResource) AllowedOps() []string    { return m.allowedOps }
func (m *mockResource) Close() error {
	m.closeCalled = true
	return m.closeErr
}
func (m *mockResource) Execute(_ context.Context, _ string, _ map[string]any) (any, error) {
	return m.execResult, m.execErr
}

func TestIsOpAllowed(t *testing.T) {
	tests := []struct {
		name    string
		allowed []string
		op      string
		want    bool
	}{
		{
			name:    "empty allowed list permits all",
			allowed: []string{},
			op:      "get",
			want:    true,
		},
		{
			name:    "nil allowed list permits all",
			allowed: nil,
			op:      "set",
			want:    true,
		},
		{
			name:    "op in allowed list",
			allowed: []string{"get", "set", "keys"},
			op:      "set",
			want:    true,
		},
		{
			name:    "op not in allowed list",
			allowed: []string{"get", "set"},
			op:      "del",
			want:    false,
		},
		{
			name:    "case insensitive match",
			allowed: []string{"GET", "SET"},
			op:      "get",
			want:    true,
		},
		{
			name:    "case insensitive match reverse",
			allowed: []string{"get", "set"},
			op:      "GET",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isOpAllowed(tt.allowed, tt.op)
			if got != tt.want {
				t.Errorf("isOpAllowed(%v, %q) = %v, want %v", tt.allowed, tt.op, got, tt.want)
			}
		})
	}
}

func TestNewManagerEmptyConfigs(t *testing.T) {
	logger := slog.Default()

	// Empty config map should produce an empty manager without error.
	m, err := NewManager(map[string]config.ResourceConfig{}, nil, logger)
	if err != nil {
		t.Fatalf("NewManager with empty configs: unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("NewManager returned nil manager")
	}
	if len(m.resources) != 0 {
		t.Errorf("expected 0 resources, got %d", len(m.resources))
	}
}

func TestNewManagerNilConfigs(t *testing.T) {
	m, err := NewManager(nil, nil, nil)
	if err != nil {
		t.Fatalf("NewManager with nil configs: unexpected error: %v", err)
	}
	if m == nil {
		t.Fatal("NewManager returned nil manager")
	}
}

func TestNewManagerUnsupportedType(t *testing.T) {
	cfgs := map[string]config.ResourceConfig{
		"test": {
			Type:        "unsupported",
			Description: "test resource",
		},
	}

	_, err := NewManager(cfgs, nil, slog.Default())
	if err == nil {
		t.Fatal("expected error for unsupported resource type, got nil")
	}
}

func TestNewManagerRedisMissingHost(t *testing.T) {
	cfgs := map[string]config.ResourceConfig{
		"test-redis": {
			Type:        "redis",
			Description: "test redis",
			// No Host set - should fail.
		},
	}

	_, err := NewManager(cfgs, nil, slog.Default())
	if err == nil {
		t.Fatal("expected error for redis resource without host, got nil")
	}
}

func TestNewManagerMySQLMissingDSN(t *testing.T) {
	cfgs := map[string]config.ResourceConfig{
		"test-mysql": {
			Type:        "mysql",
			Description: "test mysql",
			// No DSN set - should fail.
		},
	}

	_, err := NewManager(cfgs, nil, slog.Default())
	if err == nil {
		t.Fatal("expected error for mysql resource without DSN, got nil")
	}
}

func TestNewManagerMongoMissingURI(t *testing.T) {
	cfgs := map[string]config.ResourceConfig{
		"test-mongo": {
			Type:        "mongodb",
			Description: "test mongo",
			// No URI set - should fail.
		},
	}

	_, err := NewManager(cfgs, nil, slog.Default())
	if err == nil {
		t.Fatal("expected error for mongodb resource without URI, got nil")
	}
}

func TestNewManagerESMissingURL(t *testing.T) {
	cfgs := map[string]config.ResourceConfig{
		"test-es": {
			Type:        "elasticsearch",
			Description: "test es",
			// No URL set - should fail.
		},
	}

	_, err := NewManager(cfgs, nil, slog.Default())
	if err == nil {
		t.Fatal("expected error for elasticsearch resource without URL, got nil")
	}
}

func TestManagerGetExisting(t *testing.T) {
	mock := &mockResource{
		typeName: "test",
		desc:     "test resource",
	}

	m := &Manager{
		resources: map[string]Resource{
			"test-res": mock,
		},
		configs: map[string]config.ResourceConfig{
			"test-res": {Type: "test", Description: "test resource"},
		},
		logger: slog.Default(),
	}

	r, ok := m.Get("test-res")
	if !ok {
		t.Fatal("expected to find resource 'test-res'")
	}
	if r != mock {
		t.Error("returned resource does not match expected")
	}
}

func TestManagerGetNonExisting(t *testing.T) {
	m := &Manager{
		resources: map[string]Resource{},
		configs:   map[string]config.ResourceConfig{},
		logger:    slog.Default(),
	}

	r, ok := m.Get("nonexistent")
	if ok {
		t.Fatal("expected not to find resource 'nonexistent'")
	}
	if r != nil {
		t.Error("expected nil resource for non-existing key")
	}
}

func TestManagerList(t *testing.T) {
	mock1 := &mockResource{
		typeName:   "redis",
		desc:       "Redis cache",
		allowedOps: []string{"get", "set"},
	}
	mock2 := &mockResource{
		typeName:   "mysql",
		desc:       "MySQL DB",
		allowedOps: []string{"select"},
	}

	m := &Manager{
		resources: map[string]Resource{
			"cache":   mock1,
			"primary": mock2,
		},
		configs: map[string]config.ResourceConfig{
			"cache":   {Type: "redis", Description: "Redis cache", ReadOnly: false, AllowedOps: []string{"get", "set"}},
			"primary": {Type: "mysql", Description: "MySQL DB", ReadOnly: true, AllowedOps: []string{"select"}},
		},
		logger: slog.Default(),
	}

	list := m.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 resources in list, got %d", len(list))
	}

	cacheInfo, ok := list["cache"]
	if !ok {
		t.Fatal("expected 'cache' in list")
	}
	if cacheInfo.Type != "redis" {
		t.Errorf("expected type 'redis', got %q", cacheInfo.Type)
	}
	if cacheInfo.Description != "Redis cache" {
		t.Errorf("expected description 'Redis cache', got %q", cacheInfo.Description)
	}
	if cacheInfo.ReadOnly {
		t.Error("expected ReadOnly to be false for 'cache'")
	}
	if len(cacheInfo.AllowedOps) != 2 {
		t.Errorf("expected 2 allowed ops for 'cache', got %d", len(cacheInfo.AllowedOps))
	}

	primaryInfo, ok := list["primary"]
	if !ok {
		t.Fatal("expected 'primary' in list")
	}
	if primaryInfo.Type != "mysql" {
		t.Errorf("expected type 'mysql', got %q", primaryInfo.Type)
	}
	if !primaryInfo.ReadOnly {
		t.Error("expected ReadOnly to be true for 'primary'")
	}
}

func TestManagerClose(t *testing.T) {
	mock1 := &mockResource{typeName: "redis"}
	mock2 := &mockResource{typeName: "mysql"}

	m := &Manager{
		resources: map[string]Resource{
			"r1": mock1,
			"r2": mock2,
		},
		configs: map[string]config.ResourceConfig{},
		logger:  slog.Default(),
	}

	err := m.Close()
	if err != nil {
		t.Fatalf("unexpected error from Close: %v", err)
	}

	if !mock1.closeCalled {
		t.Error("expected Close to be called on mock1")
	}
	if !mock2.closeCalled {
		t.Error("expected Close to be called on mock2")
	}
}

func TestManagerCloseWithError(t *testing.T) {
	mock1 := &mockResource{typeName: "redis", closeErr: fmt.Errorf("close error")}
	mock2 := &mockResource{typeName: "mysql"}

	m := &Manager{
		resources: map[string]Resource{
			"r1": mock1,
			"r2": mock2,
		},
		configs: map[string]config.ResourceConfig{},
		logger:  slog.Default(),
	}

	err := m.Close()
	if err == nil {
		t.Fatal("expected error from Close, got nil")
	}
}

func TestManagerListEmpty(t *testing.T) {
	m := &Manager{
		resources: map[string]Resource{},
		configs:   map[string]config.ResourceConfig{},
		logger:    slog.Default(),
	}

	list := m.List()
	if len(list) != 0 {
		t.Fatalf("expected 0 resources in list, got %d", len(list))
	}
}

func TestExtractDBName(t *testing.T) {
	tests := []struct {
		uri  string
		want string
	}{
		{"mongodb://localhost:27017/mydb", "mydb"},
		{"mongodb://localhost:27017/mydb?retryWrites=true", "mydb"},
		{"mongodb://user:pass@host:27017/testdb", "testdb"},
		{"mongodb+srv://host/proddb?w=majority", "proddb"},
		{"mongodb://localhost:27017/", ""},
		{"mongodb://localhost:27017", ""},
	}

	for _, tt := range tests {
		t.Run(tt.uri, func(t *testing.T) {
			got := extractDBName(tt.uri)
			if got != tt.want {
				t.Errorf("extractDBName(%q) = %q, want %q", tt.uri, got, tt.want)
			}
		})
	}
}

func TestNewManagerCreatesRedisWithHost(t *testing.T) {
	// This test validates that NewManager can create a Redis resource
	// when given a valid host. The resource won't connect, but construction
	// should succeed.
	cfgs := map[string]config.ResourceConfig{
		"test-redis": {
			Type:        "redis",
			Host:        "localhost:6379",
			Description: "test redis resource",
			AllowedOps:  []string{"get", "set"},
		},
	}

	m, err := NewManager(cfgs, nil, slog.Default())
	if err != nil {
		t.Fatalf("NewManager with valid redis config: unexpected error: %v", err)
	}
	defer m.Close()

	r, ok := m.Get("test-redis")
	if !ok {
		t.Fatal("expected to find 'test-redis' resource")
	}
	if r.Type() != "redis" {
		t.Errorf("expected type 'redis', got %q", r.Type())
	}
	if r.Description() != "test redis resource" {
		t.Errorf("expected description 'test redis resource', got %q", r.Description())
	}
}

func TestNewManagerCreatesESWithURL(t *testing.T) {
	cfgs := map[string]config.ResourceConfig{
		"test-es": {
			Type:        "elasticsearch",
			URL:         "http://localhost:9200",
			Description: "test es resource",
			AllowedOps:  []string{"search", "count"},
		},
	}

	m, err := NewManager(cfgs, nil, slog.Default())
	if err != nil {
		t.Fatalf("NewManager with valid ES config: unexpected error: %v", err)
	}
	defer m.Close()

	r, ok := m.Get("test-es")
	if !ok {
		t.Fatal("expected to find 'test-es' resource")
	}
	if r.Type() != "elasticsearch" {
		t.Errorf("expected type 'elasticsearch', got %q", r.Type())
	}
}

func TestNewManagerCreatesMySQLWithDSN(t *testing.T) {
	cfgs := map[string]config.ResourceConfig{
		"test-mysql": {
			Type:        "mysql",
			DSN:         "user:pass@tcp(localhost:3306)/testdb",
			Description: "test mysql resource",
			AllowedOps:  []string{"select"},
		},
	}

	m, err := NewManager(cfgs, nil, slog.Default())
	if err != nil {
		t.Fatalf("NewManager with valid MySQL config: unexpected error: %v", err)
	}
	defer m.Close()

	r, ok := m.Get("test-mysql")
	if !ok {
		t.Fatal("expected to find 'test-mysql' resource")
	}
	if r.Type() != "mysql" {
		t.Errorf("expected type 'mysql', got %q", r.Type())
	}
}

func TestMockResourceExecute(t *testing.T) {
	mock := &mockResource{
		typeName:   "test",
		desc:       "mock",
		execResult: "hello",
		execErr:    nil,
	}

	result, err := mock.Execute(context.Background(), "op", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hello" {
		t.Errorf("expected 'hello', got %v", result)
	}
}

func TestMockResourceExecuteError(t *testing.T) {
	mock := &mockResource{
		typeName: "test",
		desc:     "mock",
		execErr:  fmt.Errorf("exec failed"),
	}

	_, err := mock.Execute(context.Background(), "op", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "exec failed" {
		t.Errorf("expected 'exec failed', got %q", err.Error())
	}
}
