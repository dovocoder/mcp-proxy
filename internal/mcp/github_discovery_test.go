package mcp

import (
	"fmt"
	"testing"
)

func TestDiscoverGitHubCopilotOAuth(t *testing.T) {
	metadata, err := DiscoverOAuthMetadata("https://api.githubcopilot.com/mcp")
	if err != nil {
		t.Fatalf("DiscoverOAuthMetadata failed: %v", err)
	}
	if metadata == nil {
		t.Fatal("metadata is nil")
	}
	fmt.Printf("Issuer:                %s\n", metadata.Issuer)
	fmt.Printf("AuthorizationEndpoint: %s\n", metadata.AuthorizationEndpoint)
	fmt.Printf("TokenEndpoint:          %s\n", metadata.TokenEndpoint)
	fmt.Printf("RegistrationEndpoint:   %s\n", metadata.RegistrationEndpoint)
	fmt.Printf("ScopesSupported:        %v\n", metadata.ScopesSupported)

	if metadata.AuthorizationEndpoint == "" {
		t.Fatal("AuthorizationEndpoint is empty")
	}
	if metadata.TokenEndpoint == "" {
		t.Fatal("TokenEndpoint is empty")
	}
	if metadata.Issuer != "https://github.com/login/oauth" {
		t.Fatalf("expected issuer https://github.com/login/oauth, got %s", metadata.Issuer)
	}
}
