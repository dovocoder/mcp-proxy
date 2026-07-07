package mcp

import "testing"

func TestExtractResourceMetadataURLSentryHeader(t *testing.T) {
	header := `Bearer realm="OAuth", error="invalid_token", error_description="Missing or invalid access token", resource_metadata="https://mcp.sentry.dev/.well-known/oauth-protected-resource/mcp"`

	got := extractResourceMetadataURL(header)
	want := "https://mcp.sentry.dev/.well-known/oauth-protected-resource/mcp"
	if got != want {
		t.Fatalf("extractResourceMetadataURL() = %q, want %q", got, want)
	}
}

func TestExtractResourceMetadataURLHandlesEscapedQuotes(t *testing.T) {
	header := `Bearer resource_metadata="https://example.com/.well-known/oauth-protected-resource/mcp?note=a\"b", error="invalid_token"`

	got := extractResourceMetadataURL(header)
	want := `https://example.com/.well-known/oauth-protected-resource/mcp?note=a"b`
	if got != want {
		t.Fatalf("extractResourceMetadataURL() = %q, want %q", got, want)
	}
}
