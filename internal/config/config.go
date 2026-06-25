package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strconv"
)

// Config holds all application configuration.
type Config struct {
	Port              string
	DBPath            string
	JWTSecret         string
	AdminUsername     string
	AdminPassword     string
	AdminLoginEnabled bool // if false, password login is disabled (OIDC only)
	WebDistPath       string
	AllowedOrigins    []string
	EncryptionKey     string // global encryption key for env vars at rest
	// OIDC (e.g. PocketID)
	OIDCEnabled      bool
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCRedirectURL  string // optional override; auto-detected from request if empty
}

// Load reads configuration from environment variables with sensible defaults.
func Load() (*Config, error) {
	cfg := &Config{
		Port:              envOrDefault("MCP_PROXY_PORT", "8080"),
		DBPath:            envOrDefault("MCP_PROXY_DB", "mcp-proxy.db"),
		JWTSecret:         envOrDefault("MCP_PROXY_JWT_SECRET", ""),
		AdminUsername:     envOrDefault("MCP_PROXY_ADMIN_USER", "admin"),
		AdminPassword:     envOrDefault("MCP_PROXY_ADMIN_PASS", ""),
		AdminLoginEnabled: envOrDefault("MCP_PROXY_ADMIN_LOGIN", "true") != "false" && envOrDefault("MCP_PROXY_ADMIN_LOGIN", "true") != "0",
		WebDistPath:       envOrDefault("MCP_PROXY_WEB_DIST", "web/dist"),
		AllowedOrigins:    []string{"http://localhost:5173", "http://localhost:8080"},
		// OIDC
		OIDCEnabled:      envOrDefault("OIDC_ENABLED", "") == "true" || envOrDefault("OIDC_ENABLED", "") == "1",
		OIDCIssuer:       envOrDefault("OIDC_ISSUER", ""),
		OIDCClientID:     envOrDefault("OIDC_CLIENT_ID", ""),
		OIDCClientSecret: envOrDefault("OIDC_CLIENT_SECRET", ""),
		OIDCRedirectURL:  envOrDefault("OIDC_REDIRECT_URL", ""),
		// Encryption
		EncryptionKey: envOrDefault("ENCRYPTION_KEY", ""),
	}

	if cfg.JWTSecret == "" {
		// Generate a random secret if none is provided.
		// This is more secure than a hardcoded default — each deployment
		// gets a unique key, making forged JWTs impossible without the key.
		// The downside: tokens don't survive restarts (users must re-login).
		secretBytes := make([]byte, 32)
		if _, err := rand.Read(secretBytes); err != nil {
			return nil, fmt.Errorf("failed to generate JWT secret: %w", err)
		}
		cfg.JWTSecret = hex.EncodeToString(secretBytes)
		log.Printf("WARNING: MCP_PROXY_JWT_SECRET not set — generated random secret. Tokens will not survive restarts. Set MCP_PROXY_JWT_SECRET in production for persistence.")
	}

	if cfg.AdminPassword == "" {
		// Generate a random password if none is provided.
		// This prevents the well-known "admin/admin" login attack.
		passBytes := make([]byte, 12)
		if _, err := rand.Read(passBytes); err != nil {
			return nil, fmt.Errorf("failed to generate admin password: %w", err)
		}
		cfg.AdminPassword = hex.EncodeToString(passBytes)
		log.Printf("WARNING: MCP_PROXY_ADMIN_PASS not set — generated random password: %s", cfg.AdminPassword)
	}

	return cfg, nil
}

// ListenAddr returns the full listen address.
func (c *Config) ListenAddr() string {
	return fmt.Sprintf(":%s", c.Port)
}

// IsProduction returns true if running in production mode.
func (c *Config) IsProduction() bool {
	return os.Getenv("MCP_PROXY_ENV") == "production"
}

// OIDCEnabled returns true if OIDC is explicitly enabled and configured.
func (c *Config) IsOIDCEnabled() bool {
	return c.OIDCEnabled && c.OIDCIssuer != "" && c.OIDCClientID != ""
}

func envOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

// EnvInt reads an integer env var with a default.
func EnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			return n
		}
	}
	return defaultVal
}
