/**
 * monitor-flashcard-save.mjs —— 在 Maestro 跑 _probe-flashcard-save.yaml 期间
 * 并行监测 WebView：记录 DOM click 与 fetch，看「点保存」那一步到底发生了什么。
 *
 * 与 diag-flashcard-save-click.mjs 的区别：那个是**自己动手点**（CDP/adb），
 * 这个是**旁观**（Maestro 点的时候我在旁边看），才能判定 Maestro 的 tap
 * 究竟有没有产生 click、点到了哪个按钮、有没有发出 POST。
 */
import { openCdp } from './lib/adb-cdp.mjs'

const PKG = process.env.POCKET_APP_ID || 'com.kaixuan.opencode.pocket'
const SECONDS = Number(process.env.MONITOR_SECONDS || 180)
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const c = await openCdp({ pkg: PKG })
let id = 0
const pending = new Map()
c.ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id) }
})
const ev = async (expression, ms = 10000) => {
  const i = ++id
  const raw = await new Promise((r) => {
    const t = setTimeout(() => { pending.delete(i); r(null) }, ms)
    pending.set(i, (y) => { clearTimeout(t); r(y) })
    c.ws.send(JSON.stringify({
      id: i, method: 'Runtime.evaluate',
      params: { expression, returnByValue: true, awaitPromise: true },
    }))
  })
  if (!raw) return '__timeout__'
  return raw.result?.result?.value
}

await ev(`(function(){
  window.__mon = { clicks: [], fetches: [], marks: [] };
  document.addEventListener('click', function(e){
    var t = e.target;
    var label = (t && (t.textContent || t.getAttribute('aria-label') || t.className) || '').toString().slice(0,60);
    window.__mon.clicks.push({ tag: t && t.tagName, label: label, trusted: e.isTrusted, cls: t && t.className });
  }, true);
  var of = window.fetch;
  window.fetch = function(input, init){
    var url = typeof input === 'string' ? input : (input && input.url);
    window.__mon.fetches.push({ url: String(url).slice(0,100), method: (init && init.method) || 'GET' });
    return of.apply(this, arguments);
  };
  return 'armed';
})()`)
console.log('监测已就绪，等待 Maestro…')

let last = 0
const deadline = Date.now() + SECONDS * 1000
while (Date.now() < deadline) {
  await sleep(1500)
  const n = (await ev('window.__mon ? window.__mon.clicks.length : 0')) || 0
  if (n !== last) {
    last = n
    console.log(`\n[t+${Math.round((SECONDS * 1000 - (deadline - Date.now())) / 1000)}s] hash=${await ev('location.hash')}`)
    console.log('  clicks =', JSON.stringify(await ev('window.__mon.clicks')))
    console.log('  fetches=', JSON.stringify(await ev('window.__mon.fetches')))
  }
}
console.log('\n=== 最终 ===')
console.log('hash    =', await ev('location.hash'))
console.log('clicks  =', JSON.stringify(await ev('window.__mon.clicks')))
console.log('fetches =', JSON.stringify(await ev('window.__mon.fetches')))
console.log('error节点=', await ev(`(function(){var p=document.querySelector('p.error');return p? p.textContent : '(无)'})()`))
console.log('Save 按钮=', JSON.stringify(await ev(`(function(){
  var bs = Array.prototype.slice.call(document.querySelectorAll('button'));
  return bs.filter(function(b){ return /^(保存|Save)$/.test((b.textContent||'').trim()); })
    .map(function(b){ var r=b.getBoundingClientRect(); return {cls:b.className,type:b.type,y:Math.round(r.top),x:Math.round(r.left)}; });
})()`)))
c.close()
process.exit(0)
