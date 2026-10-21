// tasks-residue-fixture.mjs —— 清理 tasks-crud 造出来的任务残留。
//
// 为什么需要它（2026-10-22 实测，emulator-5556）：
//
//   tasks-crud 每次跑都会新建一个标题为「Maestro任务」的任务（flow 第 116 行
//   的 inputText），而**跑完不删**。于是残留会跨轮累积：
//     · 上一轮那个任务挂着没有活跃会话 ⇒ App 首页顶上常驻「需要你介入」横幅，
//       横幅带「收起」按钮，**压住底部导航**；
//     · 紧接着跑的 flow 点「更多」报 COMPLETED 但页面没动 ⇒
//       `visible: "更多功能|More features"` 40s 超时。
//
//   实测现场（email-browse 的 screen-hierarchy）：
//     '疑似卡死Maestro任务32 分钟无响应' / '⏹ 停止会话' / '需要你介入' ×2
//   ⇒ 报错指向「进不了更多页」，真因是**上一轮 flow 的残留**。
//
//   这类"上一条 flow 污染下一条"的故障最难查：失败现场是当前 flow 的页面，
//   根因却在上一个 flow 留下的数据里。所以这里把清理做成**夹具**：
//     · --induce  （flow 之前跑）：清掉上一轮遗留的同名任务；
//     · --restore （flow 之后跑）：清掉本轮新建的同名任务。
//   两头都清 ⇒ 本轮不会污染下一轮，下一轮也不会被上一轮污染。
//
// 用法由 scripts/maestro-run.mjs 按 scripts/.maestro-flows.json 的
// {"file": ".maestro/tasks-crud.yaml", "fixture": "tasks-residue"} 驱动。
//
// 凭据只从环境变量取（harness 注入 maestroEnv），不落盘、不打印。

const API = (process.env.POCKET_API_BASE || 'http://127.0.0.1:18099').replace(/\/+$/, '')
const USER = process.env.POCKET_AUTH_USER || 'admin'
const PASS = process.env.POCKET_DEV_PASS || process.env.POCKET_AUTH_PASS || ''
// tasks-crud 第 116 行 inputText 的原文。
const TITLE = process.env.POCKET_TASK_RESIDUE_TITLE || 'Maestro任务'

if (!PASS) {
  console.error('[tasks-residue] 缺少 POCKET_DEV_PASS / POCKET_AUTH_PASS，无法登录')
  process.exit(1)
}

async function login() {
  const res = await fetch(`${API}/api/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: USER, password: PASS }),
  })
  if (!res.ok) {
    console.error(`[tasks-residue] 登录失败：HTTP ${res.status}`)
    process.exit(1)
  }
  const body = await res.json()
  if (!body?.token) {
    console.error('[tasks-residue] 登录响应里没有 token')
    process.exit(1)
  }
  return body.token
}

async function api(token, path, init = {}) {
  const res = await fetch(`${API}${path}`, {
    ...init,
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json', ...(init.headers || {}) },
  })
  const text = await res.text()
  let json = null
  try { json = text ? JSON.parse(text) : null } catch { /* 非 JSON 也要如实报 */ }
  if (!res.ok) {
    const detail = text ? text.slice(0, 160) : '(空响应体)'
    throw new Error(`${init.method || 'GET'} ${path} → HTTP ${res.status} ${detail}`)
  }
  return json
}

const token = await login()
const listed = await api(token, '/api/tasks')
const tasks = Array.isArray(listed?.tasks) ? listed.tasks : []

// 只删**标题精确匹配**的测试产物。宁可漏删也不要误删：这是共享后端，
// 别的会话可能有真任务在这里。
const targets = tasks.filter((t) => t?.title === TITLE)

if (!targets.length) {
  console.log(`[tasks-residue] 无残留任务（标题 ${TITLE}）`)
  process.exit(0)
}

let deleted = 0
for (const t of targets) {
  try {
    await api(token, `/api/tasks/${encodeURIComponent(t.id)}`, { method: 'DELETE' })
    deleted += 1
    console.log(`[tasks-residue] 已删除残留任务 ${t.id}（${t.title}）`)
  } catch (e) {
    console.error(`[tasks-residue] 删除 ${t.id} 失败：${e.message}`)
    // 删不掉就如实失败：留着它会让后续 flow 红在一个与它们无关的原因上。
    process.exit(1)
  }
}
console.log(`[tasks-residue] 共清理 ${deleted} 条残留任务`)

// 兜底自证：列表里不该再有同名任务。
const after = await api(token, '/api/tasks')
const left = (Array.isArray(after?.tasks) ? after.tasks : []).filter((t) => t?.title === TITLE)
if (left.length) {
  console.error(`[tasks-residue] 自证失败：仍有 ${left.length} 条同名任务`)
  process.exit(1)
}
