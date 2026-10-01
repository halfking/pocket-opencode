// diag-tts-dialog.mjs — 受控实验：开始会议录音会不会被系统对话框抢走前台。
//
// 起因：录音指示器时钟冻住，最终查到真因是 App 被压在 MIUI 系统对话框后面
// （com.xiaomi.mibrain.speech/.asr.AsrRemovalNotice，以及一个索要「录制音频」
// 权限的「系统语音引擎」首次授权页）。
//
// 待验的因果：我们的录音开始流程会调 TTS 语音播报
// （recordingRuntime 里 speakNativeText → TextToSpeech.speak，代码注释写明
// 「需求：录音时要播一段语音」）。在 MIUI 上首次调用系统 TTS 引擎是否会弹出
// 授权/告知页并抢焦点？
//
// 判据：开始录音后连续采样 topResumedActivity。只要它变成**非本应用**的包名，
// 就是「系统对话框抢焦点」；一直是本应用则否证该假设。
// 必须有对照：本脚本先采一段「未开始录音」的基线，
// 否则无法区分「录音触发的」与「本来就有的」。
//
// 收尾：点指示条停止按钮，把录音停掉（否则会留一个占着麦克风的录音）。
import { execFileSync } from 'node:child_process'
import { writeFileSync } from 'node:fs'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9460'
const adb = (a, t = 25000) => execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const adbSoft = (a) => { try { return adb(a, 8000) } catch { return '' } }
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

/** 当前前台 Activity 的包名——判断「谁抢了焦点」的唯一可靠来源。 */
function topPackage() {
  const s = adbSoft(['shell', 'dumpsys activity activities'])
  const m = s.match(/topResumedActivity=ActivityRecord\{\S+ u0 (\S+?)\//)
  return m ? m[1] : '(读不到)'
}

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
const ev = async (x, ms = 15000) => {
  const i = ++id
  const v = await new Promise((r) => {
    const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms)
    pending.set(i, (y) => { clearTimeout(t); r(y) })
    ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } }))
  })
  if (v?.__t) return { value: null, err: 'TIMEOUT' }
  if (v?.exceptionDetails) return { value: null, err: String(v.exceptionDetails.exception?.description || v.exceptionDetails.text).slice(0, 160) }
  return { value: v?.result?.value, err: '' }
}

/** 一次采样：谁在前台 + 页内录音态。 */
async function sample(tag) {
  const top = topPackage()
  const r = await ev(`JSON.stringify({
    hash: location.hash,
    vis: document.visibilityState, focus: document.hasFocus(),
    pill: !!document.querySelector('.rec-pill'),
    clock: (document.querySelector('.rec-pill .rec-clock')||{}).textContent || null,
    secs: (document.querySelector('.rec-pill .rec-secs')||{}).textContent || null,
    recWord: /录音中/.test((document.querySelector('#app')||{}).innerText || ''),
  })`)
  let st = {}
  try { st = JSON.parse(r.value) } catch { st = { err: r.err || 'parse' } }
  const stolen = top !== PKG
  console.log(`  [${tag}] 前台=${stolen ? '⚠️ ' + top : '本应用'}  hash=${st.hash}  vis=${st.vis} focus=${st.focus}  指示条=${st.pill} 时钟=${st.clock} 秒数=${st.secs}`)
  return { top, stolen, ...st }
}

// ---------- 对照基线：还没开始录音 ----------
console.log('=== 对照基线（未开始录音，连采 3 次）===')
const base = []
for (let i = 0; i < 3; i++) { base.push(await sample(`基线${i + 1}`)); if (i < 2) await sleep(2500) }
const baseStolen = base.filter((s) => s.stolen).length
console.log(`  基线被抢焦点 ${baseStolen}/3 次`)

// ---------- 实验：开始会议录音 ----------
console.log('\n=== 实验：跳转 #/meetings/new 开始录音，连采 8 次 ===')
const exp = []
for (let i = 0; i < 8; i++) {
  if (i === 0) {
    await ev(`(async () => {
      const to = (p, ms) => Promise.race([p, new Promise((r) => setTimeout(() => r('__TO__'), ms))])
      location.hash = '#/__ttsctl__'
      await to(new Promise((r) => setTimeout(r, 400)), 2000)
      location.hash = '#/meetings/new'
      await to(new Promise((r) => setTimeout(r, 2500)), 6000)
      return 'ok'
    })()`)
  }
  exp.push(await sample(`录音${i + 1}`))
  if (i < 7) await sleep(3000)
}
const stolen = exp.filter((s) => s.stolen)
console.log(`\n=== 判读 ===`)
console.log(`  开始录音后被系统对话框抢焦点：${stolen.length}/8 次`)
if (stolen.length) {
  console.log('  ⇒ 录音开始会触发系统对话框抢前台。')
  stolen.forEach((s, i) => console.log(`     第 ${i + 1} 次：${s.top}`))
  adbSoft(['shell', 'screencap', '-p', '/sdcard/tts-dialog.png'])
  execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, 'pull', '/sdcard/tts-dialog.png', 'C:/workspace/openpocket/wt3/logs/tts-dialog.png'], { encoding: 'utf8' })
  console.log('  已截图 logs/tts-dialog.png')
} else {
  console.log('  ⇒ 录音开始**没有**触发系统对话框。基线被抢焦点 ' + baseStolen + '/3 次，说明之前的对话框是外生的（系统一次性通知/其它 App），不是我们触发的。')
}

// ---------- 收尾：停掉录音 ----------
console.log('\n=== 收尾：停掉可能仍在跑的录音 ===')
const stop = await ev(`(async () => {
  const to = (p, ms) => Promise.race([p, new Promise((r) => setTimeout(() => r('__TO__'), ms))])
  const btn = document.querySelector('.rec-pill .rec-stop')
  if (!btn) return 'NO_STOP_BTN'
  btn.click()
  await to(new Promise((r) => setTimeout(r, 3000)), 6000)
  return JSON.stringify({ pillStillThere: !!document.querySelector('.rec-pill') })
})()`)
console.log('  停止结果: ' + (stop.value || stop.err))
console.log('  停后前台: ' + topPackage())
ws.close()
process.exit(0)
