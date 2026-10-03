// 回答一个决定性问题：flow 走到「填正反面」那一步时，scroll-shell 的 scrollTop 到底是多少？
//
// 为什么这是决定性的：
//   两个 textarea 按**屏幕百分比**点，而屏幕位置 = 布局位置 − scrollTop。
//   实测同一份布局在 scrollTop=0 时是 29%/53%，在 scrollTop=122.5 时是 14%/38%——
//   两组数字各自都"对"，但不在同一状态下。选哪组取决于 flow 到达时的真实 scrollTop，
//   而不是取决于哪组测得更准。
//
// 做法：按 flow 的真实路径重新走一遍（离开建卡页 → 再进来），每一步读 scrollTop。
// 只导航和读数，**不点保存、不写库**。
import { openCdp } from './lib/adb-cdp.mjs'

const SHELL = `(() => {
  const el = document.querySelector('main.content.scroll-shell') || document.querySelector('main')
  const d = (e) => { if (!e) return null; const r = e.getBoundingClientRect()
    return { ph: e.getAttribute('placeholder') || e.getAttribute('name') || e.tagName,
             pctY: Math.round((r.y + r.height / 2) / window.innerHeight * 100) } }
  return JSON.stringify({
    hash: location.hash,
    windowScrollY: Math.round(window.scrollY),
    shellScrollTop: el ? Math.round(el.scrollTop * 10) / 10 : null,
    shellScrollHeight: el ? el.scrollHeight : null,
    shellClientHeight: el ? el.clientHeight : null,
    ta: Array.from(document.querySelectorAll('textarea')).map(d),
    inp: Array.from(document.querySelectorAll('input')).map(d),
  })
})()`

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const cdp = await openCdp({ pkg: 'com.kaixuan.opencode.pocket' })
const show = async (label) => {
  const o = JSON.parse(await cdp.ev(SHELL))
  const ys = [...o.ta, ...o.inp].map((x) => `${x.ph}:${x.pctY}%`).join('  ')
  console.log(`${label.padEnd(34)} scrollTop=${String(o.shellScrollTop).padStart(6)}  ${ys}`)
  return o
}

try {
  await show('① 起点（当前状态）')

  // flow 的真实路径：卡组列表页 → 点「新建卡片」进入建卡页。
  // 这里用路由跳转模拟「重新进入」，看路由切换会不会把 scrollTop 复位。
  await cdp.ev(`location.hash = '#/flashcards'`)
  await sleep(2000)
  await show('② 离开到卡组列表')

  await cdp.ev(`location.hash = '#/flashcards/new'`)
  await sleep(2000)
  const a = await show('③ 重新进入建卡页（无 deckId）')

  await cdp.ev(`location.hash = '#/flashcards'`)
  await sleep(1500)
  await cdp.ev(`location.hash = '#/flashcards/new?deckId=deck_9a8d737efe9904f9b110b1ac3edaa98b'`)
  await sleep(2000)
  const b = await show('④ 重新进入建卡页（带 deckId）')

  console.log('')
  if (a.shellScrollTop === 0 && b.shellScrollTop === 0) {
    console.log('⇒ 路由切换会把 scrollTop 复位到 0 ⇒ flow 到达时应当用 29%/53%。')
  } else {
    console.log(`⇒ 路由切换**不复位** scrollTop（${a.shellScrollTop} / ${b.shellScrollTop}）—— ` +
      '坐标判据在这里是不稳的，必须另想办法。')
  }
} finally {
  await cdp.close()
}
