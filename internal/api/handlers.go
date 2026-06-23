package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/agentic/mcp-proxy/internal/auth"
	"github.com/agentic/mcp-proxy/internal/mcp"
	"github.com/agentic/mcp-proxy/internal/models"
	"github.com/agentic/mcp-proxy/internal/proxy"
	"github.com/agentic/mcp-proxy/internal/store"
	"github.com/google/uuid"
)

// Handlers holds all HTTP handler dependencies.
type Handlers struct {
	store      *store.Store
	proxy      *proxy.Manager
	auth       *auth.AuthService
}

// New creates a new API Handlers instance.
func New(s *store.Store, p *proxy.Manager, a *auth.AuthService) *Handlers {
	return &Handlers{store: s, proxy: p, auth: a}
}

// SetupRoutes registers all API routes on the given mux.
func (h *Handlers) SetupRoutes(mux *http.ServeMux) {
	// Auth routes (no auth required)
	mux.HandleFunc("POST /api/auth/login", h.handleLogin)

	// MCP proxy endpoint (API key auth)
	mux.Handle("POST /api/mcp", h.auth.APIKeyMiddleware(http.HandlerFunc(h.handleMCPProxy)))
	mux.Handle("GET /api/mcp/sse", h.auth.APIKeyMiddleware(http.HandlerFunc(h.handleSSE)))

	// Admin routes (JWT auth)
	adminMux := http.NewServeMux()
	adminMux.HandleFunc("GET /api/servers", h.handleListServers)
	adminMux.HandleFunc("POST /api/servers", h.handleCreateServer)
	adminMux.HandleFunc("GET /api/servers/{id}", h.handleGetServer)
	adminMux.HandleFunc("PUT /api/servers/{id}", h.handleUpdateServer)
	adminMux.HandleFunc("DELETE /api/servers/{id}", h.handleDeleteServer)
	adminMux.HandleFunc("POST /api/servers/{id}/reconnect", h.handleReconnectServer)

	adminMux.HandleFunc("GET /api/keys", h.handleListAPIKeys)
	adminMux.HandleFunc("POST /api/keys", h.handleCreateAPIKey)
	adminMux.HandleFunc("DELETE /api/keys/{id}", h.handleDeleteAPIKey)

	adminMux.HandleFunc("GET /api/tools", h.handleListTools)
	adminMux.HandleFunc("GET /api/dashboard", h.handleDashboard)

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

// --- MCP Proxy ---

func (h *Handlers) handleMCPProxy(w http.ResponseWriter, r *http.Request) {
	var req mcp.JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON-RPC request")
		return
	}

	result, err := h.proxy.HandleJSONRPC(r.Context(), req)
	if err != nil {
		resp := mcp.JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &mcp.RPCError{
				Code:    -32603,
				Message: err.Error(),
			},
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	resp := mcp.JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  result,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handlers) handleSSE(w http.ResponseWriter, r *http.Request) {
	// Basic SSE support for MCP streaming
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "Streaming not supported")
		return
	}

	// Send endpoint event
	fmt.Fprintf(w, "event: endpoint\ndata: /api/mcp\n\n")
	flusher.Flush()

	// Keep connection alive
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
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
		ID:        uuid.NewString(),
		Name:      req.Name,
		KeyHash:   keyHash,
		KeyPrefix: keyPrefix,
		Scopes:    req.Scopes,
		Active:    true,
		CreatedAt: time.Now(),
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
		"id":         apiKey.ID,
		"name":       apiKey.Name,
		"key":        keyString,
		"key_prefix": keyPrefix,
		"scopes":     apiKey.Scopes,
		"expires_at": apiKey.ExpiresAt,
		"created_at": apiKey.CreatedAt,
		"message":    "Save this key — it will not be shown again",
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

// SplitPath splits a URL path into segments.
func SplitPath(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}
