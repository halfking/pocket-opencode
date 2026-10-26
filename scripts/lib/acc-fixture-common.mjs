/**
 * ACC 夹具共用件（2026-10-26 新增）。
 *
 * 为什么抽出来：
 *   acc-approval 与 acc-delegate-stop 两条流都要做同一件事 ——
 *   **清掉 maestro-* 同族的残留任务**。不清理的后果不是「红」，
 *   而是「绿得没有意义」：
 *     · 长按「maestro-acc-stop」时，屏上可能有**两条**同名卡片
 *       （上一轮跑出来的 + 这一轮新建的）⇒ Maestro 选中的是**哪一条**
 *       取决于渲染顺序 ⇒ 断言可能对错了行；
 *     · 末尾断言「卡片消失」也可能被**另一条**残留同名卡片的重新出现顶住。
 *   ⇒ 残留必须由夹具在**跑之前**清掉。
 *
 * 凭据处理：口令只从 0600 的 env 文件读，不打印、不进仓库。
 */
import { readFileSync, existsSync } from 'node:fs'
import { homedir } from 'node:os'
import { createHmac } from 'node:crypto'

export const DEFAULT_BASE = process.env.POCKET_API_BASE || 'http://127.0.0.1:18099'
export const ENV_FILE = `${homedir()}/kaixuan/acp/logs/rerun-round52/pocketd-18099.env`

export function loadEnv(path = ENV_FILE) {
  const out = {}
  if (!existsSync(path)) return out
  for (const raw of readFileSync(path, 'utf8').split('\n')) {
    let line = raw.trim()
    if (line.startsWith('export ')) line = line.slice(7)
    if (!line || line.startsWith('#') || !line.includes('=')) continue
    const i = line.indexOf('=')
    out[line.slice(0, i).trim()] = line.slice(i + 1).trim().replace(/^["']|["']$/g, '')
  }
  return out
}

export function makeApi(base = DEFAULT_BASE) {
  let token = null
  async function api(method, path, body) {
    const headers = { 'Content-Type': 'application/json' }
    if (token) headers.Authorization = 'Bearer ' + token
    const res = await fetch(base + path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    })
    const text = await res.text()
    let parsed = null
    try { parsed = JSON.parse(text) } catch { parsed = text }
    return { status: res.status, body: parsed }
  }
  async function login(env = loadEnv()) {
    const r = await api('POST', '/api/auth/login', {
      username: env.POCKET_AUTH_USER || 'admin',
      password: env.POCKET_AUTH_PASS || '',
    })
    if (r.status !== 200 || !r.body || !r.body.token) {
      throw new Error(`登录失败 ${r.status}：${JSON.stringify(r.body).slice(0, 200)}`)
    }
    token = r.body.token
    return token
  }
  return { api, login, get token() { return token } }
}

function rows(body) {
  if (!body) return []
  if (Array.isArray(body)) return body
  if (Array.isArray(body.tasks)) return body.tasks
  if (Array.isArray(body.data)) return body.data
  return []
}

/**
 * 清掉 maestro-* 同族的残留任务。
 *
 * 清**整个同族**而不是只清某个标题：末尾断言是「这条卡片进/出某个区」，
 * 别的 maestro-* 卡片若也在那个区，断言就对错了行。
 *
 * ★ 2026-10-27 已知局限（诚实记录，不要当成「已经清干净」）：
 *   ACC 来源的任务（source=acc，id 是 ACC 的 uuid）在本地库里**删不掉** ——
 *   DELETE /api/tasks/{acc-uuid} 返回 404（本地行 id 是 task-xxx 形式，
 *   两个 id 不是一回事）。⇒ 这里的 404 是**真实结果**，不是清理成功。
 *   ⇒ 残留的 ACC 任务会以 status=queue 留在列表里；
 *     它们不会自己跑完（没有派单），所以不会落进「已结束」区，
 *     末尾断言（finished-task-title-<status> + 标题）仍然只可能命中本轮那条。
 *   ⇒ 但**同名卡片可能不止一张**这件事本身要知道：长按选卡时注意选哪一条。
 */
export async function clearResidual(api, prefix = 'maestro-') {
  const r = await api('GET', '/api/tasks', undefined)
  const hit = rows(r.body).filter((t) => String(t.title || '').startsWith(prefix))
  console.log(`[fixture] ${prefix}* 残留任务 ${hit.length} 条`)
  let deleted = 0
  let notDeletable = 0
  for (const t of hit) {
    const d = await api('DELETE', `/api/tasks/${t.id}`, undefined)
    if (d.status === 200) deleted += 1
    else notDeletable += 1
    console.log(`[fixture]   删除 ${t.id} -> ${d.status}${d.status === 200 ? '' : '（ACC 来源的任务本地删不掉，见函数注释）'}`)
  }
  console.log(`[fixture] 残留清理：删掉 ${deleted} 条，删不掉 ${notDeletable} 条（source=acc）`)
  return hit.length
}

/**
 * 直连 ACC 的 MCP 面读任务列表（只读）。
 *
 * ★ 为什么夹具要自己连 ACC，而不是只看本地 /api/tasks：
 *   本地列表由 tasksync 每 15s 从 ACC 拉一次 ⇒ **有 15s 的滞后**。
 *   写请求失败后要判断「服务器到底建没建出任务」，必须问 ACC 本尊；
 *   问本地会得出「没有」的错误结论 ⇒ 于是重试 ⇒ 建出重复任务。
 *
 * 凭据只从 0600 的 env 文件读，不打印、不进仓库。
 */
export async function accTaskList() {
  const env = loadEnv()
  const base = env.POCKET_MCP_BASE_URL
  const secret = env.POCKET_MCP_API_KEY
  if (!base || !secret) {
    console.log('[fixture] 没配 POCKET_MCP_BASE_URL / POCKET_MCP_API_KEY ⇒ 无法直连 ACC')
    return []
  }
  const b64u = (o) => Buffer.from(typeof o === 'string' ? o : JSON.stringify(o)).toString('base64url')
  const now = Math.floor(Date.now() / 1000)
  const head = b64u({ alg: 'HS256', typ: 'JWT' })
  const pay = b64u({
    user_id: 'pocketd',
    tenant_id: env.POCKET_MCP_TENANT_ID || 'tenant-a',
    custom: { scopes: ['tasks', 'sessions'] },
    iss: 'pocketd', iat: now, nbf: now, exp: now + 900,
  })
  const sig = createHmac('sha256', secret).update(`${head}.${pay}`).digest('base64url')
  const token = `${head}.${pay}.${sig}`

  const ctrl = new AbortController()
  const timer = setTimeout(() => ctrl.abort(), 15000)
  try {
    const res = await fetch(base, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Accept: 'application/json, text/event-stream',
        Authorization: `Bearer ${token}`,
      },
      body: JSON.stringify({
        jsonrpc: '2.0',
        method: 'tools/call',
        params: { name: 'acc_get_tasks', arguments: {} },
        id: Date.now() % 100000,
      }),
      signal: ctrl.signal,
    })
    const text = await res.text()
    const m = text.match(/data: (.*)/)
    const body = JSON.parse(m ? m[1] : text)
    const list = JSON.parse(body.result.content[0].text)
    return Array.isArray(list) ? list : []
  } catch (e) {
    console.log(`[fixture] 直连 ACC 读任务失败：${e.message}`)
    return []
  } finally {
    clearTimeout(timer)
  }
}

/**
 * 委托一单给 ACC，**写请求失败后先问 ACC「到底建没建出来」，再决定要不要重试**。
 *
 * ★ 为什么不能盲重试：
 *   acc_create_task **没有幂等键** —— ACC 侧 acc-go 的 internal/mcp/server.go
 *   里 acc_create_task 只吃 payload / parent_task_id，没有 idempotency /
 *   source_ref ⇒ 一次失败之后，服务器**可能已经建好任务**了
 *   ⇒ 盲重试会建出重复任务。
 *   而 ACC 的 MCP 面确实会抖（2026-10-27 01:04 实测：EOF / 超时 / 400 都出现过）。
 *
 * ★ 判定顺序（关键）：
 *   1. 委托返回 200 ⇒ 成功。
 *   2. 委托失败 ⇒ **立刻**问 ACC 的任务列表有没有同标题的任务：
 *      有 ⇒ 服务器其实建出来了 ⇒ 视作成功（避免重复建单）；
 *      没有 ⇒ 才重试。
 *   ⚠ 不能用本地 /api/tasks 来判：本地列表有 15s 的同步滞后 ⇒ 会误判成「没有」。
 */
export async function delegateWithVerify(api, { title, description, prompt }, opts = {}) {
  const attempts = opts.attempts ?? 4
  const waitMs = opts.waitMs ?? 8000
  let last = { status: 0, body: null }
  for (let i = 1; i <= attempts; i++) {
    // ★ 记下请求发出的时刻：事后认领「ACC 侧其实建出来了」必须用它做时间下界。
    const startedAt = new Date()
    const d = await api('POST', '/api/tasks/delegate', { title, description, prompt })
    last = { status: d.status, body: d.body }
    if (d.status === 200) {
      const data = (d.body && d.body.data) || d.body || {}
      return { ok: true, attempt: i, task_id: data.task_id, dispatch_id: data.dispatch_id, recovered: false }
    }
    // 写请求可能已经生效 ⇒ 先问 ACC 本尊
    const list = await accTaskList()
    const hit = pickCreatedSince(list, title, startedAt)
    if (hit) {
      console.log(`[fixture] 委托返回 ${d.status}，但 ACC 里已经有同标题的新任务（${hit.id} created=${hit.created_at}）⇒ 视作已建，不重发`)
      return { ok: true, attempt: i, task_id: hit.id, dispatch_id: null, recovered: true }
    }
    console.log(`[fixture] 委托第 ${i}/${attempts} 次失败（${d.status}），ACC 里也没有同标题的新任务 ⇒ 等 ${waitMs}ms 重试`)
    if (i < attempts) await new Promise((r) => setTimeout(r, waitMs))
  }
  return { ok: false, status: last.status, body: last.body }
}

/**
 * 从 ACC 的任务列表里挑出「标题相同、且创建时间不早于 startedAt」的那条。
 *
 * ★ 为什么要时间下界（不能只按标题）：
 *   ACC 里同名残留能堆几十条（实测 27 条 maestro-acc-approve）
 *   ⇒ 只按标题找会命中**上一轮的旧任务**，
 *     于是夹具会报告「已建」而其实本次请求一条都没建出来
 *     ⇒ 前提是假的，流会拿着一个不存在的任务去跑。
 *   ⇒ 必须加「创建时间不早于本次请求」。
 *
 * ⚠ 多个候选时取**最新**的（本次请求是最后发出的那个）。
 */
export function pickCreatedSince(list, title, startedAt) {
  const t0 = startedAt instanceof Date ? startedAt.getTime() : Date.parse(startedAt)
  let best = null
  for (const t of list || []) {
    if (String(t.title || '') !== title) continue
    const ts = Date.parse(t.created_at || '')
    if (!Number.isFinite(ts)) continue
    if (ts < t0 - 5000) continue // 5s 时钟偏差容忍
    if (!best || Date.parse(t.created_at) > Date.parse(best.created_at)) best = t
  }
  return best
}
