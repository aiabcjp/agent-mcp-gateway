// Package tunnel provides a WireGuard-based network tunnel using the
// userspace wireguard-go implementation with netstack for TCP/UDP dialing.
package tunnel

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strings"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"

	"agent-mcp-gateway/internal/config"
)

// Tunnel abstracts a network tunnel that can dial remote addresses.
type Tunnel interface {
	// DialContext establishes a connection through the tunnel.
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
	// Close tears down the tunnel and releases resources.
	Close() error
}

// WireGuardTunnel implements Tunnel using a userspace WireGuard device
// backed by netstack.
type WireGuardTunnel struct {
	tnet   *netstack.Net
	device *device.Device
}

// New creates a new WireGuard tunnel from the given configuration. The tunnel
// uses netstack (userspace TCP/IP) so no OS-level TUN device is required.
func New(cfg config.WireGuardConfig, logger *slog.Logger) (Tunnel, error) {
	// Parse local tunnel address.
	addrStr := cfg.Address
	// Strip CIDR suffix if present (e.g., "10.0.0.1/24" -> "10.0.0.1").
	if idx := strings.IndexByte(addrStr, '/'); idx >= 0 {
		addrStr = addrStr[:idx]
	}
	addr, err := netip.ParseAddr(addrStr)
	if err != nil {
		return nil, fmt.Errorf("parsing tunnel address %q: %w", cfg.Address, err)
	}

	// Parse DNS server address.
	dns, err := netip.ParseAddr(cfg.DNS)
	if err != nil {
		return nil, fmt.Errorf("parsing DNS address %q: %w", cfg.DNS, err)
	}

	// Create netstack TUN device.
	tun, tnet, err := netstack.CreateNetTUN(
		[]netip.Addr{addr},
		[]netip.Addr{dns},
		1420, // MTU
	)
	if err != nil {
		return nil, fmt.Errorf("creating netstack TUN: %w", err)
	}

	// Map slog level to wireguard-go log level.
	logLevel := device.LogLevelError
	if logger.Enabled(context.Background(), slog.LevelDebug) {
		logLevel = device.LogLevelVerbose
	} else if logger.Enabled(context.Background(), slog.LevelInfo) {
		logLevel = device.LogLevelError
	}

	// Create WireGuard device.
	dev := device.NewDevice(tun, conn.NewDefaultBind(), device.NewLogger(logLevel, "wireguard: "))

	// Convert keys from base64 to hex for the IPC protocol.
	privKeyHex, err := KeyToHex(cfg.PrivateKey)
	if err != nil {
		dev.Close()
		return nil, fmt.Errorf("converting private key: %w", err)
	}

	pubKeyHex, err := KeyToHex(cfg.Peer.PublicKey)
	if err != nil {
		dev.Close()
		return nil, fmt.Errorf("converting peer public key: %w", err)
	}

	// Build IPC configuration string.
	ipcConf := fmt.Sprintf(
		"private_key=%s\npublic_key=%s\nendpoint=%s\nallowed_ip=%s\npersistent_keepalive_interval=%d\n",
		privKeyHex,
		pubKeyHex,
		cfg.Peer.Endpoint,
		cfg.Peer.AllowedIPs,
		cfg.Peer.Keepalive,
	)

	if err := dev.IpcSet(ipcConf); err != nil {
		dev.Close()
		return nil, fmt.Errorf("setting WireGuard IPC config: %w", err)
	}

	if err := dev.Up(); err != nil {
		dev.Close()
		return nil, fmt.Errorf("bringing WireGuard device up: %w", err)
	}

	logger.Info("wireguard tunnel established",
		"address", cfg.Address,
		"peer_endpoint", cfg.Peer.Endpoint,
	)

	return &WireGuardTunnel{
		tnet:   tnet,
		device: dev,
	}, nil
}

// DialContext establishes a connection through the WireGuard tunnel to the
// given network address.
func (t *WireGuardTunnel) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return t.tnet.DialContext(ctx, network, address)
}

// Close shuts down the WireGuard device and releases all resources.
func (t *WireGuardTunnel) Close() error {
	t.device.Close()
	return nil
}

// KeyToHex converts a WireGuard key from standard base64 encoding to the
// hex encoding expected by the WireGuard IPC protocol.
func KeyToHex(b64Key string) (string, error) {
	keyBytes, err := base64.StdEncoding.DecodeString(b64Key)
	if err != nil {
		return "", fmt.Errorf("decoding base64 key: %w", err)
	}
	if len(keyBytes) != 32 {
		return "", fmt.Errorf("invalid key length: got %d bytes, want 32", len(keyBytes))
	}
	return hex.EncodeToString(keyBytes), nil
}
