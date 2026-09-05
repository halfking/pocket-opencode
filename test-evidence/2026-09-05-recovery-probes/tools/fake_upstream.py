#!/usr/bin/env python3
"""假上游：确定性复现「空流候选」与「正常候选」——验证空流透传修复（§28.3）。

POST /v1/chat/completions:
  model=fake-empty → 200 + SSE 仅 `data: [DONE]`（零 delta 空流形态）
  model=fake-good  → 200 + 正常 OpenAI SSE（正文若干帧 + finish/usage + [DONE]）
  stream=false     → 普通 JSON completion（Chat 路径用）
"""
import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 18099


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        sys.stderr.write("[fake-upstream] " + fmt % args + "\n")

    def _sse(self, frames):
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        for f in frames:
            self.wfile.write(f"data: {json.dumps(f, ensure_ascii=False)}\n\n".encode())
        self.wfile.write(b"data: [DONE]\n\n")

    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0))
        req = json.loads(self.rfile.read(n) or b"{}")
        model = req.get("model", "")
        stream = bool(req.get("stream"))
        self.log_message("model=%s stream=%s", model, stream)
        if model != "fake-good":
            # 任何其它模型（含 fake-empty）都回「200+零帧」空流
            if stream:
                self._sse([])
            else:
                body = json.dumps({"choices": [], "model": model, "usage": {
                    "prompt_tokens": 1, "completion_tokens": 0, "total_tokens": 1}}).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(body)
            return
        if stream:
            self._sse([
                {"choices": [{"delta": {"content": "会议纪要：项目进展正常。"}, "finish_reason": None}]},
                {"choices": [{"delta": {}, "finish_reason": "stop"}],
                 "usage": {"prompt_tokens": 10, "completion_tokens": 8, "total_tokens": 18}},
            ])
        else:
            body = json.dumps({"choices": [{"message": {"role": "assistant", "content": "会议纪要：项目进展正常。"}}],
                               "model": "fake-good", "usage": {
                "prompt_tokens": 10, "completion_tokens": 8, "total_tokens": 18}}).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(body)


if __name__ == "__main__":
    HTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
