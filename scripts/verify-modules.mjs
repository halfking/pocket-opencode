/**
 * 批量模块可达性验证（CDP，不依赖截图）。
 * 对每个路由：导航 → 等待渲染 → 记录最终 URL / 可见文本 / 控制台错误。
 * 重点检测「被重定向回 /login」——这正是 login-gated 模块未打通的信号。
 */
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || 'emulator-5554'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = '9224'

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// 等设备 + 选当前 App PID 对应的 WebView socket
async function ensureDevice() {
  for (let i = 0; i < 10; i++) {
    const devs = execFileSync(ADB, ['devices'], { encoding: 'utf8' })
    if (devs.includes(`${SERIAL}\tdevice`)) return
    await sleep(3000)
  }
  throw new Error(`device ${SERIAL} unreachable`)
}
await ensureDevice()

const pid = execFileSync(ADB, ['-s', SERIAL, 'shell', `pidof ${PKG}`], { encoding: 'utf8' }).trim().split(/\s+/)[0]
const socks = execFileSync(ADB, ['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`], { encoding: 'utf8' })
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
const sock = socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]
execFileSync(ADB, ['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${sock}`], { encoding: 'utf8' })

const targets = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()
const page = targets.find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE'); process.exit(1) }

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
let errs = []
const send = (method, params = {}) =>
  new Promise((res) => {
    const i = ++id
    const timer = setTimeout(() => { pending.delete(i); res({ __timeout: true }) }, 12000)
    pending.set(i, (v) => { clearTimeout(timer); res(v) })
    ws.send(JSON.stringify({ id: i, method, params }))
  })

ws.addEventListener('message', (ev) => {
  const m = JSON.parse(ev.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); return }
  if (m.method === 'Runtime.exceptionThrown') {
    errs.push('EXC ' + (m.params.exceptionDetails?.exception?.description || m.params.exceptionDetails?.text || '').split('\n')[0])
  }
  if (m.method === 'Runtime.consoleAPICalled' && (m.params.type === 'error' || m.params.type === 'warning')) {
    const txt = (m.params.args || []).map((a) => a.value ?? a.description ?? '').join(' ').slice(0, 120)
    if (txt) errs.push(m.params.type.toUpperCase() + ' ' + txt)
  }
})
await new Promise((r) => ws.addEventListener('open', r))
await send('Runtime.enable')
await send('Page.enable')

const ROUTES = [
  ['笔记 Notes', '/notes'],
  ['邮箱 Email', '/email'],
  ['财务 Finance', '/finance'],
  ['密码箱 Vault', '/vault'],
  ['闪卡 Flashcards', '/flashcards'],
  ['PKM', '/pkm/today'],
  ['本地智能体 LocalAgent', '/local-agent'],
  ['市场 Market', '/marketplace/skills'],
  ['费用配额 Cost', '/cost'],
  ['网关 Gateway', '/gateway'],
  ['实例 Instances', '/instances'],
  ['任务 Tasks', '/tasks'],
  ['会话 Sessions', '/sessions'],
]

const rows = []
for (const [name, route] of ROUTES) {
  errs = []
  await send('Runtime.evaluate', { expression: `location.hash = '#${route}'` })
  await sleep(2600)
  const r = await send('Runtime.evaluate', {
    expression: `JSON.stringify({
      url: location.href,
      hash: location.hash,
      text: (document.body.innerText || '').replace(/\\s+/g,' ').trim().slice(0,140),
      nodes: document.querySelectorAll('main,section,article,[class*=view],[class*=View]').length,
      empty: (document.body.innerText||'').trim().length
    })`,
    returnByValue: true,
  })
  let d = {}
  try { d = JSON.parse(r.result?.result?.value || '{}') } catch {}
  const gated = /#\/login/.test(d.url || '')
  const textLen = d.empty || 0
  const uniqueErrs = [...new Set(errs)].slice(0, 3)
  rows.push({
    模块: name, 目标: route,
    实际URL: (d.url || '?').replace('http://localhost', ''),
    渲染文本长度: textLen,
    判定: gated ? 'LOGIN_GATED' : textLen > 20 ? 'RENDERED' : 'BLANK',
    控制台: uniqueErrs.join(' ;; ').slice(0, 160) || '-',
  })
}

console.log(`\n=== 模块可达性验证（${SERIAL}，已登录+主密码已建）===`)
console.table(rows)
const gated = rows.filter((r) => r.判定 === 'LOGIN_GATED').map((r) => r.模块)
const blank = rows.filter((r) => r.判定 === 'BLANK').map((r) => r.模块)
console.log('LOGIN_GATED:', gated.length ? gated.join(', ') : '(none)')
console.log('BLANK      :', blank.length ? blank.join(', ') : '(none)')
ws.close()
process.exit(0)
