package mcp

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OAuthServerMetadata represents the OAuth 2.0 Authorization Server Metadata (RFC8414).
type OAuthServerMetadata struct {
	Issuer                 string   `json:"issuer"`
	AuthorizationEndpoint  string   `json:"authorization_endpoint"`
	TokenEndpoint          string   `json:"token_endpoint"`
	RegistrationEndpoint   string   `json:"registration_endpoint,omitempty"`
	RevocationEndpoint     string   `json:"revocation_endpoint,omitempty"`
	IntrospectionEndpoint  string   `json:"introspection_endpoint,omitempty"`
	ScopesSupported        []string `json:"scopes_supported,omitempty"`
	ResponseTypesSupported []string `json:"response_types_supported,omitempty"`
	GrantTypesSupported    []string `json:"grant_types_supported,omitempty"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported,omitempty"`
	CodeChallengeMethodsSupported      []string `json:"code_challenge_methods_supported,omitempty"`
}

// OAuthTokens represents stored OAuth tokens.
type OAuthTokens struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	Scope        string    `json:"scope,omitempty"`
}

// IsExpired returns true if the access token has expired.
func (t *OAuthTokens) IsExpired() bool {
	if t.ExpiresAt.IsZero() {
		return false
	}
	// Consider token expired 1 minute before actual expiry to avoid race
	return time.Now().Add(time.Minute).After(t.ExpiresAt)
}

// HasRefreshToken returns true if a refresh token is available.
func (t *OAuthTokens) HasRefreshToken() bool {
	return t.RefreshToken != ""
}

// ClientRegistration represents a dynamically registered OAuth client.
type ClientRegistration struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret,omitempty"`
}

// PKCEParams holds PKCE challenge parameters.
type PKCEParams struct {
	Verifier     string
	Challenge    string
	ChallengeMethod string
}

// GeneratePKCE creates a new PKCE verifier and challenge (S256).
func GeneratePKCE() (*PKCEParams, error) {
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return nil, fmt.Errorf("failed to generate PKCE verifier: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)

	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])

	return &PKCEParams{
		Verifier:        verifier,
		Challenge:       challenge,
		ChallengeMethod: "S256",
	}, nil
}

// AuthState represents an in-progress OAuth authorization flow.
type AuthState struct {
	ServerID         string
	AuthURL          string
	PKCE             *PKCEParams
	RedirectURI      string
	ClientID         string
	ClientSecret     string
	TokenEndpoint    string
	AuthorizationEndpoint string
	Metadata         *OAuthServerMetadata
	CreatedAt        time.Time
}

// ProtectedResourceMetadata represents the OAuth 2.0 Protected Resource Metadata (RFC 9728).
type ProtectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	BearerMethodsSupported []string `json:"bearer_methods_supported,omitempty"`
	ScopesSupported      []string `json:"scopes_supported,omitempty"`
}

// authorizationBaseURL extracts the authorization base URL from an MCP server URL.
// Per spec: discard the path component. https://api.example.com/v1/mcp → https://api.example.com
func authorizationBaseURL(serverURL string) (string, error) {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return "", fmt.Errorf("invalid server URL: %w", err)
	}
	return fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host), nil
}

// DiscoverOAuthMetadata fetches the OAuth 2.0 Authorization Server Metadata.
// This implements the MCP spec's metadata discovery with fallbacks:
// 1. Try GET /.well-known/oauth-authorization-server (RFC 8414) at the base URL
// 2. Try GET /.well-known/oauth-protected-resource (RFC 9728) at the server URL and base URL
// 3. If 401 with WWW-Authenticate, follow resource_metadata to protected resource metadata
// 4. Follow authorization_servers to the actual OAuth server metadata
func DiscoverOAuthMetadata(serverURL string) (*OAuthServerMetadata, error) {
	baseURL, err := authorizationBaseURL(serverURL)
	if err != nil {
		return nil, err
	}

	// Step 1: Try standard RFC 8414 discovery at base URL
	metadataURL := baseURL + "/.well-known/oauth-authorization-server"
	req, err := http.NewRequest("GET", metadataURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("MCP-Protocol-Version", "2025-03-26")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err == nil && resp.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var metadata OAuthServerMetadata
		if err := json.Unmarshal(body, &metadata); err == nil && metadata.AuthorizationEndpoint != "" {
			return &metadata, nil
		}
	}
	if resp != nil {
		resp.Body.Close()
	}

	// Step 2: Try fetching .well-known/oauth-protected-resource directly
	// Some MCP servers expose this at their URL path (e.g. /v1/mcp/.well-known/...)
	// rather than returning it via WWW-Authenticate.
	for _, candidateURL := range []string{serverURL, baseURL} {
		prmURL := strings.TrimSuffix(candidateURL, "/") + "/.well-known/oauth-protected-resource"
		prm, err := fetchProtectedResourceMetadata(prmURL)
		if err == nil && prm != nil && len(prm.AuthorizationServers) > 0 {
			if metadata := followAuthServers(prm); metadata != nil {
				return metadata, nil
			}
		}
	}

	// Step 3: Check WWW-Authenticate on the MCP server itself
	metadata, err := discoverViaProtectedResource(serverURL)
	if err == nil && metadata != nil {
		return metadata, nil
	}

	// Step 4: Fallback to default endpoints
	return &OAuthServerMetadata{
		AuthorizationEndpoint: baseURL + "/authorize",
		TokenEndpoint:          baseURL + "/token",
		RegistrationEndpoint:   baseURL + "/register",
	}, nil
}

// fetchProtectedResourceMetadata fetches and parses RFC 9728 Protected Resource
// Metadata from the given URL.
func fetchProtectedResourceMetadata(metadataURL string) (*ProtectedResourceMetadata, error) {
	req, err := http.NewRequest("GET", metadataURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("MCP-Protocol-Version", "2025-03-26")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("resource metadata returned HTTP %d", resp.StatusCode)
	}

	var prm ProtectedResourceMetadata
	if err := json.NewDecoder(resp.Body).Decode(&prm); err != nil {
		return nil, fmt.Errorf("failed to parse resource metadata: %w", err)
	}

	return &prm, nil
}

// followAuthServers follows authorization_servers from Protected Resource
// Metadata to find OAuth server metadata. Tries oauth-authorization-server,
// then OIDC openid-configuration, then Entra ID default endpoints.
func followAuthServers(prm *ProtectedResourceMetadata) *OAuthServerMetadata {
	client := &http.Client{Timeout: 30 * time.Second}

	// Try oauth-authorization-server endpoint first
	for _, authServerURL := range prm.AuthorizationServers {
		oauthMetaURL := authServerURL + "/.well-known/oauth-authorization-server"
		oauthReq, err := http.NewRequest("GET", oauthMetaURL, nil)
		if err != nil {
			continue
		}
		oauthReq.Header.Set("Accept", "application/json")

		oauthResp, err := client.Do(oauthReq)
		if err != nil {
			continue
		}

		if oauthResp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(oauthResp.Body)
			oauthResp.Body.Close()

			var metadata OAuthServerMetadata
			if err := json.Unmarshal(body, &metadata); err == nil && metadata.AuthorizationEndpoint != "" {
				// Add scopes from resource metadata if not present
				if len(metadata.ScopesSupported) == 0 && len(prm.ScopesSupported) > 0 {
					metadata.ScopesSupported = prm.ScopesSupported
				}
				return &metadata
			}
		} else {
			oauthResp.Body.Close()
		}
	}

	// Try OIDC discovery (Entra ID uses openid-configuration)
	for _, authServerURL := range prm.AuthorizationServers {
		oidcURL := authServerURL + "/.well-known/openid-configuration"
		oidcReq, err := http.NewRequest("GET", oidcURL, nil)
		if err != nil {
			continue
		}
		oidcReq.Header.Set("Accept", "application/json")

		oidcResp, err := client.Do(oidcReq)
		if err != nil {
			continue
		}

		if oidcResp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(oidcResp.Body)
			oidcResp.Body.Close()

			var oidc struct {
				Issuer                string `json:"issuer"`
				AuthorizationEndpoint string `json:"authorization_endpoint"`
				TokenEndpoint         string `json:"token_endpoint"`
				RevocationEndpoint    string `json:"revocation_endpoint,omitempty"`
				ScopesSupported       []string `json:"scopes_supported,omitempty"`
			}
			if json.Unmarshal(body, &oidc) == nil && oidc.AuthorizationEndpoint != "" {
				return &OAuthServerMetadata{
					Issuer:               oidc.Issuer,
					AuthorizationEndpoint: oidc.AuthorizationEndpoint,
					TokenEndpoint:         oidc.TokenEndpoint,
					RevocationEndpoint:    oidc.RevocationEndpoint,
					ScopesSupported:       prm.ScopesSupported,
				}
			}
		} else {
			oidcResp.Body.Close()
		}
	}

	// If we found authorization servers but no metadata, use Entra ID default endpoints
	if len(prm.AuthorizationServers) > 0 {
		authServer := prm.AuthorizationServers[0]
		return &OAuthServerMetadata{
			AuthorizationEndpoint: authServer + "/oauth2/v2.0/authorize",
			TokenEndpoint:          authServer + "/oauth2/v2.0/token",
			ScopesSupported:        prm.ScopesSupported,
		}
	}

	return nil
}

// discoverViaProtectedResource uses the WWW-Authenticate header to discover
// OAuth metadata via the Protected Resource Metadata approach (RFC 9728).
func discoverViaProtectedResource(serverURL string) (*OAuthServerMetadata, error) {
	// Make a test MCP initialize request to trigger a 401
	reqBody := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"mcp-proxy","version":"1.0.0"}}}`)
	req, err := http.NewRequest("POST", serverURL, strings.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-03-26")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Check WWW-Authenticate header
	authHeader := resp.Header.Get("WWW-Authenticate")
	if authHeader == "" {
		return nil, fmt.Errorf("no WWW-Authenticate header")
	}

	// Parse resource_metadata URL from the header
	// Format: Bearer resource_metadata="https://..."
	resourceMetaURL := extractResourceMetadataURL(authHeader)
	if resourceMetaURL == "" {
		return nil, fmt.Errorf("no resource_metadata in WWW-Authenticate")
	}

	// Fetch the protected resource metadata from the URL in the header
	prm, err := fetchProtectedResourceMetadata(resourceMetaURL)
	if err != nil {
		return nil, err
	}

	if len(prm.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("no authorization_servers in resource metadata")
	}

	// Follow authorization_servers to find OAuth server metadata
	if metadata := followAuthServers(prm); metadata != nil {
		return metadata, nil
	}

	return nil, fmt.Errorf("no authorization servers found")
}

// extractResourceMetadataURL parses the WWW-Authenticate header to find
// the resource_metadata URL.
func extractResourceMetadataURL(header string) string {
	// Look for resource_metadata="..."
	idx := strings.Index(header, "resource_metadata=")
	if idx == -1 {
		return ""
	}
	start := idx + len("resource_metadata=")
	if start >= len(header) {
		return ""
	}
	// Skip opening quote
	if header[start] == '"' || header[start] == '\'' {
		start++
	}
	end := start
	for end < len(header) && header[end] != '"' && header[end] != '\'' && header[end] != ' ' {
		end++
	}
	return header[start:end]
}

// RegisterClient performs dynamic client registration (RFC7591).
func RegisterClient(registrationEndpoint string, redirectURIs []string) (*ClientRegistration, error) {
	body := map[string]interface{}{
		"redirect_uris": redirectURIs,
		"token_endpoint_auth_method": "none", // public client (PKCE)
		"grant_types": []string{"authorization_code", "refresh_token"},
		"response_types": []string{"code"},
	}
	bodyBytes, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", registrationEndpoint, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("client registration failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("registration failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var reg ClientRegistration
	if err := json.Unmarshal(respBody, &reg); err != nil {
		return nil, fmt.Errorf("failed to parse registration response: %w", err)
	}

	if reg.ClientID == "" {
		return nil, fmt.Errorf("registration returned no client_id")
	}

	return &reg, nil
}

// BuildAuthURL constructs the authorization URL for the OAuth flow.
func BuildAuthURL(metadata *OAuthServerMetadata, clientID, redirectURI string, pkce *PKCEParams, scopes []string, state string) (string, error) {
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {pkce.Challenge},
		"code_challenge_method": {pkce.ChallengeMethod},
		"state":                 {state},
	}
	if len(scopes) > 0 {
		params.Set("scope", strings.Join(scopes, " "))
	}

	return metadata.AuthorizationEndpoint + "?" + params.Encode(), nil
}

// ExchangeCodeForToken exchanges an authorization code for access and refresh tokens.
func ExchangeCodeForToken(tokenEndpoint, clientID, clientSecret, code, redirectURI, codeVerifier string) (*OAuthTokens, error) {
	params := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {codeVerifier},
	}
	if clientSecret != "" {
		params.Set("client_secret", clientSecret)
	}

	req, err := http.NewRequest("POST", tokenEndpoint, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		RefreshToken string `json:"refresh_token,omitempty"`
		ExpiresIn    int    `json:"expires_in,omitempty"`
		Scope        string `json:"scope,omitempty"`
	}
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	tokens := &OAuthTokens{
		AccessToken:  tokenResp.AccessToken,
		TokenType:    tokenResp.TokenType,
		RefreshToken: tokenResp.RefreshToken,
		Scope:        tokenResp.Scope,
	}
	if tokenResp.ExpiresIn > 0 {
		tokens.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}

	if tokens.AccessToken == "" {
		return nil, fmt.Errorf("token response missing access_token")
	}

	return tokens, nil
}

// RefreshToken exchanges a refresh token for a new access token.
func RefreshToken(tokenEndpoint, clientID, clientSecret, refreshToken string) (*OAuthTokens, error) {
	params := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID},
		"refresh_token": {refreshToken},
	}
	if clientSecret != "" {
		params.Set("client_secret", clientSecret)
	}

	req, err := http.NewRequest("POST", tokenEndpoint, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token refresh failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		RefreshToken string `json:"refresh_token,omitempty"`
		ExpiresIn    int    `json:"expires_in,omitempty"`
		Scope        string `json:"scope,omitempty"`
	}
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse refresh response: %w", err)
	}

	tokens := &OAuthTokens{
		AccessToken:  tokenResp.AccessToken,
		TokenType:    tokenResp.TokenType,
		RefreshToken: tokenResp.RefreshToken,
		Scope:        tokenResp.Scope,
	}
	if tokenResp.ExpiresIn > 0 {
		tokens.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}
	// If no new refresh token, keep the old one
	if tokens.RefreshToken == "" {
		tokens.RefreshToken = refreshToken
	}

	if tokens.AccessToken == "" {
		return nil, fmt.Errorf("refresh response missing access_token")
	}

	return tokens, nil
}

// Well-known public client IDs for identity providers that don't support
// dynamic client registration. These are first-party public clients that
// accept any Azure AD / Entra ID user with PKCE (no secret required).
const (
	// Azure CLI public client — works with the /organizations tenant for any Entra ID user.
	EntraIDPublicClientID = "04b07795-8ddb-461a-bbee-02f9e1bf7b46"
)

// IsEntraID checks if an authorization server URL belongs to Microsoft Entra ID.
func IsEntraID(authServerURL string) bool {
	return isEntraIDURL(authServerURL)
}

func isEntraIDURL(url string) bool {
	return strings.Contains(url, "login.microsoftonline.com") ||
		strings.Contains(url, "login.windows.net") ||
		strings.Contains(url, "login.microsoft.com")
}

// GenerateState generates a random OAuth state parameter.
func GenerateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// DeviceCodeResponse represents the response from the device authorization endpoint.
type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	Message         string `json:"message"`
}

// RequestDeviceCode initiates the device code flow by requesting a device code
// from the authorization server's device authorization endpoint.
func RequestDeviceCode(metadata *OAuthServerMetadata, clientID, scope string) (*DeviceCodeResponse, error) {
	// Construct the device authorization endpoint.
	// Entra ID: https://login.microsoftonline.com/{tenant}/oauth2/v2.0/devicecode
	// The issuer is typically https://login.microsoftonline.com/{tenant}/v2.0
	// so we need to insert /oauth2 before /v2.0/devicecode
	deviceEndpoint := ""

	if isEntraIDURL(metadata.Issuer) || isEntraIDURL(metadata.TokenEndpoint) {
		// Entra ID device code endpoint: {base}/{tenant}/oauth2/v2.0/devicecode
		// Derive from token endpoint which is {base}/{tenant}/oauth2/v2.0/token
		if metadata.TokenEndpoint != "" {
			// Replace /token with /devicecode
			deviceEndpoint = strings.Replace(metadata.TokenEndpoint, "/token", "/devicecode", 1)
		}
		if deviceEndpoint == "" && metadata.Issuer != "" {
			// Issuer is like https://login.microsoftonline.com/{tenant}/v2.0
			// Need: https://login.microsoftonline.com/{tenant}/oauth2/v2.0/devicecode
			deviceEndpoint = strings.Replace(metadata.Issuer, "/v2.0", "/oauth2/v2.0/devicecode", 1)
		}
	} else if metadata.Issuer != "" {
		// Generic OAuth: try {issuer}/devicecode
		deviceEndpoint = strings.TrimSuffix(metadata.Issuer, "/") + "/devicecode"
	} else if metadata.TokenEndpoint != "" {
		// Derive from token endpoint: replace /token with /devicecode
		idx := strings.LastIndex(metadata.TokenEndpoint, "/")
		if idx > 0 {
			base := metadata.TokenEndpoint[:idx]
			deviceEndpoint = base + "/devicecode"
		}
	}
	if deviceEndpoint == "" {
		return nil, fmt.Errorf("cannot determine device authorization endpoint")
	}

	params := url.Values{
		"client_id": {clientID},
	}
	if scope != "" {
		params.Set("scope", scope)
	}

	req, err := http.NewRequest("POST", deviceEndpoint, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("device code request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device code request failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var dcResp DeviceCodeResponse
	if err := json.Unmarshal(respBody, &dcResp); err != nil {
		return nil, fmt.Errorf("failed to parse device code response: %w", err)
	}
	if dcResp.DeviceCode == "" || dcResp.UserCode == "" {
		return nil, fmt.Errorf("device code response missing required fields")
	}
	if dcResp.Interval == 0 {
		dcResp.Interval = 5
	}

	return &dcResp, nil
}

// PollDeviceToken polls the token endpoint for a device code flow.
// Returns tokens when the user completes authentication, or an error if expired/declined.
func PollDeviceToken(tokenEndpoint, clientID, deviceCode string) (*OAuthTokens, error) {
	params := url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"client_id":   {clientID},
		"device_code": {deviceCode},
	}

	req, err := http.NewRequest("POST", tokenEndpoint, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token poll failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		// Parse error to check if it's "authorization_pending"
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(respBody, &errResp) == nil {
			if errResp.Error == "authorization_pending" || errResp.Error == "slow_down" {
				return nil, ErrAuthorizationPending
			}
		}
		return nil, fmt.Errorf("token poll failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		RefreshToken string `json:"refresh_token,omitempty"`
		ExpiresIn    int    `json:"expires_in,omitempty"`
		Scope        string `json:"scope,omitempty"`
	}
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	tokens := &OAuthTokens{
		AccessToken:  tokenResp.AccessToken,
		TokenType:    tokenResp.TokenType,
		RefreshToken: tokenResp.RefreshToken,
		Scope:        tokenResp.Scope,
	}
	if tokenResp.ExpiresIn > 0 {
		tokens.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}

	if tokens.AccessToken == "" {
		return nil, fmt.Errorf("token response missing access_token")
	}

	return tokens, nil
}

// ErrAuthorizationPending indicates the user has not yet completed device authentication.
var ErrAuthorizationPending = fmt.Errorf("authorization pending")
