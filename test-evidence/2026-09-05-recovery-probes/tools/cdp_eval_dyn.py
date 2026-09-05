#!/usr/bin/env python3
"""CDP Runtime.evaluate 动态版 — 用法: cdp_eval_dyn.py 'JS表达式' [timeout]
page ID 自动从 http://127.0.0.1:" + PORT + "/json/list 取第一个 page。
前置: adb -s emulator-5554 forward tcp:9222 localabstract:webview_devtools_remote_<pid>"""
import os, sys, json, asyncio, urllib.request
import websockets

PORT = os.environ.get("CDP_PORT", "9222")


def page_id():
    with urllib.request.urlopen(f"http://127.0.0.1:{PORT}/json/list", timeout=5) as r:
        for p in json.load(r):
            if p.get("type") == "page":
                return p["id"]
    raise RuntimeError("no page")


async def main():
    expr = sys.argv[1]
    timeout = float(sys.argv[2]) if len(sys.argv) > 2 else 15
    url = f"ws://127.0.0.1:{PORT}/devtools/page/{page_id()}"
    async with websockets.connect(url, max_size=20 * 1024 * 1024) as ws:
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
