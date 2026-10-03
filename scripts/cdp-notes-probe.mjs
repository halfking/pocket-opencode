// 探查：笔记详情/编辑页 DOM、删除确认弹窗、IndexedDB 结构
import { execFileSync } from 'node:child_process'
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = '9233'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const sk = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`]).split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${sk.find((s) => s.endsWith(`_${pid}`)) || sk[sk.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0; const pending = new Map()
const send = (m, p = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) })
ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) } })
await new Promise((r) => ws.addEventListener('open', r))
await send('Runtime.enable')
const ev = async (x, aw = false) => {
  const r = await send('Runtime.evaluate', { expression: x, returnByValue: true, awaitPromise: aw })
  return r?.exceptionDetails ? { __err: r.exceptionDetails.text } : r?.result?.value
}

console.log('current hash =', await ev(`location.hash`))

// IndexedDB 结构
console.log('\n=== IndexedDB ===')
console.log(await ev(`(async () => {
  const dbs = await indexedDB.databases();
  const out = [];
  for (const d of dbs) {
    const stores = await new Promise(res => {
      const req = indexedDB.open(d.name);
      req.onsuccess = () => { const db = req.result; res(Array.from(db.objectStoreNames)); db.close(); };
      req.onerror = () => res(['<err>']);
    });
    out.push({ name: d.name, version: d.version, stores });
  }
  return JSON.stringify(out);
})()`, true))

// 列表页卡片结构
await ev(`location.hash = '#/notes'`); await sleep(2500)
console.log('\n=== list card markup (first 600 chars) ===')
console.log(await ev(`(function(){
  var el = document.querySelector('[class*="note-card"],[class*="card"]');
  return el ? el.outerHTML.slice(0, 700) : 'NO_CARD_CLASS';
})()`))
console.log('\n=== list buttons ===')
console.log(await ev(`JSON.stringify(Array.from(document.querySelectorAll('button')).map(b => (b.textContent||'').trim().slice(0,18)+'|'+(b.className||'').slice(0,28)+'|'+(b.getAttribute('aria-label')||'')))`))

// 进入第一条笔记详情
const opened = await ev(`(function(){
  var cards = Array.from(document.querySelectorAll('[class*="card"]'));
  if (!cards.length) return 'NO_CARDS';
  cards[0].click(); return 'opened:' + cards[0].className;
})()`)
console.log('\n=== open first card ->', opened)
await sleep(3000)
console.log('hash =', await ev(`location.hash`))
console.log('\n=== detail page ===')
console.log(await ev(`JSON.stringify({
  textareas: Array.from(document.querySelectorAll('textarea')).map(t => (t.placeholder||'')+'|'+(t.className||'')+'|len='+t.value.length),
  inputs: Array.from(document.querySelectorAll('input')).filter(i=>i.type!=='file').map(i => i.type+'|'+(i.placeholder||'')+'|'+(i.className||'')),
  buttons: Array.from(document.querySelectorAll('button')).map(b => (b.textContent||'').trim().slice(0,18)+(b.disabled?'[off]':'[on]'))
})`))
ws.close(); process.exit(0)
