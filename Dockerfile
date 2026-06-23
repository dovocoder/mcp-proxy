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
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S app && adduser -S app -G app

WORKDIR /app

COPY --from=backend /app/mcp-proxy .
RUN mkdir -p /app/data && chown -R app:app /app

USER app
VOLUME ["/app/data"]
EXPOSE 8080

ENV MCP_PROXY_DB=/app/data/mcp-proxy.db
ENV MCP_PROXY_WEB_DIST=/app/web/dist

ENTRYPOINT ["./mcp-proxy"]
CMD []
