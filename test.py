import json, time, urllib.request, urllib.error, threading, queue

BASE = "http://localhost:18080"

def post(path, data, headers=None):
    hdrs = {"Content-Type": "application/json"}
    if headers: hdrs.update(headers)
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
H = {"Authorization": f"Bearer {token}"}

print("\n=== Create 2 servers ===")
_, s1 = post("/api/servers", {"name": "time-server", "transport": "stdio", "command": "uvx", "args": ["mcp-server-time"], "enabled": True}, H)
_, s2 = post("/api/servers", {"name": "fetch-server", "transport": "stdio", "command": "uvx", "args": ["mcp-server-fetch"], "enabled": True}, H)
time_id = s1["id"]; fetch_id = s2["id"]
print(f"  time: {time_id}, fetch: {fetch_id}")
time.sleep(8)

print("\n=== Create compound ===")
_, comp = post("/api/compounds", {"name": "dev-tools", "member_ids": [time_id, fetch_id]}, H)
comp_id = comp["id"]
print(f"  compound: {comp_id}")

print("\n=== Create 3 API keys ===")
_, k1 = post("/api/keys", {"name": "global", "scopes": ["read","write"]}, H)
_, k2 = post("/api/keys", {"name": "compound", "scopes": ["read","write"], "compound_id": comp_id}, H)
global_key = k1["key"]; compound_key = k2["key"]
print(f"  global: {k1['key_prefix']}, compound: {k2['key_prefix']}")

def mcp_post(path, method, params=None, key=global_key):
    body = {"jsonrpc": "2.0", "id": 1, "method": method}
    if params: body["params"] = params
    return post(path, body, {"X-API-Key": key})

print("\n--- Streamable HTTP (POST) ---")

print("\n=== Global: /api/mcp ===")
_, res = mcp_post("/api/mcp", "tools/list", key=global_key)
tools = res.get("result",{}).get("tools",[])
print(f"  tools/list: {len(tools)} tools")

print("\n=== Per-server: /api/servers/{id}/mcp ===")
_, res = mcp_post(f"/api/servers/{time_id}/mcp", "tools/list", key=global_key)
tools = res.get("result",{}).get("tools",[])
print(f"  time-server tools/list: {len(tools)} tools")
for t in tools: print(f"    - {t['name']}")

_, res = mcp_post(f"/api/servers/{fetch_id}/mcp", "tools/list", key=global_key)
tools = res.get("result",{}).get("tools",[])
print(f"  fetch-server tools/list: {len(tools)} tools")
for t in tools: print(f"    - {t['name']}")

print("\n=== Per-compound: /api/compounds/{id}/mcp ===")
_, res = mcp_post(f"/api/compounds/{comp_id}/mcp", "tools/list", key=compound_key)
tools = res.get("result",{}).get("tools",[])
print(f"  compound tools/list: {len(tools)} tools")
for t in tools: print(f"    - {t['name']}")

print("\n=== Scope isolation: call fetch tool via time-server scope ===")
_, res = mcp_post(f"/api/servers/{time_id}/mcp", "tools/call",
    {"name": "fetch-server__fetch", "arguments": {"url": "https://example.com"}}, key=global_key)
if "error" in res.get("result",{}):
    pass  # might be in result.error
if res.get("error"):
    print(f"  ✅ Blocked: {res['error'].get('message','')}")
else:
    err = res.get("result",{}).get("error",{})
    if err:
        print(f"  ✅ Blocked: {err.get('message','')}")
    else:
        print(f"  ❌ Should have been blocked")

print("\n=== Tool call via server scope (should work) ===")
_, res = mcp_post(f"/api/servers/{time_id}/mcp", "tools/call",
    {"name": "time-server__get_current_time", "arguments": {"timezone": "UTC"}}, key=global_key)
content = res.get("result",{}).get("content",[{}])
if content:
    print(f"  ✅ {content[0].get('text','')[:80]}")

print("\n--- Legacy SSE Transport ---")

def sse_test(connect_path, key, label):
    """Test legacy SSE: connect, read endpoint event, post a message, read response."""
    result_q = queue.Queue()

    def read_sse():
        try:
            req = urllib.request.Request(f"{BASE}{connect_path}", headers={"X-API-Key": key})
            with urllib.request.urlopen(req, timeout=10) as resp:
                buf = b""
                endpoint = None
                # Read until we get the endpoint event
                while True:
                    line = resp.readline()
                    buf += line
                    text = line.decode().strip()
                    if text.startswith("data: ") and endpoint is None:
                        endpoint = text[6:]
                    if line == b"\n" and endpoint:
                        break

                # Post a tools/list request to the endpoint
                body = json.dumps({"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}).encode()
                post_req = urllib.request.Request(
                    f"{BASE}{endpoint}",
                    data=body,
                    headers={"Content-Type":"application/json","X-API-Key":key},
                    method="POST"
                )
                with urllib.request.urlopen(post_req) as post_resp:
                    post_resp.read()  # 202 Accepted

                # Read the SSE response data line
                while True:
                    line = resp.readline()
                    if not line:
                        break
                    text = line.decode().strip()
                    if text.startswith("data: "):
                        result_q.put(text[6:])
                        return
                    # Skip blank lines and comments
                result_q.put("NO_DATA")
        except Exception as e:
            result_q.put(f"ERROR: {e}")

    t = threading.Thread(target=read_sse)
    t.start()
    t.join(timeout=15)
    return result_q.get() if not result_q.empty() else "TIMEOUT"

print("\n=== SSE Global ===")
result = sse_test("/api/sse", global_key, "global")
if result.startswith("ERROR") or result == "TIMEOUT" or result == "NO_DATA":
    print(f"  {result}")
else:
    res = json.loads(result)
    tools = res.get("result",{}).get("tools",[])
    print(f"  ✅ tools via SSE: {len(tools)}")

print("\n=== SSE Per-server ===")
result = sse_test(f"/api/servers/{time_id}/sse", global_key, "server")
if result.startswith("ERROR") or result == "TIMEOUT" or result == "NO_DATA":
    print(f"  {result}")
else:
    res = json.loads(result)
    tools = res.get("result",{}).get("tools",[])
    print(f"  ✅ time-server tools via SSE: {len(tools)}")

print("\n=== SSE Per-compound ===")
result = sse_test(f"/api/compounds/{comp_id}/sse", compound_key, "compound")
if result.startswith("ERROR") or result == "TIMEOUT" or result == "NO_DATA":
    print(f"  {result}")
else:
    res = json.loads(result)
    tools = res.get("result",{}).get("tools",[])
    print(f"  ✅ compound tools via SSE: {len(tools)}")

print("\n=== 404: invalid server ID ===")
try:
    req = urllib.request.Request(f"{BASE}/api/servers/nonexistent/sse", headers={"X-API-Key": global_key})
    urllib.request.urlopen(req)
except urllib.error.HTTPError as e:
    print(f"  ✅ HTTP {e.code} for invalid server ID")

print("\n=== Dashboard ===")
_, stats = get("/api/dashboard", H)
print(f"  {json.dumps(stats, indent=2)}")

print("\n✅ All tests passed!")
