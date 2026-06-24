package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/agentic/mcp-proxy/internal/mcp"
	"github.com/agentic/mcp-proxy/internal/memory"
	"github.com/agentic/mcp-proxy/internal/models"
	"github.com/agentic/mcp-proxy/internal/store"
	"github.com/google/uuid"
)

// envVarRefPattern matches ${KEY} or ${KEY:-default} patterns in env var values.
var envVarRefPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// resolveEnvRefs resolves ${KEY} and ${KEY:-default} references in env map
// values using the decrypted env vars from the store. Unknown keys are left
// as-is (or replaced with the default if specified).
func resolveEnvRefs(envMap map[string]string, refVars map[string]string) map[string]string {
	resolved := make(map[string]string, len(envMap))
	for key, val := range envMap {
		resolved[key] = envVarRefPattern.ReplaceAllStringFunc(val, func(match string) string {
			sub := envVarRefPattern.FindStringSubmatch(match)
			refKey := sub[1]
			defaultVal := sub[2]
			if v, ok := refVars[refKey]; ok {
				return v
			}
			if defaultVal != "" || strings.Contains(match, ":-") {
				return defaultVal
			}
			return match
		})
	}
	return resolved
}

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
	store          *store.Store
	memorySets     map[string]*memory.Server    // setID -> memory server
	mu             sync.RWMutex
	clients        map[string]*mcp.Client         // serverID -> client
	errors         map[string]string              // serverID -> last error message
	authStates     map[string]*mcp.AuthState     // state -> pending OAuth flow
	deviceAuths    map[string]*DeviceAuthResult   // serverID -> pending device code flow
	logMu          sync.RWMutex
	serverLogs     map[string]*serverLog          // serverID -> stderr log ring buffer
	oauthMetaCache map[string]*mcp.OAuthServerMetadata // serverID -> cached discovery result
	oauthMetaMu    sync.RWMutex
}

// New creates a new proxy Manager.
func New(s *store.Store) *Manager {
	m := &Manager{
		store:          s,
		memorySets:     make(map[string]*memory.Server),
		clients:        make(map[string]*mcp.Client),
		errors:         make(map[string]string),
		authStates:     make(map[string]*mcp.AuthState),
		deviceAuths:    make(map[string]*DeviceAuthResult),
		serverLogs:     make(map[string]*serverLog),
		oauthMetaCache: make(map[string]*mcp.OAuthServerMetadata),
	}
	m.InitMemorySets()
	return m
}

// InitMemorySets loads all memory sets from the store and creates a memory.Server for each.
func (m *Manager) InitMemorySets() {
	sets, err := m.store.ListMemorySets()
	if err != nil {
		log.Printf("Failed to list memory sets: %v", err)
		return
	}
	for _, ms := range sets {
		m.memorySets[ms.ID] = memory.New(m.store, ms.ID, ms.Slug)
	}
	// Ensure default set exists
	if _, ok := m.memorySets["default"]; !ok {
		defaultSet := &models.MemorySet{
			ID:        "default",
			Name:      "Default",
			Slug:      "",
			IsDefault: true,
			CreatedAt: time.Now(),
		}
		_ = m.store.CreateMemorySet(defaultSet)
		m.memorySets["default"] = memory.New(m.store, "default", "")
	}
}

// MemoryServerID returns the virtual server ID for a memory set.
// "default" → "builtin-memory", other sets → "builtin-memory:{set_id}".
func memoryServerID(setID string) string {
	if setID == "default" {
		return models.BuiltinMemoryServerID
	}
	return models.BuiltinMemoryServerID + ":" + setID
}

// GetMemoryServer returns the memory server instance for a given set ID.
func (m *Manager) GetMemoryServer(setID string) *memory.Server {
	return m.memorySets[setID]
}

// findMemoryServerBySlug returns the memory server with the given slug.
func (m *Manager) findMemoryServerBySlug(slug string) *memory.Server {
	for _, srv := range m.memorySets {
		if srv.Slug() == slug {
			return srv
		}
	}
	return nil
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
	// Determine auth token based on auth method
	authToken := ""

	switch srv.AuthMethod {
	case "bearer":
		// Manual bearer token stored in auth_token field
		authToken = srv.AuthToken
		if authToken == "" {
			log.Printf("Server %s: auth_method=bearer but auth_token is empty", srv.Name)
		} else {
			log.Printf("Server %s: using manual bearer token (%d chars)", srv.Name, len(authToken))
		}
	case "env_bearer":
		// Bearer token read from an environment variable
		if srv.BearerTokenEnv != "" {
			authToken = os.Getenv(srv.BearerTokenEnv)
			if authToken == "" {
				log.Printf("Server %s: env var %s is empty — token not available", srv.Name, srv.BearerTokenEnv)
			} else {
				log.Printf("Server %s: using env var bearer token from %s (%d chars)", srv.Name, srv.BearerTokenEnv, len(authToken))
			}
		} else {
			log.Printf("Server %s: auth_method=env_bearer but bearer_token_env is not set", srv.Name)
		}
	case "oauth":
		// OAuth flow — check for stored tokens (with auto-refresh)
		log.Printf("Server %s: using OAuth auth method", srv.Name)
		if srv.Transport == "http" || srv.Transport == "streamable-http" {
			tokens, cid, csec, err := m.store.GetOAuthTokens(srv.ID)
			if err == nil && tokens != nil {
				// Check if token needs refresh
				if tokens.IsExpired() && tokens.HasRefreshToken() {
					log.Printf("Server %s: OAuth token expired, attempting refresh", srv.Name)
					meta, _ := mcp.DiscoverOAuthMetadata(srv.URL)
					if meta != nil && meta.TokenEndpoint != "" {
						refreshed, err := mcp.RefreshToken(meta.TokenEndpoint, cid, csec, tokens.RefreshToken)
						if err == nil {
							tokens = refreshed
							_ = m.store.SaveOAuthTokens(srv.ID, tokens, cid, csec)
							log.Printf("Server %s: OAuth token refreshed successfully (%d chars)", srv.Name, len(tokens.AccessToken))
						} else {
							log.Printf("Server %s: failed to refresh OAuth token: %v", srv.Name, err)
						}
					} else {
						log.Printf("Server %s: cannot refresh — no OAuth metadata or token endpoint found", srv.Name)
					}
				} else {
					log.Printf("Server %s: using stored OAuth access token (%d chars, expires=%v)", srv.Name, len(tokens.AccessToken), tokens.ExpiresAt)
				}
				authToken = tokens.AccessToken
			} else {
				log.Printf("Server %s: no stored OAuth tokens — connection will likely fail with 401", srv.Name)
			}
		}
	default:
		// "none" or empty — backward compatibility:
		// For HTTP transports, check if we have stored OAuth tokens first.
		// Existing servers may have auth_method="" but OAuth tokens in the DB
		// (from a previous OAuth flow) and auth_token set as a client_id.
		if srv.Transport == "http" || srv.Transport == "streamable-http" {
			tokens, cid, csec, err := m.store.GetOAuthTokens(srv.ID)
			if err == nil && tokens != nil {
				// We have stored OAuth tokens — use OAuth flow
				log.Printf("Server %s: auth_method=%q but found stored OAuth tokens — using OAuth (backward compat)", srv.Name, srv.AuthMethod)
				if tokens.IsExpired() && tokens.HasRefreshToken() {
					log.Printf("Server %s: OAuth token expired, attempting refresh", srv.Name)
					meta, _ := mcp.DiscoverOAuthMetadata(srv.URL)
					if meta != nil && meta.TokenEndpoint != "" {
						refreshed, err := mcp.RefreshToken(meta.TokenEndpoint, cid, csec, tokens.RefreshToken)
						if err == nil {
							tokens = refreshed
							_ = m.store.SaveOAuthTokens(srv.ID, tokens, cid, csec)
							log.Printf("Server %s: OAuth token refreshed successfully (%d chars)", srv.Name, len(tokens.AccessToken))
						} else {
							log.Printf("Server %s: failed to refresh OAuth token: %v", srv.Name, err)
						}
					}
				}
				authToken = tokens.AccessToken
			} else {
				// No OAuth tokens — fall back to auth_token as a static bearer token
				authToken = srv.AuthToken
				if authToken != "" {
					log.Printf("Server %s: no auth_method set, using auth_token as static bearer (%d chars)", srv.Name, len(authToken))
				} else {
					log.Printf("Server %s: no auth configured (auth_method=%q, no tokens, no auth_token)", srv.Name, srv.AuthMethod)
				}
			}
		} else {
			// Non-HTTP transports: use auth_token directly
			authToken = srv.AuthToken
			if authToken != "" {
				log.Printf("Server %s: using auth_token for non-HTTP transport (%d chars)", srv.Name, len(authToken))
			}
		}
	}

	// Resolve ${KEY} references in the server's env map using decrypted env
	// vars from the store. This allows servers to reference shared secrets
	// (stored in the env_vars table) without hardcoding them.
	serverEnv := srv.Env
	if len(serverEnv) > 0 {
		hasRefs := false
		for _, v := range serverEnv {
			if envVarRefPattern.MatchString(v) {
				hasRefs = true
				break
			}
		}
		if hasRefs {
			if refVars, err := m.store.ListEnvVarsDecrypted(); err == nil && len(refVars) > 0 {
				serverEnv = resolveEnvRefs(serverEnv, refVars)
				log.Printf("Server %s: resolved env var references from %d stored env vars", srv.Name, len(refVars))
			} else if err != nil {
				log.Printf("Server %s: failed to load env vars for reference resolution: %v", srv.Name, err)
			}
		}
	}

	cfg := mcp.ClientConfig{
		Transport:      srv.Transport,
		Command:        srv.Command,
		Args:           srv.Args,
		Env:            serverEnv,
		URL:            srv.URL,
		Headers:        srv.Headers,
		AuthToken:      authToken,
		Timeout:        srv.Timeout,
		ConnectTimeout: srv.ConnectTimeout,
	}
	if srv.LogsEnabled {
		cfg.OnStderr = func(line string) {
			m.addServerLog(srv.ID, line)
		}
	}

	client := mcp.NewClient(cfg)
	log.Printf("Connecting to %s (transport=%s, auth_method=%s, has_token=%v, token_len=%d)", srv.URL, srv.Transport, srv.AuthMethod, authToken != "", len(authToken))
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

	logsEnabled := true
	if req.LogsEnabled != nil {
		logsEnabled = *req.LogsEnabled
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
		AuthMethod:     req.AuthMethod,
		BearerTokenEnv: req.BearerTokenEnv,
		Timeout:        timeout,
		ConnectTimeout: connTimeout,
		Enabled:        enabled,
		LogsEnabled:    logsEnabled,
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
	if req.AuthMethod != nil {
		srv.AuthMethod = *req.AuthMethod
	}
	if req.BearerTokenEnv != nil {
		srv.BearerTokenEnv = *req.BearerTokenEnv
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
	if req.LogsEnabled != nil {
		srv.LogsEnabled = *req.LogsEnabled
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
	// Add memory tools from all memory sets
	for _, srv := range m.memorySets {
		for _, mt := range srv.Tools() {
			tools = append(tools, models.Tool{
				ServerID:    memoryServerID(srv.SetID()),
				ServerName:  "memory",
				Name:        mt.Name,
				Description: mt.Description,
			})
		}
	}
	return tools
}

// Memory returns the default memory server instance (backward compat).
func (m *Manager) Memory() *memory.Server {
	return m.memorySets["default"]
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
	case "notifications/initialized":
		// No response needed for notifications — the SSE handler already
		// returns 202 for notifications without an ID. Return nil result.
		return nil, nil
	case "tools/list":
		return m.handleToolsList(req, scope)
	case "tools/call":
		return m.handleToolsCall(ctx, req, scope)
	default:
		return nil, fmt.Errorf("unsupported method: %s", req.Method)
	}
}

func (m *Manager) handleInitialize(req mcp.JSONRPCRequest) (json.RawMessage, error) {
	// Read the client's requested protocol version from the initialize params
	clientVersion := ""
	if len(req.Params) > 0 {
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := json.Unmarshal(req.Params, &params); err == nil {
			clientVersion = params.ProtocolVersion
		}
	}

	// Negotiate the highest protocol version both client and server support.
	// The proxy currently supports "2025-03-26".
	const supportedVersion = "2025-03-26"
	negotiatedVersion := supportedVersion
	if clientVersion == supportedVersion {
		negotiatedVersion = clientVersion
	}

	result := map[string]interface{}{
		"protocolVersion": negotiatedVersion,
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
			// Dictionary mode: return ONLY the dictionary tool.
			// All member tools (including memory) are discovered lazily via the dictionary.
			tools := []mcp.Tool{{
				Name:        "dictionary",
				Description: dictionaryDescription,
				InputSchema: dictionarySchema,
			}}
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

	// Add built-in memory tools from all memory sets
	// For global and server scope: always available
	// For compound scope: only sets that are compound members
	var memorySetIDs []string
	if scope.CompoundID != "" {
		memorySetIDs = m.isMemoryCompoundMember(scope.CompoundID)
	} else {
		for setID := range m.memorySets {
			memorySetIDs = append(memorySetIDs, setID)
		}
	}
	for _, setID := range memorySetIDs {
		if srv, ok := m.memorySets[setID]; ok {
			setSuffix := ""
			if srv.Slug() != "" {
				if ms, err := m.store.GetMemorySet(setID); err == nil && ms.Name != "" {
					setSuffix = fmt.Sprintf(" [%s]", ms.Name)
				} else {
					setSuffix = fmt.Sprintf(" [%s]", srv.Slug())
				}
			}
			for _, mt := range srv.Tools() {
				tool := mcp.Tool{
					Name:        srv.NamespacedName(mt.Name),
					Description: fmt.Sprintf("[memory%s] %s", setSuffix, mt.Description),
				}
				if len(mt.InputSchema) > 0 {
					tool.InputSchema = mt.InputSchema
				}
				mcpTools = append(mcpTools, tool)
			}
		}
	}

	if mcpTools == nil {
		mcpTools = []mcp.Tool{}
	}

	// Filter out disabled tools (global + per-compound).
	mcpTools = m.filterDisabledMCPTools(mcpTools, scope)

	result := mcp.ToolListResult{Tools: mcpTools}
	return json.Marshal(result)
}

// filterDisabledMCPTools removes tools that have been disabled either globally
// or for the compound in the given scope. toolName is the namespaced name
// (serverName__toolName) as exposed to MCP clients.
func (m *Manager) filterDisabledMCPTools(tools []mcp.Tool, scope Scope) []mcp.Tool {
	var compoundIDPtr *string
	if scope.CompoundID != "" {
		compoundIDPtr = &scope.CompoundID
	}
	filtered := make([]mcp.Tool, 0, len(tools))
	for _, t := range tools {
		disabled, err := m.store.IsToolDisabled(t.Name, compoundIDPtr)
		if err == nil && disabled {
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
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
	// ParseNamespaced returns (slug, toolName, ok)
	if slug, baseName, ok := memory.ParseNamespaced(params.Name); ok {
		// Find the memory server for this slug
		memSrv := m.findMemoryServerBySlug(slug)
		if memSrv == nil {
			return nil, fmt.Errorf("memory set not found for slug: %s", slug)
		}
		// For compound scope, verify this memory set is a compound member
		if scope.CompoundID != "" {
			memberSetIDs := m.isMemoryCompoundMember(scope.CompoundID)
			allowed := false
			for _, sid := range memberSetIDs {
				if sid == memSrv.SetID() {
					allowed = true
					break
				}
			}
			if !allowed {
				return nil, fmt.Errorf("memory tools are not available in this compound — add the memory server as a member")
			}
		}
		return memSrv.HandleToolCall(baseName, params.Arguments)
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

	// Set auth method to oauth if not already set
	if srv.AuthMethod == "" || srv.AuthMethod == "none" {
		srv.AuthMethod = "oauth"
		_ = m.store.UpdateServer(srv)
	}

	// Discover OAuth metadata
	metadata, err := mcp.DiscoverOAuthMetadata(srv.URL)
	if err != nil {
		return "", fmt.Errorf("failed to discover OAuth metadata: %w", err)
	}

	redirectURI := fmt.Sprintf("%s/api/oauth/callback", callbackBaseURL)
	cimdURL := fmt.Sprintf("%s/api/oauth/client-metadata", callbackBaseURL)
	var clientID, clientSecret string

	// Priority 1: Pre-registered client ID (auth_token field)
	if srv.AuthToken != "" {
		clientID = srv.AuthToken
		log.Printf("Using pre-registered client ID for server %s", srv.Name)
	}

	// Priority 2: Client ID Metadata Documents (CIMD)
	if clientID == "" && metadata.ClientIDMetadataDocumentSupported {
		clientID = cimdURL
		log.Printf("Using Client ID Metadata Document for server %s: %s", srv.Name, cimdURL)
	}

	// Priority 3: Dynamic Client Registration (RFC 7591)
	if clientID == "" && metadata.RegistrationEndpoint != "" {
		// Check for persisted registration first (Authorization Server Binding)
		if metadata.Issuer != "" {
			if reg, err := m.store.GetOAuthRegistration(metadata.Issuer); err == nil && reg != nil {
				clientID = reg.ClientID
				clientSecret = reg.ClientSecret
				log.Printf("Reusing persisted DCR client for server %s (issuer %s)", srv.Name, metadata.Issuer)
			}
		}
		// Register new client if none persisted
		if clientID == "" {
			reg, err := mcp.RegisterClient(metadata.RegistrationEndpoint, []string{redirectURI})
			if err == nil {
				clientID = reg.ClientID
				clientSecret = reg.ClientSecret
				log.Printf("Dynamically registered OAuth client for server %s: %s", srv.Name, clientID)
				// Persist the registration keyed by issuer
				if metadata.Issuer != "" {
					if err := m.store.SaveOAuthRegistration(reg, metadata.Issuer); err != nil {
						log.Printf("Warning: failed to persist DCR registration: %v", err)
					}
				}
			} else {
				log.Printf("Dynamic registration failed for %s: %v", srv.Name, err)
			}
		}
	}

	// Priority 4: Entra ID public client (well-known client_id)
	if clientID == "" && (mcp.IsEntraID(metadata.Issuer) || mcp.IsEntraID(metadata.AuthorizationEndpoint) || mcp.IsEntraID(metadata.TokenEndpoint)) {
		clientID = mcp.EntraIDPublicClientID
		log.Printf("Using Entra ID public client for server %s (no app registration required)", srv.Name)
	}

	if clientID == "" {
		return "", fmt.Errorf("no client ID available — the authorization server does not support CIMD or dynamic registration. Configure a client_id in the server's Auth Token field, or register an OAuth app in your identity provider and enter the client ID")
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
	resource := srv.URL
	authURL, err := mcp.BuildAuthURL(metadata, clientID, redirectURI, pkce, scopes, state, resource)
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
		Resource:              resource,
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
		authState.Resource,
	)
	if err != nil {
		return fmt.Errorf("token exchange failed: %w", err)
	}

	// Store tokens
	if err := m.store.SaveOAuthTokens(authState.ServerID, tokens, authState.ClientID, authState.ClientSecret); err != nil {
		return fmt.Errorf("failed to save tokens: %w", err)
	}

	log.Printf("OAuth tokens stored for server %s", authState.ServerID)

	// Set auth method to oauth
	srv, err := m.store.GetServer(authState.ServerID)
	if err != nil {
		return fmt.Errorf("server not found after auth: %w", err)
	}
	srv.AuthMethod = "oauth"
	_ = m.store.UpdateServer(srv)

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

// GetServerAuthMethod returns the configured auth method for a server.
func (m *Manager) GetServerAuthMethod(serverID string) string {
	srv, err := m.store.GetServer(serverID)
	if err != nil || srv == nil {
		return "none"
	}
	if srv.AuthMethod == "" || srv.AuthMethod == "none" {
		// Backward compat: check if we have stored OAuth tokens
		if srv.Transport == "http" || srv.Transport == "streamable-http" {
			tokens, _, _, err := m.store.GetOAuthTokens(serverID)
			if err == nil && tokens != nil {
				return "oauth"
			}
		}
		// If auth_token is set but no OAuth tokens, infer "bearer"
		if srv.AuthToken != "" {
			return "bearer"
		}
		return "none"
	}
	return srv.AuthMethod
}

// HasBearerToken returns true if the server has a manual bearer token configured.
func (m *Manager) HasBearerToken(serverID string) bool {
	srv, err := m.store.GetServer(serverID)
	if err != nil || srv == nil {
		return false
	}
	return srv.AuthToken != ""
}

// HasEnvBearerToken returns true if the server has an env var bearer token configured.
func (m *Manager) HasEnvBearerToken(serverID string) bool {
	srv, err := m.store.GetServer(serverID)
	if err != nil || srv == nil {
		return false
	}
	return srv.BearerTokenEnv != ""
}

// GetBearerTokenEnv returns the env var name configured for env_bearer auth.
func (m *Manager) GetBearerTokenEnv(serverID string) string {
	srv, err := m.store.GetServer(serverID)
	if err != nil || srv == nil {
		return ""
	}
	return srv.BearerTokenEnv
}

// SetBearerToken sets a manual bearer token for a server and reconnects.
func (m *Manager) SetBearerToken(serverID, token string) error {
	srv, err := m.store.GetServer(serverID)
	if err != nil {
		return fmt.Errorf("server not found: %w", err)
	}
	log.Printf("Server %s: switching to bearer auth (token len=%d)", srv.Name, len(token))
	srv.AuthToken = token
	srv.AuthMethod = "bearer"
	srv.BearerTokenEnv = ""
	if err := m.store.UpdateServer(srv); err != nil {
		return fmt.Errorf("failed to update server: %w", err)
	}
	// Reconnect with the new token
	m.DisconnectServer(serverID)
	go m.connectServer(srv)
	return nil
}

// SetEnvBearerToken sets an env var-based bearer token for a server and reconnects.
func (m *Manager) SetEnvBearerToken(serverID, envVar string) error {
	srv, err := m.store.GetServer(serverID)
	if err != nil {
		return fmt.Errorf("server not found: %w", err)
	}
	log.Printf("Server %s: switching to env_bearer auth (env var=%s)", srv.Name, envVar)
	srv.BearerTokenEnv = envVar
	srv.AuthMethod = "env_bearer"
	srv.AuthToken = ""
	if err := m.store.UpdateServer(srv); err != nil {
		return fmt.Errorf("failed to update server: %w", err)
	}
	m.DisconnectServer(serverID)
	go m.connectServer(srv)
	return nil
}

// ClearAuth removes all auth configuration for a server and reconnects.
func (m *Manager) ClearAuth(serverID string) error {
	srv, err := m.store.GetServer(serverID)
	if err != nil {
		return fmt.Errorf("server not found: %w", err)
	}
	log.Printf("Server %s: clearing auth (was method=%s)", srv.Name, srv.AuthMethod)
	srv.AuthMethod = "none"
	srv.AuthToken = ""
	srv.BearerTokenEnv = ""
	if err := m.store.UpdateServer(srv); err != nil {
		return fmt.Errorf("failed to update server: %w", err)
	}
	m.DisconnectServer(serverID)
	go m.connectServer(srv)
	return nil
}

// GetOAuthMetadata returns cached OAuth metadata for a server, discovering it
// on first access and caching the result.
func (m *Manager) GetOAuthMetadata(serverID string) *mcp.OAuthServerMetadata {
	// Check cache first
	m.oauthMetaMu.RLock()
	if meta, ok := m.oauthMetaCache[serverID]; ok {
		m.oauthMetaMu.RUnlock()
		return meta
	}
	m.oauthMetaMu.RUnlock()

	// Discover and cache
	srv, err := m.store.GetServer(serverID)
	if err != nil || srv == nil || (srv.Transport != "http" && srv.Transport != "streamable-http") {
		return nil
	}

	meta, err := mcp.DiscoverOAuthMetadata(srv.URL)
	if err != nil || meta == nil {
		return nil
	}

	m.oauthMetaMu.Lock()
	m.oauthMetaCache[serverID] = meta
	m.oauthMetaMu.Unlock()

	return meta
}

// InvalidateOAuthMetadataCache removes cached OAuth metadata for a server.
func (m *Manager) InvalidateOAuthMetadataCache(serverID string) {
	m.oauthMetaMu.Lock()
	delete(m.oauthMetaCache, serverID)
	m.oauthMetaMu.Unlock()
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
	Resource        string `json:"-"`
}

// InitiateDeviceAuth starts a device code flow for a server. This is the preferred
// method for Entra ID because it doesn't require a redirect URI — works from any deployment.
// Only works when the auth server metadata has a device_authorization_endpoint.
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

	// Check if device code flow is supported
	if metadata.DeviceAuthorizationEndpoint == "" && !mcp.IsEntraID(metadata.Issuer) && !mcp.IsEntraID(metadata.AuthorizationEndpoint) && !mcp.IsEntraID(metadata.TokenEndpoint) {
		return nil, fmt.Errorf("device code flow not supported by this server — no device_authorization_endpoint in metadata")
	}

	// Determine client ID using MCP spec priority order:
	// 1. Pre-registered (auth_token)
	// 2. CIMD (if supported)
	// 3. Persisted or new Dynamic Client Registration
	// 4. Entra ID public client
	clientID := ""

	// Priority 1: Pre-registered client ID
	if srv.AuthToken != "" {
		clientID = srv.AuthToken
		log.Printf("Using pre-registered client ID for device auth (server %s)", srv.Name)
	}

	// Priority 2: CIMD — device code doesn't need redirect_uri but auth server
	// may still fetch the metadata document to validate the client
	if clientID == "" && metadata.ClientIDMetadataDocumentSupported {
		log.Printf("CIMD supported but device code flow — skipping for server %s", srv.Name)
	}

	// Priority 3: Dynamic Client Registration
	if clientID == "" && metadata.RegistrationEndpoint != "" {
		if metadata.Issuer != "" {
			if reg, err := m.store.GetOAuthRegistration(metadata.Issuer); err == nil && reg != nil {
				clientID = reg.ClientID
				log.Printf("Reusing persisted DCR client for device auth (server %s)", srv.Name)
			}
		}
		if clientID == "" {
			reg, err := mcp.RegisterClient(metadata.RegistrationEndpoint, nil)
			if err == nil {
				clientID = reg.ClientID
				log.Printf("Dynamically registered client for device auth (server %s): %s", srv.Name, clientID)
				if metadata.Issuer != "" {
					m.store.SaveOAuthRegistration(reg, metadata.Issuer)
				}
			}
		}
	}

	// Priority 4: Entra ID public client
	if clientID == "" && (mcp.IsEntraID(metadata.Issuer) || mcp.IsEntraID(metadata.AuthorizationEndpoint) || mcp.IsEntraID(metadata.TokenEndpoint)) {
		clientID = mcp.EntraIDPublicClientID
		log.Printf("Using Entra ID public client for device code flow (server %s)", srv.Name)
	}

	if clientID == "" {
		return nil, fmt.Errorf("no client ID available — configure a client_id in the server's Auth Token field")
	}

	// Build scope from metadata (spec: use all scopes_supported if no scope in WWW-Authenticate)
	scope := ""
	if len(metadata.ScopesSupported) > 0 {
		scope = strings.Join(metadata.ScopesSupported, " ")
	}

	// resource parameter (RFC 8707) — the MCP server URL
	resource := srv.URL

	// Request device code
	dcResp, err := mcp.RequestDeviceCode(metadata, clientID, scope, resource)
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
		Resource:        resource,
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

	tokens, err := mcp.PollDeviceToken(auth.TokenEndpoint, auth.ClientID, auth.DeviceCode, auth.Resource)
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

// isMemoryCompoundMember returns the set IDs of all memory sets that are
// members of the specified compound. Checks for both "builtin-memory" (default)
// and "builtin-memory:{set_id}" patterns.
// IsMemoryCompoundMember returns the set IDs of all memory sets that are
// members of the specified compound. Exported wrapper for API handlers.
func (m *Manager) IsMemoryCompoundMember(compoundID string) []string {
	return m.isMemoryCompoundMember(compoundID)
}

func (m *Manager) isMemoryCompoundMember(compoundID string) []string {
	memberIDs, err := m.store.GetCompoundMemberIDs(compoundID)
	if err != nil {
		return nil
	}
	var setIDs []string
	for _, mid := range memberIDs {
		if mid == models.BuiltinMemoryServerID {
			setIDs = append(setIDs, "default")
		} else if strings.HasPrefix(mid, models.BuiltinMemoryServerID+":") {
			setID := strings.TrimPrefix(mid, models.BuiltinMemoryServerID+":")
			setIDs = append(setIDs, setID)
		}
	}
	return setIDs
}

const dictionaryDescription = `Lazy tool discovery for compound servers. Instead of receiving all tools upfront, use this single tool to discover, inspect, and call any tool from member servers — including built-in memory tools.

HOW TO USE (recommended workflow):

1. Call with action "list" to get a lightweight catalog of all available tools (name + description + server). No schemas — just enough to decide what's relevant.

2. Call with action "describe" and pass a tool name to get its full input schema before calling it. This avoids guessing parameter formats.

3. Call with action "call" with the tool name and arguments to execute it. Arguments must match the schema from "describe".

4. Use action "search" with a keyword to find tools by name or description when you're not sure what's available.

ACTIONS (pass as "action" parameter):

— list: List all available tools. Returns {tools: [{name, server, description}], count}.
  No additional parameters.

— describe: Get full input schema for a tool. Returns {name, server, description, inputSchema}.
  Required: tool (tool name from list/search).

— call: Execute a tool. Returns the tool's raw response.
  Required: tool (tool name), arguments (JSON object matching the tool's inputSchema).

— search: Search tools by keyword. Returns {results: [{name, server, description}], count, query}.
  Required: query (search term).

TOOL NAMING:
Tool names use "serverName__toolName" format. Memory tools use "memory__toolName" (default set) or "memory_slug__toolName" (custom sets). Server tools use the server's name as prefix.

TIP: Start with "list" to see everything available, then "describe" before "call" to avoid parameter errors. Use "search" when looking for something specific.`

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
		// Return lightweight catalog: name + description + type
		var catalog []map[string]string
		for _, t := range allTools {
			name := fmt.Sprintf("%s__%s", t.ServerName, t.Name)
			catalog = append(catalog, map[string]string{
				"name":        name,
				"server":      t.ServerName,
				"type":        "server",
				"description": t.Description,
			})
		}
		// Include memory tools from sets that are compound members
		for _, setID := range m.isMemoryCompoundMember(scope.CompoundID) {
			if srv, ok := m.memorySets[setID]; ok {
				memSetName := "memory"
				if ms, err := m.store.GetMemorySet(setID); err == nil && ms.Name != "" {
					memSetName = ms.Name
				}
				for _, mt := range srv.Tools() {
					catalog = append(catalog, map[string]string{
						"name":        srv.NamespacedName(mt.Name),
						"server":      memSetName,
						"type":        "memory",
						"description": mt.Description,
					})
				}
			}
		}
		// Filter out disabled tools (global + per-compound).
		var compoundIDPtr *string
		if scope.CompoundID != "" {
			compoundIDPtr = &scope.CompoundID
		}
		filteredCatalog := make([]map[string]string, 0, len(catalog))
		for _, entry := range catalog {
			disabled, err := m.store.IsToolDisabled(entry["name"], compoundIDPtr)
			if err == nil && disabled {
				continue
			}
			filteredCatalog = append(filteredCatalog, entry)
		}
		catalog = filteredCatalog
		return wrapMCPContent(map[string]interface{}{
			"tools": catalog,
			"count": len(catalog),
		})

	case "describe":
		if params.Tool == "" {
			return nil, fmt.Errorf("tool parameter is required for describe action")
		}
		// Check memory tools first (only from sets that are compound members)
		if slug, baseName, ok := memory.ParseNamespaced(params.Tool); ok {
			for _, setID := range m.isMemoryCompoundMember(scope.CompoundID) {
				if srv, ok := m.memorySets[setID]; ok && srv.Slug() == slug {
					for _, mt := range srv.Tools() {
						if mt.Name == baseName {
							return wrapMCPContent(map[string]interface{}{
								"name":         params.Tool,
								"server":      "memory",
								"type":         "memory",
								"description": mt.Description,
								"inputSchema": mt.InputSchema,
							})
						}
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
					"type":         "server",
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
		if slug, baseName, ok := memory.ParseNamespaced(params.Tool); ok {
			for _, setID := range m.isMemoryCompoundMember(scope.CompoundID) {
				if srv, ok := m.memorySets[setID]; ok && srv.Slug() == slug {
					return srv.HandleToolCall(baseName, params.Arguments)
				}
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
					"type":        "server",
					"description": t.Description,
				})
			}
		}
		// Search memory tools too (from sets that are compound members)
		for _, setID := range m.isMemoryCompoundMember(scope.CompoundID) {
			if srv, ok := m.memorySets[setID]; ok {
				for _, mt := range srv.Tools() {
					name := srv.NamespacedName(mt.Name)
					if strings.Contains(strings.ToLower(name), query) ||
						strings.Contains(strings.ToLower(mt.Description), query) {
						results = append(results, map[string]string{
							"name":        name,
							"server":      "memory",
							"type":        "memory",
							"description": mt.Description,
						})
					}
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

// --- Memory Set Management ---

// makeSlug creates a URL-safe slug from a name: lowercase, replace spaces/special chars with underscores.
func makeSlug(name string) string {
	slug := strings.ToLower(name)
	slug = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, slug)
	// Collapse consecutive underscores and trim leading/trailing
	for strings.Contains(slug, "__") {
		slug = strings.ReplaceAll(slug, "__", "_")
	}
	return strings.Trim(slug, "_")
}

// CreateMemorySet creates a new memory set, adds it to the in-memory map, and returns it.
func (m *Manager) CreateMemorySet(name, description string) (*models.MemorySet, error) {
	slug := makeSlug(name)

	// Check slug uniqueness
	existing, err := m.store.GetMemorySetBySlug(slug)
	if err == nil && existing != nil {
		return nil, fmt.Errorf("a memory set with slug '%s' already exists", slug)
	}

	ms := &models.MemorySet{
		ID:          uuid.NewString(),
		Name:        name,
		Slug:        slug,
		Description: description,
		IsDefault:   false,
		CreatedAt:   time.Now(),
	}

	if err := m.store.CreateMemorySet(ms); err != nil {
		return nil, fmt.Errorf("failed to create memory set: %w", err)
	}

	m.memorySets[ms.ID] = memory.New(m.store, ms.ID, ms.Slug)
	return ms, nil
}

// GetMemorySet returns a memory set by ID.
func (m *Manager) GetMemorySet(id string) (*models.MemorySet, error) {
	return m.store.GetMemorySet(id)
}

// ListMemorySets returns all memory sets.
func (m *Manager) ListMemorySets() ([]*models.MemorySet, error) {
	return m.store.ListMemorySets()
}

// UpdateMemorySet updates a memory set and refreshes the in-memory server.
func (m *Manager) UpdateMemorySet(id string, req *models.UpdateMemorySetRequest) error {
	if err := m.store.UpdateMemorySet(id, req); err != nil {
		return err
	}
	// Refresh the in-memory server
	ms, err := m.store.GetMemorySet(id)
	if err == nil && ms != nil {
		m.memorySets[ms.ID] = memory.New(m.store, ms.ID, ms.Slug)
	}
	return nil
}

// DeleteMemorySet removes a memory set, all its memories, and the in-memory server.
func (m *Manager) DeleteMemorySet(id string) error {
	if id == "default" {
		return fmt.Errorf("cannot delete the default memory set")
	}
	// Delete all memories belonging to this set
	if err := m.store.DeleteMemoriesBySet(id); err != nil {
		return fmt.Errorf("failed to delete memories in set: %w", err)
	}
	if err := m.store.DeleteMemorySet(id); err != nil {
		return err
	}
	delete(m.memorySets, id)
	return nil
}
