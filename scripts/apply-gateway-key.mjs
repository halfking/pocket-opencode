/**
 * apply-gateway-key.mjs
 *
 * 把网关配置写进真实数据库，走的是应用自己的保存路径
 * （POST /api/llm-gateway/config），不是直接改表——这样加密、active 行切换、
 * 用户级设置同步、审计日志全都由现有代码完成，不绕过任何逻辑。
 *
 * 密钥只从环境变量读，不打印、不落盘。
 *
 * 用法：
 *   POCKET_BASE=http://127.0.0.1:8096 POCKET_USER=admin POCKET_PASS=... \
 *   GATEWAY_KEY=... GATEWAY_BASE=https://llm.kxpms.cn/v1 node apply-gateway-key.mjs
 */
const BASE = (process.env.POCKET_BASE || 'http://127.0.0.1:8096').replace(/\/+$/, '')
const USER = process.env.POCKET_USER || 'admin'
const PASS = process.env.POCKET_PASS || ''
const KEY = process.env.GATEWAY_KEY || ''
const GW = (process.env.GATEWAY_BASE || 'https://llm.kxpms.cn/v1').replace(/\/+$/, '')

let failures = 0
function step(name, ok, detail) {
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? '  ' + detail : ''}`)
  if (!ok) failures++
}

async function api(path, opts = {}) {
  const res = await fetch(BASE + path, {
    ...opts,
    headers: {
      'Content-Type': 'application/json',
      ...(opts.token ? { Authorization: `Bearer ${opts.token}` } : {}),
      ...(opts.headers || {}),
    },
  })
  const text = await res.text()
  let json = null
  try { json = JSON.parse(text) } catch { /* ignore */ }
  return { status: res.status, json, text }
}

if (!KEY || !PASS) {
  console.log('SKIP  未提供 GATEWAY_KEY / POCKET_PASS，跳过（不猜测、不硬编码）')
  process.exit(0)
}

// 1) 登录
const login = await api('/api/auth/login', {
  method: 'POST',
  body: JSON.stringify({ username: USER, password: PASS }),
})
step('登录', login.status === 200, `status=${login.status}`)
if (login.status !== 200) {
  console.log('  登录失败，无法继续。响应：' + login.text.slice(0, 200))
  process.exit(1)
}
const token = login.json?.token
const ws = login.json?.workspace_id
step('拿到 token', !!token, `workspace_id=${ws}`)
if (!token) process.exit(1)

// 2) 保存前先读一次，作为 before 证据
const before = await api('/api/llm-gateway/config', { token })
step('保存前 GET 成功', before.status === 200,
  `status=${before.status} apiKeySet=${before.json?.apiKeySet} baseURL=${before.json?.baseURL}`)

// 3) 写入（走应用自己的保存路径）
const save = await api('/api/llm-gateway/config', {
  method: 'POST',
  token,
  body: JSON.stringify({
    baseURL: GW,
    apiKey: KEY,
    models: [],
    format: 'openai-chat',
    preferredModels: [],
  }),
})
step('POST /api/llm-gateway/config', save.status === 200, `status=${save.status} ${save.text.slice(0, 160)}`)

// 4) 保存后再读，验证真的生效
const after = await api('/api/llm-gateway/config', { token })
step('保存后 apiKeySet === true', after.json?.apiKeySet === true,
  `apiKeySet=${after.json?.apiKeySet} baseURL=${after.json?.baseURL}`)

// 5) 真打一次网关，确认端到端通
const gwres = await fetch(`${GW}/chat/completions`, {
  method: 'POST',
  headers: { Authorization: `Bearer ${KEY}`, 'Content-Type': 'application/json' },
  body: JSON.stringify({
    model: 'abab5.5-chat',
    messages: [{ role: 'user', content: '只回复两个字：收到' }],
    max_tokens: 16,
  }),
})
const gwjson = await gwres.json().catch(() => null)
step('网关 chat/completions 可用', gwres.status === 200 && !!gwjson?.choices?.[0]?.message?.content,
  `status=${gwres.status}`)

console.log(failures === 0 ? '\nRESULT: 网关已写入并验证可用' : `\nRESULT: ${failures} 项失败`)
process.exit(failures === 0 ? 0 : 1)
