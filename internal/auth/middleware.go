package auth

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type claimsKey struct{}
type apiKeyKey struct{}

// WithClaims injects JWT claims into the context.
func WithClaims(ctx context.Context, claims jwt.MapClaims) context.Context {
	return context.WithValue(ctx, claimsKey{}, claims)
}

// ClaimsFromContext extracts JWT claims from context.
func ClaimsFromContext(ctx context.Context) jwt.MapClaims {
	claims, _ := ctx.Value(claimsKey{}).(jwt.MapClaims)
	return claims
}

// WithAPIKey injects an API key into the context.
func WithAPIKey(ctx context.Context, key interface{}) context.Context {
	return context.WithValue(ctx, apiKeyKey{}, key)
}

// APIKeyFromContext extracts the API key from context.
func APIKeyFromContext(ctx context.Context) interface{} {
	return ctx.Value(apiKeyKey{})
}

// AdminMiddleware is an alias for JWTMiddleware for clarity.
func (a *AuthService) AdminMiddleware(next http.Handler) http.Handler {
	return a.JWTMiddleware(next)
}

// rateLimiter tracks per-IP request counts for a given window.
type rateLimiter struct {
	mu       sync.Mutex
	requests map[string][]time.Time
	window   time.Duration
	maxReqs  int
}

func newRateLimiter(window time.Duration, maxReqs int) *rateLimiter {
	return &rateLimiter{
		requests: make(map[string][]time.Time),
		window:   window,
		maxReqs:  maxReqs,
	}
}

// allow returns true if the IP is within the rate limit.
func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	// Filter to recent requests
	var recent []time.Time
	for _, t := range rl.requests[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}

	if len(recent) >= rl.maxReqs {
		rl.requests[ip] = recent
		return false
	}

	recent = append(recent, now)
	rl.requests[ip] = recent
	return true
}

// cleanup removes old entries periodically (call in a goroutine).
func (rl *rateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	cutoff := time.Now().Add(-rl.window * 2)
	for ip, times := range rl.requests {
		var recent []time.Time
		for _, t := range times {
			if t.After(cutoff) {
				recent = append(recent, t)
			}
		}
		if len(recent) == 0 {
			delete(rl.requests, ip)
		} else {
			rl.requests[ip] = recent
		}
	}
}

// LoginRateLimitMiddleware applies rate limiting to login attempts.
var loginLimiter = newRateLimiter(time.Minute, 10)

// apiLimiter limits MCP API endpoint requests per IP to prevent brute-force
// and DoS attacks. 120 req/min is generous for normal MCP client usage.
var apiLimiter = newRateLimiter(time.Minute, 120)

func (a *AuthService) LoginRateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !loginLimiter.allow(ip) {
			w.Header().Set("Retry-After", "60")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"Too many login attempts. Try again later."}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP extracts the real client IP from the request, accounting for reverse proxies.
// Only trusts X-Forwarded-For / X-Real-IP when the direct connection is from a loopback
// address (i.e. the request came through a reverse proxy like Traefik/nginx).
// This prevents rate-limit bypass via spoofed X-Forwarded-For headers from direct clients.
func clientIP(r *http.Request) string {
	// Check if the direct connection is from a loopback address (reverse proxy)
	remoteHost := r.RemoteAddr
	if colonIdx := strings.LastIndex(remoteHost, ":"); colonIdx >= 0 {
		remoteHost = remoteHost[:colonIdx]
	}
	isLoopback := remoteHost == "127.0.0.1" || remoteHost == "::1" || remoteHost == "[::1]" || remoteHost == "localhost"

	if isLoopback {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// Take the first IP in the list
			for i := 0; i < len(xff); i++ {
				if xff[i] == ',' {
					return strings.TrimSpace(xff[:i])
				}
			}
			return strings.TrimSpace(xff)
		}
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			return strings.TrimSpace(xri)
		}
	}
	return remoteHost
}

// SecurityHeadersMiddleware adds security headers to all responses.
func SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Prevent clickjacking
		w.Header().Set("X-Frame-Options", "DENY")
		// Prevent MIME-type sniffing
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Disable buggy legacy XSS auditor (modern browsers ignore it;
		// setting to 0 prevents older browsers from enabling it)
		w.Header().Set("X-XSS-Protection", "0")
		// Control referrer information
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		// Enable HSTS (only meaningful on HTTPS, but safe to always set)
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		// Restrict browser features (camera, microphone, geolocation)
		w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		// Content-Security-Policy: allow same-origin + inline styles (for shadcn) + data: images
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; "+
				"font-src 'self' data:; "+
				"connect-src 'self'; "+
				"frame-ancestors 'none'; "+
				"base-uri 'self'")

		// Limit request body size to 10MB — prevents memory exhaustion from large payloads.
		// 10MB is generous enough for MCP tool calls while preventing abuse.
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
		}

		next.ServeHTTP(w, r)
	})
}

// CORSMiddleware adds CORS headers. In production, restricts to the same origin.
// Public OAuth discovery endpoints (/.well-known/*, /api/oauth/*) allow any origin
// so that MCP clients like Raycast can fetch them cross-origin.
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			// Public OAuth discovery endpoints must be accessible from any origin.
			isPublicPath := strings.HasPrefix(r.URL.Path, "/.well-known/") ||
				strings.HasPrefix(r.URL.Path, "/api/oauth/")

			if isPublicPath {
				// Public OAuth discovery endpoints: allow any origin but WITHOUT credentials.
				// Returning Access-Control-Allow-Credentials: true with a reflected origin
				// would allow cross-origin sites to make authenticated requests.
				w.Header().Set("Access-Control-Allow-Origin", "*")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key, MCP-Protocol-Version, Mcp-Session-Id")
				w.Header().Set("Access-Control-Max-Age", "3600")
				// Deliberately NOT setting Allow-Credentials: true
			} else {
				// Only allow same-origin requests (the frontend is served by the same server)
				// Check if the Origin matches the request Host
				scheme := "https"
				if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
					scheme = "http"
				}
				allowedOrigin := scheme + "://" + r.Host
				if origin == allowedOrigin {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-API-Key")
					w.Header().Set("Access-Control-Allow-Credentials", "true")
					w.Header().Set("Access-Control-Max-Age", "3600")
				}
			}
		}

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// StartCleanupRoutine starts a background goroutine to clean up expired rate limit entries.
func StartCleanupRoutine() {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			loginLimiter.cleanup()
			apiLimiter.cleanup()
		}
	}()
}

// RateLimitRemaining is a helper for debug/logging.
func RateLimitRemaining(ip string) (int, string) {
	loginLimiter.mu.Lock()
	defer loginLimiter.mu.Unlock()
	cutoff := time.Now().Add(-loginLimiter.window)
	count := 0
	for _, t := range loginLimiter.requests[ip] {
		if t.After(cutoff) {
			count++
		}
	}
	remaining := loginLimiter.maxReqs - count
	if remaining < 0 {
		remaining = 0
	}
	return remaining, strconv.Itoa(remaining)
}

// APIRateLimitMiddleware applies rate limiting to MCP API endpoints.
// Returns 429 when a client exceeds 120 requests per minute.
func (a *AuthService) APIRateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !apiLimiter.allow(ip) {
			w.Header().Set("Retry-After", "60")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"Rate limit exceeded. Try again later."}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}
