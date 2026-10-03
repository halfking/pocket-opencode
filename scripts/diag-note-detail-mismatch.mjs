// diag-note-detail-mismatch.mjs — 判定「列表里有、详情说没有」是不是真缺陷。
//
// sweep-param-routes 实测：
//   /api/notes  返回 7 条，含 note-1790767908638675300-1
//   /api/meetings 返回 7 条，含 mtg_1790818948067029300_1
//   而 /notes/<那个 id>  显示「笔记不存在或已被删除」
//     /meetings/<那个 id> 显示「会议不存在」
//
// 两种解释后果完全不同：
//   (A) 假阳性：UI 列表页里根本没有这条（API 返回的是别的模块/别的 workspace 的数据），
//       详情页说「不存在」是**正确**行为。
//   (B) 真缺陷：UI 列表页里明明列得出这条，点进去却说没有 —— 典型的
//       「列表与详情数据源不一致」，和 BUG-AR（default 分区）是同一类病。
//
// 判别方法：只看 API 判不了，必须**从 UI 列表页把 id 抓出来**，
// 再用这个 id 开详情。UI 里有 ⇒ (B)；UI 里没有 ⇒ (A)。
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9470'
const adb = (a, t = 25000) => execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const adbSoft = (a) => { try { return adb(a, 8000) } catch { return '' } }
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

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
  if (v?.exceptionDetails) return { value: null, err: String(v.exceptionDetails.exception?.description || v.exceptionDetails.text).slice(0, 200) }
  return { value: v?.result?.value, err: '' }
}

for (const listRoute of ['/notes', '/meetings']) {
  console.log(`\n########## ${listRoute} ##########`)
  const r = await ev(`(async () => {
    const to = (p, ms) => Promise.race([p, new Promise((r) => setTimeout(() => r('__TO__'), ms))])
    location.hash = ${JSON.stringify('#' + listRoute)}
    await to(new Promise((r) => setTimeout(r, 3000)), 8000)
    const app = document.querySelector('#app')
    const txt = ((app ? app.innerText : '') || '')
    const lines = txt.split('\\n').map((s) => s.trim()).filter(Boolean)
    // 从 DOM 里找带 id 形态的 data-* / 属性，以及点按目标
    const attrs = []
    for (const el of Array.from(document.querySelectorAll('*')).slice(0, 4000)) {
      for (const a of el.attributes || []) {
        if (/^(data-)?(id|note-id|meeting-id|item-id|key)$/i.test(a.name) || /^(data-testid|data-key|data-id)$/i.test(a.name)) {
          if (a.value && a.value.length > 4) attrs.push(a.name + '=' + a.value)
        }
      }
    }
    return JSON.stringify({ hash: location.hash, lines: lines.slice(0, 30), attrs: attrs.slice(0, 20) })
  })()`)
  if (r.err) { console.log('  探针失败: ' + r.err); continue }
  const d = JSON.parse(r.value)
  console.log(`  落地 hash=${d.hash}`)
  console.log('  --- 列表页文本（前 30 行）---')
  d.lines.forEach((l, i) => console.log(`   ${String(i + 1).padStart(2)}| ${l.slice(0, 90)}`))
  console.log('  --- 带 id 的属性 ---')
  if (d.attrs.length) d.attrs.forEach((a) => console.log(`   ${a.slice(0, 90)}`))
  else console.log('   （无）')
}
ws.close()
process.exit(0)
