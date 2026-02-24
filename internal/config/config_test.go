package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const validYAML = `
server:
  listen: ":8443"
  domain: "gateway.example.com"
  tls: "auto"
auth:
  provider: "okta"
  okta:
    issuer: "https://dev-12345.okta.com/oauth2/default"
    client_id: "0oa1b2c3d4e5f6"
    scopes:
      - openid
      - profile
  rbac:
    - group: "engineers"
      resources:
        - "staging-db"
        - "staging-redis"
      permissions:
        - "read"
        - "write"
    - group: "admins"
      resources:
        - "*"
      permissions:
        - "read"
        - "write"
        - "admin"
wireguard:
  private_key: "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk="
  address: "10.0.0.1/24"
  dns: "1.1.1.1"
  peer:
    public_key: "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg="
    endpoint: "vpn.example.com:51820"
    allowed_ips: "10.0.0.0/24"
    keepalive: 25
resources:
  staging-db:
    type: "postgres"
    host: "10.0.0.10"
    dsn: "postgres://user:pass@10.0.0.10:5432/app"
    description: "Staging PostgreSQL database"
    read_only: false
    allowed_ops:
      - "query"
      - "list_tables"
  staging-redis:
    type: "redis"
    host: "10.0.0.11"
    url: "redis://10.0.0.11:6379"
    password: "s3cret"
    description: "Staging Redis cache"
    read_only: true
    allowed_ops:
      - "get"
      - "keys"
clients:
  version:
    latest: "1.2.0"
    minimum: "1.0.0"
    download_url: "https://releases.example.com/cli"
  wireguard:
    pool: "10.0.0.128/25"
    auto_provision: true
logging:
  level: "info"
  file: "/var/log/gateway.log"
  format: "json"
metering:
  enabled: true
  storage: "sqlite"
  db_path: "/var/lib/gateway/metering.db"
`

func TestLoad_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(validYAML), 0o644); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	// Server
	if cfg.Server.Listen != ":8443" {
		t.Errorf("Server.Listen = %q, want %q", cfg.Server.Listen, ":8443")
	}
	if cfg.Server.Domain != "gateway.example.com" {
		t.Errorf("Server.Domain = %q, want %q", cfg.Server.Domain, "gateway.example.com")
	}
	if cfg.Server.TLS != "auto" {
		t.Errorf("Server.TLS = %q, want %q", cfg.Server.TLS, "auto")
	}

	// Auth
	if cfg.Auth.Provider != "okta" {
		t.Errorf("Auth.Provider = %q, want %q", cfg.Auth.Provider, "okta")
	}
	if cfg.Auth.Okta.Issuer != "https://dev-12345.okta.com/oauth2/default" {
		t.Errorf("Auth.Okta.Issuer = %q", cfg.Auth.Okta.Issuer)
	}
	if cfg.Auth.Okta.ClientID != "0oa1b2c3d4e5f6" {
		t.Errorf("Auth.Okta.ClientID = %q", cfg.Auth.Okta.ClientID)
	}
	if len(cfg.Auth.Okta.Scopes) != 2 {
		t.Errorf("Auth.Okta.Scopes length = %d, want 2", len(cfg.Auth.Okta.Scopes))
	}
	if len(cfg.Auth.RBAC) != 2 {
		t.Errorf("Auth.RBAC length = %d, want 2", len(cfg.Auth.RBAC))
	}
	if cfg.Auth.RBAC[0].Group != "engineers" {
		t.Errorf("Auth.RBAC[0].Group = %q, want %q", cfg.Auth.RBAC[0].Group, "engineers")
	}

	// WireGuard
	if cfg.WireGuard.PrivateKey != "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=" {
		t.Errorf("WireGuard.PrivateKey mismatch")
	}
	if cfg.WireGuard.Address != "10.0.0.1/24" {
		t.Errorf("WireGuard.Address = %q", cfg.WireGuard.Address)
	}
	if cfg.WireGuard.Peer.Keepalive != 25 {
		t.Errorf("WireGuard.Peer.Keepalive = %d, want 25", cfg.WireGuard.Peer.Keepalive)
	}

	// Resources
	if len(cfg.Resources) != 2 {
		t.Fatalf("Resources count = %d, want 2", len(cfg.Resources))
	}
	db, ok := cfg.Resources["staging-db"]
	if !ok {
		t.Fatal("missing staging-db resource")
	}
	if db.Type != "postgres" {
		t.Errorf("staging-db.Type = %q, want %q", db.Type, "postgres")
	}
	if db.ReadOnly != false {
		t.Errorf("staging-db.ReadOnly = %v, want false", db.ReadOnly)
	}
	redis, ok := cfg.Resources["staging-redis"]
	if !ok {
		t.Fatal("missing staging-redis resource")
	}
	if redis.Password != "s3cret" {
		t.Errorf("staging-redis.Password = %q", redis.Password)
	}
	if !redis.ReadOnly {
		t.Error("staging-redis.ReadOnly = false, want true")
	}

	// Clients
	if cfg.Clients.Version.Latest != "1.2.0" {
		t.Errorf("Clients.Version.Latest = %q", cfg.Clients.Version.Latest)
	}
	if !cfg.Clients.WireGuard.AutoProvision {
		t.Error("Clients.WireGuard.AutoProvision = false, want true")
	}

	// Logging
	if cfg.Logging.Level != "info" {
		t.Errorf("Logging.Level = %q", cfg.Logging.Level)
	}
	if cfg.Logging.Format != "json" {
		t.Errorf("Logging.Format = %q", cfg.Logging.Format)
	}

	// Metering
	if !cfg.Metering.Enabled {
		t.Error("Metering.Enabled = false, want true")
	}
	if cfg.Metering.Storage != "sqlite" {
		t.Errorf("Metering.Storage = %q", cfg.Metering.Storage)
	}
}

func TestLoad_EnvVarExpansion(t *testing.T) {
	t.Setenv("TEST_DB_PASSWORD", "env-secret-pw")
	t.Setenv("TEST_LISTEN_PORT", "9090")

	yamlContent := `
server:
  listen: ":${TEST_LISTEN_PORT}"
  domain: "gw.example.com"
  tls: "auto"
resources:
  mydb:
    type: "postgres"
    dsn: "postgres://user:${TEST_DB_PASSWORD}@localhost/db"
    description: "test db"
    read_only: false
    allowed_ops:
      - "query"
`

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Server.Listen != ":9090" {
		t.Errorf("Server.Listen = %q, want %q", cfg.Server.Listen, ":9090")
	}

	db, ok := cfg.Resources["mydb"]
	if !ok {
		t.Fatal("missing mydb resource")
	}
	want := "postgres://user:env-secret-pw@localhost/db"
	if db.DSN != want {
		t.Errorf("mydb.DSN = %q, want %q", db.DSN, want)
	}
}

func TestLoad_EnvVarExpansion_UndefinedVar(t *testing.T) {
	// Make sure the variable is not set.
	t.Setenv("TEST_UNDEFINED_VAR_XYZ", "")
	os.Unsetenv("TEST_UNDEFINED_VAR_XYZ")

	yamlContent := `
server:
  listen: ":${TEST_UNDEFINED_VAR_XYZ}"
  domain: "x"
  tls: "none"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	// Undefined env var should expand to empty string.
	if cfg.Server.Listen != ":" {
		t.Errorf("Server.Listen = %q, want %q (undefined var should be empty)", cfg.Server.Listen, ":")
	}
}

func TestLoad_EnvVarExpansion_InSliceAndNestedStruct(t *testing.T) {
	t.Setenv("TEST_OKTA_ISSUER", "https://test.okta.com")
	t.Setenv("TEST_SCOPE", "custom-scope")

	yamlContent := `
auth:
  provider: "okta"
  okta:
    issuer: "${TEST_OKTA_ISSUER}"
    client_id: "abc"
    scopes:
      - "${TEST_SCOPE}"
      - openid
  rbac:
    - group: "devs"
      resources:
        - "db"
      permissions:
        - "read"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Auth.Okta.Issuer != "https://test.okta.com" {
		t.Errorf("Okta.Issuer = %q", cfg.Auth.Okta.Issuer)
	}
	if len(cfg.Auth.Okta.Scopes) < 1 || cfg.Auth.Okta.Scopes[0] != "custom-scope" {
		t.Errorf("Okta.Scopes = %v", cfg.Auth.Okta.Scopes)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("{{{{not yaml at all!!!!"), 0o644); err != nil {
		t.Fatalf("writing temp file: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestExpandEnvVars_NilMap(t *testing.T) {
	// Ensure expandEnvVars handles a nil map without panic.
	var cfg Config // Resources map is nil by default
	expandEnvVars(reflect.ValueOf(&cfg).Elem())
	if cfg.Resources != nil {
		t.Errorf("expected nil Resources, got %v", cfg.Resources)
	}
}

func TestExpandEnvVars_Pointer(t *testing.T) {
	// Exercise the Ptr branch of expandEnvVars.
	t.Setenv("PTR_VAR", "pointer-value")
	s := "${PTR_VAR}"
	v := reflect.ValueOf(&s)
	expandEnvVars(v)
	if s != "pointer-value" {
		t.Errorf("s = %q, want %q", s, "pointer-value")
	}
}

func TestExpandEnvVars_NilPointer(t *testing.T) {
	// Exercise the nil Ptr branch -- should not panic.
	var p *string
	v := reflect.ValueOf(&p).Elem()
	expandEnvVars(v) // should be a no-op
	if p != nil {
		t.Error("expected nil pointer to remain nil")
	}
}

func TestExpandString(t *testing.T) {
	t.Setenv("FOO", "bar")
	t.Setenv("BAZ", "qux")

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no vars", "hello world", "hello world"},
		{"single var", "${FOO}", "bar"},
		{"var in middle", "pre-${FOO}-post", "pre-bar-post"},
		{"multiple vars", "${FOO}/${BAZ}", "bar/qux"},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandString(tt.input)
			if got != tt.want {
				t.Errorf("expandString(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
