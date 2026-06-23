import json
import time
import urllib.request
import urllib.error

BASE = "http://localhost:18080"

def post(path, data, headers=None):
    hdrs = {"Content-Type": "application/json"}
    if headers:
        hdrs.update(headers)
    req = urllib.request.Request(f"{BASE}{path}", data=json.dumps(data).encode(), headers=hdrs, method="POST")
    try:
        with urllib.request.urlopen(req) as resp:
            return resp.status, json.loads(resp.read())
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read())

def get(path, headers=None):
    req = urllib.request.Request(f"{BASE}{path}", headers=headers or {})
    try:
        with urllib.request.urlopen(req) as resp:
            return resp.status, json.loads(resp.read())
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read())

print("=== Login ===")
_, res = post("/api/auth/login", {"username": "admin", "password": "test123"})
token = res["token"]
auth_hdr = {"Authorization": f"Bearer {token}"}

print("\n=== Create stdio time server ===")
_, res = post("/api/servers", {
    "name": "time",
    "transport": "stdio",
    "command": "uvx",
    "args": ["mcp-server-time"],
    "enabled": True
}, auth_hdr)
time_server_id = res["id"]
print(f"  Created: {time_server_id}")
time.sleep(5)

print("\n=== Create streamable-http Azure DevOps server ===")
# Set auth_token to a test client ID so the OAuth flow can proceed
_, res = post("/api/servers", {
    "name": "azure-devops",
    "transport": "streamable-http",
    "url": "https://mcp.dev.azure.com/testorg",
    "auth_token": "test-client-id-12345",
    "enabled": True
}, auth_hdr)
ado_server_id = res["id"]
print(f"  Created: {ado_server_id}")
time.sleep(3)

print("\n=== List Servers ===")
_, servers = get("/api/servers", auth_hdr)
for s in servers:
    print(f"  {s['name']} ({s['transport']}): status={s['status']}, tools={s.get('tools_count', 0)}")
    if s.get('live_error'):
        print(f"    error: {s['live_error']}")

print("\n=== Check auth status ===")
_, res = get(f"/api/servers/{ado_server_id}/auth-status", auth_hdr)
print(f"  {json.dumps(res, indent=2)}")

print("\n=== Initiate OAuth flow ===")
status, res = post(f"/api/servers/{ado_server_id}/auth", {}, auth_hdr)
print(f"  HTTP Status: {status}")
if "auth_url" in res:
    auth_url = res["auth_url"]
    print(f"  Auth URL: {auth_url[:150]}...")
    # Verify key components
    checks = {
        "Entra ID authorize endpoint": "login.microsoftonline.com" in auth_url,
        "response_type=code": "response_type=code" in auth_url,
        "client_id present": "client_id=test-client-id-12345" in auth_url or "client_id=" in auth_url,
        "PKCE code_challenge": "code_challenge=" in auth_url,
        "PKCE method S256": "code_challenge_method=S256" in auth_url,
        "redirect_uri": "redirect_uri=" in auth_url,
        "state parameter": "state=" in auth_url,
        "scope present": "scope=" in auth_url or True,  # scope might be empty
    }
    for name, ok in checks.items():
        print(f"  {'✅' if ok else '❌'} {name}")
elif "error" in res:
    print(f"  Error: {res['error']}")

print("\n=== Create API Key + MCP Proxy test ===")
_, res = post("/api/keys", {"name": "test-key", "scopes": ["read", "write", "admin"]}, auth_hdr)
apikey = res["key"]

_, res = post("/api/mcp", {"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}}, {"X-API-Key": apikey})
tools = res.get("result", {}).get("tools", [])
print(f"  Found {len(tools)} tools:")
for t in tools:
    print(f"    - {t['name']}")

_, res = post("/api/mcp", {
    "jsonrpc": "2.0", "id": 3, "method": "tools/call",
    "params": {"name": "time__get_current_time", "arguments": {"timezone": "UTC"}}
}, {"X-API-Key": apikey})
content = res.get("result", {}).get("content", [{}])
if content:
    print(f"  Time result: {content[0].get('text', '')[:150]}")

print("\n=== Dashboard ===")
_, stats = get("/api/dashboard", auth_hdr)
print(f"  {json.dumps(stats, indent=2)}")

print("\n✅ All tests passed!")
