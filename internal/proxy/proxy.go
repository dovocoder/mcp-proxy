package proxy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/agentic/mcp-proxy/internal/mcp"
	"github.com/agentic/mcp-proxy/internal/memory"
	"github.com/agentic/mcp-proxy/internal/models"
	"github.com/agentic/mcp-proxy/internal/skills"
	"github.com/agentic/mcp-proxy/internal/store"
	"github.com/agentic/mcp-proxy/internal/taskboard"
	"github.com/agentic/mcp-proxy/internal/tasks"
	"github.com/google/uuid"
)

// envVarRefPattern matches ${KEY} or ${KEY:-default} patterns in env var values.
var envVarRefPattern = models.EnvVarRefPattern

// projectEnvVarRefPattern matches $[project:environment:var] patterns.
var projectEnvVarRefPattern = models.ProjectEnvVarRefPattern

// resolveEnvRefs resolves ${KEY}, ${KEY:-default}, and $[project:env:var] references
// in env map values. Flat ${KEY} refs resolve against refVars (all env vars).
// $[project:env:var] refs resolve against groupedVars (project:env:key → value).
func resolveEnvRefs(envMap map[string]string, refVars map[string]string) map[string]string {
	return resolveEnvRefsWithGrouped(envMap, refVars, nil)
}

// resolveEnvRefsWithGrouped is the full resolver that also handles
// $[project:env:var] references using the groupedVars map.
func resolveEnvRefsWithGrouped(envMap map[string]string, refVars map[string]string, groupedVars map[string]string) map[string]string {
	resolved := make(map[string]string, len(envMap))
	for key, val := range envMap {
		// First resolve $[project:env:var] references
		if groupedVars != nil {
			val = projectEnvVarRefPattern.ReplaceAllStringFunc(val, func(match string) string {
				subs := projectEnvVarRefPattern.FindStringSubmatch(match)
				if len(subs) < 4 {
					return match
				}
				groupKey := subs[1] + ":" + subs[2] + ":" + subs[3]
				if v, ok := groupedVars[groupKey]; ok {
					return v
				}
				return match
			})
		}
		// Then resolve ${KEY} and ${KEY:-default} references
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

// resolveTokenRef resolves a token reference that may be:
//   - A plain env var name (e.g. "GITHUB_TOKEN") → os.Getenv
//   - A $[project:env:var] reference → resolved from the store's grouped env vars
//   - A ${KEY} reference → resolved from the store's flat env vars
//
// Returns the resolved value, or empty string if not found.
func resolveTokenRef(ref string, st *store.Store) string {
	if ref == "" {
		return ""
	}
	// Check for $[project:env:var] pattern
	if projectEnvVarRefPattern.MatchString(ref) {
		subs := projectEnvVarRefPattern.FindStringSubmatch(ref)
		if len(subs) == 4 {
			groupKey := subs[1] + ":" + subs[2] + ":" + subs[3]
			grouped, err := st.ListEnvVarsDecryptedGrouped()
			if err == nil {
				if v, ok := grouped[groupKey]; ok {
					return v
				}
			}
		}
		return ""
	}
	// Check for ${KEY} pattern
	if envVarRefPattern.MatchString(ref) {
		subs := envVarRefPattern.FindStringSubmatch(ref)
		if len(subs) >= 2 {
			refKey := subs[1]
			flat, err := st.ListEnvVarsDecrypted()
			if err == nil {
				if v, ok := flat[refKey]; ok {
					return v
				}
			}
		}
		return ""
	}
	// Plain env var name
	return os.Getenv(ref)
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
	memorySets     map[string]*memory.Server // setID -> memory server
	memoryMu       sync.RWMutex              // protects memorySets
	skillSets      map[string]*skills.Server // setID -> skill server
	skillMu        sync.RWMutex              // protects skillSets
	mu             sync.RWMutex
	clients        map[string]*mcp.Client       // serverID -> client
	errors         map[string]string            // serverID -> last error message
	authStates     map[string]*mcp.AuthState    // state -> pending OAuth flow
	deviceAuths    map[string]*DeviceAuthResult // serverID -> pending device code flow
	logMu          sync.RWMutex
	serverLogs     map[string]*serverLog               // serverID -> stderr log ring buffer
	oauthMetaCache map[string]*oauthMetaEntry // serverID -> cached discovery result (with TTL)
	oauthMetaMu    sync.RWMutex
	// onToolsChanged is a callback fired when the tool list changes (server connect/disconnect, etc.)
	// Set by the API layer to broadcast notifications/tools/list_changed to SSE clients.
	onToolsChanged func()
	// taskMgr manages task-augmented requests (experimental, 2025-11-25 spec).
	taskMgr *tasks.Manager
	// taskBoard is the built-in persistent task board MCP server.
	taskBoard *taskboard.Server
}

// New creates a new proxy Manager.
func New(s *store.Store) *Manager {
	m := &Manager{
		store:          s,
		memorySets:     make(map[string]*memory.Server),
		skillSets:      make(map[string]*skills.Server),
		clients:        make(map[string]*mcp.Client),
		errors:         make(map[string]string),
		authStates:     make(map[string]*mcp.AuthState),
		deviceAuths:    make(map[string]*DeviceAuthResult),
		serverLogs:     make(map[string]*serverLog),
		oauthMetaCache: make(map[string]*oauthMetaEntry),
		taskMgr:        tasks.New(),
		taskBoard:      taskboard.New(s),
	}
	m.InitMemorySets()
	m.InitSkillSets()
	// Start cleanup goroutine for stale OAuth states and device auths.
	// authStates entries expire after 10 minutes (OAuth flows left incomplete).
	// deviceAuths entries expire after 15 minutes (device code flows left pending).
	go m.cleanupStaleAuthStates()
	return m
}

// SetOnToolsChanged sets a callback that fires when the tool list changes.
// The API layer uses this to broadcast notifications/tools/list_changed to SSE clients.
func (m *Manager) SetOnToolsChanged(fn func()) {
	m.onToolsChanged = fn
}

// fireToolsChanged fires the onToolsChanged callback if set.
func (m *Manager) fireToolsChanged() {
	if m.onToolsChanged != nil {
		go m.onToolsChanged()
	}
}

// authStateTTL is the maximum lifetime of an incomplete OAuth state.
const authStateTTL = 10 * time.Minute

// deviceAuthTTL is the maximum lifetime of a pending device auth flow.
const deviceAuthTTL = 15 * time.Minute

// cleanupStaleAuthStates periodically removes expired OAuth states and device
// auth flows to prevent unbounded memory growth from abandoned OAuth flows.
func (m *Manager) cleanupStaleAuthStates() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		m.mu.Lock()
		for state, as := range m.authStates {
			if now.Sub(as.CreatedAt) > authStateTTL {
				delete(m.authStates, state)
			}
		}
		for serverID, da := range m.deviceAuths {
			if da.ExpiresIn > 0 && now.Sub(da.CreatedAt) > deviceAuthTTL {
				delete(m.deviceAuths, serverID)
			}
		}
		m.mu.Unlock()
	}
}

// InitMemorySets loads all memory sets from the store and creates a memory.Server for each.
func (m *Manager) InitMemorySets() {
	sets, err := m.store.ListMemorySets()
	if err != nil {
		log.Printf("Failed to list memory sets: %v", err)
		return
	}
	m.memoryMu.Lock()
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
		if err := m.store.CreateMemorySet(defaultSet); err != nil {
			log.Printf("Failed to create default memory set: %v", err)
		}
		m.memorySets["default"] = memory.New(m.store, "default", "")
	}
	m.memoryMu.Unlock()
}

// InitSkillSets loads all skill sets from the store and creates a skills.Server for each.
func (m *Manager) InitSkillSets() {
	sets, err := m.store.ListSkillSets()
	if err != nil {
		log.Printf("Failed to list skill sets: %v", err)
		return
	}
	m.skillMu.Lock()
	for _, ss := range sets {
		m.skillSets[ss.ID] = skills.New(m.store, ss.ID, ss.Slug)
	}
	// Ensure default set exists
	if _, ok := m.skillSets["default"]; !ok {
		defaultSet := &models.SkillSet{
			ID:        "default",
			Name:      "Default",
			Slug:      "",
			IsDefault: true,
			CreatedAt: time.Now(),
		}
		if err := m.store.CreateSkillSet(defaultSet); err != nil {
			log.Printf("Failed to create default skill set: %v", err)
		}
		m.skillSets["default"] = skills.New(m.store, "default", "")
	}
	m.skillMu.Unlock()
}

// skillServerID returns the virtual server ID for a skill set.
// "default" → "builtin-skills", other sets → "builtin-skills:{set_id}".
func skillServerID(setID string) string {
	if setID == "default" {
		return models.BuiltinSkillServerID
	}
	return models.BuiltinSkillServerID + ":" + setID
}

// GetSkillServer returns the skill server instance for a given set ID.
func (m *Manager) GetSkillServer(setID string) *skills.Server {
	m.skillMu.RLock()
	defer m.skillMu.RUnlock()
	return m.skillSets[setID]
}

// findSkillServerBySlug returns the skill server with the given slug.
func (m *Manager) findSkillServerBySlug(slug string) *skills.Server {
	m.skillMu.RLock()
	defer m.skillMu.RUnlock()
	for _, srv := range m.skillSets {
		if srv.Slug() == slug {
			return srv
		}
	}
	return nil
}

// ListSkillSets returns all skill sets (exported for API handlers).
func (m *Manager) ListSkillSets() []*models.SkillSet {
	sets, err := m.store.ListSkillSets()
	if err != nil {
		log.Printf("Failed to list skill sets: %v", err)
		return nil
	}
	return sets
}

// IsSkillCompoundMember returns the set IDs of all skill sets that are
// members of the specified compound.
func (m *Manager) IsSkillCompoundMember(compoundID string) []string {
	memberIDs, err := m.store.GetCompoundMemberIDs(compoundID)
	if err != nil {
		return nil
	}
	var setIDs []string
	for _, mid := range memberIDs {
		if mid == models.BuiltinSkillServerID {
			setIDs = append(setIDs, "default")
		} else if strings.HasPrefix(mid, models.BuiltinSkillServerID+":") {
			setID := strings.TrimPrefix(mid, models.BuiltinSkillServerID+":")
			setIDs = append(setIDs, setID)
		}
	}
	return setIDs
}

// getSkillSetIDs returns the skill set IDs accessible in the given scope.
func (m *Manager) getSkillSetIDs(scope Scope) []string {
	if scope.CompoundID != "" {
		return m.IsSkillCompoundMember(scope.CompoundID)
	}
	// Global scope — all skill sets
	m.skillMu.RLock()
	defer m.skillMu.RUnlock()
	var ids []string
	for setID := range m.skillSets {
		ids = append(ids, setID)
	}
	return ids
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
	m.memoryMu.RLock()
	defer m.memoryMu.RUnlock()
	return m.memorySets[setID]
}

// findMemoryServerBySlug returns the memory server with the given slug.
func (m *Manager) findMemoryServerBySlug(slug string) *memory.Server {
	m.memoryMu.RLock()
	defer m.memoryMu.RUnlock()
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
// On failure, it schedules automatic reconnection with exponential backoff.
func (m *Manager) connectServer(srv *models.Server) {
	m.connectServerWithRetry(srv, 0)
}

// connectServerWithRetry connects to a backend server and, on failure,
// schedules a reconnection attempt with exponential backoff.
// retryCount is the number of failed attempts so far (0 for first try).
func (m *Manager) connectServerWithRetry(srv *models.Server, retryCount int) {
	// Check if a connection already exists for this server (e.g., from a
	// concurrent reconnect call). Skip if already connected.
	m.mu.RLock()
	_, exists := m.clients[srv.ID]
	m.mu.RUnlock()
	if exists {
		log.Printf("Server %s: skipping connect — already connected", srv.Name)
		return
	}

	// Determine auth token based on auth method
	authToken := ""

	switch srv.AuthMethod {
	case "bearer":
		// Manual bearer token stored in auth_token field.
		// Supports ${KEY} and $[project:env:var] references — if the token
		// matches a reference pattern, resolve it from the store's env vars.
		authToken = srv.AuthToken
		if authToken != "" && (envVarRefPattern.MatchString(authToken) || projectEnvVarRefPattern.MatchString(authToken)) {
			authToken = resolveTokenRef(authToken, m.store)
			if authToken == "" {
				log.Printf("Server %s: bearer token reference resolved to empty", srv.Name)
			} else {
				log.Printf("Server %s: resolved bearer token reference (%d chars)", srv.Name, len(authToken))
			}
		} else if authToken == "" {
			log.Printf("Server %s: auth_method=bearer but auth_token is empty", srv.Name)
		} else {
			log.Printf("Server %s: using manual bearer token (%d chars)", srv.Name, len(authToken))
		}
	case "env_bearer":
		// Bearer token read from an environment variable or resolved from $[project:env:var]
		if srv.BearerTokenEnv != "" {
			authToken = resolveTokenRef(srv.BearerTokenEnv, m.store)
			if authToken == "" {
				log.Printf("Server %s: env ref %s is empty — token not available", srv.Name, srv.BearerTokenEnv)
			} else {
				log.Printf("Server %s: using bearer token from %s (%d chars)", srv.Name, srv.BearerTokenEnv, len(authToken))
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
							if err := m.store.SaveOAuthTokens(srv.ID, tokens, cid, csec); err != nil {
								log.Printf("Server %s: warning — failed to persist refreshed OAuth token: %v", srv.Name, err)
							}
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

	// Resolve ${KEY} and $[project:env:var] references in the server's env map
	// and headers using decrypted env vars from the store. This allows servers
	// to reference shared secrets (stored in the env_vars table) without
	// hardcoding them.
	serverEnv := srv.Env
	resolvedHeaders := srv.Headers
	if (len(serverEnv) > 0 || len(resolvedHeaders) > 0) {
		hasRefs := false
		for _, v := range serverEnv {
			if envVarRefPattern.MatchString(v) || projectEnvVarRefPattern.MatchString(v) {
				hasRefs = true
				break
			}
		}
		if !hasRefs {
			for _, v := range resolvedHeaders {
				if envVarRefPattern.MatchString(v) || projectEnvVarRefPattern.MatchString(v) {
					hasRefs = true
					break
				}
			}
		}
		if hasRefs {
			refVars, err := m.store.ListEnvVarsDecrypted()
			if err != nil {
				log.Printf("Server %s: failed to load env vars for reference resolution: %v", srv.Name, err)
			}
			groupedVars, err := m.store.ListEnvVarsDecryptedGrouped()
			if err != nil {
				log.Printf("Server %s: failed to load grouped env vars: %v", srv.Name, err)
			}
			if (refVars != nil && len(refVars) > 0) || (groupedVars != nil && len(groupedVars) > 0) {
				serverEnv = resolveEnvRefsWithGrouped(serverEnv, refVars, groupedVars)
				if len(resolvedHeaders) > 0 {
					resolvedHeaders = resolveEnvRefsWithGrouped(resolvedHeaders, refVars, groupedVars)
				}
				log.Printf("Server %s: resolved env var references from %d flat + %d grouped env vars", srv.Name, len(refVars), len(groupedVars))
			}
		}
	}

	cfg := mcp.ClientConfig{
		Transport:      srv.Transport,
		Command:        srv.Command,
		Args:           srv.Args,
		Env:            serverEnv,
		URL:            srv.URL,
		Headers:        resolvedHeaders,
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
		if err := m.store.UpdateServerStatus(srv.ID, "error"); err != nil {
			log.Printf("[Proxy] Warning: failed to update server status to 'error' for %s: %v", srv.ID, err)
		}

		// Schedule reconnection with exponential backoff (max 5 attempts, max 5 min delay)
		if retryCount < 5 {
			backoff := time.Duration(1<<retryCount) * time.Second
			if backoff > 5*time.Minute {
				backoff = 5 * time.Minute
			}
			log.Printf("Server %s: scheduling reconnection attempt %d in %v", srv.Name, retryCount+1, backoff)
			time.AfterFunc(backoff, func() {
				m.connectServerWithRetry(srv, retryCount+1)
			})
		} else {
			log.Printf("Server %s: max reconnection attempts reached — giving up. Use the reconnect button to retry.", srv.Name)
		}
		return
	}

	m.mu.Lock()
	m.clients[srv.ID] = client
	delete(m.errors, srv.ID)
	m.mu.Unlock()

	if err := m.store.UpdateServerStatus(srv.ID, "connected"); err != nil {
		log.Printf("[Proxy] Warning: failed to update server status to 'connected' for %s: %v", srv.ID, err)
	}
	log.Printf("Connected to MCP server: %s (%d tools)", srv.Name, len(client.Tools()))
	// Notify SSE clients that the tool list has changed
	m.fireToolsChanged()
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
		LogsEnabled:     logsEnabled,
		Labels:         req.Labels,
		Tags:           req.Tags,
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
	if req.Labels != nil {
		srv.Labels = *req.Labels
	}
	if req.Tags != nil {
		srv.Tags = *req.Tags
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
	if err := m.store.UpdateServerStatus(id, "disconnected"); err != nil {
		log.Printf("[Proxy] Warning: failed to update server status to 'disconnected' for %s: %v", id, err)
	}
	// Notify SSE clients that the tool list has changed
	m.fireToolsChanged()
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
	AuthKeyID  string // API key ID or OIDC subject — for task auth binding
}

// ListTools returns all tools from all connected servers plus built-in memory tools.
func (m *Manager) ListTools() []models.Tool {
	tools := m.listToolsFiltered(nil)
	// Add memory tools from all memory sets
	m.memoryMu.RLock()
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
	m.memoryMu.RUnlock()
	// Add skill tools from all skill sets
	m.skillMu.RLock()
	for _, srv := range m.skillSets {
		for _, st := range srv.Tools() {
			tools = append(tools, models.Tool{
				ServerID:    skillServerID(srv.SetID()),
				ServerName:  "skills",
				Name:        st.Name,
				Description: st.Description,
			})
		}
	}
	m.skillMu.RUnlock()

	// Add task board tools (built-in, always available)
	for _, tt := range m.taskBoard.Tools() {
		tools = append(tools, models.Tool{
			ServerID:    models.BuiltinTaskBoardServerID,
			ServerName:  "tasks",
			Name:        tt.Name,
			Description: tt.Description,
		})
	}

	return tools
}

// Memory returns the default memory server instance (backward compat).
func (m *Manager) Memory() *memory.Server {
	m.memoryMu.RLock()
	defer m.memoryMu.RUnlock()
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
	// Snapshot clients and their tools under the lock, then release.
	// This avoids holding the lock during DB queries (GetServer).
	type clientInfo struct {
		id     string
		tools  []mcp.Tool
		client *mcp.Client
	}
	m.mu.RLock()
	infos := make([]clientInfo, 0, len(m.clients))
	for id, client := range m.clients {
		if filter != nil && !filter[id] {
			continue
		}
		infos = append(infos, clientInfo{
			id:     id,
			tools:  client.Tools(),
			client: client,
		})
	}
	m.mu.RUnlock()

	var tools []models.Tool
	for _, ci := range infos {
		srv, err := m.store.GetServer(ci.id)
		if err != nil {
			continue
		}
		for _, t := range ci.tools {
			tool := models.Tool{
				ServerID:    ci.id,
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
		return m.handleInitialize(req, scope)
	case "notifications/initialized":
		// No response needed for notifications — the SSE handler already
		// returns 202 for notifications without an ID. Return nil result.
		return nil, nil
	case "notifications/cancelled":
		// Forward cancellation to backend servers
		m.handleCancelledNotification(req, scope)
		return nil, nil
	case "notifications/progress":
		// Progress notifications are fire-and-forget — forward to the
		// appropriate backend. The progressToken in _meta identifies the
		// associated request. We forward to all backends in scope since
		// we don't track which backend owns which progress token.
		m.forwardNotificationToBackends(req, scope)
		return nil, nil
	case "notifications/message":
		// Log message notifications from backends — forward to clients
		// via SSE. These are MAY per spec.
		return nil, nil
	case "notifications/resources/list_changed", "notifications/prompts/list_changed":
		// Forward list-changed notifications from backends to clients.
		// The proxy already broadcasts its own tools/list_changed; these
		// cover resources and prompts from upstream servers.
		m.forwardNotificationToBackends(req, scope)
		return nil, nil
	case "ping":
		// Ping is a keepalive — return an empty result per spec.
		// Also forward pings to backend servers to keep their connections alive.
		m.handlePing(scope)
		return json.Marshal(map[string]interface{}{})
	case "tools/list":
		return m.handleToolsList(req, scope)
	case "tools/call":
		return m.handleToolsCall(ctx, req, scope)
	case "resources/list":
		return m.handleResourcesList(req, scope)
	case "resources/read":
		return m.handleResourcesRead(req, scope)
	case "resources/templates/list":
		return m.handleResourcesTemplatesList(req, scope)
	case "prompts/list":
		return m.handlePromptsList(req)
	case "prompts/get":
		return m.handlePromptsGet(req, scope)
	case "logging/setLevel":
		// Acknowledge logging level change — store it for future log filtering
		return json.Marshal(map[string]interface{}{})
	case "completion/complete":
		return m.handleCompletionComplete(req, scope)
	case "tasks/get":
		return m.handleTasksGet(req, scope)
	case "tasks/result":
		return m.handleTasksResult(ctx, req, scope)
	case "tasks/list":
		return m.handleTasksList(req, scope)
	case "tasks/cancel":
		return m.handleTasksCancel(req, scope)
	default:
		return nil, fmt.Errorf("unsupported method: %s", req.Method)
	}
}

func (m *Manager) handleInitialize(req mcp.JSONRPCRequest, scope Scope) (json.RawMessage, error) {
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
	negotiatedVersion := mcp.NegotiateProtocolVersion(clientVersion)

	// Build instructions: static guidance + auto-injected top memories
	instructions := memoryInstructions

	// Add skill instructions
	instructions += "\n\n" + skillInstructions

	// Add task board instructions
	instructions += "\n\n" + taskBoardInstructions

	// Auto-inject top memories so LLMs see them without calling any tools.
	// Limited to top 5 to avoid bloating the initialize response.
	memorySetIDs := m.getMemorySetIDs(scope)
	if len(memorySetIDs) > 0 {
		var memorySummary strings.Builder
		memorySummary.WriteString("\n\n## Existing Memories (auto-injected — you already know these)\n")
		totalCount := 0
		for _, setID := range memorySetIDs {
			memories, err := m.store.ListMemories(setID, "")
			if err != nil {
				continue
			}
			// Sort by importance (highest first) — simple insertion sort for top N
			maxInject := 5
			topMems := make([]*models.Memory, 0, maxInject)
			for _, mem := range memories {
				if mem.Importance < 60 {
					continue // Skip low-importance memories in init response
				}
				// Insert sorted by importance
				inserted := false
				for i, t := range topMems {
					if mem.Importance > t.Importance {
						topMems = append(topMems, nil)
						copy(topMems[i+1:], topMems[i:])
						topMems[i] = mem
						inserted = true
						break
					}
				}
				if !inserted && len(topMems) < maxInject {
					topMems = append(topMems, mem)
				}
				if len(topMems) > maxInject {
					topMems = topMems[:maxInject]
				}
			}

			for _, mem := range topMems {
				tags := ""
				if len(mem.Tags) > 0 {
					tags = " [" + strings.Join(mem.Tags, ", ") + "]"
				}
				memorySummary.WriteString(fmt.Sprintf("- [%s/%s] (imp:%d) %s%s\n",
					mem.Palace, mem.Room, mem.Importance, mem.Content, tags))
				totalCount++
			}

			// If there are more memories, note it
			if len(memories) > maxInject {
				memorySummary.WriteString(fmt.Sprintf("(... and %d more memories — use memory_recall or memory_search to find them)\n", len(memories)-maxInject))
			}
		}
		if totalCount > 0 {
			instructions += memorySummary.String()
		}
	}

	result := map[string]interface{}{
		"protocolVersion": negotiatedVersion,
		"capabilities": map[string]interface{}{
			"tools":       map[string]interface{}{"listChanged": true},
			"resources":   map[string]interface{}{"listChanged": true},
			"prompts":     map[string]interface{}{"listChanged": true},
			"logging":     map[string]interface{}{},
			"completions": map[string]interface{}{},
			"tasks": map[string]interface{}{
				"list":    map[string]interface{}{},
				"cancel":  map[string]interface{}{},
				"requests": map[string]interface{}{
					"tools": map[string]interface{}{
						"call": map[string]interface{}{},
					},
				},
			},
		},
		"serverInfo": map[string]interface{}{
			"name":        "mcp-proxy",
			"version":     "1.0.0",
			"title":       "MCP Proxy",
			"description": "Aggregates multiple MCP servers, built-in memory, and skills into a single endpoint",
		},
		"instructions": instructions,
	}
	return json.Marshal(result)
}

// memoryInstructions provides guidance to LLM clients on how to use the
// built-in memory tools effectively. Returned in the MCP initialize response.
const memoryInstructions = `This server provides persistent memory tools (memory_store, memory_recall, memory_search, memory_update, memory_delete, memory_reflect). Use them wisely:

## When to store memories
- Durable facts: user preferences, environment details, project conventions, API endpoints, tool quirks
- Important decisions: architectural choices, trade-off rationale, why something was done a certain way
- Hard-won knowledge: debugging insights, non-obvious configurations, gotchas discovered through trial and error
- User corrections: when the user corrects your approach — save what they preferred instead

## When NOT to store memories
- Transient state: task progress, current step, "what I'm doing now"
- Ephemeral context: conversation summaries, session outcomes, temporary TODO state
- Trivial details: anything easily re-discovered (file paths that can be listed, default values, API response shapes)
- Raw data dumps: logs, API responses, command output — extract the signal, not the noise
- Duplicates: search first before storing — update existing memories instead of creating new ones

## How to write good memories
- Be concise: one fact per memory, not paragraphs. A memory should be useful in 1-2 sentences.
- Use tags: add 2-4 relevant tags for searchability (e.g. ["golang", "security", "config"])
- Choose palaces wisely: "projects", "decisions", "learnings", "context", "preferences" — be consistent
- Set importance: 80-100 for critical gotchas/user corrections, 50 for general context, 20-30 for nice-to-know
- Update, don't duplicate: use memory_search before storing. If a similar memory exists, use memory_update.

## Memory lifecycle
1. Before storing: search (memory_search) to check for existing similar memories
2. When recalling: use memory_recall for palace browsing, memory_search for specific lookups
3. Periodically: use memory_reflect to see what's being used and what's stale
4. When facts change: update the existing memory (memory_update) rather than creating a new one`

// skillInstructions provides guidance to LLM clients on how to use the
// built-in skill tools effectively. Returned in the MCP initialize response.
const skillInstructions = `This server also provides skill tools (skill_list, skill_load, skill_search, skill_create, skill_update, skill_delete, skill_search_directory, skill_get_remote). Skills are reusable procedures — documented workflows with steps, commands, and pitfalls.

## When to use skills
- Before starting a task: search (skill_search) or list (skill_list) to check if a procedure exists
- When executing a procedure: load it (skill_load) to get the full content with exact commands
- After learning a new workflow: create a skill (skill_create) so it can be reused next time
- When a procedure has issues: update the skill (skill_update) with new pitfalls or corrected steps

## How skills differ from memories
- Memories store facts (what, why, context) — skills store procedures (how, steps, commands)
- Use memory_search for "what do I know about X" — use skill_search for "how do I do X"
- A skill should contain numbered steps with exact commands, not prose descriptions

## Good skill structure
1. Trigger conditions: when to use this skill
2. Prerequisites: what needs to be set up first
3. Numbered steps with exact commands
4. Pitfalls and common errors
5. Verification steps to confirm success

## Skills.sh directory
- skill_search_directory: Search the global skills.sh registry for community skills (e.g. "react", "deploy", "database")
- skill_get_remote: Fetch the full SKILL.md content of a remote skill using its install_ref (e.g. "vercel-labs/agent-skills@vercel-react-best-practices")
- These tools let you discover and read skills from the broader ecosystem without installing them locally`

// taskBoardInstructions provides guidance to LLM clients on how to use the
// built-in task board tools. Returned in the MCP initialize response.
const taskBoardInstructions = `This server also provides task board tools (task_create, task_list, task_get, task_update, task_delete, task_search). The task board is a persistent kanban-style task list — tasks survive across sessions.

## When to use the task board
- When the user asks you to "create a task", "add a TODO", "track this", "put that on the board"
- When working on multi-step projects: break down work into tasks and track them
- When the user wants to see what's pending: use task_list with status filters
- When updating progress: mark tasks as in_progress/done/blocked

## Task lifecycle
- Statuses: todo → in_progress → done (or blocked)
- Priorities: low, medium, high, urgent
- Use tags for categorization (e.g. ["frontend", "bug"])
- Use assignee to track ownership

## Best practices
- Create tasks proactively when the user mentions future work
- Update status as you make progress
- Search before creating to avoid duplicates
- Keep titles concise — put details in description`

// getMemorySetIDs returns the memory set IDs accessible in the given scope.
func (m *Manager) getMemorySetIDs(scope Scope) []string {
	if scope.CompoundID != "" {
		return m.isMemoryCompoundMember(scope.CompoundID)
	}
	// Global scope — all memory sets
	m.memoryMu.RLock()
	defer m.memoryMu.RUnlock()
	var ids []string
	for setID := range m.memorySets {
		ids = append(ids, setID)
	}
	return ids
}

// handlePing forwards ping requests to backend servers to keep their connections alive.
// The proxy itself responds to the client with an empty result (handled in HandleJSONRPC).
func (m *Manager) handlePing(scope Scope) {
	// Forward ping to all connected servers in scope
	var serverIDs []string
	if scope.ServerID != "" {
		serverIDs = []string{scope.ServerID}
	} else if scope.CompoundID != "" {
		if memberIDs, err := m.store.GetCompoundMemberIDs(scope.CompoundID); err == nil {
			serverIDs = memberIDs
		}
	} else {
		// Global — ping all servers
		m.mu.RLock()
		for id := range m.clients {
			serverIDs = append(serverIDs, id)
		}
		m.mu.RUnlock()
	}

	for _, id := range serverIDs {
		m.mu.RLock()
		client, ok := m.clients[id]
		m.mu.RUnlock()
		if ok {
			go client.Call("ping", nil)
		}
	}
}

// handleCancelledNotification forwards a cancellation notification to backend servers.
// The client sends this when it wants to cancel a long-running request.
func (m *Manager) handleCancelledNotification(req mcp.JSONRPCRequest, scope Scope) {
	// Parse the cancellation params to find which request to cancel
	var params struct {
		RequestID interface{} `json:"requestId"`
	}
	if len(req.Params) > 0 {
		json.Unmarshal(req.Params, &params)
	}
	// Forward the cancellation notification to all backend servers in scope.
	// We don't track which server is handling the request, so we broadcast.
	notif := mcp.JSONRPCNotification{
		JSONRPC: "2.0",
		Method:  "notifications/cancelled",
	}
	if len(req.Params) > 0 {
		notif.Params = req.Params
	}

	var serverIDs []string
	if scope.ServerID != "" {
		serverIDs = []string{scope.ServerID}
	} else if scope.CompoundID != "" {
		if memberIDs, err := m.store.GetCompoundMemberIDs(scope.CompoundID); err == nil {
			serverIDs = memberIDs
		}
	} else {
		m.mu.RLock()
		for id := range m.clients {
			serverIDs = append(serverIDs, id)
		}
		m.mu.RUnlock()
	}

	for _, id := range serverIDs {
		m.mu.RLock()
		client, ok := m.clients[id]
		m.mu.RUnlock()
		if ok {
			// Send as notification (no ID) — best effort, ignore errors
			go func(c *mcp.Client) {
				c.Call("notifications/cancelled", notif.Params)
			}(client)
		}
	}
}

// forwardNotificationToBackends sends a notification to all backend servers
// in the given scope. Used for fire-and-forget notifications like
// notifications/progress, notifications/resources/list_changed, etc.
func (m *Manager) forwardNotificationToBackends(req mcp.JSONRPCRequest, scope Scope) {
	notif := mcp.JSONRPCNotification{
		JSONRPC: "2.0",
		Method:  req.Method,
	}
	if len(req.Params) > 0 {
		notif.Params = req.Params
	}

	var serverIDs []string
	if scope.ServerID != "" {
		serverIDs = []string{scope.ServerID}
	} else if scope.CompoundID != "" {
		if memberIDs, err := m.store.GetCompoundMemberIDs(scope.CompoundID); err == nil {
			serverIDs = memberIDs
		}
	} else {
		m.mu.RLock()
		for id := range m.clients {
			serverIDs = append(serverIDs, id)
		}
		m.mu.RUnlock()
	}

	for _, id := range serverIDs {
		m.mu.RLock()
		client, ok := m.clients[id]
		m.mu.RUnlock()
		if ok {
			go func(c *mcp.Client) {
				c.Call(req.Method, notif.Params)
			}(client)
		}
	}
}

// handleResourcesTemplatesList returns resource templates from backend servers.
// The proxy doesn't expose its own resource templates — it forwards to backends.
func (m *Manager) handleResourcesTemplatesList(req mcp.JSONRPCRequest, scope Scope) (json.RawMessage, error) {
	var serverIDs []string
	if scope.ServerID != "" {
		serverIDs = []string{scope.ServerID}
	} else if scope.CompoundID != "" {
		if memberIDs, err := m.store.GetCompoundMemberIDs(scope.CompoundID); err == nil {
			serverIDs = memberIDs
		}
	} else {
		m.mu.RLock()
		for id := range m.clients {
			serverIDs = append(serverIDs, id)
		}
		m.mu.RUnlock()
	}

	var allTemplates []map[string]interface{}
	for _, id := range serverIDs {
		m.mu.RLock()
		client, ok := m.clients[id]
		m.mu.RUnlock()
		if !ok {
			continue
		}
		result, err := client.Call("resources/templates/list", nil)
		if err != nil {
			continue // Server may not support resources/templates/list
		}
		var tmplResult struct {
			ResourceTemplates []map[string]interface{} `json:"resourceTemplates"`
		}
		if json.Unmarshal(result, &tmplResult) == nil {
			// Prefix template names with server name
			srv, _ := m.store.GetServer(id)
			srvName := "server"
			if srv != nil {
				srvName = srv.Name
			}
			for _, t := range tmplResult.ResourceTemplates {
				t["server"] = srvName
				allTemplates = append(allTemplates, t)
			}
		}
	}

	return json.Marshal(map[string]interface{}{
		"resourceTemplates": allTemplates,
	})
}

// handleCompletionComplete handles completion/complete requests by forwarding
// to the appropriate backend server.
func (m *Manager) handleCompletionComplete(req mcp.JSONRPCRequest, scope Scope) (json.RawMessage, error) {
	var params struct {
		Ref      map[string]interface{} `json:"ref"`
		Argument map[string]interface{} `json:"argument"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, fmt.Errorf("invalid completion params: %w", err)
	}

	// Forward to all backend servers in scope and collect results
	var serverIDs []string
	if scope.ServerID != "" {
		serverIDs = []string{scope.ServerID}
	} else if scope.CompoundID != "" {
		if memberIDs, err := m.store.GetCompoundMemberIDs(scope.CompoundID); err == nil {
			serverIDs = memberIDs
		}
	} else {
		m.mu.RLock()
		for id := range m.clients {
			serverIDs = append(serverIDs, id)
		}
		m.mu.RUnlock()
	}

	for _, id := range serverIDs {
		m.mu.RLock()
		client, ok := m.clients[id]
		m.mu.RUnlock()
		if !ok {
			continue
		}
		result, err := client.Call("completion/complete", req.Params)
		if err == nil {
			return result, nil
		}
	}

	// No server could handle the completion request — return empty
	return json.Marshal(map[string]interface{}{
		"completion": map[string]interface{}{
			"values": []string{},
			"total":  0,
		},
	})
}

// handleResourcesList exposes memories as MCP resources so clients can auto-discover them.
func (m *Manager) handleResourcesList(req mcp.JSONRPCRequest, scope Scope) (json.RawMessage, error) {
	type resource struct {
		URI         string `json:"uri"`
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		MimeType    string `json:"mimeType,omitempty"`
	}

	var resources []resource

	memorySetIDs := m.getMemorySetIDs(scope)
	for _, setID := range memorySetIDs {
		memories, err := m.store.ListMemories(setID, "")
		if err != nil {
			continue
		}
		for _, mem := range memories {
			// Only expose memories with importance >= 30 as resources
			// (avoid flooding the client with low-value memories)
			if mem.Importance < 30 {
				continue
			}
			name := mem.Content
			if len(name) > 60 {
				name = name[:57] + "..."
			}
			resources = append(resources, resource{
				URI:         fmt.Sprintf("memory://%s/%s", mem.Palace, mem.ID),
				Name:        name,
				Description: fmt.Sprintf("[%s/%s] importance:%d", mem.Palace, mem.Room, mem.Importance),
				MimeType:    "text/plain",
			})
		}
	}

	return json.Marshal(map[string]interface{}{
		"resources": resources,
	})
}

// handleResourcesRead returns the content of a specific memory resource.
func (m *Manager) handleResourcesRead(req mcp.JSONRPCRequest, scope Scope) (json.RawMessage, error) {
	var params struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if params.URI == "" {
		return nil, fmt.Errorf("uri is required")
	}

	// Parse memory://palace/mem_id
	var memID string
	if strings.HasPrefix(params.URI, "memory://") {
		parts := strings.SplitN(strings.TrimPrefix(params.URI, "memory://"), "/", 2)
		if len(parts) == 2 {
			memID = parts[1]
		}
	}
	if memID == "" {
		return nil, fmt.Errorf("invalid memory URI: %s", params.URI)
	}

	// Look up the memory across all accessible sets
	memorySetIDs := m.getMemorySetIDs(scope)
	for _, setID := range memorySetIDs {
		mem, err := m.store.GetMemory(memID)
		if err != nil || mem == nil {
			continue
		}
		// Verify the memory belongs to this set
		if mem.SetID != setID {
			continue
		}
		_ = m.store.TouchMemory(mem.ID) // increment access count

		// Build the text content
		text := fmt.Sprintf("Palace: %s\nRoom: %s\nImportance: %d\nTags: %s\nCreated: %s\n\n%s",
			mem.Palace, mem.Room, mem.Importance,
			strings.Join(mem.Tags, ", "),
			mem.CreatedAt.Format(time.RFC3339),
			mem.Content,
		)

		return json.Marshal(map[string]interface{}{
			"contents": []map[string]interface{}{
				{
					"uri":      params.URI,
					"mimeType": "text/plain",
					"text":     text,
				},
			},
		})
	}

	return nil, fmt.Errorf("memory not found: %s", params.URI)
}

// handlePromptsList exposes memory-related prompts for MCP clients.
func (m *Manager) handlePromptsList(req mcp.JSONRPCRequest) (json.RawMessage, error) {
	prompts := []map[string]interface{}{
		{
			"name":        "recall_context",
			"description": "Recall all relevant memories for a given topic or task. Auto-searches by keyword and returns matching memories with context.",
			"arguments": []map[string]interface{}{
				{
					"name":        "topic",
					"description": "The topic, project name, or keyword to search for",
					"required":    true,
				},
			},
		},
		{
			"name":        "memory_overview",
			"description": "Get an overview of all stored memories — palace distribution, recent additions, and most-accessed entries. Useful for understanding what context is available.",
			"arguments":   []map[string]interface{}{},
		},
	}
	return json.Marshal(map[string]interface{}{"prompts": prompts})
}

// handlePromptsGet returns the content of a memory prompt.
func (m *Manager) handlePromptsGet(req mcp.JSONRPCRequest, scope Scope) (json.RawMessage, error) {
	var params struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	switch params.Name {
	case "recall_context":
		topic := params.Arguments["topic"]
		if topic == "" {
			return nil, fmt.Errorf("topic argument is required")
		}

		memorySetIDs := m.getMemorySetIDs(scope)
		var allResults []*models.Memory
		for _, setID := range memorySetIDs {
			results, err := m.store.SearchMemories(setID, topic)
			if err != nil {
				continue
			}
			allResults = append(allResults, results...)
		}

		var text strings.Builder
		text.WriteString(fmt.Sprintf("Relevant memories for '%s':\n\n", topic))
		if len(allResults) == 0 {
			text.WriteString("No memories found for this topic.\n")
			text.WriteString("This means there is no prior context stored. You may want to store new memories as you learn.\n")
		} else {
			for _, mem := range allResults {
				tags := ""
				if len(mem.Tags) > 0 {
					tags = " [tags: " + strings.Join(mem.Tags, ", ") + "]"
				}
				text.WriteString(fmt.Sprintf("- [%s/%s] (importance:%d) %s%s\n", mem.Palace, mem.Room, mem.Importance, mem.Content, tags))
			}
		}

		return json.Marshal(map[string]interface{}{
			"messages": []map[string]interface{}{
				{
					"role": "user",
					"content": map[string]interface{}{
						"type": "text",
						"text": text.String(),
					},
				},
			},
		})

	case "memory_overview":
		memorySetIDs := m.getMemorySetIDs(scope)
		var text strings.Builder
		text.WriteString("Memory Overview:\n\n")

		for _, setID := range memorySetIDs {
			palaces, err := m.store.ListPalaces(setID)
			if err != nil {
				continue
			}
			allMemories, err := m.store.ListMemories(setID, "")
			if err != nil {
				continue
			}
			text.WriteString(fmt.Sprintf("Total memories: %d\n\nPalaces:\n", len(allMemories)))
			for _, p := range palaces {
				text.WriteString(fmt.Sprintf("  - %s (%d memories)\n", p["palace"], p["count"]))
			}

			// Recent memories
			text.WriteString("\nMost recent:\n")
			recent := 5
			if len(allMemories) < recent {
				recent = len(allMemories)
			}
			for i := len(allMemories) - 1; i >= 0 && i >= len(allMemories)-recent; i-- {
				mem := allMemories[i]
				text.WriteString(fmt.Sprintf("  - [%s] %s\n", mem.Palace, mem.Content))
			}
		}

		return json.Marshal(map[string]interface{}{
			"messages": []map[string]interface{}{
				{
					"role": "user",
					"content": map[string]interface{}{
						"type": "text",
						"text": text.String(),
					},
				},
			},
		})

	default:
		return nil, fmt.Errorf("unknown prompt: %s", params.Name)
	}
}

func (m *Manager) handleToolsList(req mcp.JSONRPCRequest, scope Scope) (json.RawMessage, error) {
	// Parse pagination cursor from params
	var params struct {
		Cursor string `json:"cursor"`
	}
	if len(req.Params) > 0 {
		json.Unmarshal(req.Params, &params)
	}
	// Cursor is an opaque token encoding the offset (base64 of the offset number)
	offset := 0
	if params.Cursor != "" {
		if decoded, err := base64.StdEncoding.DecodeString(params.Cursor); err == nil {
			fmt.Sscanf(string(decoded), "%d", &offset)
		}
	}
	// Page size — large enough that most clients get everything in one page
	const pageSize = 200

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
						Title:       "Dictionary (lazy tool discovery)",
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
			Title:       fmt.Sprintf("%s (%s)", t.Name, t.ServerName),
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
		m.memoryMu.RLock()
		for setID := range m.memorySets {
			memorySetIDs = append(memorySetIDs, setID)
		}
		m.memoryMu.RUnlock()
	}
	for _, setID := range memorySetIDs {
		srv := m.GetMemoryServer(setID)
		if srv != nil {
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
					Title:       mt.Title,
					Description: fmt.Sprintf("[memory%s] %s", setSuffix, mt.Description),
				}
				if len(mt.InputSchema) > 0 {
					tool.InputSchema = mt.InputSchema
				}
				mcpTools = append(mcpTools, tool)
			}
		}
	}

	// Add built-in skill tools from all skill sets
	var skillSetIDs []string
	if scope.CompoundID != "" {
		skillSetIDs = m.IsSkillCompoundMember(scope.CompoundID)
	} else {
		m.skillMu.RLock()
		for setID := range m.skillSets {
			skillSetIDs = append(skillSetIDs, setID)
		}
		m.skillMu.RUnlock()
	}
	for _, setID := range skillSetIDs {
		srv := m.GetSkillServer(setID)
		if srv != nil {
			setSuffix := ""
			if srv.Slug() != "" {
				if ss, err := m.store.GetSkillSet(setID); err == nil && ss.Name != "" {
					setSuffix = fmt.Sprintf(" [%s]", ss.Name)
				} else {
					setSuffix = fmt.Sprintf(" [%s]", srv.Slug())
				}
			}
			for _, st := range srv.Tools() {
				tool := mcp.Tool{
					Name:        srv.NamespacedName(st.Name),
					Title:       st.Title,
					Description: fmt.Sprintf("[skills%s] %s", setSuffix, st.Description),
				}
				if len(st.InputSchema) > 0 {
					tool.InputSchema = st.InputSchema
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

	// Apply pagination
	totalTools := len(mcpTools)
	if offset >= totalTools {
		mcpTools = []mcp.Tool{}
	} else {
		end := offset + pageSize
		if end > totalTools {
			end = totalTools
		}
		mcpTools = mcpTools[offset:end]
	}

	result := mcp.ToolListResult{Tools: mcpTools}
	// Set nextCursor if there are more pages
	if offset+pageSize < totalTools {
		nextOffset := offset + pageSize
		cursor := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d", nextOffset)))
		result.NextCursor = cursor
	}
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
		Task      *struct {
			TTL int64 `json:"ttl"`
		} `json:"task,omitempty"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, fmt.Errorf("invalid tools/call params: %w", err)
	}

	// Check if this is a task-augmented request
	if params.Task != nil {
		return m.handleTaskAugmentedToolCall(ctx, req, scope, params.Name, params.Arguments, params.Task.TTL)
	}

	// Check if this is a task board tool (built-in)
	if toolName, ok := taskboard.ParseNamespaced(params.Name); ok {
		return m.taskBoard.HandleToolCall(toolName, params.Arguments)
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

	// Check if this is a built-in skill tool
	if slug, baseName, ok := skills.ParseNamespaced(params.Name); ok {
		skillSrv := m.findSkillServerBySlug(slug)
		if skillSrv == nil {
			return nil, fmt.Errorf("skill set not found for slug: %s", slug)
		}
		// For compound scope, verify this skill set is a compound member
		if scope.CompoundID != "" {
			memberSetIDs := m.IsSkillCompoundMember(scope.CompoundID)
			allowed := false
			for _, sid := range memberSetIDs {
				if sid == skillSrv.SetID() {
					allowed = true
					break
				}
			}
			if !allowed {
				return nil, fmt.Errorf("skill tools are not available in this compound — add the skill server as a member")
			}
		}
		return skillSrv.HandleToolCall(baseName, params.Arguments)
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

// --- Task handlers (experimental, 2025-11-25 spec) ---

// handleTaskAugmentedToolCall handles a tools/call request with task augmentation.
// It creates a task in "working" status, executes the tool asynchronously, and
// returns a CreateTaskResult immediately. The result can be retrieved via tasks/result.
func (m *Manager) handleTaskAugmentedToolCall(ctx context.Context, req mcp.JSONRPCRequest, scope Scope, toolName string, arguments json.RawMessage, ttlMS int64) (json.RawMessage, error) {
	// Create the task bound to the caller's auth context
	task := m.taskMgr.CreateTask(scope.AuthKeyID, ttlMS)

	// Copy the request params WITHOUT the task field for the backend call
	backendParams, _ := json.Marshal(map[string]interface{}{
		"name":      toolName,
		"arguments": arguments,
	})
	backendReq := mcp.JSONRPCRequest{
		JSONRPC: req.JSONRPC,
		ID:      req.ID,
		Method:  "tools/call",
		Params:  backendParams,
	}

	// Execute the tool call asynchronously
	go func() {
		result, err := m.handleToolsCall(ctx, backendReq, scope)
		if err != nil {
			m.taskMgr.FailTask(task.TaskID, err)
		} else {
			m.taskMgr.CompleteTask(task.TaskID, result)
		}
	}()

	// Return CreateTaskResult immediately
	return m.marshalTaskResult(task.TaskID)
}

// handleTasksGet returns the current status of a task.
func (m *Manager) handleTasksGet(req mcp.JSONRPCRequest, scope Scope) (json.RawMessage, error) {
	var params struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, fmt.Errorf("invalid tasks/get params: %w", err)
	}
	if params.TaskID == "" {
		return nil, &mcp.RPCError{Code: mcp.ErrCodeInvalidParams, Message: "missing taskId"}
	}

	task, err := m.taskMgr.Get(params.TaskID, scope.AuthKeyID)
	if err != nil {
		return nil, &mcp.RPCError{Code: mcp.ErrCodeInvalidParams, Message: err.Error()}
	}

	return m.marshalTask(task)
}

// handleTasksResult blocks until the task reaches a terminal state, then returns the result.
func (m *Manager) handleTasksResult(ctx context.Context, req mcp.JSONRPCRequest, scope Scope) (json.RawMessage, error) {
	var params struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, fmt.Errorf("invalid tasks/result params: %w", err)
	}
	if params.TaskID == "" {
		return nil, &mcp.RPCError{Code: mcp.ErrCodeInvalidParams, Message: "missing taskId"}
	}

	// Verify the task exists and belongs to this auth context
	_, err := m.taskMgr.Get(params.TaskID, scope.AuthKeyID)
	if err != nil {
		return nil, &mcp.RPCError{Code: mcp.ErrCodeInvalidParams, Message: err.Error()}
	}

	// Block until terminal or context cancelled
	result, err := m.taskMgr.WaitForResult(ctx, params.TaskID, scope.AuthKeyID)
	if err != nil {
		return nil, &mcp.RPCError{Code: mcp.ErrCodeInternalError, Message: err.Error()}
	}

	// Return the result with related-task metadata in _meta
	resultMap := map[string]interface{}{}
	if result != nil {
		json.Unmarshal(result, &resultMap)
	}
	// Add _meta with related-task per spec
	if meta, ok := resultMap["_meta"].(map[string]interface{}); ok {
		meta["io.modelcontextprotocol/related-task"] = map[string]string{"taskId": params.TaskID}
		resultMap["_meta"] = meta
	} else {
		resultMap["_meta"] = map[string]interface{}{
			"io.modelcontextprotocol/related-task": map[string]string{"taskId": params.TaskID},
		}
	}
	return json.Marshal(resultMap)
}

// handleTasksList lists tasks for the current auth context with pagination.
func (m *Manager) handleTasksList(req mcp.JSONRPCRequest, scope Scope) (json.RawMessage, error) {
	var params struct {
		Cursor string `json:"cursor,omitempty"`
	}
	if len(req.Params) > 0 {
		json.Unmarshal(req.Params, &params)
	}

	tasks, nextCursor, err := m.taskMgr.ListTasks(scope.AuthKeyID, params.Cursor, 50)
	if err != nil {
		return nil, &mcp.RPCError{Code: mcp.ErrCodeInternalError, Message: err.Error()}
	}

	taskList := make([]map[string]interface{}, 0, len(tasks))
	for _, t := range tasks {
		taskList = append(taskList, m.taskToMap(t))
	}

	result := map[string]interface{}{
		"tasks": taskList,
	}
	if nextCursor != "" {
		result["nextCursor"] = nextCursor
	}
	return json.Marshal(result)
}

// handleTasksCancel cancels a task by transitioning it to "cancelled" status.
func (m *Manager) handleTasksCancel(req mcp.JSONRPCRequest, scope Scope) (json.RawMessage, error) {
	var params struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, fmt.Errorf("invalid tasks/cancel params: %w", err)
	}
	if params.TaskID == "" {
		return nil, &mcp.RPCError{Code: mcp.ErrCodeInvalidParams, Message: "missing taskId"}
	}

	task, err := m.taskMgr.CancelTask(params.TaskID, scope.AuthKeyID)
	if err != nil {
		return nil, &mcp.RPCError{Code: mcp.ErrCodeInvalidParams, Message: err.Error()}
	}

	return m.marshalTask(task)
}

// marshalTaskResult creates a CreateTaskResult JSON response from a task ID.
func (m *Manager) marshalTaskResult(taskID string) (json.RawMessage, error) {
	task, err := m.taskMgr.Get(taskID, "")
	if err != nil {
		return nil, err
	}
	return m.marshalTask(task)
}

// marshalTask serializes a task to JSON.
func (m *Manager) marshalTask(t *tasks.Task) (json.RawMessage, error) {
	return json.Marshal(m.taskToMap(t))
}

// taskToMap converts a task to a map for JSON serialization.
func (m *Manager) taskToMap(t *tasks.Task) map[string]interface{} {
	taskMap := map[string]interface{}{
		"taskId":        t.TaskID,
		"status":        t.Status,
		"createdAt":     t.CreatedAt.UTC().Format(time.RFC3339),
		"lastUpdatedAt": t.LastUpdatedAt.UTC().Format(time.RFC3339),
	}
	if t.StatusMessage != "" {
		taskMap["statusMessage"] = t.StatusMessage
	}
	if t.TTL != nil {
		taskMap["ttl"] = t.TTL.Milliseconds()
	} else {
		taskMap["ttl"] = nil
	}
	if t.PollInterval != nil {
		taskMap["pollInterval"] = t.PollInterval.Milliseconds()
	}
	return taskMap
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

// oauthMetaEntry wraps cached OAuth metadata with a timestamp for TTL expiry.
type oauthMetaEntry struct {
	meta      *mcp.OAuthServerMetadata
	cachedAt  time.Time
}

// oauthMetaCacheTTL is the maximum lifetime of cached OAuth metadata.
const oauthMetaCacheTTL = 30 * time.Minute

// GetOAuthMetadata returns cached OAuth metadata for a server, discovering it
// on first access and caching the result. Cache entries expire after 30 minutes
// to ensure stale metadata doesn't cause auth failures when upstream changes.
func (m *Manager) GetOAuthMetadata(serverID string) *mcp.OAuthServerMetadata {
	// Check cache first
	m.oauthMetaMu.RLock()
	if entry, ok := m.oauthMetaCache[serverID]; ok && time.Since(entry.cachedAt) < oauthMetaCacheTTL {
		m.oauthMetaMu.RUnlock()
		return entry.meta
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
	m.oauthMetaCache[serverID] = &oauthMetaEntry{meta: meta, cachedAt: time.Now()}
	m.oauthMetaMu.Unlock()

	return meta
}

// InvalidateOAuthMetadataCache removes cached OAuth metadata for a server.
func (m *Manager) InvalidateOAuthMetadataCache(serverID string) {
	m.oauthMetaMu.Lock()
	delete(m.oauthMetaCache, serverID)
	m.oauthMetaMu.Unlock()
}

// oauthMetaEntry and oauthMetaCacheTTL are defined above with GetOAuthMetadata.

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
	CreatedAt       time.Time `json:"-"`
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
		CreatedAt:       time.Now(),
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
	m.logMu.Lock()
	sl, ok := m.serverLogs[serverID]
	if !ok {
		sl = newServerLog(maxLogLines)
		m.serverLogs[serverID] = sl
	}
	m.logMu.Unlock()
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
			srv := m.GetMemoryServer(setID)
			if srv != nil {
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
				srv := m.GetMemoryServer(setID)
				if srv != nil && srv.Slug() == slug {
					for _, mt := range srv.Tools() {
						if mt.Name == baseName {
							return wrapMCPContent(map[string]interface{}{
								"name":        params.Tool,
								"server":      "memory",
								"type":        "memory",
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
					"name":        name,
					"server":      t.ServerName,
					"type":        "server",
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
				srv := m.GetMemoryServer(setID)
				if srv != nil && srv.Slug() == slug {
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
			srv := m.GetMemoryServer(setID)
			if srv != nil {
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

// wrapMCPContent wraps a successful result in MCP content format.
// Per spec, tool results MUST include isError: false.
func wrapMCPContent(result interface{}) (json.RawMessage, error) {
	textBytes, _ := json.Marshal(result)
	return json.Marshal(map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": string(textBytes),
			},
		},
		"isError": false,
	})
}

// wrapMCPError wraps a tool execution error in MCP content format.
// Per spec, tool execution errors are returned with isError: true (not as JSON-RPC errors).
// This allows the LLM to self-correct and retry with adjusted parameters.
func wrapMCPError(message string) (json.RawMessage, error) {
	return json.Marshal(map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": message,
			},
		},
		"isError": true,
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

	m.memoryMu.Lock()
	m.memorySets[ms.ID] = memory.New(m.store, ms.ID, ms.Slug)
	m.memoryMu.Unlock()
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
		m.memoryMu.Lock()
		m.memorySets[ms.ID] = memory.New(m.store, ms.ID, ms.Slug)
		m.memoryMu.Unlock()
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
	m.memoryMu.Lock()
	delete(m.memorySets, id)
	m.memoryMu.Unlock()
	return nil
}
