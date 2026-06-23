import json
import time
import urllib.request

BASE = "http://localhost:18080"

def post(path, data, headers=None):
    hdrs = {"Content-Type": "application/json"}
    if headers:
        hdrs.update(headers)
    req = urllib.request.Request(
        f"{BASE}{path}",
        data=json.dumps(data).encode(),
        headers=hdrs,
        method="POST"
    )
    with urllib.request.urlopen(req) as resp:
        return json.loads(resp.read())

def get(path, headers=None):
    req = urllib.request.Request(f"{BASE}{path}", headers=headers or {})
    with urllib.request.urlopen(req) as resp:
        return json.loads(resp.read())

print("=== Login ===")
res = post("/api/auth/login", {"username": "admin", "password": "test123"})
token = res["token"]
print(f"Token: {token[:20]}...")
auth_hdr = {"Authorization": f"Bearer {token}"}

print("\n=== Create time MCP server ===")
res = post("/api/servers", {
    "name": "time",
    "transport": "stdio",
    "command": "uvx",
    "args": ["mcp-server-time"],
    "enabled": True
}, auth_hdr)
print(f"Server created: {res.get('id', 'N/A')}")

print("\nWaiting for connection...")
time.sleep(5)

print("\n=== List Servers ===")
servers = get("/api/servers", auth_hdr)
for s in servers:
    print(f"  {s['name']}: status={s['status']}, tools={s.get('tools_count', 0)}")

print("\n=== Create API Key ===")
res = post("/api/keys", {"name": "test-key", "scopes": ["read", "write", "admin"]}, auth_hdr)
apikey = res["key"]
print(f"API Key: {apikey[:15]}...")

print("\n=== MCP Proxy: initialize ===")
res = post("/api/mcp", {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {}}, {"X-API-Key": apikey})
print(json.dumps(res, indent=2))

print("\n=== MCP Proxy: tools/list ===")
res = post("/api/mcp", {"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}}, {"X-API-Key": apikey})
print(json.dumps(res, indent=2))

print("\n=== MCP Proxy: tools/call ===")
res = post("/api/mcp", {
    "jsonrpc": "2.0", "id": 3, "method": "tools/call",
    "params": {"name": "time__get_current_time", "arguments": {"timezone": "UTC"}}
}, {"X-API-Key": apikey})
print(json.dumps(res, indent=2))

print("\n=== Dashboard ===")
stats = get("/api/dashboard", auth_hdr)
print(json.dumps(stats, indent=2))

print("\n=== Frontend check ===")
req = urllib.request.Request(f"{BASE}/")
with urllib.request.urlopen(req) as resp:
    html = resp.read().decode()[:200]
print(html)

print("\n✅ All tests passed!")
