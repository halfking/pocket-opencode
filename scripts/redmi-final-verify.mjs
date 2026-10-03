#!/usr/bin/env node
// 真机终验：清 override → 重载 → 真实 UI 登录 → 验证「App 自己的」WS → tasks 模块
//
// 关键纪律（BUG-F 归因翻车的教训）：
//   这里**不手工 new WebSocket**，只观察 App 启动/登录时自己发起的那条连接。
//   判据 = Network.webSocketCreated 的 URL 主机:端口 与 resolveRuntimeApiBase() 一致，
//   且 Network.webSocketHandshakeResponseReceived.status == 101。
import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9228'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const adb = (args, timeout = 60000) =>
  execFileSync(ADB, args, { encoding: 'utf8', timeout, maxBuffer: 32 * 1024 * 1024 })

try { adb(['-s', SERIAL, 'reverse', 'tcp:8088', 'tcp:8088']) } catch {}
console.log('reverse:', adb(['-s', SERIAL, 'reverse', '--list']).trim() || '(none)')

const src = readFileSync(join(ROOT, 'backend/internal/server/server_assistant.go'), 'utf8')
const DEV_PASS = (src.match(/devPass\s*=\s*"([^"]+)"/) || [])[1]
const DEV_USER = 'admin'
const MASTER = process.env.POCKET_MASTER || ''

const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const all = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
const sock = all.find((s) => s.endsWith(`_${pid}`)) || all[all.length - 1]
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${sock}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE'); process.exit(1) }

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
const events = []
const send = (m, p = {}) => new Promise((res) => { const i = ++id; pending.set(i, res); ws.send(JSON.stringify({ id: i, method: m, params: p })) })
ws.addEventListener('message', (ev) => {
  const m = JSON.parse(ev.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  const p = m.params || {}
  if (m.method === 'Network.webSocketCreated') events.push({ t: 'created', url: String(p.url).replace(/token=[^&]+/, 'token=<redacted>') })
  if (m.method === 'Network.webSocketHandshakeResponseReceived') events.push({ t: 'hs', status: p.response.status })
  if (m.method === 'Network.webSocketFrameError') events.push({ t: 'err', msg: p.errorMessage })
  if (m.method === 'Network.webSocketFrameReceived') events.push({ t: 'frame', data: String(p.response?.payloadData ?? '').slice(0, 90) })
  if (m.method === 'Network.responseReceived' && p.response?.url?.includes('/api/')) {
    events.push({ t: 'http', status: p.response.status, mime: p.response.mimeType, url: p.response.url.replace(/token=[^&]+/, 'token=<redacted>') })
  }
  if (m.method === 'Network.loadingFailed') events.push({ t: 'failed', err: p.errorText, type: p.type })
  if (m.method === 'Runtime.consoleAPICalled' && ['error', 'warning'].includes(m.params.type)) {
    events.push({ t: `console.${m.params.type}`, msg: m.params.args.map((a) => a.value ?? a.description ?? '').join(' ').slice(0, 200) })
  }
})
await new Promise((r) => ws.addEventListener('open', r))
await send('Network.enable')
await send('Runtime.enable')

const evaluate = async (expression, awaitPromise = false) => {
  const r = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise })
  if (r?.exceptionDetails) return { __err: r.exceptionDetails.text }
  return r?.result?.value
}

// ---- 1. 清掉设备上残留的 pocket_api_base override（它会静默压过构建默认值）----
console.log('\n=== 1. clear stale pocket_api_base override ===')
console.log(await evaluate(`(function(){
  var before = localStorage.getItem('pocket_api_base');
  localStorage.removeItem('pocket_api_base');
  return 'before=' + before + ' after=' + localStorage.getItem('pocket_api_base');
})()`))

// ---- 2. 重载，让 App 用构建默认值重新起 ----
console.log('\n=== 2. reload ===')
await send('Page.enable')
await send('Page.reload', { ignoreCache: false })
await sleep(6000)
console.log('page =', await evaluate(`location.origin + location.hash`))

// ---- 3. 解锁本地库（若被 gate）----
if (MASTER) {
  await evaluate(`location.hash = '#/login'`)
  await sleep(2000)
  const r = await evaluate(`(function(){
    var el = document.querySelector('input[placeholder*="主密码"]');
    if (!el) return 'NOT_GATED';
    Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set.call(el, ${JSON.stringify(MASTER)});
    el.dispatchEvent(new Event('input',{bubbles:true}));
    var b = Array.from(document.querySelectorAll('button')).find(x => (x.textContent||'').trim() === '解锁');
    if (!b) return 'NO_UNLOCK_BTN'; b.click(); return 'unlock-clicked';
  })()`)
  console.log('unlock =', r)
  await sleep(4000)
}

// ---- 4. 真实 UI 登录 ----
console.log('\n=== 3. login via real UI ===')
const setField = (val, which) => `(function(){
  var inputs = Array.from(document.querySelectorAll('input'));
  var user = inputs.find(i => (i.placeholder||'').indexOf('用户名') >= 0) || inputs.find(i => i.type === 'text');
  if (!user) return 'NO_USER_INPUT:' + inputs.map(i=>i.type+'/'+(i.placeholder||'')).join(',');
  var target = ${JSON.stringify(which)} === 'user' ? user
    : inputs.filter(i => user.compareDocumentPosition(i) & Node.DOCUMENT_POSITION_FOLLOWING).find(i => i.type === 'password');
  if (!target) return 'NO_TARGET';
  Object.getOwnPropertyDescriptor(Object.getPrototypeOf(target),'value').set.call(target, ${JSON.stringify(val)});
  target.dispatchEvent(new Event('input',{bubbles:true}));
  return 'set:' + target.type;
})()`
console.log('user ->', await evaluate(setField(DEV_USER, 'user')))
console.log('pass ->', await evaluate(setField(DEV_PASS, 'pass')))
console.log('click ->', await evaluate(`(function(){
  var b = Array.from(document.querySelectorAll('button')).find(x => /^(登录|登录中\\.\\.\\.)$/.test((x.textContent||'').trim()));
  if (!b) return 'NO_BUTTON'; if (b.disabled) return 'DISABLED'; b.click(); return 'clicked';
})()`))
await sleep(8000)

console.log('\n=== 4. state after login ===')
console.log(await evaluate(`JSON.stringify({
  origin: location.origin, hash: location.hash,
  hasToken: !!localStorage.getItem('pocket_token'),
  apiBaseOverride: localStorage.getItem('pocket_api_base'),
  isSecureContext: window.isSecureContext
})`))

// ---- 5. App 自己的 WS（不手工构造）----
console.log('\n=== 5. App-owned WebSocket (no manual probe) ===')
await sleep(6000)
const wsEvents = events.filter((e) => ['created', 'hs', 'err', 'frame'].includes(e.t))
if (!wsEvents.length) console.log('(no websocket events at all)')
for (const e of wsEvents) console.log(' ', JSON.stringify(e))
const createdUrl = wsEvents.find((e) => e.t === 'created')?.url || ''
const got101 = wsEvents.some((e) => e.t === 'hs' && e.status === 101)
const hostOk = /ws:\/\/localhost:8088\//.test(createdUrl)
console.log(`  -> target host matches resolved base: ${hostOk}   handshake 101: ${got101}`)

// ---- 6. /tasks 模块（BUG-J 回归）----
console.log('\n=== 6. BUG-J regression: /tasks module ===')
events.length = 0
await evaluate(`location.hash = '#/tasks'`)
await sleep(6000)
const taskHttp = events.filter((e) => e.t === 'http' && e.url.includes('/api/tasks'))
for (const e of taskHttp) console.log(' ', JSON.stringify(e))
const htmlTasks = taskHttp.some((e) => e.mime === 'text/html')
const jsonTasks = taskHttp.some((e) => e.mime && e.mime.includes('json'))
const tasksErr = events.filter((e) => e.t === 'console.error' && /task/i.test(e.msg || ''))
console.log('  -> tasks returned JSON:', jsonTasks, ' returned HTML:', htmlTasks, ' console errors:', tasksErr.length)
if (tasksErr.length) tasksErr.forEach((e) => console.log('     !', e.msg))

console.log('\n=== VERDICT ===')
console.log(JSON.stringify({
  appWsHandshake101: got101,
  appWsTargetOk: hostOk,
  tasksJson: jsonTasks,
  tasksHtmlRegression: htmlTasks,
  tasksConsoleErrors: tasksErr.length,
}, null, 2))
ws.close()
process.exit(got101 && hostOk && jsonTasks && !htmlTasks && tasksErr.length === 0 ? 0 : 4)
