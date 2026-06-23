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
auth_hdr = {"Authorization": f"Bearer {token}"}

print("\n=== Create stdio time server (regression check) ===")
res = post("/api/servers", {
    "name": "time",
    "transport": "stdio",
    "command": "uvx",
    "args": ["mcp-server-time"],
    "enabled": True
}, auth_hdr)
print(f"  Created: {res.get('id', 'N/A')}")

time.sleep(5)

print("\n=== Create streamable-http server (Azure DevOps) ===")
res = post("/api/servers", {
    "name": "azure-devops",
    "transport": "streamable-http",
    "url": "https://mcp.dev.azure.com/testorg",
    "auth_token": "dummy-token-for-testing",
    "enabled": True
}, auth_hdr)
print(f"  Created: {res.get('id', 'N/A')}")
print(f"  Transport: {res.get('transport')}")
print(f"  URL: {res.get('url')}")

time.sleep(3)

print("\n=== List Servers ===")
servers = get("/api/servers", auth_hdr)
for s in servers:
    print(f"  {s['name']}: transport={s['transport']}, status={s['status']}, tools={s.get('tools_count', 0)}")
    if s.get('live_error'):
        print(f"    error: {s['live_error']}")

print("\n=== Create API Key ===")
res = post("/api/keys", {"name": "test-key", "scopes": ["read", "write", "admin"]}, auth_hdr)
apikey = res["key"]
print(f"  Key: {apikey[:15]}...")

print("\n=== MCP Proxy: initialize ===")
res = post("/api/mcp", {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {}}, {"X-API-Key": apikey})
print(f"  {json.dumps(res, indent=2)[:200]}")

print("\n=== MCP Proxy: tools/list ===")
res = post("/api/mcp", {"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}}, {"X-API-Key": apikey})
tools = res.get("result", {}).get("tools", [])
print(f"  Found {len(tools)} tools:")
for t in tools:
    print(f"    - {t['name']}")

print("\n=== MCP Proxy: tools/call (time) ===")
res = post("/api/mcp", {
    "jsonrpc": "2.0", "id": 3, "method": "tools/call",
    "params": {"name": "time__get_current_time", "arguments": {"timezone": "UTC"}}
}, {"X-API-Key": apikey})
result = res.get("result", {})
content = result.get("content", [{}])
if content:
    print(f"  {content[0].get('text', '')[:200]}")

print("\n=== Dashboard ===")
stats = get("/api/dashboard", auth_hdr)
print(f"  {json.dumps(stats, indent=2)}")

print("\n=== Frontend check ===")
req = urllib.request.Request(f"{BASE}/")
with urllib.request.urlopen(req) as resp:
    html = resp.read().decode()[:100]
print(f"  {html}")

print("\n✅ All tests passed!")
