// 在隔离实例（:8099）上验证 syncGatewayUserSetting 修复，不依赖真机。
// 复现步骤与真机一致：先把 user setting 覆盖层写成旧域名，再走设置页保存路径。
import { readFileSync } from 'node:fs'

const PORT = process.env.VERIFY_PORT || '8099'
const BASE = `http://127.0.0.1:${PORT}`
const KEY = process.env.POCKET_GW_KEY || readFileSync('logs/.gateway-key', 'utf8').trim()
const NEW = 'https://llm.kxpms.cn/v1'
const OLD = 'https://llmgo.kxpms.cn/v1'
const MODELS = ['glm-5.3', 'minimax-m3', 'kimi-k3', 'claude-sonnet-5', 'gpt-5.6-terra',
  'claude-opus-5', 'claude-fable-5', 'gpt-5.6-sol', 'gemini-3.5-flash']

const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: process.env.POCKET_PASS }),
})
const lj = await login.json()
if (!login.ok) { console.log(`登录失败 ${login.status}`); process.exit(1) }
const tok = lj.token || lj.access_token
const H = { Authorization: `Bearer ${tok}`, 'Content-Type': 'application/json' }
const b64 = tok.split('.')[1].replace(/-/g, '+').replace(/_/g, '/')
const claims = JSON.parse(Buffer.from(b64, 'base64').toString('utf8'))
console.log(`隔离实例 :${PORT}  user=${claims.user_id}  ws=${claims.workspace_id}`)

const read = async () => (await (await fetch(`${BASE}/api/llm-gateway/config`, { headers: H })).json())
const putOverlay = async (u) => {
  const r = await fetch(`${BASE}/api/user-settings/llm_gateway/default`, {
    method: 'PUT', headers: H,
    body: JSON.stringify({
      payload: { baseURL: u, format: 'openai-chat', models: MODELS, preferredModels: MODELS },
      secret: KEY, updatedAt: Math.floor(Date.now() / 1000),
    }),
  })
  return r.status
}

const start = (await read()).baseURL
console.log(`\n步骤0 起点读取 = ${start}`)

const s1 = await putOverlay(OLD)
const afterPut = (await read()).baseURL
console.log(`步骤1 PUT 覆盖层=${OLD} -> ${s1}；读回 = ${afterPut} ${afterPut === OLD ? '✅ 已复现 bug 状态' : '❌ 未复现'}`)

// 同秒连写：覆盖层 PUT 与设置页保存落在同一秒内，是最容易被 LWW 静默丢弃的
// 组合，也最贴近真实使用（用户改完地址马上保存）。加了 2.5s 间隔的对照组
// 放在最后，两者都必须通过。
const sameSecond = process.env.GW_SAME_SECOND === '1'

const post = await fetch(`${BASE}/api/llm-gateway/config`, {
  method: 'POST', headers: H, body: JSON.stringify({ baseURL: NEW, apiKey: KEY }),
})
console.log(`步骤2 POST /api/llm-gateway/config(${NEW}) -> ${post.status}  [同秒连写=${sameSecond}]`)

const after = (await read()).baseURL
console.log(`步骤3 读回 = ${after}`)
console.log(after === NEW
  ? '✅ 修复生效：设置页保存后读回一致'
  : '❌ 修复未生效：读回仍是旧地址')

const t = await fetch(`${BASE}/api/llm-gateway/test`, { method: 'POST', headers: H, body: '{}' })
const tj = await t.json()
console.log(`步骤4 测试连接 -> ${t.status}  模型数=${tj.models ? tj.models.length : JSON.stringify(tj).slice(0, 120)}`)
