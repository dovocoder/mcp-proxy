package api

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/agentic/mcp-proxy/internal/auth"
	"github.com/agentic/mcp-proxy/internal/crypto"
	"github.com/agentic/mcp-proxy/internal/mcp"
	"github.com/agentic/mcp-proxy/internal/memory"
	"github.com/agentic/mcp-proxy/internal/models"
	"github.com/agentic/mcp-proxy/internal/proxy"
	"github.com/agentic/mcp-proxy/internal/store"
	"github.com/google/uuid"
)

// Handlers holds all HTTP handler dependencies.
type Handlers struct {
	store             *store.Store
	proxy             *proxy.Manager
	auth              *auth.AuthService
	sseManager        *sseSessionManager
	streamManager     *streamSessionManager
	masterKey         [32]byte
	adminLoginEnabled bool
}

// New creates a new API Handlers instance.
func New(s *store.Store, p *proxy.Manager, a *auth.AuthService, adminLoginEnabled bool) *Handlers {
	// Derive master key for at-rest env var encryption.
	// Prefer ENCRYPTION_KEY, fall back to the JWT secret.
	encKey := os.Getenv("ENCRYPTION_KEY")
	if encKey == "" {
		encKey = a.JWTSecret()
	}
	return &Handlers{
		store:             s,
		proxy:             p,
		auth:              a,
		sseManager:        newSSESessionManager(),
		streamManager:     newStreamSessionManager(),
		masterKey:         crypto.DeriveKey(encKey),
		adminLoginEnabled: adminLoginEnabled,
	}
}

// SetupRoutes registers all API routes on the given mux.
func (h *Handlers) SetupRoutes(mux *http.ServeMux) {
	// Health check (no auth — used by load balancers / Dokploy / Traefik)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// Auth routes (no auth required)
	mux.Handle("POST /api/auth/login", h.auth.LoginRateLimitMiddleware(http.HandlerFunc(h.handleLogin)))

	// Registry search (no auth — public catalog)
	mux.HandleFunc("GET /api/registry/search", h.handleRegistrySearch)

	// OIDC routes (no auth — browser redirect flow for admin UI)
	mux.HandleFunc("GET /api/auth/oidc/status", h.handleOIDCStatus)
	mux.HandleFunc("GET /api/auth/oidc/login", h.handleOIDCLogin)
	mux.HandleFunc("GET /api/auth/oidc/callback", h.handleOIDCCallback)

	// Protected Resource Metadata (RFC 9728) — for MCP client OAuth discovery
	// Root: /.well-known/oauth-protected-resource
	// Path-insertion: /.well-known/oauth-protected-resource/api/compounds/{id}/mcp
	// The path-insertion variant is tried FIRST by MCP clients (per RFC 9728 §5.1).
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", h.handleProtectedResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/", h.handleProtectedResourceMetadata)

	// Authorization Server Metadata (RFC 8414) — ALL endpoints proxied through the proxy
	// Also serve path-insertion variant for clients that try it.
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", h.handleAuthorizationServerMetadata)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server/", h.handleAuthorizationServerMetadata)

	// OIDC Discovery 1.0 — same metadata, for clients that prefer OIDC format
	mux.HandleFunc("GET /.well-known/openid-configuration", h.handleAuthorizationServerMetadata)

	// Client ID Metadata Document (CIMD) — public endpoint fetched by authorization servers
	mux.HandleFunc("GET /api/oauth/client-metadata", h.handleClientMetadata)

	// Dynamic Client Registration (RFC 7591) — returns pre-registered OIDC credentials
	mux.HandleFunc("POST /api/oauth/register", h.handleOAuthRegister)

	// OAuth proxy endpoints — all on the proxy's domain so issuer matches endpoints.
	// These forward requests to the upstream OIDC provider (PocketID).
	mux.HandleFunc("GET /api/oauth/authorize", h.handleOAuthAuthorize)
	mux.HandleFunc("GET /api/oauth/callback", h.handleOAuthCallback)
	mux.HandleFunc("POST /api/oauth/callback", h.handleOAuthCallback)
	mux.HandleFunc("POST /api/oauth/token", h.handleOAuthProxy)
	mux.HandleFunc("GET /api/oauth/jwks", h.handleOAuthProxy)
	mux.HandleFunc("GET /api/oauth/userinfo", h.handleOAuthProxy)
	mux.HandleFunc("POST /api/oauth/userinfo", h.handleOAuthProxy)
	mux.HandleFunc("POST /api/oauth/revoke", h.handleOAuthProxy)
	mux.HandleFunc("POST /api/oauth/introspect", h.handleOAuthProxy)

	// --- MCP client endpoints (API key auth) ---
	// Global (all servers)
	mux.Handle("POST /api/mcp", h.auth.APIKeyMiddleware(http.HandlerFunc(h.handleMCPProxyGlobal)))
	mux.Handle("GET /api/mcp", h.auth.APIKeyMiddleware(http.HandlerFunc(h.handleMCPProxyGlobal)))
	mux.Handle("DELETE /api/mcp", h.auth.APIKeyMiddleware(http.HandlerFunc(h.handleMCPProxyGlobal)))
	mux.Handle("GET /api/sse", h.auth.APIKeyMiddleware(http.HandlerFunc(h.handleSSEConnectGlobal)))
	mux.Handle("POST /api/messages", h.auth.APIKeyMiddleware(http.HandlerFunc(h.handleSSEMessageGlobal)))

	// Per-server
	mux.Handle("POST /api/servers/{id}/mcp", h.auth.APIKeyMiddleware(http.HandlerFunc(h.handleMCPProxyServer)))
	mux.Handle("GET /api/servers/{id}/mcp", h.auth.APIKeyMiddleware(http.HandlerFunc(h.handleMCPProxyServer)))
	mux.Handle("DELETE /api/servers/{id}/mcp", h.auth.APIKeyMiddleware(http.HandlerFunc(h.handleMCPProxyServer)))
	mux.Handle("GET /api/servers/{id}/sse", h.auth.APIKeyMiddleware(http.HandlerFunc(h.handleSSEConnectServer)))
	mux.Handle("POST /api/servers/{id}/messages", h.auth.APIKeyMiddleware(http.HandlerFunc(h.handleSSEMessageServer)))

	// Per-compound
	mux.Handle("POST /api/compounds/{id}/mcp", h.auth.APIKeyMiddleware(http.HandlerFunc(h.handleMCPProxyCompound)))
	mux.Handle("GET /api/compounds/{id}/mcp", h.auth.APIKeyMiddleware(http.HandlerFunc(h.handleMCPProxyCompound)))
	mux.Handle("DELETE /api/compounds/{id}/mcp", h.auth.APIKeyMiddleware(http.HandlerFunc(h.handleMCPProxyCompound)))
	mux.Handle("GET /api/compounds/{id}/sse", h.auth.APIKeyMiddleware(http.HandlerFunc(h.handleSSEConnectCompound)))
	mux.Handle("POST /api/compounds/{id}/messages", h.auth.APIKeyMiddleware(http.HandlerFunc(h.handleSSEMessageCompound)))

	// Admin routes (JWT auth)
	adminMux := http.NewServeMux()
	adminMux.HandleFunc("GET /api/servers", h.handleListServers)
	adminMux.HandleFunc("POST /api/servers", h.handleCreateServer)
	adminMux.HandleFunc("GET /api/servers/{id}", h.handleGetServer)
	adminMux.HandleFunc("PUT /api/servers/{id}", h.handleUpdateServer)
	adminMux.HandleFunc("DELETE /api/servers/{id}", h.handleDeleteServer)
	adminMux.HandleFunc("POST /api/servers/{id}/reconnect", h.handleReconnectServer)
	adminMux.HandleFunc("GET /api/servers/{id}/logs", h.handleGetServerLogs)
	adminMux.HandleFunc("DELETE /api/servers/{id}/logs", h.handleClearServerLogs)
	adminMux.HandleFunc("PATCH /api/servers/{id}/logs-enabled", h.handleToggleLogsEnabled)
	adminMux.HandleFunc("POST /api/servers/{id}/auth", h.handleInitiateAuth)
	adminMux.HandleFunc("GET /api/servers/{id}/auth-status", h.handleAuthStatus)
	adminMux.HandleFunc("POST /api/servers/{id}/bearer-token", h.handleSetBearerToken)
	adminMux.HandleFunc("POST /api/servers/{id}/device-auth", h.handleInitiateDeviceAuth)
	adminMux.HandleFunc("POST /api/servers/{id}/device-auth/poll", h.handlePollDeviceAuth)
	adminMux.HandleFunc("DELETE /api/servers/{id}/device-auth", h.handleCancelDeviceAuth)
	adminMux.HandleFunc("GET /api/servers/{id}/registration", h.handleGetRegistration)
	adminMux.HandleFunc("DELETE /api/servers/{id}/registration", h.handleDeleteRegistration)

	adminMux.HandleFunc("GET /api/keys", h.handleListAPIKeys)
	adminMux.HandleFunc("POST /api/keys", h.handleCreateAPIKey)
	adminMux.HandleFunc("DELETE /api/keys/{id}", h.handleDeleteAPIKey)

	adminMux.HandleFunc("GET /api/tools", h.handleListTools)
	adminMux.HandleFunc("GET /api/dashboard", h.handleDashboard)

	// Compound server routes
	adminMux.HandleFunc("GET /api/compounds", h.handleListCompounds)
	adminMux.HandleFunc("POST /api/compounds", h.handleCreateCompound)
	adminMux.HandleFunc("GET /api/compounds/{id}", h.handleGetCompound)
	adminMux.HandleFunc("PUT /api/compounds/{id}", h.handleUpdateCompound)
	adminMux.HandleFunc("DELETE /api/compounds/{id}", h.handleDeleteCompound)
	adminMux.HandleFunc("POST /api/compounds/{id}/members/{serverId}", h.handleAddCompoundMember)
	adminMux.HandleFunc("DELETE /api/compounds/{id}/members/{serverId}", h.handleRemoveCompoundMember)

	// Memory routes
	adminMux.HandleFunc("GET /api/memories", h.handleListMemories)
	adminMux.HandleFunc("POST /api/memories", h.handleCreateMemory)
	adminMux.HandleFunc("GET /api/memories/{id}", h.handleGetMemory)
	adminMux.HandleFunc("PUT /api/memories/{id}", h.handleUpdateMemory)
	adminMux.HandleFunc("DELETE /api/memories/{id}", h.handleDeleteMemory)
	adminMux.HandleFunc("GET /api/memories/palaces", h.handleListPalaces)
	adminMux.HandleFunc("GET /api/memories/search", h.handleSearchMemories)

	// Memory set routes
	adminMux.HandleFunc("GET /api/memory-sets", h.handleListMemorySets)
	adminMux.HandleFunc("POST /api/memory-sets", h.handleCreateMemorySet)
	adminMux.HandleFunc("PATCH /api/memory-sets/{id}", h.handleUpdateMemorySet)
	adminMux.HandleFunc("DELETE /api/memory-sets/{id}", h.handleDeleteMemorySet)

	// Env var routes (admin — JWT auth)
	// Register more specific paths first for safety.
	adminMux.HandleFunc("GET /api/env-vars/projects", h.handleListEnvVarProjects)
	adminMux.HandleFunc("GET /api/env-vars/environments", h.handleListEnvVarEnvironments)
	adminMux.HandleFunc("GET /api/env-vars", h.handleListEnvVars)
	adminMux.HandleFunc("POST /api/env-vars", h.handleCreateEnvVar)
	adminMux.HandleFunc("PUT /api/env-vars/{id}", h.handleUpdateEnvVar)
	adminMux.HandleFunc("DELETE /api/env-vars/{id}", h.handleDeleteEnvVar)

	// Disabled tools routes (admin — JWT auth)
	adminMux.HandleFunc("GET /api/disabled-tools", h.handleListDisabledTools)
	adminMux.HandleFunc("POST /api/disabled-tools", h.handleCreateDisabledTool)
	adminMux.HandleFunc("DELETE /api/disabled-tools/{id}", h.handleDeleteDisabledTool)

	// Env var export (API key auth)
	mux.Handle("GET /api/env-vars/export", h.auth.APIKeyMiddleware(http.HandlerFunc(h.handleExportEnvVars)))

	mux.Handle("/api/", h.auth.JWTMiddleware(adminMux))
}

// --- Auth ---

func (h *Handlers) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !h.adminLoginEnabled {
		writeError(w, http.StatusForbidden, "Password login is disabled. Use SSO.")
		return
	}

	var req models.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	user, err := h.store.GetUserByUsername(req.Username)
	if err != nil || user == nil {
		writeError(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}

	if !auth.VerifyPassword(user.PasswordHash, req.Password) {
		writeError(w, http.StatusUnauthorized, "Invalid credentials")
		return
	}

	token, expiresAt, err := h.auth.GenerateToken(user.ID, user.Username, user.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	writeJSON(w, http.StatusOK, models.TokenResponse{
		Token:     token,
		ExpiresAt: expiresAt,
	})
}

// --- MCP Proxy (Streamable HTTP) ---

// extractScopeFromAPIKey builds a Scope from the API key's compound_id (if any).
func extractScopeFromAPIKey(r *http.Request) proxy.Scope {
	if apiKey, ok := auth.APIKeyFromContext(r.Context()).(*models.APIKey); ok && apiKey != nil {
		if apiKey.CompoundID != nil {
			return proxy.Scope{CompoundID: *apiKey.CompoundID}
		}
	}
	return proxy.Scope{}
}

// handleMCPProxyGlobal handles POST/GET/DELETE /api/mcp (global scope — all servers).
func (h *Handlers) handleMCPProxyGlobal(w http.ResponseWriter, r *http.Request) {
	scope := extractScopeFromAPIKey(r)
	h.handleStreamableHTTP(w, r, scope)
}

// handleMCPProxyServer handles POST/GET/DELETE /api/servers/{id}/mcp (single server scope).
func (h *Handlers) handleMCPProxyServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := h.store.GetServer(id); err != nil {
		writeError(w, http.StatusNotFound, "Server not found")
		return
	}
	h.handleStreamableHTTP(w, r, proxy.Scope{ServerID: id})
}

// handleMCPProxyCompound handles POST/GET/DELETE /api/compounds/{id}/mcp (compound scope).
func (h *Handlers) handleMCPProxyCompound(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := h.store.GetCompound(id); err != nil {
		writeError(w, http.StatusNotFound, "Compound not found")
		return
	}
	h.handleStreamableHTTP(w, r, proxy.Scope{CompoundID: id})
}

// --- Servers CRUD ---

func (h *Handlers) handleListServers(w http.ResponseWriter, r *http.Request) {
	servers, err := h.store.ListServers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list servers")
		return
	}

	// Enrich with live status
	type serverWithStatus struct {
		*models.Server
		ToolsCount int    `json:"tools_count"`
		LiveError  string `json:"live_error,omitempty"`
	}

	var result []serverWithStatus

	// Prepend all memory set servers as virtual builtin servers
	memSets, _ := h.proxy.ListMemorySets()
	for _, ms := range memSets {
		sid := ms.ID
		if sid == "default" {
			sid = models.BuiltinMemoryServerID
		} else {
			sid = models.BuiltinMemoryServerID + ":" + ms.ID
		}
		srv := &models.Server{
			ID:        sid,
			Name:      ms.Name,
			Transport: "builtin",
			Enabled:   true,
			IsBuiltin: true,
			Status:    "connected",
		}
		toolsCount := 0
		if memSrv := h.proxy.GetMemoryServer(ms.ID); memSrv != nil {
			toolsCount = len(memSrv.Tools())
		}
		result = append(result, serverWithStatus{
			Server:     srv,
			ToolsCount: toolsCount,
		})
	}

	for _, srv := range servers {
		status, toolCount, lastErr := h.proxy.GetServerStatus(srv.ID)
		srv.Status = status
		result = append(result, serverWithStatus{
			Server:     srv,
			ToolsCount: toolCount,
			LiveError:  lastErr,
		})
	}
	if result == nil {
		result = []serverWithStatus{}
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handlers) handleCreateServer(w http.ResponseWriter, r *http.Request) {
	var req models.CreateServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "Name is required")
		return
	}
	if req.Transport != "stdio" && req.Transport != "http" && req.Transport != "streamable-http" {
		req.Transport = "stdio"
	}
	if req.Transport == "stdio" && req.Command == "" {
		writeError(w, http.StatusBadRequest, "Command is required for stdio transport")
		return
	}
	if (req.Transport == "http" || req.Transport == "streamable-http") && req.URL == "" {
		writeError(w, http.StatusBadRequest, "URL is required for http/streamable-http transport")
		return
	}

	srv, err := h.proxy.AddServer(&req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create server: %v", err))
		return
	}

	writeJSON(w, http.StatusCreated, srv)
}

func (h *Handlers) handleGetServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Handle builtin memory servers (virtual — not in the database)
	if memory.IsMemoryServerID(id) {
		setID := memory.MemorySetIDFromServerID(id)
		ms, err := h.store.GetMemorySet(setID)
		var name string
		if err == nil && ms != nil {
			name = ms.Name
		} else {
			name = "memory" // default set
		}
		srv := &models.Server{
			ID:        id,
			Name:      name,
			Transport: "builtin",
			Enabled:   true,
			IsBuiltin: true,
			Status:    "connected",
		}
		toolCount := 0
		if memSrv := h.proxy.GetMemoryServer(setID); memSrv != nil {
			toolCount = len(memSrv.Tools())
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"server":      srv,
			"tools_count": toolCount,
			"live_error":  "",
		})
		return
	}

	srv, err := h.store.GetServer(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "Server not found")
		return
	}

	status, toolCount, lastErr := h.proxy.GetServerStatus(id)
	srv.Status = status

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"server":      srv,
		"tools_count": toolCount,
		"live_error":  lastErr,
	})
}

func (h *Handlers) handleUpdateServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req models.UpdateServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	srv, err := h.proxy.UpdateServer(id, &req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to update server: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, srv)
}

func (h *Handlers) handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.proxy.DeleteServer(id); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to delete server: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handlers) handleReconnectServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.proxy.ReconnectServer(id); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to reconnect: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reconnecting"})
}

func (h *Handlers) handleGetServerLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	logs := h.proxy.GetServerLogs(id)
	if logs == nil {
		logs = []proxy.LogEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"logs":  logs,
		"count": len(logs),
	})
}

func (h *Handlers) handleClearServerLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	h.proxy.ClearServerLogs(id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}

func (h *Handlers) handleToggleLogsEnabled(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		LogsEnabled bool `json:"logs_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	enabled := req.LogsEnabled
	_, err := h.proxy.UpdateServer(id, &models.UpdateServerRequest{
		LogsEnabled: &enabled,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to toggle logs: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"logs_enabled": enabled,
	})
}

// --- API Keys ---

func (h *Handlers) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.store.ListAPIKeys()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list API keys")
		return
	}
	if keys == nil {
		keys = []*models.APIKey{}
	}
	writeJSON(w, http.StatusOK, keys)
}

func (h *Handlers) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	var req models.CreateAPIKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "Name is required")
		return
	}

	keyString, keyHash, keyPrefix, err := h.auth.GenerateAPIKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to generate API key")
		return
	}

	apiKey := &models.APIKey{
		ID:         uuid.NewString(),
		Name:       req.Name,
		KeyHash:    keyHash,
		KeyPrefix:  keyPrefix,
		Scopes:     req.Scopes,
		CompoundID: req.CompoundID,
		Active:     true,
		CreatedAt:  time.Now(),
	}

	if req.ExpiresIn != nil && *req.ExpiresIn > 0 {
		exp := time.Now().Add(time.Duration(*req.ExpiresIn) * 24 * time.Hour)
		apiKey.ExpiresAt = &exp
	}

	if err := h.store.CreateAPIKey(apiKey); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create API key: %v", err))
		return
	}

	// Return the key once — never again
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":          apiKey.ID,
		"name":        apiKey.Name,
		"key":         keyString,
		"key_prefix":  keyPrefix,
		"scopes":      apiKey.Scopes,
		"compound_id": apiKey.CompoundID,
		"expires_at":  apiKey.ExpiresAt,
		"created_at":  apiKey.CreatedAt,
		"message":     "Save this key — it will not be shown again",
	})
}

func (h *Handlers) handleDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.store.DeleteAPIKey(id); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete API key")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Tools ---

func (h *Handlers) handleListTools(w http.ResponseWriter, r *http.Request) {
	tools := h.proxy.ListTools()
	if tools == nil {
		tools = []models.Tool{}
	}
	writeJSON(w, http.StatusOK, tools)
}

// --- Dashboard ---

func (h *Handlers) handleDashboard(w http.ResponseWriter, r *http.Request) {
	servers, _ := h.store.ListServers()
	keys, _ := h.store.ListAPIKeys()
	compounds, _ := h.store.ListCompounds()
	memCount, _ := h.store.CountMemories()
	tools := h.proxy.ListTools()

	connected := 0
	for _, srv := range servers {
		status, _, _ := h.proxy.GetServerStatus(srv.ID)
		if status == "connected" {
			connected++
		}
	}

	stats := models.DashboardStats{
		TotalServers:     len(servers),
		ConnectedServers: connected,
		TotalTools:       len(tools),
		TotalAPIKeys:     len(keys),
		TotalCompounds:   len(compounds),
		TotalMemories:    memCount,
	}
	writeJSON(w, http.StatusOK, stats)
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// validateRedirectURI checks that a client redirect_uri is safe.
// Allowed: http(s)://localhost:*, http(s)://127.0.0.1:*, http(s)://[::1]:*,
// or custom app schemes (e.g. com.example.app://callback).
// Rejects: external HTTP(S) hosts, file://, data:, javascript:.
func validateRedirectURI(rawURI string) error {
	parsed, err := url.Parse(rawURI)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	scheme := strings.ToLower(parsed.Scheme)

	// Custom app schemes (e.g. com.raycast://) — allow but must have a path or host
	if scheme != "http" && scheme != "https" {
		if scheme == "" || scheme == "file" || scheme == "data" || scheme == "javascript" {
			return fmt.Errorf("scheme %q is not allowed", scheme)
		}
		// App scheme — allow (MCP clients use deep links)
		return nil
	}

	// HTTP(S) — only allow localhost / loopback
	host := parsed.Hostname()
	if host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "[::1]" {
		return nil
	}

	return fmt.Errorf("external host %q is not allowed — only localhost is accepted", host)
}

// --- Compound Servers ---

func (h *Handlers) handleListCompounds(w http.ResponseWriter, r *http.Request) {
	compounds, err := h.store.ListCompounds()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list compound servers")
		return
	}
	if compounds == nil {
		compounds = []*models.CompoundServer{}
	}
	writeJSON(w, http.StatusOK, compounds)
}

func (h *Handlers) handleCreateCompound(w http.ResponseWriter, r *http.Request) {
	var req models.CreateCompoundRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "Name is required")
		return
	}

	compound := &models.CompoundServer{
		ID:             uuid.NewString(),
		Name:           req.Name,
		Description:    req.Description,
		DictionaryMode: req.DictionaryMode,
		CreatedAt:      time.Now(),
	}

	if err := h.store.CreateCompound(compound, req.MemberIDs); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create compound: %v", err))
		return
	}

	writeJSON(w, http.StatusCreated, compound)
}

func (h *Handlers) handleGetCompound(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	compound, err := h.store.GetCompound(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "Compound server not found")
		return
	}

	memberIDs, _ := h.store.GetCompoundMemberIDs(id)
	var members []models.Server
	for _, mid := range memberIDs {
		if memory.IsMemoryServerID(mid) {
			// Virtual builtin memory server — not in the database
			setID := memory.MemorySetIDFromServerID(mid)
			ms, err := h.store.GetMemorySet(setID)
			if err == nil && ms != nil {
				members = append(members, models.Server{
					ID:        mid,
					Name:      ms.Name,
					Transport: "builtin",
					Enabled:   true,
					IsBuiltin: true,
					Status:    "connected",
				})
			} else {
				// Fallback for default set
				builtin := models.BuiltinMemoryServer()
				members = append(members, builtin)
			}
			continue
		}
		srv, err := h.store.GetServer(mid)
		if err != nil {
			continue
		}
		status, _, _ := h.proxy.GetServerStatus(mid)
		srv.Status = status
		members = append(members, *srv)
	}
	if members == nil {
		members = []models.Server{}
	}

	toolCount := len(h.proxy.ListToolsForCompound(id))
	// Also count memory tools from compound members
	memorySetIDs := h.proxy.IsMemoryCompoundMember(id)
	memoryToolCount := 0
	for _, setID := range memorySetIDs {
		if srv := h.proxy.GetMemoryServer(setID); srv != nil {
			memoryToolCount += len(srv.Tools())
		}
	}
	totalToolCount := toolCount + memoryToolCount

	writeJSON(w, http.StatusOK, models.CompoundServerWithMembers{
		CompoundServer:  *compound,
		Members:         members,
		ToolCount:       totalToolCount,
		ServerToolCount: toolCount,
		MemoryToolCount: memoryToolCount,
	})
}

func (h *Handlers) handleUpdateCompound(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req models.UpdateCompoundRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := h.store.UpdateCompound(id, &req); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to update compound: %v", err))
		return
	}

	compound, _ := h.store.GetCompound(id)
	writeJSON(w, http.StatusOK, compound)
}

func (h *Handlers) handleDeleteCompound(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.store.DeleteCompound(id); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to delete compound: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handlers) handleAddCompoundMember(w http.ResponseWriter, r *http.Request) {
	compoundID := r.PathValue("id")
	serverID := r.PathValue("serverId")

	// Verify compound exists
	if _, err := h.store.GetCompound(compoundID); err != nil {
		writeError(w, http.StatusNotFound, "Compound server not found")
		return
	}
	// Allow builtin memory servers without DB lookup
	if !memory.IsMemoryServerID(serverID) {
		if _, err := h.store.GetServer(serverID); err != nil {
			writeError(w, http.StatusNotFound, "Server not found")
			return
		}
	}

	if err := h.store.AddCompoundMember(compoundID, serverID); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to add member: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "added"})
}

func (h *Handlers) handleRemoveCompoundMember(w http.ResponseWriter, r *http.Request) {
	compoundID := r.PathValue("id")
	serverID := r.PathValue("serverId")

	if err := h.store.RemoveCompoundMember(compoundID, serverID); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to remove member: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// --- OAuth ---

func (h *Handlers) handleInitiateAuth(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Build callback base URL from the request
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	// Check X-Forwarded-Proto for reverse proxy scenarios
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	callbackBaseURL := fmt.Sprintf("%s://%s", scheme, r.Host)

	authURL, err := h.proxy.InitiateAuth(id, callbackBaseURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"auth_url": authURL,
		"message":  "Open the auth_url in your browser to authenticate",
	})
}

// handleOAuthAuthorize is the proxy's authorization endpoint.
// It receives the MCP client's authorization request, stores the client's
// redirect_uri + state in a signed JWT, then redirects to the upstream OIDC
// provider (PocketID) with the proxy's own callback URL as redirect_uri.
// When PocketID redirects back, handleOAuthCallback decodes the JWT and
// forwards the authorization code to the client's actual redirect_uri.
func (h *Handlers) handleOAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	if !h.auth.HasOIDC() {
		writeError(w, http.StatusBadRequest, "OAuth not configured")
		return
	}

	q := r.URL.Query()
	clientRedirectURI := q.Get("redirect_uri")
	clientState := q.Get("state")
	codeChallenge := q.Get("code_challenge")
	codeChallengeMethod := q.Get("code_challenge_method")
	scope := q.Get("scope")
	resource := q.Get("resource")

	if clientRedirectURI == "" {
		writeError(w, http.StatusBadRequest, "Missing redirect_uri parameter")
		return
	}

	// Validate redirect_uri to prevent open redirect / auth code interception.
	// Allowed: http(s)://localhost:*, http(s)://127.0.0.1:*, or custom app schemes (e.g. com.example://).
	if err := validateRedirectURI(clientRedirectURI); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Invalid redirect_uri: %v", err))
		return
	}

	oidc := h.auth.OIDC()
	discovery := oidc.Discovery()
	if discovery == nil || discovery.AuthorizationEndpoint == "" {
		writeError(w, http.StatusInternalServerError, "OIDC provider not discovered")
		return
	}

	// Build the proxy's callback URL (the redirect_uri registered with PocketID)
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	proxyCallbackURL := fmt.Sprintf("%s://%s/api/oauth/callback", scheme, r.Host)

	// Create a signed state JWT that encodes the client's redirect_uri + state.
	// This allows the proxy to forward the callback to the client after PocketID
	// redirects back to the proxy's callback URL.
	stateJWT, err := h.auth.CreateOAuthState(clientRedirectURI, clientState)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create state")
		return
	}

	// Redirect to PocketID's authorization endpoint
	upstreamURL := discovery.AuthorizationEndpoint
	upstreamParams := url.Values{
		"response_type": {"code"},
		"client_id":     {oidc.ClientID()},
		"redirect_uri":  {proxyCallbackURL},
		"state":         {stateJWT},
	}
	if scope != "" {
		upstreamParams.Set("scope", scope)
	}
	if codeChallenge != "" {
		upstreamParams.Set("code_challenge", codeChallenge)
	}
	if codeChallengeMethod != "" {
		upstreamParams.Set("code_challenge_method", codeChallengeMethod)
	}
	if resource != "" {
		upstreamParams.Set("resource", resource)
	}

	log.Printf("[OAuth-Authorize] redirect_uri=%s -> %s, has_pkce=%v, resource=%s, scope=%s",
		clientRedirectURI, proxyCallbackURL, codeChallenge != "", resource, scope)

	http.Redirect(w, r, upstreamURL+"?"+upstreamParams.Encode(), http.StatusFound)
}

// handleOAuthCallback handles the OAuth callback from the upstream OIDC provider.
// It decodes the signed state JWT to extract the MCP client's actual redirect_uri,
// then redirects the browser to the client's redirect_uri with the authorization
// code and the client's original state parameter.
func (h *Handlers) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")

	if state == "" || code == "" {
		writeError(w, http.StatusBadRequest, "Missing state or code parameter")
		return
	}

	// Try to decode the state as a proxy-issued JWT
	clientRedirect, clientState, err := h.auth.VerifyOAuthState(state)
	if err != nil {
		log.Printf("[OAuth-Callback] State not a proxy JWT (len=%d), trying MCP server auth flow: %v", len(state), err)
		// Not a proxy-issued state — fall back to the existing MCP server auth flow
		// (this handles the case where the proxy itself initiates OAuth for an
		// upstream MCP server like GitHub Copilot)
		if err := h.proxy.HandleAuthCallback(state, code); err != nil {
			log.Printf("[OAuth-Callback] MCP server auth callback failed: %v", err)
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `<!DOCTYPE html><html><body><h2>Authentication Failed</h2><p>%s</p><p>You can close this window.</p></body></html>`, err.Error())
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<!DOCTYPE html><html><body><h2>✅ Authentication Successful</h2><p>You can close this window and return to MCP Proxy.</p><script>setTimeout(() => window.close(), 3000);</script></body></html>`))
		return
	}

	// Proxy-issued state: forward the code + client's original state to the
	// client's actual redirect_uri
	log.Printf("[OAuth-Callback] Redirecting to client: %s (has_state=%v)", clientRedirect, clientState != "")
	targetURL := clientRedirect
	if clientState != "" {
		targetURL += "?" + url.Values{
			"code":  {code},
			"state": {clientState},
		}.Encode()
	} else {
		targetURL += "?" + url.Values{"code": {code}}.Encode()
	}

	http.Redirect(w, r, targetURL, http.StatusFound)
}

func (h *Handlers) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	hasTokens, expired := h.proxy.GetAuthStatus(id)

	status := "none"
	if hasTokens && !expired {
		status = "valid"
	} else if hasTokens && expired {
		status = "expired"
	}

	// Get the auth method configured for this server
	authMethod := h.proxy.GetServerAuthMethod(id)

	// For bearer/env_bearer, status is based on whether token is configured
	if authMethod == "bearer" {
		if h.proxy.HasBearerToken(id) {
			status = "valid"
		} else {
			status = "none"
		}
	}
	if authMethod == "env_bearer" {
		if h.proxy.HasEnvBearerToken(id) {
			status = "valid"
		} else {
			status = "none"
		}
	}

	resp := map[string]interface{}{
		"status":      status,
		"has_tokens":  hasTokens,
		"expired":     expired,
		"auth_method": authMethod,
	}

	// For env_bearer, include the env var name
	if authMethod == "env_bearer" {
		resp["bearer_token_env"] = h.proxy.GetBearerTokenEnv(id)
	}

	// Include OAuth provider info from discovered metadata (only for OAuth method)
	if authMethod == "oauth" {
		if meta := h.proxy.GetOAuthMetadata(id); meta != nil {
			resp["issuer"] = meta.Issuer
			resp["authorization_endpoint"] = meta.AuthorizationEndpoint
			resp["device_auth_supported"] = meta.DeviceAuthorizationEndpoint != "" ||
				mcp.IsEntraID(meta.Issuer) || mcp.IsEntraID(meta.AuthorizationEndpoint) || mcp.IsEntraID(meta.TokenEndpoint)
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handlers) handleSetBearerToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var body struct {
		BearerToken    string `json:"bearer_token"`
		BearerTokenEnv string `json:"bearer_token_env"`
		Method         string `json:"method"` // "bearer" or "env_bearer"
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	switch body.Method {
	case "bearer":
		if body.BearerToken == "" {
			writeError(w, http.StatusBadRequest, "bearer_token is required")
			return
		}
		if err := h.proxy.SetBearerToken(id, body.BearerToken); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	case "env_bearer":
		if body.BearerTokenEnv == "" {
			writeError(w, http.StatusBadRequest, "bearer_token_env is required")
			return
		}
		if err := h.proxy.SetEnvBearerToken(id, body.BearerTokenEnv); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	case "none":
		if err := h.proxy.ClearAuth(id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "method must be 'bearer', 'env_bearer', or 'none'")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handlers) handleInitiateDeviceAuth(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	result, err := h.proxy.InitiateDeviceAuth(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handlers) handlePollDeviceAuth(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.proxy.PollDeviceAuth(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Check if auth completed
	hasTokens, expired := h.proxy.GetAuthStatus(id)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"completed": hasTokens,
		"expired":   expired,
	})
}

func (h *Handlers) handleCancelDeviceAuth(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	h.proxy.CancelDeviceAuth(id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// SplitPath splits a URL path into segments.
func SplitPath(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

// --- Memories ---

func (h *Handlers) handleListMemories(w http.ResponseWriter, r *http.Request) {
	setID := r.URL.Query().Get("set_id")
	if setID == "" {
		setID = "default"
	}
	palace := r.URL.Query().Get("palace")
	memories, err := h.store.ListMemories(setID, palace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list memories")
		return
	}
	if memories == nil {
		memories = []*models.Memory{}
	}
	writeJSON(w, http.StatusOK, memories)
}

func (h *Handlers) handleCreateMemory(w http.ResponseWriter, r *http.Request) {
	var req models.CreateMemoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	if req.Palace == "" {
		req.Palace = "general"
	}
	importance := 50
	if req.Importance != nil {
		importance = *req.Importance
	}
	if req.Tags == nil {
		req.Tags = []string{}
	}
	now := time.Now()
	setID := r.URL.Query().Get("set_id")
	if setID == "" {
		setID = "default"
	}
	mem := &models.Memory{
		ID:         "mem_" + uuid.NewString(),
		SetID:      setID,
		Palace:     req.Palace,
		Room:       req.Room,
		Content:    req.Content,
		Tags:       req.Tags,
		Importance: importance,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := h.store.CreateMemory(mem); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create memory")
		return
	}
	writeJSON(w, http.StatusCreated, mem)
}

func (h *Handlers) handleGetMemory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	mem, err := h.store.GetMemory(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "Memory not found")
		return
	}
	writeJSON(w, http.StatusOK, mem)
}

func (h *Handlers) handleUpdateMemory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req models.UpdateMemoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if err := h.store.UpdateMemory(id, &req); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update memory")
		return
	}
	mem, _ := h.store.GetMemory(id)
	writeJSON(w, http.StatusOK, mem)
}

func (h *Handlers) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.store.DeleteMemory(id); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete memory")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handlers) handleListPalaces(w http.ResponseWriter, r *http.Request) {
	setID := r.URL.Query().Get("set_id")
	if setID == "" {
		setID = "default"
	}
	palaces, err := h.store.ListPalaces(setID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list palaces")
		return
	}
	if palaces == nil {
		palaces = []map[string]interface{}{}
	}
	writeJSON(w, http.StatusOK, palaces)
}

func (h *Handlers) handleSearchMemories(w http.ResponseWriter, r *http.Request) {
	setID := r.URL.Query().Get("set_id")
	if setID == "" {
		setID = "default"
	}
	query := r.URL.Query().Get("q")
	if query == "" {
		writeError(w, http.StatusBadRequest, "Query parameter 'q' is required")
		return
	}
	memories, err := h.store.SearchMemories(setID, query)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Search failed")
		return
	}
	if memories == nil {
		memories = []*models.Memory{}
	}
	writeJSON(w, http.StatusOK, memories)
}

// --- Memory Sets ---

func (h *Handlers) handleListMemorySets(w http.ResponseWriter, r *http.Request) {
	sets, err := h.proxy.ListMemorySets()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list memory sets")
		return
	}
	if sets == nil {
		sets = []*models.MemorySet{}
	}
	writeJSON(w, http.StatusOK, sets)
}

func (h *Handlers) handleCreateMemorySet(w http.ResponseWriter, r *http.Request) {
	var req models.CreateMemorySetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "Name is required")
		return
	}
	ms, err := h.proxy.CreateMemorySet(req.Name, req.Description)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create memory set: %v", err))
		return
	}
	writeJSON(w, http.StatusCreated, ms)
}

func (h *Handlers) handleUpdateMemorySet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req models.UpdateMemorySetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if err := h.proxy.UpdateMemorySet(id, &req); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to update memory set: %v", err))
		return
	}
	ms, _ := h.store.GetMemorySet(id)
	writeJSON(w, http.StatusOK, ms)
}

func (h *Handlers) handleDeleteMemorySet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.proxy.DeleteMemorySet(id); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to delete memory set: %v", err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Env Vars (admin) ---

// envVarRefPattern matches ${KEY} or ${KEY:-default} patterns in env var values.
var envVarRefPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// resolveEnvVarReferences resolves ${KEY} and ${KEY:-default} references in env var values.
// Keys are resolved within the same project/environment scope. Unknown keys are left
// as-is (or replaced with the default if specified).
func resolveEnvVarReferences(envMap map[string]string) {
	for key, val := range envMap {
		envMap[key] = envVarRefPattern.ReplaceAllStringFunc(val, func(match string) string {
			sub := envVarRefPattern.FindStringSubmatch(match)
			refKey := sub[1]
			defaultVal := sub[2]
			if resolved, ok := envMap[refKey]; ok {
				return resolved
			}
			if defaultVal != "" || strings.Contains(match, ":-") {
				return defaultVal
			}
			return match
		})
	}
}

func (h *Handlers) handleListEnvVars(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	environment := r.URL.Query().Get("environment")

	envVars, err := h.store.ListEnvVars(project, environment)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list env vars")
		return
	}

	// Decrypt values for admin response and build reference map
	envMap := make(map[string]string)
	for _, ev := range envVars {
		decrypted, err := crypto.Decrypt(h.masterKey, ev.Value)
		if err != nil {
			continue
		}
		ev.Value = decrypted
		envMap[ev.Key] = decrypted
		ev.IsReference = envVarRefPattern.MatchString(decrypted)
	}

	// Resolve ${KEY} references for display
	resolvedMap := make(map[string]string, len(envMap))
	for k, v := range envMap {
		resolvedMap[k] = v
	}
	resolveEnvVarReferences(resolvedMap)
	for _, ev := range envVars {
		if resolved, ok := resolvedMap[ev.Key]; ok && resolved != ev.Value {
			ev.ResolvedValue = resolved
		}
	}

	if envVars == nil {
		envVars = []*models.EnvVar{}
	}
	writeJSON(w, http.StatusOK, envVars)
}

func (h *Handlers) handleCreateEnvVar(w http.ResponseWriter, r *http.Request) {
	var req models.CreateEnvVarRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if req.Project == "" {
		writeError(w, http.StatusBadRequest, "project is required")
		return
	}
	if req.Environment == "" {
		writeError(w, http.StatusBadRequest, "environment is required")
		return
	}
	if req.Key == "" {
		writeError(w, http.StatusBadRequest, "key is required")
		return
	}

	// Encrypt the value at rest with the master key
	encryptedValue, err := crypto.Encrypt(h.masterKey, req.Value)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to encrypt env var value")
		return
	}

	now := time.Now()
	ev := &models.EnvVar{
		ID:          uuid.NewString(),
		Project:     req.Project,
		Environment: req.Environment,
		Key:         req.Key,
		Value:       encryptedValue,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := h.store.CreateEnvVar(ev); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create env var: %v", err))
		return
	}

	// Return with the decrypted value
	ev.Value = req.Value
	writeJSON(w, http.StatusCreated, ev)
}

func (h *Handlers) handleUpdateEnvVar(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req models.UpdateEnvVarRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Encrypt the new value before storing
	if req.Value != nil {
		encryptedValue, err := crypto.Encrypt(h.masterKey, *req.Value)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to encrypt env var value")
			return
		}
		req.Value = &encryptedValue
	}

	if err := h.store.UpdateEnvVar(id, &req); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to update env var: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *Handlers) handleDeleteEnvVar(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.store.DeleteEnvVar(id); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete env var")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handlers) handleListEnvVarProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := h.store.ListEnvVarProjects()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list env var projects")
		return
	}
	if projects == nil {
		projects = []string{}
	}
	writeJSON(w, http.StatusOK, projects)
}

func (h *Handlers) handleListEnvVarEnvironments(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	if project == "" {
		writeError(w, http.StatusBadRequest, "project query parameter is required")
		return
	}

	envs, err := h.store.ListEnvVarEnvironments(project)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list env var environments")
		return
	}
	if envs == nil {
		envs = []string{}
	}
	writeJSON(w, http.StatusOK, envs)
}

// --- Env Vars export (API key auth) ---

func (h *Handlers) handleExportEnvVars(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project")
	environment := r.URL.Query().Get("environment")

	envVars, err := h.store.ListEnvVars(project, environment)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load env vars")
		return
	}

	// Build a JSON object of key-value pairs (decrypted with master key)
	envMap := make(map[string]string)
	for _, ev := range envVars {
		decrypted, err := crypto.Decrypt(h.masterKey, ev.Value)
		if err != nil {
			continue
		}
		envMap[ev.Key] = decrypted
	}

	// Resolve ${KEY} references before exporting
	resolveEnvVarReferences(envMap)

	jsonData, err := json.Marshal(envMap)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to marshal env vars")
		return
	}

	// Derive a NaCl key from the raw API key string
	rawAPIKey := auth.ExtractAPIKey(r)
	if rawAPIKey == "" {
		writeError(w, http.StatusUnauthorized, "Missing API key")
		return
	}
	apiKeyDerived := crypto.DeriveKey(rawAPIKey)

	// Encrypt the JSON blob with the API key
	encrypted, err := crypto.Encrypt(apiKeyDerived, string(jsonData))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to encrypt env vars")
		return
	}

	// Extract nonce hint (first 4 bytes of nonce, hex-encoded = 8 chars)
	nonceHint := ""
	if raw, err := base64.StdEncoding.DecodeString(encrypted); err == nil && len(raw) >= 4 {
		nonceHint = hex.EncodeToString(raw[:4])
	}

	writeJSON(w, http.StatusOK, models.EnvVarExport{
		Project:     project,
		Environment: environment,
		Encrypted:   encrypted,
		Nonce:       nonceHint,
	})
}

// --- OIDC ---

// handleOIDCStatus returns auth configuration (OIDC + password login).
func (h *Handlers) handleOIDCStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{
		"enabled":        h.auth.HasOIDC(),
		"password_login": h.adminLoginEnabled,
	})
}

// handleOIDCLogin redirects to the OIDC provider's authorization endpoint.
func (h *Handlers) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if !h.auth.HasOIDC() {
		writeError(w, http.StatusBadRequest, "OIDC not configured")
		return
	}
	provider := h.auth.OIDC()
	state := auth.GenerateState()
	redirectURL := provider.RedirectURL(r)

	// Store state in a short-lived cookie for CSRF protection
	isSecure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{
		Name:     "oidc_state",
		Value:    state,
		Path:     "/",
		MaxAge:   300, // 5 minutes
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isSecure,
	})

	authURL := provider.AuthURL(state, redirectURL)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// handleOIDCCallback handles the OIDC callback, exchanges code for token,
// provisions the user, and redirects to the frontend with a JWT.
func (h *Handlers) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if !h.auth.HasOIDC() {
		writeError(w, http.StatusBadRequest, "OIDC not configured")
		return
	}

	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		writeError(w, http.StatusBadRequest, "Missing code or state")
		return
	}

	// Verify state from cookie
	cookie, err := r.Cookie("oidc_state")
	if err != nil || cookie.Value != state {
		writeError(w, http.StatusBadRequest, "Invalid or expired state")
		return
	}
	// Clear the cookie
	http.SetCookie(w, &http.Cookie{
		Name:   "oidc_state",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})

	provider := h.auth.OIDC()
	redirectURL := provider.RedirectURL(r)

	// Exchange code for tokens
	token, err := provider.Exchange(code, redirectURL)
	if err != nil {
		log.Printf("OIDC callback: token exchange failed (redirect=%s): %v", redirectURL, err)
		writeError(w, http.StatusUnauthorized, fmt.Sprintf("Token exchange failed: %v", err))
		return
	}

	// Fetch user info
	userInfo, err := provider.UserInfo(token.AccessToken)
	if err != nil {
		log.Printf("OIDC callback: userinfo failed: %v", err)
		writeError(w, http.StatusUnauthorized, fmt.Sprintf("Failed to get user info: %v", err))
		return
	}

	// Extract normalized user
	pu := auth.ExtractUser(userInfo)
	if pu.Subject == "" {
		writeError(w, http.StatusUnauthorized, "No subject in OIDC response")
		return
	}

	// Find or provision user
	user, err := h.auth.LoginOrProvisionUser(pu)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to provision user: %v", err))
		return
	}

	// Generate JWT
	jwtToken, _, err := h.auth.GenerateToken(user.ID, user.Username, user.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to generate token")
		return
	}

	// Redirect to frontend with token in URL fragment (not query param)
	// Fragments (#) are NOT sent to the server in HTTP requests, so the token
	// won't appear in server logs, browser history entries, or referrer headers
	frontendURL := "/login#token=" + jwtToken
	http.Redirect(w, r, frontendURL, http.StatusFound)
}

// handleRegistrySearch proxies a search query to an MCP registry.
// Default registry: https://registry.modelcontextprotocol.io/v0/servers
// Can be overridden with MCP_REGISTRY_URL env var.
func (h *Handlers) handleRegistrySearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	registryURL := os.Getenv("MCP_REGISTRY_URL")
	if registryURL == "" {
		registryURL = "https://registry.modelcontextprotocol.io/v0/servers"
	}

	targetURL := registryURL
	if q != "" {
		targetURL += "?search=" + url.QueryEscape(q)
	}

	client := sharedOAuthProxyClient
	resp, err := client.Get(targetURL)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Failed to reach registry"})
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024)) // 2MB max
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to read registry response"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

// handleProtectedResourceMetadata returns RFC 9728 Protected Resource Metadata.
// Handles both root (/.well-known/oauth-protected-resource) and path-insertion
// (/.well-known/oauth-protected-resource/api/compounds/{id}/mcp) variants.
// The resource field reflects the actual MCP endpoint URL when accessed via path-insertion.
func (h *Handlers) handleProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	baseURL := fmt.Sprintf("%s://%s", scheme, r.Host)

	// Check if this is a path-insertion request (e.g. /.well-known/oauth-protected-resource/api/compounds/.../mcp)
	// The path after the well-known prefix is the actual MCP endpoint path.
	wellKnownPrefix := "/.well-known/oauth-protected-resource"
	resourceURL := baseURL
	if len(r.URL.Path) > len(wellKnownPrefix) {
		// Extract the MCP endpoint path from the URL
		mcpPath := r.URL.Path[len(wellKnownPrefix):]
		// Strip leading slashes
		mcpPath = strings.TrimPrefix(mcpPath, "/")
		if mcpPath != "" {
			resourceURL = fmt.Sprintf("%s/%s", baseURL, mcpPath)
		}
	}

	resp := map[string]interface{}{
		"resource": resourceURL,
	}

	if h.auth.HasOIDC() {
		// Point to the proxy itself as the auth server — the proxy serves
		// /.well-known/oauth-authorization-server which wraps the OIDC provider
		// and adds DCR + CIMD support that the upstream provider may lack.
		resp["authorization_servers"] = []string{baseURL}
		resp["bearer_methods"] = []string{"header"}
		// Per MCP spec: "If scope is not available, use all scopes defined in
		// scopes_supported from the Protected Resource Metadata document."
		resp["scopes_supported"] = []string{"openid", "profile", "email"}
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleAuthorizationServerMetadata returns RFC 8414 Authorization Server Metadata.
// ALL endpoints are proxied through the proxy itself so that the issuer URL
// matches all endpoint URLs. This is required because some MCP clients
// validate that endpoints are on the same domain as the issuer.
func (h *Handlers) handleAuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	proxyURL := fmt.Sprintf("%s://%s", scheme, r.Host)

	if !h.auth.HasOIDC() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "OAuth not configured"})
		return
	}

	oidc := h.auth.OIDC()
	discovery := oidc.Discovery()

	// All endpoints are on the proxy's domain — the proxy forwards to PocketID
	resp := map[string]interface{}{
		"issuer":                                proxyURL,
		"authorization_endpoint":                fmt.Sprintf("%s/api/oauth/authorize", proxyURL),
		"token_endpoint":                        fmt.Sprintf("%s/api/oauth/token", proxyURL),
		"registration_endpoint":                 fmt.Sprintf("%s/api/oauth/register", proxyURL),
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_method_supported":  []string{"none", "client_secret_post"},
		"scopes_supported":                      []string{"openid", "profile", "email"},
		"code_challenge_methods_supported":      []string{"S256"},
		"client_id_metadata_document_supported": true,
	}

	// Proxy these endpoints through the proxy too
	if discovery != nil {
		if discovery.JwksURI != "" {
			resp["jwks_uri"] = fmt.Sprintf("%s/api/oauth/jwks", proxyURL)
		}
		if discovery.UserinfoEndpoint != "" {
			resp["userinfo_endpoint"] = fmt.Sprintf("%s/api/oauth/userinfo", proxyURL)
		}
		if discovery.RevocationEndpoint != "" {
			resp["revocation_endpoint"] = fmt.Sprintf("%s/api/oauth/revoke", proxyURL)
		}
		if discovery.IntrospectionEndpoint != "" {
			resp["introspection_endpoint"] = fmt.Sprintf("%s/api/oauth/introspect", proxyURL)
		}
		// OIDC-specific fields
		if len(discovery.IDTokenSigningAlgValuesSupported) > 0 {
			resp["id_token_signing_alg_values_supported"] = discovery.IDTokenSigningAlgValuesSupported
		}
		if len(discovery.SubjectTypesSupported) > 0 {
			resp["subject_types_supported"] = discovery.SubjectTypesSupported
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleOAuthRegister implements RFC 7591 Dynamic Client Registration.
// Returns the proxy's pre-registered OIDC client credentials so that
// any MCP client can register without manual setup.
func (h *Handlers) handleOAuthRegister(w http.ResponseWriter, r *http.Request) {
	if !h.auth.HasOIDC() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "OAuth not configured"})
		return
	}

	oidc := h.auth.OIDC()
	clientID := oidc.ClientID()
	clientSecret := oidc.ClientSecret()

	if clientID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "No OAuth client configured"})
		return
	}

	// Parse the request body to extract the client's requested redirect_uris.
	// We echo the client's redirect_uris back so the client uses its OWN
	// redirect_uri (e.g. http://localhost:PORT/callback). The proxy handles
	// the mismatch with PocketID's registered redirect_uri internally:
	// - Authorize: stores client's redirect_uri in a signed state JWT,
	//   redirects to PocketID with the proxy's callback URL
	// - Token: replaces redirect_uri with the proxy's callback URL before
	//   forwarding to PocketID
	clientRedirectURIs := []string{}
	if r.Body != nil {
		bodyBytes, err := io.ReadAll(r.Body)
		if err == nil && len(bodyBytes) > 0 {
			var reg struct {
				RedirectURIs []string `json:"redirect_uris"`
				ClientName   string   `json:"client_name"`
			}
			if err := json.Unmarshal(bodyBytes, &reg); err == nil && len(reg.RedirectURIs) > 0 {
				clientRedirectURIs = reg.RedirectURIs
			}
		}
	}

	resp := map[string]interface{}{
		"client_id":                  clientID,
		"client_id_issued_at":        time.Now().Unix(),
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	}

	// Echo the client's redirect_uris, or use a sensible default
	if len(clientRedirectURIs) > 0 {
		resp["redirect_uris"] = clientRedirectURIs
	} else {
		resp["redirect_uris"] = []string{"http://localhost/callback"}
	}

	// Only include client_secret for confidential clients
	if clientSecret != "" {
		resp["client_secret"] = clientSecret
		resp["client_secret_expires_at"] = 0 // never expires
		resp["token_endpoint_auth_method"] = "client_secret_post"
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

// handleOAuthProxy forwards OAuth requests to the upstream OIDC provider (PocketID).
// This ensures all endpoints in the auth server metadata are on the proxy's domain,
// matching the issuer URL. Routes: /api/oauth/token, /jwks, /userinfo, /revoke, /introspect.
// sharedOAuthProxyClient is a pooled HTTP client for all upstream OAuth requests.
// Using a shared client with a shared Transport enables connection reuse (keep-alive)
// and prevents the resource leak of creating a new Transport per request.
var sharedOAuthProxyClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 5,
		IdleConnTimeout:     90 * time.Second,
	},
	// Don't follow redirects — OAuth endpoints should not redirect POST requests.
	// A 301/302 redirect would convert POST→GET, causing upstream to return 404.
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		log.Printf("[OAuth-Proxy] Redirect blocked: %s -> %s (method=%s)", req.URL.Host, req.URL.String(), req.Method)
		return http.ErrUseLastResponse
	},
}

func (h *Handlers) handleOAuthProxy(w http.ResponseWriter, r *http.Request) {
	if !h.auth.HasOIDC() {
		writeError(w, http.StatusBadRequest, "OAuth not configured")
		return
	}

	oidc := h.auth.OIDC()
	discovery := oidc.Discovery()
	if discovery == nil {
		writeError(w, http.StatusInternalServerError, "OIDC provider not discovered")
		return
	}

	// Map the proxy path to the upstream OIDC provider endpoint
	var upstreamURL string
	switch r.URL.Path {
	case "/api/oauth/token":
		upstreamURL = discovery.TokenEndpoint
	case "/api/oauth/jwks":
		upstreamURL = discovery.JwksURI
	case "/api/oauth/userinfo":
		upstreamURL = discovery.UserinfoEndpoint
	case "/api/oauth/revoke":
		upstreamURL = discovery.RevocationEndpoint
	case "/api/oauth/introspect":
		upstreamURL = discovery.IntrospectionEndpoint
	default:
		writeError(w, http.StatusNotFound, "Unknown OAuth endpoint")
		return
	}

	if upstreamURL == "" {
		writeError(w, http.StatusNotFound, "Endpoint not supported by upstream provider")
		return
	}

	// For token requests, fix the redirect_uri to match the one used in
	// the authorization request (the proxy's callback URL). The MCP client
	// sends its own redirect_uri (e.g. http://localhost:PORT/callback),
	// but PocketID expects the proxy's callback URL that was used during
	// the authorize step.
	var bodyReader io.Reader
	if r.URL.Path == "/api/oauth/token" && r.Method == "POST" {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Failed to read request body")
			return
		}
		r.Body.Close()

		// Determine if body is JSON or form-encoded
		contentType := r.Header.Get("Content-Type")
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		proxyCallbackURL := fmt.Sprintf("%s://%s/api/oauth/callback", scheme, r.Host)

		if strings.Contains(contentType, "json") {
			// JSON body — parse, replace redirect_uri, re-encode as JSON
			var jsonBody map[string]interface{}
			if err := json.Unmarshal(bodyBytes, &jsonBody); err != nil {
				writeError(w, http.StatusBadRequest, "Failed to parse JSON body")
				return
			}
			jsonBody["redirect_uri"] = proxyCallbackURL
			newBody, _ := json.Marshal(jsonBody)
			bodyReader = strings.NewReader(string(newBody))
			log.Printf("[OAuth-Proxy] Token request (JSON): replaced redirect_uri with %s", proxyCallbackURL)
		} else {
			// Form-encoded body — parse, replace redirect_uri, re-encode
			vals, err := url.ParseQuery(string(bodyBytes))
			if err != nil {
				writeError(w, http.StatusBadRequest, "Failed to parse form data")
				return
			}

			// Always override client_id and client_secret with the proxy's
			// PocketID credentials. The MCP client may send its own client_id
			// (e.g. generated in the DCR request), but PocketID only recognizes
			// the proxy's pre-registered client_id. The authorize step also
			// uses the proxy's client_id, so they must match for the token
			// exchange to succeed.
			vals.Set("client_id", oidc.ClientID())
			if oidc.ClientSecret() != "" {
				vals.Set("client_secret", oidc.ClientSecret())
			}

			log.Printf("[OAuth-Proxy] Token request (form): grant_type=%s, has_code=%v, has_verifier=%v, client_id=%s, redirect_uri=%s -> %s",
				vals.Get("grant_type"), vals.Get("code") != "", vals.Get("code_verifier") != "",
				vals.Get("client_id"), vals.Get("redirect_uri"), proxyCallbackURL)
			vals.Set("redirect_uri", proxyCallbackURL)
			bodyReader = strings.NewReader(vals.Encode())
		}
	} else if r.Body != nil {
		bodyReader = r.Body
	}

	// Forward the request to the upstream provider
	// Using shared pooled client — prevents resource leak from per-request Transport creation.
	// Redirects are blocked via CheckRedirect (see sharedOAuthProxyClient definition).

	// Always use POST for token requests — never let a redirect change the method
	method := r.Method
	if r.URL.Path == "/api/oauth/token" {
		method = http.MethodPost
	}
	upstreamReq, err := http.NewRequestWithContext(r.Context(), method, upstreamURL, bodyReader)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create upstream request")
		return
	}

	// Copy headers
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/x-www-form-urlencoded"
	}
	upstreamReq.Header.Set("Content-Type", contentType)
	for _, h2 := range []string{"Authorization", "Accept"} {
		if v := r.Header.Get(h2); v != "" {
			upstreamReq.Header.Set(h2, v)
		}
	}

	resp, err := sharedOAuthProxyClient.Do(upstreamReq)
	if err != nil {
		log.Printf("[OAuth-Proxy] Failed to reach upstream %s: %v", upstreamURL, err)
		writeError(w, http.StatusBadGateway, "Failed to reach upstream OAuth provider")
		return
	}
	defer resp.Body.Close()

	// For token requests, log the upstream response status
	if r.URL.Path == "/api/oauth/token" {
		respBody, _ := io.ReadAll(resp.Body)
		respBodyStr := string(respBody)
		// Log whether token exchange succeeded (don't log token values)
		hasToken := strings.Contains(respBodyStr, "\"access_token\"")
		if resp.StatusCode != 200 {
			log.Printf("[OAuth-Proxy] Token exchange FAILED: upstream=%s, status=%d, body_len=%d",
				upstreamURL, resp.StatusCode, len(respBodyStr))
		} else {
			log.Printf("[OAuth-Proxy] Token exchange OK: upstream=%s, status=%d, has_token=%v, body_len=%d",
				upstreamURL, resp.StatusCode, hasToken, len(respBodyStr))
		}
		// Write the response body back to the client — filter upstream headers
		tokenSafeHeaders := map[string]bool{
			"Content-Type":           true,
			"Cache-Control":          true,
			"Pragma":                 true,
			"Expires":                true,
			"X-Content-Type-Options": true,
		}
		for k, vs := range resp.Header {
			if !tokenSafeHeaders[k] {
				continue
			}
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
		return
	}

	// Copy response headers — filter out potentially sensitive upstream headers
	safeHeaders := map[string]bool{
		"Content-Type":           true,
		"Cache-Control":          true,
		"Pragma":                 true,
		"Expires":                true,
		"X-Content-Type-Options": true,
	}
	for k, vs := range resp.Header {
		if !safeHeaders[k] {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// handleClientMetadata serves the Client ID Metadata Document (CIMD).
// This is a public endpoint that authorization servers fetch to validate
// the client when a URL-formatted client_id is used.
// Per MCP spec: client_id MUST equal the URL this document is served at.
func (h *Handlers) handleClientMetadata(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	clientID := fmt.Sprintf("%s://%s/api/oauth/client-metadata", scheme, r.Host)
	redirectURI := fmt.Sprintf("%s://%s/api/oauth/callback", scheme, r.Host)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"client_id":                  clientID,
		"client_name":                "MCP Proxy",
		"client_uri":                 fmt.Sprintf("%s://%s", scheme, r.Host),
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
}

// handleGetRegistration returns the OAuth client registration status for a server.
func (h *Handlers) handleGetRegistration(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	meta := h.proxy.GetOAuthMetadata(id)
	if meta == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status": "none",
			"reason": "No OAuth metadata discovered for this server",
		})
		return
	}

	resp := map[string]interface{}{
		"status":                                "none",
		"issuer":                                meta.Issuer,
		"authorization_endpoint":                meta.AuthorizationEndpoint,
		"registration_endpoint":                 meta.RegistrationEndpoint,
		"client_id_metadata_document_supported": meta.ClientIDMetadataDocumentSupported,
	}

	// Check for persisted dynamic registration
	if meta.Issuer != "" {
		if reg, err := h.store.GetOAuthRegistration(meta.Issuer); err == nil && reg != nil {
			resp["status"] = "registered"
			resp["client_id"] = reg.ClientID
			resp["registration_method"] = "dynamic"
			resp["created_at"] = reg.CreatedAt
		}
	}

	// Check for pre-registered client ID (auth_token).
	// Only expose when NOT used as a bearer token — the auth_token field is
	// overloaded: it holds either a secret bearer token or a public OAuth client_id.
	srv, err := h.store.GetServer(id)
	if err == nil && srv != nil && srv.AuthToken != "" {
		effectiveAuthMethod := h.proxy.GetServerAuthMethod(id)
		if effectiveAuthMethod != "bearer" {
			resp["status"] = "pre-registered"
			resp["client_id"] = srv.AuthToken
			resp["registration_method"] = "pre-registration"
		}
	}

	// If CIMD is supported, report it as available
	if meta.ClientIDMetadataDocumentSupported {
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		cimdURL := fmt.Sprintf("%s://%s/api/oauth/client-metadata", scheme, r.Host)
		resp["cimd_url"] = cimdURL
		if resp["status"] == "none" {
			resp["status"] = "cimd-available"
			resp["registration_method"] = "cimd"
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleDeleteRegistration removes a persisted dynamic client registration.
func (h *Handlers) handleDeleteRegistration(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	meta := h.proxy.GetOAuthMetadata(id)
	if meta == nil || meta.Issuer == "" {
		writeError(w, http.StatusBadRequest, "No OAuth metadata found for this server")
		return
	}
	if err := h.store.DeleteOAuthRegistration(meta.Issuer); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete registration")
		return
	}
	// Invalidate the metadata cache so it's rediscovered next time
	h.proxy.InvalidateOAuthMetadataCache(id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Disabled Tools ---

func (h *Handlers) handleListDisabledTools(w http.ResponseWriter, r *http.Request) {
	compoundID := r.URL.Query().Get("compound_id")
	var tools []*models.DisabledTool
	var err error
	if compoundID != "" {
		tools, err = h.store.ListDisabledToolsByCompound(compoundID)
	} else {
		tools, err = h.store.ListDisabledTools()
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list disabled tools")
		return
	}
	if tools == nil {
		tools = []*models.DisabledTool{}
	}
	writeJSON(w, http.StatusOK, tools)
}

func (h *Handlers) handleCreateDisabledTool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ToolName   string  `json:"tool_name"`
		CompoundID *string `json:"compound_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.ToolName == "" {
		writeError(w, http.StatusBadRequest, "tool_name is required")
		return
	}
	dt, err := h.store.CreateDisabledTool(req.ToolName, req.CompoundID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to disable tool: %v", err))
		return
	}
	writeJSON(w, http.StatusCreated, dt)
}

func (h *Handlers) handleDeleteDisabledTool(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.store.DeleteDisabledTool(id); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to enable tool")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
