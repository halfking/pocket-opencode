// 单独验证「创建主密码」弹窗能否用 CDP 处理完，不跑整条 flow。
//
// 背景（2026-10-04 00:4x）：maestro-run.mjs 报「登录后仍停在登录页」，
// 但登录其实成功（pocket_token 291 字符）。挡住路由的是一个
// 「创建主密码」弹窗。第一版处理脚本只发 input 事件，弹窗仍显示
// 「主密码至少需要 8 位」⇒ 值没进 v-model。
//
// 本脚本对比几种填值手法，看哪一种能让弹窗真正消失 ——
// 写死一种手法然后跑整条 flow 去碰运气，太贵。
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9426'
const MASTER = process.env.POCKET_MASTER || '12345678'
const adb = (a, t = 60000) =>
  execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, ...a], {
    encoding: 'utf8', timeout: t, maxBuffer: 33554432,
  })
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const pid = adb(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
await new Promise((r) => ws.addEventListener('open', r))
const ev = async (x, ms = 15000) => {
  const i = ++id
  const v = await new Promise((r) => {
    const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms)
    pending.set(i, (y) => { clearTimeout(t); r(y) })
    ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } }))
  })
  if (v?.__t) return '(frozen)'
  if (v?.exceptionDetails) return 'ERR:' + String(v.exceptionDetails.exception?.description || '').slice(0, 160)
  return v?.result?.value
}

const state = async () => {
  const s = await ev(`(function(){
    var b = (document.body.innerText || '')
    var dlg = b.indexOf('创建主密码') >= 0
    var btns = Array.from(document.querySelectorAll('button'))
      .filter(function(x){ return /^(确认|确定)$/.test((x.textContent||'').trim()) })[0]
    return JSON.stringify({
      dialog: dlg,
      confirmDisabled: btns ? !!btns.disabled : null,
      snippet: b.replace(/\\s+/g,' ').slice(0, 160),
    })
  })()`)
  try { return JSON.parse(s) } catch { return { raw: String(s).slice(0, 200) } }
}

console.log('=== 初始状态 ===')
console.log(JSON.stringify(await state()))

// 手法：按 placeholder 精确填**两个**密码框（主密码 + 再次输入）
console.log('\n=== 手法 A：按 placeholder 填两个密码框 ===')
const rA = await ev(`(function(){
  try {
    var inputs = Array.from(document.querySelectorAll('input'))
    var byPh = function(re){ return inputs.filter(function(i){ return re.test(i.placeholder||'') })[0] }
    // ⚠️ placeholder 是「主密码（至少 8 位）」，**全角括号**，
    // 用 \\s* / 半角括号都匹配不到。改成「弹窗内前两个 password 框」。
    var pwInputs = inputs.filter(function(i){ return (i.type||'') === 'password' })
    var p1 = pwInputs[0]
    var p2 = pwInputs[1]
    if (!p1) return 'no-input1'
    if (!p2) return 'no-input2'
    var want = ${JSON.stringify(MASTER)}
    var setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype,'value').set
    setter.call(p1, ''); setter.call(p2, '')
    // ⚠️ 一次性 setter 只让 v-model 收到一次「最终值」，实测第二个框仍判
    // 「两次输入的主密码不一致」⇒ 两个框的响应式状态不同步。
    // 改成**逐字符**派发（keydown + input），让每一步都进 Vue 的响应式链。
    var typeInto = function(el, s){
      el.focus()
      for (var i=0;i<s.length;i++) {
        var v = el.value + s[i]
        setter.call(el, v)
        el.dispatchEvent(new KeyboardEvent('keydown', { key: s[i], bubbles: true }))
        el.dispatchEvent(new Event('input', { bubbles: true }))
        el.dispatchEvent(new KeyboardEvent('keyup', { key: s[i], bubbles: true }))
      }
      el.dispatchEvent(new Event('change', { bubbles: true }))
      el.dispatchEvent(new FocusEvent('blur', { bubbles: true }))
    }
    typeInto(p1, want)
    typeInto(p2, want)
    if (p1.value !== want) return 'set1-failed'
    if (p2.value !== want) return 'set2-failed'
    return 'filled2:' + p1.value.length
  } catch (e) { return 'err:' + String(e).slice(0,120) }
})()`)
console.log('  ', rA)
await sleep(1200)
console.log('  状态:', JSON.stringify(await state()))

// 点确认
console.log('\n=== 点「确认」 ===')
const rB = await ev(`(function(){
  try {
    var btns = Array.from(document.querySelectorAll('button'))
    var b = btns.filter(function(x){ return /^(确认|确定|OK|Confirm)$/.test((x.textContent||'').trim()) })[0]
    if (!b) return 'no-confirm'
    if (b.disabled) return 'confirm-disabled'
    b.click(); return 'clicked'
  } catch (e) { return 'err:' + String(e).slice(0,120) }
})()`)
console.log('  ', rB)
await sleep(2500)
console.log('  状态:', JSON.stringify(await state()))
ws.close()
