// 一次性诊断：量「软键盘开合」对闪卡编辑页布局的影响。
//
// 为什么必须量：flashcards-write.yaml 的两个 textarea 是**按屏幕百分比**点的。
// 第一次 inputText 会唤起软键盘，若 WebView 压缩视口，整张表单在屏幕上重新映射，
// 第二次点同一个百分比就落到别的控件上（2026-10-04 实测「回归背面」进了标签框）。
// 只量键盘关的状态是不够的——那只覆盖第一次 tapOn。
//
// 输出：两次采样的 rect / devPct / innerHeight / visualViewport，
// 外加 imeHeight（键盘实际占屏高），用来把 css 坐标换算成「屏幕百分比」。
//
// 只读：不点不输入不改状态。唯一副作用是 正面 会被 focus（软键盘弹出）。
import { openCdp } from './lib/adb-cdp.mjs'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// 键盘弹出后布局需要时间稳定（WebView resize + 可能的 scrollIntoView 动画）
const SETTLE_MS = Number(process.env.KBD_SETTLE_MS || 2500)

const SAMPLE = `(() => {
  const vv = window.visualViewport
  const desc = (el) => {
    const r = el.getBoundingClientRect()
    return {
      ph: el.getAttribute('placeholder') || el.getAttribute('name') || el.tagName,
      rect: [Math.round(r.x), Math.round(r.y), Math.round(r.width), Math.round(r.height)],
      devPct: [Math.round((r.x + r.width / 2) / window.innerWidth * 100),
               Math.round((r.y + r.height / 2) / window.innerHeight * 100)],
      // 相对**布局视口**的 y（不受软键盘压缩影响，是稳定的布局坐标）
      layoutY: Math.round(r.y + window.scrollY),
    }
  }
  return JSON.stringify({
    hash: location.hash,
    innerH: window.innerHeight,
    innerW: window.innerWidth,
    scrollY: Math.round(window.scrollY),
    vvH: vv ? Math.round(vv.height) : null,
    vvOffTop: vv ? Math.round(vv.offsetTop) : null,
    active: document.activeElement ? (document.activeElement.getAttribute('placeholder') || document.activeElement.tagName) : null,
    textareas: Array.from(document.querySelectorAll('textarea')).map(desc),
    inputs: Array.from(document.querySelectorAll('input')).map(desc),
  })
})()`

const cdp = await openCdp({ pkg: PKG })
try {
  const before = JSON.parse(await cdp.ev(SAMPLE))
  console.log('=== A. 键盘关 ===')
  console.log(JSON.stringify(before, null, 2))

  // 真实唤起软键盘：focus 后必须真的弹 IME，仅 focus 不一定触发。
  await cdp.ev(`(() => { const t = document.querySelectorAll('textarea')[0]; if (!t) return 'no-textarea'; t.focus(); t.click(); return 'focused' })()`)
  await sleep(SETTLE_MS)

  const after = JSON.parse(await cdp.ev(SAMPLE))
  console.log('=== B. 键盘开（focus 正面后） ===')
  console.log(JSON.stringify(after, null, 2))

  console.log('=== C. 位移对照 ===')
  const imeH = after.innerH - before.innerH
  console.log(`innerH ${before.innerH} -> ${after.innerH}（键盘压缩 ${imeH} css px）`)
  console.log(`scrollY ${before.scrollY} -> ${after.scrollY}`)
  console.log(`视觉视口 vvH=${after.vvH} vvOffTop=${after.vvOffTop}`)
  for (const key of ['textareas', 'inputs']) {
    before[key].forEach((b, i) => {
      const a = after[key]?.[i]
      if (!a) return
      console.log(`  ${key}[${i}] "${b.ph}": layoutY ${b.layoutY} -> ${a.layoutY} (Δ${a.layoutY - b.layoutY}) | devPctY ${b.devPct[1]}% -> ${a.devPct[1]}%`)
    })
  }
} finally {
  await cdp.close()
}
