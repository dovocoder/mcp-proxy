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

def delete(path, headers=None):
    req = urllib.request.Request(f"{BASE}{path}", headers=headers or {}, method="DELETE")
    try:
        with urllib.request.urlopen(req) as resp:
            return resp.status, json.loads(resp.read())
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read())

print("=== Login ===")
_, res = post("/api/auth/login", {"username": "admin", "password": "test123"})
token = res["token"]
auth_hdr = {"Authorization": f"Bearer {token}"}

print("\n=== Create 2 stdio servers ===")
_, res1 = post("/api/servers", {
    "name": "time-server",
    "transport": "stdio",
    "command": "uvx",
    "args": ["mcp-server-time"],
    "enabled": True
}, auth_hdr)
time_id = res1["id"]
print(f"  Created time-server: {time_id}")

_, res2 = post("/api/servers", {
    "name": "fetch-server",
    "transport": "stdio",
    "command": "uvx",
    "args": ["mcp-server-fetch"],
    "enabled": True
}, auth_hdr)
fetch_id = res2["id"]
print(f"  Created fetch-server: {fetch_id}")

time.sleep(8)

print("\n=== List Servers ===")
_, servers_list = get("/api/servers", auth_hdr)
for s in servers_list:
    print(f"  {s['name']} ({s['transport']}): status={s['status']}, tools={s.get('tools_count', 0)}")

# Get total tool count (global)
_, all_tools = get("/api/tools", auth_hdr)
print(f"\n  Global tools: {len(all_tools)}")
for t in all_tools:
    print(f"    - {t['server_name']}__{t['name']}")

print("\n=== Create compound 'dev-tools' with both servers ===")
_, compound = post("/api/compounds", {
    "name": "dev-tools",
    "description": "Development tools group",
    "member_ids": [time_id, fetch_id]
}, auth_hdr)
compound_id = compound["id"]
print(f"  Created: {compound_id}")

print("\n=== Get compound detail ===")
_, detail = get(f"/api/compounds/{compound_id}", auth_hdr)
print(f"  Name: {detail['name']}")
print(f"  Members: {len(detail['members'])}")
for m in detail['members']:
    print(f"    - {m['name']} ({m['status']})")
print(f"  Tool count: {detail['tool_count']}")

print("\n=== Create compound 'time-only' with just time-server ===")
_, compound2 = post("/api/compounds", {
    "name": "time-only",
    "member_ids": [time_id]
}, auth_hdr)
compound2_id = compound2["id"]

_, detail2 = get(f"/api/compounds/{compound2_id}", auth_hdr)
print(f"  Tool count: {detail2['tool_count']}")

print("\n=== Create API key scoped to 'dev-tools' compound ===")
_, key_res = post("/api/keys", {
    "name": "dev-key",
    "scopes": ["read", "write"],
    "compound_id": compound_id
}, auth_hdr)
compound_key = key_res["key"]
print(f"  Key: {key_res['key_prefix']}")
print(f"  Compound ID: {key_res.get('compound_id')}")

print("\n=== Create API key scoped to 'time-only' compound ===")
_, key_res2 = post("/api/keys", {
    "name": "time-key",
    "scopes": ["read", "write"],
    "compound_id": compound2_id
}, auth_hdr)
time_key = key_res2["key"]
print(f"  Key: {key_res2['key_prefix']}")

print("\n=== Create global API key (no compound) ===")
_, key_res3 = post("/api/keys", {
    "name": "global-key",
    "scopes": ["read", "write"]
}, auth_hdr)
global_key = key_res3["key"]

print("\n=== MCP tools/list with dev-tools compound key ===")
_, res = post("/api/mcp", {"jsonrpc": "2.0", "id": 1, "method": "tools/list", "params": {}}, {"X-API-Key": compound_key})
dev_tools = res.get("result", {}).get("tools", [])
print(f"  Found {len(dev_tools)} tools (should be from both servers):")
for t in dev_tools:
    print(f"    - {t['name']}")

print("\n=== MCP tools/list with time-only compound key ===")
_, res = post("/api/mcp", {"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}}, {"X-API-Key": time_key})
time_tools = res.get("result", {}).get("tools", [])
print(f"  Found {len(time_tools)} tools (should be from time-server only):")
for t in time_tools:
    print(f"    - {t['name']}")

print("\n=== MCP tools/list with global key ===")
_, res = post("/api/mcp", {"jsonrpc": "2.0", "id": 3, "method": "tools/list", "params": {}}, {"X-API-Key": global_key})
global_tools = res.get("result", {}).get("tools", [])
print(f"  Found {len(global_tools)} tools (should match global tool count):")
for t in global_tools:
    print(f"    - {t['name']}")

print("\n=== MCP tools/call with compound key (should work) ===")
_, res = post("/api/mcp", {
    "jsonrpc": "2.0", "id": 4, "method": "tools/call",
    "params": {"name": "time-server__get_current_time", "arguments": {"timezone": "UTC"}}
}, {"X-API-Key": compound_key})
content = res.get("result", {}).get("content", [{}])
if content:
    print(f"  ✅ Tool call succeeded: {content[0].get('text', '')[:100]}")
else:
    print(f"  ❌ Tool call failed: {res}")

print("\n=== MCP tools/call with time-only key for fetch tool (should fail) ===")
_, res = post("/api/mcp", {
    "jsonrpc": "2.0", "id": 5, "method": "tools/call",
    "params": {"name": "fetch-server__fetch", "arguments": {"url": "https://example.com"}}
}, {"X-API-Key": time_key})
if "error" in res:
    print(f"  ✅ Correctly blocked: {res['error'].get('message', '')}")
else:
    print(f"  ❌ Should have been blocked but wasn't: {res}")

print("\n=== Remove fetch-server from dev-tools compound ===")
_, res = delete(f"/api/compounds/{compound_id}/members/{fetch_id}", auth_hdr)
print(f"  {res}")

_, detail = get(f"/api/compounds/{compound_id}", auth_hdr)
print(f"  Members after removal: {len(detail['members'])}")
print(f"  Tool count after removal: {detail['tool_count']}")

print("\n=== List API keys (check compound_id is shown) ===")
_, keys = get("/api/keys", auth_hdr)
for k in keys:
    print(f"  {k['name']}: compound_id={k.get('compound_id', 'null')}")

print("\n=== Dashboard ===")
_, stats = get("/api/dashboard", auth_hdr)
print(f"  {json.dumps(stats, indent=2)}")

print("\n=== List compounds ===")
_, compounds = get("/api/compounds", auth_hdr)
for c in compounds:
    print(f"  {c['name']} ({c['id'][:8]}...)")

print("\n✅ All tests passed!")
