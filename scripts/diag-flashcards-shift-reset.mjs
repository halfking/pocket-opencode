#!/usr/bin/env node
/**
 * diag-flashcards-shift-reset.mjs —— 在**失败现场还热着**的时候，量清「页面被顶上去」
 * 到底是哪一层动的，并逐个试补救手段。
 *
 * 现场（2026-10-22 13:44，emulator-5556 / AOSP Android 14，健康后端、刚重启的模拟器）：
 *   .maestro/flashcards-write.yaml 稳定复现同一处红：
 *     Tap on "保存|Save", enabled, Index: 0 ... COMPLETED
 *     Assert that ".*回归正面.*" is visible ... COMPLETED   ← 假通过（被编辑页 textarea 喂饱）
 *     Assert that ".*今日待复习 1 张.*|.*1 due today.*" is visible ... FAILED
 *   失败那一帧的 a11y 树：
 *     'Front *' [34,31][141,76]   'Back *' [34,496][139,540]
 *     'Deck' [34,1283][115,1328]  Button 'Save'(submit) [556,1687][1047,1803]
 *     **顶栏那个 Save 不在树里** ⇒ `index: 0` 选到的是**底部**那个（被键盘盖住）
 *   与未开输入法时的几何对照（CDP 实测，设备像素）：
 *     顶栏 Save  180..254（中心 217 ⇒ 在状态栏 129 之下 ⇒ 点得到）
 *     底部 Save 2125..2236
 *   ⇒ 失败时整页**上移了 337 设备像素（≈128 CSS）**。
 *
 * 本脚本要回答三个问题：
 *   Q1 这一层位移是 **DOM 滚动**（某个容器的 scrollTop）还是**视觉视口偏移**
 *      （visualViewport.offsetTop / pageTop）？
 *   Q2 怎么把它**复位**：关输入法（BACK）、向下滑（真实拖动）、还是别的？
 *   Q3 复位后，Save 的可点位置在哪、点它能不能真的把卡片存进去？
 *
 * 只读为主；最后一步会真的点一次 Save（若成功会建出一张卡，属诊断副作用，
 * 下一次 flow 跑批前的清场脚本会清掉）。
 */
import { openCdp, adb } from './lib/adb-cdp.mjs'

const PKG = process.env.POCKET_APP_ID || 'com.kaixuan.opencode.pocket'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const c = await openCdp({ pkg: PKG })
let id = 0
const pending = new Map()
c.ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) {
    pending.get(m.id)(m)
    pending.delete(m.id)
  }
})
const ev = async (expression, ms = 12000) => {
  const i = ++id
  const raw = await new Promise((r) => {
    const t = setTimeout(() => { pending.delete(i); r(null) }, ms)
    pending.set(i, (y) => { clearTimeout(t); r(y) })
    c.ws.send(JSON.stringify({
      id: i, method: 'Runtime.evaluate',
      params: { expression, returnByValue: true, awaitPromise: true },
    }))
  })
  if (!raw) return null
  if (raw.result?.exceptionDetails) return `__error__ ${raw.result.exceptionDetails.text}`
  return raw.result?.result?.value
}

const read = async (tag) => {
  const raw = await ev(`(function(){
    var dpr = window.devicePixelRatio || 1;
    var padTop = parseFloat(getComputedStyle(document.body).paddingTop) || 0;
    var out = { dpr: dpr, inset: Math.round(padTop*dpr), hash: location.hash,
      vv: { h: Math.round(visualViewport.height), off: Math.round(visualViewport.offsetTop), pt: Math.round(visualViewport.pageTop), sl: Math.round(visualViewport.scale*100)/100 },
      sy: window.scrollY,
      se: { st: document.scrollingElement ? document.scrollingElement.scrollTop : null, sh: document.scrollingElement ? document.scrollingElement.scrollHeight : null, ch: document.scrollingElement ? document.scrollingElement.clientHeight : null },
      save: [], scrollers: [] };
    document.querySelectorAll('button').forEach(function(b){
      if (!/^(保存|Save)$/.test((b.textContent||'').trim())) return;
      var r = b.getBoundingClientRect();
      out.save.push({ cls: b.className, type: b.type, top: Math.round(r.top*dpr), bottom: Math.round((r.top+r.height)*dpr),
        left: Math.round(r.left*dpr), cx: Math.round((r.left+r.width/2)*dpr), cy: Math.round((r.top+r.height/2)*dpr) });
    });
    document.querySelectorAll('*').forEach(function(el){
      if (el.scrollTop > 0 || el.scrollHeight > el.clientHeight + 4) {
        var cs = getComputedStyle(el);
        out.scrollers.push({ tag: el.tagName, cls: (el.className||'').toString().slice(0,40), oy: cs.overflowY,
          st: el.scrollTop, sh: el.scrollHeight, ch: el.clientHeight, top: Math.round(el.getBoundingClientRect().top*dpr) });
      }
    });
    return JSON.stringify(out);
  })()`)
  if (!raw || String(raw).startsWith('__error__')) {
    console.log(`--- 【${tag}】读不到页面：${raw}`)
    return null
  }
  const g = JSON.parse(raw)
  let ime = '?'
  try {
    const d = adb(['shell', 'dumpsys', 'input_method'], 20000)
    const m = d.match(/mInputShown=([a-z]+)/i)
    ime = m ? m[1] : '?'
  } catch { ime = 'x' }
  console.log(`--- 【${tag}】ime=${ime}`)
  console.log('   hash =', g.hash, ' inset =', g.inset)
  console.log('   visualViewport =', JSON.stringify(g.vv), ' scrollY =', g.sy, ' scrollingElement =', JSON.stringify(g.se))
  console.log('   Save 按钮 =', JSON.stringify(g.save))
  console.log('   候选滚动容器 =', JSON.stringify(g.scrollers))
  return g
}

const g0 = await read('失败现场（未做任何补救）')
if (!g0) { console.error('DIAG_NOT_ON_EDIT_PAGE：当前不在建卡页'); c.close(); process.exit(3) }

// ---- Q2-a：关输入法（BACK）能不能把位移复位？-------------------------------
console.log('\n>>> 试 BACK（keyevent 4）关输入法')
adb(['shell', 'input', 'keyevent', '4'], 20000)
await sleep(2500)
const g1 = await read('BACK 之后')

// ---- Q2-b：真实拖动（向下滑）能不能复位？----------------------------------
if (g1 && (g1.vv.off !== 0 || g1.sy !== 0)) {
  console.log('\n>>> 试向下滑的真实拖动（input swipe 540,400 → 540,1000）')
  adb(['shell', 'input', 'swipe', '540', '400', '540', '1000', '400'], 25000)
  await sleep(2500)
  await read('向下滑之后')
}

c.close()
process.exit(0)
