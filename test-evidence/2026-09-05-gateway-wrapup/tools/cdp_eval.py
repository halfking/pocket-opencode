#!/usr/bin/env python3
"""CDP Runtime.evaluate 探针 — 用法: cdp_eval.py 'JS表达式' [timeout]
在 App WebView 页面上下文执行 JS，支持 awaitPromise。"""
import sys, json, asyncio
import websockets

PAGE_ID = "53B93E5128662D8D740492AA73CBCB3D"
URL = f"ws://127.0.0.1:9222/devtools/page/{PAGE_ID}"

async def main():
    expr = sys.argv[1]
    timeout = float(sys.argv[2]) if len(sys.argv) > 2 else 15
    async with websockets.connect(URL, max_size=20 * 1024 * 1024) as ws:
        await ws.send(json.dumps({
            "id": 1, "method": "Runtime.evaluate",
            "params": {"expression": expr, "awaitPromise": True,
                       "returnByValue": True, "silent": False},
        }))
        while True:
            try:
                msg = json.loads(await asyncio.wait_for(ws.recv(), timeout))
            except asyncio.TimeoutError:
                print("TIMEOUT"); return 2
            if msg.get("id") == 1:
                r = msg.get("result", {})
                if "exceptionDetails" in r:
                    print("EXCEPTION:", json.dumps(r["exceptionDetails"].get("exception", {}).get("description", r["exceptionDetails"]))[:800])
                    return 1
                val = r.get("result", {}).get("value")
                print(json.dumps(val, ensure_ascii=False)[:4000] if val is not None else "undefined")
                return 0

sys.exit(asyncio.run(main()))
