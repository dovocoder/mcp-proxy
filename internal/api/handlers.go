package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/agentic/mcp-proxy/internal/auth"
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
}

// New creates a new API Handlers instance.
func New(s *store.Store, p *proxy.Manager, a *auth.AuthService) *Handlers {
	return &Handlers{
		store:         s,
		proxy:         p,
		auth:          a,
		sseManager:    newSSESessionManager(),
		streamManager: newStreamSessionManager(),
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
		ID:        uuid.NewString(),
		Name:      req.Name,
		Description: req.Description,
		CreatedAt: time.Now(),
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

	writeJSON(w, http.StatusOK, models.CompoundServerWithMembers{
		CompoundServer: *compound,
		Members:         members,
		ToolCount:       toolCount,
	})
}

func (h *Handlers) handleUpdateCompound(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req models.UpdateCompoundRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	if err := h.store.UpdateCompound(id, req.Name, req.Description); err != nil {
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

	// Verify both exist
	if _, err := h.store.GetCompound(compoundID); err != nil {
		writeError(w, http.StatusNotFound, "Compound server not found")
		return
	}
	if _, err := h.store.GetServer(serverID); err != nil {
		writeError(w, http.StatusNotFound, "Server not found")
		return
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
