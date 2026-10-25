#!/usr/bin/env node
/**
 * 夹具：造出「ACC 派单正在等人工审批」这个前提（induce），事后清掉（restore）。
 *
 * ★ 为什么前提由夹具造，而不是让 flow 去点「委托给 ACC」：
 *
 *   1. 弹窗里的输入框在无障碍树里是 `[EditText] t="" cd=""`（placeholder 与
 *      label 都不暴露）⇒ 只能按坐标点 ⇒ 而弹窗里任何一处内容改动都会让
 *      sheet 变高、坐标全部位移（实测：sheet 顶部从 y≈1200 变成 y=0，
 *      标题输入框从 y≈1350 移到 y≈324）。⇒ 坐标判据天生脆弱。
 *   2. 「委托给 ACC」这条链在设备上已经由 .maestro/acc-delegate-stop.yaml
 *      覆盖（含派单 + 停止）。这条流要验的是**审批面**：App 看得到待批项、
 *      答得动、答完任务往下走。
 *
 * 所以这里用 API 直接委托一单「要写文件」的任务（智能体必然要审批），
 * 然后等 daemon 把待批项挂上（`GET /api/acc/approvals` 出现 pending）。
 *
 * 用法（由 scripts/maestro-run.mjs 按 .maestro-flows.json 的 fixture 字段调用）：
 *   node scripts/acc-approval-fixture.mjs --induce
 *   node scripts/acc-approval-fixture.mjs --restore
 */
import { readFileSync, existsSync } from 'node:fs'
import { homedir } from 'node:os'
import { resolve } from 'node:path'

const ROOT = resolve(new URL('..', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1'))
const BASE = process.env.POCKET_API_BASE || 'http://127.0.0.1:18099'
const TITLE = 'maestro-acc-approve'
const ENV_FILE = `${homedir()}/kaixuan/acp/logs/rerun-round52/pocketd-18099.env`

function loadEnv() {
  const out = {}
  if (!existsSync(ENV_FILE)) return out
  for (const raw of readFileSync(ENV_FILE, 'utf8').split('\n')) {
    let line = raw.trim()
    if (line.startsWith('export ')) line = line.slice(7)
    if (!line || line.startsWith('#') || !line.includes('=')) continue
    const i = line.indexOf('=')
    out[line.slice(0, i).trim()] = line.slice(i + 1).trim().replace(/^["']|["']$/g, '')
  }
  return out
}

const env = loadEnv()

async function api(method, path, body, token) {
  const headers = { 'Content-Type': 'application/json' }
  if (token) headers.Authorization = 'Bearer ' + token
  const res = await fetch(BASE + path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const text = await res.text()
  let parsed = null
  try { parsed = JSON.parse(text) } catch { parsed = text }
  return { status: res.status, body: parsed }
}

async function login() {
  const r = await api('POST', '/api/auth/login', {
    username: env.POCKET_AUTH_USER || 'admin',
    password: env.POCKET_AUTH_PASS || '',
  })
  if (r.status !== 200 || !r.body || !r.body.token) {
    throw new Error(`登录失败 ${r.status}：${JSON.stringify(r.body).slice(0, 200)}`)
  }
  return r.body.token
}

function rows(body) {
  if (!body) return []
  if (Array.isArray(body)) return body
  if (Array.isArray(body.tasks)) return body.tasks
  if (Array.isArray(body.data)) return body.data
  return []
}

/** 删掉同名残留 —— 不删的话 flow 断言会假绿（残留卡片也算「可见」）。 */
async function clearResidual(token) {
  const r = await api('GET', '/api/tasks', undefined, token)
  // 清掉**整个 maestro-* 同族**的残留：flow 最后一步断言屏上的「已完成」，
  // 别的 maestro-* 卡片若也是已完成，那一步会对错了行。
  const hit = rows(r.body).filter((t) => String(t.title || '').startsWith('maestro-'))
  console.log(`[fixture] maestro-* 残留任务 ${hit.length} 条`)
  for (const t of hit) {
    const d = await api('DELETE', `/api/tasks/${t.id}`, undefined, token)
    console.log(`[fixture]   删除 ${t.id} -> ${d.status}`)
  }
}

async function induce() {
  const token = await login()
  await clearResidual(token)

  const prompt =
    '请在 /tmp/maestro-acc-approve 目录下创建文件 hello.txt，内容写一行 hello，' +
    '然后读取该文件并把内容原样告诉我。'
  const d = await api('POST', '/api/tasks/delegate', {
    title: TITLE,
    description: prompt,
    prompt,
  }, token)
  if (d.status !== 200) {
    throw new Error(`委托失败 ${d.status}：${JSON.stringify(d.body).slice(0, 300)}`)
  }
  const data = (d.body && d.body.data) || d.body || {}
  console.log(`[fixture] 已委托：task_id=${data.task_id} dispatch_id=${data.dispatch_id}`)

  // 等 daemon 把待批项挂上（App 的卡片靠它出现）。
  const deadline = Date.now() + 120_000
  while (Date.now() < deadline) {
    await new Promise((r) => setTimeout(r, 3000))
    const a = await api('GET', '/api/acc/approvals?status=pending', undefined, token)
    const items = (a.body && a.body.approvals) || []
    if (items.length > 0) {
      console.log(`[fixture] ✅ 待批项已出现：${items[0].id} title=${JSON.stringify(items[0].title)}`)
      return
    }
  }
  throw new Error('120s 内没有待批项出现 ⇒ 前提没造出来（这条流必须响亮失败）')
}

async function restore() {
  try {
    const token = await login()
    await clearResidual(token)
  } catch (e) {
    console.log(`[fixture] 还原跳过：${e.message}`)
  }
}

const mode = process.argv[2]
if (mode === '--induce') {
  await induce()
} else if (mode === '--restore') {
  await restore()
} else {
  await restore()
}
