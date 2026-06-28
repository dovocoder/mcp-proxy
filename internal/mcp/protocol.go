package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agentic/mcp-proxy/internal/ssrf"
)

// ProtocolVersionLatest is the latest MCP protocol version supported.
const ProtocolVersionLatest = "2025-11-25"

// ProtocolVersionLegacy is the older protocol version (2025-03-26).
// Used for backwards compatibility with servers that haven't upgraded yet.
const ProtocolVersionLegacy = "2025-03-26"

// supportedProtocolVersions lists all protocol versions the proxy can negotiate.
var supportedProtocolVersions = map[string]bool{
	ProtocolVersionLatest: true,
	ProtocolVersionLegacy: true,
}

// NegotiateProtocolVersion selects the highest protocol version both sides support.
// If the client requests a version we support, we echo it back.
// Otherwise we fall back to the latest version we support.
func NegotiateProtocolVersion(clientVersion string) string {
	if supportedProtocolVersions[clientVersion] {
		return clientVersion
	}
	return ProtocolVersionLatest
}

// JSONRPCRequest is a JSON-RPC 2.0 request.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCNotification is a JSON-RPC 2.0 notification (no ID).
type JSONRPCNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse is a JSON-RPC 2.0 response.
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Error implements the error interface so RPCError can be used as a Go error.
func (e *RPCError) Error() string {
	return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message)
}

// Standard JSON-RPC error codes.
const (
	ErrCodeParseError     = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternalError  = -32603
)

// Tool represents an MCP tool definition.
type Tool struct {
	Name        string          `json:"name"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
	Annotations json.RawMessage `json:"annotations,omitempty"`
}

// ToolListResult is the result of tools/list.
type ToolListResult struct {
	Tools      []Tool `json:"tools"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// CallToolResult is the result of tools/call.
// Per MCP spec, tool results MUST include isError field.
type CallToolResult struct {
	Content           []map[string]interface{} `json:"content"`
	IsError           bool                     `json:"isError"`
	StructuredContent json.RawMessage          `json:"structuredContent,omitempty"`
}

// TaskStatus represents the lifecycle state of a task.
type TaskStatus string

const (
	TaskStatusWorking       TaskStatus = "working"
	TaskStatusInputRequired TaskStatus = "input_required"
	TaskStatusCompleted     TaskStatus = "completed"
	TaskStatusFailed        TaskStatus = "failed"
	TaskStatusCancelled     TaskStatus = "cancelled"
)

// IsTerminal returns true if the task is in a terminal state.
func (s TaskStatus) IsTerminal() bool {
	return s == TaskStatusCompleted || s == TaskStatusFailed || s == TaskStatusCancelled
}

// Task represents an MCP task (experimental, 2025-11-25 spec).
type Task struct {
	TaskID        string     `json:"taskId"`
	Status        TaskStatus `json:"status"`
	StatusMessage string     `json:"statusMessage,omitempty"`
	CreatedAt     string     `json:"createdAt"`              // ISO 8601
	LastUpdatedAt string     `json:"lastUpdatedAt"`          // ISO 8601
	TTL           *int64     `json:"ttl"`                    // milliseconds, null = unlimited
	PollInterval  *int64     `json:"pollInterval,omitempty"` // milliseconds
}

// CreateTaskResult is the response when a task-augmented request is accepted.
type CreateTaskResult struct {
	Task Task `json:"task"`
}

// ResourceTemplate is a parameterized resource template.
type ResourceTemplate struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// InitializeResult is the result of initialize.
type InitializeResult struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    json.RawMessage `json:"capabilities"`
	ServerInfo      json.RawMessage `json:"serverInfo"`
}

// sseEvent represents a parsed Server-Sent Event.
type sseEvent struct {
	Event string
	Data  string
	ID    string
}

// sharedHTTPClient is a pooled HTTP client for all backend Streamable HTTP connections.
// Uses SSRF-safe transport to prevent connections to private/internal IP ranges.
var sharedHTTPClient = &http.Client{
	Transport: ssrf.SafeTransportWithSettings(20, 5, 10),
}

// Client is a connection to a backend MCP server.
type Client struct {
	transport      string // "stdio", "http", or "streamable-http"
	command        string
	args           []string
	env            map[string]string
	url            string
	headers        map[string]string
	authToken      string
	timeout        time.Duration
	connectTimeout time.Duration
	onStderr       func(string) // callback for stderr lines (stdio transport only)

	mu        sync.Mutex
	sessionMu sync.Mutex // separate mutex for sessionID — avoids deadlock when httpCall is called from Connect (which holds mu)
	tools     []Tool
	status    string
	lastErr   string
	conn      *stdioConn
	httpURL   string
	sessionID string // Mcp-Session-Id for Streamable HTTP
	idCounter uint64
}

// ClientConfig holds the configuration for an MCP client connection.
type ClientConfig struct {
	Transport      string
	Command        string
	Args           []string
	Env            map[string]string
	URL            string
	Headers        map[string]string
	AuthToken      string
	Timeout        int          // seconds
	ConnectTimeout int          // seconds
	OnStderr       func(string) // optional callback for stderr lines (stdio only)
}

// NewClient creates a new MCP client for a backend server.
func NewClient(cfg ClientConfig) *Client {
	timeout := time.Duration(cfg.Timeout) * time.Second
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	connTimeout := time.Duration(cfg.ConnectTimeout) * time.Second
	if connTimeout == 0 {
		connTimeout = 60 * time.Second
	}
	return &Client{
		transport:      cfg.Transport,
		command:        cfg.Command,
		args:           cfg.Args,
		env:            cfg.Env,
		url:            cfg.URL,
		headers:        cfg.Headers,
		authToken:      cfg.AuthToken,
		timeout:        timeout,
		connectTimeout: connTimeout,
		onStderr:       cfg.OnStderr,
		status:         "disconnected",
	}
}

// Connect establishes the connection to the backend server.
func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch c.transport {
	case "stdio":
		conn, err := newStdioConn(c.command, c.args, c.env, c.connectTimeout, c.onStderr)
		if err != nil {
			c.status = "error"
			c.lastErr = err.Error()
			return err
		}
		c.conn = conn
	case "http", "streamable-http":
		c.httpURL = c.url
	default:
		c.status = "error"
		c.lastErr = fmt.Sprintf("unknown transport: %s", c.transport)
		return fmt.Errorf("unknown transport: %s", c.transport)
	}

	// Perform MCP initialize handshake
	if err := c.initialize(); err != nil {
		c.status = "error"
		c.lastErr = err.Error()
		return err
	}

	// Send initialized notification (required by MCP spec)
	if err := c.sendInitialized(); err != nil {
		// Non-fatal — some servers don't require it
	}

	// Discover tools
	if err := c.discoverTools(); err != nil {
		c.status = "error"
		c.lastErr = err.Error()
		return err
	}

	c.status = "connected"
	c.lastErr = ""
	return nil
}

// Disconnect closes the connection.
func (c *Client) Disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.close()
		c.conn = nil
	}

	// For Streamable HTTP, send DELETE to close session
	if (c.transport == "http" || c.transport == "streamable-http") && c.sessionID != "" {
		c.closeSession()
	}

	c.status = "disconnected"
}

// Status returns the current connection status.
func (c *Client) Status() (string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status, c.lastErr
}

// Tools returns the discovered tools.
func (c *Client) Tools() []Tool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tools
}

// CallTool invokes a tool on the backend server.
func (c *Client) CallTool(name string, arguments json.RawMessage) (json.RawMessage, error) {
	params, _ := json.Marshal(map[string]interface{}{
		"name":      name,
		"arguments": arguments,
	})
	return c.Call("tools/call", params)
}

// Call sends a JSON-RPC request to the backend server.
func (c *Client) Call(method string, params json.RawMessage) (json.RawMessage, error) {
	switch c.transport {
	case "stdio":
		return c.stdioCall(method, params)
	case "http", "streamable-http":
		return c.httpCall(method, params)
	default:
		return nil, fmt.Errorf("unknown transport: %s", c.transport)
	}
}

func (c *Client) initialize() error {
	params, _ := json.Marshal(map[string]interface{}{
		"protocolVersion": ProtocolVersionLatest,
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]interface{}{
			"name":    "mcp-proxy",
			"version": "1.0.0",
		},
	})
	result, err := c.Call("initialize", params)
	if err != nil {
		return err
	}

	// Validate the server's protocol version response.
	// If the server negotiated an older version, we accept it for backwards compat.
	var initResp struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(result, &initResp); err == nil && initResp.ProtocolVersion != "" {
		if !supportedProtocolVersions[initResp.ProtocolVersion] {
			return fmt.Errorf("server returned unsupported protocol version: %s", initResp.ProtocolVersion)
		}
		// Re-negotiate: use the server's version if it's older than what we sent
		if initResp.ProtocolVersion != ProtocolVersionLatest {
			log.Printf("[MCP] Server negotiated protocol version %s (we requested %s)", initResp.ProtocolVersion, ProtocolVersionLatest)
		}
	}
	return nil
}

// sendInitialized sends the notifications/initialized notification.
func (c *Client) sendInitialized() error {
	switch c.transport {
	case "stdio":
		notif := JSONRPCNotification{
			JSONRPC: "2.0",
			Method:  "notifications/initialized",
		}
		data, _ := json.Marshal(notif)
		data = append(data, '\n')
		// Note: caller (Connect) already holds c.mu
		if c.conn == nil || c.conn.closed {
			return fmt.Errorf("connection closed")
		}
		_, err := c.conn.stdin.Write(data)
		return err
	case "http", "streamable-http":
		// Send as a notification (no ID) — server returns 202 Accepted
		notif := JSONRPCNotification{
			JSONRPC: "2.0",
			Method:  "notifications/initialized",
		}
		body, _ := json.Marshal(notif)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		httpReq, err := http.NewRequestWithContext(ctx, "POST", c.httpURL, strings.NewReader(string(body)))
		if err != nil {
			return err
		}
		c.setHeaders(httpReq)
		c.setSessionHeader(httpReq)

		resp, err := sharedHTTPClient.Do(httpReq)
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	default:
		return nil
	}
}

func (c *Client) discoverTools() error {
	result, err := c.Call("tools/list", nil)
	if err != nil {
		log.Printf("[MCP] discoverTools failed: %v", err)
		return err
	}
	var tl ToolListResult
	if err := json.Unmarshal(result, &tl); err != nil {
		log.Printf("[MCP] discoverTools: failed to parse result: %v (raw len=%d)", err, len(result))
		return fmt.Errorf("failed to parse tools/list result: %w", err)
	}
	c.tools = tl.Tools
	log.Printf("[MCP] discoverTools: found %d tools", len(c.tools))
	return nil
}

// --- stdio transport ---

func (c *Client) stdioCall(method string, params json.RawMessage) (json.RawMessage, error) {
	if c.conn == nil {
		return nil, fmt.Errorf("stdio connection not established")
	}
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      c.conn.nextID(),
		Method:  method,
		Params:  params,
	}
	return c.conn.sendAndWait(req, c.timeout)
}

// --- HTTP / Streamable HTTP transport ---

// setHeaders sets common headers on an HTTP request.
func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", ProtocolVersionLatest)
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}
}

// setSessionHeader sets the Mcp-Session-Id header if we have one.
func (c *Client) setSessionHeader(req *http.Request) {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
}

// closeSession sends a DELETE request to close the MCP session.
func (c *Client) closeSession() {
	c.sessionMu.Lock()
	sessionID := c.sessionID
	httpURL := c.httpURL
	c.sessionMu.Unlock()
	if httpURL == "" || sessionID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "DELETE", httpURL, nil)
	if err != nil {
		return
	}
	c.setHeaders(req)
	req.Header.Set("Mcp-Session-Id", sessionID)
	resp, err := sharedHTTPClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}
	c.sessionMu.Lock()
	c.sessionID = ""
	c.sessionMu.Unlock()
}

// httpCall sends a JSON-RPC request over Streamable HTTP transport.
// It handles both simple JSON responses and SSE-streamed responses.
func (c *Client) httpCall(method string, params json.RawMessage) (json.RawMessage, error) {
	reqID := atomic.AddUint64(&c.idCounter, 1)

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      reqID,
		Method:  method,
		Params:  params,
	}
	body, _ := json.Marshal(req)

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.httpURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	c.setHeaders(httpReq)
	c.setSessionHeader(httpReq)

	log.Printf("[MCP] → %s (id=%d, url=%s, auth=%v)", method, reqID, c.httpURL, c.authToken != "")

	// Use the shared HTTP client — this enables connection reuse (keep-alive)
	// and prevents the resource leak of creating a new Transport per request.
	// The context handles per-request timeout including connect deadline.
	resp, err := sharedHTTPClient.Do(httpReq)
	if err != nil {
		log.Printf("[MCP] ✗ %s (id=%d) network error: %v", method, reqID, err)
		return nil, err
	}
	defer resp.Body.Close()

	log.Printf("[MCP] ← %s (id=%d) HTTP %d, content-type=%s, session=%q", method, reqID, resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("Mcp-Session-Id"))

	// Handle non-200 responses
	if resp.StatusCode == http.StatusNotFound {
		c.sessionMu.Lock()
		c.sessionID = ""
		c.sessionMu.Unlock()
		return nil, fmt.Errorf("session expired (HTTP 404) — reconnection needed")
	}
	if resp.StatusCode == http.StatusUnauthorized {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		log.Printf("[MCP] ✗ %s (id=%d) 401 Unauthorized: %s", method, reqID, string(respBody))
		return nil, fmt.Errorf("unauthorized (HTTP 401) — check your auth token")
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		log.Printf("[MCP] ✗ %s (id=%d) HTTP %d: %s", method, reqID, resp.StatusCode, string(respBody))
		return nil, fmt.Errorf("HTTP %d from backend server", resp.StatusCode)
	}

	// Capture session ID from initialize response
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sessionMu.Lock()
		c.sessionID = sid
		c.sessionMu.Unlock()
	}

	contentType := resp.Header.Get("Content-Type")

	// For SSE streams, use line-by-line reader directly.
	// io.ReadAll blocks forever because SSE streams never send EOF.
	if strings.Contains(contentType, "text/event-stream") {
		log.Printf("[MCP] %s (id=%d) reading SSE stream...", method, reqID)
		result, err := c.readSSEWithTimeout(resp.Body, reqID, c.timeout)
		if err != nil {
			log.Printf("[MCP] ✗ %s (id=%d) SSE read error: %v", method, reqID, err)
			return nil, err
		}
		log.Printf("[MCP] %s (id=%d) SSE response parsed successfully", method, reqID)
		return result, nil
	}

	// For non-SSE responses (plain JSON), read full body with context deadline
	type readResult struct {
		data []byte
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		// Limit response body to 10MB — prevents OOM from malicious backend servers
		data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		ch <- readResult{data, err}
	}()

	select {
	case res := <-ch:
		if res.err != nil {
			log.Printf("[MCP] ✗ %s (id=%d) read error: %v", method, reqID, res.err)
			return nil, fmt.Errorf("failed to read response: %w", res.err)
		}
		rawBody := res.data
		log.Printf("[MCP] %s (id=%d) body: %d bytes, first 500: %s", method, reqID, len(rawBody), truncateLog(string(rawBody), 500))

		// Parse as JSON
		var rpcResp JSONRPCResponse
		if err := json.Unmarshal(rawBody, &rpcResp); err != nil {
			log.Printf("[MCP] ✗ %s (id=%d) JSON parse error: %v (body len=%d)", method, reqID, err, len(rawBody))
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}
		if rpcResp.Error != nil {
			log.Printf("[MCP] ✗ %s (id=%d) RPC error %d: %s", method, reqID, rpcResp.Error.Code, rpcResp.Error.Message)
			return nil, fmt.Errorf("RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
		}
		return rpcResp.Result, nil

	case <-ctx.Done():
		log.Printf("[MCP] ✗ %s (id=%d) context timeout after %v", method, reqID, c.timeout)
		return nil, fmt.Errorf("timeout reading response body after %v", c.timeout)
	}
}

// readSSEWithTimeout reads an SSE stream line-by-line with a per-line timeout.
// Returns as soon as a matching JSON-RPC response is found.
// Uses a single long-lived reader goroutine to avoid spawning a goroutine per line
// (which would leak goroutines on timeout).
func (c *Client) readSSEWithTimeout(body io.Reader, reqID uint64, timeout time.Duration) (json.RawMessage, error) {
	reader := bufio.NewReaderSize(body, 1024*1024)
	var dataLines []string
	deadline := time.Now().Add(timeout)

	tryMatch := func(d string) (json.RawMessage, bool, error) {
		var rpcResp JSONRPCResponse
		if err := json.Unmarshal([]byte(d), &rpcResp); err != nil {
			return nil, false, nil
		}
		matched := false
		switch id := rpcResp.ID.(type) {
		case float64:
			matched = uint64(id) == reqID
		case string:
			var parsed uint64
			if _, err := fmt.Sscanf(id, "%d", &parsed); err == nil {
				matched = parsed == reqID
			}
		case json.Number:
			if parsed, err := id.Int64(); err == nil {
				matched = uint64(parsed) == reqID
			}
		}
		if !matched {
			return nil, false, nil
		}
		if rpcResp.Error != nil {
			return nil, true, fmt.Errorf("RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
		}
		return rpcResp.Result, true, nil
	}

	// Single long-lived reader goroutine — sends lines on a channel.
	// This avoids spawning a new goroutine per line (which leaks on timeout).
	type lineResult struct {
		line string
		err  error
	}
	lineCh := make(chan lineResult, 1)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			line, err := reader.ReadString('\n')
			select {
			case lineCh <- lineResult{line, err}:
			case <-readerDone:
				return // reader is no longer needed
			}
		}
	}()

	defer func() {
		// Signal the reader goroutine to stop on return.
		select {
		case <-readerDone:
		default:
		}
	}()

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			// Timeout — try to match whatever we have
			if len(dataLines) > 0 {
				d := strings.Join(dataLines, "\n")
				if result, found, err := tryMatch(d); found {
					log.Printf("[MCP] SSE: matched response for reqID=%d (timeout fallback, data len=%d)", reqID, len(d))
					return result, err
				}
			}
			log.Printf("[MCP] SSE: timeout after %v, no response found (data_lines=%d)", timeout, len(dataLines))
			return nil, fmt.Errorf("SSE timeout — server did not send response within %v", timeout)
		}

		select {
		case res := <-lineCh:
			if res.err != nil && res.err != io.EOF {
				// Read error
				if len(dataLines) > 0 {
					d := strings.Join(dataLines, "\n")
					if result, found, err := tryMatch(d); found {
						log.Printf("[MCP] SSE: matched response for reqID=%d (after read err, data len=%d)", reqID, len(d))
						return result, err
					}
				}
				return nil, fmt.Errorf("SSE read error: %w", res.err)
			}

			line := strings.TrimRight(res.line, "\r\n")

			log.Printf("[MCP] SSE line: %q", truncateLog(line, 200))

			if line == "" {
				// Empty line = event boundary
				if len(dataLines) > 0 {
					d := strings.Join(dataLines, "\n")
					dataLines = nil
					if result, found, err := tryMatch(d); found {
						log.Printf("[MCP] SSE: matched response for reqID=%d (event boundary, data len=%d)", reqID, len(d))
						return result, err
					}
				}
			} else if strings.HasPrefix(line, "data:") {
				d := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				dataLines = append(dataLines, d)
				// Try immediate match
				combined := strings.Join(dataLines, "\n")
				if result, found, err := tryMatch(combined); found {
					log.Printf("[MCP] SSE: matched response for reqID=%d (immediate, data len=%d)", reqID, len(combined))
					return result, err
				}
			}

			if res.err == io.EOF {
				// Stream ended — try to match remaining data
				if len(dataLines) > 0 {
					d := strings.Join(dataLines, "\n")
					if result, found, err := tryMatch(d); found {
						log.Printf("[MCP] SSE: matched response for reqID=%d (EOF, data len=%d)", reqID, len(d))
						return result, err
					}
				}
				log.Printf("[MCP] SSE: stream ended, no matching response for reqID=%d", reqID)
				return nil, fmt.Errorf("no matching response found in SSE stream")
			}

		case <-time.After(remaining):
			// Timeout
			if len(dataLines) > 0 {
				d := strings.Join(dataLines, "\n")
				if result, found, err := tryMatch(d); found {
					log.Printf("[MCP] SSE: matched response for reqID=%d (timeout, data len=%d)", reqID, len(d))
					return result, err
				}
			}
			log.Printf("[MCP] SSE: timeout after %v, no response found (data_lines=%d)", timeout, len(dataLines))
			return nil, fmt.Errorf("SSE timeout — server did not send response within %v", timeout)
		}
	}
}

// parseSSEResponse reads an SSE stream and extracts the JSON-RPC response
// matching the given request ID.
func (c *Client) parseSSEResponse(body io.Reader, reqID uint64) (json.RawMessage, error) {
	return c.readSSEWithTimeout(body, reqID, c.timeout)
}

// parseSSEBytes parses SSE event data from a byte slice.
func (c *Client) parseSSEBytes(data []byte, reqID uint64) (json.RawMessage, error) {
	lines := strings.Split(string(data), "\n")

	var dataLines []string

	tryMatch := func(d string) (json.RawMessage, bool, error) {
		var rpcResp JSONRPCResponse
		if err := json.Unmarshal([]byte(d), &rpcResp); err != nil {
			return nil, false, nil
		}
		matched := false
		switch id := rpcResp.ID.(type) {
		case float64:
			matched = uint64(id) == reqID
		case string:
			var parsed uint64
			if _, err := fmt.Sscanf(id, "%d", &parsed); err == nil {
				matched = parsed == reqID
			}
		case json.Number:
			if parsed, err := id.Int64(); err == nil {
				matched = uint64(parsed) == reqID
			}
		}
		if !matched {
			return nil, false, nil
		}
		if rpcResp.Error != nil {
			return nil, true, fmt.Errorf("RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
		}
		return rpcResp.Result, true, nil
	}

	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			if len(dataLines) > 0 {
				d := strings.Join(dataLines, "\n")
				dataLines = nil
				if result, found, err := tryMatch(d); found {
					return result, err
				}
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			d := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			dataLines = append(dataLines, d)
			combined := strings.Join(dataLines, "\n")
			if result, found, err := tryMatch(combined); found {
				return result, err
			}
		}
	}

	if len(dataLines) > 0 {
		d := strings.Join(dataLines, "\n")
		if result, found, err := tryMatch(d); found {
			return result, err
		}
	}

	// Last resort: try parsing the entire body as JSON
	if result, found, err := tryMatch(string(data)); found {
		return result, err
	}

	return nil, fmt.Errorf("no matching response found in SSE data")
}

func truncateLog(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
