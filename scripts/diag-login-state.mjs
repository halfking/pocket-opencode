// preflight 报「登录后仍停在登录页（30s）」，但后端 /api/auth/login 实测
// HTTP 200、后端日志也有 App 的 WebSocket 连接。
// 本脚本用 CDP 读 App 此刻的 hash、token 长度、以及登录页是否真的还在屏幕上，
// 用来区分三种情况：
//   (a) 真的没登录成功（token 为空、hash 是 #/login）
//   (b) 登录成功了但 hash 跳转比 preflight 的等待慢（token 在、hash 已是业务页）
//   (c) 登录页 DOM 还在，但其实是别的遮罩/弹窗
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
if (!page) { console.log('NO_PAGE'); process.exit(3) }

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
await new Promise((r) => ws.addEventListener('open', r))
const v = await new Promise((res) => {
  const i = ++id
  const t = setTimeout(() => res({ __t: 1 }), 15000)
  pending.set(i, (y) => { clearTimeout(t); res(y) })
  ws.send(JSON.stringify({
    id: i, method: 'Runtime.evaluate',
    params: {
      returnByValue: true,
      expression: `(() => {
        const tok = localStorage.getItem('pocket_token') || localStorage.getItem('token') || ''
        const all = Object.keys(localStorage)
        const main = document.querySelector('#main')
        return {
          hash: location.hash,
          tokenLen: tok.length,
          lsKeys: all,
          mainChildCount: main ? main.children.length : -1,
          bodyText: (document.body.innerText || '').replace(/\\s+/g, ' ').slice(0, 300),
          hasPasswordField: !!document.querySelector('input[type=password]'),
          dialogs: Array.from(document.querySelectorAll('[role=dialog],dialog,.modal,.sheet'))
            .map(e => ((e.textContent||'').replace(/\\s+/g,' ')).slice(0,80)).slice(0,5),
        }
      })()`,
    },
  }))
})

if (v?.__t) { console.log('FROZEN'); process.exit(4) }
const out = v?.result?.value
console.log(JSON.stringify(out, null, 2))
console.log('\n=== 判定 ===')
if (out && out.tokenLen > 0 && !out.hash.includes('/login')) {
  console.log('⇒ 登录其实**成功**了，是 preflight 的等待判据误报')
} else if (out && out.hasPasswordField) {
  console.log('⇒ 真的还停在登录页')
} else {
  console.log('⇒ 状态不明确，需看上面的 hash/token/遮罩')
}
ws.close()
