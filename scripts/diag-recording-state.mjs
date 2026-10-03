// diag-recording-state.mjs — 分清两件不同的事，并收尾巡检留下的录音。
//
// 1) 「录音跨页面存续」是**设计**：RecordingPill.vue 头注释写明
//    「2026-09-20 P0 录音后台化……会议/笔记录音跨页面存续」，指示条还带停止按钮。
//    所以指示条出现在别的页面上**不是缺陷**。
// 2) 但计数**冻在 00:01** 是另一回事：真在录的话 secondsOnly 必须增长。
//    指示条显示一个不动的时长 = 界面在说谎。
//
// 判据：连续读三次指示条文本（间隔 8s），看 mm:ss 与秒数是否推进。
// 若在录但不动 ⇒ 显示层 bug；若不在录（停止后指示条消失）⇒ 之前是陈旧 DOM。
//
// 同时：巡检访问 /meetings/new 会「进页面即建会并开始录音」（落地 ?record=1），
// 那是本脚本之外留下的副作用——读完状态后若仍在录，**顺手点停止收尾**。
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9427'
const adb = (a, t = 60000) => execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

const pid = adb(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0; const pending = new Map()
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
await new Promise((r) => ws.addEventListener('open', r))
const ev = async (x, ms = 20000) => {
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

// 读录音 runtime 的真实状态（不只看 DOM）
const READ_STATE = `(() => {
  const pill = document.querySelector('.rec-pill')
  const app = document.querySelector('#app')
  const txt = pill ? (pill.innerText || '').replace(/\\n/g, ' ').trim() : null
  return JSON.stringify({
    hash: location.hash,
    pillPresent: !!pill,
    pillText: txt,
    stopBtn: !!document.querySelector('.rec-pill .rec-stop'),
    pageHasRecWord: /录音中/.test((app ? app.innerText : '') || ''),
  })
})()`

console.log('=== 连续 3 次读录音状态，间隔 8s ===')
const samples = []
for (let i = 0; i < 3; i++) {
  const { value, err } = await ev(READ_STATE)
  if (err) { console.log(`  第 ${i + 1} 次读取失败: ${err}`); samples.push(null) }
  else { const s = JSON.parse(value); samples.push(s); console.log(`  [${i}] hash=${s.hash} 指示条=${s.pillPresent} 文本=${JSON.stringify(s.pillText)} 停止按钮=${s.stopBtn}`) }
  if (i < 2) await sleep(8000)
}

const withPill = samples.filter(Boolean).filter((s) => s.pillPresent)
const clocks = withPill.map((s) => (s.pillText || '').match(/(\d+:\d+)/)?.[1] || (s.pillText || '').replace(/\D/g, ''))
console.log(`\n=== 判读 ===`)
if (!withPill.length) {
  console.log('  指示条不存在 → 当前没有录音在进行。此前巡检看到的「录音中 00:01」是已消失的陈旧快照。')
} else {
  const secs = clocks.map((c) => Number(c))
  const grew = secs.length > 1 && secs[secs.length - 1] > secs[0]
  console.log(`  指示条存在，读到的时间序列: ${clocks.join(' -> ')}`)
  console.log(grew
    ? '  ✅ 计数在推进 → 录音确实在跑，指示条如实反映。「跨页面存续」是设计，不是缺陷。'
    : '  ❌ 计数**没有推进** → 指示条显示一个不动的时长 = 界面在说谎，这是显示层缺陷。')
  if (!grew) {
    console.log('\n=== 收尾：点击指示条上的停止按钮 ===')
    const { value } = await ev(`(async () => {
      const withTimeout = (p, ms) => Promise.race([p, new Promise((r) => setTimeout(() => r('__TO__'), ms))])
      const btn = document.querySelector('.rec-pill .rec-stop')
      if (!btn) return 'NO_STOP_BTN'
      btn.click()
      await withTimeout(new Promise((r) => setTimeout(r, 2500)), 5000)
      return JSON.stringify({
        pillStillThere: !!document.querySelector('.rec-pill'),
        pageHasRecWord: /录音中/.test(document.querySelector('#app').innerText || ''),
      })
    })()`)
    console.log('  停止后：' + (value || '（无返回）'))
  }
}
ws.close()
process.exit(0)
