# MCP Proxy

A Go gateway service that proxies and aggregates multiple MCP (Model Context Protocol) servers behind a single authenticated endpoint, with a React frontend for management.

## Features

- **MCP Gateway**: Single JSON-RPC endpoint that proxies to multiple backend MCP servers (stdio & HTTP transports)
- **Tool Aggregation**: Automatically discovers and aggregates tools from all connected servers, namespaced as `serverName__toolName`
- **Authentication**: Dual auth system — JWT for admin UI, API keys for MCP clients
- **Server Management**: Add, remove, enable/disable, and reconnect MCP servers via REST API or web UI
- **API Key Management**: Generate scoped API keys with optional expiration
- **Dashboard**: Real-time overview of server health, tool counts, and API key stats
- **Embedded Frontend**: React app compiled and embedded in the Go binary (single deployable)
- **SQLite Storage**: No external database required

## Architecture

```
┌─────────────┐     JWT      ┌──────────────────┐
│  React UI   │─────────────▶│   REST API       │
│  (Tailwind) │              │   /api/servers    │
└─────────────┘              │   /api/keys       │
                             │   /api/tools      │
┌─────────────┐   API Key    │   /api/dashboard  │
│ MCP Client  │─────────────▶│                   │
│ (Hermes,    │              │   MCP Proxy       │
│  Claude...) │              │   /api/mcp        │
└─────────────┘              └───────┬───────────┘
                                     │ JSON-RPC
                              ┌──────┴──────┐
                              │  Proxy Mgr  │
                              └──────┬──────┘
                          ┌──────────┼──────────┐
                          ▼          ▼          ▼
                     ┌────────┐ ┌────────┐ ┌────────┐
                     │ MCP    │ │ MCP    │ │ MCP    │
                     │ Server │ │ Server │ │ Server │
                     │ (stdio)│ │ (HTTP) │ │ (stdio)│
                     └────────┘ └────────┘ └────────┘
```

## Quick Start

```bash
# Build frontend
cd web && npm install && npm run build && cd ..

# Build Go binary (includes embedded frontend)
go build -o mcp-proxy .

# Run
./mcp-proxy

# Or with custom settings
MCP_PROXY_PORT=8080 \
MCP_PROXY_ADMIN_USER=admin \
MCP_PROXY_ADMIN_PASS=your-secret \
MCP_PROXY_JWT_SECRET=your-jwt-secret \
./mcp-proxy
```

Open `http://localhost:8080` and log in with your admin credentials (default: `admin`/`admin`).

## Configuration

All configuration via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `MCP_PROXY_PORT` | `8080` | HTTP listen port |
| `MCP_PROXY_DB` | `mcp-proxy.db` | SQLite database path |
| `MCP_PROXY_JWT_SECRET` | `dev-secret-...` | JWT signing secret |
| `MCP_PROXY_ADMIN_USER` | `admin` | Default admin username |
| `MCP_PROXY_ADMIN_PASS` | `admin` | Default admin password |
| `MCP_PROXY_WEB_DIST` | `web/dist` | Frontend dist path (dev mode) |
| `MCP_PROXY_ENV` | — | Set to `production` for prod mode |

## API Reference

### Auth

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/api/auth/login` | — | Login, returns JWT |

### MCP Proxy (API Key required)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/api/mcp` | API Key | JSON-RPC 2.0 proxy endpoint |
| `GET` | `/api/mcp/sse` | API Key | SSE endpoint for streaming |

### Admin (JWT required)

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/servers` | List all servers |
| `POST` | `/api/servers` | Create server |
| `GET` | `/api/servers/:id` | Get server details |
| `PUT` | `/api/servers/:id` | Update server |
| `DELETE` | `/api/servers/:id` | Delete server |
| `POST` | `/api/servers/:id/reconnect` | Reconnect server |
| `GET` | `/api/keys` | List API keys |
| `POST` | `/api/keys` | Create API key |
| `DELETE` | `/api/keys/:id` | Delete API key |
| `GET` | `/api/tools` | List all aggregated tools |
| `GET` | `/api/dashboard` | Dashboard statistics |

## Using as MCP Client

Configure your MCP client (e.g., Hermes Agent) to use the proxy:

```yaml
mcp_servers:
  proxy:
    url: "http://localhost:8080/api/mcp"
    headers:
      X-API-Key: "mcp_your_api_key_here"
```

All tools from all connected backend servers will be available with namespaced names: `serverName__toolName`.

## Development

```bash
# Terminal 1: Go backend (serves API + falls back to disk frontend)
cd mcp-proxy && go run .

# Terminal 2: Vite dev server (hot reload frontend)
cd web && npm run dev
```

The Vite dev server proxies `/api` requests to the Go backend at `localhost:8080`.

## Project Structure

```
mcp-proxy/
├── main.go                    # Entry point, HTTP server, embedded frontend
├── go.mod
├── internal/
│   ├── config/                # Environment-based configuration
│   ├── models/                # Data models (Server, APIKey, User, Tool)
│   ├── store/                 # SQLite storage layer with migrations
│   ├── mcp/                   # MCP protocol client (stdio + HTTP transports)
│   ├── proxy/                 # Proxy manager (connection pooling, tool aggregation)
│   ├── auth/                  # JWT + API key authentication middleware
│   └── api/                   # REST API handlers
├── web/                       # React frontend
│   ├── src/
│   │   ├── App.tsx            # Router + protected routes
│   │   ├── main.tsx           # Entry point
│   │   ├── api/client.ts      # API client (typed)
│   │   ├── components/
│   │   │   └── Layout.tsx     # Sidebar layout
│   │   └── pages/
│   │       ├── Login.tsx      # Admin login
│   │       ├── Dashboard.tsx  # Stats + server health
│   │       ├── Servers.tsx    # Server CRUD
│   │       ├── ServerDetail.tsx # Server config + tools
│   │       ├── APIKeys.tsx    # API key management
│   │       └── Tools.tsx      # Aggregated tool listing
│   ├── package.json
│   └── vite.config.ts
└── test.py                    # Integration test script
```

## Tech Stack

**Backend:** Go 1.26, SQLite (modernc.org/sqlite — pure Go, no CGO), JWT (golang-jwt/v5), bcrypt

**Frontend:** React 19, TypeScript, Vite 6, Tailwind CSS v4, TanStack Query, React Router v7, lucide-react

## License

MIT
