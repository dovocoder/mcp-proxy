package mcp

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// stdioConn manages a subprocess communicating via JSON-RPC over stdin/stdout.
// MCP stdio uses newline-delimited JSON (NDJSON) by default.
type stdioConn struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	nextIDCounter uint64

	mu       sync.Mutex
	pending  map[uint64]chan JSONRPCResponse
	closed   bool
}

func newStdioConn(command string, args []string, env map[string]string, connectTimeout time.Duration) (*stdioConn, error) {
	cmd := exec.Command(command, args...)

	// Set environment
	cmd.Env = append([]string{}, safeEnv()...)
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start command %s: %w", command, err)
	}

	conn := &stdioConn{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		stderr:  stderr,
		pending: make(map[uint64]chan JSONRPCResponse),
	}

	// Start reading responses
	go conn.readLoop()
	// Start reading stderr for debugging
	go conn.readStderr()

	return conn, nil
}

func (c *stdioConn) nextID() uint64 {
	return atomic.AddUint64(&c.nextIDCounter, 1)
}

func (c *stdioConn) sendAndWait(req JSONRPCRequest, timeout time.Duration) (json.RawMessage, error) {
	id, ok := req.ID.(uint64)
	if !ok {
		// fallback for numeric IDs
		switch v := req.ID.(type) {
		case float64:
			id = uint64(v)
		default:
			id = c.nextID()
			req.ID = id
		}
	}

	ch := make(chan JSONRPCResponse, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	data = append(data, '\n')

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("connection closed")
	}
	_, err = c.stdin.Write(data)
	c.mu.Unlock()

	if err != nil {
		return nil, fmt.Errorf("failed to write to stdin: %w", err)
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("RPC error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("request timed out after %s", timeout)
	}
}

func (c *stdioConn) readLoop() {
	scanner := bufio.NewScanner(c.stdout)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024) // 10MB max

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var resp JSONRPCResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			continue // skip non-JSON lines
		}

		// Extract the ID
		var id uint64
		switch v := resp.ID.(type) {
		case float64:
			id = uint64(v)
		case json.Number:
			n, _ := v.Int64()
			id = uint64(n)
		default:
			continue
		}

		c.mu.Lock()
		ch, ok := c.pending[id]
		c.mu.Unlock()
		if ok {
			ch <- resp
		}
	}
}

func (c *stdioConn) readStderr() {
	scanner := bufio.NewScanner(c.stderr)
	for scanner.Scan() {
		// Log stderr for debugging - in production this would go to a logger
		_ = scanner.Text()
	}
}

func (c *stdioConn) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	if c.stdin != nil {
		c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
		c.cmd.Wait()
	}
}

// safeEnv returns a filtered list of safe environment variables for subprocesses.
func safeEnv() []string {
	safeKeys := map[string]bool{
		"PATH": true, "HOME": true, "USER": true, "LANG": true,
		"LC_ALL": true, "TERM": true, "SHELL": true, "TMPDIR": true,
	}
	var result []string
	for _, e := range os.Environ() {
		// Check for XDG_ prefix
		if len(e) > 4 && e[:4] == "XDG_" {
			result = append(result, e)
			continue
		}
		for k := range safeKeys {
			if len(e) > len(k) && e[:len(k)] == k && e[len(k)] == '=' {
				result = append(result, e)
				break
			}
		}
	}
	return result
}

// Suppress unused import warning for binary package
var _ = binary.BigEndian
