/**
 * verify-gateway-key-live.mjs
 *
 * 验证「网关本身是通的」——把问题从「部署有没有把 key 传进去」和
 * 「DB 里的 active 配置有没有 key」这两件事里剥离开来。
 *
 * 密钥只从环境变量读，不打印、不落盘、不进命令行历史。
 *
 * 用法：GATEWAY_KEY=<key> GATEWAY_BASE=<url> node verify-gateway-key-live.mjs
 */
const BASE = (process.env.GATEWAY_BASE || 'https://llm.kxpms.cn/v1').replace(/\/+$/, '')
const KEY = process.env.GATEWAY_KEY || ''
const MODEL = process.env.GATEWAY_MODEL || ''

let failures = 0
function step(name, ok, detail) {
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? '  ' + detail : ''}`)
  if (!ok) failures++
}

if (!KEY) {
  console.log('SKIP  未提供 GATEWAY_KEY，跳过（不猜测、不硬编码密钥）')
  process.exit(0)
}

// 1) /models —— 证明 key 可用且网关地址正确
let models = []
{
  const res = await fetch(`${BASE}/models`, {
    headers: { Authorization: `Bearer ${KEY}` },
  })
  const text = await res.text()
  let json = null
  try { json = JSON.parse(text) } catch { /* 非 JSON 也算一次失败 */ }
  models = Array.isArray(json?.data) ? json.data.map((m) => m.id) : []
  step('GET /models 用该 key 鉴权通过', res.status === 200,
    `status=${res.status} models=${models.length}`)
  if (res.status === 401 || res.status === 403) {
    step('key 未被拒', false, `网关拒绝了该 key (${res.status})`)
  }
}

// 2) chat/completions —— 证明端到端能出内容（这才是"即时总结"依赖的调用）
if (models.length > 0) {
  const model = MODEL || models[0]
  const res = await fetch(`${BASE}/chat/completions`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${KEY}`, 'Content-Type': 'application/json' },
    body: JSON.stringify({
      model,
      messages: [{ role: 'user', content: '只回复两个字：收到' }],
      max_tokens: 16,
    }),
  })
  const text = await res.text()
  let json = null
  try { json = JSON.parse(text) } catch { /* ignore */ }
  const content = json?.choices?.[0]?.message?.content
  step('POST /chat/completions 出内容', res.status === 200 && !!content,
    `status=${res.status} model=${model} content=${JSON.stringify(content)}`)
} else {
  step('POST /chat/completions 出内容', false, '没有可用模型，跳过（先修 /models）')
}

console.log(`\n模型数=${models.length}${models.length ? '，前几个: ' + models.slice(0, 5).join(', ') : ''}`)
console.log(failures === 0
  ? 'RESULT: 网关地址 + 该 key 可用（问题不在网关本身）'
  : `RESULT: ${failures} 项失败`)
process.exit(failures === 0 ? 0 : 1)
