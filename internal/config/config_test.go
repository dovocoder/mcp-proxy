package config

import (
	"os"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	os.Unsetenv("MCP_PROXY_PORT")
	os.Unsetenv("MCP_PROXY_DB")
	os.Unsetenv("MCP_PROXY_JWT_SECRET")
	os.Unsetenv("MCP_PROXY_ADMIN_USER")
	os.Unsetenv("MCP_PROXY_ADMIN_PASS")
	os.Unsetenv("MCP_PROXY_ADMIN_LOGIN")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != "8080" {
		t.Errorf("Port: expected 8080, got %s", cfg.Port)
	}
	if cfg.DBPath != "mcp-proxy.db" {
		t.Errorf("DBPath: expected mcp-proxy.db, got %s", cfg.DBPath)
	}
	if cfg.AdminUsername != "admin" {
		t.Errorf("AdminUsername: expected admin, got %s", cfg.AdminUsername)
	}
	if !cfg.AdminLoginEnabled {
		t.Error("AdminLoginEnabled should be true by default")
	}
}

func TestLoad_JWTSecretDefault(t *testing.T) {
	os.Unsetenv("MCP_PROXY_JWT_SECRET")
	cfg, _ := Load()
	if cfg.JWTSecret == "" {
		t.Error("JWTSecret should have a default value")
	}
	if cfg.JWTSecret == "dev-secret-change-in-production" {
		t.Log("WARNING: using default JWT secret — should be overridden in production")
	}
}

func TestLoad_AdminLoginDisabled(t *testing.T) {
	os.Setenv("MCP_PROXY_ADMIN_LOGIN", "false")
	defer os.Unsetenv("MCP_PROXY_ADMIN_LOGIN")
	cfg, _ := Load()
	if cfg.AdminLoginEnabled {
		t.Error("AdminLoginEnabled should be false when MCP_PROXY_ADMIN_LOGIN=false")
	}
}

func TestLoad_AdminLoginDisabledNumeric(t *testing.T) {
	os.Setenv("MCP_PROXY_ADMIN_LOGIN", "0")
	defer os.Unsetenv("MCP_PROXY_ADMIN_LOGIN")
	cfg, _ := Load()
	if cfg.AdminLoginEnabled {
		t.Error("AdminLoginEnabled should be false when MCP_PROXY_ADMIN_LOGIN=0")
	}
}

func TestIsOIDCEnabled(t *testing.T) {
	cases := []struct {
		enabled bool
		issuer  string
		clientID string
		want    bool
	}{
		{false, "", "", false},
		{true, "", "", false},      // enabled but no issuer/clientID
		{true, "https://id.example.com", "", false}, // enabled + issuer but no clientID
		{true, "https://id.example.com", "client-123", true},
		{false, "https://id.example.com", "client-123", false}, // not enabled
	}
	for _, tc := range cases {
		cfg := &Config{
			OIDCEnabled:  tc.enabled,
			OIDCIssuer:    tc.issuer,
			OIDCClientID:  tc.clientID,
		}
		if cfg.IsOIDCEnabled() != tc.want {
			t.Errorf("enabled=%v issuer=%q clientID=%q: expected %v, got %v",
				tc.enabled, tc.issuer, tc.clientID, tc.want, cfg.IsOIDCEnabled())
		}
	}
}

func TestListenAddr(t *testing.T) {
	cfg := &Config{Port: "3000"}
	if cfg.ListenAddr() != ":3000" {
		t.Errorf("expected :3000, got %s", cfg.ListenAddr())
	}
}

func TestLoad_EnvOverride(t *testing.T) {
	os.Setenv("MCP_PROXY_PORT", "9090")
	os.Setenv("MCP_PROXY_DB", "/tmp/test.db")
	os.Setenv("MCP_PROXY_JWT_SECRET", "custom-secret")
	defer os.Unsetenv("MCP_PROXY_PORT")
	defer os.Unsetenv("MCP_PROXY_DB")
	defer os.Unsetenv("MCP_PROXY_JWT_SECRET")

	cfg, _ := Load()
	if cfg.Port != "9090" {
		t.Errorf("Port: expected 9090, got %s", cfg.Port)
	}
	if cfg.DBPath != "/tmp/test.db" {
		t.Errorf("DBPath: expected /tmp/test.db, got %s", cfg.DBPath)
	}
	if cfg.JWTSecret != "custom-secret" {
		t.Errorf("JWTSecret: expected custom-secret, got %s", cfg.JWTSecret)
	}
}
