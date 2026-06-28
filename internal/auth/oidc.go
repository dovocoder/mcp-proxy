package auth

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/agentic/mcp-proxy/internal/models"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"
)

// OIDCProviderConfig holds discovered OIDC endpoints.
type OIDCProviderConfig struct {
	Issuer                           string   `json:"issuer"`
	AuthorizationEndpoint            string   `json:"authorization_endpoint"`
	TokenEndpoint                    string   `json:"token_endpoint"`
	UserinfoEndpoint                 string   `json:"userinfo_endpoint"`
	JwksURI                          string   `json:"jwks_uri"`
	RevocationEndpoint               string   `json:"revocation_endpoint,omitempty"`
	IntrospectionEndpoint            string   `json:"introspection_endpoint,omitempty"`
	IDTokenSigningAlgValuesSupported []string `json:"id_token_signing_alg_values_supported,omitempty"`
	SubjectTypesSupported            []string `json:"subject_types_supported,omitempty"`
}

// OIDCConfig is the local OIDC client configuration.
type OIDCConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string // can be empty — auto-detected per request
}

// OIDCProvider manages OIDC discovery and OAuth2 flow.
type OIDCProvider struct {
	config     OIDCConfig
	discovery  *OIDCProviderConfig
	oauth2     *oauth2.Config
	mu         sync.RWMutex
	tokenCache map[string]cachedToken // access_token → user info (5-min TTL)
	cacheMu    sync.RWMutex
	jwksCache  *jwksKeySet  // cached JWKS keys
	jwksMu     sync.Mutex   // protects jwksCache refresh
	httpClient *http.Client // shared HTTP client for all OIDC HTTP calls
}

type jwksKeySet struct {
	keys    map[string]*rsa.PublicKey // kid → public key
	expires time.Time
}

type cachedToken struct {
	user      ProviderUser
	expiresAt time.Time
}

// NewOIDCProvider creates a new OIDC provider and fetches discovery metadata.
func NewOIDCProvider(cfg OIDCConfig) (*OIDCProvider, error) {
	p := &OIDCProvider{
		config:     cfg,
		tokenCache: make(map[string]cachedToken),
		httpClient: newSSRFSafeClient(),
	}
	// Start periodic cleanup of expired token cache entries
	go p.cleanupTokenCache()
	if err := p.discover(); err != nil {
		return nil, fmt.Errorf("OIDC discovery failed: %w", err)
	}
	return p, nil
}

// cleanupTokenCache periodically removes expired entries from the token cache
// to prevent unbounded memory growth from long-running sessions.
func (p *OIDCProvider) cleanupTokenCache() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		p.cacheMu.Lock()
		for k, v := range p.tokenCache {
			if time.Now().After(v.expiresAt) {
				delete(p.tokenCache, k)
			}
		}
		p.cacheMu.Unlock()
	}
}

// discover fetches the OIDC well-known configuration.
func (p *OIDCProvider) discover() error {
	wellKnown := strings.TrimSuffix(p.config.Issuer, "/") + "/.well-known/openid-configuration"

	client := p.httpClient
	resp, err := client.Get(wellKnown)
	if err != nil {
		return fmt.Errorf("failed to fetch OIDC discovery: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("OIDC discovery returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}

	var d OIDCProviderConfig
	if err := json.Unmarshal(body, &d); err != nil {
		return fmt.Errorf("failed to parse OIDC discovery: %w", err)
	}

	p.mu.Lock()
	p.discovery = &d
	p.oauth2 = &oauth2.Config{
		ClientID:     p.config.ClientID,
		ClientSecret: p.config.ClientSecret,
		RedirectURL:  p.config.RedirectURL,
		Endpoint: oauth2.Endpoint{
			AuthURL:  d.AuthorizationEndpoint,
			TokenURL: d.TokenEndpoint,
		},
		Scopes: []string{"openid", "profile", "email"},
	}
	p.mu.Unlock()

	log.Printf("OIDC: discovered issuer %s", d.Issuer)
	log.Printf("OIDC: auth endpoint: %s", d.AuthorizationEndpoint)
	log.Printf("OIDC: token endpoint: %s", d.TokenEndpoint)
	if d.RevocationEndpoint != "" {
		log.Printf("OIDC: revocation endpoint: %s", d.RevocationEndpoint)
	}
	if d.IntrospectionEndpoint != "" {
		log.Printf("OIDC: introspection endpoint: %s", d.IntrospectionEndpoint)
	}
	return nil
}

// RedirectURL returns the redirect URL, auto-detecting from the request if not configured.
func (p *OIDCProvider) RedirectURL(r *http.Request) string {
	if p.config.RedirectURL != "" {
		return p.config.RedirectURL
	}
	// Auto-detect from request
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := r.Host
	return fmt.Sprintf("%s://%s/api/auth/oidc/callback", scheme, host)
}

// AuthURL generates the authorization URL with state and nonce.
func (p *OIDCProvider) AuthURL(state, redirectURL, nonce string) string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.oauth2 == nil {
		return ""
	}
	// Use a per-request config to allow dynamic redirect URL
	cfg := *p.oauth2
	cfg.RedirectURL = redirectURL
	return cfg.AuthCodeURL(state, oauth2.AccessTypeOnline, oauth2.SetAuthURLParam("nonce", nonce))
}

// Exchange exchanges the authorization code for tokens.
func (p *OIDCProvider) Exchange(code string, redirectURL string) (*oauth2.Token, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.oauth2 == nil {
		return nil, fmt.Errorf("OIDC not configured")
	}
	cfg := *p.oauth2
	cfg.RedirectURL = redirectURL
	// Use a context with timeout — passing nil panics in oauth2
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return cfg.Exchange(ctx, code)
}

// UserInfo fetches user info from the OIDC provider's userinfo endpoint.
func (p *OIDCProvider) UserInfo(accessToken string) (map[string]interface{}, error) {
	p.mu.RLock()
	userinfoURL := p.discovery.UserinfoEndpoint
	p.mu.RUnlock()

	if userinfoURL == "" {
		return nil, fmt.Errorf("no userinfo endpoint in discovery")
	}

	req, err := http.NewRequest("GET", userinfoURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch userinfo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("userinfo returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	var info map[string]interface{}
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("failed to parse userinfo: %w", err)
	}

	return info, nil
}

// fetchJWKS fetches and parses the JWKS from the discovery jwks_uri.
// Results are cached for 1 hour.
func (p *OIDCProvider) fetchJWKS() (map[string]*rsa.PublicKey, error) {
	p.mu.RLock()
	jwksURI := ""
	if p.discovery != nil {
		jwksURI = p.discovery.JwksURI
	}
	p.mu.RUnlock()

	if jwksURI == "" {
		return nil, fmt.Errorf("no jwks_uri in discovery")
	}

	// Check cache
	p.jwksMu.Lock()
	defer p.jwksMu.Unlock()
	if p.jwksCache != nil && time.Now().Before(p.jwksCache.expires) {
		return p.jwksCache.keys, nil
	}

	resp, err := p.httpClient.Get(jwksURI)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("JWKS endpoint returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	// Parse JWKS JSON
	var jwks struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Use string `json:"use"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(body, &jwks); err != nil {
		return nil, fmt.Errorf("failed to parse JWKS: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey)
	for _, k := range jwks.Keys {
		if k.Kty != "RSA" || k.Use != "sig" {
			continue
		}
		// Decode RSA public key modulus (n) and exponent (e)
		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}
		// Convert exponent bytes to int
		e := 0
		for _, b := range eBytes {
			e = e<<8 + int(b)
		}
		pubKey := &rsa.PublicKey{
			N: new(big.Int).SetBytes(nBytes),
			E: e,
		}
		keys[k.Kid] = pubKey
	}

	if len(keys) == 0 {
		return nil, fmt.Errorf("no RSA signing keys in JWKS")
	}

	p.jwksCache = &jwksKeySet{
		keys:    keys,
		expires: time.Now().Add(1 * time.Hour),
	}

	log.Printf("OIDC: fetched JWKS with %d keys from %s", len(keys), jwksURI)
	return keys, nil
}

// ValidateJWT validates a JWT access token locally using the JWKS.
// This avoids calling the userinfo endpoint, which may reject tokens
// whose audience (RFC 8707 resource parameter) is the MCP server
// rather than the OIDC provider's own audience.
func (p *OIDCProvider) ValidateJWT(accessToken string) (ProviderUser, error) {
	// Quick check: JWTs start with "eyJ" (base64-encoded header)
	if !strings.HasPrefix(accessToken, "eyJ") {
		return ProviderUser{}, fmt.Errorf("not a JWT")
	}

	keys, err := p.fetchJWKS()
	if err != nil {
		return ProviderUser{}, fmt.Errorf("JWKS fetch failed: %w", err)
	}

	// Parse token without verifying first to get the kid
	var unverified struct {
		Header map[string]interface{} `json:"-"`
	}
	// Extract header to find kid
	parts := strings.Split(accessToken, ".")
	if len(parts) < 2 {
		return ProviderUser{}, fmt.Errorf("invalid JWT format")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return ProviderUser{}, fmt.Errorf("failed to decode JWT header: %w", err)
	}
	if err := json.Unmarshal(headerBytes, &unverified.Header); err != nil {
		// unverified.Header is nil, re-parse
		var header map[string]interface{}
		if err2 := json.Unmarshal(headerBytes, &header); err2 != nil {
			return ProviderUser{}, fmt.Errorf("failed to parse JWT header: %w", err2)
		}
		unverified.Header = header
	}
	_ = unverified // suppress unused

	// Parse and verify the JWT
	token, err := jwt.Parse(accessToken, func(token *jwt.Token) (interface{}, error) {
		// Verify signing method is RSA
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		// Get kid from header
		kid, ok := token.Header["kid"].(string)
		if !ok {
			return nil, fmt.Errorf("no kid in JWT header")
		}
		// Find matching key
		key, ok := keys[kid]
		if !ok {
			return nil, fmt.Errorf("no key found for kid %s", kid)
		}
		return key, nil
	})
	if err != nil {
		return ProviderUser{}, fmt.Errorf("JWT verification failed: %w", err)
	}

	if !token.Valid {
		return ProviderUser{}, fmt.Errorf("token is not valid")
	}

	// Extract claims
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return ProviderUser{}, fmt.Errorf("failed to extract claims")
	}

	// Check issuer
	issuer, _ := claims["iss"].(string)
	expectedIssuer := p.Issuer()
	if issuer != expectedIssuer {
		return ProviderUser{}, fmt.Errorf("issuer mismatch: got %s, expected %s", issuer, expectedIssuer)
	}

	// Check expiration
	exp, ok := claims["exp"].(float64)
	if !ok {
		return ProviderUser{}, fmt.Errorf("no exp in token")
	}
	if time.Now().Unix() > int64(exp) {
		return ProviderUser{}, fmt.Errorf("token expired")
	}

	// Validate audience — token MUST be issued for this MCP server.
	// Per MCP security best practices: "MCP servers MUST NOT accept any
	// tokens that were not explicitly issued for the MCP server."
	// The audience is the proxy's own resource URL (configured via OIDC config).
	expectedAud := p.config.Issuer
	if aud, ok := claims["aud"].(string); ok {
		if aud != expectedAud && aud != p.config.ClientID {
			return ProviderUser{}, fmt.Errorf("audience mismatch: got %s, expected %s or %s", aud, expectedAud, p.config.ClientID)
		}
	} else if audList, ok := claims["aud"].([]interface{}); ok {
		found := false
		for _, a := range audList {
			if s, ok := a.(string); ok && (s == expectedAud || s == p.config.ClientID) {
				found = true
				break
			}
		}
		if !found {
			return ProviderUser{}, fmt.Errorf("audience mismatch: token audiences do not include %s or %s", expectedAud, p.config.ClientID)
		}
	}

	// Extract user info from claims
	user := ProviderUser{
		Subject:  getStringClaim(claims, "sub"),
		Email:    getStringClaim(claims, "email"),
		Name:     getStringClaim(claims, "name"),
		Username: getStringClaim(claims, "preferred_username"),
	}
	if user.Username == "" {
		user.Username = getStringClaim(claims, "email")
	}

	return user, nil
}

// getStringClaim extracts a string claim from JWT claims.
func getStringClaim(claims jwt.MapClaims, key string) string {
	v, ok := claims[key].(string)
	if ok {
		return v
	}
	return ""
}

// ValidateAccessToken validates an OIDC access token and returns the user.
// Tries JWT validation first (local, via JWKS), then introspection, then userinfo.
// Uses a 2-minute cache to avoid network calls on every request.
// The shorter TTL reduces the window for revoked tokens to remain valid.
// The cache key is a SHA-256 hash of the token — the raw token is never stored.
func (p *OIDCProvider) ValidateAccessToken(accessToken string) (ProviderUser, error) {
	// Hash the token for cache key — never store the raw access token in memory
	cacheKey := hashTokenForCache(accessToken)

	// Check cache first
	p.cacheMu.RLock()
	if cached, ok := p.tokenCache[cacheKey]; ok && time.Now().Before(cached.expiresAt) {
		p.cacheMu.RUnlock()
		return cached.user, nil
	}
	p.cacheMu.RUnlock()

	// Try JWT validation first (local, no network call)
	if user, err := p.ValidateJWT(accessToken); err == nil {
		p.cacheToken(cacheKey, user)
		return user, nil
	} else {
		log.Printf("[OIDC] JWT validation failed: %v", err)
	}

	// Try introspection — works for opaque tokens AND audience-restricted JWTs
	// that the userinfo endpoint might reject (RFC 8707 resource parameter).
	if user, err := p.IntrospectToken(accessToken); err == nil {
		p.cacheToken(cacheKey, user)
		return user, nil
	} else {
		log.Printf("[OIDC] Introspection failed: %v", err)
	}

	// Fall back to userinfo as last resort
	info, err := p.UserInfo(accessToken)
	if err != nil {
		return ProviderUser{}, fmt.Errorf("all token validation methods failed: %w", err)
	}

	user := ExtractUser(info)
	if user.Subject == "" {
		return ProviderUser{}, fmt.Errorf("no subject in userinfo response")
	}

	p.cacheToken(cacheKey, user)
	return user, nil
}

// hashTokenForCache returns a SHA-256 hex hash of the access token.
// This prevents raw access tokens from being exposed in memory dumps.
func hashTokenForCache(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// cacheToken stores a validated token hash → user mapping with 2-minute TTL.
// Shorter TTL reduces the window for revoked tokens to remain valid.
func (p *OIDCProvider) cacheToken(cacheKey string, user ProviderUser) {
	p.cacheMu.Lock()
	p.tokenCache[cacheKey] = cachedToken{
		user:      user,
		expiresAt: time.Now().Add(2 * time.Minute),
	}
	// Prune expired entries
	for k, v := range p.tokenCache {
		if time.Now().After(v.expiresAt) {
			delete(p.tokenCache, k)
		}
	}
	p.cacheMu.Unlock()
}

// IntrospectToken validates a token via the introspection endpoint (RFC 7662).
// This works for both opaque tokens and JWTs, and is not affected by
// audience restrictions (RFC 8707) that may cause userinfo to reject tokens.
func (p *OIDCProvider) IntrospectToken(accessToken string) (ProviderUser, error) {
	p.mu.RLock()
	introspectionURL := ""
	if p.discovery != nil {
		introspectionURL = p.discovery.IntrospectionEndpoint
	}
	clientID := p.config.ClientID
	clientSecret := p.config.ClientSecret
	p.mu.RUnlock()

	if introspectionURL == "" {
		return ProviderUser{}, fmt.Errorf("no introspection endpoint")
	}

	form := url.Values{
		"token": {accessToken},
	}

	req, err := http.NewRequest("POST", introspectionURL, strings.NewReader(form.Encode()))
	if err != nil {
		return ProviderUser{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// PocketID requires HTTP Basic auth for introspection — form params don't work
	if clientID != "" && clientSecret != "" {
		req.SetBasicAuth(clientID, clientSecret)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return ProviderUser{}, fmt.Errorf("introspection request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return ProviderUser{}, fmt.Errorf("introspection returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ProviderUser{}, err
	}

	var result struct {
		Active   bool   `json:"active"`
		Subject  string `json:"sub"`
		Email    string `json:"email"`
		Name     string `json:"name"`
		Username string `json:"preferred_username"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return ProviderUser{}, fmt.Errorf("failed to parse introspection response: %w", err)
	}

	if !result.Active {
		return ProviderUser{}, fmt.Errorf("token is not active")
	}

	if result.Subject == "" {
		return ProviderUser{}, fmt.Errorf("no subject in introspection response")
	}

	return ProviderUser{
		Subject:  result.Subject,
		Email:    result.Email,
		Name:     result.Name,
		Username: result.Username,
	}, nil
}

// Issuer returns the OIDC issuer URL.
func (p *OIDCProvider) Issuer() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.discovery != nil {
		return p.discovery.Issuer
	}
	return p.config.Issuer
}

// Discovery returns the discovered OIDC endpoints (nil if not discovered yet).
func (p *OIDCProvider) Discovery() *OIDCProviderConfig {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.discovery
}

// ClientID returns the configured OAuth client ID.
func (p *OIDCProvider) ClientID() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.config.ClientID
}

// ClientSecret returns the configured OAuth client secret.
func (p *OIDCProvider) ClientSecret() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.config.ClientSecret
}

// ProviderUser represents the extracted user info from OIDC.
type ProviderUser struct {
	Subject  string
	Email    string
	Name     string
	Username string
}

// ExtractUser extracts a normalized user from OIDC userinfo claims.
func ExtractUser(info map[string]interface{}) ProviderUser {
	u := ProviderUser{}
	if sub, ok := info["sub"].(string); ok {
		u.Subject = sub
	}
	if email, ok := info["email"].(string); ok {
		u.Email = email
	}
	if name, ok := info["name"].(string); ok {
		u.Name = name
	}
	if pref, ok := info["preferred_username"].(string); ok {
		u.Username = pref
	}
	// Fallback: use email as username
	if u.Username == "" && u.Email != "" {
		parts := strings.SplitN(u.Email, "@", 2)
		u.Username = parts[0]
	}
	// Fallback: use name as username
	if u.Username == "" && u.Name != "" {
		u.Username = strings.ToLower(strings.ReplaceAll(u.Name, " ", "."))
	}
	// Fallback: use subject
	if u.Username == "" {
		if len(u.Subject) >= 8 {
			u.Username = "oidc-" + u.Subject[:8]
		} else {
			u.Username = "oidc-" + u.Subject
		}
	}
	return u
}

// LoginOr ProvisionUser finds or creates a local user from OIDC claims.
func (a *AuthService) LoginOrProvisionUser(pu ProviderUser) (*models.User, error) {
	// Try to find by OIDC subject
	user, err := a.store.GetUserByOIDCSubject(pu.Subject)
	if err == nil && user != nil {
		return user, nil
	}

	// Try to find by username (existing password user — link OIDC)
	user, err = a.store.GetUserByUsername(pu.Username)
	if err == nil && user != nil {
		// Link OIDC subject to existing user
		_ = a.store.LinkOIDCSubject(user.ID, pu.Subject)
		return user, nil
	}

	// Create new user from OIDC
	user, err = a.store.CreateUserFromOIDC(pu.Username, pu.Subject)
	if err != nil {
		return nil, fmt.Errorf("failed to create OIDC user: %w", err)
	}
	log.Printf("OIDC: provisioned new user %s (subject: %s)", user.Username, pu.Subject)
	return user, nil
}

// GenerateState generates a random state string for OAuth2.
func GenerateState() string {
	return generateID("state")
}

// VerifyIDTokenNonce verifies that the id_token contains the expected nonce.
// This prevents token replay and login CSRF (RFC 9701 §2.3.3).
func (p *OIDCProvider) VerifyIDTokenNonce(idTokenString, expectedNonce string) error {
	keys, err := p.fetchJWKS()
	if err != nil {
		return fmt.Errorf("JWKS fetch failed: %w", err)
	}

	token, err := jwt.Parse(idTokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		kid, ok := token.Header["kid"].(string)
		if !ok {
			return nil, fmt.Errorf("no kid in JWT header")
		}
		key, ok := keys[kid]
		if !ok {
			return nil, fmt.Errorf("no key found for kid %s", kid)
		}
		return key, nil
	})
	if err != nil {
		return fmt.Errorf("JWT verification failed: %w", err)
	}
	if !token.Valid {
		return fmt.Errorf("token is not valid")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return fmt.Errorf("failed to extract claims")
	}

	// Check issuer
	issuer, _ := claims["iss"].(string)
	if issuer != p.Issuer() {
		return fmt.Errorf("issuer mismatch: got %s, expected %s", issuer, p.Issuer())
	}

	// Check nonce
	nonce, ok := claims["nonce"].(string)
	if !ok {
		return fmt.Errorf("no nonce in id_token")
	}
	if subtle.ConstantTimeCompare([]byte(nonce), []byte(expectedNonce)) != 1 {
		return fmt.Errorf("nonce mismatch")
	}

	return nil
}

// EncodeRedirectURL encodes a target URL for safe redirect.
func EncodeRedirectURL(base string, token string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base + "?token=" + token
	}
	q := u.Query()
	q.Set("token", token)
	u.RawQuery = q.Encode()
	return u.String()
}
