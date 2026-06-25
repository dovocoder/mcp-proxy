// Command mcp-cli is a command-line client for the MCP proxy.
//
// It communicates with the proxy's Streamable HTTP endpoint at /api/mcp,
// using an API key for authentication (Authorization: Bearer <key>).
//
// Configuration is read from ~/.mcp-cli.json first, then ./.env in the
// current directory. The --api-key and --proxy-url flags override config.
//
// Usage:
//
//	mcp-cli init                        Interactive setup
//	mcp-cli tools list                  List available tools
//	mcp-cli tools call <name> [args]    Call a tool (args is JSON)
//	mcp-cli resources list              List resources
//	mcp-cli resources read <uri>        Read a resource
//	mcp-cli env load [file]             Print export statements for a .env file
//	mcp-cli env set <key> <value>       Set a key in the local .env file
//	mcp-cli api-key                     Show the current API key
//
// Flags:
//
//	--api-key    Override the configured API key
//	--proxy-url  Override the configured proxy base URL
//	--env        Path to the .env file (default: ./.env)
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// MCP protocol version supported by the proxy (see internal/mcp/protocol.go).
const (
	protocolVersionLatest = "2025-11-25"
	protocolVersionLegacy = "2025-03-26"
)

// config holds the resolved proxy URL and API key.
type config struct {
	ProxyURL string `json:"proxy_url"`
	APIKey   string `json:"api_key"`
}

// jsonRPCRequest is a JSON-RPC 2.0 request.
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonRPCResponse is a JSON-RPC 2.0 response.
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func main() {
	// Global flags — parsed before subcommand dispatch.
	apiKeyFlag := flag.String("api-key", "", "API key (overrides config)")
	proxyURLFlag := flag.String("proxy-url", "", "Proxy base URL (overrides config)")
	envFlag := flag.String("env", "./.env", "Path to .env file")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	// Global flags are only meaningful for commands that talk to the proxy,
	// but we parse them once up front so they work in any position before the
	// subcommand. Re-parse without the global flag set when subcommands have
	// their own flags.
	cfg := loadConfig(*proxyURLFlag, *apiKeyFlag, *envFlag)

	cmd := args[0]
	rest := args[1:]

	switch cmd {
	case "init":
		runInit(*envFlag)
	case "tools":
		runTools(cfg, rest)
	case "resources":
		runResources(cfg, rest)
	case "env":
		runEnv(*envFlag, rest)
	case "api-key":
		runAPIKey(cfg)
	case "-h", "--help", "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprint(os.Stderr, `mcp-cli — CLI for the MCP proxy

Usage:
  mcp-cli init                         Interactive setup
  mcp-cli tools list                   List available tools
  mcp-cli tools call <name> [args]     Call a tool (args is a JSON object string)
  mcp-cli resources list               List resources
  mcp-cli resources read <uri>         Read a resource
  mcp-cli env load [file]              Print export statements for a .env file
  mcp-cli env set <key> <value>        Set a key in the local .env file
  mcp-cli api-key                      Show the current API key

Flags:
  --api-key <key>      Override the configured API key
  --proxy-url <url>    Override the configured proxy base URL
  --env <path>          Path to .env file (default: ./.env)
`)
}

// ---------------------------------------------------------------------------
// Config loading
// ---------------------------------------------------------------------------

// loadConfig resolves the proxy URL and API key from (in order):
//  1. ~/.mcp-cli.json
//  2. ./.env (or the path given by --env)
//  3. Flag overrides (--api-key / --proxy-url)
//
// Flag values always win when non-empty. Environment variables
// MCP_PROXY_URL and MCP_API_KEY are checked as a final fallback.
func loadConfig(proxyURLOverride, apiKeyOverride, envPath string) config {
	cfg := config{}

	// 1. ~/.mcp-cli.json
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		path := filepath.Join(home, ".mcp-cli.json")
		if data, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(data, &cfg) // best-effort; ignore malformed file
		}
	}

	// 2. .env file
	envVars := parseEnvFile(envPath)
	if cfg.ProxyURL == "" {
		cfg.ProxyURL = envVars["MCP_PROXY_URL"]
	}
	if cfg.APIKey == "" {
		cfg.APIKey = envVars["MCP_API_KEY"]
	}

	// 3. Real environment variables (MCP_PROXY_URL / MCP_API_KEY)
	if cfg.ProxyURL == "" {
		cfg.ProxyURL = os.Getenv("MCP_PROXY_URL")
	}
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("MCP_API_KEY")
	}

	// 4. Flag overrides
	if proxyURLOverride != "" {
		cfg.ProxyURL = proxyURLOverride
	}
	if apiKeyOverride != "" {
		cfg.APIKey = apiKeyOverride
	}

	return cfg
}

// parseEnvFile reads a KEY=value file and returns a map. Lines starting with
// '#' are comments. Surrounding quotes are stripped from values.
func parseEnvFile(path string) map[string]string {
	result := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return result
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		// Strip surrounding quotes.
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') ||
				(val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		result[key] = val
	}
	return result
}

// ---------------------------------------------------------------------------
// init command
// ---------------------------------------------------------------------------

func runInit(envPath string) {
	reader := bufio.NewReader(os.Stdin)

	prompt := func(label, def string) string {
		if def != "" {
			fmt.Printf("%s [%s]: ", label, def)
		} else {
			fmt.Printf("%s: ", label)
		}
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			return def
		}
		return line
	}

	// Load existing values as defaults.
	existing := loadConfig("", "", envPath)

	proxyURL := prompt("Proxy URL (e.g. http://localhost:8080)", existing.ProxyURL)
	apiKey := prompt("API key", existing.APIKey)

	if proxyURL == "" || apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: both proxy URL and API key are required.")
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("Save to:")
	fmt.Println("  1) ~/.mcp-cli.json  (recommended — global)")
	fmt.Println("  2) ./.env           (local — MCP_PROXY_URL / MCP_API_KEY)")
	fmt.Print("Choice [1]: ")
	choice, _ := reader.ReadString('\n')
	choice = strings.TrimSpace(choice)

	if choice == "2" {
		if err := writeEnvFile(envPath, "MCP_PROXY_URL", proxyURL, "MCP_API_KEY", apiKey); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing .env: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Saved to %s\n", envPath)
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error finding home directory: %v\n", err)
			os.Exit(1)
		}
		path := filepath.Join(home, ".mcp-cli.json")
		data, _ := json.MarshalIndent(config{ProxyURL: proxyURL, APIKey: apiKey}, "", "  ")
		if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing config: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Saved to %s\n", path)
	}
}

// writeEnvFile updates (or creates) a .env file, setting the given keys.
// Existing keys are updated in place; new keys are appended.
func writeEnvFile(path string, pairs ...string) error {
	if len(pairs)%2 != 0 {
		return fmt.Errorf("writeEnvFile: odd number of key/value args")
	}
	existing := parseEnvFile(path)
	for i := 0; i < len(pairs); i += 2 {
		existing[pairs[i]] = pairs[i+1]
	}

	// Preserve original ordering of keys that were already in the file, then
	// append any new keys at the end.
	var origOrder []string
	if data, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			eq := strings.IndexByte(line, '=')
			if eq >= 0 {
				origOrder = append(origOrder, strings.TrimSpace(line[:eq]))
			}
		}
	}

	seen := map[string]bool{}
	var out strings.Builder
	// Rewrite the original file line-by-line so comments and blank lines are kept.
	if data, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				out.WriteString(line)
				out.WriteByte('\n')
				continue
			}
			eq := strings.IndexByte(trimmed, '=')
			if eq < 0 {
				out.WriteString(line)
				out.WriteByte('\n')
				continue
			}
			key := strings.TrimSpace(trimmed[:eq])
			if val, ok := existing[key]; ok {
				out.WriteString(key + "=" + val + "\n")
				seen[key] = true
			}
		}
	}
	// Append any keys that weren't already in the file.
	for i := 0; i < len(pairs); i += 2 {
		key := pairs[i]
		if !seen[key] {
			out.WriteString(key + "=" + pairs[i+1] + "\n")
		}
	}
	return os.WriteFile(path, []byte(out.String()), 0600)
}

// ---------------------------------------------------------------------------
// tools command
// ---------------------------------------------------------------------------

func runTools(cfg config, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: mcp-cli tools <list|call> ...")
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		requireConfig(cfg)
		result, err := mcpCall(cfg, "tools/list", json.RawMessage(`{}`))
		if err != nil {
			fatal(err)
		}
		prettyPrint(result)
	case "call":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: mcp-cli tools call <name> [args-json]")
			os.Exit(1)
		}
		requireConfig(cfg)
		name := args[1]
		arguments := json.RawMessage(`{}`)
		if len(args) >= 3 {
			arguments = json.RawMessage(args[2])
			// Validate the JSON early so we get a clear error.
			if !json.Valid(arguments) {
				fmt.Fprintf(os.Stderr, "Error: args-json is not valid JSON: %s\n", args[2])
				os.Exit(1)
			}
		}
		params, _ := json.Marshal(map[string]json.RawMessage{
			"name":      json.RawMessage(`"` + escapeJSONString(name) + `"`),
			"arguments": arguments,
		})
		result, err := mcpCall(cfg, "tools/call", params)
		if err != nil {
			fatal(err)
		}
		prettyPrint(result)
	default:
		fmt.Fprintf(os.Stderr, "unknown tools subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// resources command
// ---------------------------------------------------------------------------

func runResources(cfg config, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: mcp-cli resources <list|read> ...")
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		requireConfig(cfg)
		result, err := mcpCall(cfg, "resources/list", json.RawMessage(`{}`))
		if err != nil {
			fatal(err)
		}
		prettyPrint(result)
	case "read":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: mcp-cli resources read <uri>")
			os.Exit(1)
		}
		requireConfig(cfg)
		uri := args[1]
		params, _ := json.Marshal(map[string]string{"uri": uri})
		result, err := mcpCall(cfg, "resources/read", params)
		if err != nil {
			fatal(err)
		}
		prettyPrint(result)
	default:
		fmt.Fprintf(os.Stderr, "unknown resources subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// env command
// ---------------------------------------------------------------------------

func runEnv(envPath string, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: mcp-cli env <load|set> ...")
		os.Exit(1)
	}
	switch args[0] {
	case "load":
		path := envPath
		if len(args) >= 2 {
			path = args[1]
		}
		vars := parseEnvFile(path)
		if len(vars) == 0 {
			fmt.Fprintf(os.Stderr, "no variables found in %s\n", path)
			os.Exit(1)
		}
		// Print eval-friendly export statements.
		for k, v := range vars {
			fmt.Printf("export %s=%s\n", k, shellQuote(v))
		}
	case "set":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: mcp-cli env set <key> <value>")
			os.Exit(1)
		}
		key, val := args[1], args[2]
		if err := writeEnvFile(envPath, key, val); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", envPath, err)
			os.Exit(1)
		}
		fmt.Printf("Set %s in %s\n", key, envPath)
	default:
		fmt.Fprintf(os.Stderr, "unknown env subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// api-key command
// ---------------------------------------------------------------------------

func runAPIKey(cfg config) {
	if cfg.APIKey == "" {
		fmt.Fprintln(os.Stderr, "No API key configured. Run 'mcp-cli init' or set MCP_API_KEY.")
		os.Exit(1)
	}
	fmt.Println(cfg.APIKey)
}

// ---------------------------------------------------------------------------
// MCP JSON-RPC client
// ---------------------------------------------------------------------------

// mcpCall performs the full MCP handshake (initialize → initialized → request)
// against the proxy and returns the result of the final request.
//
// The proxy's handleStreamableHTTP requires:
//   - MCP-Protocol-Version header on all non-initialize requests
//   - Mcp-Session-Id header on non-initialize requests that have an ID
//
// So we send initialize first to obtain a session ID, then the
// notifications/initialized notification, then the actual request.
func mcpCall(cfg config, method string, params json.RawMessage) (json.RawMessage, error) {
	endpoint := strings.TrimRight(cfg.ProxyURL, "/") + "/api/mcp"

	// 1. initialize
	initParams, _ := json.Marshal(map[string]any{
		"protocolVersion": protocolVersionLatest,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "mcp-cli",
			"version": "1.0.0",
		},
	})
	initReq := jsonRPCRequest{JSONRPC: "2.0", ID: 1, Method: "initialize", Params: initParams}
	sessionID, err := doPost(cfg.APIKey, endpoint, initReq, "")
	if err != nil {
		return nil, fmt.Errorf("initialize: %w", err)
	}

	// 2. notifications/initialized (no ID → 202 Accepted, no body expected)
	notif := struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
	}{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	notifBody, _ := json.Marshal(notif)
	if err := postNotification(cfg.APIKey, endpoint, notifBody, sessionID); err != nil {
		// Non-fatal — some servers don't require it.
		_ = err
	}

	// 3. The actual request
	req := jsonRPCRequest{JSONRPC: "2.0", ID: 2, Method: method, Params: params}
	return doPostForResult(cfg.APIKey, endpoint, req, sessionID)
}

// doPost sends a JSON-RPC request and returns the Mcp-Session-Id from the
// response (used for the initialize handshake). The response body is parsed
// but only the session ID is returned.
func doPost(apiKey, endpoint string, req jsonRPCRequest, sessionID string) (string, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	setCommonHeaders(httpReq, apiKey, sessionID)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	sid := resp.Header.Get("Mcp-Session-Id")
	if sid == "" {
		sid = sessionID // keep existing if server didn't echo
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return sid, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	// We don't need the body here for initialize, but read+close is done by defer.
	_, _ = io.Copy(io.Discard, resp.Body)
	return sid, nil
}

// postNotification sends a JSON-RPC notification (no id). The server should
// respond with 202 Accepted.
func postNotification(apiKey, endpoint string, body []byte, sessionID string) error {
	httpReq, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	setCommonHeaders(httpReq, apiKey, sessionID)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("notification HTTP %d", resp.StatusCode)
	}
	return nil
}

// doPostForResult sends a JSON-RPC request and parses the response, handling
// both application/json and text/event-stream content types.
func doPostForResult(apiKey, endpoint string, req jsonRPCRequest, sessionID string) (json.RawMessage, error) {
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	setCommonHeaders(httpReq, apiKey, sessionID)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	contentType := resp.Header.Get("Content-Type")

	// Handle SSE responses — parse the first data: line containing our response.
	if strings.Contains(contentType, "text/event-stream") {
		return parseSSEResponse(resp.Body, req.ID)
	}

	// Plain JSON response.
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	var rpcResp jsonRPCResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("parsing response: %w (body: %s)", err, string(respBody))
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return rpcResp.Result, nil
}

// parseSSEResponse reads an SSE stream and returns the result from the first
// JSON-RPC response whose id matches reqID.
func parseSSEResponse(body io.Reader, reqID int) (json.RawMessage, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	var dataLines []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			// Event boundary — try to match accumulated data.
			if len(dataLines) > 0 {
				if result, found, err := tryMatchSSE(dataLines, reqID); found {
					return result, err
				}
				dataLines = nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			d := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			dataLines = append(dataLines, d)
			// Try immediate match.
			if result, found, err := tryMatchSSE(dataLines, reqID); found {
				return result, err
			}
		}
		// Ignore other SSE fields (event:, id:, etc.)
	}

	// Try remaining data after stream ends.
	if len(dataLines) > 0 {
		if result, found, err := tryMatchSSE(dataLines, reqID); found {
			return result, err
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("SSE read error: %w", err)
	}
	return nil, fmt.Errorf("no matching JSON-RPC response in SSE stream")
}

func tryMatchSSE(dataLines []string, reqID int) (json.RawMessage, bool, error) {
	data := strings.Join(dataLines, "\n")
	var rpcResp jsonRPCResponse
	if err := json.Unmarshal([]byte(data), &rpcResp); err != nil {
		return nil, false, nil // not valid JSON — skip
	}
	if rpcResp.ID != reqID {
		return nil, false, nil
	}
	if rpcResp.Error != nil {
		return nil, true, fmt.Errorf("RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return rpcResp.Result, true, nil
}

// setCommonHeaders sets the headers required by the proxy on every request.
func setCommonHeaders(req *http.Request, apiKey, sessionID string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", protocolVersionLatest)
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func requireConfig(cfg config) {
	if cfg.ProxyURL == "" {
		fmt.Fprintln(os.Stderr, "Error: proxy URL not configured. Run 'mcp-cli init' or set MCP_PROXY_URL.")
		os.Exit(1)
	}
	if cfg.APIKey == "" {
		fmt.Fprintln(os.Stderr, "Error: API key not configured. Run 'mcp-cli init' or set MCP_API_KEY.")
		os.Exit(1)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}

// prettyPrint pretty-prints a JSON value. If the input isn't valid JSON it's
// printed as-is.
func prettyPrint(data json.RawMessage) {
	var v interface{}
	if err := json.Unmarshal(data, &v); err == nil {
		out, _ := json.MarshalIndent(v, "", "  ")
		fmt.Println(string(out))
		return
	}
	fmt.Println(string(data))
}

// shellQuote wraps a value in single quotes for safe shell usage, escaping
// any embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// escapeJSONString escapes a string so it's safe inside a JSON string literal.
func escapeJSONString(s string) string {
	b, _ := json.Marshal(s)
	// json.Marshal returns a quoted string; strip the surrounding quotes.
	return string(b[1 : len(b)-1])
}
