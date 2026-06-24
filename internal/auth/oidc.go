package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/agentic/mcp-proxy/internal/models"
	"golang.org/x/oauth2"
)

// OIDCProviderConfig holds discovered OIDC endpoints.
type OIDCProviderConfig struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
	JwksURI               string `json:"jwks_uri"`
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
	}
	if err := p.discover(); err != nil {
		return nil, fmt.Errorf("OIDC discovery failed: %w", err)
	}
	return p, nil
}

// discover fetches the OIDC well-known configuration.
func (p *OIDCProvider) discover() error {
	wellKnown := strings.TrimSuffix(p.config.Issuer, "/") + "/.well-known/openid-configuration"

	resp, err := http.Get(wellKnown)
	if err != nil {
		return fmt.Errorf("failed to fetch OIDC discovery: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("OIDC discovery returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
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

// AuthURL generates the authorization URL with PKCE-like state.
func (p *OIDCProvider) AuthURL(state string, redirectURL string) string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.oauth2 == nil {
		return ""
	}
	// Use a per-request config to allow dynamic redirect URL
	cfg := *p.oauth2
	cfg.RedirectURL = redirectURL
	return cfg.AuthCodeURL(state, oauth2.AccessTypeOnline)
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
	return cfg.Exchange(nil, code)
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

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch userinfo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("userinfo returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var info map[string]interface{}
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("failed to parse userinfo: %w", err)
	}

	return info, nil
}

// ValidateAccessToken validates an OIDC access token and returns the user.
// Uses a 5-minute cache to avoid calling userinfo on every request.
func (p *OIDCProvider) ValidateAccessToken(accessToken string) (ProviderUser, error) {
	// Check cache first
	p.cacheMu.RLock()
	if cached, ok := p.tokenCache[accessToken]; ok && time.Now().Before(cached.expiresAt) {
		p.cacheMu.RUnlock()
		return cached.user, nil
	}
	p.cacheMu.RUnlock()

	// Call userinfo
	info, err := p.UserInfo(accessToken)
	if err != nil {
		return ProviderUser{}, err
	}

	user := ExtractUser(info)
	if user.Subject == "" {
		return ProviderUser{}, fmt.Errorf("no subject in userinfo response")
	}

	// Cache for 5 minutes
	p.cacheMu.Lock()
	p.tokenCache[accessToken] = cachedToken{
		user:      user,
		expiresAt: time.Now().Add(5 * time.Minute),
	}
	// Prune expired entries
	for k, v := range p.tokenCache {
		if time.Now().After(v.expiresAt) {
			delete(p.tokenCache, k)
		}
	}
	p.cacheMu.Unlock()

	return user, nil
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

// ProviderUser represents the extracted user info from OIDC.
type ProviderUser struct {
	Subject string
	Email   string
	Name    string
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
		u.Username = "oidc-" + u.Subject[:8]
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
