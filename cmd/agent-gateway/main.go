// Package main provides the CLI entry point for the agent-gateway binary. It uses
// Cobra to expose serve, connect, update, and version sub-commands.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"agent-gateway/internal/auth"
	"agent-gateway/internal/client"
	"agent-gateway/internal/config"
	"agent-gateway/internal/mcp"
	"agent-gateway/internal/metering"
	"agent-gateway/internal/resources"
	"agent-gateway/internal/server"
	"agent-gateway/internal/tunnel"
)

// Version is set at build time via -ldflags.
var Version = "dev"

// cfgFile holds the path to the configuration file.
var cfgFile string

func main() {
	rootCmd := &cobra.Command{
		Use:   "agent-gateway",
		Short: "MCP gateway for secure QA resource access",
	}
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "config.yaml", "config file path")

	rootCmd.AddCommand(serveCmd())
	rootCmd.AddCommand(connectCmd())
	rootCmd.AddCommand(updateCmd())
	rootCmd.AddCommand(versionCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func serveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the MCP gateway server",
		RunE: func(cmd *cobra.Command, args []string) error {
			// 1. Load configuration.
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			// 2. Set up structured logger.
			logger := setupLogger(cfg.Logging)

			// 3. Create OIDC authenticator.
			var authn auth.Authenticator
			if cfg.Auth.Okta.Issuer != "" && cfg.Auth.Okta.ClientID != "" {
				a, err := auth.NewOIDCAuthenticator(cfg.Auth.Okta.Issuer, cfg.Auth.Okta.ClientID)
				if err != nil {
					logger.Warn("failed to create OIDC authenticator, authentication disabled", "error", err)
				} else {
					authn = a
				}
			} else {
				logger.Warn("OIDC authenticator not configured")
			}

			// 4. Create RBAC authorizer.
			authz := auth.NewRBACAuthorizer(cfg.Auth.RBAC)

			// 5. Create WireGuard tunnel (optional).
			var dialFn resources.DialContextFunc
			if cfg.WireGuard.PrivateKey != "" {
				tun, err := tunnel.New(cfg.WireGuard, logger)
				if err != nil {
					logger.Warn("failed to create WireGuard tunnel, using direct connections", "error", err)
				} else {
					defer tun.Close()
					dialFn = tun.DialContext
					logger.Info("WireGuard tunnel established")
				}
			}

			// 6. Create resource manager.
			mgr, err := resources.NewManager(cfg.Resources, dialFn, logger)
			if err != nil {
				return fmt.Errorf("creating resource manager: %w", err)
			}
			defer mgr.Close()

			// 7. Create metering (if enabled).
			var meter metering.Meter
			if cfg.Metering.Enabled {
				dbPath := cfg.Metering.DBPath
				if dbPath == "" {
					dbPath = "usage.db"
				}
				m, err := metering.NewSQLiteMeter(dbPath)
				if err != nil {
					return fmt.Errorf("creating meter: %w", err)
				}
				meter = m
				defer meter.Close()
				logger.Info("metering enabled", "db_path", dbPath)
			}

			// 8. Create MCP server.
			mcpSrv := mcp.NewServer("agent-gateway", Version, mgr, authz, meter, logger)

			// 9. Create HTTP server.
			httpSrv := server.New(cfg, mcpSrv, authn, meter, logger)

			// 10. Handle signals for graceful shutdown.
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			// 11. Start server.
			logger.Info("starting agent-gateway", "version", Version)
			return httpSrv.Start(ctx)
		},
	}
}

func connectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "connect",
		Short: "Connect to the MCP gateway",
		RunE: func(cmd *cobra.Command, args []string) error {
			serverURL, _ := cmd.Flags().GetString("server")
			token, _ := cmd.Flags().GetString("token")
			logger := slog.Default()
			return client.Connect(serverURL, token, logger)
		},
	}
	cmd.Flags().String("server", "", "Gateway server URL")
	cmd.Flags().String("token", "", "Authentication token (skip SSO)")
	_ = cmd.MarkFlagRequired("server")
	return cmd
}

func updateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Update the client to the latest version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("Self-update not yet implemented. Download from the releases page.")
			return nil
		},
	}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("agent-gateway version %s\n", Version)
		},
	}
}

// setupLogger creates a structured logger based on the logging configuration.
func setupLogger(cfg config.LoggingConfig) *slog.Logger {
	var level slog.Level
	switch strings.ToLower(cfg.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if strings.ToLower(cfg.Format) == "json" {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}

	// If a log file is configured, write there instead of stderr.
	if cfg.File != "" {
		f, err := os.OpenFile(cfg.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			if strings.ToLower(cfg.Format) == "json" {
				handler = slog.NewJSONHandler(f, opts)
			} else {
				handler = slog.NewTextHandler(f, opts)
			}
		}
	}

	return slog.New(handler)
}
