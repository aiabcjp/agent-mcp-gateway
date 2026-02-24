// Package resources manages backend data resources accessible through the gateway.
package resources

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"

	"agent-gateway/internal/config"
)

// DialContextFunc is a function that dials a network connection, typically through a tunnel.
type DialContextFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Resource represents a backend data resource accessible through the gateway.
type Resource interface {
	Type() string
	Description() string
	AllowedOps() []string
	Execute(ctx context.Context, op string, params map[string]any) (any, error)
	Close() error
}

// ResourceInfo describes a resource for listing purposes.
type ResourceInfo struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Description string   `json:"description"`
	AllowedOps  []string `json:"allowed_ops"`
	ReadOnly    bool     `json:"read_only"`
}

// Manager manages all configured backend resources.
type Manager struct {
	resources map[string]Resource
	configs   map[string]config.ResourceConfig
	logger    *slog.Logger
}

// NewManager creates a Manager and initializes resources based on their Type field.
// Supported types: "redis", "mongodb", "mysql", "elasticsearch".
// If dialFn is nil, resources use default connections.
func NewManager(cfgs map[string]config.ResourceConfig, dialFn DialContextFunc, logger *slog.Logger) (*Manager, error) {
	if logger == nil {
		logger = slog.Default()
	}

	m := &Manager{
		resources: make(map[string]Resource),
		configs:   cfgs,
		logger:    logger,
	}

	for name, cfg := range cfgs {
		var (
			r   Resource
			err error
		)

		switch strings.ToLower(cfg.Type) {
		case "redis":
			r, err = newRedisResource(name, cfg, dialFn)
		case "mongodb":
			r, err = newMongoResource(name, cfg, dialFn)
		case "mysql":
			r, err = newMySQLResource(name, cfg, dialFn)
		case "elasticsearch":
			r, err = newESResource(name, cfg, dialFn)
		default:
			err = fmt.Errorf("unsupported resource type: %s", cfg.Type)
		}

		if err != nil {
			// Close any resources that were already created before returning the error.
			for _, existing := range m.resources {
				if closeErr := existing.Close(); closeErr != nil {
					logger.Warn("failed to close resource during cleanup", "error", closeErr)
				}
			}
			return nil, fmt.Errorf("creating resource %q (type %s): %w", name, cfg.Type, err)
		}

		m.resources[name] = r
		logger.Info("initialized resource", "name", name, "type", cfg.Type)
	}

	return m, nil
}

// Get returns the named resource and a boolean indicating whether it exists.
func (m *Manager) Get(name string) (Resource, bool) {
	r, ok := m.resources[name]
	return r, ok
}

// List returns information about all configured resources.
func (m *Manager) List() map[string]ResourceInfo {
	result := make(map[string]ResourceInfo, len(m.resources))
	for name, r := range m.resources {
		cfg := m.configs[name]
		result[name] = ResourceInfo{
			Name:        name,
			Type:        r.Type(),
			Description: r.Description(),
			AllowedOps:  r.AllowedOps(),
			ReadOnly:    cfg.ReadOnly,
		}
	}
	return result
}

// Close closes all managed resources and returns the first error encountered.
func (m *Manager) Close() error {
	var firstErr error
	for name, r := range m.resources {
		if err := r.Close(); err != nil {
			m.logger.Error("failed to close resource", "name", name, "error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// isOpAllowed checks whether the given operation is in the allowed list.
// An empty allowed list means all operations are allowed.
func isOpAllowed(allowed []string, op string) bool {
	if len(allowed) == 0 {
		return true
	}
	op = strings.ToLower(op)
	for _, a := range allowed {
		if strings.ToLower(a) == op {
			return true
		}
	}
	return false
}
