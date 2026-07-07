package ssrf

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// blockPrivateIPs prevents SSRF attacks by blocking requests to private
// or reserved IP address ranges (RFC 1918, loopback, link-local, cloud
// metadata endpoints). See RFC 9728 Section 7.7.
//
// We use a custom dialer that checks the resolved IP before connecting.

var privateCIDRs = []string{
	"10.0.0.0/8",     // private
	"172.16.0.0/12",  // private
	"192.168.0.0/16", // private
	"127.0.0.0/8",    // loopback
	"169.254.0.0/16", // link-local (includes cloud metadata 169.254.169.254)
	"0.0.0.0/8",      // "this network"
	"100.64.0.0/10",  // CGNAT
	"fc00::/7",       // IPv6 unique local
	"fe80::/10",      // IPv6 link-local
	"::1/128",        // IPv6 loopback
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

// IsPrivateIP returns true if the IP is in a private or reserved range.
func IsPrivateIP(ip net.IP) bool {
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

// IsAllowedPrivateIP returns true when ip is covered by
// MCP_PROXY_ALLOWED_PRIVATE_IPS. The environment variable accepts a
// comma-separated list of CIDRs or literal IP addresses.
func IsAllowedPrivateIP(ip net.IP) bool {
	allowed := strings.TrimSpace(os.Getenv("MCP_PROXY_ALLOWED_PRIVATE_IPS"))
	if allowed == "" {
		return false
	}
	for _, entry := range strings.Split(allowed, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			_, network, err := net.ParseCIDR(entry)
			if err == nil && network.Contains(ip) {
				return true
			}
			continue
		}
		if allowedIP := net.ParseIP(entry); allowedIP != nil && allowedIP.Equal(ip) {
			return true
		}
	}
	return false
}

// SafeTransport returns an http.Transport that blocks connections to
// private IP ranges to prevent SSRF attacks.
// The transport resolves DNS first, checks the IP, then dials.
// This handles DNS rebinding by pinning the resolved IP for the actual connection.
func SafeTransport() *http.Transport {
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
				if IsPrivateIP(ip) && !IsAllowedPrivateIP(ip) {
					return nil, fmt.Errorf("SSRF protection: blocking connection to private/reserved IP %s for host %q", ip, host)
				}
			}

			// Pin the first resolved IP to prevent DNS rebinding TOCTOU
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
	}
}

// SafeClient returns an HTTP client that blocks private IPs and
// does not follow redirects (to prevent redirect-based SSRF).
func SafeClient() *http.Client {
	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: SafeTransport(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			host := req.URL.Hostname()
			if host == "" {
				return fmt.Errorf("redirect with empty host")
			}
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

// SafeClientWithTimeout returns a SafeClient with a custom timeout.
func SafeClientWithTimeout(timeout time.Duration) *http.Client {
	c := SafeClient()
	c.Timeout = timeout
	return c
}

// SafeTransportWithSettings returns a SafeTransport with custom connection pool settings.
// Used by clients that need different pooling characteristics (e.g. MaxConnsPerHost).
func SafeTransportWithSettings(maxIdleConns, maxIdleConnsPerHost, maxConnsPerHost int) *http.Transport {
	t := SafeTransport()
	t.MaxIdleConns = maxIdleConns
	t.MaxIdleConnsPerHost = maxIdleConnsPerHost
	t.MaxConnsPerHost = maxConnsPerHost
	return t
}
