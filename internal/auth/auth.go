package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/agentic/mcp-proxy/internal/models"
	"github.com/agentic/mcp-proxy/internal/store"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// AuthService handles authentication (JWT + API keys + optional OIDC).
type AuthService struct {
	store     *store.Store
	jwtSecret []byte
	oidc      *OIDCProvider
}

// New creates a new AuthService.
func New(s *store.Store, jwtSecret string) *AuthService {
	return &AuthService{
		store:     s,
		jwtSecret: []byte(jwtSecret),
	}
}

// SetOIDCProvider configures OIDC after construction.
func (a *AuthService) SetOIDCProvider(p *OIDCProvider) {
	a.oidc = p
}

// HasOIDC returns true if OIDC is configured.
func (a *AuthService) HasOIDC() bool {
	return a.oidc != nil
}

// OIDC returns the OIDC provider (nil if not configured).
func (a *AuthService) OIDC() *OIDCProvider {
	return a.oidc
}

// JWTSecret returns the JWT secret as a string.
func (a *AuthService) JWTSecret() string {
	return string(a.jwtSecret)
}

// CreateOAuthState creates a signed JWT encoding the MCP client's redirect_uri
// and state. This allows the proxy to forward the OAuth callback to the client
// after the upstream OIDC provider redirects back to the proxy's callback URL.
func (a *AuthService) CreateOAuthState(clientRedirectURI, clientState string) (string, error) {
	claims := jwt.MapClaims{
		"rd":  clientRedirectURI, // client's redirect_uri
		"st":  clientState,         // client's original state
		"exp": time.Now().Add(10 * time.Minute).Unix(),
		"iat": time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(a.jwtSecret)
}

// VerifyOAuthState decodes a proxy-issued state JWT and returns the client's
// redirect_uri and original state. Returns an error if the state is not a
// valid proxy-issued JWT (which means it's from the legacy MCP server auth flow).
func (a *AuthService) VerifyOAuthState(state string) (clientRedirectURI, clientState string, err error) {
	token, err := jwt.Parse(state, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return a.jwtSecret, nil
	})
	if err != nil || !token.Valid {
		return "", "", fmt.Errorf("invalid state")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", "", fmt.Errorf("invalid claims")
	}
	rd, _ := claims["rd"].(string)
	st, _ := claims["st"].(string)
	if rd == "" {
		return "", "", fmt.Errorf("missing redirect_uri in state")
	}
	return rd, st, nil
}

// HashPassword hashes a password using bcrypt.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// VerifyPassword checks a password against a bcrypt hash.
func VerifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// GenerateToken creates a JWT token for a user.
// Includes iss and aud claims to prevent cross-service token replay.
func (a *AuthService) GenerateToken(userID, username, role string) (string, time.Time, error) {
	expiresAt := time.Now().Add(24 * time.Hour)
	claims := jwt.MapClaims{
		"user_id":  userID,
		"username": username,
		"role":     role,
		"iss":      "mcp-proxy",
		"aud":      "mcp-proxy",
		"exp":      expiresAt.Unix(),
		"iat":      time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(a.jwtSecret)
	if err != nil {
		return "", time.Time{}, err
	}
	return tokenString, expiresAt, nil
}

// ValidateToken validates a JWT token and returns the claims.
// Validates issuer and audience to prevent cross-service token replay.
func (a *AuthService) ValidateToken(tokenString string) (jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return a.jwtSecret, nil
	}, jwt.WithIssuer("mcp-proxy"), jwt.WithAudience("mcp-proxy"))
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		return claims, nil
	}
	return nil, fmt.Errorf("invalid token")
}

// GenerateAPIKey creates a new API key string and stores its hash.
func (a *AuthService) GenerateAPIKey() (string, string, string, error) {
	// Generate a random key
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return "", "", "", err
	}
	keyString := "mcp_" + hex.EncodeToString(keyBytes)
	keyPrefix := keyString[:12] + "..."

	// Hash the key for storage
	hash := sha256.Sum256([]byte(keyString))
	keyHash := hex.EncodeToString(hash[:])

	return keyString, keyHash, keyPrefix, nil
}

// ValidateAPIKey checks an API key and returns the associated key record.
func (a *AuthService) ValidateAPIKey(keyString string) (*models.APIKey, error) {
	hash := sha256.Sum256([]byte(keyString))
	keyHash := hex.EncodeToString(hash[:])

	apiKey, err := a.store.GetAPIKeyByHash(keyHash)
	if err != nil || apiKey == nil {
		return nil, fmt.Errorf("invalid API key")
	}

	// Check expiration
	if apiKey.ExpiresAt != nil && time.Now().After(*apiKey.ExpiresAt) {
		return nil, fmt.Errorf("API key expired")
	}

	// Update last used
	_ = a.store.UpdateAPIKeyLastUsed(apiKey.ID)

	return apiKey, nil
}

// HasScope checks if the API key has a required scope.
func HasScope(scopes []string, required string) bool {
	for _, s := range scopes {
		if s == required || s == "admin" {
			return true
		}
	}
	return false
}

// ExtractToken extracts the Bearer token from the Authorization header.
func ExtractToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

// ExtractAPIKey extracts the API key from the header.
// Does NOT accept query params (api_key=) to avoid leaking keys in server logs and referrer headers.
func ExtractAPIKey(r *http.Request) string {
	// Check X-API-Key header first
	if key := r.Header.Get("X-API-Key"); key != "" {
		return key
	}
	// Check Authorization header with Bearer scheme
	if key := ExtractToken(r); key != "" && strings.HasPrefix(key, "mcp_") {
		return key
	}
	return ""
}

// JWTMiddleware protects routes requiring admin JWT auth.
func (a *AuthService) JWTMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenString := ExtractToken(r)
		if tokenString == "" {
			writeAuthError(w, "Missing or invalid Authorization header")
			return
		}

		claims, err := a.ValidateToken(tokenString)
		if err != nil {
			writeAuthError(w, "Invalid or expired token")
			return
		}

		// Inject claims into request context
		ctx := WithClaims(r.Context(), claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// APIKeyMiddleware protects MCP proxy routes requiring API key auth.
// Also accepts OIDC access tokens (Bearer) when OIDC is configured.
// Includes per-IP rate limiting to prevent brute-force and DoS attacks.
func (a *AuthService) APIKeyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Rate limit check — 120 req/min per IP
		ip := clientIP(r)
		if !apiLimiter.allow(ip) {
			w.Header().Set("Retry-After", "60")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"Rate limit exceeded. Try again later."}`))
			return
		}

		// Try API key first (X-API-Key, Bearer mcp_*, query param)
		keyString := ExtractAPIKey(r)
		if keyString != "" {
			apiKey, err := a.ValidateAPIKey(keyString)
			if err != nil {
				a.writeMCPAuthError(w, r, "Invalid or expired API key")
				return
			}
			ctx := WithAPIKey(r.Context(), apiKey)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Try OIDC access token (Bearer without mcp_ prefix)
		if a.HasOIDC() {
			bearerToken := ExtractToken(r)
			if bearerToken != "" {
				log.Printf("[MCP-Auth] Attempting OIDC validation (token_len=%d, starts_with_eyJ=%v, path=%s)",
					len(bearerToken), strings.HasPrefix(bearerToken, "eyJ"), r.URL.Path)
				user, err := a.oidc.ValidateAccessToken(bearerToken)
				if err == nil {
					// Create synthetic API key with full access
					syntheticKey := &models.APIKey{
						ID:     "oidc:" + user.Subject,
						Name:   user.Username,
						Scopes: []string{"read", "write", "admin"},
						Active:  true,
					}
					ctx := WithAPIKey(r.Context(), syntheticKey)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				// Log the OIDC validation error for debugging
				log.Printf("[MCP-Auth] OIDC token validation failed: %v (token_len=%d, path=%s)", err, len(bearerToken), r.URL.Path)
			}
		}

		a.writeMCPAuthError(w, r, "Missing or invalid API key")
	})
}

// writeMCPAuthError writes a 401 with WWW-Authenticate header for MCP client discovery.
// Also logs the auth failure for debugging MCP client connection issues.
func (a *AuthService) writeMCPAuthError(w http.ResponseWriter, r *http.Request, message string) {
	// Log the auth failure with request details for debugging
	clientIP := r.RemoteAddr
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		clientIP = forwarded
	}
	log.Printf("[MCP-Auth] 401 %s %s — %s (client=%s, has_auth_header=%v, auth_methods=%s)",
		r.Method, r.URL.Path, message, clientIP,
		r.Header.Get("Authorization") != "" || r.Header.Get("X-API-Key") != "",
		a.authMethodsDescription())

	// If OIDC is configured, add resource_metadata hint for MCP clients.
	// Per RFC 9728, the resource_metadata URL must point to the path that
	// matches the resource (path-insertion variant). MCP clients try this
	// variant FIRST. For a request to /api/compounds/{id}/mcp, the URL becomes:
	// https://host/.well-known/oauth-protected-resource/api/compounds/{id}/mcp
	if a.HasOIDC() {
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		metadataURL := fmt.Sprintf("%s://%s/.well-known/oauth-protected-resource%s", scheme, r.Host, r.URL.Path)
		w.Header().Set("WWW-Authenticate",
			fmt.Sprintf(`Bearer resource_metadata="%s", scope="openid profile email"`, metadataURL))
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// authMethodsDescription returns a comma-separated list of configured auth methods.
func (a *AuthService) authMethodsDescription() string {
	methods := []string{"api-key"}
	if a.HasOIDC() {
		methods = append(methods, "oidc")
	}
	return strings.Join(methods, ", ")
}

// AdminUserFromContext extracts the admin user info from JWT claims in context.
func AdminUserFromContext(r *http.Request) string {
	claims, ok := r.Context().Value(claimsKey{}).(jwt.MapClaims)
	if !ok {
		return ""
	}
	if username, ok := claims["username"].(string); ok {
		return username
	}
	return ""
}

func writeAuthError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// EnsureDefaultAdmin creates a default admin user if none exists.
// If the admin user already exists, it logs a warning if the provided
// password doesn't match the stored hash (indicating MCP_PROXY_ADMIN_PASS
// was changed but not applied via the UI/API).
func (a *AuthService) EnsureDefaultAdmin(username, password string) error {
	existing, err := a.store.GetUserByUsername(username)
	if err == nil && existing != nil {
		// User exists — check if the provided password matches
		if !VerifyPassword(existing.PasswordHash, password) {
			log.Printf("WARNING: MCP_PROXY_ADMIN_PASS does not match the stored password for user '%s'. "+
				"The password was not rotated. Change it via the web UI or API, or delete the user to re-create with the new password.",
				username)
		}
		return nil
	}

	hash, err := HashPassword(password)
	if err != nil {
		return err
	}

	user := &models.User{
		ID:           generateID("usr"),
		Username:     username,
		PasswordHash: hash,
		Role:         "admin",
		CreatedAt:    time.Now(),
	}
	return a.store.CreateUser(user)
}

// generateID creates a short unique ID with a prefix.
func generateID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// This should never happen — crypto/rand failure indicates a critical
		// system issue. Fall back to timestamp-based uniqueness to avoid panicking.
		log.Printf("WARNING: crypto/rand failed in generateID: %v — using timestamp fallback", err)
		return fmt.Sprintf("%s_%x", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(b)
}

// ConstantTimeCompare is re-exported for convenience.
var _ = subtle.ConstantTimeCompare
var _ = os.Getenv
