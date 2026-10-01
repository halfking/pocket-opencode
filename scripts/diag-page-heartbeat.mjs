// diag-page-heartbeat.mjs — 判别「录音时钟冻住」是产品缺陷还是页面被节流。
//
// 观察到的矛盾（同一块 .rec-pill 上）：
//   .rec-clock  = "05:15"   ← 纯 computed 文本，依赖 200ms setInterval 推进
//   .rec-secs   = "1 s"     ← AnimatedNumber，依赖 useCountUp 的 requestAnimationFrame
//   两者 8s×3 采样都不动，但彼此读数**不一致**（315s vs 1s）。
//
// 两种解释后果完全相反：
//   (A) 页面被节流（切后台/屏幕关/窗口失焦）
//       → rAF 完全不跑（display 停在最后一帧），setInterval 被钳到 ~1/min
//         （但值仍由 Date.now() 算出，所以 clock 读数是**对**的，只是很少刷新）。
//         ⇒ 冻结是环境必然，不是产品缺陷。
//   (B) 页面在前台、心跳满速
//       ⇒ 是录音真没推进（timer 被 clearInterval），或 AnimatedNumber 的 rAF 循环死了。
//         两个都要分别定位。
//
// 已排除的第三条：isPaused 全代码库从未被赋值（只读不写），暂停路径不成立。
//
// 判据：页内埋心跳 2s，回读 interval 触发数与 rAF 帧数，与 Date.now() 实耗对比。
// 满速基线：interval≈5/s，rAF≈60/s。
//
// 工具坑：设备 dumpsys window 服务不稳（会 Broken pipe / 挂死），本脚本只用
// dumpsys power，并且给所有 adb 调用、WebSocket open、/json/list 都加了超时兜底 ——
// 无兜底的 await 会让整条探针永久挂起。
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9427'
const adb = (a, t = 25000) => execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
/** 拿不到就返回空串，绝不让辅助查询把主诊断拖死。 */
const adbSoft = (a) => { try { return adb(a, 8000) } catch { return '' } }
const log = (...a) => { console.log(...a) }

log('=== 屏幕电源（辅证）===')
const power = adbSoft(['shell', 'dumpsys power']).split(/\r?\n/).map((l) => l.trim())
  .filter((l) => /mWakefulness=|Display Power: state=/.test(l)).slice(0, 3)
power.forEach((l) => log('  ' + l))
if (!power.length) log('  （取不到，仅作参考；判据不依赖它）')

const pid = adbSoft(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { log('APP_NOT_RUNNING'); process.exit(2) }
log(`  appPid=${pid}`)

const socks = adbSoft(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
const sock = socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]
if (!sock) { log('NO_DEVTOOLS_SOCKET'); process.exit(3) }
adbSoft(['forward', `tcp:${PORT}`, `localabstract:${sock}`])

const pages = await (await fetch(`http://127.0.0.1:${PORT}/json/list`, { signal: AbortSignal.timeout(10000) })).json()
const page = pages.find((t) => t.type === 'page')
if (!page) { log('NO_PAGE_TARGET'); process.exit(4) }
log(`  devtoolsSocket=${sock}`)

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0; const pending = new Map()
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
// 兜底：没有 open 也必须往下走，否则整条探针永久挂起
const opened = await Promise.race([
  new Promise((r) => ws.addEventListener('open', () => r(true))),
  new Promise((r) => setTimeout(() => r(false), 10000)),
])
if (!opened) { log('CDP_OPEN_TIMEOUT'); process.exit(5) }

const ev = async (x, ms = 15000) => {
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
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

log('\n=== 页内可见性 / 焦点 ===')
{
  const { value, err } = await ev(`JSON.stringify({
    vis: document.visibilityState, hasFocus: document.hasFocus(), hidden: document.hidden,
    hash: location.hash,
    clock: (document.querySelector('.rec-pill .rec-clock')||{}).textContent,
    secs: (document.querySelector('.rec-pill .rec-secs')||{}).textContent,
  })`)
  log('  ' + (err ? '读取失败: ' + err : value))
}

log('\n=== 页内心跳 2s 实测 ===')
{
  const { err } = await ev(`(() => {
    const h = window.__hb = { iv: 0, raf: 0, t0: Date.now(), stop: false }
    h.ivId = setInterval(() => { h.iv++ }, 200)
    const loop = () => { if (h.stop) return; h.raf++; requestAnimationFrame(loop) }
    requestAnimationFrame(loop)
    return 'ok'
  })()`)
  if (err) { log('  埋点失败: ' + err); process.exit(6) }
  await sleep(2000)
  const { value, err: e2 } = await ev(`(() => {
    const h = window.__hb
    h.stop = true; clearInterval(h.ivId)
    return JSON.stringify({ elapsedMs: Date.now() - h.t0, interval200ms: h.iv, rafFrames: h.raf })
  })()`)
  if (e2) { log('  回读失败: ' + e2); process.exit(7) }
  const hb = JSON.parse(value)
  const ivRate = hb.interval200ms / (hb.elapsedMs / 1000)   // 满速≈5/s
  const rafRate = hb.rafFrames / (hb.elapsedMs / 1000)      // 满速≈60/s
  log(`  实耗 ${hb.elapsedMs}ms：setInterval(200ms) 触发 ${hb.interval200ms} 次（${ivRate.toFixed(2)}/s，满速≈5）`)
  log(`             requestAnimationFrame ${hb.rafFrames} 帧（${rafRate.toFixed(2)}/s，满速≈60）`)

  log('\n=== 判读 ===')
  if (ivRate < 1.0 || rafRate < 5) {
    log('  ⚠️  页内心跳被节流（interval 或 rAF 远低于满速）')
    log('     ⇒ 时钟「不动」是浏览器节流的必然结果，不是录音停止，也不是显示 bug。')
    log('     ⇒ 之前巡检看到的「00:01 冻住」同样可由节流解释，不能记成产品缺陷。')
  } else {
    log('  ✅ 页内心跳满速 ⇒ 页面在前台正常运行')
    log('     ⇒ 时钟冻住**不能**用节流解释，是产品缺陷。')
    log('     ⇒ 分两路定位：clock 停(timer 被 clearInterval?) vs secs 停(rAF 循环死?)。')
  }
}

log('\n=== 指示条读数 6s 复采 ===')
for (let i = 0; i < 3; i++) {
  const r = await ev(`JSON.stringify({
    clock: (document.querySelector('.rec-pill .rec-clock')||{}).textContent,
    secs: (document.querySelector('.rec-pill .rec-secs')||{}).textContent,
  })`)
  log(`  [${i}] ${r.value || ('失败:' + r.err)}`)
  if (i < 2) await sleep(6000)
}
ws.close()
process.exit(0)
