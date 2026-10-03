#!/usr/bin/env node
/**
 * diag-login-dom.mjs — 登录页真实 DOM 盘点。
 *
 * 现象：清掉 pocket_token 并 reload 后，会话恢复脚本既没找到
 * `input[placeholder*="主密码"]` 也没找到 `input[placeholder*="用户名"]`，
 * hash 停在 #/login，localStorage 里没有 token。
 *
 * 两种可能，页面上直接分辨：
 *   A. 登录表单根本没渲染（路由守卫 / 初始化卡住 / 白屏）
 *   B. 渲染了但 placeholder 与脚本假设的不一致
 *
 * 只读，不改状态。
 *
 * 用法：POCKET_SERIAL=... POCKET_MASTER=... node scripts/diag-login-dom.mjs
 */
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9252'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE'); process.exit(1) }

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
const errs = []
const send = (m, p = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) })
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  if (m.method === 'Runtime.exceptionThrown') errs.push(m.params?.exceptionDetails?.exception?.description || m.params?.exceptionDetails?.text || '')
  if (m.method === 'Runtime.consoleAPICalled' && m.params?.type === 'error') {
    errs.push('console.error: ' + (m.params.args || []).map((a) => a.value ?? a.description ?? '').join(' ').slice(0, 180))
  }
})
await new Promise((r) => ws.addEventListener('open', r))
await send('Runtime.enable')
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true }))?.result?.value

console.log('--- 1. 清 token 前 ---')
console.log('hash =', await ev('location.hash'))
console.log('有 token =', await ev(`!!localStorage.getItem('pocket_token')`))

console.log('\n--- 2. 清 token + reload ---')
await ev(`(function(){try{localStorage.removeItem('pocket_token');localStorage.removeItem('pocket_workspace_id')}catch(e){};return 1})()`)
await send('Page.reload', { ignoreCache: false })
await sleep(9000)
console.log('reload 后 hash =', await ev('location.hash'))

console.log('\n--- 3. 页面文本（全量，别截断）---')
console.log(await ev(`JSON.stringify({
  innerText: ((document.body.innerText||'').replace(/\\s+/g,' ').trim()).slice(0,400),
  len: (document.body.innerText||'').trim().length,
  appHTMLLen: (document.querySelector('#app')||{innerHTML:''}).innerHTML.length
}, null, 1)`))

console.log('\n--- 4. 所有 input（真实 placeholder / type / name）---')
console.log(await ev(`JSON.stringify(Array.prototype.map.call(document.querySelectorAll('input'), function(el){
  return { type: el.type, name: el.name, id: el.id, placeholder: el.placeholder, ariaLabel: el.getAttribute('aria-label') };
}), null, 1)`))

console.log('\n--- 5. 所有 button 文本 ---')
console.log(await ev(`JSON.stringify(Array.prototype.map.call(document.querySelectorAll('button'), function(b){
  return { text: (b.textContent||'').trim().slice(0,30), disabled: b.disabled, cls: b.className };
}).slice(0,15), null, 1)`))

console.log('\n--- 6. 选择器命中率（脚本当前的假设）---')
console.log(await ev(`JSON.stringify({
  'input[placeholder*="主密码"]': document.querySelectorAll('input[placeholder*="主密码"]').length,
  'input[placeholder*="用户名"]': document.querySelectorAll('input[placeholder*="用户名"]').length,
  'input[type="password"]': document.querySelectorAll('input[type="password"]').length,
  'input 总数': document.querySelectorAll('input').length
}, null, 1)`))

console.log('\n--- 7. localStorage 键 ---')
console.log(await ev(`JSON.stringify(Object.keys(localStorage), null, 1)`))

console.log('\n--- 8. 异常 / console.error ---')
console.log(errs.length ? errs.slice(0, 6).join('\n') : '（无）')
process.exit(0)
