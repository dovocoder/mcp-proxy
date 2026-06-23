package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// JSONRPCRequest is a JSON-RPC 2.0 request.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
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

// Client is a connection to a backend MCP server.
type Client struct {
	transport    string // "stdio" or "http"
	command      string
	args         []string
	env          map[string]string
	url          string
	headers      map[string]string
	timeout      time.Duration
	connectTimeout time.Duration

	mu       sync.Mutex
	tools    []Tool
	status   string
	lastErr  string
	conn     *stdioConn
	httpURL  string
}

// ClientConfig holds the configuration for an MCP client connection.
type ClientConfig struct {
	Transport      string
	Command        string
	Args           []string
	Env            map[string]string
	URL            string
	Headers        map[string]string
	Timeout        int // seconds
	ConnectTimeout int // seconds
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
		timeout:        timeout,
		connectTimeout: connTimeout,
		status:         "disconnected",
	}
}

// Connect establishes the connection to the backend server.
func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch c.transport {
	case "stdio":
		conn, err := newStdioConn(c.command, c.args, c.env, c.connectTimeout)
		if err != nil {
			c.status = "error"
			c.lastErr = err.Error()
			return err
		}
		c.conn = conn
	case "http":
		c.httpURL = c.url
		// Test connection with an initialize request
		_, err := c.httpCall("initialize", nil)
		if err != nil {
			c.status = "error"
			c.lastErr = err.Error()
			return err
		}
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
	case "http":
		return c.httpCall(method, params)
	default:
		return nil, fmt.Errorf("unknown transport: %s", c.transport)
	}
}

func (c *Client) initialize() error {
	params, _ := json.Marshal(map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]interface{}{
			"name":    "mcp-proxy",
			"version": "1.0.0",
		},
	})
	_, err := c.Call("initialize", params)
	return err
}

func (c *Client) discoverTools() error {
	result, err := c.Call("tools/list", nil)
	if err != nil {
		return err
	}
	var tl ToolListResult
	if err := json.Unmarshal(result, &tl); err != nil {
		return fmt.Errorf("failed to parse tools/list result: %w", err)
	}
	c.tools = tl.Tools
	return nil
}

// stdioCall sends a request over stdio transport.
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

// httpCall sends a request over HTTP transport.
func (c *Client) httpCall(method string, params json.RawMessage) (json.RawMessage, error) {
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  method,
		Params:  params,
	}
	body, _ := json.Marshal(req)

	httpReq, err := http.NewRequest("POST", c.httpURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}

	client := &http.Client{Timeout: c.timeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var rpcResp JSONRPCResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return rpcResp.Result, nil
}
