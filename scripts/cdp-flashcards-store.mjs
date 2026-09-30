// cdp-flashcards-store.mjs — 在真机 WebView 上下文里读闪卡 store 的真实状态。
//
// 2026-09-30：真机 UI 保存卡片 201、PG 确认落库、宿主侧 API since=0 也返回 3 张卡，
// 但闪卡列表显示「0 cards」。宿主侧看不到 store，只能到真机里问。
// 本探针在页面上下文里：
//   1. 直接 fetch 增量 URL，看**真机**拿到的 envelope；
//   2. 尝试从 Vue devtools hook / window 上摸到 Pinia store 的 cards 数组；
//   3. 打印当前 hash 与列表 DOM 文本。
// 用法：POCKET_SERIAL=... node scripts/cdp-flashcards-store.mjs
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = '9239'
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
const send = (m, p = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) })
ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) } })
await new Promise((r) => ws.addEventListener('open', r))
await send('Runtime.enable')
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true, awaitPromise: true }))?.result?.value

console.log('hash =', await ev('location.hash'))

// 1. 真机上下文里直接打增量 API —— 看真机实际收到什么
//
// 踩过的坑：这里原本用裸相对路径 fetch('/api/flashcards?...')，在真机上返回的是
// **`<!doctype html>` 而不是 JSON**。原因和 BUG-J 同源 —— WebView origin 是
// https://localhost，裸相对路径落到打包资源，返回 index.html。App 自己的
// http 封装会加 resolveRuntimeApiBase() 前缀所以功能正常，探针必须照做，
// 否则会把"探针写错"误判成"产品有 bug"（这正是本轮已经犯过一次的教训）。
const since = process.env.POCKET_SINCE || '0'
const base = process.env.POCKET_BASE_ONDEVICE || 'http://localhost:8088'
const probe = await ev(`(async function(){
  var t = localStorage.getItem('pocket_token') || '';
  var h = t ? { Authorization: 'Bearer ' + t } : {};
  var out = { hasToken: !!t, tokenLen: t.length };
  for (var p of ['/api/flashcards?since=${since}&limit=200', '/api/flashcards/notes?since=${since}&limit=200']) {
    try {
      var r = await fetch('${base}' + p, { headers: h });
      var txt = await r.text();
      try {
        var j = JSON.parse(txt);
        out[p] = { status: r.status, cards: (j.cards||[]).length, decks: (j.decks||[]).length, notes: (j.notes||[]).length,
                   cardDecks: (j.cards||[]).map(function(c){return c.deckId;}) };
      } catch (e) { out[p] = { status: r.status, notJson: txt.slice(0, 80) }; }
    } catch (e) { out[p] = 'ERR ' + e.message; }
  }
  return JSON.stringify(out);
})()`)
console.log('真机 API 探测 =', probe)

// 2. 摸 Pinia store：优先 __VUE_DEVTOOLS_GLOBAL_HOOK__
const store = await ev(`(function(){
  try {
    var hook = window.__VUE_DEVTOOLS_GLOBAL_HOOK__;
    if (!hook) return 'NO_DEVTOOLS_HOOK';
    var apps = hook.apps || [];
    if (!apps.length) return 'NO_APPS';
    var pinia = apps[0]._instance && apps[apps.length-1]._instance.config && apps[apps.length-1]._instance.config.globalProperties.$pinia;
    if (!pinia) return 'NO_PINIA';
    var s = pinia.state.value;
    var fc = s.flashcards || {};
    return JSON.stringify({
      keys: Object.keys(s),
      cards: (fc.cards||[]).length,
      cardsSample: (fc.cards||[]).slice(0,5),
      deckConfigs: (fc.deckConfigs||[]).map(function(d){return d.deckId + ':' + d.name;}),
      notes: (fc.notes||[]).length,
      lastSyncedAt: fc.lastSyncedAt,
      error: fc.error ? String(fc.error) : null,
    });
  } catch (e) { return 'ERR ' + e.message; }
})()`)
console.log('store =', store)

// 3. 列表 DOM 文本
console.log('body =', ((await ev(`(document.body.innerText||'').replace(/\\s+/g,' ').slice(0, 400)`)) || ''))
