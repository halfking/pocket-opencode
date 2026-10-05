// 负控：确认「键盘没压缩布局」这个结论不是**没弹键盘**造成的假象。
//
// 背景：diag-flashcard-kbd-shift.mjs 量到 innerH 820→820、scrollY 0→0、所有 layoutY Δ0，
// 推翻了「软键盘开合会压缩 WebView 布局」这个长期假设（flashcards-write.yaml 的注释里写着它）。
//
// 但如果 CDP `el.focus()` 根本没唤起 IME，那「没位移」就只是「键盘压根没出现」，
// 两种情况在数字上完全一样 —— 这正是判据失明。
//
// 正解：同时从**系统侧**独立取证 IME 是否可见，两边对不上就不许下结论。
import { execFileSync } from 'node:child_process'
import { openCdp } from './lib/adb-cdp.mjs'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const adbBin = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const adb = (a, t = 30000) =>
  execFileSync(adbBin, ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// 从 input_method 服务读真实 IME 状态。mInputShown=true 才是「键盘在屏上」。
const imeState = () => {
  const out = adb(['shell', 'dumpsys', 'input_method'])
  const shown = /mInputShown=(true|false)/.exec(out)?.[1] ?? 'unknown'
  // 键盘高度：InsetsSource 的 frame，能顺带确认它占了多少屏
  const frame = /frame=\[(\d+),(\d+)\]\[(\d+),(\d+)\]/.exec(out)
  return { mInputShown: shown, imeFrame: frame ? frame.slice(1).join(',') : null }
}

const cdp = await openCdp({ pkg: PKG })
try {
  console.log('键盘关（基线）:', JSON.stringify(imeState()))

  await cdp.ev(`(() => { const t = document.querySelectorAll('textarea')[0]; if (!t) return 'no-textarea'; t.focus(); return document.activeElement === t ? 'focused' : 'focus-moved-elsewhere' })()`)
  await sleep(2000)

  const st = imeState()
  const active = await cdp.ev(`(() => { const a = document.activeElement; return a ? (a.getAttribute('placeholder') || a.tagName) : 'none' })()`)
  console.log('键盘开（focus 正面后）:', JSON.stringify(st))
  console.log('  document.activeElement =', JSON.stringify(active))

  if (st.mInputShown !== 'true') {
    console.log('❌ IME 未显示 —— 「布局无位移」是假象（键盘压根没弹），本次测量不构成证据。')
    process.exitCode = 1
  } else {
    const [, , bottom] = (st.imeFrame || '0,0,0,0').split(',').map(Number)
    console.log(`✅ IME 确实在屏（frame 底边 y=${bottom}）。`)
    console.log(`   若视口仍为 820 且 layoutY 不动，说明键盘是**浮在上面**、不参与 WebView 布局。`)
  }
} finally {
  await cdp.close()
}
