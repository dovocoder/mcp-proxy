package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/agentic/mcp-proxy/internal/mcp"
	"github.com/agentic/mcp-proxy/internal/memory"
	"github.com/agentic/mcp-proxy/internal/models"
	"github.com/agentic/mcp-proxy/internal/store"
	"github.com/google/uuid"
)

// LogEntry is a single stderr log line with timestamp.
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Line      string    `json:"line"`
}

// serverLog is a per-server ring buffer of stderr log lines.
type serverLog struct {
	mu     sync.Mutex
	lines  []LogEntry
	maxLen int
}

func newServerLog(maxLen int) *serverLog {
	return &serverLog{maxLen: maxLen}
}

func (sl *serverLog) add(line string) {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	sl.lines = append(sl.lines, LogEntry{
		Timestamp: time.Now(),
		Line:      line,
	})
	if len(sl.lines) > sl.maxLen {
		sl.lines = sl.lines[len(sl.lines)-sl.maxLen:]
	}
}

func (sl *serverLog) get() []LogEntry {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	out := make([]LogEntry, len(sl.lines))
	copy(out, sl.lines)
	return out
}

func (sl *serverLog) clear() {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	sl.lines = nil
}

// Manager manages all backend MCP server connections.
type Manager struct {
	store       *store.Store
	memory      *memory.Server
	mu          sync.RWMutex
	clients     map[string]*mcp.Client         // serverID -> client
	errors      map[string]string              // serverID -> last error message
	authStates  map[string]*mcp.AuthState     // state -> pending OAuth flow
	deviceAuths map[string]*DeviceAuthResult   // serverID -> pending device code flow
	logMu       sync.RWMutex
	serverLogs  map[string]*serverLog          // serverID -> stderr log ring buffer
}

// New creates a new proxy Manager.
func New(s *store.Store) *Manager {
	return &Manager{
		store:       s,
		memory:      memory.New(s),
		clients:     make(map[string]*mcp.Client),
		errors:      make(map[string]string),
		authStates:  make(map[string]*mcp.AuthState),
		deviceAuths: make(map[string]*DeviceAuthResult),
		serverLogs:  make(map[string]*serverLog),
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
		OnStderr: func(line string) {
			m.addServerLog(srv.ID, line)
		},
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

// Scope defines what servers a request can access.
// If ServerID is set, only that server is exposed.
// If CompoundID is set, only compound member servers are exposed.
// If neither is set, all servers are exposed (global).
type Scope struct {
	ServerID   string
	CompoundID string
}

// ListTools returns all tools from all connected servers plus built-in memory tools.
func (m *Manager) ListTools() []models.Tool {
	tools := m.listToolsFiltered(nil)
	// Add memory tools as virtual "memory" server tools
	for _, mt := range m.memory.Tools() {
		tools = append(tools, models.Tool{
			ServerID:    "builtin-memory",
			ServerName:  "memory",
			Name:        mt.Name,
			Description: mt.Description,
		})
	}
	return tools
}

// Memory returns the built-in memory server instance.
func (m *Manager) Memory() *memory.Server {
	return m.memory
}

// ListToolsForServer returns tools from a single server.
func (m *Manager) ListToolsForServer(serverID string) []models.Tool {
	return m.listToolsFiltered(map[string]bool{serverID: true})
}

// ListToolsForCompound returns tools only from servers that are members of the compound.
func (m *Manager) ListToolsForCompound(compoundID string) []models.Tool {
	memberIDs, err := m.store.GetCompoundMemberIDs(compoundID)
	if err != nil || len(memberIDs) == 0 {
		return []models.Tool{}
	}
	memberSet := make(map[string]bool, len(memberIDs))
	for _, id := range memberIDs {
		memberSet[id] = true
	}
	return m.listToolsFiltered(memberSet)
}

// listToolsFiltered returns tools from connected servers. If filter is non-nil,
// only servers whose ID is in the filter set are included.
func (m *Manager) listToolsFiltered(filter map[string]bool) []models.Tool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var tools []models.Tool
	for id, client := range m.clients {
		if filter != nil && !filter[id] {
			continue
		}
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
// The scope determines which servers are accessible.
func (m *Manager) HandleJSONRPC(ctx context.Context, req mcp.JSONRPCRequest, scope Scope) (json.RawMessage, error) {
	switch req.Method {
	case "initialize":
		return m.handleInitialize(req)
	case "tools/list":
		return m.handleToolsList(req, scope)
	case "tools/call":
		return m.handleToolsCall(ctx, req, scope)
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

func (m *Manager) handleToolsList(req mcp.JSONRPCRequest, scope Scope) (json.RawMessage, error) {
	var allTools []models.Tool
	if scope.ServerID != "" {
		allTools = m.ListToolsForServer(scope.ServerID)
	} else if scope.CompoundID != "" {
		// Check if compound has dictionary mode enabled
		compound, err := m.store.GetCompound(scope.CompoundID)
		if err == nil && compound != nil && compound.DictionaryMode {
			// Dictionary mode: return the dictionary tool + memory tools (if memory is a member)
			tools := []mcp.Tool{{
				Name:        "dictionary",
				Description: dictionaryDescription,
				InputSchema: dictionarySchema,
			}}
			if m.isMemoryCompoundMember(scope.CompoundID) {
				for _, mt := range m.memory.Tools() {
					tools = append(tools, mcp.Tool{
						Name:        memory.NamespacedName(mt.Name),
						Description: fmt.Sprintf("[memory] %s", mt.Description),
					})
					if len(mt.InputSchema) > 0 {
						tools[len(tools)-1].InputSchema = mt.InputSchema
					}
				}
			}
			result := mcp.ToolListResult{Tools: tools}
			return json.Marshal(result)
		}
		allTools = m.ListToolsForCompound(scope.CompoundID)
	} else {
		allTools = m.listToolsFiltered(nil) // not ListTools() — that would double-add memory tools
	}
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

	// Add built-in memory tools
	// For global and server scope: always available
	// For compound scope: only if the memory server is a compound member
	addMemory := true
	if scope.CompoundID != "" {
		addMemory = m.isMemoryCompoundMember(scope.CompoundID)
	}
	if addMemory {
		for _, mt := range m.memory.Tools() {
			tool := mcp.Tool{
				Name:        memory.NamespacedName(mt.Name),
				Description: fmt.Sprintf("[memory] %s", mt.Description),
			}
			if len(mt.InputSchema) > 0 {
				tool.InputSchema = mt.InputSchema
			}
			mcpTools = append(mcpTools, tool)
		}
	}

	if mcpTools == nil {
		mcpTools = []mcp.Tool{}
	}
	result := mcp.ToolListResult{Tools: mcpTools}
	return json.Marshal(result)
}

func (m *Manager) handleToolsCall(ctx context.Context, req mcp.JSONRPCRequest, scope Scope) (json.RawMessage, error) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, fmt.Errorf("invalid tools/call params: %w", err)
	}

	// Check if this is a built-in memory tool
	// For compound scope, memory tools only work if memory is a compound member
	if baseName, ok := memory.ParseNamespaced(params.Name); ok {
		if scope.CompoundID != "" && !m.isMemoryCompoundMember(scope.CompoundID) {
			return nil, fmt.Errorf("memory tools are not available in this compound — add the memory server as a member")
		}
		return m.memory.HandleToolCall(baseName, params.Arguments)
	}

	// Check if this is a dictionary tool call (compound dictionary mode)
	if params.Name == "dictionary" && scope.CompoundID != "" {
		compound, err := m.store.GetCompound(scope.CompoundID)
		if err == nil && compound != nil && compound.DictionaryMode {
			return m.handleDictionaryCall(ctx, params.Arguments, scope)
		}
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

	// If scoped to a single server, verify it matches
	if scope.ServerID != "" {
		if srv.ID != scope.ServerID {
			return nil, fmt.Errorf("tool '%s' is not available in this server scope", params.Name)
		}
	}

	// If compound-scoped, verify the server is a member
	if scope.CompoundID != "" {
		memberIDs, err := m.store.GetCompoundMemberIDs(scope.CompoundID)
		if err != nil {
			return nil, fmt.Errorf("failed to verify compound membership: %w", err)
		}
		allowed := false
		for _, mid := range memberIDs {
			if mid == srv.ID {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("tool '%s' is not available in this compound", params.Name)
		}
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

	// If still no client ID, check if the authorization server is Entra ID.
	// Entra ID doesn't support dynamic registration, but we can use a well-known
	// public client (Azure CLI's client_id) with PKCE — no app registration needed.
	if clientID == "" && (mcp.IsEntraID(metadata.Issuer) || mcp.IsEntraID(metadata.AuthorizationEndpoint) || mcp.IsEntraID(metadata.TokenEndpoint)) {
		clientID = mcp.EntraIDPublicClientID
		log.Printf("Using Entra ID public client for server %s (no app registration required)", srv.Name)
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

// DeviceAuthResult holds the result of initiating a device code flow.
type DeviceAuthResult struct {
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	Message         string `json:"message"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	DeviceCode      string `json:"-"`
	ServerID        string `json:"-"`
	ClientID        string `json:"-"`
	TokenEndpoint   string `json:"-"`
}

// InitiateDeviceAuth starts a device code flow for a server. This is the preferred
// method for Entra ID because it doesn't require a redirect URI — works from any deployment.
func (m *Manager) InitiateDeviceAuth(serverID string) (*DeviceAuthResult, error) {
	srv, err := m.store.GetServer(serverID)
	if err != nil {
		return nil, fmt.Errorf("server not found: %w", err)
	}

	if srv.Transport != "http" && srv.Transport != "streamable-http" {
		return nil, fmt.Errorf("OAuth only supported for http/streamable-http transports")
	}

	// Discover OAuth metadata
	metadata, err := mcp.DiscoverOAuthMetadata(srv.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to discover OAuth metadata: %w", err)
	}

	// Determine client ID: configured > Entra ID public client
	clientID := srv.AuthToken
	if clientID == "" && (mcp.IsEntraID(metadata.Issuer) || mcp.IsEntraID(metadata.AuthorizationEndpoint) || mcp.IsEntraID(metadata.TokenEndpoint)) {
		clientID = mcp.EntraIDPublicClientID
		log.Printf("Using Entra ID public client for device code flow (server %s)", srv.Name)
	}
	if clientID == "" {
		return nil, fmt.Errorf("no client ID available — configure a client_id in the server's Auth Token field")
	}

	// Build scope from metadata
	scope := ""
	if len(metadata.ScopesSupported) > 0 {
		scope = strings.Join(metadata.ScopesSupported, " ")
	}

	// Request device code
	dcResp, err := mcp.RequestDeviceCode(metadata, clientID, scope)
	if err != nil {
		return nil, fmt.Errorf("device code request failed: %w", err)
	}

	result := &DeviceAuthResult{
		UserCode:        dcResp.UserCode,
		VerificationURI: dcResp.VerificationURI,
		Message:         dcResp.Message,
		ExpiresIn:       dcResp.ExpiresIn,
		Interval:        dcResp.Interval,
		DeviceCode:      dcResp.DeviceCode,
		ServerID:        serverID,
		ClientID:        clientID,
		TokenEndpoint:   metadata.TokenEndpoint,
	}

	// Store for polling
	m.mu.Lock()
	m.deviceAuths[serverID] = result
	m.mu.Unlock()

	return result, nil
}

// PollDeviceAuth polls the token endpoint for a pending device code flow.
// Returns nil if authentication is still pending, or tokens if completed.
func (m *Manager) PollDeviceAuth(serverID string) error {
	m.mu.Lock()
	auth, ok := m.deviceAuths[serverID]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("no pending device auth for server %s", serverID)
	}

	tokens, err := mcp.PollDeviceToken(auth.TokenEndpoint, auth.ClientID, auth.DeviceCode)
	if err != nil {
		if errors.Is(err, mcp.ErrAuthorizationPending) {
			return nil // Still pending — frontend should keep polling
		}
		return fmt.Errorf("device auth poll failed: %w", err)
	}

	// Success — store tokens and clean up
	if err := m.store.SaveOAuthTokens(serverID, tokens, auth.ClientID, ""); err != nil {
		return fmt.Errorf("failed to save tokens: %w", err)
	}

	m.mu.Lock()
	delete(m.deviceAuths, serverID)
	m.mu.Unlock()

	log.Printf("OAuth tokens stored via device code flow for server %s", serverID)

	// Reconnect the server with the new token
	srv, err := m.store.GetServer(serverID)
	if err == nil {
		m.DisconnectServer(serverID)
		go m.connectServer(srv)
	}

	return nil
}

// CancelDeviceAuth removes a pending device auth flow.
func (m *Manager) CancelDeviceAuth(serverID string) {
	m.mu.Lock()
	delete(m.deviceAuths, serverID)
	m.mu.Unlock()
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

// --- Server Logs (stdio debug) ---

const maxLogLines = 500

// addServerLog appends a stderr line to the server's log buffer.
func (m *Manager) addServerLog(serverID, line string) {
	m.logMu.RLock()
	sl, ok := m.serverLogs[serverID]
	m.logMu.RUnlock()

	if !ok {
		sl = newServerLog(maxLogLines)
		m.logMu.Lock()
		m.serverLogs[serverID] = sl
		m.logMu.Unlock()
	}

	sl.add(line)
}

// GetServerLogs returns recent stderr log lines for a server.
func (m *Manager) GetServerLogs(serverID string) []LogEntry {
	m.logMu.RLock()
	sl, ok := m.serverLogs[serverID]
	m.logMu.RUnlock()

	if !ok {
		return []LogEntry{}
	}
	return sl.get()
}

// ClearServerLogs clears the log buffer for a server.
func (m *Manager) ClearServerLogs(serverID string) {
	m.logMu.RLock()
	sl, ok := m.serverLogs[serverID]
	m.logMu.RUnlock()

	if ok {
		sl.clear()
	}
}

// --- Compound Dictionary Mode ---

// isMemoryCompoundMember returns true if the built-in memory server is a member
// of the specified compound.
func (m *Manager) isMemoryCompoundMember(compoundID string) bool {
	memberIDs, err := m.store.GetCompoundMemberIDs(compoundID)
	if err != nil {
		return false
	}
	for _, mid := range memberIDs {
		if mid == models.BuiltinMemoryServerID {
			return true
		}
	}
	return false
}

const dictionaryDescription = `Dictionary tool for compound servers. Instead of receiving all tools upfront, use this tool to discover, inspect, and call tools from member servers lazily.

Actions (pass as "action" parameter):

— list: List all available tools with names and descriptions (no schemas). Returns a lightweight catalog.
  No additional parameters needed.

— describe: Get the full input schema for a specific tool.
  Required: tool (the tool name from list).

— call: Execute a specific tool.
  Required: tool (the tool name), arguments (JSON object of tool parameters).

— search: Search tool names and descriptions by keyword.
  Required: query (search term).

Tool names are in "serverName__toolName" format. Use list or search to discover them, describe to inspect schemas, and call to execute.`

var dictionarySchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"action": {
			"type": "string",
			"enum": ["list", "describe", "call", "search"],
			"description": "The dictionary operation to perform"
		},
		"tool": {
			"type": "string",
			"description": "Tool name (for actions: describe, call). Use list/search to discover."
		},
		"arguments": {
			"type": "object",
			"description": "Tool arguments as a JSON object (for action: call)",
			"additionalProperties": true
		},
		"query": {
			"type": "string",
			"description": "Search query (for action: search)"
		}
	},
	"required": ["action"]
}`)

// handleDictionaryCall processes a dictionary tool call for a compound server.
func (m *Manager) handleDictionaryCall(ctx context.Context, args json.RawMessage, scope Scope) (json.RawMessage, error) {
	var params struct {
		Action    string          `json:"action"`
		Tool      string          `json:"tool,omitempty"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
		Query     string          `json:"query,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid dictionary arguments: %w", err)
	}

	allTools := m.ListToolsForCompound(scope.CompoundID)

	switch params.Action {
	case "list":
		// Return lightweight catalog: name + description only
		var catalog []map[string]string
		for _, t := range allTools {
			name := fmt.Sprintf("%s__%s", t.ServerName, t.Name)
			catalog = append(catalog, map[string]string{
				"name":        name,
				"server":      t.ServerName,
				"description": t.Description,
			})
		}
		// Include memory tools only if memory is a compound member
		if m.isMemoryCompoundMember(scope.CompoundID) {
			for _, mt := range m.memory.Tools() {
				catalog = append(catalog, map[string]string{
					"name":        memory.NamespacedName(mt.Name),
					"server":      "memory",
					"description": mt.Description,
				})
			}
		}
		return wrapMCPContent(map[string]interface{}{
			"tools": catalog,
			"count": len(catalog),
		})

	case "describe":
		if params.Tool == "" {
			return nil, fmt.Errorf("tool parameter is required for describe action")
		}
		// Check memory tools first (only if memory is a compound member)
		if m.isMemoryCompoundMember(scope.CompoundID) {
			if baseName, ok := memory.ParseNamespaced(params.Tool); ok {
				for _, mt := range m.memory.Tools() {
					if mt.Name == baseName {
						return wrapMCPContent(map[string]interface{}{
							"name":         params.Tool,
							"server":      "memory",
							"description": mt.Description,
							"inputSchema": mt.InputSchema,
						})
					}
				}
			}
		}
		// Find among member server tools
		for _, t := range allTools {
			name := fmt.Sprintf("%s__%s", t.ServerName, t.Name)
			if name == params.Tool {
				return wrapMCPContent(map[string]interface{}{
					"name":         name,
					"server":      t.ServerName,
					"description": t.Description,
					"inputSchema": t.InputSchema,
				})
			}
		}
		return nil, fmt.Errorf("tool not found: %s", params.Tool)

	case "call":
		if params.Tool == "" {
			return nil, fmt.Errorf("tool parameter is required for call action")
		}
		// Check if it's a memory tool — route to memory handler (only if member)
		if m.isMemoryCompoundMember(scope.CompoundID) {
			if baseName, ok := memory.ParseNamespaced(params.Tool); ok {
				return m.memory.HandleToolCall(baseName, params.Arguments)
			}
		}
		// Route to the backend server — reuse the normal tool call path
		serverName, toolName, err := parseNamespacedTool(params.Tool)
		if err != nil {
			return nil, fmt.Errorf("invalid tool name format: %s (expected serverName__toolName)", params.Tool)
		}
		srv, err := m.store.GetServerByName(serverName)
		if err != nil {
			return nil, fmt.Errorf("server not found: %s", serverName)
		}
		// Verify server is a compound member
		memberIDs, err := m.store.GetCompoundMemberIDs(scope.CompoundID)
		if err != nil {
			return nil, fmt.Errorf("failed to verify compound membership: %w", err)
		}
		allowed := false
		for _, mid := range memberIDs {
			if mid == srv.ID {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("tool '%s' is not available in this compound", params.Tool)
		}
		m.mu.RLock()
		client, ok := m.clients[srv.ID]
		m.mu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("server not connected: %s", serverName)
		}
		return client.CallTool(toolName, params.Arguments)

	case "search":
		if params.Query == "" {
			return nil, fmt.Errorf("query parameter is required for search action")
		}
		query := strings.ToLower(params.Query)
		var results []map[string]string
		for _, t := range allTools {
			name := fmt.Sprintf("%s__%s", t.ServerName, t.Name)
			if strings.Contains(strings.ToLower(name), query) ||
				strings.Contains(strings.ToLower(t.Description), query) ||
				strings.Contains(strings.ToLower(t.ServerName), query) {
				results = append(results, map[string]string{
					"name":        name,
					"server":      t.ServerName,
					"description": t.Description,
				})
			}
		}
		// Search memory tools too (only if memory is a compound member)
		if m.isMemoryCompoundMember(scope.CompoundID) {
			for _, mt := range m.memory.Tools() {
				name := memory.NamespacedName(mt.Name)
				if strings.Contains(strings.ToLower(name), query) ||
					strings.Contains(strings.ToLower(mt.Description), query) {
					results = append(results, map[string]string{
						"name":        name,
						"server":      "memory",
						"description": mt.Description,
					})
				}
			}
		}
		return wrapMCPContent(map[string]interface{}{
			"results": results,
			"count":   len(results),
			"query":   params.Query,
		})

	default:
		return nil, fmt.Errorf("unknown dictionary action: %s (valid: list, describe, call, search)", params.Action)
	}
}

// wrapMCPContent wraps a result in MCP content format.
func wrapMCPContent(result interface{}) (json.RawMessage, error) {
	textBytes, _ := json.Marshal(result)
	return json.Marshal(map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": string(textBytes),
			},
		},
	})
}
