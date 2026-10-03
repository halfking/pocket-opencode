// verify-sheet-a11y.mjs — 判定 BottomSheet 的内容到底能不能被 Maestro 看见。
//
// 背景（2026-10-01 12:20~12:30）：点「+ 新任务」后 20s 内 Maestro 始终看不到
// 「创建任务」，而 CDP 量到 DOM 里 .create-task-form=1。两个解释：
//   (a) tap 没送达按钮；(b) 弹窗不进入 Android 无障碍树。
// 之前的证据链有**两个缺陷**：DOM 量测与 hierarchy 抓取不是同一时刻；
// 且本机跑完 `maestro hierarchy` 之后 WebView devtools socket 会哑掉，
// 导致「抓完树再回查 DOM」这条路根本走不通。
//
// 所以本脚本换三条**互相独立**的通道，在同一时刻各取一次证：
//   1. CDP  : DOM 里有没有弹窗（抓树**之前**）
//   2. 截图 : 屏幕上有没有弹窗（抓树**当时**，adb screencap，不依赖 CDP）
//   3. 树   : maestro hierarchy 里有没有弹窗（抓树**当时**）
// 截图既是「弹窗确实开着」的独立证据，也让人眼能复核，不靠我的转述。
import { execFileSync, spawnSync } from 'node:child_process'
import { writeFileSync, readFileSync } from 'node:fs'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9600'
const JAVA = process.env.JAVA_HOME || 'C:\\Program Files\\Eclipse Adoptium\\jdk-21.0.12.101-hotspot'
const MAESTRO = 'C:/workspace/openpocket/logs/maestro/maestro/bin/maestro.bat'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 30000) => execFileSync(ADB, ['-s', SERIAL, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const adbSoft = (a) => { try { return adb(a, 10000) } catch { return '' } }

function connect() {
  const pid = adbSoft(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
  if (!pid) throw new Error('APP_NOT_RUNNING')
  const socks = adbSoft(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
    .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
  const sock = socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]
  adbSoft(['forward', `tcp:${PORT}`, `localabstract:${sock}`])
  return pid
}

async function openWs() {
  for (let i = 0; i < 4; i++) {
    try {
      connect()
      const l = await (await fetch(`http://127.0.0.1:${PORT}/json/list`, { signal: AbortSignal.timeout(8000) })).json()
      const p = l.find((t) => t.type === 'page')
      if (p) return p.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`)
    } catch { /* 重试 */ }
    await sleep(2000)
  }
  throw new Error('CDP_UNREACHABLE')
}

const url = await openWs()
const ws = new WebSocket(url)
let id = 0
const pending = new Map()
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  // ⚠️ 传 m.result 而不是整条 m：Runtime.evaluate 的响应是
  //    {id, result:{result:{value}}}；传整条再读 .result.value 恒为 undefined，
  //    会被误判成「CDP 断了」。踩过。
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
if (!(await Promise.race([
  new Promise((r) => ws.addEventListener('open', () => r(true), { once: true })),
  new Promise((r) => setTimeout(() => r(false), 10000)),
]))) { console.log('CDP_OPEN_TIMEOUT'); process.exit(5) }
const ev = async (x, ms = 12000) => {
  const i = ++id
  const v = await new Promise((r) => {
    const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms)
    pending.set(i, (y) => { clearTimeout(t); r(y) })
    ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } }))
  })
  if (v?.__t) return { err: 'TIMEOUT' }
  if (v?.exceptionDetails) return { err: String(v.exceptionDetails.exception?.description || v.exceptionDetails.text).slice(0, 160) }
  return { val: v?.result?.value }
}

// ---- 1. CDP：复位 + 清掉可能残留的弹窗，再打开一次 ----
await ev('location.hash="#/ai"')
await sleep(2500)
await ev(`(function(){var o=document.querySelector('.bottom-sheet-overlay');if(o){o.remove();return 1}return 0})()`)
await sleep(800)
console.log('CLICK ->', (await ev(`(function(){
  var b=Array.prototype.slice.call(document.querySelectorAll('button')).filter(function(x){return (x.textContent||'').trim()==='+ 新任务'})[0];
  if(!b) return 'NO_BTN'; b.click(); return 'ok';
})()`)).val)
await sleep(2000)

const PROBE = `(function(){
  var o=document.querySelector('.bottom-sheet-overlay');
  var s=document.querySelector('.bottom-sheet');
  var t=document.querySelector('.bottom-sheet .sheet-title');
  var r=s?s.getBoundingClientRect():null;
  return JSON.stringify({
    overlay:!!o, sheet:!!s, title:t?t.textContent.trim():'',
    form:!!document.querySelector('.create-task-form'),
    rect:r?[Math.round(r.left),Math.round(r.top),Math.round(r.right),Math.round(r.bottom)]:null,
    role:o?(o.getAttribute('role')||''):'', parent:o&&o.parentElement?o.parentElement.tagName.toLowerCase():'',
    inApp:!!document.querySelector('#app .bottom-sheet'), vw:[window.innerWidth,window.innerHeight],
    ctrls:Array.prototype.slice.call(document.querySelectorAll('.bottom-sheet input,.bottom-sheet textarea,.bottom-sheet button,.bottom-sheet .sheet-title')).map(function(e){
      var b=e.getBoundingClientRect();
      return {tag:e.tagName.toLowerCase(),ph:e.getAttribute('placeholder')||'',txt:(e.textContent||'').trim().slice(0,10),rect:[Math.round(b.left),Math.round(b.top),Math.round(b.right),Math.round(b.bottom)]};
    })
  });
})()`
const dom = await ev(PROBE)
console.log('CDP-DOM :', dom.err || dom.val)
ws.close()

// ---- 2. 截图（不依赖 CDP 的独立通道）----
const shot = 'logs/sheet-probe.png'
try { writeFileSync(shot, execFileSync(ADB, ['-s', SERIAL, 'exec-out', 'screencap', '-p'], { encoding: 'buffer', timeout: 60000, maxBuffer: 33554432 })) } catch (e) { console.log('SCREENSHOT 失败:', e.message) }

// ---- 3. hierarchy（放最后：跑完 CDP 就会哑）----
const RAW = 'logs/hier-sheet-probe.json'
spawnSync(`"${MAESTRO}" hierarchy --no-ansi --no-reinstall-driver > ${RAW} 2>logs/hier-sheet-probe.err`, {
  encoding: 'buffer', timeout: 180000, shell: true,
  env: { ...process.env, JAVA_HOME: JAVA, MAESTRO_CLI_NO_ANALYTICS: 'true', MAESTRO_DEVICE: SERIAL },
})
const text = new TextDecoder('gbk').decode(readFileSync(RAW))
const has = (s) => (text.includes(s) ? '有' : '无')
console.log(`HIER    : bytes=${readFileSync(RAW).length}`)
for (const probe of ['创建任务', '输入任务标题', '取消', '创建', '+ 新任务', '打开菜单']) {
  console.log(`          ${probe.padEnd(8)} → ${has(probe)}`)
}
console.log(`截图     → ${shot}`)
process.exit(0)
