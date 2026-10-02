// 无凭据的 https 设备侧探测：只验「真机 WebView 能不能走到生产并拿到 JSON」。
//
// 为什么拆出来：scripts/verify-https-prod.mjs 的三项检查里，第 2、3 项
// （生产登录签发 token、带 token 读）必须要有生产口令，本机没有、也不猜。
// 但第 1 项——TLS 握手 + 返回的是 JSON 而不是 index.html——**不需要任何凭据**，
// 而它恰恰是 BUG-D 那类故障（nginx 把 /api 顶成 index.html）的分水岭。
// 把能验的验掉，比因为缺口令就整条不跑要好。
//
// ⚠️ 全程只读：不带 Authorization 头，不登录，不写任何东西到生产。
// ⚠️ 覆盖值必须还原，且失败路径也要还原（见 restoreBase）。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PROD = 'https://pocket.itestu.cn'
const BACKUP = process.env.POCKET_CDP_PORT || '9472'   // 仅用于打印，实际用 tcp:0

const adb = (a, t = 25000) => execFileSync(ADB, ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const adbSoft = (a) => { try { return adb(a, 8000) } catch { return '' } }
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const appPid = adbSoft(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!appPid) { console.log('APP_NOT_RUNNING（先跑 node scripts/maestro-run.mjs 任意 flow 拉起 App）'); process.exit(2) }
const socks = adbSoft(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
// 必须挑**当前进程**的 socket：设备上常留着死进程的 socket
const sock = socks.find((s) => s.endsWith(`_${appPid}`)) || socks[socks.length - 1]
const PORT = Number(adbSoft(['forward', 'tcp:0', `localabstract:${sock}`]).trim())
if (!Number.isInteger(PORT) || PORT <= 0) { console.log('CDP_FORWARD_NO_PORT'); process.exit(3) }

try {
  const pages = await (await fetch(`http://127.0.0.1:${PORT}/json/list`, { signal: AbortSignal.timeout(10000) })).json()
  const page = pages.find((t) => t.type === 'page')
  if (!page) { console.log('NO_PAGE_TARGET'); process.exit(4) }
  const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
  let id = 0
  const pending = new Map()
  ws.addEventListener('message', (e) => {
    const m = JSON.parse(e.data)
    if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
  })
  const opened = await Promise.race([
    new Promise((r) => ws.addEventListener('open', () => r(true))),
    new Promise((r) => setTimeout(() => r(false), 10000)),
  ])
  if (!opened) { console.log('CDP_OPEN_TIMEOUT'); process.exit(5) }
  const ev = async (x, ms = 40000) => {
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

  const before = await ev(`(function(){ try { return localStorage.getItem('pocket_api_base') } catch (e) { return '__ERR__' } })()`)
  console.log(`原 pocket_api_base = ${JSON.stringify(before.value)}`)

  let restored = false
  async function restoreBase(reason) {
    if (restored) return
    restored = true
    const r = await ev(`(function(){
      try {
        var v = ${JSON.stringify(before.value)}
        if (v === null || v === '__ERR__') localStorage.removeItem('pocket_api_base')
        else localStorage.setItem('pocket_api_base', v)
        return localStorage.getItem('pocket_api_base')
      } catch (e) { return 'RESTORE_FAIL: ' + String(e) }
    })()`)
    console.log(`已还原 pocket_api_base = ${JSON.stringify(r.value)}${reason ? '（' + reason + '）' : ''}`)
  }

  const setRes = await ev(`(function(){
    try { localStorage.setItem('pocket_api_base', ${JSON.stringify(PROD)}); return localStorage.getItem('pocket_api_base') }
    catch (e) { return 'SET_FAIL: ' + String(e) }
  })()`)
  if (setRes.value !== PROD) {
    console.log('覆盖写入失败，本轮作废')
    await restoreBase('覆盖写入失败')
    ws.close()
    process.exit(6)
  }
  console.log(`已写入 = ${PROD}（脚本旧默认端口 ${BACKUP} 未再用，改 tcp:0）`)

  const probe = await ev(`(async function(){
    var to = function (p, ms) { return Promise.race([p, new Promise(function (r) { setTimeout(function () { r('__TO__'); }, ms); })]) };
    var base = ${JSON.stringify(PROD)};
    var out = [];
    // 逐个端点：无鉴权 GET，验 TLS + content-type + body 是不是 JSON
    for (var i = 0; i < 2; i++) {
      var ep = ['/healthz', '/api/tasks'][i];
      try {
        var r = await to(fetch(base + ep, { cache: 'no-store' }), 20000);
        var ct = r.headers.get('content-type') || '';
        var txt = await to(r.text(), 8000);
        var isJson = true; var shape = null;
        try { var j = JSON.parse(txt); shape = Array.isArray(j) ? 'array(' + j.length + ')' : Object.keys(j).slice(0, 5); }
        catch (e) { isJson = false; }
        out.push({ ep: ep, status: r.status, ct: ct.slice(0, 40), isJson: isJson, shape: shape, body: txt.slice(0, 80) });
      } catch (e) { out.push({ ep: ep, err: String(e && e.message || e).slice(0, 100) }); }
    }
    // 同时验生产与备选入口是否都可达（api-base.ts:9/15 记录了热备关系）
    try {
      var r2 = await to(fetch('https://pocket.kxpms.cn/healthz', { cache: 'no-store' }), 15000);
      out.push({ ep: 'backup /healthz', status: r2.status, body: (await to(r2.text(), 5000)).slice(0, 40) });
    } catch (e) { out.push({ ep: 'backup /healthz', err: String(e && e.message || e).slice(0, 100) }); }
    return JSON.stringify(out);
  })()`, 70000)

  if (probe.err) { console.log('探针失败: ' + probe.err); await restoreBase('失败路径'); ws.close(); process.exit(7) }
  const rows = JSON.parse(probe.value)
  for (const r of rows) {
    if (r.err) { console.log(`  ❌ ${r.ep.padEnd(18)} ${r.err}`); continue }
    const jsonTag = r.isJson === false ? '  ⚠️ 不是 JSON（BUG-D 形态：被 index.html 顶替）' : ''
    console.log(`  ${String(r.status).padEnd(4)} ${r.ep.padEnd(18)} ct=${r.ct || '(none)'} body=${JSON.stringify(String(r.body).slice(0, 60))}${jsonTag}`)
  }
  await restoreBase()

  const healthz = rows.find((r) => r.ep === '/healthz')
  const tasks = rows.find((r) => r.ep === '/api/tasks')
  // ⚠️ 原先写的是 `tasks.isJson !== false` —— 当 tasks 带 err 时 isJson 是 undefined，
  //    `undefined !== false` 为真，于是「三条全失败」也判成 ✅ JSON 正确。
  //    恒真的判据比没有判据更糟：它会把失败印成通过。必须显式排除 err。
  const tlsOk = !!(healthz && !healthz.err && healthz.status === 200 && healthz.isJson !== false)
  const jsonOk = !!(tasks && !tasks.err && tasks.isJson !== false)
  console.log('\n=== 判读（只覆盖不需要凭据的那部分）===')
  console.log(`  真机 WebView → 生产 TLS 握手 + /healthz 200: ${tlsOk ? '✅' : '❌'}`)
  console.log(`  /api/tasks 返回 JSON 而非 index.html:          ${jsonOk ? '✅' : '❌'}`)
  console.log('  ⛔ 未覆盖：生产登录签发 token、带 token 读 —— 需要 POCKET_PROD_PASS')
  ws.close()
  process.exit(tlsOk && jsonOk ? 0 : 1)
} finally {
  adbSoft(['forward', '--remove', `tcp:${PORT}`])
}
