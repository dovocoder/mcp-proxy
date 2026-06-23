package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/agentic/mcp-proxy/internal/mcp"
	"github.com/agentic/mcp-proxy/internal/models"
	"github.com/agentic/mcp-proxy/internal/store"
	"github.com/google/uuid"
)

// Manager manages all backend MCP server connections.
type Manager struct {
	store      *store.Store
	mu         sync.RWMutex
	clients    map[string]*mcp.Client // serverID -> client
	errors     map[string]string     // serverID -> last error message
	authStates map[string]*mcp.AuthState // serverID -> pending OAuth flow
}

// New creates a new proxy Manager.
func New(s *store.Store) *Manager {
	return &Manager{
		store:      s,
		clients:    make(map[string]*mcp.Client),
		errors:     make(map[string]string),
		authStates: make(map[string]*mcp.AuthState),
	}
}

// StartAll connects to all enabled servers.
func (m *Manager) StartAll() {
	servers, err := m.store.ListServers()
	if err != nil {
		log.Printf("Failed to list servers: %v", err)
		return
	}

	for _, srv := range servers {
		if !srv.Enabled {
			continue
		}
		go m.connectServer(srv)
	}
}

// connectServer establishes a connection to a backend server.
func (m *Manager) connectServer(srv *models.Server) {
	// For HTTP transports, check if we have stored OAuth tokens
	authToken := srv.AuthToken

	if srv.Transport == "http" || srv.Transport == "streamable-http" {
		tokens, cid, csec, err := m.store.GetOAuthTokens(srv.ID)
		if err == nil && tokens != nil {
			// Check if token needs refresh
			if tokens.IsExpired() && tokens.HasRefreshToken() {
				// Need metadata for refresh endpoint
				meta, _ := mcp.DiscoverOAuthMetadata(srv.URL)
				if meta != nil && meta.TokenEndpoint != "" {
					refreshed, err := mcp.RefreshToken(meta.TokenEndpoint, cid, csec, tokens.RefreshToken)
					if err == nil {
						tokens = refreshed
						_ = m.store.SaveOAuthTokens(srv.ID, tokens, cid, csec)
						log.Printf("Refreshed OAuth token for server %s", srv.Name)
					} else {
						log.Printf("Failed to refresh OAuth token for %s: %v", srv.Name, err)
					}
				}
			}
			authToken = tokens.AccessToken
			}
	}

	cfg := mcp.ClientConfig{
		Transport:      srv.Transport,
		Command:        srv.Command,
		Args:           srv.Args,
		Env:            srv.Env,
		URL:            srv.URL,
		Headers:        srv.Headers,
		AuthToken:      authToken,
		Timeout:        srv.Timeout,
		ConnectTimeout: srv.ConnectTimeout,
	}

	client := mcp.NewClient(cfg)
	if err := client.Connect(); err != nil {
		log.Printf("Failed to connect to server %s: %v", srv.Name, err)
		m.mu.Lock()
		m.errors[srv.ID] = err.Error()
		m.mu.Unlock()
		_ = m.store.UpdateServerStatus(srv.ID, "error")
		return
	}

	m.mu.Lock()
	m.clients[srv.ID] = client
	delete(m.errors, srv.ID)
	m.mu.Unlock()

	_ = m.store.UpdateServerStatus(srv.ID, "connected")
	log.Printf("Connected to MCP server: %s (%d tools)", srv.Name, len(client.Tools()))
}

// AddServer creates and connects to a new server.
func (m *Manager) AddServer(req *models.CreateServerRequest) (*models.Server, error) {
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	timeout := req.Timeout
	if timeout == 0 {
		timeout = 120
	}
	connTimeout := req.ConnectTimeout
	if connTimeout == 0 {
		connTimeout = 60
	}

	srv := &models.Server{
		ID:             uuid.NewString(),
		Name:           req.Name,
		Transport:      req.Transport,
		Command:        req.Command,
		Args:           req.Args,
		URL:            req.URL,
		Headers:        req.Headers,
		Env:            req.Env,
		AuthToken:      req.AuthToken,
		Timeout:        timeout,
		ConnectTimeout: connTimeout,
		Enabled:        enabled,
		Status:         "disconnected",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if err := m.store.CreateServer(srv); err != nil {
		return nil, fmt.Errorf("failed to create server: %w", err)
	}

	if enabled {
		go m.connectServer(srv)
	}

	return srv, nil
}

// UpdateServer updates a server's configuration and reconnects.
func (m *Manager) UpdateServer(id string, req *models.UpdateServerRequest) (*models.Server, error) {
	srv, err := m.store.GetServer(id)
	if err != nil {
		return nil, fmt.Errorf("server not found: %w", err)
	}

	if req.Name != nil {
		srv.Name = *req.Name
	}
	if req.Transport != nil {
		srv.Transport = *req.Transport
	}
	if req.Command != nil {
		srv.Command = *req.Command
	}
	if req.Args != nil {
		srv.Args = *req.Args
	}
	if req.URL != nil {
		srv.URL = *req.URL
	}
	if req.Headers != nil {
		srv.Headers = *req.Headers
	}
	if req.Env != nil {
		srv.Env = *req.Env
	}
	if req.AuthToken != nil {
		srv.AuthToken = *req.AuthToken
	}
	if req.Timeout != nil {
		srv.Timeout = *req.Timeout
	}
	if req.ConnectTimeout != nil {
		srv.ConnectTimeout = *req.ConnectTimeout
	}
	if req.Enabled != nil {
		srv.Enabled = *req.Enabled
	}
	srv.UpdatedAt = time.Now()

	if err := m.store.UpdateServer(srv); err != nil {
		return nil, fmt.Errorf("failed to update server: %w", err)
	}

	// Reconnect
	m.DisconnectServer(id)
	if srv.Enabled {
		go m.connectServer(srv)
	}

	return srv, nil
}

// DeleteServer removes a server and disconnects it.
func (m *Manager) DeleteServer(id string) error {
	m.DisconnectServer(id)
	return m.store.DeleteServer(id)
}

// DisconnectServer closes the connection to a server.
func (m *Manager) DisconnectServer(id string) {
	m.mu.Lock()
	client, ok := m.clients[id]
	if ok {
		delete(m.clients, id)
	}
	delete(m.errors, id)
	m.mu.Unlock()

	if ok {
		client.Disconnect()
	}
	_ = m.store.UpdateServerStatus(id, "disconnected")
}

// ReconnectServer disconnects and reconnects a server.
func (m *Manager) ReconnectServer(id string) error {
	srv, err := m.store.GetServer(id)
	if err != nil {
		return fmt.Errorf("server not found: %w", err)
	}

	m.DisconnectServer(id)
	go m.connectServer(srv)
	return nil
}

// GetServerStatus returns the live status of a server.
func (m *Manager) GetServerStatus(id string) (status string, toolCount int, lastErr string) {
	m.mu.RLock()
	client, ok := m.clients[id]
	lastErr = m.errors[id]
	m.mu.RUnlock()

	if !ok {
		srv, err := m.store.GetServer(id)
		if err != nil {
			return "disconnected", 0, lastErr
		}
		return srv.Status, 0, lastErr
	}

	status, _ = client.Status()
	toolCount = len(client.Tools())
	return
}

// ListTools returns all tools from all connected servers.
func (m *Manager) ListTools() []models.Tool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var tools []models.Tool
	for id, client := range m.clients {
		srv, err := m.store.GetServer(id)
		if err != nil {
			continue
		}
		for _, t := range client.Tools() {
			tool := models.Tool{
				ServerID:    id,
				ServerName:  srv.Name,
				Name:        t.Name,
				Description: t.Description,
			}
			if len(t.InputSchema) > 0 {
				json.Unmarshal(t.InputSchema, &tool.InputSchema)
			}
			tools = append(tools, tool)
		}
	}
	return tools
}

// HandleJSONRPC processes a JSON-RPC request from a client, routing to the appropriate backend.
func (m *Manager) HandleJSONRPC(ctx context.Context, req mcp.JSONRPCRequest) (json.RawMessage, error) {
	switch req.Method {
	case "initialize":
		return m.handleInitialize(req)
	case "tools/list":
		return m.handleToolsList(req)
	case "tools/call":
		return m.handleToolsCall(ctx, req)
	default:
		return nil, fmt.Errorf("unsupported method: %s", req.Method)
	}
}

func (m *Manager) handleInitialize(req mcp.JSONRPCRequest) (json.RawMessage, error) {
	result := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{},
		},
		"serverInfo": map[string]interface{}{
			"name":    "mcp-proxy",
			"version": "1.0.0",
		},
	}
	return json.Marshal(result)
}

func (m *Manager) handleToolsList(req mcp.JSONRPCRequest) (json.RawMessage, error) {
	allTools := m.ListTools()
	var mcpTools []mcp.Tool
	for _, t := range allTools {
		// Prefix tool name with server name to avoid collisions
		namespacedName := fmt.Sprintf("%s__%s", t.ServerName, t.Name)
		tool := mcp.Tool{
			Name:        namespacedName,
			Description: fmt.Sprintf("[%s] %s", t.ServerName, t.Description),
		}
		if t.InputSchema != nil {
			schema, _ := json.Marshal(t.InputSchema)
			tool.InputSchema = schema
		}
		mcpTools = append(mcpTools, tool)
	}
	if mcpTools == nil {
		mcpTools = []mcp.Tool{}
	}
	result := mcp.ToolListResult{Tools: mcpTools}
	return json.Marshal(result)
}

func (m *Manager) handleToolsCall(ctx context.Context, req mcp.JSONRPCRequest) (json.RawMessage, error) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, fmt.Errorf("invalid tools/call params: %w", err)
	}

	// Parse namespaced tool name: "serverName__toolName"
	serverName, toolName, err := parseNamespacedTool(params.Name)
	if err != nil {
		return nil, err
	}

	// Find the server
	srv, err := m.store.GetServerByName(serverName)
	if err != nil {
		return nil, fmt.Errorf("server not found: %s", serverName)
	}

	m.mu.RLock()
	client, ok := m.clients[srv.ID]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("server not connected: %s", serverName)
	}

	return client.CallTool(toolName, params.Arguments)
}

// parseNamespacedTool splits "serverName__toolName" into parts.
func parseNamespacedTool(namespaced string) (string, string, error) {
	for i := len(namespaced) - 1; i >= 0; i-- {
		if namespaced[i] == '_' && i > 0 && namespaced[i-1] == '_' {
			return namespaced[:i-1], namespaced[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("invalid tool name format, expected 'serverName__toolName'")
}

// InitiateAuth starts the OAuth flow for a server. Returns the authorization URL
// the user should open in their browser.
func (m *Manager) InitiateAuth(serverID string, callbackBaseURL string) (string, error) {
	srv, err := m.store.GetServer(serverID)
	if err != nil {
		return "", fmt.Errorf("server not found: %w", err)
	}

	if srv.Transport != "http" && srv.Transport != "streamable-http" {
		return "", fmt.Errorf("OAuth only supported for http/streamable-http transports")
	}

	// Discover OAuth metadata
	metadata, err := mcp.DiscoverOAuthMetadata(srv.URL)
	if err != nil {
		return "", fmt.Errorf("failed to discover OAuth metadata: %w", err)
	}

	redirectURI := fmt.Sprintf("%s/api/oauth/callback", callbackBaseURL)
	var clientID, clientSecret string

	// Try dynamic client registration if endpoint exists
	if metadata.RegistrationEndpoint != "" {
		reg, err := mcp.RegisterClient(metadata.RegistrationEndpoint, []string{redirectURI})
		if err == nil {
			clientID = reg.ClientID
			clientSecret = reg.ClientSecret
			log.Printf("Dynamically registered OAuth client for server %s: %s", srv.Name, clientID)
		} else {
			log.Printf("Dynamic registration failed for %s: %v", srv.Name, err)
		}
	}

	// If no client ID from registration, check if the server config has an auth_token
	// that could be a pre-configured client ID (stored in auth_token field)
	if clientID == "" && srv.AuthToken != "" {
		// Use the configured auth_token as a static client ID
		clientID = srv.AuthToken
		log.Printf("Using configured client ID for server %s", srv.Name)
	}

	if clientID == "" {
		return "", fmt.Errorf("no client ID available — dynamic registration not supported by the authorization server. Configure a client_id in the server's Auth Token field, or register an OAuth app in your identity provider (e.g., Microsoft Entra ID) and enter the client ID")
	}

	// Generate PKCE
	pkce, err := mcp.GeneratePKCE()
	if err != nil {
		return "", fmt.Errorf("failed to generate PKCE: %w", err)
	}

	// Generate state
	state, err := mcp.GenerateState()
	if err != nil {
		return "", fmt.Errorf("failed to generate state: %w", err)
	}

	// Build authorization URL
	scopes := metadata.ScopesSupported
	authURL, err := mcp.BuildAuthURL(metadata, clientID, redirectURI, pkce, scopes, state)
	if err != nil {
		return "", fmt.Errorf("failed to build auth URL: %w", err)
	}

	// Store auth state
	authState := &mcp.AuthState{
		ServerID:              serverID,
		AuthURL:               authURL,
		PKCE:                  pkce,
		RedirectURI:           redirectURI,
		ClientID:              clientID,
		ClientSecret:          clientSecret,
		TokenEndpoint:         metadata.TokenEndpoint,
		AuthorizationEndpoint: metadata.AuthorizationEndpoint,
		Metadata:              metadata,
		CreatedAt:             time.Now(),
	}

	m.mu.Lock()
	m.authStates[state] = authState
	m.mu.Unlock()

	return authURL, nil
}

// HandleAuthCallback processes the OAuth callback, exchanges the code for tokens,
// stores them, and reconnects the server.
func (m *Manager) HandleAuthCallback(state, code string) error {
	m.mu.Lock()
	authState, ok := m.authStates[state]
	if ok {
		delete(m.authStates, state)
	}
	m.mu.Unlock()

	if !ok {
		return fmt.Errorf("invalid or expired OAuth state")
	}

	// Exchange code for tokens
	tokens, err := mcp.ExchangeCodeForToken(
		authState.TokenEndpoint,
		authState.ClientID,
		authState.ClientSecret,
		code,
		authState.RedirectURI,
		authState.PKCE.Verifier,
	)
	if err != nil {
		return fmt.Errorf("token exchange failed: %w", err)
	}

	// Store tokens
	if err := m.store.SaveOAuthTokens(authState.ServerID, tokens, authState.ClientID, authState.ClientSecret); err != nil {
		return fmt.Errorf("failed to save tokens: %w", err)
	}

	log.Printf("OAuth tokens stored for server %s", authState.ServerID)

	// Reconnect the server with the new token
	srv, err := m.store.GetServer(authState.ServerID)
	if err != nil {
		return fmt.Errorf("server not found after auth: %w", err)
	}

	m.DisconnectServer(authState.ServerID)
	go m.connectServer(srv)

	return nil
}

// GetAuthStatus returns the OAuth status for a server.
func (m *Manager) GetAuthStatus(serverID string) (hasTokens bool, expired bool) {
	tokens, _, _, err := m.store.GetOAuthTokens(serverID)
	if err != nil || tokens == nil {
		return false, false
	}
	return true, tokens.IsExpired()
}

// StopAll disconnects all servers.
func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, client := range m.clients {
		client.Disconnect()
		delete(m.clients, id)
	}
}
