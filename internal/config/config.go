package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all application configuration.
type Config struct {
	Port           string
	DBPath         string
	JWTSecret      string
	AdminUsername  string
	AdminPassword  string
	WebDistPath    string
	AllowedOrigins []string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() (*Config, error) {
	cfg := &Config{
		Port:           envOrDefault("MCP_PROXY_PORT", "8080"),
		DBPath:         envOrDefault("MCP_PROXY_DB", "mcp-proxy.db"),
		JWTSecret:      envOrDefault("MCP_PROXY_JWT_SECRET", ""),
		AdminUsername:  envOrDefault("MCP_PROXY_ADMIN_USER", "admin"),
		AdminPassword:  envOrDefault("MCP_PROXY_ADMIN_PASS", ""),
		WebDistPath:    envOrDefault("MCP_PROXY_WEB_DIST", "web/dist"),
		AllowedOrigins: []string{"http://localhost:5173", "http://localhost:8080"},
	}

	if cfg.JWTSecret == "" {
		cfg.JWTSecret = "dev-secret-change-in-production"
	}

	if cfg.AdminPassword == "" {
		cfg.AdminPassword = "admin"
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
