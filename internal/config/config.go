// Package config handles loading and parsing of the gateway YAML configuration.
package config

import (
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the top-level gateway configuration.
type Config struct {
	Server    ServerConfig              `yaml:"server"`
	Auth      AuthConfig                `yaml:"auth"`
	WireGuard WireGuardConfig           `yaml:"wireguard"`
	Resources map[string]ResourceConfig `yaml:"resources"`
	Clients   ClientsConfig             `yaml:"clients"`
	Logging   LoggingConfig             `yaml:"logging"`
	Metering  MeteringConfig            `yaml:"metering"`
}

// ServerConfig holds the HTTP/TLS listener settings.
type ServerConfig struct {
	Listen string `yaml:"listen"`
	Domain string `yaml:"domain"`
	TLS    string `yaml:"tls"`
}

// AuthConfig holds authentication and authorization settings.
type AuthConfig struct {
	Provider string     `yaml:"provider"`
	Okta     OktaConfig `yaml:"okta"`
	RBAC     []RBACRule `yaml:"rbac"`
}

// OktaConfig holds OIDC provider settings for Okta.
type OktaConfig struct {
	Issuer   string   `yaml:"issuer"`
	ClientID string   `yaml:"client_id"`
	Scopes   []string `yaml:"scopes"`
}

// RBACRule maps a group to a set of resources and permissions.
type RBACRule struct {
	Group       string   `yaml:"group"`
	Resources   []string `yaml:"resources"`
	Permissions []string `yaml:"permissions"`
}

// WireGuardConfig holds the local WireGuard interface settings.
type WireGuardConfig struct {
	PrivateKey string        `yaml:"private_key"`
	Address    string        `yaml:"address"`
	DNS        string        `yaml:"dns"`
	Peer       WireGuardPeer `yaml:"peer"`
}

// WireGuardPeer holds the remote WireGuard peer settings.
type WireGuardPeer struct {
	PublicKey  string `yaml:"public_key"`
	Endpoint  string `yaml:"endpoint"`
	AllowedIPs string `yaml:"allowed_ips"`
	Keepalive int    `yaml:"keepalive"`
}

// ResourceConfig describes a single backend resource accessible through the gateway.
type ResourceConfig struct {
	Type        string   `yaml:"type"`
	Host        string   `yaml:"host,omitempty"`
	URI         string   `yaml:"uri,omitempty"`
	DSN         string   `yaml:"dsn,omitempty"`
	URL         string   `yaml:"url,omitempty"`
	Password    string   `yaml:"password,omitempty"`
	Description string   `yaml:"description"`
	ReadOnly    bool     `yaml:"read_only"`
	AllowedOps  []string `yaml:"allowed_ops"`
}

// ClientsConfig holds client distribution and provisioning settings.
type ClientsConfig struct {
	Version   VersionConfig  `yaml:"version"`
	WireGuard ClientWGConfig `yaml:"wireguard"`
}

// VersionConfig describes expected client version constraints.
type VersionConfig struct {
	Latest      string `yaml:"latest"`
	Minimum     string `yaml:"minimum"`
	DownloadURL string `yaml:"download_url"`
}

// ClientWGConfig holds client-side WireGuard provisioning settings.
type ClientWGConfig struct {
	Pool          string `yaml:"pool"`
	AutoProvision bool   `yaml:"auto_provision"`
}

// LoggingConfig holds structured logging settings.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	File   string `yaml:"file"`
	Format string `yaml:"format"`
}

// MeteringConfig holds usage metering settings.
type MeteringConfig struct {
	Enabled bool   `yaml:"enabled"`
	Storage string `yaml:"storage"`
	DBPath  string `yaml:"db_path"`
}

// envVarPattern matches ${ENV_VAR} patterns in strings.
var envVarPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// Load reads a YAML configuration file from path, expands ${ENV_VAR}
// patterns in all string values using os.Getenv, and returns the
// parsed Config.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %s: %w", path, err)
	}

	expandEnvVars(reflect.ValueOf(&cfg).Elem())

	return &cfg, nil
}

// expandEnvVars recursively walks a reflect.Value and replaces ${ENV_VAR}
// patterns in all settable string fields with the corresponding environment
// variable value.
func expandEnvVars(v reflect.Value) {
	switch v.Kind() {
	case reflect.String:
		if v.CanSet() {
			v.SetString(expandString(v.String()))
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			expandEnvVars(v.Field(i))
		}
	case reflect.Map:
		if v.IsNil() {
			return
		}
		for _, key := range v.MapKeys() {
			elem := v.MapIndex(key)
			// Map values are not addressable, so we need to copy, expand,
			// and set back.
			expanded := expandMapValue(elem)
			v.SetMapIndex(key, expanded)
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			expandEnvVars(v.Index(i))
		}
	case reflect.Ptr:
		if !v.IsNil() {
			expandEnvVars(v.Elem())
		}
	case reflect.Interface:
		if !v.IsNil() {
			expandEnvVars(v.Elem())
		}
	}
}

// expandMapValue returns a new reflect.Value with all string fields expanded.
// Since map values are not directly addressable, we must work with copies.
func expandMapValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.String:
		return reflect.ValueOf(expandString(v.String()))
	case reflect.Struct:
		// Create a new addressable copy of the struct.
		cp := reflect.New(v.Type()).Elem()
		cp.Set(v)
		expandEnvVars(cp)
		return cp
	case reflect.Interface:
		if !v.IsNil() {
			return expandMapValue(v.Elem())
		}
	}
	return v
}

// expandString replaces all ${ENV_VAR} patterns in s with their environment
// variable values. Unknown variables expand to the empty string.
func expandString(s string) string {
	if !strings.Contains(s, "${") {
		return s
	}
	return envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		// Extract the variable name from ${VAR_NAME}.
		varName := match[2 : len(match)-1]
		return os.Getenv(varName)
	})
}
