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
	writeMu   sync.Mutex // serializes writes to ResponseWriter to prevent corruption
}

type sseSessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*sseSession
}

// maxSessions limits the number of concurrent SSE/stream sessions per type
// to prevent resource exhaustion from excessive connections.
const maxSessions = 1000

func newSSESessionManager() *sseSessionManager {
	sm := &sseSessionManager{sessions: make(map[string]*sseSession)}
	go sm.cleanupLoop()
	return sm
}

// cleanupLoop periodically removes expired SSE sessions to prevent memory leaks.
func (sm *sseSessionManager) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		sm.mu.Lock()
		for id, s := range sm.sessions {
			if time.Since(s.createdAt) > sessionTTL {
				close(s.done)
				delete(sm.sessions, id)
			}
		}
		sm.mu.Unlock()
	}
}

func (sm *sseSessionManager) generateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID — ensures uniqueness even if crypto/rand fails
		log.Printf("WARNING: crypto/rand failed in sseSessionManager.generateID: %v — using fallback", err)
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
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

// broadcastNotification sends a JSON-RPC notification to all active legacy SSE sessions.
func (sm *sseSessionManager) broadcastNotification(method string, params json.RawMessage) {
	notif := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		notif["params"] = params
	}
	data, _ := json.Marshal(notif)

	sm.mu.RLock()
	defer sm.mu.RUnlock()
	for _, s := range sm.sessions {
		s.sendMessage(string(data))
	}
}

func (s *sseSession) send(event string, data string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, data)
	if err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

func (s *sseSession) sendMessage(data string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
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
	if !h.enforceCompoundScope(r, id, "") {
		writeError(w, http.StatusForbidden, "API key is not scoped to this server")
		return
	}
	h.sseConnect(w, r, proxy.Scope{ServerID: id})
}

func (h *Handlers) handleSSEConnectCompound(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := h.store.GetCompound(id); err != nil {
		writeError(w, http.StatusNotFound, "Compound server not found")
		return
	}
	if !h.enforceCompoundScope(r, "", id) {
		writeError(w, http.StatusForbidden, "API key is not scoped to this compound")
		return
	}
	h.sseConnect(w, r, proxy.Scope{CompoundID: id})
}

func (h *Handlers) sseConnect(w http.ResponseWriter, r *http.Request, scope proxy.Scope) {
	// Limit concurrent SSE sessions to prevent resource exhaustion
	h.sseManager.mu.RLock()
	sessionCount := len(h.sseManager.sessions)
	h.sseManager.mu.RUnlock()
	if sessionCount >= maxSessions {
		writeError(w, http.StatusServiceUnavailable, "Too many concurrent SSE sessions")
		return
	}

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
	if !h.enforceCompoundScope(r, id, "") {
		writeError(w, http.StatusForbidden, "API key is not scoped to this server")
		return
	}
	h.sseMessage(w, r, proxy.Scope{ServerID: id})
}

func (h *Handlers) handleSSEMessageCompound(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := h.store.GetCompound(id); err != nil {
		writeError(w, http.StatusNotFound, "Compound server not found")
		return
	}
	if !h.enforceCompoundScope(r, "", id) {
		writeError(w, http.StatusForbidden, "API key is not scoped to this compound")
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
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON-RPC request")
		return
	}

	// Use the session's scope
	// Notifications (no ID) get 202 Accepted — process asynchronously, no response on SSE
	if req.ID == nil {
		go h.proxy.HandleJSONRPC(r.Context(), req, sess.scope)
		w.WriteHeader(http.StatusAccepted)
		return
	}

	result, err := h.proxy.HandleJSONRPC(r.Context(), req, sess.scope)

	var resp mcp.JSONRPCResponse
	if err != nil {
		resp = mcp.JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &mcp.RPCError{Code: mcp.ErrCodeInternalError, Message: "internal error"},
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

// BroadcastToolsListChanged sends notifications/tools/list_changed to all connected SSE clients.
// Called when servers connect/disconnect or when tools are modified.
func (h *Handlers) BroadcastToolsListChanged() {
	// Broadcast to streamable HTTP sessions (GET /api/mcp streams)
	h.streamManager.broadcastNotification("notifications/tools/list_changed", nil)
	// Broadcast to legacy SSE sessions
	h.sseManager.broadcastNotification("notifications/tools/list_changed", nil)
}

// BroadcastResourcesListChanged sends notifications/resources/list_changed to all connected SSE clients.
func (h *Handlers) BroadcastResourcesListChanged() {
	h.streamManager.broadcastNotification("notifications/resources/list_changed", nil)
	h.sseManager.broadcastNotification("notifications/resources/list_changed", nil)
}

// --- Streamable HTTP transport (MCP spec 2025-11-25) ---
// POST   /api/mcp                    — global
// POST   /api/servers/{id}/mcp       — per-server
// POST   /api/compounds/{id}/mcp     — per-compound
// GET    same paths                  — SSE notification stream
// DELETE same paths                  — terminate session

// sessionTTL is the maximum lifetime of an idle stream session.
const sessionTTL = 30 * time.Minute

// validProtocolVersions lists MCP protocol versions the proxy accepts.
var validProtocolVersions = map[string]bool{
	mcp.ProtocolVersionLatest: true,
	mcp.ProtocolVersionLegacy: true,
}

func (h *Handlers) handleStreamableHTTP(w http.ResponseWriter, r *http.Request, scope proxy.Scope) {
	// Set MCP-Protocol-Version on ALL responses (POST, GET, DELETE)
	w.Header().Set("MCP-Protocol-Version", mcp.ProtocolVersionLatest)

	// --- Origin header validation (DNS rebinding prevention, spec §Streamable HTTP Security) ---
	if origin := r.Header.Get("Origin"); origin != "" {
		scheme := "https"
		if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
			scheme = "http"
		}
		allowedOrigin := scheme + "://" + r.Host
		if origin != allowedOrigin {
			// Reject invalid Origin with 403 Forbidden
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "Invalid Origin header"})
			return
		}
	}

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
		// Validate session ID if provided — return 404 for expired sessions
		sessionID := r.Header.Get("Mcp-Session-Id")
		if sessionID != "" {
			if _, ok := h.streamManager.get(sessionID); !ok {
				// Session expired or unknown — spec says MUST return 404
				writeError(w, http.StatusNotFound, "Session not found or expired")
				return
			}
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError, "Streaming not supported")
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		// Send initial SSE event with ID to prime reconnection (spec §Resumability)
		initialEventID := h.streamManager.generateID()
		fmt.Fprintf(w, "id: %s\ndata: \n\n", initialEventID)
		flusher.Flush()

		// If client sent Last-Event-ID, replay missed events
		if lastEventID := r.Header.Get("Last-Event-ID"); lastEventID != "" {
			if sess, ok := h.streamManager.get(sessionID); ok && sess != nil {
				sess.replayEvents(lastEventID)
			}
		}

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

	// Validate MCP-Protocol-Version header on non-initialize requests.
	// Per spec: if the server receives a request with an invalid/unsupported
	// MCP-Protocol-Version, it MUST respond with 400 Bad Request.
	// For backwards compat, if no header is present, assume 2025-03-26.
	protocolVersion := r.Header.Get("MCP-Protocol-Version")
	if protocolVersion == "" {
		protocolVersion = mcp.ProtocolVersionLegacy
	}
	if !validProtocolVersions[protocolVersion] {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Unsupported MCP-Protocol-Version: %s", protocolVersion))
		return
	}

	var req mcp.JSONRPCRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON-RPC request")
		return
	}

	// Session management: validate Mcp-Session-Id for non-initialize requests.
	// Per spec: servers that require a session ID SHOULD respond to requests
	// without one (other than initialization) with HTTP 400 Bad Request.
	sessionID := r.Header.Get("Mcp-Session-Id")
	isInitialize := req.Method == "initialize"
	if !isInitialize && sessionID == "" && req.ID != nil {
		// This is a request (not notification) without a session — require one.
		writeError(w, http.StatusBadRequest, "Missing Mcp-Session-Id header")
		return
	}
	// Per spec: if the session was terminated/expired, MUST respond with 404 Not Found.
	if !isInitialize && sessionID != "" && req.ID != nil {
		if _, ok := h.streamManager.get(sessionID); !ok {
			writeError(w, http.StatusNotFound, "Session not found or expired — reinitialize")
			return
		}
	}

	// JSON-RPC notifications (no id) — return 202 Accepted with no body.
	// Per spec: if the input is a JSON-RPC response or notification and the
	// server accepts it, return 202 Accepted.
	if req.ID == nil {
		// Process the notification (e.g. notifications/initialized, notifications/cancelled)
		go h.proxy.HandleJSONRPC(r.Context(), req, scope)
		w.WriteHeader(http.StatusAccepted)
		return
	}

	result, err := h.proxy.HandleJSONRPC(r.Context(), req, scope)
	var resp mcp.JSONRPCResponse
	if err != nil {
		resp = mcp.JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &mcp.RPCError{Code: mcp.ErrCodeInternalError, Message: "internal error"},
		}
	} else {
		resp = mcp.JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
	}

	// Echo or create session ID
	if isInitialize && sessionID == "" {
		sessionID = h.streamManager.generateID()
		// Register the session so it's tracked for GET streams and 404 detection
		if !h.streamManager.registerSession(sessionID, scope) {
			writeError(w, http.StatusServiceUnavailable, "Too many concurrent sessions")
			return
		}
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
		// Send SSE event with ID for resumability support (spec §Resumability).
		eventID := h.streamManager.generateID()
		fmt.Fprintf(w, "id: %s\ndata: %s\n\n", eventID, string(respBytes))
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
	writeMu   sync.Mutex // serializes writes to ResponseWriter
	// eventLog stores recent SSE event IDs and their data for resumability (Last-Event-ID support).
	eventLog []sseEventLog
	// eventLogMu protects eventLog
	eventLogMu sync.Mutex
}

type sseEventLog struct {
	id   string
	data string
}

const maxEventLogSize = 100

type streamSessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*streamSession
}

func newStreamSessionManager() *streamSessionManager {
	sm := &streamSessionManager{sessions: make(map[string]*streamSession)}
	go sm.cleanupLoop()
	return sm
}

// cleanupLoop periodically removes expired sessions to prevent memory leaks.
func (sm *streamSessionManager) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		sm.mu.Lock()
		for id, s := range sm.sessions {
			if time.Since(s.createdAt) > sessionTTL {
				select {
				case <-s.done:
				default:
					close(s.done)
				}
				delete(sm.sessions, id)
			}
		}
		sm.mu.Unlock()
	}
}

func (sm *streamSessionManager) generateID() string {
	// Session IDs are generated using crypto/rand (128 bits) — cryptographically
	// secure and non-deterministic per MCP spec security best practices.
	// Sessions are implicitly bound to the API key used to create them:
	// the API key is validated on every request via APIKeyMiddleware,
	// so even if a session ID is stolen, the attacker still needs a valid
	// API key to make authenticated requests.
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		log.Printf("WARNING: crypto/rand failed in streamSessionManager.generateID: %v — using fallback", err)
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func (sm *streamSessionManager) get(id string) (*streamSession, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	s, ok := sm.sessions[id]
	return s, ok
}

// registerSession creates a session entry without an active SSE writer.
// This is used when a session ID is assigned during initialize (POST)
// so that subsequent requests with that session ID are recognized.
func (sm *streamSessionManager) registerSession(id string, scope proxy.Scope) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if len(sm.sessions) >= maxSessions {
		return false
	}
	if _, exists := sm.sessions[id]; !exists {
		sm.sessions[id] = &streamSession{
			id:        id,
			scope:     scope,
			createdAt: time.Now(),
		}
	}
	return true
}

// isValidSession checks if a session ID is known but expired (for 404 response).
func (sm *streamSessionManager) isKnownButExpired(id string) bool {
	// If the session is not in the active map but the ID looks valid (non-empty,
	// hex-encoded), it was likely a valid session that expired.
	// We check the active map first — if present, it's still alive.
	sm.mu.RLock()
	_, ok := sm.sessions[id]
	sm.mu.RUnlock()
	return !ok && id != ""
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

// broadcastNotification sends a JSON-RPC notification to all active stream sessions.
// Used for notifications/tools/list_changed, notifications/resources/list_changed, etc.
func (sm *streamSessionManager) broadcastNotification(method string, params json.RawMessage) {
	notif := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		notif["params"] = params
	}
	data, _ := json.Marshal(notif)

	sm.mu.RLock()
	defer sm.mu.RUnlock()
	for _, s := range sm.sessions {
		// Append to event log for resumability
		s.eventLogMu.Lock()
		eventID := sm.generateID()
		s.eventLog = append(s.eventLog, sseEventLog{id: eventID, data: string(data)})
		if len(s.eventLog) > maxEventLogSize {
			s.eventLog = s.eventLog[len(s.eventLog)-maxEventLogSize:]
		}
		s.eventLogMu.Unlock()

		// Try to send on the SSE stream
		if s.flusher != nil {
			s.writeMu.Lock()
			fmt.Fprintf(s.w, "id: %s\ndata: %s\n\n", eventID, string(data))
			s.flusher.Flush()
			s.writeMu.Unlock()
		}
	}
}

// replayEvents replays events after the given Last-Event-ID on a stream.
func (s *streamSession) replayEvents(lastEventID string) {
	s.eventLogMu.Lock()
	defer s.eventLogMu.Unlock()

	found := false
	for _, evt := range s.eventLog {
		if found {
			fmt.Fprintf(s.w, "id: %s\ndata: %s\n\n", evt.id, evt.data)
			s.flusher.Flush()
		}
		if evt.id == lastEventID {
			found = true
		}
	}
}
