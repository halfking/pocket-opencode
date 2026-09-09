#!/usr/bin/env python3
"""CDP 驱动 WebView：起 AI 流 + 伪造 hidden → 触发 keepalive 链路"""
import json, sys, time
import websocket

WS_URL = sys.argv[1]
ws = websocket.create_connection(WS_URL, timeout=10, suppress_origin=True)
mid = 0

def evaluate(expr, timeout=15):
    global mid
    mid += 1
    ws.send(json.dumps({"id": mid, "method": "Runtime.evaluate",
                        "params": {"expression": expr, "returnByValue": True, "awaitPromise": True}}))
    deadline = time.time() + timeout
    while time.time() < deadline:
        msg = json.loads(ws.recv())
        if msg.get("id") == mid:
            if "exceptionDetails" in msg.get("result", {}):
                raise RuntimeError(json.dumps(msg["result"]["exceptionDetails"])[:500])
            return msg["result"]["result"].get("value")
    raise TimeoutError("evaluate timeout")

print("runtime global:", evaluate("typeof globalThis.__openpocket_aiStreamRuntime__"))
print("stats before:", evaluate("JSON.stringify(globalThis.__openpocket_aiStreamRuntime__.getStats())"))
print("spawn:", evaluate(
    "globalThis.__openpocket_aiStreamRuntime__.spawnChat('e2e-keepalive-test-5',"
    "{messages:[{role:'user',content:'poem of the night city'}]},{}); 'spawned'"))
print("stats after:", evaluate("JSON.stringify(globalThis.__openpocket_aiStreamRuntime__.getStats())"))
print("hidden:", evaluate(
    "Object.defineProperty(document,'visibilityState',{get:()=>'hidden',configurable:true});"
    "window.dispatchEvent(new Event('visibilitychange')); 'dispatched'"))
time.sleep(2)
print("ALL DONE")
