package auth

import (
	"net/http"

	"github.com/agentic/mcp-proxy/internal/ssrf"
)

// NewSSRFSafeTransport returns an http.Transport that blocks connections to
// private IP ranges to prevent SSRF attacks. Exported for use by other packages.
// Deprecated: use ssrf.SafeTransport() directly.
func NewSSRFSafeTransport() *http.Transport {
	return ssrf.SafeTransport()
}

// newSSRFSafeTransport returns an http.Transport that blocks connections to
// private IP ranges to prevent SSRF attacks during OIDC discovery, JWKS
// fetching, token introspection, and userinfo calls.
func newSSRFSafeTransport() *http.Transport {
	return ssrf.SafeTransport()
}

// newSSRFSafeClient returns an HTTP client that blocks private IPs and
// does not follow redirects (to prevent redirect-based SSRF).
func newSSRFSafeClient() *http.Client {
	return ssrf.SafeClient()
}
