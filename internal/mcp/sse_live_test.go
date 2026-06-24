package mcp

import (
	"os"
	"testing"
)

func TestSSELiveGitHub(t *testing.T) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		t.Skip("GITHUB_TOKEN not set")
	}

	client := NewClient(ClientConfig{
		URL:            "https://api.githubcopilot.com/mcp",
		Transport:      "streamable-http",
		AuthToken:      token,
		Timeout:        30,
		ConnectTimeout: 15,
	})

	if err := client.Connect(); err != nil {
		t.Fatalf("Connect error: %v", err)
	}

	status, lastErr := client.Status()
	tools := client.Tools()
	t.Logf("Status: %s (err=%q)", status, lastErr)
	t.Logf("Found %d tools", len(tools))
	if len(tools) == 0 {
		t.Fatal("expected tools but got 0")
	}
	for i, tool := range tools {
		if i >= 5 {
			break
		}
		t.Logf("  - %s", tool.Name)
	}
	if len(tools) > 5 {
		t.Logf("  ... and %d more", len(tools)-5)
	}

	client.Disconnect()
}
