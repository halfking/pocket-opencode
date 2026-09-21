"""WebSocket devtools helper - bypasses /json discovery."""
import socket
import struct
import sys
import json
import argparse


GUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"


def connect(host: str, port: int, path: str):
    s = socket.create_connection((host, port), timeout=10)
    key = "dGhlIHNhbXBsZSBub25jZQ=="
    req = (
        f"GET {path} HTTP/1.1\r\n"
        f"Host: {host}:{port}\r\n"
        f"Upgrade: websocket\r\n"
        f"Connection: Upgrade\r\n"
        f"Sec-WebSocket-Key: {key}\r\n"
        f"Sec-WebSocket-Version: 13\r\n\r\n"
    )
    s.sendall(req.encode())
    buf = b""
    while b"\r\n\r\n" not in buf:
        c = s.recv(4096)
        if not c:
            raise RuntimeError(f"handshake closed; head={buf!r}")
        buf += c
    head = buf.split(b"\r\n\r\n", 1)[0]
    if b"101" not in head:
        raise RuntimeError(f"ws failed: {head!r}")
    return s


def send_frame(sock, payload: bytes):
    header = bytes([0x81])  # FIN + text frame
    n = len(payload)
    if n < 126:
        header += bytes([n])
    elif n < 65536:
        header += bytes([126]) + struct.pack(">H", n)
    else:
        header += bytes([127]) + struct.pack(">Q", n)
    mask = b"\x01\x02\x03\x04"  # any
    masked = bytes(b ^ mask[i & 3] for i, b in enumerate(payload))
    sock.sendall(header + bytes([0x80 | 0]) + mask + masked)


def recv_frame(sock):
    head = b""
    while len(head) < 2:
        c = sock.recv(2 - len(head))
        if not c: raise RuntimeError("eof in head")
        head += c
    b1, b2 = head[0], head[1]
    opcode = b1 & 0x0F
    masked = b2 & 0x80
    L = b2 & 0x7F
    idx_in = 2
    if L == 126:
        ext = b""
        while len(ext) < 2:
            ext += sock.recv(2 - len(ext))
        L = struct.unpack(">H", ext)[0]
    elif L == 127:
        ext = b""
        while len(ext) < 8:
            ext += sock.recv(8 - len(ext))
        L = struct.unpack(">Q", ext)[0]
    body = b""
    while len(body) < L:
        body += sock.recv(L - len(body))
    return opcode, body


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--host", default="127.0.0.1")
    ap.add_argument("--port", type=int, default=9333)
    ap.add_argument("--path", default="/webview/devtools/inspector")
    ap.add_argument("--expr", required=True)
    ap.add_argument("--wait", type=float, default=3.0)
    args = ap.parse_args()

    s = connect(args.host, args.port, args.path)
    msg = json.dumps({"id": 1, "method": "Runtime.evaluate", "params": {"expression": args.expr, "returnByValue": True}})
    send_frame(s, msg.encode())
    s.settimeout(args.wait)
    try:
        op, body = recv_frame(s)
        print(body.decode("utf-8", errors="replace"))
    except Exception as e:
        print(f"err: {e}", file=sys.stderr)
        sys.exit(2)


if __name__ == "__main__":
    main()
