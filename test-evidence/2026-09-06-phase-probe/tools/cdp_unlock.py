#!/usr/bin/env python3
"""CDP 主密码解锁 — 口令经 backend/start-dev.sh 缺省约定间接提取,
不经命令行参数/落盘明文(变量注入规约 §14.1)。
用法: CDP_PORT=19222 python3 cdp_unlock.py"""
import os, sys, re, json, asyncio, urllib.request
import websockets

PORT = os.environ.get("CDP_PORT", "9222")


def dev_passphrase():
    src = os.path.join(os.path.dirname(__file__), "../../../backend/start-dev.sh")
    m = re.search(r'POCKET_AUTH_PASS:-([^}"\']+)', open(os.path.abspath(src)).read())
    if not m:
        raise RuntimeError("dev passphrase convention not found in start-dev.sh")
    return m.group(1).strip()


def page_id():
    with urllib.request.urlopen(f"http://127.0.0.1:{PORT}/json/list", timeout=5) as r:
        for p in json.load(r):
            if p.get("type") == "page":
                return p["id"]
    raise RuntimeError("no page")


async def main():
    pw = dev_passphrase()
    js = """
(async () => {
  const inp = document.querySelector('input[type="password"]');
  if (!inp) return JSON.stringify({ok: false, why: 'no password input', hash: location.hash});
  const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set;
  setter.call(inp, %s);
  inp.dispatchEvent(new Event('input', {bubbles: true}));
  await new Promise(r => setTimeout(r, 300));
  const btn = [...document.querySelectorAll('button')].find(b => /解锁/.test(b.innerText));
  if (!btn) return JSON.stringify({ok: false, why: 'no unlock button'});
  btn.click();
  await new Promise(r => setTimeout(r, 2500));
  return JSON.stringify({ok: true, hash: location.hash, snippet: document.body.innerText.slice(0, 200)});
})()
""" % json.dumps(pw)
    url = f"ws://127.0.0.1:{PORT}/devtools/page/{page_id()}"
    async with websockets.connect(url, max_size=20 * 1024 * 1024) as ws:
        await ws.send(json.dumps({"id": 1, "method": "Runtime.evaluate",
                                  "params": {"expression": js, "awaitPromise": True,
                                             "returnByValue": True}}))
        while True:
            msg = json.loads(await asyncio.wait_for(ws.recv(), timeout=20))
            if msg.get("id") == 1:
                r = msg.get("result", {})
                if "exceptionDetails" in r:
                    print("EXCEPTION:", json.dumps(r["exceptionDetails"])[:500]); return 1
                print(r.get("result", {}).get("value"))
                return 0


sys.exit(asyncio.run(main()))
