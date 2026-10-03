// 建卡页「渲染不出任何表单控件」的定位：把 body 文本、根节点结构、
// 控制台错误、网络失败请求一并取出来。
// 2026-10-04 00:15 实测：flow 失败截图是**纯空白页**，
// 且此时 hash 已是 #/flashcards/new?deckId=…，但 document 里
// textarea 数量为 0、保存按钮不存在 ⇒ 不是「点错控件」，是**没渲染**。
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9426'
const adb = (a, t = 60000) =>
  execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, ...a], {
    encoding: 'utf8',
    timeout: t,
    maxBuffer: 33554432,
  })
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const pid = adb(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) {
  console.log('APP_NOT_RUNNING')
  process.exit(2)
}
const socks = adb(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/)
  .map((l) => l.trim().replace('@', ''))
  .filter(Boolean)
adb(['forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
if (!page) {
  console.log('NO_PAGE_TARGET')
  process.exit(3)
}
console.log('page url =', page.url, '| title =', page.title)

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
const consoleErrs = []
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) {
    pending.get(m.id)(m.result)
    pending.delete(m.id)
  }
  if (m.method === 'Log.entryAdded' && m.params?.entry?.level === 'error') {
    consoleErrs.push(m.params.entry.text)
  }
  if (m.method === 'Runtime.consoleAPICalled' && m.params?.type === 'error') {
    consoleErrs.push((m.params.args || []).map((a) => a.value ?? a.description ?? '').join(' '))
  }
})
await new Promise((r) => ws.addEventListener('open', r))
const raw = (method, params) =>
  new Promise((r) => {
    const i = ++id
    pending.set(i, r)
    ws.send(JSON.stringify({ id: i, method, params }))
  })
const ev = async (x, ms = 20000) => {
  const v = await new Promise((r) => {
    const i = ++id
    const t = setTimeout(() => {
      pending.delete(i)
      r({ __t: 1 })
    }, ms)
    pending.set(i, (y) => {
      clearTimeout(t)
      r(y)
    })
    ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } }))
  })
  if (v?.__t) return { value: undefined, err: '__frozen__' }
  if (v?.exceptionDetails) return { value: undefined, err: String(v.exceptionDetails.exception?.description || v.exceptionDetails.text).slice(0, 300) }
  return { value: v?.result?.value, err: '' }
}

await raw('Log.enable', {})
await raw('Runtime.enable', {})
await raw('Network.enable', {})

const failedReqs = []
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.method === 'Network.responseReceived') {
    const r = m.params?.response || {}
    if (r.status >= 400) {
      failedReqs.push({ status: r.status, url: (r.url || '').replace(/\?.*$/, '') })
    }
  }
})

const { value, err } = await ev(`(() => {
  const app = document.querySelector('#app') || document.body.firstElementChild
  const walk = (el, d) => {
    if (!el || d > 3) return null
    const kids = Array.from(el.children || []).slice(0, 8).map(c => walk(c, d + 1))
    return { tag: el.tagName, id: el.id || '', cls: (el.className||'').toString().slice(0,50),
             nchild: (el.children||[]).length, kids: kids.filter(Boolean) }
  }
  return {
    hash: location.hash,
    readyState: document.readyState,
    bodyText: (document.body.innerText || '').slice(0, 300),
    bodyChildCount: document.body.children.length,
    appHtmlLen: (app ? app.innerHTML.length : 0),
    tree: walk(app, 0),
    counts: {
      textarea: document.querySelectorAll('textarea').length,
      input: document.querySelectorAll('input').length,
      button: document.querySelectorAll('button').length,
    },
  }
})()`)

if (err) console.log('EVAL_ERR:', err)
else console.log(JSON.stringify(value, null, 2))
console.log('\n=== console errors ===')
console.log(consoleErrs.length ? consoleErrs.slice(0, 20).join('\n') : '(none captured)')

// 复现一次导航，把失败请求的 URL 抓下来
console.log('\n=== 重新导航以捕获失败请求 ===')
await ev(`location.hash = '#/flashcards'`)
await sleep(1500)
await ev(`location.hash = '#/flashcards/new?deckId=deck_2ae984b23d864fb5d35adb6545e3018d'`)
await sleep(4000)
for (const r of failedReqs) console.log(`  HTTP ${r.status}  ${r.url}`)
if (!failedReqs.length) console.log('  (本次导航未捕获到 4xx/5xx)')

const { value: after } = await ev(`(() => {
  const main = document.querySelector('#main')
  return { hash: location.hash, mainChildCount: main ? main.children.length : -1,
           textarea: document.querySelectorAll('textarea').length,
           bodyText: (document.body.innerText||'').slice(0,200) }
})()`)
console.log('\n=== 导航后 ===')
console.log(JSON.stringify(after, null, 2))
ws.close()
