#!/usr/bin/env node
/**
 * 排掉「端点形态选错」这个比「网关坏了」更可能、且可修的可能。
 *
 * 已确认：GET /v1/models 通（243ms），POST /v1/chat/completions 25s 超时且 0 bytes，
 * 裸连与走代理表现一致 → 代理无关。
 *
 * 剩下两种解释：
 *   ① 网关的 chat/completions 本身不响应（上游问题，代码改不了）
 *   ② 端点形态选错：网关只认 /v1/messages（Anthropic）或 /v1/responses（OpenAI 新形态）
 *      —— 配置里 formats 恰好列了 anthropic-messages 与 openai-responses
 *
 * 这里把三种形态各打一次，看哪种能出字。
 *
 * Run: node scripts/llm-endpoint-shapes.mjs
 */
import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { execFileSync } from 'node:child_process'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const key = readFileSync(join(ROOT, 'logs', '.gateway-key'), 'utf8').trim()
const V1 = 'https://llm.kxpms.cn/v1'
const MODEL = 'claude-sonnet-4-6'

const auth = `Authorization: Bearer ${key}`
const anthropic = `x-api-key: ${key}`

const shapes = [
  {
    label: '1 OpenAI chat/completions',
    args: ['-X', 'POST', '-H', auth, '-H', 'Content-Type: application/json',
      '-d', JSON.stringify({ model: MODEL, messages: [{ role: 'user', content: 'hi' }], max_tokens: 16 }),
      `${V1}/chat/completions`],
  },
  {
    label: '2 Anthropic /messages',
    args: ['-X', 'POST', '-H', anthropic, '-H', 'anthropic-version: 2023-06-01', '-H', 'Content-Type: application/json',
      '-d', JSON.stringify({ model: MODEL, max_tokens: 16, messages: [{ role: 'user', content: 'hi' }] }),
      `${V1}/messages`],
  },
  {
    label: '3 OpenAI /responses',
    args: ['-X', 'POST', '-H', auth, '-H', 'Content-Type: application/json',
      '-d', JSON.stringify({ model: MODEL, input: 'hi', max_output_tokens: 16 }),
      `${V1}/responses`],
  },
  {
    label: '4 chat/completions + stream=true',
    args: ['-X', 'POST', '-H', auth, '-H', 'Content-Type: application/json',
      '-d', JSON.stringify({ model: MODEL, messages: [{ role: 'user', content: 'hi' }], max_tokens: 16, stream: true }),
      `${V1}/chat/completions`],
  },
  {
    label: '5 鉴权故意写错（对照组）',
    args: ['-X', 'POST', '-H', 'Authorization: Bearer sk-invalid-key-for-control' /* secret-scan-ok: 对照组故意写错的密钥 */, '-H', 'Content-Type: application/json',
      '-d', JSON.stringify({ model: MODEL, messages: [{ role: 'user', content: 'hi' }], max_tokens: 16 }),
      `${V1}/chat/completions`],
  },
]

const results = shapes.map(({ label, args }) => {
  const t0 = Date.now()
  try {
    const out = execFileSync('curl.exe', ['-sS', '--noproxy', '*', '--max-time', '20', ...args],
      { encoding: 'utf8', timeout: 30000, maxBuffer: 1 << 20 })
    return { label, ok: true, ms: Date.now() - t0, head: out.replace(/\s+/g, ' ').slice(0, 220) }
  } catch (e) {
    return { label, ok: false, ms: Date.now() - t0, curlExit: e.status, stderr: String(e.stderr || '').trim().slice(0, 160) }
  }
})

console.log(JSON.stringify({ model: MODEL, results }, null, 1))
