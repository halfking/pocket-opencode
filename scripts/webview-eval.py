"""
WebView Chrome DevTools Protocol evaluator.

Connects to the WebView's devtools socket via adb forward, evaluates arbitrary JS,
returns the result.

Usage:
  python scripts/webview-eval.py --socket webview_devtools_remote_22886 \\
          --port 9333 --expr "window.location.href"

Returns 0 on success, prints the result to stdout.
"""
import argparse
import json
import socket
import sys
import time
import urllib.request

DEFAULT_PORTS = [9333, 9444, 9555]


def fetch_target(port: int, timeout: float = 5.0):
    """
    Try a few JSON endpoints to discover the WS URL of the WebView page.
    The Android WebView devtools socket accepts /json/version (similar to Chrome)
    but the standard /json/list call has been restricted by some OEM ROMs.
    We try both, and on success return the page websocket URL.
    """
    candidates = [
        f"http://127.0.0.1:{port}/json/version",
        f"http://127.0.0.1:{port}/json",
        f"http://127.0.0.1:{port}/json/list",
        f"http://127.0.0.1:{port}/json/protocol",
    ]
    for url in candidates:
        try:
            with urllib.request.urlopen(url, timeout=timeout) as resp:
                data = resp.read().decode()
                return json.loads(data)
        except Exception:
            continue
    return None


def probe(port: int, count: int = 3):
    """Try consecutive ports. Returns first port that responds or None."""
    for offset in range(count):
        try:
            data = fetch_target(port + offset)
            if data:
                return port + offset, data
        except Exception:
            continue
    return None, None


# Inline ws client — avoid pulling external deps
class WSClient:
    GUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

    def __init__(self, host: str, port: int, path: str):
        self.sock = socket.create_connection((host, port), timeout=10)
        key = "ABCDEFGHIJKLMNOPQRSTUVWXYZ=="
        req = (
            f"GET {path} HTTP/1.1\r\n"
            f"Host: {host}:{port}\r\n"
            f"Upgrade: websocket\r\n"
            f"Connection: Upgrade\r\n"
            f"Sec-WebSocket-Key: {key}\r\n"
            f"Sec-WebSocket-Version: 13\r\n\r\n"
        )
        self.sock.sendall(req.encode())
        # read until \r\n\r\n
        buf = b""
        while b"\r\n\r\n" not in buf:
            chunk = self.sock.recv(4096)
            if not chunk:
                raise RuntimeError("ws handshake failed")
            buf += chunk
        head, _, rest = buf.partition(b"\r\n\r\n")
        # naive: server returns 101 if accepted; we don't actually verify
        self.buf = rest

    def _read_frame(self):
        while len(self.buf) < 2:
            self.buf += self.sock.recv(4096)
        b1, b2 = self.buf[0], self.buf[1]
        opcode = b1 & 0x0F
        masked = b2 & 0x80
        length = b2 & 0x7F
        idx = 2
        if length == 126:
            while len(self.buf) < idx + 2:
                self.buf += self.sock.recv(4096)
            length = int.from_bytes(self.buf[idx:idx+2], "big"); idx += 2
        elif length == 127:
            while len(self.buf) < idx + 8:
                self.buf += self.sock.recv(4096)
            length = int.from_bytes(self.buf[idx:idx+8], "big"); idx += 8
        if masked:
            while len(self.buf) < idx + 4:
                self.buf += self.sock.recv(4096)
            mask = self.buf[idx:idx+4]; idx += 4
        else:
            mask = b""
        while len(self.buf) < idx + length:
            self.buf += self.sock.recv(65536)
        payload = self.buf[idx:idx+length]
        self.buf = self.buf[idx+length:]
        if mask:
            payload = bytes(b ^ mask[i & 3] for i, b in enumerate(payload))
        return opcode, payload

    def send_text(self, msg: str):
        data = msg.encode()
        header = bytes([0x81])  # FIN + text
        L = len(data)
        if L < 126:
            header += bytes([0x80 | L])
        elif L < 65536:
            header += bytes([0x80 | 126]) + L.to_bytes(2, "big")
        else:
            header += bytes([0x80 | 127]) + L.to_bytes(8, "big")
        mask = b"\x00\x00\x00\x00"  # client must mask but skipping for brevity
        # server-side here doesn't enforce mask for some devtools impls
        self.sock.sendall(header + mask + data)

    def recv_frame(self):
        opcode, payload = self._read_frame()
        return opcode, payload.decode("utf-8", errors="replace")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--host", default="127.0.0.1")
    ap.add_argument("--port", type=int, default=9333)
    ap.add_argument("--path", default="/devtools/browser")
    ap.add_argument("--path-page", default="/devtools/page")
    ap.add_argument("--expr", required=True)
    ap.add_argument("--wait-ms", type=int, default=1500)
    args = ap.parse_args()

    # Discover endpoints
    data = fetch_target(args.port)
    if data is None:
        print(f"ERR: no devtools HTTP at 127.0.0.1:{args.port}; cannot connect", file=sys.stderr)
        sys.exit(2)

    print("devtools info:", json.dumps(data)[:300])
    # Some implementations return browser-level version only; pull page URLs
    pages = []
    try:
        with urllib.request.urlopen(f"http://{args.host}:{args.port}/json", timeout=5) as r:
            pages = json.loads(r.read().decode())
    except Exception:
        try:
            with urllib.request.urlopen(f"http://{args.host}:{args.port}/json/list", timeout=5) as r:
                pages = json.loads(r.read().decode())
        except Exception:
            pages = []

    if not pages:
        print("ERR: /json returned no targets; check forward", file=sys.stderr)
        sys.exit(3)

    target = next((p for p in pages if p.get("type") == "page"), pages[0])
    ws_url = target.get("webSocketDebuggerUrl")
    if not ws_url:
        print("ERR: no webSocketDebuggerUrl on target", file=sys.stderr)
        sys.exit(4)
    print("target:", ws_url)

    # parse ws url
    if not ws_url.startswith("ws://"):
        print("ERR: unexpected ws url", file=sys.stderr); sys.exit(5)
    rest = ws_url[5:]
    host_port, path = rest.split("/", 1)
    host, port = host_port.split(":")
    port = int(port)
    if host in ("0.0.0.0", "127.0.0.1", "localhost"):
        host = args.host

    client = WSClient(host, port, "/" + path)
    msg_id = 1
    payload = {"id": msg_id, "method": "Runtime.evaluate", "params": {"expression": args.expr, "returnByValue": True, "awaitPromise": True}}
    client.send_text(json.dumps(payload))
    deadline = time.time() + (args.wait_ms / 1000.0)
    while time.time() < deadline:
        try:
            opcode, txt = client.recv_frame()
        except Exception as e:
            print(f"ERR: ws recv: {e}", file=sys.stderr); sys.exit(6)
        if opcode != 1:
            continue
        try:
            d = json.loads(txt)
        except Exception:
            print(txt); sys.exit(0)
        if d.get("id") == msg_id:
            print(json.dumps(d, indent=2, ensure_ascii=False))
            sys.exit(0)
    print("ERR: timeout", file=sys.stderr); sys.exit(7)


if __name__ == "__main__":
    main()
