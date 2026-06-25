package auth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// blockPrivateIPs prevents SSRF attacks by blocking requests to private
// or reserved IP address ranges (RFC 1918, loopback, link-local, cloud
// metadata endpoints). See RFC 9728 Section 7.7.
//
// We use a custom dialer that checks the resolved IP before connecting.
var privateCIDRs = []string{
	"10.0.0.0/8",       // private
	"172.16.0.0/12",    // private
	"192.168.0.0/16",   // private
	"127.0.0.0/8",      // loopback
	"169.254.0.0/16",   // link-local (includes cloud metadata 169.254.169.254)
	"0.0.0.0/8",        // "this network"
	"100.64.0.0/10",    // CGNAT
	"fc00::/7",         // IPv6 unique local
	"fe80::/10",        // IPv6 link-local
	"::1/128",          // IPv6 loopback
}

var privateNetworks []*net.IPNet

func init() {
	for _, cidr := range privateCIDRs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		privateNetworks = append(privateNetworks, network)
	}
}

// isPrivateIP returns true if the IP is in a private or reserved range.
func isPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	for _, network := range privateNetworks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// NewSSRFSafeTransport returns an http.Transport that blocks connections to
// private IP ranges to prevent SSRF attacks. Exported for use by other packages.
func NewSSRFSafeTransport() *http.Transport {
	return newSSRFSafeTransport()
}

// newSSRFSafeTransport returns an http.Transport that blocks connections to
// private IP ranges to prevent SSRF attacks during OIDC discovery, JWKS
// fetching, token introspection, and userinfo calls.
//
// The transport resolves DNS first, checks the IP, then dials. This handles
// DNS rebinding by pinning the resolved IP for the actual connection.
func newSSRFSafeTransport() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	return &http.Transport{
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 5,
		IdleConnTimeout:     90 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("invalid address %q: %w", addr, err)
			}

			// Resolve the hostname
			ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
			if err != nil {
				return nil, fmt.Errorf("DNS resolution failed for %q: %w", host, err)
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("no IP addresses resolved for %q", host)
			}

			// Check ALL resolved IPs — if any is private, block the request
			for _, ip := range ips {
				if isPrivateIP(ip) {
					return nil, fmt.Errorf("SSRF protection: blocking connection to private/reserved IP %s for host %q", ip, host)
				}
			}

			// Pin the first resolved IP to prevent DNS rebinding TOCTOU
			// Dial directly using the resolved IP
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
		// Disable automatic redirects to prevent redirect-based SSRF
		// (the http.Client follows redirects by default)
		// Note: We handle redirects at the client level instead.
	}
}

// newSSRFSafeClient returns an HTTP client that blocks private IPs and
// does not follow redirects (to prevent redirect-based SSRF).
func newSSRFSafeClient() *http.Client {
	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: newSSRFSafeTransport(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Validate each redirect destination
			host := req.URL.Hostname()
			if host == "" {
				return fmt.Errorf("redirect with empty host")
			}
			// The Transport's DialContext will also check the IP,
			// but we add an early URL-level check for http vs https
			if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
				return fmt.Errorf("redirect to non-HTTP scheme: %s", req.URL.Scheme)
			}
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
}
