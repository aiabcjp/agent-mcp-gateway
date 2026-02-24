package tunnel

import (
	"encoding/base64"
	"encoding/hex"
	"log/slog"
	"os"
	"strings"
	"testing"

	"agent-gateway/internal/config"
)

// ---------------------------------------------------------------------------
// KeyToHex tests
// ---------------------------------------------------------------------------

func TestKeyToHex_ValidKey(t *testing.T) {
	// Generate a known 32-byte key and verify round-trip.
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i)
	}
	b64 := base64.StdEncoding.EncodeToString(raw)
	wantHex := hex.EncodeToString(raw)

	got, err := KeyToHex(b64)
	if err != nil {
		t.Fatalf("KeyToHex returned error: %v", err)
	}
	if got != wantHex {
		t.Errorf("KeyToHex(%q) = %q, want %q", b64, got, wantHex)
	}
}

func TestKeyToHex_RealWireGuardKey(t *testing.T) {
	// Use the same key from the config test fixtures.
	b64Key := "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk="
	hexKey, err := KeyToHex(b64Key)
	if err != nil {
		t.Fatalf("KeyToHex returned error: %v", err)
	}

	// Verify it's a valid 64-char hex string (32 bytes).
	if len(hexKey) != 64 {
		t.Errorf("hex key length = %d, want 64", len(hexKey))
	}

	// Verify it decodes back to the same bytes.
	decoded, err := hex.DecodeString(hexKey)
	if err != nil {
		t.Fatalf("hex.DecodeString failed: %v", err)
	}
	reEncoded := base64.StdEncoding.EncodeToString(decoded)
	if reEncoded != b64Key {
		t.Errorf("round-trip mismatch: got %q, want %q", reEncoded, b64Key)
	}
}

func TestKeyToHex_InvalidBase64(t *testing.T) {
	_, err := KeyToHex("not-valid-base64!!!")
	if err == nil {
		t.Fatal("expected error for invalid base64, got nil")
	}
	if !strings.Contains(err.Error(), "decoding base64") {
		t.Errorf("error = %q, want it to contain 'decoding base64'", err.Error())
	}
}

func TestKeyToHex_WrongLength(t *testing.T) {
	// 16 bytes instead of 32.
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))
	_, err := KeyToHex(short)
	if err == nil {
		t.Fatal("expected error for wrong key length, got nil")
	}
	if !strings.Contains(err.Error(), "invalid key length") {
		t.Errorf("error = %q, want it to contain 'invalid key length'", err.Error())
	}
}

func TestKeyToHex_EmptyString(t *testing.T) {
	_, err := KeyToHex("")
	if err == nil {
		t.Fatal("expected error for empty key, got nil")
	}
}

func TestKeyToHex_TooLong(t *testing.T) {
	long := base64.StdEncoding.EncodeToString(make([]byte, 64))
	_, err := KeyToHex(long)
	if err == nil {
		t.Fatal("expected error for too-long key, got nil")
	}
	if !strings.Contains(err.Error(), "invalid key length") {
		t.Errorf("error = %q, want it to contain 'invalid key length'", err.Error())
	}
}

// ---------------------------------------------------------------------------
// New() error path tests - no real WireGuard tunnel created
// ---------------------------------------------------------------------------

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestNew_InvalidAddress(t *testing.T) {
	cfg := config.WireGuardConfig{
		PrivateKey: "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=",
		Address:    "not-an-ip",
		DNS:        "1.1.1.1",
		Peer: config.WireGuardPeer{
			PublicKey:  "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=",
			Endpoint:   "vpn.example.com:51820",
			AllowedIPs: "10.0.0.0/24",
			Keepalive:  25,
		},
	}

	_, err := New(cfg, testLogger())
	if err == nil {
		t.Fatal("expected error for invalid address, got nil")
	}
	if !strings.Contains(err.Error(), "parsing tunnel address") {
		t.Errorf("error = %q, want it to contain 'parsing tunnel address'", err.Error())
	}
}

func TestNew_InvalidDNS(t *testing.T) {
	cfg := config.WireGuardConfig{
		PrivateKey: "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=",
		Address:    "10.0.0.1/24",
		DNS:        "not-a-dns",
		Peer: config.WireGuardPeer{
			PublicKey:  "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=",
			Endpoint:   "vpn.example.com:51820",
			AllowedIPs: "10.0.0.0/24",
			Keepalive:  25,
		},
	}

	_, err := New(cfg, testLogger())
	if err == nil {
		t.Fatal("expected error for invalid DNS, got nil")
	}
	if !strings.Contains(err.Error(), "parsing DNS address") {
		t.Errorf("error = %q, want it to contain 'parsing DNS address'", err.Error())
	}
}

func TestNew_InvalidPrivateKey(t *testing.T) {
	cfg := config.WireGuardConfig{
		PrivateKey: "bad-key!!!",
		Address:    "10.0.0.1/24",
		DNS:        "1.1.1.1",
		Peer: config.WireGuardPeer{
			PublicKey:  "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=",
			Endpoint:   "vpn.example.com:51820",
			AllowedIPs: "10.0.0.0/24",
			Keepalive:  25,
		},
	}

	_, err := New(cfg, testLogger())
	if err == nil {
		t.Fatal("expected error for invalid private key, got nil")
	}
	if !strings.Contains(err.Error(), "converting private key") {
		t.Errorf("error = %q, want it to contain 'converting private key'", err.Error())
	}
}

func TestNew_InvalidPeerPublicKey(t *testing.T) {
	cfg := config.WireGuardConfig{
		PrivateKey: "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=",
		Address:    "10.0.0.1/24",
		DNS:        "1.1.1.1",
		Peer: config.WireGuardPeer{
			PublicKey:  "bad-peer-key!!!",
			Endpoint:   "vpn.example.com:51820",
			AllowedIPs: "10.0.0.0/24",
			Keepalive:  25,
		},
	}

	_, err := New(cfg, testLogger())
	if err == nil {
		t.Fatal("expected error for invalid peer public key, got nil")
	}
	if !strings.Contains(err.Error(), "converting peer public key") {
		t.Errorf("error = %q, want it to contain 'converting peer public key'", err.Error())
	}
}
