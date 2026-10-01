// diag-vault-error.mjs — 取 App 通过 registerPlugin 代理实际收到的错误文案，
// 以及点「解锁」后界面变成什么。用来判断错误归因是否误导。
//
// 第二版的坑：页内 await 的 promise 若永不 settle，CDP 的 awaitPromise 会一直等，
// 结果整条探针 __frozen__（两次都栽了）。修法：**页内自己加超时兜底**，
// 无论如何都让 promise settle，CDP 才拿得到应答。
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
const ev = async (x, ms = 20000) => {
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
const show = async (label, expr) => {
  const { value, err } = await ev(expr)
  console.log(`\n### ${label}`)
  console.log(err ? '  !! ' + err : String(value))
}

// 页内通用兜底：任何 await 都套一层 3s 超时，绝不让 CDP 空等
const GUARD = `const withTimeout = (p, ms, tag) => Promise.race([
  p.then(v => tag + 'RESOLVED: ' + JSON.stringify(v), e => tag + 'REJECTED: ' + (e && e.message ? e.message : String(e))),
  new Promise(r => setTimeout(() => r(tag + 'NEVER_SETTLED(>3s)'), ms)),
]);`

await show('1. registerPlugin 代理的真实行为（页内 3s 兜底）', `(async () => { ${GUARD}
  const C = window.Capacitor
  const proxy = C.Plugins.Keystore
  if (!proxy) return 'C.Plugins.Keystore === undefined'
  const out = []
  for (const m of ['isVaultInitialized', 'unlockWithPassword', 'listEntries']) {
    let p
    try { p = proxy[m]() } catch (e) { out.push(m + ' SYNC_THROW: ' + e.message); continue }
    out.push(await withTimeout(p, 3000, m + ' => '))
  }
  return out.join('\\n')
})()`)

await show('2. 点「解锁」后界面文本（页内兜底）', `(async () => { ${GUARD}
  const btns = Array.from(document.querySelectorAll('button')).filter(b => /解锁/.test(b.innerText || ''))
  if (!btns.length) return 'NO_UNLOCK_BUTTON  hash=' + location.hash
  const label = btns[0].innerText.trim()
  btns[0].click()
  await withTimeout(new Promise(r => setTimeout(r, 2000)), 5000, 'WAIT ')
  const txt = (document.querySelector('#app').innerText || '').replace(/\\n{2,}/g, '\\n').trim()
  return '点击了「' + label + '」  hash=' + location.hash + '\\n---- 之后 ----\\n' + txt.slice(0, 700)
})()`)
ws.close()
process.exit(0)
