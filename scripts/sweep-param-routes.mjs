// sweep-param-routes.mjs — 扫带 :param 的详情/编辑页（30 条模板）。
//
// 补 sweep-routes.mjs 的盲区：它只扫静态路由，30 条带 :param 的路由一条没验过。
// 而详情/编辑页恰恰最容易「功能没做、界面照常展示」——列表页看着正常，
// 点进去才发现是空壳或直接报技术错误。
//
// id 来源（scripts/harvest-route-ids.mjs 于 2026-10-01 10:5x 在**页面上下文**里
// 用 App 自己的 token 实采，不是猜的）：��
//   /api/flashcards          -> deck_87e69818f9c025a22736e565ac2489cd
//   /api/flashcards/notes    -> note_82d291fc08d3a86f90a1be0437a7b378
//   /api/notes               -> note-1790767908638675300-1
//   /api/tasks               -> task-5f0efe1c689a6ff1e6366b3fe0428672
//   /api/emails              -> em-1298896143-acct-1790784255240360000-5
//   /api/meetings            -> mtg_1790818948067029300_1
//   /api/email/accounts      -> acct-1790782486625898900-1
//   网关节点                 -> PROBE-NODE-948776-RENAMED（#/gateway 页面上实测可见）
//
// ⚠️ 采不到 id 的模板**不假装测过**，单列出来并写明原因
//   （端点 404 / 列表为空 / 仅本地无后端）。这是本脚本与「无脑填假 id 跑一遍」
//   的根本区别：填假 id 会全部落在「未找到」分支，什么都测不到。
import { readFileSync, writeFileSync } from 'node:fs'
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9468'
const adb = (a, t = 25000) => execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const adbSoft = (a) => { try { return adb(a, 8000) } catch { return '' } }
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// 采到的真实 id
const ID = {
  deck: 'deck_87e69818f9c025a22736e565ac2489cd',
  fcNote: 'note_82d291fc08d3a86f90a1be0437a7b378',
  note: 'note-1790767908638675300-1',
  task: 'task-5f0efe1c689a6ff1e6366b3fe0428672',
  email: 'em-1298896143-acct-1790784255240360000-5',
  meeting: 'mtg_1790818948067029300_1',
  acct: 'acct-1790782486625898900-1',
  gwNode: 'PROBE-NODE-948776-RENAMED',
}

// 30 条 :param 模板 → 具体 URL；采不到 id 的写进 NO_ID 并注明原因
const PLAN = [
  ['/notes/:id', `/notes/${ID.note}`],
  ['/notes/:id/edit', `/notes/${ID.note}/edit`],
  ['/tasks/:id', `/tasks/${ID.task}`],
  ['/email/:id', `/email/${ID.email}`],
  ['/meetings/:id', `/meetings/${ID.meeting}`],
  ['/meetings/:id/record', `/meetings/${ID.meeting}/record`],
  ['/flashcards/decks/:deckId', `/flashcards/decks/${ID.deck}`],
  ['/flashcards/decks/:deckId/review', `/flashcards/decks/${ID.deck}/review`],
  ['/flashcards/decks/:deckId/options', `/flashcards/decks/${ID.deck}/options`],
  ['/flashcards/notes/:noteId/edit', `/flashcards/notes/${ID.fcNote}/edit`],
  ['/gateway/:nodeId', `/gateway/${ID.gwNode}`],
  ['/gateway/:nodeId/providers', `/gateway/${ID.gwNode}/providers`],
  ['/gateway/:nodeId/credentials', `/gateway/${ID.gwNode}/credentials`],
  ['/gateway/:nodeId/models', `/gateway/${ID.gwNode}/models`],
  ['/gateway/:nodeId/catalog', `/gateway/${ID.gwNode}/catalog`],
  ['/gateway/:nodeId/routing-config', `/gateway/${ID.gwNode}/routing-config`],
  ['/gateway/:nodeId/live', `/gateway/${ID.gwNode}/live`],
  // 第二轮补：/api/agents 是 {"agents":null}（无用户自建 agent），但
  // **/api/chat-agents 里有真实 agent**，id 形如 academic-anthropologist。
  // 日期型参数不需要先有数据也能开页面。
  ['/agents/:agentId', '/agents/academic-anthropologist'],
  ['/agents/:agentId/edit', '/agents/academic-anthropologist/edit'],
  ['/email/summary/:date', '/email/summary/2026-10-01'],
  // 无 id 可采的：写明原因，不假装测过
  ['/contacts/:id', null, '/api/contacts 是 404「404 page not found」——后端无该端点，造数据也没用'],
  ['/sessions/:id', null, '/api/sessions 返回 {sessions:[],total:0}，需先造数据'],
  ['/opencode/sessions/:id', null, '无列表端点可采 id'],
  ['/settings/scheduled-tasks/:id', null, '/api/scheduled-tasks 返回 {tasks:[]}，需先造数据'],
  ['/settings/scheduled-tasks/:id/edit', null, '同上'],
  ['/rss/items/:id', null, '/api/rss/items 返回 {count:0,items:[]}，需先造数据'],
  ['/vault/:id', null, '密码箱是纯本地原生功能，BUG-AT 已定性为 Android 不可用'],
  ['/vault/:id/edit', null, '同上'],
  ['/pkm/n/:id', null, 'PKM 落设备本地 local_assets，无后端 id 可采'],
  ['/gateway/:nodeId/credentials/:credentialId', null, '需要先建凭据才有 credentialId'],
]

const RED_FLAGS = [
  [/plugin is not implemented/i, '原生插件未实现'],
  [/not implemented on/i, '原生插件未实现'],
  [/Cannot read propert/i, 'JS 运行时错误'],
  [/is not a function/i, 'JS 运行时错误'],
  [/undefined\s*[:：]/, '渲染出 undefined'],
  [/\[object Object\]/, '渲染出 [object Object]'],
  [/Failed to fetch|NetworkError|ERR_CONNECTION/i, '网络请求失败'],
  [/TypeError|ReferenceError|SyntaxError/, 'JS 异常类型外泄'],
  [/渲染出 NaN|NaN\s*张|NaN\s*条/, '渲染出 NaN'],
]
const NOT_FOUND = [/未找到|不存在|Not Found|not found|404/]
const CHROME_WORDS = ['跳到主要内容', 'menu', 'notifications', '首页', '学习', '会议', '更多', '主导航', 'Redclaw']
const KNOWN_GOOD = [/当前平台未提供密码箱原生插件/, /功能不可用/]

const pid = adbSoft(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adbSoft(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adbSoft(['forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const pages = await (await fetch(`http://127.0.0.1:${PORT}/json/list`, { signal: AbortSignal.timeout(10000) })).json()
const page = pages.find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE_TARGET'); process.exit(4) }
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0; const pending = new Map()
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
const opened = await Promise.race([
  new Promise((r) => ws.addEventListener('open', () => r(true))),
  new Promise((r) => setTimeout(() => r(false), 10000)),
])
if (!opened) { console.log('CDP_OPEN_TIMEOUT'); process.exit(5) }
const ev = async (x, ms = 20000) => {
  const i = ++id
  const v = await new Promise((r) => {
    const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms)
    pending.set(i, (y) => { clearTimeout(t); r(y) })
    ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } }))
  })
  if (v?.__t) return { value: null, err: 'TIMEOUT' }
  if (v?.exceptionDetails) return { value: null, err: String(v.exceptionDetails.exception?.description || v.exceptionDetails.text).slice(0, 160) }
  return { value: v?.result?.value, err: '' }
}

const visit = async (hash) => ev(`(async () => {
  const to = (p, ms) => Promise.race([p, new Promise((r) => setTimeout(() => r('__TO__'), ms))])
  location.hash = ${JSON.stringify(hash)}
  await to(new Promise((r) => setTimeout(r, 2400)), 7000)
  const app = document.querySelector('#app')
  return JSON.stringify({ hash: location.hash, txt: ((app ? app.innerText : '') || '').replace(/\\n{2,}/g, '\\n').trim() })
})()`)

const todo = PLAN.filter((p) => p[1])
const noId = PLAN.filter((p) => !p[1])
console.log(`扫 ${todo.length} 条具体详情/编辑页（30 条 :param 模板中采到真实 id 的）\n`)

const findings = []
let lastBody = ''
for (const [tpl, url] of todo) {
  const { value, err } = await visit('#' + url)
  if (err) { findings.push({ tpl, url, kind: 'PROBE_FAIL', detail: err }); console.log(`  ?? ${url} 探针失败`); continue }
  const { hash, txt } = JSON.parse(value)
  if (!hash.startsWith('#' + url)) {
    findings.push({ tpl, url, kind: 'NAV_FAIL', detail: `hash 停在 ${hash}` })
    console.log(`  ⚠️  ${url.padEnd(48)} 导航未生效 hash=${hash}`)
    continue
  }
  const knownGood = KNOWN_GOOD.some((re) => re.test(txt))
  const hits = RED_FLAGS.filter(([re]) => re.test(txt)).map(([, w]) => w)
  const notFound = !hits.length && NOT_FOUND.some((re) => re.test(txt))
  const body = txt.split('\n').map((s) => s.trim()).filter((s) => s && !CHROME_WORDS.includes(s))
  const fullBody = body.join('\n')
  if (hits.length && !knownGood) {
    findings.push({ tpl, url, kind: 'RED_FLAG', detail: [...new Set(hits)].join('/'), sample: body.slice(0, 3).join(' | ').slice(0, 100) })
    console.log(`  ❌ ${url.padEnd(48)} ${[...new Set(hits)].join('/')}`)
    console.log(`      ${body.slice(0, 3).join(' | ').slice(0, 110)}`)
  } else if (knownGood) {
    console.log(`  🔒 ${url.padEnd(48)} 如实降级提示（BUG-AT 已知正确行为）`)
  } else if (notFound) {
    findings.push({ tpl, url, kind: 'NOT_FOUND', detail: 'id 来自真实列表，页面却说找不到', sample: body.slice(0, 2).join(' | ').slice(0, 80) })
    console.log(`  ⚠️  ${url.padEnd(48)} 报「未找到」（id 来自真实列表）`)
  } else if (body.length === 0) {
    findings.push({ tpl, url, kind: 'EMPTY', detail: '除导航壳外无任何内容' })
    console.log(`  ⚠️  ${url.padEnd(48)} 除导航壳外无内容`)
  } else if (body.length > 0 && fullBody === lastBody) {
    findings.push({ tpl, url, kind: 'SUSPECT_STALE', detail: '整段正文与上一条完全相同' })
    console.log(`  ⚠️  ${url.padEnd(48)} 整段正文与上一条相同，疑似未重渲染`)
  } else {
    console.log(`  ✅ ${url.padEnd(48)} ${body[0].slice(0, 40)}`)
  }
  if (body.length) lastBody = fullBody
  await sleep(200)
}

const by = (k) => findings.filter((f) => f.kind === k)
console.log(`\n===== 汇总 =====`)
console.log(`实扫 ${todo.length} 条 / 30 条模板；采不到 id 未扫 ${noId.length} 条（已逐条注明原因）`)
console.log(`红旗 ${by('RED_FLAG').length} · 报未找到 ${by('NOT_FOUND').length} · 空页 ${by('EMPTY').length} · 疑似陈旧 ${by('SUSPECT_STALE').length} · 导航失败 ${by('NAV_FAIL').length} · 探针失败 ${by('PROBE_FAIL').length}`)
for (const f of findings) console.log(`  [${f.kind}] ${f.url}  ${f.detail}  ${f.sample || ''}`)
console.log(`\n--- 未扫（采不到真实 id，附原因）---`)
for (const [tpl, , why] of noId) console.log(`  ${tpl.padEnd(46)} ${why}`)

writeFileSync('C:/workspace/openpocket/wt3/logs/sweep-param.json',
  JSON.stringify({ scannedAt: new Date().toISOString(), ids: ID, scanned: todo, notScanned: noId, findings }, null, 2), 'utf8')
ws.close()
process.exit(by('RED_FLAG').length ? 1 : 0)
