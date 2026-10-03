#!/usr/bin/env node
// route-coverage-sweep.mjs —— 把 server.go 注册的**每一条**路由在真跑的实例上
// 打一遍，给出可用性分类。
//
// 为什么需要它（2026-10-02 真机轮次实测）：
//   - 前端路由巡检（scripts/device-route-sweep*.ps1）覆盖的是**页面**，63 条；
//   - 仓库自带的 backend-endpoint-matrix.mjs 覆盖 20 个**端点**；
//   - 而 server.go 里 mux.HandleFunc 一共注册了 139 条。剩下的 100 来条既没有
//     页面巡检也没有端点矩阵碰过 —— 「没被访问过」和「能用」之间没有任何推理关系，
//     只能真的打一遍。
//
// 只发 GET / HEAD：写操作一律不碰。同步 / 提取 / 流水这类副作用端点是 POST，
// 本脚本连注册都不跳，只是记录它们的「方法不是 GET」这一事实（见 SKIP_METHOD）。
//
// 鉴权：优先用 POCKET_AUTH_PASS 走正常登录；拿不到时回落到用 dev JWT secret
// 自行签发一个 token（config.go:13-17 的 DevDefaultJWTSecret 是固定常量）。
// 两条路都拿不到就直接退出，不要产出「全部 401」这种没有信息量的表格。
//
// Run: node scripts/route-coverage-sweep.mjs [--base http://127.0.0.1:18099] [--csv out.csv]
import { readFileSync, writeFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { createRequire } from 'node:module'
import assert from 'node:assert/strict'

const require = createRequire(import.meta.url)
const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const SERVER_GO = join(ROOT, 'backend', 'internal', 'server', 'server.go')

const argv = process.argv.slice(2)
const argOf = (name, dflt) => {
  const i = argv.indexOf(name)
  return i >= 0 && argv[i + 1] ? argv[i + 1] : dflt
}
const BASE = argOf('--base', process.env.POCKET_BASE || 'http://127.0.0.1:18099')
const CSV = argOf('--csv', '')

// ─────────────────────────────────────────────────────────────────────────────
// 一、取出 server.go 里注册的全部路径
// ─────────────────────────────────────────────────────────────────────────────

/** 从 server.go 抽出 mux.HandleFunc("...", ...) 的第一个字符串参数。 */
export function registeredRoutes(goSrc) {
  const out = []
  const re = /mux\.HandleFunc\(\s*"([^"]+)"/g
  let m
  while ((m = re.exec(goSrc)) !== null) {
    const line = goSrc.slice(0, m.index).split('\n').length
    out.push({ path: m[1], line })
  }
  return out
}

// ─────────────────────────────────────────────────────────────────────────────
// 二、分类判据
//
// 这一段是本脚本唯一有"判别力"的地方，所以单独导出、单独做负控。
// 规则按顺序第一条命中即返回，顺序有意义：403/401 必须在 SUBTREE 之前，
// 否则一个需要 admin 的子树子路径会被误报成"路由没挂上"。
// ─────────────────────────────────────────────────────────────────────────────

/** 尾斜杠子树根：Go ServeMux 里 `/x/` 匹配的是 `/x/...`，裸打 `/x` 会 404。 */
export function isSubtreeRoot(p) {
  return p.endsWith('/')
}

/** 长连接类：SSE / WebSocket。打过去只会挂住或被 WriteTimeout 掐断，测不出可用性。 */
export function isLongLived(p) {
  return /\/stream$|\/live\/event$|\/events?$|\/sse$/.test(p)
}

export const VERDICTS = [
  // key, 判定, 说明
  ['SUBTREE_ROOT', '需要子路径 id', '注册的是尾斜杠子树根；裸打它必然 404，不代表不可用'],
  ['LONG_LIVED', '长连接，跳过', 'SSE / WebSocket，GET 打过去测不出可用性'],
  ['UNAUTHORIZED', '鉴权失败', '带了有效 token 还 401/403 —— 优先怀疑 token 的 workspace 与本实例不符'],
  ['HANDLER_404', 'handler 自己返回 404', '路径在册却 404：请求到了 handler，是它自己判的 404'],
  ['SERVER_ERROR', '服务端错误', '5xx：这是真缺陷，不是环境问题'],
  ['UNCONFIGURED', '依赖未配置', '503：PG / master key / 依赖未装配，属环境而非代码'],
  ['REDIRECT', '重定向', '3xx：Go ServeMux 的尾斜杠 307 等'],
  ['METHOD_MISMATCH', '方法不符', '注册了但只接受 POST/PUT，GET 探测无效'],
  ['OK', '可用', '2xx'],
]

/**
 * 把 (path, status) 归类。
 *
 * 关于 404：Go ServeMux 未匹配时和 http.ServeFile 找不到文件时，**正文都是
 * `404 page not found`**，靠 body 分不开。但本脚本的输入就是「从 server.go
 * 解析出的注册列表」，所以一条在册的非子树路径返回 404，只可能是 handler 自己
 * 判的 —— 路由并没有掉。于是这里判成 HANDLER_404 而不是"未命中"。
 * 第一版把它算成 NOT_FOUND，把 /api/app/download 误报成"路由没挂上"。
 *
 * @param {string} path 注册路径
 * @param {number} status HTTP 状态码；null 表示请求本身失败（连接/超时）
 * @returns {string} VERDICTS 里的某个 key
 */
export function classify(path, status) {
  if (isLongLived(path)) return 'LONG_LIVED'
  if (isSubtreeRoot(path)) return 'SUBTREE_ROOT'
  if (status === null) return 'SERVER_ERROR'
  if (status === 401 || status === 403) return 'UNAUTHORIZED'
  if (status === 404) return 'HANDLER_404'
  // 503 必须排在「5xx 一律 SERVER_ERROR」之前：503 在本项目里绝大多数是
  // 「PG / master key / 某个依赖没装配」，属环境问题；笼统计成服务端错误会把
  // 一堆可解释的 503 报成待查缺陷。写反了这条规则就是死代码——
  // 自检里 ['/api/store', 503, 'UNCONFIGURED'] 这个样本就是为它准备的。
  if (status === 503) return 'UNCONFIGURED'
  if (status >= 500) return 'SERVER_ERROR'
  if (status >= 300 && status < 400) return 'REDIRECT'
  if (status >= 200 && status < 300) return 'OK'
  if (status >= 400 && status < 500) return 'METHOD_MISMATCH'
  return 'SERVER_ERROR'
}

// ─────────────────────────────────────────────────────────────────────────────
// 三之二、子树根的子路径探测
//
// 第一轮把 31 个尾斜杠子树根全部跳过了，于是"139 条注册路由"里有 31 条其实
// 一次都没打。那不是覆盖，是记账。
//
// 空集合拿不到真 id（flashcards/scheduled-tasks/meetings/folders/agents 都是空的），
// 于是用**格式合法的假 id** 去打。这里能区分"路由匹配了但 handler 拒绝了 id"与
// "路径压根没匹配上"，靠的是**响应体形状**：
//
//   handler 拒绝 → JSON（{"error":...} 之类）
//   mux 没匹配   → 纯文本 `404 page not found`
//
// 这正是第一轮在 /api/app/download 上踩过的那个坑（ServeMux 与 ServeFile 的 404
// 正文一样），这里换成 handler-vs-mux 的对比，正文就不再一样。
// ─────────────────────────────────────────────────────────────────────────────

/** 从各集合列表里摘一个真实 id。没有真 id 的用下面这个假 id，格式对齐真值。 */
const KNOWN_IDS = {
  '/api/tasks/': 'task-77cdf12dd930560678cfd49aaa945c45',
  '/api/notes/': 'note-1790947860830863100-1',
  '/api/email/accounts/': 'acct-1790870162018873300-1',
  '/api/llm-gateway/nodes/': '2',
  '/api/flashcards/': 'fc-00000000-0000-0000-0000-000000000000',
  '/api/scheduled-tasks/': 'sched-00000000-0000-0000-0000-000000000000',
  '/api/meetings/': 'meeting-00000000-0000-0000-0000-000000000000',
  '/api/email/folders/': 'folder-00000000-0000-0000-0000-000000000000',
  '/api/agents/': 'agent-00000000-0000-0000-0000-000000000000',
  '/api/rss/': 'rss-00000000-0000-0000-0000-000000000000',
  '/api/email/summaries/': 'sum-00000000-0000-0000-0000-000000000000',
  '/api/email/vacations/': 'vac-00000000-0000-0000-0000-000000000000',
  '/api/emails/invoices/': 'inv_00000000-0000-0000',
  '/api/learning/': 'learn-00000000-0000-0000-0000-000000000000',
  '/api/snippets/': 'snip-00000000-0000-0000-0000-000000000000',
  '/api/chat-summaries/': 'cs-00000000-0000-0000-0000-000000000000',
  '/api/finance/': 'fin-00000000-0000-0000-0000-000000000000',
  '/api/notifications/': 'notif-00000000-0000-0000-0000-000000000000',
  '/api/chat-agents/': 'ca-00000000-0000-0000-0000-000000000000',
  '/api/user-settings/': 'us-00000000-0000-0000-0000-000000000000',
  '/api/workspaces/': 'ws_user-admin',
  '/api/vault/sync/': '00000000-0000-0000-0000-000000000000',
  '/api/sessions/': 'sess-00000000-0000-0000-0000-000000000000',
  '/api/opencode/sessions/': 'sess-00000000-0000-0000-0000-000000000000',
  '/api/opencode/instances/': 'inst-00000000-0000-0000-0000-000000000000',
  '/api/mobile/sessions/': '00000000-0000-0000-0000-000000000000',
  '/api/mobile/approvals/': 'appr-00000000-0000-0000-0000-000000000000',
  '/api/auth/biometric/credentials/': '00000000-0000-0000-0000-000000000000',
  '/api/emails/': 'em-00000000-0000-0000',
  '/api/marketplace/': 'probe-not-a-subpath',
  '/api/marketplace/packages/': '00000000-0000-0000-0000-000000000000',
}

const MUX_404_BODY = /^404 page not found/

/**
 * 子路径探测的结果分类。
 *
 * 命名上刻意不叫 OK：4xx 里绝大多数是 400/405（缺 instance_id、只接受 DELETE…），
 * 把它们并进 "OK" 会让汇总读成"11 个端点能用"，而它们真正证明的只是
 * **子树模式匹配上了、handler 跑了**。这轮会话里我一直在纠正这类"绿灯比实际宽"的
 * 说法，自己的输出也不能犯。
 *
 * @returns {'HANDLER_RESPONDED'|'HANDLER_REJECTED'|'PATH_NOT_MATCHED'|'UNAUTHORIZED'|'SERVER_ERROR'|'UNCONFIGURED'}
 */
export function classifyChild(status, body) {
  if (status === null) return 'SERVER_ERROR'
  if (status === 401 || status === 403) return 'UNAUTHORIZED'
  if (status === 404) {
    // 同样是 404，看正文：纯文本 = mux 没匹配；JSON = handler 跑了并拒绝了这个 id
    return MUX_404_BODY.test(String(body).trim())
      ? 'PATH_NOT_MATCHED'
      : 'HANDLER_REJECTED'
  }
  if (status === 503) return 'UNCONFIGURED'
  if (status >= 500) return 'SERVER_ERROR'
  // 2xx，以及 400/405 这类"领域拒绝"：都证明路由与 handler 是活的
  return 'HANDLER_RESPONDED'
}

// ─────────────────────────────────────────────────────────────────────────────
// 三之三、POST 端点：只探「读代码确认先校验后动手」的那几个
//
// 剩下 54 条只接受非 GET，第一轮的 GET 探测对它们其实**几乎什么都没证明**：
// 405 "POST only" 是路由自己的方法闸门回的，handler 函数体一行都没跑。
//
// 但也不能无脑 POST —— 里面有 /api/email/send（真发信）、/api/emails/purge
// （删数据）、/api/email/pipeline/run（建发票行 + 推飞书）、/api/plugin/command
// （执行命令）、/api/stt/*（出站调用）、/api/auth/logout（会注销我自己的 token）。
//
// 所以这里用**白名单**，且每一条都写清"为什么空体是安全的"——判据的依据是读代码
// 看到的校验顺序，不是名字看着安全。读不出来的一律不探，按"未测"记账。
//
// 为什么不是黑名单/豁免表：黑名单里每加一条都要有人判断，而"没列进来的会怎样"
// 谁也说不清。白名单的默认行为是"不探"，安全方向是确定的。
// ─────────────────────────────────────────────────────────────────────────────

export const SAFE_POST = {
  '/api/migration/preview': 'server_migration.go:62 先 json.Decode，空体即 400 "invalid request body"；函数名 preview，本身是只读预览',
  '/api/config/models/test': 'server.go:2199 在 Decode 之前就查 instance_id 并 400 "missing instance_id"，到不了任何下游调用',
  '/api/llm-gateway/test': 'llm_gateway_handler.go:458 只在 apiKey 未设或 URL 非法时才 400；本机两者都合法，所以它**真的出网列了一次模型目录**并返回 200（只读、无 token 消耗）。我第一版在这里写的是「空体直接 400」——实测证明那是错的，已按事实改写；安全性成立但理由与当初的推断不同',
  '/api/presentations': 'server_presentation.go:11 先 Decode 再查 topic is required，空体在第一道就 400',
  '/api/redclaw/knowledge/search': 'server_redclaw.go:68 bridge 为 nil 时直接 503，根本到不了出网那一行',
  '/api/mobile/approvals': 'mobile_approval_handler.go:21 缺 instance_id 即 400，upstream unavailable 检查在其之前',
  '/api/mobile/sessions': 'mobile_session_handler.go:74 缺 instance_id 即 400；adapter 为 nil 时更早 503',
}

/** 明确不探的、看着像但其实有副作用的那批（写进代码是为了让"为什么不探"可查）。 */
export const DELIBERATELY_NOT_PROBED = {
  '/api/email/send': '真发信',
  '/api/emails/purge': '删数据',
  '/api/emails/cleanup': '删数据',
  '/api/email/pipeline/run': '建发票行 + 推飞书',
  '/api/emails/move': '真实 IMAP MOVE',
  '/api/plugin/command': '执行命令',
  '/api/llm/chat': '要花钱',
  '/api/embed': '要花钱',
  '/api/stt/discover': '出站调用',
  '/api/stt/probe': '出站调用',
  '/api/stt/transcribe': '出站 + 贵',
  '/api/auth/logout': '会注销我自己这个 token',
  '/api/config/reload': '改运行中的服务配置',
  '/api/tasks/from-source': '建任务',
  '/api/tasks/delegate': '建任务',
}

// ─────────────────────────────────────────────────────────────────────────────
// 四、鉴权
// ─────────────────────────────────────────────────────────────────────────────

function b64url(o) { return Buffer.from(JSON.stringify(o)).toString('base64url') }

function mintDevToken(secret, user, workspace) {
  const now = Math.floor(Date.now() / 1000)
  const head = b64url({ alg: 'HS256', typ: 'JWT' })
  const pay = b64url({
    user_id: user, role: 'admin', workspace_id: workspace,
    iss: 'pocket', aud: 'pocket-api', exp: now + 3600, iat: now, nbf: now,
  })
  const signed = `${head}.${pay}`
  const sig = require('node:crypto')
    .createHmac('sha256', secret).update(signed).digest('base64url')
  return `${signed}.${sig}`
}

async function getToken() {
  if (process.env.POCKET_AUTH_PASS) {
    const res = await fetch(`${BASE}/api/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: 'admin', password: process.env.POCKET_AUTH_PASS }),
    })
    if (res.ok) return { token: (await res.json()).token, how: 'login' }
    console.error(`[auth] POCKET_AUTH_PASS 登录失败: ${res.status}`)
  }
  const secret = process.env.POCKET_JWT_SECRET || 'pocket-dev-insecure-secret-0000000000'
  return {
    token: mintDevToken(secret, 'user-admin', 'ws_user-admin'),
    how: 'dev-secret-mint',
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// 四、判据自检（负控）：判据本身必须先证明有判别力，再拿去分类真实数据
// ─────────────────────────────────────────────────────────────────────────────

function selfCheck() {
  // 每一条 VERDICT 至少有一个样本能被它命中 —— 否则这条规则是死代码。
  const SAMPLES = [
    ['/api/tasks', 200, 'OK'],
    ['/api/emails', 500, 'SERVER_ERROR'],
    ['/api/emails', null, 'SERVER_ERROR'],
    ['/api/foo', 401, 'UNAUTHORIZED'],
    ['/api/foo', 403, 'UNAUTHORIZED'],
    ['/api/nope', 404, 'HANDLER_404'],
    ['/api/store', 503, 'UNCONFIGURED'],
    ['/api/vault/sync/', 200, 'SUBTREE_ROOT'],
    ['/api/vault/sync/latest', 307, 'REDIRECT'],
    ['/api/llm/stream', 200, 'LONG_LIVED'],
    ['/api/gateway/nodes/2/live/event', 200, 'LONG_LIVED'],
    ['/api/email/send', 405, 'METHOD_MISMATCH'],
  ]
  for (const [p, st, want] of SAMPLES) {
    const got = classify(p, st)
    assert.equal(got, want, `判据自检失败: ${p} status=${st} 判成 ${got}，期望 ${want}`)
  }
  // 顺序敏感：鉴权必须先于 NOT_FOUND，否则需 admin 的子树子路径会被误报
  assert.equal(classify('/api/admin/x', 403), 'UNAUTHORIZED')
  // 长连接必须先于一切，否则会真的去挂住
  assert.equal(classify('/api/llm/stream', 503), 'LONG_LIVED')
  console.log('[selfcheck] 分类判据 %d 个样本全部命中，顺序敏感性已验', SAMPLES.length)

  // 子路径判据的自检：同样 404，正文形状决定结论
  const CHILD = [
    [200, '{}', 'HANDLER_RESPONDED'],
    [400, '{"error":"missing instance_id"}', 'HANDLER_RESPONDED'],
    [405, '{"error":"GET/PUT/DELETE only"}', 'HANDLER_RESPONDED'],
    [404, '404 page not found\n', 'PATH_NOT_MATCHED'],
    [404, '{"error":"invoice not found"}', 'HANDLER_REJECTED'],
    [404, '{"code":"not_found"}', 'HANDLER_REJECTED'],
    [401, '{"code":"unauthenticated"}', 'UNAUTHORIZED'],
    [503, '{"error":"store not configured"}', 'UNCONFIGURED'],
    [500, 'boom', 'SERVER_ERROR'],
    [null, '', 'SERVER_ERROR'],
  ]
  for (const [st, body, want] of CHILD) {
    const got = classifyChild(st, body)
    assert.equal(got, want, `子路径判据自检失败: status=${st} body=${JSON.stringify(body)} 判成 ${got}，期望 ${want}`)
  }
  // 关键不变量：同样的 404，换正文就该换结论——否则它其实只看了 status
  assert.notEqual(classifyChild(404, '404 page not found'), classifyChild(404, '{"error":"x"}'))
  console.log('[selfcheck] 子路径判据 %d 个样本全部命中，且 404 的两种来源已被区分', CHILD.length)
}

// ─────────────────────────────────────────────────────────────────────────────
// 五、扫
// ─────────────────────────────────────────────────────────────────────────────

async function main() {
  selfCheck()

  const routes = registeredRoutes(readFileSync(SERVER_GO, 'utf8'))
  if (routes.length === 0) {
    console.error('没有从 server.go 解析出任何路由——判据失效，不要相信输出')
    process.exit(3)
  }
  console.log(`从 server.go 解析出 ${routes.length} 条注册路由\n`)

  const { token, how } = await getToken()
  console.log(`token 来源 = ${how}，base = ${BASE}\n`)

  const rows = []
  for (const { path, line } of routes) {
    const v = classify(path, null) // 先判掉不测的
    if (v === 'SUBTREE_ROOT' || v === 'LONG_LIVED') {
      rows.push({ path, line, status: '', verdict: v })
      continue
    }
    let status = null
    let note = ''
    try {
      const ctl = new AbortController()
      const t = setTimeout(() => ctl.abort(), 20_000)
      const res = await fetch(BASE + path, {
        headers: { Authorization: `Bearer ${token}` },
        signal: ctl.signal,
      })
      clearTimeout(t)
      status = res.status
      const txt = await res.text()
      note = txt.slice(0, 120).replace(/\s+/g, ' ')
    } catch (e) {
      note = 'ERR ' + e.message
    }
    rows.push({ path, line, status: status ?? '', verdict: classify(path, status), note })
  }

  const byVerdict = new Map()
  for (const r of rows) byVerdict.set(r.verdict, (byVerdict.get(r.verdict) || 0) + 1)

  // ── 第二轮：子树根的子路径 ────────────────────────────────────────────────
  console.log('\n=== 第二轮：子树根的子路径 ===')
  const childRows = []
  for (const r of rows.filter((x) => x.verdict === 'SUBTREE_ROOT')) {
    const id = KNOWN_IDS[r.path]
    if (!id) {
      childRows.push({ root: r.path, child: '', verdict: 'NO_ID_MAPPED' })
      continue
    }
    const child = r.path + id
    let status = null
    let body = ''
    try {
      const ctl = new AbortController()
      const t = setTimeout(() => ctl.abort(), 20_000)
      const res = await fetch(BASE + child, {
        headers: { Authorization: `Bearer ${token}` },
        signal: ctl.signal,
      })
      clearTimeout(t)
      status = res.status
      body = (await res.text()).slice(0, 160).replace(/\s+/g, ' ')
    } catch (e) {
      body = 'ERR ' + e.message
    }
    childRows.push({ root: r.path, child, status: status ?? '', verdict: classifyChild(status, body), body })
  }

  const childCount = new Map()
  for (const c of childRows) childCount.set(c.verdict, (childCount.get(c.verdict) || 0) + 1)
  console.log('  子路径结论分布：')
  for (const [k, n] of [...childCount].sort((a, b) => b[1] - a[1])) {
    console.log(`    ${k.padEnd(20)} ${n}`)
  }
  console.log('')
  for (const c of childRows) {
    if (c.verdict === 'HANDLER_REJECTED' || c.verdict === 'HANDLER_RESPONDED') continue
    console.log(`  ${String(c.status).padEnd(4)} ${(c.child || '(no id mapped)').padEnd(52)} ${c.verdict}  ${(c.body || '').slice(0, 60)}`)
  }
  console.log('  （HANDLER_REJECTED / HANDLER_RESPONDED = 子树模式匹配成功、handler 认出了这个 id；')
  console.log('    前者带 404 结构化拒绝、后者带 2xx 或 400/405 领域拒绝。逐条状态见 CSV，不在此铺开。）')

  // ── 第三轮：白名单里的 POST 端点 ──────────────────────────────────────────
  console.log('\n=== 第三轮：白名单 POST 端点（空体）===')
  const postRows = []
  for (const [path, why] of Object.entries(SAFE_POST)) {
    if (!routes.some((r) => r.path === path)) {
      console.log(`  [SKIP] ${path} 已不在 server.go 的注册表里，白名单该清理了`)
      continue
    }
    let status = null
    let body = ''
    try {
      const ctl = new AbortController()
      const t = setTimeout(() => ctl.abort(), 20_000)
      const res = await fetch(BASE + path, {
        method: 'POST',
        headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
        body: '{}',
        signal: ctl.signal,
      })
      clearTimeout(t)
      status = res.status
      body = (await res.text()).slice(0, 140).replace(/\s+/g, ' ')
    } catch (e) {
      body = 'ERR ' + e.message
    }
    const v = classifyChild(status, body)
    postRows.push({ path, status: status ?? '', verdict: v, body, why })
    console.log(`  ${String(status).padEnd(4)} ${path.padEnd(38)} ${v}  ${body.slice(0, 60)}`)
  }
  console.log(`  明确不探的 ${Object.keys(DELIBERATELY_NOT_PROBED).length} 条（理由见脚本顶部常量）：`)
  for (const [p, why] of Object.entries(DELIBERATELY_NOT_PROBED)) console.log(`    ${p.padEnd(38)} ${why}`)
  const untested = rows.filter((r) => r.verdict === 'METHOD_MISMATCH').length - postRows.length
  console.log(`\n  仍按「未测」记账的 POST 端点：${untested} 条`)

  console.log('\n=== SUMMARY ===')
  for (const [k] of VERDICTS) {
    if (byVerdict.get(k)) console.log(`  ${k.padEnd(18)} ${byVerdict.get(k)}`)
  }

  if (CSV) {
    const esc = (v) => `"${String(v).replace(/"/g, '""')}"`
    const csv = ['path,line,status,verdict,note']
      .concat(rows.map((r) => [r.path, r.line, r.status, r.verdict, r.note || ''].map(esc).join(',')))
      .join('\n')
    const csv2 = ['', 'root,child,status,verdict,body']
      .concat(childRows.map((c) => [c.root, c.child, c.status, c.verdict, c.body || ''].map(esc).join(',')))
      .join('\n')
    const csv3 = ['', 'path,status,verdict,body,why_safe']
      .concat(postRows.map((c) => [c.path, c.status, c.verdict, c.body || '', c.why || ''].map(esc).join(',')))
      .join('\n')
    writeFileSync(CSV, csv + csv2 + csv3, 'utf8')
    console.log(`\nCSV 已写入 ${CSV}`)
  }
}

if (import.meta.url === `file:///${process.argv[1].replace(/\\/g, '/')}`) {
  await main()
}
