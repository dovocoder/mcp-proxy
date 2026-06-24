package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/agentic/mcp-proxy/internal/mcp"
	"github.com/agentic/mcp-proxy/internal/proxy"
)

// --- Session management ---

// sseSession represents an active SSE connection (legacy SSE transport).
type sseSession struct {
	id        string
	w         http.ResponseWriter
	flusher   http.Flusher
	done      chan struct{}
	scope     proxy.Scope
	createdAt time.Time
}

type sseSessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*sseSession
}

func newSSESessionManager() *sseSessionManager {
	return &sseSessionManager{sessions: make(map[string]*sseSession)}
}

func (sm *sseSessionManager) generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (sm *sseSessionManager) create(id string, w http.ResponseWriter, f http.Flusher, scope proxy.Scope) *sseSession {
	sess := &sseSession{
		id:        id,
		w:         w,
		flusher:   f,
		done:      make(chan struct{}),
		scope:     scope,
		createdAt: time.Now(),
	}
	sm.mu.Lock()
	sm.sessions[id] = sess
	sm.mu.Unlock()
	return sess
}

func (sm *sseSessionManager) get(id string) (*sseSession, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	s, ok := sm.sessions[id]
	return s, ok
}

func (sm *sseSessionManager) remove(id string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if s, ok := sm.sessions[id]; ok {
		close(s.done)
		delete(sm.sessions, id)
	}
}

func (s *sseSession) send(event string, data string) error {
	_, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, data)
	if err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

func (s *sseSession) sendMessage(data string) error {
	_, err := fmt.Fprintf(s.w, "data: %s\n\n", data)
	if err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// --- Legacy SSE transport ---
// GET /api/sse                    — global SSE stream
// GET /api/servers/{id}/sse       — per-server SSE stream
// GET /api/compounds/{id}/sse     — per-compound SSE stream
// POST /api/messages              — global messages (needs ?sessionId=)
// POST /api/servers/{id}/messages — per-server messages
// POST /api/compounds/{id}/messages — per-compound messages

func (h *Handlers) handleSSEConnectGlobal(w http.ResponseWriter, r *http.Request) {
	scope := extractScopeFromAPIKey(r)
	h.sseConnect(w, r, scope)
}

func (h *Handlers) handleSSEConnectServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := h.store.GetServer(id); err != nil {
		writeError(w, http.StatusNotFound, "Server not found")
		return
	}
	h.sseConnect(w, r, proxy.Scope{ServerID: id})
}

func (h *Handlers) handleSSEConnectCompound(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := h.store.GetCompound(id); err != nil {
		writeError(w, http.StatusNotFound, "Compound not found")
		return
	}
	h.sseConnect(w, r, proxy.Scope{CompoundID: id})
}

func (h *Handlers) sseConnect(w http.ResponseWriter, r *http.Request, scope proxy.Scope) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "Streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	sessionID := h.sseManager.generateID()
	sess := h.sseManager.create(sessionID, w, flusher, scope)
	defer h.sseManager.remove(sessionID)

	log.Printf("SSE session %s connected (scope: server=%s compound=%s)", sessionID, scope.ServerID, scope.CompoundID)

	// Send the endpoint event — the URL the client should POST messages to
	var endpointURL string
	if scope.ServerID != "" {
		endpointURL = fmt.Sprintf("/api/servers/%s/messages?sessionId=%s", scope.ServerID, sessionID)
	} else if scope.CompoundID != "" {
		endpointURL = fmt.Sprintf("/api/compounds/%s/messages?sessionId=%s", scope.CompoundID, sessionID)
	} else {
		endpointURL = fmt.Sprintf("/api/messages?sessionId=%s", sessionID)
	}

	if err := sess.send("endpoint", endpointURL); err != nil {
		log.Printf("Failed to send endpoint event: %v", err)
		return
	}

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			log.Printf("SSE session %s disconnected", sessionID)
			return
		case <-sess.done:
			return
		case <-ticker.C:
			if _, err := fmt.Fprintf(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (h *Handlers) handleSSEMessageGlobal(w http.ResponseWriter, r *http.Request) {
	h.sseMessage(w, r, extractScopeFromAPIKey(r))
}

func (h *Handlers) handleSSEMessageServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := h.store.GetServer(id); err != nil {
		writeError(w, http.StatusNotFound, "Server not found")
		return
	}
	h.sseMessage(w, r, proxy.Scope{ServerID: id})
}

func (h *Handlers) handleSSEMessageCompound(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := h.store.GetCompound(id); err != nil {
		writeError(w, http.StatusNotFound, "Compound not found")
		return
	}
	h.sseMessage(w, r, proxy.Scope{CompoundID: id})
}

func (h *Handlers) sseMessage(w http.ResponseWriter, r *http.Request, fallbackScope proxy.Scope) {
	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "Missing sessionId parameter")
		return
	}

	sess, ok := h.sseManager.get(sessionID)
	if !ok {
		writeError(w, http.StatusNotFound, "Session not found")
		return
	}

	var req mcp.JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON-RPC request")
		return
	}

	// Use the session's scope
	result, err := h.proxy.HandleJSONRPC(r.Context(), req, sess.scope)

	var resp mcp.JSONRPCResponse
	if err != nil {
		resp = mcp.JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &mcp.RPCError{Code: -32603, Message: err.Error()},
		}
	} else {
		resp = mcp.JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
	}

	respBytes, _ := json.Marshal(resp)
	if err := sess.sendMessage(string(respBytes)); err != nil {
		log.Printf("Failed to send SSE message to session %s: %v", sessionID, err)
		writeError(w, http.StatusInternalServerError, "Failed to send response")
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

// --- Streamable HTTP transport (MCP spec 2025-03-26) ---
// POST   /api/mcp                    — global
// POST   /api/servers/{id}/mcp       — per-server
// POST   /api/compounds/{id}/mcp     — per-compound
// GET    same paths                  — SSE notification stream
// DELETE same paths                  — terminate session

func (h *Handlers) handleStreamableHTTP(w http.ResponseWriter, r *http.Request, scope proxy.Scope) {
	// DELETE — terminate session
	if r.Method == http.MethodDelete {
		sessionID := r.Header.Get("Mcp-Session-Id")
		if sessionID != "" {
			h.streamManager.remove(sessionID)
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	// GET — open SSE notification stream
	if r.Method == http.MethodGet {
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError, "Streaming not supported")
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := fmt.Fprintf(w, ": ping\n\n"); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}

	// POST — process JSON-RPC request
	var req mcp.JSONRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON-RPC request")
		return
	}

	// Set MCP-Protocol-Version on all responses
	w.Header().Set("MCP-Protocol-Version", "2025-03-26")

	// JSON-RPC notifications (no id) — return 202 Accepted with no body
	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	result, err := h.proxy.HandleJSONRPC(r.Context(), req, scope)

	var resp mcp.JSONRPCResponse
	if err != nil {
		resp = mcp.JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &mcp.RPCError{Code: -32603, Message: err.Error()},
		}
	} else {
		resp = mcp.JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
	}

	// Echo or create session ID
	sessionID := r.Header.Get("Mcp-Session-Id")
	if req.Method == "initialize" && sessionID == "" {
		sessionID = h.streamManager.generateID()
	}
	if sessionID != "" {
		w.Header().Set("Mcp-Session-Id", sessionID)
	}

	// Check if client wants SSE response
	accept := r.Header.Get("Accept")
	if accept == "" {
		accept = "application/json"
	}

	if strings.Contains(accept, "text/event-stream") {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSON(w, http.StatusOK, resp)
			return
		}

		respBytes, _ := json.Marshal(resp)
		fmt.Fprintf(w, "data: %s\n\n", string(respBytes))
		flusher.Flush()
	} else {
		writeJSON(w, http.StatusOK, resp)
	}
}

// --- Streamable HTTP session manager ---

type streamSession struct {
	id        string
	w         http.ResponseWriter
	flusher   http.Flusher
	done      chan struct{}
	scope     proxy.Scope
	createdAt time.Time
}

type streamSessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*streamSession
}

func newStreamSessionManager() *streamSessionManager {
	return &streamSessionManager{sessions: make(map[string]*streamSession)}
}

func (sm *streamSessionManager) generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (sm *streamSessionManager) get(id string) (*streamSession, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	s, ok := sm.sessions[id]
	return s, ok
}

func (sm *streamSessionManager) remove(id string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if s, ok := sm.sessions[id]; ok {
		select {
		case <-s.done:
		default:
			close(s.done)
		}
		delete(sm.sessions, id)
	}
}
