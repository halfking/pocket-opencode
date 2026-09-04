#!/usr/bin/env python3
"""CDP Input 域受信任点击 — 用法: cdp_click.py X Y [页面ID]"""
import sys, json, asyncio
import websockets

URL = "ws://127.0.0.1:9222/devtools/page/53B93E5128662D8D740492AA73CBCB3D"

async def main():
    x, y = float(sys.argv[1]), float(sys.argv[2])
    async with websockets.connect(URL, max_size=20 * 1024 * 1024) as ws:
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
        # 验证 hash
        mid += 1
        await ws.send(json.dumps({"id": mid, "method": "Runtime.evaluate",
                                  "params": {"expression": "location.hash", "returnByValue": True}}))
        while True:
            m = json.loads(await asyncio.wait_for(ws.recv(), 10))
            if m.get("id") == mid:
                print("hash:", m["result"]["result"].get("value")); break

asyncio.run(main())
