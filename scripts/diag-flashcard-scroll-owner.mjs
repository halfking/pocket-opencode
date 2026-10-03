// 找出闪卡建卡页**真正在滚的那个元素**。
//
// 为什么必须查：window.scrollY 恒为 0、scrollHeight 也等于 innerHeight（页面不滚），
// 但 header 的 .save-link 却量到 rect.y = -70（整个在视口外）。
// ⇒ 滚动发生在某个**内层 overflow:auto 容器**上，window 级的读数完全看不到它。
//
// 这对坐标判据是致命的：坐标是屏幕百分比，而屏幕位置 = 布局位置 - 容器 scrollTop。
// 只要那个容器的 scrollTop 在不同时刻不同，同一个百分比就落在不同控件上。
// 而且 window.scrollY=0 会让人误以为「页面没滚，坐标稳定」。
import { openCdp } from './lib/adb-cdp.mjs'

const PROBE = `(() => {
  const scrollers = []
  for (const el of Array.from(document.querySelectorAll('*'))) {
    const st = el.scrollTop
    const sh = el.scrollHeight
    const ch = el.clientHeight
    if (st > 0 || sh > ch + 1) {
      scrollers.push({
        tag: el.tagName,
        cls: (el.className || '').toString().slice(0, 60),
        id: el.id || '',
        scrollTop: st,
        scrollHeight: sh,
        clientHeight: ch,
        overflowY: getComputedStyle(el).overflowY,
      })
    }
  }
  const head = document.querySelector('.head') || document.querySelector('header')
  const hr = head ? head.getBoundingClientRect() : null
  return JSON.stringify({
    hash: location.hash,
    windowScrollY: Math.round(window.scrollY),
    scrollers,
    head: hr ? { cls: (head.className || '').toString(), rect: [Math.round(hr.x), Math.round(hr.y), Math.round(hr.width), Math.round(hr.height)] } : null,
  })
})()`

const cdp = await openCdp({ pkg: 'com.kaixuan.opencode.pocket' })
try {
  const out = JSON.parse(await cdp.ev(PROBE))
  console.log(`hash = ${out.hash}   window.scrollY = ${out.windowScrollY}`)
  console.log('可滚元素（scrollTop>0 或 scrollHeight>clientHeight）:')
  if (!out.scrollers.length) console.log('  （无）')
  for (const s of out.scrollers) {
    console.log(`  <${s.tag} class="${s.cls}"> overflowY=${s.overflowY} scrollTop=${s.scrollTop} ` +
      `scrollHeight=${s.scrollHeight} clientHeight=${s.clientHeight}` +
      `${s.scrollTop > 0 ? '  ← **当前真的滚了这么多**' : ''}`)
  }
  console.log('header:', JSON.stringify(out.head))
} finally {
  await cdp.close()
}
