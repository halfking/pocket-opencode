#!/usr/bin/env python3
"""CDP Input 域受信任点击动态版 — 用法: cdp_click_dyn.py X Y
page ID 自动发现；点击后回显 location.hash 供核对。"""
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
    x, y = float(sys.argv[1]), float(sys.argv[2])
    url = f"ws://127.0.0.1:{PORT}/devtools/page/{page_id()}"
    async with websockets.connect(url, max_size=20 * 1024 * 1024) as ws:
        mid = 0
        async def send(method, params):
            nonlocal mid
            mid += 1
            await ws.send(json.dumps({"id": mid, "method": method, "params": params}))
            while True:
                m = json.loads(await asyncio.wait_for(ws.recv(), 10))
                if m.get("id") == mid:
                    return m
        for meth, p in [("Input.dispatchMouseEvent", {"type": "mousePressed", "x": x, "y": y, "button": "left", "clickCount": 1}),
                        ("Input.dispatchMouseEvent", {"type": "mouseReleased", "x": x, "y": y, "button": "left", "clickCount": 1})]:
            await send(meth, p)
            await asyncio.sleep(0.08)
        mid += 1
        await ws.send(json.dumps({"id": mid, "method": "Runtime.evaluate",
                                  "params": {"expression": "location.hash", "returnByValue": True}}))
        while True:
            m = json.loads(await asyncio.wait_for(ws.recv(), 10))
            if m.get("id") == mid:
                print("hash:", m["result"]["result"].get("value")); break


asyncio.run(main())
