#!/usr/bin/env node
/**
 * diag-login-click.mjs — 点「登录」之后到底发生了什么。
 *
 * 已经确认：表单渲染正常、选择器命中、按钮变 enabled、click 也执行了，
 * 但 localStorage 里没有 token、hash 停在 #/login。
 * 所以问题在**登录请求本身**或它的响应处理，这里抓 network + console。
 *
 * 用法：POCKET_SERIAL=... POCKET_MASTER=... node scripts/diag-login-click.mjs
 */
import { execFileSync } from 'node:child_process'
import { requireDevPass } from './lib/dev-pass.mjs'
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9253'
const MASTER = process.env.POCKET_MASTER || ''
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
const net = []
const logs = []
const send = (m, p = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) })
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  if (m.method === 'Network.responseReceived') {
    const r = m.params.response
    if (/auth|login/i.test(r.url)) net.push({ url: r.url, status: r.status, reqId: m.params.requestId })
  }
  if (m.method === 'Network.loadingFailed') net.push({ failed: m.params.errorText })
  if (m.method === 'Runtime.consoleAPICalled' && ['error', 'warning'].includes(m.params.type)) {
    logs.push(m.params.type + ': ' + (m.params.args || []).map((a) => a.value ?? a.description ?? '').join(' ').slice(0, 200))
  }
  if (m.method === 'Runtime.exceptionThrown') logs.push('EXC: ' + (m.params.exceptionDetails?.exception?.description || '').slice(0, 200))
})
await new Promise((r) => ws.addEventListener('open', r))
await send('Runtime.enable')
await send('Network.enable')
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true }))?.result?.value

const devPass = requireDevPass()
console.log('devPass 长度 =', devPass.length, '(>0 才继续；不回显内容)')

await ev(`(function(){try{localStorage.removeItem('pocket_token')}catch(e){};return 1})()`)
await send('Page.reload', { ignoreCache: false })
await sleep(9000)
console.log('hash =', await ev('location.hash'))

const fillBy = (sel, val) => `(function(){var el=document.querySelector(${JSON.stringify(sel)});if(!el)return 'NF';var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;s.call(el,${JSON.stringify(val)});el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));return 'ok'})()`
console.log('填用户名 =', await ev(fillBy('input[placeholder*="用户名"]', 'admin')))
console.log('填密码   =', await ev(fillBy('input[type="password"]', devPass)))

// 轮询按钮 enabled
let dis = null
const dl = Date.now() + 10000
while (Date.now() < dl) {
  await sleep(300)
  dis = await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim()==='登录'});return b?b.disabled:null})()`)
  if (dis === false) break
}
console.log('登录按钮 disabled =', dis)
console.log('输入框回读 =', await ev(`JSON.stringify(Array.prototype.map.call(document.querySelectorAll('input'),function(e){return {ph:e.placeholder, len:(e.value||'').length}}))`))

net.length = 0
console.log('点击 =', await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim()==='登录'});if(b){b.click();return 'clicked'}return 'NF'})()`))
await sleep(8000)

console.log('\n--- network ---')
console.log(JSON.stringify(net, null, 1))
console.log('\n--- console / 异常 ---')
console.log(logs.length ? logs.slice(0, 8).join('\n') : '（无）')
console.log('\n--- 登录后状态 ---')
console.log(await ev(`JSON.stringify({
  hash: location.hash,
  hasToken: !!localStorage.getItem('pocket_token'),
  hasUser: !!localStorage.getItem('pocket_user'),
  bodyText: ((document.body.innerText||'').replace(/\\s+/g,' ').trim()).slice(0,220)
}, null, 1)`))
process.exit(0)
