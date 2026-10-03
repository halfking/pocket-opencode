// diag-meeting-detail-click.mjs — 会议列表点进去到底用哪个 id。
//
// 已知：/api/meetings 返回 7 条（含 mtg_1790818948067029300_1），
// 而 /meetings/mtg_1790818948067029300_1 显示「会议不存在」；
// 同时 UI 的会议列表页**确实列出了会议**（10月1日 10:05/10:07 …）。
// 两种可能：
//   (A) UI 列表里的会议用的是另一套 id（如 meeting-1790823390906-db5t4q），
//       那我采的 mtg_… 根本不是 UI 认的 id ⇒ 详情报「不存在」是正确的。
//   (B) UI 列表里的就是 mtg_…，点进去却说没有 ⇒ 真缺陷（列表/详情数据源不一致）。
//
// 判别：直接在列表页点第一条会议，读它跳转后的 hash。
// 这一步无法用 API 代替——只有真实点击能给出 UI 认的 id。
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9471'
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

// 在列表页里按「含日期的会议标题行」点进去
const r = await ev(`(async () => {
  const to = (p, ms) => Promise.race([p, new Promise((r) => setTimeout(() => r('__TO__'), ms))])
  location.hash = '#/meetings'
  await to(new Promise((r) => setTimeout(r, 3000)), 8000)
  // 会议列表项：找文本匹配 /\\d+月\\d+日 .* 会议/ 的可点击祖先
  const nodes = Array.from(document.querySelectorAll('*')).filter((el) => {
    const t = (el.innerText || '').trim()
    return /^\\d{1,2}月\\d{1,2}日\\s*\\d{2}:\\d{2}\\s*会议$/.test(t) && el.children.length <= 3
  })
  if (!nodes.length) return JSON.stringify({ err: 'NO_MEETING_NODE', hash: location.hash })
  const el = nodes[0]
  const before = location.hash
  el.click()
  await to(new Promise((r) => setTimeout(r, 2500)), 6000)
  const app = document.querySelector('#app')
  const txt = ((app ? app.innerText : '') || '')
  return JSON.stringify({
    clickedText: (el.innerText || '').trim(),
    before, after: location.hash,
    lines: txt.split('\\n').map((s) => s.trim()).filter(Boolean).slice(0, 14),
  })
})()`)
if (r.err) { console.log('探针失败: ' + r.err); process.exit(6) }
const d = JSON.parse(r.value)
if (d.err) { console.log('  ' + d.err + '  hash=' + d.hash); process.exit(7) }
console.log('=== 在会议列表点第一条 ===')
console.log(`  点中的文本: ${d.clickedText}`)
console.log(`  点击前 hash: ${d.before}`)
console.log(`  点击后 hash: ${d.after}`)
console.log('  --- 详情页文本 ---')
d.lines.forEach((l, i) => console.log(`   ${String(i + 1).padStart(2)}| ${l.slice(0, 90)}`))
const usesMtg = /mtg_/.test(d.after)
console.log('')
console.log('=== 判读 ===')
if (usesMtg) {
  console.log('  ⇒ UI 认的是 mtg_ 系列 id。')
  console.log('     若该 id 的详情页显示「会议不存在」，则是**真缺陷**（列表/详情数据源不一致）。')
} else {
  console.log('  ⇒ UI 认的是**另一套 id**（不是 mtg_）。')
  console.log('     所以拿 /api/meetings 采的 id 开详情报「不存在」是**正确行为** ⇒ 巡检那条是假阳性。')
}
ws.close()
process.exit(0)
