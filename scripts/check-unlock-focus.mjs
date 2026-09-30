/** 读解锁页密码框是否真的有内容（只读，不改状态）——用来判定 tap/input 是否生效，
 *  而不是看 Maestro 报没报 COMPLETED。坐标点击没有结果校验，COMPLETED 不代表生效。 */
import { execFileSync } from 'node:child_process'
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = '9412'
const adb = (a, t = 60000) => execFileSync(ADB, ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const pid = adb(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`]).split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0; const pending = new Map()
ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) } })
await new Promise((r) => ws.addEventListener('open', r))
const ev = async (x, ms = 8000) => {
  const i = ++id
  const v = await new Promise((r) => { const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms); pending.set(i, (y) => { clearTimeout(t); r(y) }); ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } })) })
  return v?.__t ? '__frozen__' : v?.result?.value
}
console.log('hash =', await ev('location.hash'))
console.log('焦点元素 =', await ev(`(function(){var a=document.activeElement;return a?a.tagName+'.'+String(a.className||'').slice(0,30):'none'})()`))
console.log(await ev(`JSON.stringify(Array.prototype.slice.call(document.querySelectorAll('input')).map(function(i){
  return {type:i.type, ph:i.placeholder||'', len:String(i.value||'').length, focused:i===document.activeElement};
}), null, 1)`))
console.log('「解锁」按钮 disabled =', await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim()==='解锁'});return b?!!b.disabled:'NO_BTN'})()`))
adb(['forward', '--remove', `tcp:${PORT}`])
process.exit(0)
