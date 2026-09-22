"""webview-cdp.py — Minimal Chrome DevTools Protocol client over WebSocket.

Reads params JSON from a file passed as -f, or from stdin, or from arg[2].

Usage:
    python scripts/webview-cdp.py Runtime.evaluate '{"expression":"1+1"}'
    python scripts/webview-cdp.py Runtime.evaluate -f params.json
    python scripts/webview-cdp.py Runtime.evaluate < params.json
"""
import asyncio
import json
import os
import sys
import urllib.request


async def main(method: str, params_json: str) -> None:
    pages = None
    last_err = None
    for attempt in range(3):
        try:
            with urllib.request.urlopen("http://localhost:9222/json", timeout=5) as r:
                pages = json.loads(r.read())
            break
        except Exception as e:
            last_err = e
            print(f"  retry {attempt+1}: {e}", file=sys.stderr)
            await asyncio.sleep(1)
    if not pages:
        print(f"Cannot reach DevTools: {last_err}", file=sys.stderr)
        sys.exit(1)
    page = next((p for p in pages if p.get("type") == "page"), None)
    if not page:
        print("No WebView page found", file=sys.stderr)
        sys.exit(1)
    print(f"page: {page['title']} ({page['url']})", file=sys.stderr)

    import websockets

    async with websockets.connect(page["webSocketDebuggerUrl"], max_size=2**24) as ws:
        params = json.loads(params_json) if params_json.strip() else {}
        msg = {"id": 1, "method": method, "params": params}
        await ws.send(json.dumps(msg))
        while True:
            raw = await ws.recv()
            data = json.loads(raw)
            if data.get("id") == 1:
                print(json.dumps(data, indent=2, ensure_ascii=False))
                break


if __name__ == "__main__":
    if len(sys.argv) < 2:
        print(__doc__)
        sys.exit(1)
    method = sys.argv[1]
    params_json = ""
    if len(sys.argv) > 2:
        if sys.argv[2] == "-f" and len(sys.argv) > 3:
            with open(sys.argv[3], "r", encoding="utf-8") as fh:
                params_json = fh.read()
        else:
            params_json = sys.argv[2]
    elif not sys.stdin.isatty():
        params_json = sys.stdin.read()
    asyncio.run(main(method, params_json))