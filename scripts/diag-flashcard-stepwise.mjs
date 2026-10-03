// 复现 flashcards-write 第 5 步，**逐步**记录每次 tap 前后
// 正面/背面 textarea 与标签 input 的真实 rect，定位坐标为何落空。
//
// 为什么不用 diag-flashcard-selectors.mjs 的静态测量：它只量了一个瞬间，
// 而 flow 里每次 inputText / hideKeyboard 都会改变布局。
// 这里按 flow 的真实顺序逐步采样。
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
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) {
    pending.get(m.id)(m.result)
    pending.delete(m.id)
  }
})
await new Promise((r) => ws.addEventListener('open', r))
const ev = async (x, ms = 20000) => {
  const i = ++id
  const v = await new Promise((r) => {
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

const SAMPLE = `(() => {
  const pct = (el) => { const r = el.getBoundingClientRect()
    return { top: Math.round(r.top), h: Math.round(r.height),
             p: [Math.round((r.x + r.width/2)/window.innerWidth*100),
                 Math.round((r.y + r.height/2)/window.innerHeight*100)] } }
  const tas = Array.from(document.querySelectorAll('textarea'))
  const inp = Array.from(document.querySelectorAll('input'))
  return {
    hash: location.hash,
    innerH: window.innerHeight, scrollY: window.scrollY,
    visualH: window.visualViewport ? Math.round(window.visualViewport.height) : null,
    textareaCount: tas.length,
    front: tas[0] ? { ph: tas[0].placeholder, val: tas[0].value.slice(0,12), ...pct(tas[0]) } : null,
    back:  tas[1] ? { ph: tas[1].placeholder, val: tas[1].value.slice(0,12), ...pct(tas[1]) } : null,
    tag:   inp[0] ? { ph: inp[0].placeholder, val: inp[0].value.slice(0,12), ...pct(inp[0]) } : null,
    active: document.activeElement ? document.activeElement.tagName + '/' +
             (document.activeElement.placeholder || document.activeElement.id || '?') : null,
    saveDisabled: (() => { const b = Array.from(document.querySelectorAll('button'))
        .find(b => /保存|Save/.test(b.textContent||''))
      return b ? b.disabled : 'no-save-button' })(),
  }
})()`

const show = async (tag) => {
  const { value, err } = await ev(SAMPLE)
  if (err) {
    console.log(`\n### ${tag}  EVAL_ERR ${err}`)
    return
  }
  const v = value
  const p = (x) => (x ? `pct=${x.p[0]}%,${x.p[1]}% top=${x.top} h=${x.h} val='${x.val}'` : 'null')
  console.log(
    `\n### ${tag}\n  hash=${v.hash} innerH=${v.innerH} visualH=${v.visualH} scrollY=${v.scrollY} tas=${v.textareaCount}\n  front: ${p(v.front)}\n  back : ${p(v.back)}\n  tag  : ${p(v.tag)}\n  active=${v.active}  saveDisabled=${v.saveDisabled}`,
  )
}

const tap = async (label, x, y) => {
  adb(['shell', 'input', 'tap', String(x), String(y)])
  console.log(`  [tap ${label} -> ${x},${y}]`)
}

const type = async (t) => {
  adb(['shell', 'input', 'text', t])
  console.log(`  [inputText ${t}]`)
}
const hideKb = () => {
  adb(['shell', 'input', 'keyevent', '111'])
  console.log('  [hideKeyboard ESC]')
}

await ev(`location.hash = '#/flashcards/new?deckId=deck_2ae984b23d864fb5d35adb6545e3018d'`)
await sleep(3500)
await show('进入建卡页后（初始）——与 flow 同入口 ?deckId=')

await tap('正面 14%', 360, Math.round(1640 * 0.14))
await sleep(800)
await show('tap 正面 14% 之后')
await type('FRONT')
await sleep(800)
await show('inputText FRONT 之后（键盘应已开）')
await hideKb()
await sleep(1200)
await show('hideKeyboard 之后')

const { value: v2 } = await ev(SAMPLE)
if (v2?.back) {
  const devY = Math.round(((v2.back.top + v2.back.h / 2) / 820) * 1640)
  await tap('背面（按此刻实测 rect 换算）', 360, devY)
} else {
  await tap('背面 38%（回退）', 360, Math.round(1640 * 0.38))
}
await sleep(800)
await show('tap 背面坐标之后')
await type('BACK')
await sleep(800)
await show('inputText BACK 之后')
await hideKb()
await sleep(1200)
await show('第二次 hideKeyboard 之后（终态）')
