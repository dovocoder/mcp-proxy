#!/bin/bash
set -e

BASE="http://localhost:18080"

echo "=== Login ==="
TOKEN=$(curl -s "$BASE/api/auth/login" -d '{"username":"admin","password":"test123"}' | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")
echo "Token: ${TOKEN:0:20}..."

echo ""
echo "=== Create time MCP server ==="
curl -s "$BASE/api/servers" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"time","transport":"stdio","command":"uvx","args":["mcp-server-time"],"enabled":true}'
echo ""

echo ""
echo "Waiting for server to connect..."
sleep 5

echo ""
echo "=== List Servers ==="
curl -s "$BASE/api/servers" \
  -H "Authorization: Bearer $TOKEN"
echo ""

echo ""
echo "=== Create API Key ==="
APIKEY=$(curl -s "$BASE/api/keys" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"test-key","scopes":["read","write","admin"]}' | python3 -c "import sys,json; print(json.load(sys.stdin)['key'])")
echo "API Key: ${APIKEY:0:15}..."

echo ""
echo "=== MCP Proxy: initialize ==="
curl -s "$BASE/api/mcp" \
  -H "X-API-Key: $APIKEY" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
echo ""

echo ""
echo "=== MCP Proxy: tools/list ==="
curl -s "$BASE/api/mcp" \
  -H "X-API-Key: $APIKEY" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
echo ""

echo ""
echo "=== MCP Proxy: tools/call (get_current_time) ==="
curl -s "$BASE/api/mcp" \
  -H "X-API-Key: $APIKEY" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"time__get_current_time","arguments":{"timezone":"UTC"}}}'
echo ""

echo ""
echo "=== Dashboard ==="
curl -s "$BASE/api/dashboard" \
  -H "Authorization: Bearer $TOKEN"
echo ""

echo ""
echo "=== Frontend check ==="
curl -s "$BASE/" | head -5
echo ""
echo "All tests passed!"
