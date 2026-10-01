// diag-webview-https-fail.mjs — 定位「WebView fetch 生产失败、原生 curl 正常」的真因。
//
// 上一轮现象：
//   设备原生 curl  → https://pocket.itestu.cn/api/tasks  200 + 正确 JSON
//   WebView 内 fetch → 同一个 URL，全部 "Failed to fetch"
//
// 「Failed to fetch」是浏览器把**底层原因吞掉**后的统一文案，可能是：
//   (A) CORS：Capacitor WebView 的 origin 是 https://localhost，生产没给这个 origin
//       发 Access-Control-Allow-Origin。原生 curl 不执行同源策略，所以它通。
//   (B) TLS/证书链：WebView 的信任锚与系统 curl 不同。
//   (C) 混合内容 / cleartext 策略拦截。
//   (D) 探针本身坏了（比如页面 CSP 拦了 connect-src）。
//
// 判别办法：**同源对照**。在同一个 WebView 里同时打
//   1) 本地 http://127.0.0.1:8088（adb reverse，已知通）
//   2) 生产 https://pocket.itestu.cn
// 若 1 通 2 不通 ⇒ 排除 (D)「探针坏了」，问题在 (A)/(B)/(C) 之中；
// 再用 no-cors 模式与 OPTIONS 预检把 (A) 和 (B)/(C) 分开。
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9473'
const adb = (a, t = 25000) => execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const adbSoft = (a) => { try { return adb(a, 8000) } catch { return '' } }
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const pid = adbSoft(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adbSoft(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adbSoft(['forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const pages = await (await fetch(`http://127.0.0.1:${PORT}/json/list`, { signal: AbortSignal.timeout(10000) })).json()
const page = pages.find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE_TARGET'); process.exit(4) }
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0; const pending = new Map()
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
const opened = await Promise.race([
  new Promise((r) => ws.addEventListener('open', () => r(true))),
  new Promise((r) => setTimeout(() => r(false), 10000)),
])
if (!opened) { console.log('CDP_OPEN_TIMEOUT'); process.exit(5) }
const ev = async (x, ms = 60000) => {
  const i = ++id
  const v = await new Promise((r) => {
    const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms)
    pending.set(i, (y) => { clearTimeout(t); r(y) })
    ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } }))
  })
  if (v?.__t) return { value: null, err: 'TIMEOUT' }
  if (v?.exceptionDetails) return { value: null, err: String(v.exceptionDetails.exception?.description || v.exceptionDetails.text).slice(0, 200) }
  return { value: v?.result?.value, err: '' }
}

const r = await ev(`(async () => {
  const to = (p, ms) => Promise.race([p, new Promise((r) => setTimeout(() => r('__TO__'), ms))])
  const out = { origin: location.origin, href: location.href, results: [] }
  const probe = async (label, url, init) => {
    try {
      const res = await to(fetch(url, init), 15000)
      const ct = res.headers.get('content-type') || ''
      // 跨域时 CORS 头对脚本是不可见的（除非 ACAO:*），看不到本身就是信号
      const acao = res.headers.get('access-control-allow-origin')
      let bodyLen = -1
      try { bodyLen = (await to(res.text(), 8000)).length } catch (e) { bodyLen = -2 }
      out.results.push({ label, ok: true, status: res.status, contentType: ct.slice(0, 50), acaoVisible: acao, bodyLen })
    } catch (e) {
      out.results.push({ label, ok: false, err: String(e && e.message || e).slice(0, 120) })
    }
  }
  // 对照 1：本地（adb reverse，同源无关但同 http 栈）
  await probe('local  http://127.0.0.1:8088/api/tasks', 'http://127.0.0.1:8088/api/tasks')
  // 对照 2：生产 https
  await probe('prod   https /api/tasks', 'https://pocket.itestu.cn/api/tasks')
  // 判别 3：no-cors —— 若请求真的发出且服务器响应，浏览器只是不给脚本读
  await probe('prod   no-cors', 'https://pocket.itestu.cn/api/tasks', { mode: 'no-cors' })
  // 判别 4：预检 OPTIONS —— 直接看服务器认不认跨域
  try {
    const res = await to(fetch('https://pocket.itestu.cn/api/tasks', {
      method: 'OPTIONS',
      headers: { Origin: location.origin, 'Access-Control-Request-Method': 'GET', 'Access-Control-Request-Headers': 'authorization' },
    }), 15000)
    out.results.push({ label: 'prod   OPTIONS preflight', ok: true, status: res.status,
      acao: res.headers.get('access-control-allow-origin'),
      acam: res.headers.get('access-control-allow-methods'),
      acah: res.headers.get('access-control-allow-headers') })
  } catch (e) {
    out.results.push({ label: 'prod   OPTIONS preflight', ok: false, err: String(e && e.message || e).slice(0, 120) })
  }
  return JSON.stringify(out)
})()`)
if (r.err) { console.log('探针失败: ' + r.err); process.exit(6) }
const d = JSON.parse(r.value)
console.log(`页面 origin = ${d.origin}`)
console.log(`页面 href   = ${d.href}`)
console.log('')
for (const s of d.results) {
  if (!s.ok) { console.log(`  ❌ ${s.label.padEnd(38)} ${s.err}`); continue }
  if (s.status === undefined) { console.log(`  ${s.label.padEnd(38)} status=${s.status} ACAO=${s.acao} ACAM=${s.acam} ACAH=${s.acah}`); continue }
  console.log(`  ${String(s.status).padEnd(4)} ${s.label.padEnd(38)} ct=${s.contentType} 脚本可见ACAO=${s.acaoVisible} bodyLen=${s.bodyLen}`)
}
console.log('')
console.log('=== 判读 ===')
const local = d.results.find((s) => s.label.includes('local'))
const prod = d.results.find((s) => s.label === 'prod   https /api/tasks')
const nocors = d.results.find((s) => s.label === 'prod   no-cors')
if (local?.ok && !prod?.ok) {
  console.log('  ✅ 探针本身是好的（同源本地通），失败只发生在生产跨域 https 上。')
  if (nocors?.ok) {
    console.log('     no-cors 能拿到响应 ⇒ 请求确实发出去了，服务器有响应，')
    console.log('     只是**脚本读不到** ⇒ 典型的 CORS 缺 ACAO。')
  } else {
    console.log('     no-cors 也失败 ⇒ 请求根本没到服务器，偏向 TLS 信任链 / 混合内容 / CSP connect-src。')
  }
} else if (!local?.ok) {
  console.log('  ⚠️  连本地都失败 ⇒ 先怀疑探针或 adb reverse，不要归因到生产。')
} else {
  console.log('  生产跨域 https 在 WebView 内可读 ⇒ 与宿主 curl 结果一致，无特殊问题。')
}
ws.close()
process.exit(0)
