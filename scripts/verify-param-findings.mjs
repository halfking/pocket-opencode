// verify-param-findings.mjs — 复核 sweep-param-routes 的 3 条命中，判定真假阳性。
//
// sweep-param-routes 报了：
//   [NOT_FOUND] /notes/note-1790…      样例首行 "arrow_back | 笔记详情"
//   [NOT_FOUND] /meetings/mtg_1790…    样例首行 "arrow_back | 会议详情"
//   [NAV_FAIL]  /meetings/mtg_…/record  hash 停在 …?record=1
//
// 三条都**可疑**：前两条页面明明渲染出了标题却被判「未找到」，
// 第三条 §4.64.3 已记载 /meetings/* 存在「进页面即带 record=1」的业务跳转。
// 假阳性比漏报更危险——写进 handoff 就成了「已确认缺陷」。
// 所以这里把整页文本打出来，逐条看它到底说了什么。
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9469'
const adb = (a, t = 25000) => execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const adbSoft = (a) => { try { return adb(a, 8000) } catch { return '' } }
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const CASES = [
  ['#/notes/note-1790767908638675300-1', 'NOT_FOUND 命中'],
  ['#/meetings/mtg_1790818948067029300_1', 'NOT_FOUND 命中'],
  ['#/meetings/mtg_1790818948067029300_1/record', 'NAV_FAIL 命中'],
]

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

for (const [hash, tag] of CASES) {
  const r = await ev(`(async () => {
    const to = (p, ms) => Promise.race([p, new Promise((r) => setTimeout(() => r('__TO__'), ms))])
    location.hash = ${JSON.stringify(hash)}
    await to(new Promise((r) => setTimeout(r, 2500)), 7000)
    const app = document.querySelector('#app')
    const txt = ((app ? app.innerText : '') || '')
    // 单独把含「未找到/不存在/404」的行摘出来，看它出现在正文还是别的容器
    const lines = txt.split('\\n').map((s) => s.trim()).filter(Boolean)
    const hits = lines.filter((l) => /未找到|不存在|Not Found|not found|404/.test(l))
    return JSON.stringify({ hash: location.hash, total: lines.length, lines: lines.slice(0, 22), hits })
  })()`)
  console.log(`\n===== ${hash}  （${tag}） =====`)
  if (r.err) { console.log('  探针失败: ' + r.err); continue }
  const d = JSON.parse(r.value)
  console.log(`  落地 hash = ${d.hash}   正文行数 = ${d.total}`)
  console.log('  --- 正文前 22 行 ---')
  d.lines.forEach((l, i) => console.log(`   ${String(i + 1).padStart(2)}| ${l.slice(0, 96)}`))
  console.log('  --- 含「未找到/不存在/404」的行 ---')
  if (d.hits.length) d.hits.forEach((l) => console.log(`   ⚠ ${l.slice(0, 120)}`))
  else console.log('   （无）')
}
ws.close()
process.exit(0)
