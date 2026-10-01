// diag-vault-view.mjs — 真机看「密码箱」页面用户实际看到什么。
//
// 背景：Android 模块里**没有** KeystorePlugin.java（唯一含 "Keystore" 的
// BiometricAuthPlugin.java 用的是 Android 原生 KeyStore API，无关）。
// 所以 keystore.ts 的 registerPlugin('Keystore') 在设备上会 reject。
// 而 VaultListView.vue:117-122 把这个 reject 吞进 catch，显示
//   「主密码尚未设置（登录后自动初始化）」
// ⇒ 若真机上显示的是这句，那就是**错误归因**：真实原因是插件不存在，
//   而界面告诉用户「主密码没设」，会把人和后续排查都带偏。
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
const ev = async (x, ms = 25000) => {
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

await show('0. 当前路由', 'location.hash')
await show('1. 插件在设备上到底存不存在（直接问 Capacitor）', `(async () => {
  const cap = await import('/assets/index-BDqrheGf.js').catch(() => null)
  // 直接用全局 Capacitor 问，比 import bundle 稳
  const C = window.Capacitor
  if (!C) return 'NO_GLOBAL_CAPACITOR'
  try {
    const names = Object.keys(C.Plugins || {})
    const has = names.includes('Keystore')
    return JSON.stringify({ registered: names, hasKeystore: has })
  } catch (e) { return 'ERR:' + e.message }
})()`)
await show('2. 实际调用 isVaultInitialized()', `(async () => {
  try {
    const C = window.Capacitor
    const r = await C.Plugins.Keystore.isVaultInitialized()
    return 'RESOLVED: ' + JSON.stringify(r)
  } catch (e) { return 'REJECTED: ' + (e && e.message ? e.message : String(e)) }
})()`)
await show('3. 跳到 #/vault 并读用户实际看到的文字', `(async () => {
  location.hash = '#/vault'
  await new Promise(r => setTimeout(r, 2500))
  const root = document.querySelector('#app')
  const txt = (root.innerText || '').replace(/\\n{2,}/g, '\\n').trim()
  return 'hash=' + location.hash + '\\n---- 页面文本 ----\\n' + txt.slice(0, 900)
})()`)
ws.close()
process.exit(0)
