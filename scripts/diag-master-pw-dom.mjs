// 列出「创建主密码」弹窗里**所有** input/button 的真实属性。
// 目的：确认我之前是不是**填错了输入框** —— 页面上很可能有两个密码框
// （「主密码」+「确认主密码」），而「主密码至少需要 8 位」的提示属于其中之一。
// 实测现象：填了一个框、确认按钮可点、点了之后弹窗不消失。
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9426'
const adb = (a, t = 60000) =>
  execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, ...a], {
    encoding: 'utf8', timeout: t, maxBuffer: 33554432,
  })

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
const res = await new Promise((r) => {
  const i = ++id
  pending.set(i, r)
  ws.send(JSON.stringify({
    id: i, method: 'Runtime.evaluate',
    params: {
      returnByValue: true,
      expression: `(() => {
        const nodes = Array.from(document.querySelectorAll('input, button'))
        return nodes.map((e, idx) => ({
          idx,
          tag: e.tagName,
          type: e.type || '',
          placeholder: e.placeholder || '',
          valueMasked: (e.value || '').replace(/./g, '*'),
          text: (e.textContent || '').trim().slice(0, 12),
          disabled: !!e.disabled,
          cls: (e.className || '').toString().slice(0, 40),
          // 最近的可见 label / 提示文本
          nearLabel: (() => {
            const wrap = e.closest('label, div')
            return wrap ? (wrap.textContent || '').replace(/\\s+/g, ' ').trim().slice(0, 60) : ''
          })(),
        }))
      })()`,
    },
  }))
})
console.log(JSON.stringify(res?.result?.value, null, 1))
ws.close()
