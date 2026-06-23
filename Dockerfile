# ─── Stage 1: Build frontend ─────────────────────────────────────────
FROM node:22-alpine AS frontend

WORKDIR /app/web

COPY web/package.json web/package-lock.json* ./
RUN npm ci

COPY web/ ./
RUN npx tsc -b && npx vite build

# ─── Stage 2: Build Go binary ───────────────────────────────────────
FROM golang:1.26-alpine AS backend

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Copy the built frontend into the embed path
COPY --from=frontend /app/web/dist ./web/dist

# Pure-Go SQLite (modernc.org/sqlite) — no CGO needed
ENV CGO_ENABLED=0
RUN go build -ldflags="-s -w" -o mcp-proxy .

# ─── Stage 3: Runtime ───────────────────────────────────────────────
FROM node:22-alpine

# Base packages
RUN apk add --no-cache ca-certificates tzdata wget curl git && \
    addgroup -S app && adduser -S app -G app

# Python 3 + pip (for pip-based MCP servers)
RUN apk add --no-cache python3 py3-pip && \
    python3 -m pip install --no-cache-dir --break-system-packages pip --upgrade

# uv (fast Python package installer, for `uv run` / `uvx` stdio servers)
RUN curl -LsSf https://astral.sh/uv/install.sh | sh && \
    mv /root/.local/bin/uv /usr/local/bin/uv && \
    mv /root/.local/bin/uvx /usr/local/bin/uvx

# pnpm (for `pnpm dlx` stdio servers)
RUN npm install -g pnpm

# Corepack is bundled with Node — enable it for yarn/pnpm version management
RUN corepack enable

WORKDIR /app

COPY --from=backend /app/mcp-proxy .
RUN mkdir -p /app/data && chown -R app:app /app

# Create cache dirs for the app user so npx/pnpm/uv don't fail on permission
RUN mkdir -p /home/app/.npm /home/app/.cache/uv /home/app/.local/share/pnpm && \
    chown -R app:app /home/app

USER app

# Set PATH so all tools are discoverable when spawning stdio subprocesses
ENV PATH="/usr/local/bin:/usr/bin:/bin:/home/app/.local/bin:${PATH}"
ENV npm_config_cache=/home/app/.npm
ENV UV_CACHE_DIR=/home/app/.cache/uv
ENV PNPM_HOME=/home/app/.local/share/pnpm

VOLUME ["/app/data"]
EXPOSE 8080

ENV MCP_PROXY_DB=/app/data/mcp-proxy.db
ENV MCP_PROXY_PORT=8080

HEALTHCHECK --interval=10s --timeout=5s --start-period=5s --retries=3 \
    CMD wget -qO- http://localhost:8080/health || exit 1

ENTRYPOINT ["./mcp-proxy"]
CMD []
