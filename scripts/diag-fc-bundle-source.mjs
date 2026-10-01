// diag-fc-bundle-source.mjs — 把装机 bundle 里闪卡到期判据的**原文**抓出来。
//
// 为什么必须看 bundle 而不是工作区源码：设备上的 APK 是更早构建的（hash 与本地 dist 不同），
// 「读源码推断设备行为」正是最容易骗人的地方。本轮真机现象（数据算出 1、computed 返回 []）
// 只有看设备上真正在跑的代码才能定性。
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9427'
const adb = (a, t = 60000) => execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

const pid = adb(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0; const pending = new Map()
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
await new Promise((r) => ws.addEventListener('open', r))
const ev = async (x, ms = 30000) => {
  const i = ++id
  const v = await new Promise((r) => {
    const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms)
    pending.set(i, (y) => { clearTimeout(t); r(y) })
    ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } }))
  })
  if (v?.__t) return { value: undefined, err: '__frozen__' }
  if (v?.exceptionDetails) return { value: undefined, err: String(v.exceptionDetails.exception?.description || v.exceptionDetails.text).slice(0, 300) }
  return { value: v?.result?.value, err: '' }
}

const expr = `(async () => {
  const srcs = Array.from(document.querySelectorAll('script[src]')).map(s => s.getAttribute('src'))
  const out = []
  for (const src of srcs) {
    const t = await (await fetch(src)).text()
    // 闪卡 store 的判据必然同时出现 deletedAt 与 .due
    let i = -1, hits = 0
    while ((i = t.indexOf('deletedAt', i + 1)) !== -1 && hits < 40) {
      const win = t.slice(Math.max(0, i - 260), i + 260)
      if (win.includes('.due') && win.includes('state')) {
        hits++
        out.push('--- @' + i + ' ---\\n' + win)
      }
    }
    if (out.length) out.unshift('=== ' + src + ' len=' + t.length + ' 命中 ' + hits + ' 处 ===')
  }
  return out.join('\\n').slice(0, 6000) || 'NO_MATCH'
})()`

const { value, err } = await ev(expr)
console.log(err ? '!! ' + err : value)
ws.close()
process.exit(0)
