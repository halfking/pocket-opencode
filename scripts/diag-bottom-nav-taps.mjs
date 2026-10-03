// 复现「更多」tab 点击不生效：量底部导航四个 tab 的真实 rect，再逐个按坐标点。
//
// 为什么要单独做：more-grid-reach.yaml 在 step-008 断言「更多功能」时红，
// 失败截图显示 App **还停在首页**（底部导航确实是 首页/笔记/消息/更多）。
// 而 messages-hub.yaml 点「消息」是通的 ⇒ 问题不是「底部导航整体点不动」，
// 而是与**具体某个 tab** 有关。静态读代码看不出来，只能量。
//
// 只读 + 点击导航，不改任何数据。
import { execFileSync } from 'node:child_process'
import { openCdp } from './lib/adb-cdp.mjs'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const adbBin = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const adb = (a, t = 20000) =>
  execFileSync(adbBin, ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
// ⚠️ getBoundingClientRect 给的是 **CSS 像素**，adb input tap 要的是**设备像素**。
//    本机 dpr=2，不乘就是点错地方（第一版没乘，tap 落在屏幕中部，
//    四个 tab 全报「没动」，看起来像导航整体坏了 —— 那是尺子错了，不是产品坏了）。
const DPR = 2
const tap = (cssX, cssY) => adb(['shell', 'input', 'tap', String(Math.round(cssX * DPR)), String(Math.round(cssY * DPR))])

const cdp = await openCdp({ pkg: PKG })
try {
  // ① 回到有底部导航的首页
  await cdp.ev(`location.hash = '#/ai'`)
  await sleep(2500)
  const hash0 = await cdp.ev('location.hash')
  console.log(`① 起点 hash=${hash0}`)

  // ② 量底部导航：把导航区里所有可点元素连同 rect 一起读出来
  const nav = await cdp.ev(`(function(){
    var out = [];
    var els = document.querySelectorAll('a,button,[role="tab"],[role="button"],li');
    for (var i = 0; i < els.length; i++) {
      var e = els[i];
      var r = e.getBoundingClientRect();
      if (r.width < 40 || r.height < 30) continue;
      if (r.y < window.innerHeight * 0.85) continue;          // 只看底部导航带
      var t = (e.innerText || e.textContent || '').trim().replace(/\\s+/g, ' ').slice(0, 12);
      if (!t) continue;
      out.push({ tag: e.tagName, text: t,
        rect: [Math.round(r.x), Math.round(r.y), Math.round(r.width), Math.round(r.height)],
        cx: Math.round(r.x + r.width / 2), cy: Math.round(r.y + r.height / 2) });
    }
    return JSON.stringify({ innerH: window.innerHeight, items: out });
  })()`)
  const { innerH, items } = JSON.parse(nav)
  console.log(`② 底部导航带（y > ${Math.round(innerH * 0.85)}）共 ${items.length} 项：`)
  for (const it of items) {
    console.log(`   <${it.tag}> "${it.text}" rect=${JSON.stringify(it.rect)} 中心=(${it.cx},${it.cy})`)
  }

  // ③ 逐个点，看 hash 是否真的变
  const startHash = String(hash0)
  for (const it of items) {
    if (!/首页|笔记|消息|更多|Home|Notes|Messages|More/.test(it.text)) continue
    // 每次都从同一个起点出发，否则上一轮点过去之后「没动」可能只是「已经在那一页」
    await cdp.ev(`location.hash = '#/ai'`)
    await sleep(1800)
    tap(it.cx, it.cy)
    await sleep(2500)
    const h = String(await cdp.ev('location.hash'))
    console.log(`③ 点 "${it.text}" css(${it.cx},${it.cy}) → device(${it.cx * DPR},${it.cy * DPR}) → hash=${h} ` +
      `${h !== startHash ? '✅ 导航生效' : '❌ 没动'}`)
  }
} finally {
  await cdp.close()
}
