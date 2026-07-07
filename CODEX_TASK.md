# Codex Task: Sentry MCP Compatibility + Bug Fixes

## Project
MCP Proxy — a Go-based MCP (Model Context Protocol) proxy server at `/home/agentic/repos/mcp-proxy`.
Module: `github.com/agentic/mcp-proxy`, Go 1.26.4, pure-Go SQLite (modernc.org/sqlite).

## Context

This is an MCP proxy that connects to backend MCP servers (stdio or Streamable HTTP) and exposes them through a unified API. It has OAuth support for backend MCP servers that require authentication (RFC 8414, RFC 9728, RFC 7591, RFC 8707, PKCE).

The proxy already supports:
- OAuth discovery via WWW-Authenticate → Protected Resource Metadata (RFC 9728) → Authorization Server Metadata (RFC 8414)
- Dynamic Client Registration (RFC 7591)
- PKCE (S256)
- Authorization Code flow with callback
- Device Code flow (RFC 8628)
- Token refresh
- Entra ID public client fallback

## Task 1: Sentry MCP Compatibility

Make the proxy fully compatible with `https://mcp.sentry.dev/mcp` and its OAuth flow.

### Sentry's OAuth Setup (verified):

1. **Protected Resource Metadata** at `https://mcp.sentry.dev/.well-known/oauth-protected-resource/mcp`:
```json
{
  "resource": "https://mcp.sentry.dev/mcp",
  "authorization_servers": ["https://mcp.sentry.dev"],
  "scopes_supported": ["org:read","project:write","team:write","event:write"],
  "bearer_methods_supported": ["header"]
}
```

2. **Authorization Server Metadata** at `https://mcp.sentry.dev/.well-known/oauth-authorization-server`:
```json
{
  "issuer": "https://mcp.sentry.dev",
  "authorization_endpoint": "https://mcp.sentry.dev/oauth/authorize",
  "token_endpoint": "https://mcp.sentry.dev/oauth/token",
  "registration_endpoint": "https://mcp.sentry.dev/oauth/register",
  "scopes_supported": ["org:read","project:write","team:write","event:write"],
  "response_types_supported": ["code"],
  "response_modes_supported": ["query"],
  "grant_types_supported": ["authorization_code","refresh_token"],
  "token_endpoint_auth_methods_supported": ["client_secret_basic","client_secret_post","none"],
  "revocation_endpoint": "https://mcp.sentry.dev/oauth/token",
  "code_challenge_methods_supported": ["plain","S256"],
  "client_id_metadata_document_supported": false
}
```

3. **WWW-Authenticate header** on 401:
```
Bearer realm="OAuth", error="invalid_token", error_description="Missing or invalid access token", resource_metadata="https://mcp.sentry.dev/.well-known/oauth-protected-resource/mcp"
```

### What needs to work:
- Adding `https://mcp.sentry.dev/mcp` as a server with `auth_method: oauth`
- OAuth discovery should find the PRM at the path-insertion URL `/.well-known/oauth-protected-resource/mcp`
- Dynamic Client Registration should work with Sentry's `/oauth/register` endpoint
- The authorization code flow should complete and tokens should be stored
- The proxy should connect to Sentry's MCP server with the obtained access token
- Token refresh should work when the access token expires

### Potential issues to check and fix:
1. The `discoverViaProtectedResource` function sends an MCP `initialize` request to trigger a 401. Verify the WWW-Authenticate header parsing correctly extracts `resource_metadata` from Sentry's format (which includes `realm=`, `error=`, `error_description=` alongside `resource_metadata=`).
2. The `extractResourceMetadataURL` function uses simple string parsing — verify it handles the Sentry format correctly where there are multiple parameters in the WWW-Authenticate header.
3. The PRM URL from Sentry is `https://mcp.sentry.dev/.well-known/oauth-protected-resource/mcp` (path-insertion). The current code tries path-based first (`{baseURL}/.well-known/oauth-protected-resource{path}`), which should produce `https://mcp.sentry.dev/.well-known/oauth-protected-resource/mcp` — verify this works.
4. The authorization server is `https://mcp.sentry.dev` (no path). The `discoverAuthServerMetadata` function should try `https://mcp.sentry.dev/.well-known/oauth-authorization-server` — verify this works.
5. Sentry supports `token_endpoint_auth_methods_supported: ["none"]` which means PKCE-only public clients should work. The `RegisterClient` function already sets `token_endpoint_auth_method: "none"` — verify Sentry accepts this.
6. The `resource` parameter (RFC 8707) should be set to `https://mcp.sentry.dev/mcp` (the MCP server URL) in the auth URL and token exchange — verify this is happening correctly.
7. Sentry's `revocation_endpoint` is the same as the token endpoint (`https://mcp.sentry.dev/oauth/token`). This is valid per RFC 7009. No changes needed but verify token revocation isn't broken.

## Task 2: Fix Issues

Review and fix bugs across the codebase. Focus on:

### 2a. OAuth Discovery Issues
- The `discoverViaProtectedResource` function sends a full MCP initialize request to trigger a 401. Some servers may not return a 401 on initialize but instead return 200 or 400. Add fallback: if no 401/WWW-Authenticate, proceed directly to well-known URLs.
- The `extractResourceMetadataURL` function is fragile — it does manual string parsing of the WWW-Authenticate header. Use a proper parser that handles quoted values, multiple parameters, and different whitespace.
- When the WWW-Authenticate header has `error="invalid_token"` and `error_description="..."`, these should be parsed but not cause the discovery to fail.

### 2b. HTTP Transport Issues
- In `httpCall`, when the server returns 401, the error message says "check your auth token" but doesn't trigger OAuth re-authentication. Consider returning a structured error that the proxy layer can use to trigger re-auth.
- The `setHeaders` function always sets `MCP-Protocol-Version` to `ProtocolVersionLatest` ("2025-11-25"). Some servers may only support older versions. The `initialize` handshake should negotiate the version, but subsequent requests should use the negotiated version.
- The SSE reader in `readSSEWithTimeout` has a potential goroutine leak: if the function returns due to timeout, the reader goroutine may block forever on `reader.ReadString`. The `defer` block signals via `readerDone` but the goroutine may still be blocked on the channel send. Consider using `context.WithCancel` to properly cancel the reader.

### 2c. Token Refresh Issues
- In `connectServerWithRetry`, when the OAuth token is expired and refresh fails, the server is still attempted with the expired token. It should skip the connection attempt and mark the server as needing re-authentication.
- The `RefreshToken` function doesn't include the `resource` parameter. Per RFC 8707, if the original authorization included a `resource` parameter, the refresh request should also include it. Store the resource in the OAuth tokens or auth state and include it in refresh requests.

### 2d. SSRF Protection
- The `SafeTransport` blocks all private IPs. However, when the proxy itself is running on a private network and needs to connect to a backend MCP server on the same network (e.g., `http://192.168.1.100:3000/mcp`), the SSRF protection blocks it. Add a configurable allowlist of private IP ranges that are permitted (e.g., via an environment variable `MCP_PROXY_ALLOWED_PRIVATE_IPS`).
- The OAuth HTTP client (`oauthHTTPClient`) uses `SafeTransport()` which blocks private IPs. This means OAuth discovery against a private auth server will fail. The same allowlist should apply.

### 2e. Session Management
- In `httpCall`, when a 404 is received (session expired), the session ID is cleared but no reconnection is triggered. The caller gets an error but the server stays in "connected" status. Add automatic reconnection on session expiry.
- The `closeSession` function sends a DELETE request but doesn't check the response status. If the server doesn't support session deletion, it silently fails (which is fine), but log it for debugging.

### 2f. Error Handling
- In `discoverTools`, if `tools/list` fails, the entire connection fails. Some servers may not support tools (they might only have resources or prompts). Make tool discovery non-fatal — log a warning and continue with zero tools.
- In `initialize`, if the server returns a protocol version we don't support, we return an error. Consider accepting any version and just logging a warning, since the spec says the server SHOULD negotiate but some servers may return unexpected versions.

### 2g. Concurrency
- In `connectServerWithRetry`, the check for existing connection (`m.clients[srv.ID]`) is done under a read lock, but the actual connection happens later without holding the lock. Two concurrent calls could both pass the check and both connect. Use a write lock or a connection-in-progress flag.
- The `authStates` map is protected by `m.mu` (the same lock as `clients`). If an OAuth callback comes in while a server is being connected, there could be lock contention. Consider using a separate mutex for auth states.

## Constraints
- Do NOT break existing tests. Run `go test ./...` after changes.
- Do NOT change the database schema (no migrations).
- Keep the code style consistent with the existing codebase.
- All changes must be in Go — do not modify the frontend (React/TypeScript).
- Do NOT modify the Dockerfile or docker-compose.yml.
- Commit all changes with clear commit messages.

## Verification
After making changes:
1. Run `go vet ./...`
2. Run `go test ./...`
3. Run `go build -o /dev/null .`
4. Verify the Sentry MCP discovery flow works by running a test that calls `DiscoverOAuthMetadata("https://mcp.sentry.dev/mcp")` and checks that it returns the correct endpoints.
