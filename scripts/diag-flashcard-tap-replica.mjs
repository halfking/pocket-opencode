// 真机复刻 flashcards-write.yaml 的「填正反面」两步，验证坐标是否真的落在正确的 textarea。
//
// 为什么不直接改 flow 再跑整条：
//   整条 flow 一次 50s+，且失败时只给一张截图 —— 截图能看出「填错框」，
//   但看不出「为什么这个百分比落在那儿」。这里把 Maestro 的两个原语
//   （tapOn point = adb input tap；inputText = adb input text）
//   原样复刻成可逐步取证的脚本，**每步之后回读 DOM 的真实值**。
//
// 复刻的是**状态**而不是坐标：起点必须是「键盘关、scrollY=0、空表单」，
// 因为 flow 第一次 tapOn 之前就是这个状态。
//
// 负控：node diag-flashcard-tap-replica.mjs 29 53
//   旧坐标必须失败。否则「14/38 成立」可能只是巧合，不能当成修复。
//
// ⚠️ 本文件是 UTF-8 无 BOM。**不要**用 Get-Content -Raw / Set-Content 改它：
//    PS 5.1 的 Get-Content 不带 -Encoding 时按 ANSI 解码，中文注释会被
//    逐字破坏成「鍧愭爣」并连带吃掉换行，node --check 直接报 Illegal return statement。
//    要改就用 write / edit 工具。
//
// 只写测试数据（两个 textarea + 一个 input），不点保存，不落库。
import { execFileSync } from 'node:child_process'
import { openCdp } from './lib/adb-cdp.mjs'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const adbBin = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const adb = (a, t = 30000) =>
  execFileSync(adbBin, ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// 从 dumpsys window 里精确取 IME 的 frame（必须按 type=ime 定位，
// 早先用正则抓第一个 InsetsSource 会抓到别的源，量出 32px 的假高度）。
const ime = () => {
  const out = adb(['shell', 'dumpsys', 'window'])
  const line = out.split(/\r?\n/).find((l) => /type=ime/.test(l) && /visible=(true|false)/.test(l)) || ''
  const shown = /mInputShown=(true|false)/.exec(adb(['shell', 'dumpsys', 'input_method']))?.[1]
  const f = /frame=\[(\d+),(\d+)\]\[(\d+),(\d+)\]/.exec(line)
  return {
    mInputShown: shown || 'unknown',
    top: f ? Number(f[2]) : null,
    bottom: f ? Number(f[4]) : null,
  }
}

const READ = `(() => {
  const g = (sel, i) => { const e = document.querySelectorAll(sel)[i]; return e ? e.value : null }
  return JSON.stringify({
    hash: location.hash,
    innerH: window.innerHeight,
    scrollY: Math.round(window.scrollY),
    focused: document.activeElement ? (document.activeElement.getAttribute('placeholder') || document.activeElement.tagName) : null,
    front: g('textarea', 0),
    back: g('textarea', 1),
    tag: g('input', 0),
  })
})()`

const focused = () =>
  cdp.ev(`document.activeElement? (document.activeElement.getAttribute('placeholder')||document.activeElement.tagName):'none'`)

const tap = (pctX, pctY) => {
  // 屏幕 720x1640；CapacitorWebView 本身占满 0,0-720,1640（dumpsys 的 ServedView 证实），
  // 所以 css 百分比 == 屏幕百分比，dpr=2 只需 ×2 换算成设备像素。
  const W = 720
  const H = 1640
  const x = Math.round((pctX / 100) * W)
  const y = Math.round((pctY / 100) * H)
  adb(['shell', 'input', 'tap', String(x), String(y)])
  return { x, y }
}

// 百分比可从命令行覆盖，用来跑负控。
const PCT_FRONT = Number(process.argv[2] || 14)
const PCT_BACK = Number(process.argv[3] || 38)
console.log(`坐标：正面 ${PCT_FRONT}%，背面 ${PCT_BACK}%`)

const cdp = await openCdp({ pkg: PKG })
try {
  // ⓿ 复位到 flow 到达时的状态：**路由切换会把 shell 的 scrollTop 复位到 0**。
  //    这一步不是可有可无的——滚动与否会整体挪动 122.5px（见下方对照），
  //    在被污染的滚动态里测坐标，结论会与 flow 实际遇到的完全无关。
  //    （2026-10-04 我就踩过：先在滚动态里量到 14%/38%，据此判「旧坐标 29%/53% 是错的」，
  //      那是把「坐标错」和「状态错」混为一谈。）
  await cdp.ev(`location.hash = '#/flashcards'`)
  await sleep(1800)
  await cdp.ev(`location.hash = '#/flashcards/new?deckId=deck_replica_probe'`)
  await sleep(2200)
  const st0 = await cdp.ev(`(() => { const el = document.querySelector('main.content.scroll-shell') || document.querySelector('main')
    return JSON.stringify({ hash: location.hash, scrollTop: el ? Math.round(el.scrollTop*10)/10 : null }) })()`)
  console.log('⓿ 复位到入口状态:', st0)
  if (/"scrollTop":0/.test(String(st0)) === false) {
    console.log('   ⚠️ scrollTop 不是 0，本轮结果不能与 flow 对比。')
  }

  // ① 复位到 flow 第一次 tapOn 之前的状态：键盘关 + 字段清空。
  //    用 blur() 关键盘而不是 ESC/BACK —— 后者在 WebView 里会触发路由回退。
  await cdp.ev(`(() => { const a=document.activeElement; if(a&&a.blur) a.blur();
    document.querySelectorAll('textarea,input').forEach(e=>{e.value='';e.dispatchEvent(new Event('input',{bubbles:true}))});
    return 'reset' })()`)
  await sleep(1500)
  const ime0 = ime()
  console.log('① 复位后:', await cdp.ev(READ), JSON.stringify(ime0))
  if (ime0.mInputShown !== 'false') {
    console.log(`   ⚠️ 键盘未收起（mInputShown=${ime0.mInputShown}），起点与 flow 不一致，结果不可比。`)
  }

  // ② flow 的第 1 步：tapOn 正面 + inputText
  const p1 = tap(50, PCT_FRONT)
  await sleep(1200)
  console.log(`② tap 50%,${PCT_FRONT}% -> adb tap ${p1.x},${p1.y}`)
  console.log('   聚焦:', await focused(), '| ime:', JSON.stringify(ime()))
  adb(['shell', 'input', 'text', 'RegFront'])
  await sleep(1200)
  console.log('   inputText 后:', await cdp.ev(READ))

  // ③ flow 的第 2 步：此时 shell 可能已被第一次 inputText 滚动过，
  //    打印滚动量——这是整个 bug 的要害：同一个百分比在两次 tap 之间含义不同。
  const st1 = await cdp.ev(`(() => { const el = document.querySelector('main.content.scroll-shell') || document.querySelector('main')
    return JSON.stringify({ scrollTop: el ? Math.round(el.scrollTop*10)/10 : null }) })()`)
  console.log('   （第 1 次 inputText 之后）', st1)
  const p2 = tap(50, PCT_BACK)
  await sleep(1200)
  console.log(`③ tap 50%,${PCT_BACK}%（键盘开）-> adb tap ${p2.x},${p2.y}`)
  console.log('   聚焦:', await focused(), '| ime:', JSON.stringify(ime()))
  adb(['shell', 'input', 'text', 'RegBack'])
  await sleep(1500)
  const fin = JSON.parse(await cdp.ev(READ))
  console.log('   inputText 后:', JSON.stringify(fin))

  // ④ 判定：正面/背面各自拿到自己的文本，且标签没被误填。
  //    只断言「没串」，不靠「保存按钮亮了」——那是另一个断言的职责。
  const ok = fin.front === 'RegFront' && fin.back === 'RegBack' && !fin.tag
  console.log(ok
    ? '✅ 坐标成立：正反面各归各位，标签未被误填。'
    : `❌ 坐标不成立：front=${JSON.stringify(fin.front)} back=${JSON.stringify(fin.back)} tag=${JSON.stringify(fin.tag)}`)
  if (!ok) process.exitCode = 1
} finally {
  await cdp.close()
}
