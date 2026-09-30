// 复核：#/study 页面上到底有没有把 i18n key 原样渲染出来？
// 上一轮 audit-list-views 的输出里出现了 "study.deue.all" 字样，
// 但那可能是 PowerShell 控制台把 UTF-8 转 ANSI 造成的显示损坏（假警报）。
// 这次用 JS 侧布尔判定 + 显式 hex 编码回传，绕开控制台编码。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9375'
const MASTER = process.env.POCKET_MASTER || 'PocketTest2026'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
const socks = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
await new Promise((r) => ws.addEventListener('open', r))
const send = (method, params = {}) =>
  new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method, params })) })
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true, awaitPromise: true }))?.result?.value
await send('Runtime.enable')

if (await ev('!!document.querySelector(\'input[placeholder*="主密码"]\')')) {
  await ev(`(function(){var e=document.querySelector('input[placeholder*="主密码"]');var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(e),'value').set;s.call(e,${JSON.stringify(MASTER)});e.dispatchEvent(new Event('input',{bubbles:true}));return 1})()`)
  await sleep(700)
  await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(x=>(x.textContent||'').trim()==='解锁');if(b)b.click();return 1})()`)
  await sleep(5000)
}
await sleep(4000)
await ev(`location.hash='#/study'`)
await sleep(5000)

const r = await ev(`(function(){
  var txt = (document.querySelector('.app-layout')||document.body).textContent || '';
  // 用 hex 回传，绕开控制台编码
  function hex(s){ return Array.prototype.map.call(s, function(c){ return c.charCodeAt(0).toString(16).padStart(4,'0'); }).join(''); }
  var hasKeyLiteral = /study\\.[a-zA-Z.]+/.test(txt);
  var m = txt.match(/study\\.[a-zA-Z.]+/);
  var btn = Array.prototype.slice.call(document.querySelectorAll('button.link-btn')).map(function(b){return (b.textContent||'').trim()});
  return JSON.stringify({
    hasKeyLiteral: hasKeyLiteral,
    matchedKey: m ? m[0] : null,
    linkBtnTexts: btn,
    linkBtnHex: btn.map(hex),
    locale: localStorage.getItem('pocket_locale') || '(未设)',
    htmlLang: document.documentElement.lang
  });
})()`)
console.log(r)
ws.close()
process.exit(0)
