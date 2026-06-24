# MCP Proxy

A Go gateway service that proxies, aggregates, and secures multiple MCP (Model Context Protocol) servers behind a single authenticated endpoint — with a React web UI for management, compound server grouping, a built-in memory server, dictionary mode for tool discovery, OIDC SSO, and encrypted environment variable management.

## Features

### MCP Gateway
- **Multi-server proxying**: Single JSON-RPC endpoint that proxies to multiple backend MCP servers (stdio, HTTP, and streamable-HTTP transports)
- **Tool aggregation**: Automatically discovers and aggregates tools from all connected servers, namespaced as `serverName__toolName`
- **URL-scoped endpoints**: Connect to all servers, a single server, or a compound group via distinct URLs
- **Dual transport**: Streamable HTTP (`POST /api/mcp`) and legacy SSE (`GET /api/sse` + `POST /api/messages`)
- **OAuth for backend servers**: Full OAuth 2.1 with PKCE for servers like Azure DevOps MCP; Protected Resource Metadata (RFC 9728) discovery; token storage with auto-refresh

### Compound Servers
- **Group multiple MCP servers** into a single virtual endpoint
- **Dictionary mode**: Instead of flooding clients with all tools, exposes a single `dictionary` tool with `list` / `describe` / `call` / `search` actions for lazy discovery
- **Per-compound connection URLs**: `POST /api/compounds/{id}/mcp` and `GET /api/compounds/{id}/sse`
- **API key scoping**: Compound-scoped API keys only access tools within that compound's member servers

### Built-in Memory Server
- **Persistent memory** stored in SQLite — survives restarts
- **Multiple memory sets**: Isolate memories per project, team, or organization (each set is a separate virtual MCP server with namespaced tools: `memory_{slug}__tool`)
- **6 MCP tools per set**: `store`, `recall`, `search`, `update`, `delete`, `reflect`
- **Compound membership**: Add memory sets to compounds; dictionary mode discovers them lazily

### Authentication
- **Admin UI**: Password login or OIDC SSO (PocketID, Keycloak, Google, etc.)
- **MCP clients**: API keys (scoped, expiring) **or** OIDC access tokens (same SSO account)
- **Protected Resource Metadata** (RFC 9728): MCP clients auto-discover the auth flow via `/.well-known/oauth-protected-resource`
- **Token caching**: OIDC token validation cached for 5 minutes

### Environment Variable Management
- **Encrypted at rest** using NaCl secretbox (pure Go, no CGO)
- **Per-project / per-environment** organization
- **Export endpoint**: API key-authenticated; re-encrypts variables for local decryption (e.g. with PyNaCl)

### Operations
- **Embedded frontend**: React app compiled and embedded in the Go binary (single deployable)
- **SQLite storage**: No external database required
- **Docker ready**: Multi-stage Dockerfile with Python, uv, pnpm, and Node for stdio subprocess spawning
- **Health checks**: `/health` and `/healthz` endpoints

## Architecture

```
┌─────────────┐    JWT/OIDC  ┌──────────────────────┐
│  React UI   │─────────────▶│   REST API (JWT)     │
│  (shadcn/ui)│              │   /api/servers       │
└─────────────┘              │   /api/keys          │
                             │   /api/compounds     │
┌─────────────┐  API Key or  │   /api/memories      │
│ MCP Client  │  OIDC token  │   /api/env-vars      │
│ (Hermes,    │─────────────▶│                      │
│  Claude...) │              │   MCP Proxy (no JWT) │
└─────────────┘              │   /api/mcp           │
                             │   /api/sse           │
                             └────────┬─────────────┘
                                      │ JSON-RPC
                               ┌──────┴──────┐
                               │  Proxy Mgr  │
                               │  (Scope)    │
                               └──────┬──────┘
                          ┌───────────┼───────────┐
                          ▼          ▼           ▼
                    ┌──────────┐ ┌──────────┐ ┌──────────────┐
                    │ MCP      │ │ MCP      │ │ Built-in     │
                    │ Server   │ │ Server   │ │ Memory       │
                    │ (stdio)  │ │ (HTTP)   │ │ Server (SQLite)│
                    └──────────┘ └──────────┘ └──────────────┘
                                        │
                               ┌────────┴────────┐
                               │  Compound Group  │
                               │  (Dictionary)    │
                               └─────────────────┘
```

## Quick Start

### Docker (recommended)

```bash
docker compose up -d
```

See [`docker-compose.yml`](docker-compose.yml) for configuration. Open `http://localhost:8080`.

### Local build

```bash
# Build frontend
cd web && npm install && npx tsc -b && node node_modules/vite/bin/vite.js build && cd ..

# Build Go binary (includes embedded frontend)
go build -o mcp-proxy .

# Run
./mcp-proxy

# Or with custom settings
MCP_PROXY_PORT=8080 \
MCP_PROXY_ADMIN_PASS=your-secret \
MCP_PROXY_JWT_SECRET=your-jwt-secret \
./mcp-proxy
```

Open `http://localhost:8080` and log in with your admin credentials (default: `admin`/`admin`).

### Dokploy

Deploy via the Dockerfile. The container exposes port 8080 with a `/health` endpoint for health checks. Configure environment variables in your Dokploy service settings.

## Configuration

All configuration via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `MCP_PROXY_PORT` | `8080` | HTTP listen port |
| `MCP_PROXY_DB` | `mcp-proxy.db` | SQLite database path |
| `MCP_PROXY_JWT_SECRET` | `dev-secret-...` | JWT signing secret (also used as encryption key fallback) |
| `MCP_PROXY_ADMIN_USER` | `admin` | Default admin username |
| `MCP_PROXY_ADMIN_PASS` | `admin` | Default admin password |
| `MCP_PROXY_WEB_DIST` | `web/dist` | Frontend dist path (dev mode) |
| `MCP_PROXY_ENV` | — | Set to `production` for prod mode |
| `ENCRYPTION_KEY` | — | Master encryption key for env vars at rest (falls back to JWT secret) |
| `OIDC_ENABLED` | `false` | Enable OIDC SSO (`true` or `1`) |
| `OIDC_ISSUER` | — | OIDC issuer URL (e.g. `https://pocketid.example.com`) |
| `OIDC_CLIENT_ID` | — | OIDC client ID |
| `OIDC_CLIENT_SECRET` | — | OIDC client secret |
| `OIDC_REDIRECT_URL` | — | Redirect URL override (auto-detected from request if empty) |

## API Reference

### Auth & Discovery

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/api/auth/login` | — | Password login, returns JWT |
| `GET` | `/api/auth/oidc/status` | — | Check if OIDC is enabled |
| `GET` | `/api/auth/oidc/login` | — | Redirect to OIDC provider |
| `GET` | `/api/auth/oidc/callback` | — | OIDC callback, issues JWT |
| `GET` | `/.well-known/oauth-protected-resource` | — | RFC 9728 Protected Resource Metadata |
| `GET` | `/health` | — | Health check |

### MCP Proxy Endpoints (API Key or OIDC token)

| Method | Path | Scope | Description |
|--------|------|-------|-------------|
| `POST`/`GET`/`DELETE` | `/api/mcp` | Global | JSON-RPC 2.0 proxy (all servers) |
| `GET` | `/api/sse` | Global | Legacy SSE stream |
| `POST` | `/api/messages` | Global | SSE message endpoint |
| `POST`/`GET`/`DELETE` | `/api/servers/{id}/mcp` | Single server | JSON-RPC scoped to one server |
| `GET` | `/api/servers/{id}/sse` | Single server | SSE scoped to one server |
| `POST` | `/api/servers/{id}/messages` | Single server | SSE message endpoint |
| `POST`/`GET`/`DELETE` | `/api/compounds/{id}/mcp` | Compound | JSON-RPC scoped to compound members |
| `GET` | `/api/compounds/{id}/sse` | Compound | SSE scoped to compound members |
| `POST` | `/api/compounds/{id}/messages` | Compound | SSE message endpoint |

### Admin API (JWT required)

**Servers**

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/servers` | List all servers |
| `POST` | `/api/servers` | Create server |
| `GET` | `/api/servers/:id` | Get server details |
| `PUT` | `/api/servers/:id` | Update server |
| `DELETE` | `/api/servers/:id` | Delete server |
| `POST` | `/api/servers/:id/reconnect` | Reconnect server |
| `GET` | `/api/servers/:id/logs` | Get server logs |
| `POST` | `/api/servers/:id/auth` | Initiate OAuth flow |
| `GET` | `/api/servers/:id/auth-status` | Check OAuth status |
| `POST` | `/api/servers/:id/device-auth` | Initiate device code auth |
| `POST` | `/api/servers/:id/device-auth/poll` | Poll device auth |
| `DELETE` | `/api/servers/:id/device-auth` | Cancel device auth |

**Compound Servers**

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/compounds` | List all compounds |
| `POST` | `/api/compounds` | Create compound |
| `GET` | `/api/compounds/:id` | Get compound with members |
| `PUT` | `/api/compounds/:id` | Update compound (incl. dictionary mode) |
| `DELETE` | `/api/compounds/:id` | Delete compound |
| `POST` | `/api/compounds/:id/members/:serverId` | Add member server |
| `DELETE` | `/api/compounds/:id/members/:serverId` | Remove member server |

**Memory Sets & Memories**

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/memory-sets` | List memory sets |
| `POST` | `/api/memory-sets` | Create memory set |
| `PATCH` | `/api/memory-sets/:id` | Update memory set |
| `DELETE` | `/api/memory-sets/:id` | Delete memory set |
| `GET` | `/api/memories` | List memories (filter by set) |
| `POST` | `/api/memories` | Create memory |
| `GET` | `/api/memories/:id` | Get memory |
| `PUT` | `/api/memories/:id` | Update memory |
| `DELETE` | `/api/memories/:id` | Delete memory |
| `GET` | `/api/memories/search` | Search memories |
| `GET` | `/api/memories/palaces` | List memory palaces |

**API Keys**

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/keys` | List API keys |
| `POST` | `/api/keys` | Create API key (scoped, expiring) |
| `DELETE` | `/api/keys/:id` | Delete API key |

**Environment Variables**

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/env-vars/projects` | JWT | List env var projects |
| `GET` | `/api/env-vars/environments` | JWT | List env var environments |
| `GET` | `/api/env-vars` | JWT | List env vars (filter by project/env) |
| `POST` | `/api/env-vars` | JWT | Create env var (encrypted at rest) |
| `PUT` | `/api/env-vars/:id` | JWT | Update env var |
| `DELETE` | `/api/env-vars/:id` | JWT | Delete env var |
| `GET` | `/api/env-vars/export` | API Key | Export env vars (re-encrypted for local decryption) |

**Other**

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/tools` | List all aggregated tools |
| `GET` | `/api/dashboard` | Dashboard statistics |

## Using as MCP Client

### With an API key

```yaml
mcp_servers:
  proxy:
    url: "https://your-domain.com/api/mcp"
    headers:
      X-API-Key: "mcp_your_api_key_here"
```

### With OIDC token

```yaml
mcp_servers:
  proxy:
    url: "https://your-domain.com/api/mcp"
    headers:
      Authorization: "Bearer <oidc-access-token>"
```

MCP clients that support RFC 9728 (like Claude Desktop) will auto-discover the OIDC flow:
1. Client sends unauthenticated request → gets `401` with `WWW-Authenticate: Bearer resource_metadata="..."`
2. Client fetches `/.well-known/oauth-protected-resource` → finds the authorization server URL
3. Client completes OAuth flow with the OIDC provider (e.g. PocketID)
4. Client retries with `Authorization: Bearer <token>`

### URL-scoped connections

| Scope | URL | Access |
|-------|-----|--------|
| Global | `https://domain.com/api/mcp` | All connected servers |
| Single server | `https://domain.com/api/servers/{id}/mcp` | One server only |
| Compound | `https://domain.com/api/compounds/{id}/mcp` | Compound member servers only |

## Compound Servers & Dictionary Mode

A **compound server** groups multiple MCP servers (including built-in memory servers) into a single endpoint. When **dictionary mode** is enabled, instead of sending all tools to the client, the proxy exposes a single `dictionary` tool:

```
1. list       → See available tools (server tools + memory tools)
2. describe   → Get the schema for a specific tool
3. call       → Invoke a tool by name
4. search     → Find tools by keyword
```

This prevents tool flooding — clients like Claude Desktop won't hit token limits from hundreds of tool definitions. Agents discover tools lazily through the dictionary.

### Built-in Memory Server

Each memory set appears as a virtual MCP server (ID: `builtin-memory` for default, `builtin-memory:{set_id}` for custom sets). Add them to compounds like any other server:

```
POST /api/compounds/{id}/members/builtin-memory
POST /api/compounds/{id}/members/builtin-memory:abc123
```

Memory tools are namespaced: `memory__store`, `memory__recall`, etc. (default set) or `memory_myproject__store`, `memory_myproject__recall` (custom set with slug `myproject`).

## Environment Variable Management

Store secrets per project and environment, encrypted at rest with NaCl secretbox:

```bash
# Create an env var (encrypted at rest)
curl -X POST https://domain.com/api/env-vars \
  -H "Authorization: Bearer <jwt>" \
  -d '{"project":"myapp","environment":"production","key":"DATABASE_URL","value":"postgres://..."}'

# Export for local decryption (API key auth, re-encrypted)
curl https://domain.com/api/env-vars/export?project=myapp&environment=production \
  -H "X-API-Key: mcp_your_key"
```

The export endpoint re-encrypts variables using the API key as the encryption key. Decrypt locally:

```python
import base64
from nacl.secret import SecretBox
from hashlib import sha256

key = sha256(b"mcp_your_api_key_here").digest()
raw = base64.b64decode(exported_blob)
box = SecretBox(key)
plaintext = box.decrypt(raw[24:], raw[:24])
```

## Development

```bash
# Terminal 1: Go backend (serves API + falls back to disk frontend)
cd mcp-proxy && go run .

# Terminal 2: Vite dev server (hot reload frontend)
cd web && npm run dev
```

The Vite dev server proxies `/api` requests to the Go backend at `localhost:8080`.

### Running tests

```bash
# Integration tests (requires running server on port 18080)
MCP_PROXY_PORT=18080 MCP_PROXY_ADMIN_PASS=test123 MCP_PROXY_DB=/tmp/test.db ./mcp-proxy &
python3 test.py
```

## Project Structure

```
mcp-proxy/
├── main.go                        # Entry point, HTTP server, embedded frontend
├── go.mod / go.sum
├── Dockerfile                     # Multi-stage: node + golang → node:alpine runtime
├── docker-compose.yml             # Container config with all env vars
├── internal/
│   ├── config/                    # Environment-based configuration
│   ├── models/                    # Data models (Server, APIKey, CompoundServer,
│   │                              #   MemorySet, Memory, EnvVar, User, etc.)
│   ├── store/                     # SQLite storage layer with migrations
│   ├── crypto/                    # NaCl secretbox encryption (pure Go)
│   ├── mcp/                       # MCP protocol client
│   │   ├── protocol.go            #   Streamable HTTP transport, session mgmt
│   │   ├── oauth.go               #   OAuth 2.1 PKCE + RFC 9728 discovery
│   │   └── stdio.go               #   stdio subprocess transport
│   ├── proxy/                     # Proxy manager (Scope, tool aggregation,
│   │                              #   dictionary mode, memory server routing)
│   ├── memory/                    # Built-in memory MCP server (multi-set)
│   ├── auth/                      # JWT + API key + OIDC authentication
│   │   ├── auth.go                #   AuthService, OIDC provider
│   │   ├── oidc.go                #   OIDC discovery, token validation
│   │   └── middleware.go          #   JWT, API key, OIDC middleware
│   └── api/                       # REST API + MCP proxy handlers
│       ├── handlers.go            #   Admin REST, MCP endpoints, OIDC, env vars
│       └── sse.go                 #   Legacy SSE transport with scoped sessions
├── web/                           # React frontend
│   ├── src/
│   │   ├── App.tsx                # Router + protected routes
│   │   ├── api/client.ts          # Typed API client
│   │   ├── components/
│   │   │   └── Layout.tsx         # Responsive layout (sidebar + bottom nav)
│   │   └── pages/
│   │       ├── Login.tsx          # Password + OIDC SSO login
│   │       ├── Dashboard.tsx      # Stats + server health
│   │       ├── Servers.tsx        # Server CRUD + builtin badge
│   │       ├── ServerDetail.tsx   # Server config, OAuth, connection URLs
│   │       ├── CompoundServers.tsx# Compound CRUD + dictionary mode
│   │       ├── APIKeys.tsx        # API key management (scoped)
│   │       ├── Tools.tsx          # Aggregated tool listing
│   │       ├── Memories.tsx       # Memory set management
│   │       └── EnvVars.tsx        # Env var CRUD (per project/environment)
│   ├── package.json
│   └── vite.config.ts
├── test.py                        # Integration test script
└── test.sh                        # Shell-based integration tests
```

## Tech Stack

**Backend:** Go 1.26, SQLite (`modernc.org/sqlite` — pure Go, no CGO), JWT (`golang-jwt/v5`), bcrypt, NaCl secretbox (`golang.org/x/crypto/nacl/secretbox`), OAuth2 (`golang.org/x/oauth2`)

**Frontend:** React 19, TypeScript, Vite 6, Tailwind CSS v4, shadcn/ui (base-nova, dark mode), TanStack Query, React Router v7, lucide-react

**Runtime (Docker):** Node 22 Alpine with Python 3, uv, pnpm, and corepack for spawning stdio MCP server subprocesses

## License

MIT
