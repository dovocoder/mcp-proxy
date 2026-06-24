package api

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/agentic/mcp-proxy/internal/auth"
	"github.com/agentic/mcp-proxy/internal/crypto"
	"github.com/agentic/mcp-proxy/internal/memory"
	"github.com/agentic/mcp-proxy/internal/models"
	"github.com/agentic/mcp-proxy/internal/proxy"
	"github.com/agentic/mcp-proxy/internal/store"
	"github.com/google/uuid"
)

// Handlers holds all HTTP handler dependencies.
type Handlers struct {
	store         *store.Store
	proxy         *proxy.Manager
	auth          *auth.AuthService
	sseManager    *sseSessionManager
	streamManager *streamSessionManager
	masterKey     [32]byte
}

// New creates a new API Handlers instance.
func New(s *store.Store, p *proxy.Manager, a *auth.AuthService) *Handlers {
	// Derive master key for at-rest env var encryption.
	// Prefer ENCRYPTION_KEY, fall back to the JWT secret.
	encKey := os.Getenv("ENCRYPTION_KEY")
	if encKey == "" {
		encKey = a.JWTSecret()
	}
	return &Handlers{
		store:         s,
		proxy:         p,
		auth:          a,
		sseManager:    newSSESessionManager(),
		streamManager: newStreamSessionManager(),
		masterKey:     crypto.DeriveKey(encKey),
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
	mux.HandleFunc("POST /api/auth/login", h.handleLogin)

	// OAuth callback (no auth — browser redirect)
	mux.HandleFunc("GET /api/oauth/callback", h.handleOAuthCallback)

	// OIDC routes (no auth — browser redirect flow)
	mux.HandleFunc("GET /api/auth/oidc/status", h.handleOIDCStatus)
	mux.HandleFunc("GET /api/auth/oidc/login", h.handleOIDCLogin)
	mux.HandleFunc("GET /api/auth/oidc/callback", h.handleOIDCCallback)

	// Protected Resource Metadata (RFC 9728) — for MCP client OAuth discovery
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", h.handleProtectedResourceMetadata)

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
	adminMux.HandleFunc("POST /api/servers/{id}/auth", h.handleInitiateAuth)
	adminMux.HandleFunc("GET /api/servers/{id}/auth-status", h.handleAuthStatus)
	adminMux.HandleFunc("POST /api/servers/{id}/device-auth", h.handleInitiateDeviceAuth)
	adminMux.HandleFunc("POST /api/servers/{id}/device-auth/poll", h.handlePollDeviceAuth)
	adminMux.HandleFunc("DELETE /api/servers/{id}/device-auth", h.handleCancelDeviceAuth)

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

	// Env var export (API key auth)
	mux.Handle("GET /api/env-vars/export", h.auth.APIKeyMiddleware(http.HandlerFunc(h.handleExportEnvVars)))

	mux.Handle("/api/", h.auth.JWTMiddleware(adminMux))
}

// --- Auth ---

func (h *Handlers) handleLogin(w http.ResponseWriter, r *http.Request) {
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
		CompoundServer:   *compound,
		Members:           members,
		ToolCount:         totalToolCount,
		ServerToolCount:   toolCount,
		MemoryToolCount:   memoryToolCount,
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

func (h *Handlers) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")

	if state == "" || code == "" {
		writeError(w, http.StatusBadRequest, "Missing state or code parameter")
		return
	}

	if err := h.proxy.HandleAuthCallback(state, code); err != nil {
		// Return an HTML page so the browser shows a readable result
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `<!DOCTYPE html><html><body><h2>Authentication Failed</h2><p>%s</p><p>You can close this window.</p></body></html>`, err.Error())
		return
	}

	// Return success HTML page
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`<!DOCTYPE html><html><body><h2>✅ Authentication Successful</h2><p>You can close this window and return to MCP Proxy.</p><script>setTimeout(() => window.close(), 3000);</script></body></html>`))
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

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":     status,
		"has_tokens": hasTokens,
		"expired":    expired,
	})
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
		"completed":  hasTokens,
		"expired":    expired,
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

// handleOIDCStatus returns whether OIDC is configured.
func (h *Handlers) handleOIDCStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{
		"enabled": h.auth.HasOIDC(),
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
	http.SetCookie(w, &http.Cookie{
		Name:     "oidc_state",
		Value:    state,
		Path:     "/",
		MaxAge:   300, // 5 minutes
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
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
		writeError(w, http.StatusUnauthorized, fmt.Sprintf("Token exchange failed: %v", err))
		return
	}

	// Fetch user info
	userInfo, err := provider.UserInfo(token.AccessToken)
	if err != nil {
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

	// Redirect to frontend with token
	// The frontend will read the token from the URL and store it
	frontendURL := "/?token=" + jwtToken
	http.Redirect(w, r, frontendURL, http.StatusFound)
}

// handleProtectedResourceMetadata returns RFC 9728 Protected Resource Metadata.
// MCP clients use this to discover the authorization server (OIDC issuer).
func (h *Handlers) handleProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	resourceURL := fmt.Sprintf("%s://%s", scheme, r.Host)

	resp := map[string]interface{}{
		"resource": resourceURL,
	}

	if h.auth.HasOIDC() {
		resp["authorization_servers"] = []string{h.auth.OIDC().Issuer()}
		// Also include OAuth2 metadata for clients that expect it
		resp["bearer_methods"] = []string{"header"}
	}

	writeJSON(w, http.StatusOK, resp)
}
