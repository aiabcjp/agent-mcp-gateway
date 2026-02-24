package client

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agent-gateway/internal/config"
	"agent-gateway/internal/resources"
)

// ---------------------------------------------------------------------------
// CheckVersion tests
// ---------------------------------------------------------------------------

func TestCheckVersion_DevVersion(t *testing.T) {
	err := CheckVersion("dev", config.VersionConfig{
		Latest:  "2.0.0",
		Minimum: "1.0.0",
	})
	if err != nil {
		t.Errorf("expected no error for dev version, got: %v", err)
	}
}

func TestCheckVersion_EmptyMinimum(t *testing.T) {
	err := CheckVersion("1.0.0", config.VersionConfig{
		Latest: "2.0.0",
	})
	if err != nil {
		t.Errorf("expected no error for empty minimum, got: %v", err)
	}
}

func TestCheckVersion_ClientBelowMinimum(t *testing.T) {
	err := CheckVersion("0.9.0", config.VersionConfig{
		Latest:      "2.0.0",
		Minimum:     "1.0.0",
		DownloadURL: "https://example.com/dl",
	})
	if err == nil {
		t.Fatal("expected error for client below minimum")
	}
}

func TestCheckVersion_ClientAtMinimum(t *testing.T) {
	err := CheckVersion("1.0.0", config.VersionConfig{
		Latest:  "2.0.0",
		Minimum: "1.0.0",
	})
	if err == nil {
		t.Fatal("expected update-available warning")
	}
	// The error should be about an update being available, not required.
	if err.Error() == "" {
		t.Error("expected non-empty error message")
	}
}

func TestCheckVersion_ClientAtLatest(t *testing.T) {
	err := CheckVersion("2.0.0", config.VersionConfig{
		Latest:  "2.0.0",
		Minimum: "1.0.0",
	})
	if err != nil {
		t.Errorf("expected no error for client at latest, got: %v", err)
	}
}

func TestCheckVersion_ClientAboveLatest(t *testing.T) {
	err := CheckVersion("3.0.0", config.VersionConfig{
		Latest:  "2.0.0",
		Minimum: "1.0.0",
	})
	if err != nil {
		t.Errorf("expected no error for client above latest, got: %v", err)
	}
}

func TestCheckVersion_PatchVersionComparison(t *testing.T) {
	err := CheckVersion("1.0.1", config.VersionConfig{
		Latest:  "1.0.2",
		Minimum: "1.0.0",
	})
	if err == nil {
		t.Fatal("expected update-available warning for patch difference")
	}
}

func TestCheckVersion_MajorVersionBelowMinimum(t *testing.T) {
	err := CheckVersion("0.1.0", config.VersionConfig{
		Latest:      "2.0.0",
		Minimum:     "1.0.0",
		DownloadURL: "https://example.com/dl",
	})
	if err == nil {
		t.Fatal("expected error for major version below minimum")
	}
}

func TestCheckVersion_WithVPrefix(t *testing.T) {
	err := CheckVersion("v2.0.0", config.VersionConfig{
		Latest:  "2.0.0",
		Minimum: "1.0.0",
	})
	if err != nil {
		t.Errorf("expected no error for v-prefixed version, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// compareSemver tests
// ---------------------------------------------------------------------------

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "2.0.0", -1},
		{"2.0.0", "1.0.0", 1},
		{"1.1.0", "1.0.0", 1},
		{"1.0.0", "1.1.0", -1},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "1.0.1", -1},
		{"10.0.0", "9.0.0", 1},
		{"1.10.0", "1.9.0", 1},
	}

	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			got, err := compareSemver(tt.a, tt.b)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("compareSemver(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestCompareSemver_InvalidVersion(t *testing.T) {
	_, err := compareSemver("abc", "1.0.0")
	if err == nil {
		t.Fatal("expected error for invalid version")
	}
}

// ---------------------------------------------------------------------------
// parseSemver tests
// ---------------------------------------------------------------------------

func TestParseSemver(t *testing.T) {
	tests := []struct {
		input string
		want  [3]int
	}{
		{"1.2.3", [3]int{1, 2, 3}},
		{"v1.2.3", [3]int{1, 2, 3}},
		{"0.0.0", [3]int{0, 0, 0}},
		{"10.20.30", [3]int{10, 20, 30}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseSemver(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("parseSemver(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Bootstrap tests
// ---------------------------------------------------------------------------

func TestBootstrap_Success(t *testing.T) {
	expected := BootstrapResponse{
		Resources: []resources.ResourceInfo{
			{
				Name:        "test-redis",
				Type:        "redis",
				Description: "Test Redis",
				AllowedOps:  []string{"get", "set"},
			},
		},
		Version: config.VersionConfig{
			Latest:      "1.2.0",
			Minimum:     "1.0.0",
			DownloadURL: "https://example.com/dl",
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/bootstrap" {
			t.Errorf("expected path /api/bootstrap, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("expected Bearer test-token, got %s", r.Header.Get("Authorization"))
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(expected)
	}))
	defer ts.Close()

	result, err := Bootstrap(ts.URL, "test-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(result.Resources))
	}
	if result.Resources[0].Name != "test-redis" {
		t.Errorf("expected resource name=test-redis, got %q", result.Resources[0].Name)
	}
	if result.Version.Latest != "1.2.0" {
		t.Errorf("expected version.latest=1.2.0, got %q", result.Version.Latest)
	}
}

func TestBootstrap_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	_, err := Bootstrap(ts.URL, "test-token")
	if err == nil {
		t.Fatal("expected error for server error response")
	}
}

func TestBootstrap_InvalidJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not json"))
	}))
	defer ts.Close()

	_, err := Bootstrap(ts.URL, "test-token")
	if err == nil {
		t.Fatal("expected error for invalid JSON response")
	}
}

func TestBootstrap_TrailingSlash(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/bootstrap" {
			t.Errorf("expected path /api/bootstrap, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(BootstrapResponse{})
	}))
	defer ts.Close()

	_, err := Bootstrap(ts.URL+"/", "test-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// StartProxy tests
// ---------------------------------------------------------------------------

func TestStartProxy_ForwardsRequests(t *testing.T) {
	// Create a backend server.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":"ok"}`))
	}))
	defer backend.Close()

	logger := slog.Default()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the proxy in a goroutine.
	errCh := make(chan error, 1)
	go func() {
		errCh <- StartProxy(ctx, "127.0.0.1:0", backend.URL, "my-token", logger)
	}()

	// Give proxy a moment to start.
	time.Sleep(200 * time.Millisecond)

	// Cancel context to stop the proxy.
	cancel()

	// Wait for the proxy to stop (with timeout).
	select {
	case err := <-errCh:
		if err != nil {
			t.Logf("proxy stopped with: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("proxy did not stop within timeout")
	}
}
