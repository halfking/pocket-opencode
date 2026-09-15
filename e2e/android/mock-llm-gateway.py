#!/usr/bin/env python3
"""mock-llm-gateway.py — 本地确定性 OpenAI 兼容 mock 网关(本地智能体 E2E 用)。

  python3 mock-llm-gateway.py [port]   # 默认 18099

端点:
  GET  /v1/models                 → {"data":[{"id":"mock-agent-model"}]}
  POST /v1/chat/completions       → SSE 流式(chat.completion.chunk 帧)

脚本逻辑(按最后一条 user 消息内容确定性决策):
  1. 含 <tool_result → 工具已执行:
     - ok="true" 且含 "289"              → 终答:计算结果 289
     - ok="true" 且含 write_file 语义    → 终答:已保存(买牛奶/预约牙医)
     - ok="true" 且含 example.com        → 终答:标题 Example Domain
     - ok="false"(含 用户拒绝/拒绝)     → 终答:承认被拒、改道回答
     - 其他失败                          → 终答:工具失败说明
  2. 无 <tool_result → 发起工具调用(围栏 JSON):
     - 23*7 或 calculate → calculate(23*7+128)
     - todo.md / 保存    → write_file(notes/todo.md)
     - http_fetch / 抓取 → http_fetch(https://example.com)
     - task_plan / 计划  → task_plan(set 三条)
     - 其他              → 直接文本回答(不调工具)
"""
import json, sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 18099
LOG = open('/tmp/mock-llm.log', 'a')

TOOL_CALLS = {
    'calc': '我来精确计算。\n```json\n{"tool": "calculate", "args": {"expression": "23*7+128"}}\n```',
    'write': '我来保存文件。\n```json\n{"tool": "write_file", "args": {"path": "notes/todo.md", "content": "买牛奶\\n预约牙医"}}\n```',
    'fetch': '我来抓取页面。\n```json\n{"tool": "http_fetch", "args": {"url": "https://example.com"}}\n```',
    'plan': '先建立计划。\n```json\n{"tool": "task_plan", "args": {"action": "set", "items": "[{\\"title\\":\\"计算\\",\\"status\\":\\"in_progress\\"},{\\"title\\":\\"保存\\",\\"status\\":\\"todo\\"},{\\"title\\":\\"汇报\\",\\"status\\":\\"todo\\"}]"}}\n```',
}

FINALS = {
    'calc_ok': '计算完成:23*7+128 = **289**。',
    'write_ok': '已保存到 notes/todo.md,共 2 条:买牛奶;预约牙医。',
    'fetch_ok': '页面标题是 Example Domain。',
    'denied': '好的,你拒绝了该工具执行,我不再重试。基于我已有的知识直接回答。',
    'fail': '工具执行失败了,我直接说明已知信息。',
}


def decide(messages):
    last_user = ''
    for m in reversed(messages):
        if m.get('role') == 'user':
            last_user = m.get('content') or ''
            break
    if '<tool_result' in last_user:
        if 'ok="false"' in last_user:
            return [FINALS['denied']]
        if '289' in last_user:
            return [FINALS['calc_ok']]
        if 'write_file' in last_user or '已写入' in last_user:
            return [FINALS['write_ok']]
        if 'Example Domain' in last_user:
            return [FINALS['fetch_ok']]
        return [FINALS['fail']]
    low = last_user
    if '23*7' in low or 'calculate' in low:
        return [TOOL_CALLS['calc']]
    if 'todo.md' in low or '保存' in low:
        return [TOOL_CALLS['write']]
    if 'http_fetch' in low or '抓取' in low:
        return [TOOL_CALLS['fetch']]
    if 'task_plan' in low or '建立计划' in low:
        return [TOOL_CALLS['plan']]
    return ['收到:' + low[:40]]


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        LOG.write('%s\n' % (fmt % args))
        LOG.flush()

    def _json(self, obj, status=200):
        body = json.dumps(obj).encode()
        self.send_response(status)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(body)))
        self.send_header('Access-Control-Allow-Origin', '*')
        self.end_headers()
        self.wfile.write(body)

    def do_OPTIONS(self):
        self.send_response(200)
        self.send_header('Access-Control-Allow-Origin', '*')
        self.send_header('Access-Control-Allow-Methods', 'GET, POST, OPTIONS')
        self.send_header('Access-Control-Allow-Headers', 'Content-Type, Authorization')
        self.end_headers()

    def do_GET(self):
        if self.path.startswith('/v1/models'):
            self._json({'object': 'list', 'data': [{'id': 'mock-agent-model', 'object': 'model'}]})
        else:
            self._json({'error': 'not found'}, 404)

    def do_POST(self):
        if not self.path.startswith('/v1/chat/completions'):
            self._json({'error': 'not found'}, 404)
            return
        n = int(self.headers.get('Content-Length') or 0)
        req = json.loads(self.rfile.read(n) or b'{}')
        parts = decide(req.get('messages') or [])
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.send_header('Cache-Control', 'no-cache')
        self.send_header('Access-Control-Allow-Origin', '*')
        self.end_headers()
        full = ''.join(parts)
        # 分 3 帧发正文,末帧带 usage + finish_reason,再 [DONE]。
        third = max(1, len(full) // 3)
        chunks = [full[0:third], full[third:2 * third], full[2 * third:]]
        for i, c in enumerate(chunks):
            if not c:
                continue
            frame = {'id': 'mock-1', 'object': 'chat.completion.chunk', 'model': 'mock-agent-model',
                     'choices': [{'index': 0, 'delta': {'content': c}, 'finish_reason': None}]}
            self.wfile.write(f'data: {json.dumps(frame, ensure_ascii=False)}\n\n'.encode())
            self.wfile.flush()
        frame = {'id': 'mock-1', 'object': 'chat.completion.chunk', 'model': 'mock-agent-model',
                 'choices': [{'index': 0, 'delta': {}, 'finish_reason': 'stop'}],
                 'usage': {'prompt_tokens': 120, 'completion_tokens': 40, 'total_tokens': 160}}
        self.wfile.write(f'data: {json.dumps(frame, ensure_ascii=False)}\n\n'.encode())
        self.wfile.write(b'data: [DONE]\n\n')
        self.wfile.flush()
        LOG.write('served: %s\n' % full[:80].replace('\n', ' '))
        LOG.flush()


if __name__ == '__main__':
    print('mock llm gateway on :%d' % PORT)
    ThreadingHTTPServer(('127.0.0.1', PORT), Handler).serve_forever()
