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
 */
export async function clearResidual(api, prefix = 'maestro-') {
  const r = await api('GET', '/api/tasks', undefined)
  const hit = rows(r.body).filter((t) => String(t.title || '').startsWith(prefix))
  console.log(`[fixture] ${prefix}* 残留任务 ${hit.length} 条`)
  for (const t of hit) {
    const d = await api('DELETE', `/api/tasks/${t.id}`, undefined)
    console.log(`[fixture]   删除 ${t.id} -> ${d.status}`)
  }
  return hit.length
}
