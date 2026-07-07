package mcp

import "testing"

func TestSentryOAuthDiscovery(t *testing.T) {
	metadata, err := DiscoverOAuthMetadata("https://mcp.sentry.dev/mcp")
	if err != nil {
		t.Fatalf("DiscoverOAuthMetadata failed: %v", err)
	}

	if metadata.Issuer != "https://mcp.sentry.dev" {
		t.Fatalf("issuer = %q, want %q", metadata.Issuer, "https://mcp.sentry.dev")
	}
	if metadata.AuthorizationEndpoint != "https://mcp.sentry.dev/oauth/authorize" {
		t.Fatalf("authorization_endpoint = %q", metadata.AuthorizationEndpoint)
	}
	if metadata.TokenEndpoint != "https://mcp.sentry.dev/oauth/token" {
		t.Fatalf("token_endpoint = %q", metadata.TokenEndpoint)
	}
	if metadata.RegistrationEndpoint != "https://mcp.sentry.dev/oauth/register" {
		t.Fatalf("registration_endpoint = %q", metadata.RegistrationEndpoint)
	}
}
