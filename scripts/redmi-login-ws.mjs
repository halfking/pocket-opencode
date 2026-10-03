#!/usr/bin/env node
// 真机登录 + WebSocket 握手闭环验证
//
// 背景：证伪「Chromium 硬阻断 ws://」后，剩余问题是真机持有的 JWT 失效
//      （同一 token 连普通 HTTP /api/auth/me 也是 401 invalid or expired token）。
//      本脚本走真实登录 UI（v-model 原生 setter），再用 CDP Network 域
//      抓 WebSocket 握手响应码，作为「真机端到端打通」的判定证据。
//
// 口令不落在命令行/日志/仓库：从后端源码读取 dev 默认口令，只在内存中使用。
import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9225'

const adb = (args, timeout = 60000) =>
  execFileSync(ADB, args, { encoding: 'utf8', timeout, maxBuffer: 32 * 1024 * 1024 })

// ---- 0. reverse 隧道（真机走 localhost:8088）----
try { adb(['-s', SERIAL, 'reverse', 'tcp:8088', 'tcp:8088']) } catch (e) { console.log('reverse warn:', e.message) }
console.log('reverse:', adb(['-s', SERIAL, 'reverse', '--list']).trim())

// ---- 1. 从后端源码取 dev 默认口令（只在内存中）----
const src = readFileSync(join(ROOT, 'backend/internal/server/server_assistant.go'), 'utf8')
const m = src.match(/devPass\s*=\s*"([^"]+)"/)
if (!m) { console.log('CANNOT_READ_DEV_PASS'); process.exit(3) }
const DEV_PASS = m[1]
const DEV_USER = 'admin'

// ---- 2. 连接 CDP ----
const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const all = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
const sock = all.find((s) => s.endsWith(`_${pid}`)) || all[all.length - 1]
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${sock}`])
console.log('pid =', pid, 'sock =', sock)

const targets = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()
const page = targets.find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE'); process.exit(1) }
console.log('page url =', page.url)

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/9222|9223|9224|9225/, PORT))
let id = 0
const pending = new Map()
const events = []
const send = (method, params = {}) =>
  new Promise((res) => { const i = ++id; pending.set(i, res); ws.send(JSON.stringify({ id: i, method, params })) })

ws.addEventListener('message', (ev) => {
  const m = JSON.parse(ev.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  const p = m.params || {}
  const t = p.requestId
  if (m.method === 'Network.webSocketCreated') events.push(`${t} created ${String(p.url).replace(/token=[^&]+/, 'token=<redacted>')}`)
  if (m.method === 'Network.webSocketWillSendHandshakeRequest') events.push(`${t} handshake-sent`)
  if (m.method === 'Network.webSocketHandshakeResponseReceived') events.push(`${t} HANDSHAKE-RESPONSE status=${p.response.status}`)
  if (m.method === 'Network.webSocketFrameError') events.push(`${t} FRAME-ERROR "${p.errorMessage}"`)
  if (m.method === 'Network.webSocketFrameReceived') events.push(`${t} frame-received ${String(p.response?.payloadData ?? '').slice(0, 120)}`)
  if (m.method === 'Network.webSocketClosed') events.push(`${t} closed`)
  if (m.method === 'Log.entryAdded' && /websocket/i.test(p.entry.text)) {
    events.push(`LOG[${p.entry.level}] ${p.entry.text.replace(/token=[^'"\s]+/g, 'token=<redacted>')}`)
  }
})
await new Promise((r) => ws.addEventListener('open', r))
await send('Network.enable')
await send('Runtime.enable')
await send('Log.enable')

const evaluate = async (expression, awaitPromise = false) => {
  const r = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise })
  if (r?.exceptionDetails) return { __err: r.exceptionDetails.text }
  return r?.result?.value
}

// ---- 3. 走真实登录 UI ----
console.log('\n=== step 1: navigate to #/login ===')
await evaluate(`location.hash = '#/login'; 'ok'`)
await new Promise((r) => setTimeout(r, 2500))
console.log('hash =', await evaluate(`location.hash`))

// 1a) 若停在本地库解锁面板，先用主密码解锁——LoginView 用 v-if 把账号登录表单
//     挡在解锁面板后面，不解锁就找不到用户名输入框。
const MASTER = process.env.POCKET_MASTER || ''
console.log('\n=== step 1b: unlock local vault if gated ===')
if (MASTER) {
  console.log('unlock ->', await evaluate(`(function(){
    var el = document.querySelector('input[placeholder*="主密码"]');
    if (!el) return 'NOT_GATED';
    var setter = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el), 'value').set;
    setter.call(el, ${JSON.stringify(MASTER)});
    el.dispatchEvent(new Event('input', { bubbles: true }));
    var b = Array.from(document.querySelectorAll('button')).find(x => (x.textContent||'').trim() === '解锁');
    if (!b) return 'NO_UNLOCK_BTN';
    b.click();
    return 'unlock-clicked';
  })()`))
  await new Promise((r) => setTimeout(r, 4000))
}
console.log('inputs now =', await evaluate(
  `Array.from(document.querySelectorAll('input')).map(i => i.type + '/' + (i.placeholder||'')).join(', ') || '(none)'`))

// 输入框定位：LoginView 同时存在「本地库解锁」密码框与「账号登录」密码框，
// 必须按 placeholder + 文档顺序取账号登录那一组，否则会填错框。
const setLoginField = (val, which) => `(function(){
  var inputs = Array.from(document.querySelectorAll('input'));
  var user = inputs.find(i => (i.placeholder || '').indexOf('用户名') >= 0)
           || inputs.find(i => i.type === 'text');
  if (!user) return 'NO_USER_INPUT:' + inputs.map(i => i.type + '/' + (i.placeholder||'')).join(',');
  var target;
  if (${JSON.stringify(which)} === 'user') {
    target = user;
  } else {
    var after = inputs.filter(i => user.compareDocumentPosition(i) & Node.DOCUMENT_POSITION_FOLLOWING);
    target = after.find(i => i.type === 'password') || null;
    if (!target) return 'NO_PASSWORD_AFTER_USER';
  }
  var setter = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(target), 'value').set;
  setter.call(target, ${JSON.stringify(val)});
  target.dispatchEvent(new Event('input', { bubbles: true }));
  target.dispatchEvent(new Event('change', { bubbles: true }));
  return 'set:' + target.type + ':' + (target.placeholder || target.className || '');
})()`

console.log('\n=== step 2: fill credentials (pass not printed) ===')
console.log('username ->', await evaluate(setLoginField(DEV_USER, 'user')))
console.log('password ->', await evaluate(setLoginField(DEV_PASS, 'pass')))

const before = await evaluate(`localStorage.getItem('pocket_token') ? 'had-token' : 'no-token'`)
console.log('token before login =', before)

console.log('\n=== step 3: click login ===')
const clicked = await evaluate(`(function(){
  var btns = Array.from(document.querySelectorAll('button'));
  var b = btns.find(x => /^(登录|登录中\\.\\.\\.)$/.test((x.textContent||'').trim()));
  if (!b) return 'NO_BUTTON:' + btns.map(x=>x.textContent.trim()).join('|');
  if (b.disabled) return 'BUTTON_DISABLED';
  b.click();
  return 'clicked:' + b.textContent.trim();
})()`)
console.log('login click =', clicked)

await new Promise((r) => setTimeout(r, 7000))

// 登录后立即用新 token 验一个普通 HTTP 端点，区分「WS 特有」与「token 普遍失效」
console.log('\n=== step 3b: probe HTTP with fresh token ===')
console.log(await evaluate(`(async () => {
  var t = localStorage.getItem('pocket_token');
  if (!t) return 'no-token';
  try {
    var base = location.origin === 'https://localhost' ? 'http://localhost:8088' : 'http://localhost:8088';
    var r = await fetch(base + '/api/auth/me', { headers: { Authorization: 'Bearer ' + t } });
    return 'HTTP ' + r.status + ' ' + (await r.text()).slice(0, 160);
  } catch (e) { return 'fetch-err:' + e.message; }
})()`, true))

// ---- 4. 判定 ----
const after = await evaluate(`(function(){
  var t = localStorage.getItem('pocket_token') || '';
  return JSON.stringify({
    hasToken: !!t, tokenLen: t.length,
    hash: location.hash,
    user: localStorage.getItem('pocket_user') || ''
  });
})()`)
console.log('\n=== step 4: result ===')
console.log('after login =', after)

// 主动再连一次 WS，判定握手码（不依赖登录时序）
console.log('\n=== step 5: explicit WS handshake with current token ===')
events.length = 0
console.log(await evaluate(`(function(){
  var t = localStorage.getItem('pocket_token');
  if (!t) return 'no-token';
  window.__wsProbe = 'pending';
  var ws = new WebSocket('ws://localhost:8088/ws?token=' + encodeURIComponent(t));
  window.__wsProbeWs = ws;
  ws.onopen = function(){ window.__wsProbe = 'OPEN' };
  ws.onclose = function(e){ if (window.__wsProbe !== 'OPEN') window.__wsProbe = 'CLOSE code=' + e.code };
  ws.onerror = function(){ if (window.__wsProbe === 'pending') window.__wsProbe = 'ERROR' };
  return 'started';
})()`))
await new Promise((r) => setTimeout(r, 4000))
console.log('in-page ws state =', await evaluate(`window.__wsProbe`))

console.log('\n=== CDP websocket events ===')
const seen = [...new Set(events)]
if (!seen.length) console.log('(no websocket events)')
for (const e of seen) console.log(' ', e)

const hs = events.find((e) => e.includes('HANDSHAKE-RESPONSE status=101'))
console.log('\nVERDICT_WS_101 =', !!hs)
ws.close()
process.exit(hs ? 0 : 4)
