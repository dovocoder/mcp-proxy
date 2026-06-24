package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

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

// Tool represents an MCP tool definition.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// ToolListResult is the result of tools/list.
type ToolListResult struct {
	Tools []Tool `json:"tools"`
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
	Timeout        int // seconds
	ConnectTimeout int // seconds
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
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]interface{}{
			"name":    "mcp-proxy",
			"version": "1.0.0",
		},
	})
	_, err := c.Call("initialize", params)
	return err
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

		httpReq, err := http.NewRequest("POST", c.httpURL, strings.NewReader(string(body)))
		if err != nil {
			return err
		}
		c.setHeaders(httpReq)
		c.setSessionHeader(httpReq)

		client := &http.Client{Timeout: c.timeout}
		resp, err := client.Do(httpReq)
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
	req.Header.Set("MCP-Protocol-Version", "2025-03-26")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}
}

// setSessionHeader sets the Mcp-Session-Id header if we have one.
func (c *Client) setSessionHeader(req *http.Request) {
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
}

// closeSession sends a DELETE request to close the MCP session.
func (c *Client) closeSession() {
	if c.httpURL == "" || c.sessionID == "" {
		return
	}
	req, err := http.NewRequest("DELETE", c.httpURL, nil)
	if err != nil {
		return
	}
	c.setHeaders(req)
	c.setSessionHeader(req)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
	c.sessionID = ""
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

	httpReq, err := http.NewRequest("POST", c.httpURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	c.setHeaders(httpReq)
	c.setSessionHeader(httpReq)

	log.Printf("[MCP] → %s (id=%d, url=%s, auth=%v)", method, reqID, c.httpURL, c.authToken != "")

	client := &http.Client{Timeout: c.timeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		log.Printf("[MCP] ✗ %s (id=%d) network error: %v", method, reqID, err)
		return nil, err
	}
	defer resp.Body.Close()

	log.Printf("[MCP] ← %s (id=%d) HTTP %d, content-type=%s, session=%q", method, reqID, resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("Mcp-Session-Id"))

	// Handle non-200 responses
	if resp.StatusCode == http.StatusNotFound {
		// Session expired — reset and return error
		c.mu.Lock()
		c.sessionID = ""
		c.mu.Unlock()
		return nil, fmt.Errorf("session expired (HTTP 404) — reconnection needed")
	}
	if resp.StatusCode == http.StatusUnauthorized {
		respBody, _ := io.ReadAll(resp.Body)
		log.Printf("[MCP] ✗ %s (id=%d) 401 Unauthorized: %s", method, reqID, string(respBody))
		return nil, fmt.Errorf("unauthorized (HTTP 401) — check your auth token. Server response: %s", string(respBody))
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		respBody, _ := io.ReadAll(resp.Body)
		log.Printf("[MCP] ✗ %s (id=%d) HTTP %d: %s", method, reqID, resp.StatusCode, string(respBody))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	// Capture session ID from initialize response
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.mu.Lock()
		c.sessionID = sid
		c.mu.Unlock()
	}

	contentType := resp.Header.Get("Content-Type")

	// Handle SSE streaming response
	if strings.Contains(contentType, "text/event-stream") {
		result, err := c.readSSEWithTimeout(resp.Body, reqID, 15*time.Second)
		if err != nil {
			log.Printf("[MCP] ✗ %s (id=%d) SSE error: %v", method, reqID, err)
		}
		return result, err
	}

	// Handle simple JSON response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	log.Printf("[MCP] %s (id=%d) body: %d bytes, first 200: %s", method, reqID, len(respBody), truncateLog(string(respBody), 200))

	var rpcResp JSONRPCResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		log.Printf("[MCP] ✗ %s (id=%d) JSON parse error: %v (body len=%d)", method, reqID, err, len(respBody))
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	if rpcResp.Error != nil {
		log.Printf("[MCP] ✗ %s (id=%d) RPC error %d: %s", method, reqID, rpcResp.Error.Code, rpcResp.Error.Message)
		return nil, fmt.Errorf("RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return rpcResp.Result, nil
}

// readSSEWithTimeout reads an SSE stream line-by-line with a per-line timeout.
// Returns as soon as a matching JSON-RPC response is found.
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

		// Read one line with a timeout
		type lineResult struct {
			line string
			err  error
		}
		ch := make(chan lineResult, 1)
		go func() {
			line, err := reader.ReadString('\n')
			ch <- lineResult{line, err}
		}()

		select {
		case res := <-ch:
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

func truncateLog(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
