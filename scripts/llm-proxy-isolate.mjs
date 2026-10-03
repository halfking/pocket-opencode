#!/usr/bin/env node
/**
 * 分离「代理」与「网关」两个因素（用系统 curl.exe，不引第三方依赖）。
 *
 * 已确认事实（本轮实测）：
 *   GET  https://llm.kxpms.cn/v1/models            → 200，604 个模型
 *   POST https://llm.kxpms.cn/v1/chat/completions   → 三个模型各 60s 超时（裸连）
 *   pocketd /api/llm/chat                          → fetch failed
 *
 * GET 通而 POST 挂，需要分清是网关 chat 端点不响应，还是出网路径的问题。
 * 环境里 HTTP_PROXY / HTTPS_PROXY 指向 192.168.31.34:7897（已知的废弃端口）。
 *
 * 四组对照：
 *   A 裸连 GET  /models      B 裸连 POST /chat
 *   C 走代理 GET /models     D 走代理 POST /chat
 * A/C 通而 B 挂 → 网关 chat 端点问题；B 通而 D 挂 → 代理问题。
 *
 * 密钥从 logs/.gateway-key 读，不写进任何落盘文件，也不打印。
 *
 * Run: node scripts/llm-proxy-isolate.mjs
 */
import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { execFileSync } from 'node:child_process'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const key = readFileSync(join(ROOT, 'logs', '.gateway-key'), 'utf8').trim()
const BASE = 'https://llm.kxpms.cn/v1'
const PROXY = 'http://192.168.31.34:7897'
const BODY = JSON.stringify({
  model: 'claude-sonnet-4-6',
  messages: [{ role: 'user', content: 'hi' }],
  max_tokens: 16,
})

const curl = (label, args) => {
  const t0 = Date.now()
  try {
    const out = execFileSync('curl.exe', args, { encoding: 'utf8', timeout: 30000, maxBuffer: 1 << 20 })
    return { label, ok: true, ms: Date.now() - t0, head: out.replace(/\s+/g, ' ').slice(0, 200) }
  } catch (e) {
    // curl 用退出码表达失败：28=超时 7=连不上 56=接收错误
    return { label, ok: false, ms: Date.now() - t0, curlExit: e.status, stderr: String(e.stderr || '').slice(0, 200) }
  }
}

const auth = `Authorization: Bearer ${key}`
const results = [
  curl('A 裸连 GET /models', ['-sS', '--noproxy', '*', '--max-time', '25', '-H', auth, `${BASE}/models`]),
  curl('B 裸连 POST /chat', ['-sS', '--noproxy', '*', '--max-time', '25', '-X', 'POST', '-H', auth, '-H', 'Content-Type: application/json', '-d', BODY, `${BASE}/chat/completions`]),
  curl('C 走代理 GET /models', ['-sS', '--proxy', PROXY, '--max-time', '25', '-H', auth, `${BASE}/models`]),
  curl('D 走代理 POST /chat', ['-sS', '--proxy', PROXY, '--max-time', '25', '-X', 'POST', '-H', auth, '-H', 'Content-Type: application/json', '-d', BODY, `${BASE}/chat/completions`]),
  curl('E 代理端口本身', ['-sS', '--max-time', '8', '-o', 'NUL', '-w', 'proxy_http_code=%{http_code}', '--proxy', PROXY, 'http://example.com/']),
]

console.log(JSON.stringify({ proxy: PROXY, results }, null, 1))
