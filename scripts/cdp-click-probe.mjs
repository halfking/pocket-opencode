// 探查：闪卡/任务按钮的点击为何无效——事件绑定类型？弹窗结构？
import { execFileSync } from 'node:child_process'
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = '9237'
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
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true }))?.result?.value

// 完整指针事件序列派发
const FULL_CLICK = (matcher) => `(function(){
  function find(){
    return Array.from(document.querySelectorAll('button')).find(${matcher});
  }
  var b = find();
  if (!b) return 'NO_BUTTON';
  var r = b.getBoundingClientRect();
  var base = { bubbles: true, cancelable: true, composed: true, view: window,
               clientX: r.left + r.width/2, clientY: r.top + r.height/2, button: 0, buttons: 1, pointerId: 1, isPrimary: true };
  ['pointerdown','mousedown','pointerup','mouseup','click'].forEach(function(t){
    var Ctor = t.indexOf('pointer') === 0 ? PointerEvent : MouseEvent;
    b.dispatchEvent(new Ctor(t, base));
  });
  return 'dispatched:' + (b.textContent||'').trim().slice(0,16);
})()`

console.log('=== flashcards ===')
await ev(`location.hash = '#/flashcards'`); await sleep(3000)
console.log('buttons html:', await ev(`JSON.stringify(Array.from(document.querySelectorAll('button')).map(b => ({ t:(b.textContent||'').trim().slice(0,14), c:b.className, tag:b.tagName })))`))
console.log('full-click 新建卡组 ->', await ev(FULL_CLICK(`b => (b.textContent||'').indexOf('新建卡组') >= 0`)))
await sleep(2500)
console.log('after: inputs =', await ev(`JSON.stringify(Array.from(document.querySelectorAll('input')).map(i=>i.type+'|'+(i.placeholder||'')))`))
console.log('after: dialogs =', await ev(`JSON.stringify(Array.from(document.querySelectorAll('[class*="modal"],[class*="dialog"],[class*="sheet"],[class*="overlay"],[role="dialog"]')).map(d=>d.className))`))
console.log('after: body head =', ((await ev(`document.body.innerText.replace(/\\s+/g,' ')`))||'').slice(0,200))

console.log('\n=== flashcard card page (card selector) ===')
await ev(`location.hash = '#/flashcards/new'`); await sleep(3000)
console.log('保存 btn detail:', await ev(`(function(){
  var b = Array.from(document.querySelectorAll('button')).find(x => (x.textContent||'').indexOf('保存') >= 0);
  if (!b) return 'NO_BTN';
  return JSON.stringify({ disabled: b.disabled, cls: b.className, html: b.outerHTML.slice(0,220) });
})()`))
console.log('卡组 selector el:', await ev(`(function(){
  var b = Array.from(document.querySelectorAll('button')).find(x => (x.textContent||'').indexOf('卡组') >= 0);
  return b ? b.outerHTML.slice(0,200) : 'NONE';
})()`))

console.log('\n=== tasks ===')
await ev(`location.hash = '#/tasks'`); await sleep(3000)
console.log('full-click + 新任务 ->', await ev(FULL_CLICK(`b => (b.textContent||'').indexOf('新任务') >= 0`)))
await sleep(2500)
console.log('after: inputs =', await ev(`JSON.stringify(Array.from(document.querySelectorAll('input,textarea')).map(i=>i.tagName+'|'+(i.placeholder||'')))`))
console.log('after: dialogs =', await ev(`JSON.stringify(Array.from(document.querySelectorAll('[class*="modal"],[class*="dialog"],[class*="sheet"],[class*="overlay"],[role="dialog"]')).map(d=>d.className))`))
console.log('after: body head =', ((await ev(`document.body.innerText.replace(/\\s+/g,' ')`))||'').slice(0,200))
ws.close(); process.exit(0)
