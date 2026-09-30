/**
 * 校验 CDP Fetch 域在本机 WebView 上是否真的可用（拦截模式能否命中）。
 * 在动 stream-abort-probe 的结论之前必须先证明拦截本身有效。
 */
import { execFileSync } from 'node:child_process'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9224'
const adb = (args) => execFileSync(ADB, args, { encoding: 'utf8', timeout: 60000 })
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) throw new Error('app not running')
try { adb(['-s', SERIAL, 'forward', '--remove', `tcp:${PORT}`]) } catch { /* noop */ }
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:webview_devtools_remote_${pid}`])

const list = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()
const page = list.find((t) => t.type === 'page' && t.webSocketDebuggerUrl)

const ws = new WebSocket(page.webSocketDebuggerUrl)
const pending = new Map()
const paused = []
let msgId = 0
ws.addEventListener('message', (ev) => {
  const m = JSON.parse(ev.data)
  if (m.id && pending.has(m.id)) {
    const { resolve, reject } = pending.get(m.id)
    pending.delete(m.id)
    m.error ? reject(new Error(m.error.message)) : resolve(m.result)
  } else if (m.method === 'Fetch.requestPaused') {
    paused.push(m.params)
  }
})
await new Promise((res, rej) => {
  ws.addEventListener('open', res, { once: true })
  setTimeout(() => rej(new Error('ws open timeout')), 15000)
})
const send = (method, params = {}) => new Promise((resolve, reject) => {
  const id = ++msgId
  pending.set(id, { resolve, reject })
  ws.send(JSON.stringify({ id, method, params }))
  setTimeout(() => { if (pending.has(id)) { pending.delete(id); reject(new Error(`${method} timeout`)) } }, 20000)
})
const evaluate = async (expression) => {
  const r = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true, allowUnsafeEvalBlocklistedAPI: true })
  return r.result?.value
}

await send('Fetch.enable', { patterns: [{ urlPattern: '*__cdp_probe__*', requestStage: 'Request' }] })
console.log('Fetch.enable（模式 *__cdp_probe__*）已开启')

// 注意：这里**不能 await 页面里的 fetch**。
// 一旦拦截生效，fetch 会被 CDP 挂起、Promise 永不落定，
// await 它会让本脚本自己的 Runtime.evaluate 先超时（曾经踩过）。
// 改为发起后立刻返回，由 CDP 侧观察 requestPaused。
const pageResult = await evaluate(`
  window.__probeResult = 'pending';
  fetch('http://127.0.0.1:8088/__cdp_probe__/ping')
    .then(r => { window.__probeResult = 'status ' + r.status; })
    .catch(e => { window.__probeResult = 'fetch-failed: ' + e.message; });
  'fired';
`)
console.log('页面侧 fetch 已发起:', pageResult)

for (let i = 0; i < 20 && paused.length === 0; i++) await sleep(150)

if (paused.length) {
  console.log('✅ 拦截命中:', paused[0].request.url)
  for (const p of paused) {
    await send('Fetch.failRequest', { requestId: p.requestId, errorReason: 'Aborted' }).catch(() => {})
  }
} else {
  console.log('❌ 未命中任何请求 —— 本 WebView 上 Fetch 域不可用或模式不匹配')
}
await send('Fetch.disable').catch(() => {})
ws.close()
process.exitCode = paused.length ? 0 : 1
