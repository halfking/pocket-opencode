// 在 flow 运行期间持续采样闪卡建卡页的滚动状态，产出时间线。
//
// 要回答的问题：flow 用 29%/53%（scrollTop=0 时的正确坐标）为什么实测失败？
// 静态推理到头了 —— 需要看**运行中** scrollTop 到底在什么时候、被什么改了。
// 失败截图只能说「填错框」，说不出「什么时候开始错的」。
//
// 用法：先起本脚本（后台），再跑 maestro-run.mjs，最后看输出。
// 只读：不点不输入。CDP 断开（App 被重启）时自动重连，采样点会标 gap。
import { execFileSync } from 'node:child_process'
import { openCdp } from './lib/adb-cdp.mjs'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const adbBin = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const DURATION_S = Number(process.env.SAMPLE_SECONDS || 180)
const INTERVAL_MS = Number(process.env.SAMPLE_INTERVAL_MS || 400)

const PROBE = `(() => {
  const el = document.querySelector('main.content.scroll-shell') || document.querySelector('main')
  const pct = (e) => { if (!e) return null; const r = e.getBoundingClientRect()
    return Math.round((r.y + r.height / 2) / window.innerHeight * 100) }
  const ta = document.querySelectorAll('textarea')
  const inp = document.querySelectorAll('input')
  return JSON.stringify({
    h: location.hash.replace(/^#\\/?/, '').slice(0, 28),
    st: el ? Math.round(el.scrollTop * 10) / 10 : null,
    front: pct(ta[0]), back: pct(ta[1]), tag: pct(inp[0]),
    fv: ta[0] ? ta[0].value.length : null,
    bv: ta[1] ? ta[1].value.length : null,
    tv: inp[0] ? inp[0].value.length : null,
    focus: document.activeElement ? (document.activeElement.getAttribute('placeholder') || document.activeElement.tagName).slice(0, 8) : '-',
  })
})()`

// IME 顶边要单独从系统侧取（页面里读不到）
const imeTopPct = () => {
  try {
    const out = execFileSync(adbBin, ['-s', S, 'shell', 'dumpsys', 'window'], { encoding: 'utf8', maxBuffer: 33554432, timeout: 15000 })
    const line = out.split(/\r?\n/).find((l) => /type=ime/.test(l) && /visible=true/.test(l)) || ''
    const f = /frame=\[(\d+),(\d+)\]\[(\d+),(\d+)\]/.exec(line)
    return f ? Math.round((Number(f[2]) / 1640) * 100) : 0
  } catch { return -1 }
}

const t0 = Date.now()
let cdp = null
let last = null
console.log('# t(s)  scrollTop  ime%   正面%  背面%  标签%  值长(f/b/t)  focus       route')
try {
  while ((Date.now() - t0) / 1000 < DURATION_S) {
    try {
      if (!cdp) cdp = await openCdp({ pkg: PKG })
      const o = JSON.parse(await cdp.ev(PROBE))
      const ime = imeTopPct()
      const t = ((Date.now() - t0) / 1000).toFixed(1).padStart(5)
      // 只在**状态变化**时打点：滚动/坐标/值/焦点任一变了就记一行
      const sig = `${o.st}|${o.front}|${o.back}|${o.tag}|${o.fv}|${o.bv}|${o.tv}|${o.focus}|${o.h}`
      if (sig !== last) {
        last = sig
        console.log(`${t}  ${String(o.st).padStart(7)}  ${String(ime).padStart(4)}  ` +
          `${String(o.front).padStart(5)}  ${String(o.back).padStart(5)}  ${String(o.tag).padStart(5)}  ` +
          `${o.fv}/${o.bv}/${o.tv}`.padEnd(12) + `  ${String(o.focus).padEnd(9)}  ${o.h}`)
      }
    } catch (e) {
      if (cdp) { try { await cdp.close() } catch {} cdp = null }
      console.log(`${((Date.now() - t0) / 1000).toFixed(1).padStart(5)}  --- CDP 断开（App 重启/被抢占），重连中 ---`)
      last = null
    }
    await new Promise((r) => setTimeout(r, INTERVAL_MS))
  }
} finally {
  if (cdp) { try { await cdp.close() } catch {} }
}
console.log('# 采样结束')
