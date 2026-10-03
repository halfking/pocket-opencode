/**
 * 软键盘避让（useKeyboardInset）——用户诉求「键盘不能遮盖正在输入的框」的承重测试。
 *
 * ## 为什么这个文件存在
 *
 * 2026-10-03 全量审计发现：全仓 196 个测试文件、1804 个用例里，**键盘避让
 * 机制一条测试都没有**。它同时是三条用户诉求的承重墙——
 *
 *   - 「注意输入框与键盘联动，键盘不能遮盖正在输入的框」
 *   - 「多行文本输入区域展示要完整」
 *   - 大模型/录音任务切后台不丢失（切后台 → WebView resize → 键盘态收放）
 *
 * 一条**只被文档承诺、没有任何用例守住的**机制，正是「上一轮审过、这轮
 * 改坏」那类事故的高发位置。机制的核心复杂度全在两件事上：
 *
 *   1. **两条路径要归一**。Capacitor Android 壳是 edge-to-edge，键盘弹起时
 *      WebView 可能自己缩（resize 路径，模拟器 API 36），也可能不缩、直接盖
 *      在视口上（overlay 路径，真机 Android 15）。同一个 `--kb-inset` 要在
 *      两条路径下都给出正确结果，且**不能双重收缩**。
 *   2. **聚焦字段必须被顶到键盘上沿之上**，而不是只把 --kb-inset 算对就算完。
 *
 * ## 这组用例的判据从哪来
 *
 * 判据不是「我猜应该这样」，而是 `useKeyboardInset.ts` 文件头写明的建模：
 *
 *   baseline = 无键盘时见过的最大 innerHeight
 *   overlap   = baseline - min(innerHeight, visualViewport.height)
 *
 * 断言全部写在这条公式上。
 */

import { JSDOM } from 'jsdom'
import test from 'node:test'
import assert from 'node:assert/strict'

/** 装一套可控的 window：visualViewport / innerHeight 都由用例自己驱动。 */
function makeWindow({ innerHeight = 800, vvHeight = 800 } = {}) {
  const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'https://localhost/' })
  const win = dom.window

  const listeners = new Map()
  const vv = {
    height: vvHeight,
    addEventListener(type, fn) {
      if (!listeners.has(type)) listeners.set(type, [])
      listeners.get(type).push(fn)
    },
    removeEventListener(type, fn) {
      const arr = listeners.get(type) ?? []
      const i = arr.indexOf(fn)
      if (i >= 0) arr.splice(i, 1)
    },
  }
  Object.defineProperty(win, 'visualViewport', { value: vv, configurable: true })
  win.innerHeight = innerHeight

  return {
    win,
    vv,
    /** 模拟一次视口变化并广播（模拟器/真机的 visualViewport resize 就是这条）。 */
    resize(next) {
      if (next.innerHeight !== undefined) win.innerHeight = next.innerHeight
      if (next.vvHeight !== undefined) vv.height = next.vvHeight
      for (const fn of listeners.get('resize') ?? []) fn()
    },
  }
}

/**
 * 让 nearestScrollableAncestor 认得这个 div 是滚动容器。
 *
 * 必须在 freshModule() **之前**装：模块源码用的是裸全局 getComputedStyle，
 * freshModule 会把当时的 win.getComputedStyle 绑到 globalThis 上，晚一步
 * 覆盖就绑不到。
 */
function makeScrollable(win, el) {
  const real = win.getComputedStyle.bind(win)
  win.getComputedStyle = (target) => {
    const s = real(target)
    return new Proxy(s, {
      get(t, prop) {
        if (prop === 'overflowY') return 'auto'
        const v = t[prop]
        return typeof v === 'function' ? v.bind(t) : v
      },
    })
  }
  Object.defineProperty(el, 'clientHeight', { value: 500, configurable: true })
  Object.defineProperty(el, 'scrollHeight', { value: 1200, configurable: true })
  el.getBoundingClientRect = () => ({ top: 0, bottom: 500, left: 0, right: 360 })
}

/**
 * 每个场景要一份全新的模块实例：useKeyboardInset 的 baseline / installed
 * 都是**模块级**状态，复用同一个实例会让上一场的 baseline 污染下一场。
 * 加随机 query 让 ESM 缓存失效。
 */
let seq = 0
async function freshModule(win) {
  const g = globalThis
  g.window = win
  g.document = win.document
  g.getComputedStyle = win.getComputedStyle.bind(win)
  g.requestAnimationFrame = (fn) => setTimeout(fn, 0)
  g.cancelAnimationFrame = (id) => clearTimeout(id)
  g.matchMedia = () => ({ matches: false })
  const m = await import(`../useKeyboardInset.ts?n=${++seq}`)
  // keyboardHeight 是模块级 ref，没有直接导出，要经 useKeyboardInset() 取。
  return { ...m, kb: m.useKeyboardInset() }
}

/**
 * 等 measure() 里两条校正路径都跑完：rAF 两连（≈0ms）与 320ms 兜底。
 * 必须等够，否则定时器会打到下一个用例的 globals 上（那时 window 已经换人），
 * 或者在用例结束后触发 "asynchronous activity after the test ended"。
 */
function settle() {
  return new Promise((r) => setTimeout(r, 380))
}

/** 读回当前下发到 <html> 上的键盘态。 */
function readKbState(doc) {
  const root = doc.documentElement
  return {
    inset: root.style.getPropertyValue('--kb-inset'),
    open: root.classList.contains('kb-open'),
    resized: root.classList.contains('kb-resized'),
  }
}

const KEYBOARD = 300

test.afterEach(() => {
  delete globalThis.window
  delete globalThis.document
  delete globalThis.getComputedStyle
  delete globalThis.requestAnimationFrame
  delete globalThis.cancelAnimationFrame
  delete globalThis.matchMedia
})

// ---------------------------------------------------------------------------
// 1. overlay 路径：WebView 不缩，键盘直接盖视口
// ---------------------------------------------------------------------------

test('overlay 路径：WebView 不缩时 --kb-inset = 键盘高度，页面让位', async () => {
  const h = makeWindow({ innerHeight: 800, vvHeight: 800 })
  const m = await freshModule(h.win)
  m.installKeyboardInset()

  // 键盘弹起：visualViewport 缩了，innerHeight 没动
  h.resize({ vvHeight: 800 - KEYBOARD })

  assert.equal(m.kb.keyboardHeight.value, KEYBOARD, '键盘高度应被识别为 300')
  const st = readKbState(h.win.document)
  assert.equal(st.open, true, 'html.kb-open 必须置位（驱动 tabbar 滑走 / 槽位让位）')
  assert.equal(st.inset, `${KEYBOARD}px`, '#app 需要收缩键盘高度，底部输入区才贴得上键盘上沿')
  assert.equal(st.resized, false, 'overlay 路径不是原生 resize，不该打 kb-resized')
  await settle()
})

// ---------------------------------------------------------------------------
// 2. resize 路径：WebView 已经自己缩过 —— 再收一次就是双重收缩
// ---------------------------------------------------------------------------

test('resize 路径：WebView 已缩时 --kb-inset 必须为 0（否则双重收缩把内容顶飞）', async () => {
  const h = makeWindow({ innerHeight: 800, vvHeight: 800 })
  const m = await freshModule(h.win)
  m.installKeyboardInset()

  // 键盘弹起：两条路径都缩
  h.resize({ innerHeight: 800 - KEYBOARD, vvHeight: 800 - KEYBOARD })

  assert.equal(m.kb.keyboardHeight.value, KEYBOARD, '键盘高度仍应被识别为 300')
  const st = readKbState(h.win.document)
  assert.equal(st.open, true, 'kb-open 仍需置位')
  assert.equal(st.inset, '0px', '原生已缩，--kb-inset 必须为 0——这是两条路径最容易写错的一格')
  assert.equal(st.resized, true, 'kb-resized 标记要置上，供 CSS 区分两条路径')
  await settle()
})

// ---------------------------------------------------------------------------
// 3. 桌面 / 浏览器工具栏：不该误判成键盘
// ---------------------------------------------------------------------------

test('桌面浏览器（innerHeight === vv.height）整套机制 no-op', async () => {
  const h = makeWindow({ innerHeight: 900, vvHeight: 900 })
  const m = await freshModule(h.win)
  m.installKeyboardInset()

  assert.equal(m.kb.keyboardHeight.value, 0, '桌面不应被判为键盘在场')
  assert.equal(readKbState(h.win.document).open, false)
  await settle()
})

test('小幅视口变化（地址栏收缩 ~60px）不判为键盘弹起', async () => {
  const h = makeWindow({ innerHeight: 800, vvHeight: 800 })
  const m = await freshModule(h.win)
  m.installKeyboardInset()

  h.resize({ vvHeight: 800 - 60 })

  assert.equal(m.kb.keyboardHeight.value, 0, '60px < OPEN_THRESHOLD(120)，不该弹键盘态')
  assert.equal(readKbState(h.win.document).open, false)
  await settle()
})

// ---------------------------------------------------------------------------
// 4. 迟滞：临界抖动不来回抖
// ---------------------------------------------------------------------------

test('键盘已开后收到 30px 回落仍保持开启（迟滞区间不抖）', async () => {
  const h = makeWindow({ innerHeight: 800, vvHeight: 800 })
  const m = await freshModule(h.win)
  m.installKeyboardInset()

  h.resize({ vvHeight: 800 - KEYBOARD })
  assert.equal(m.kb.keyboardHeight.value, KEYBOARD)

  // 候选栏展开/收起引起的小幅回落
  h.resize({ vvHeight: 800 - KEYBOARD + 30 })

  assert.equal(m.kb.keyboardHeight.value, KEYBOARD - 30, '已开时按新高度更新，但不应判为收起')
  assert.equal(readKbState(h.win.document).open, true, '30px 回落仍在开启态（迟滞生效）')
  await settle()
})

test('键盘收起到 30px 残留判为关闭', async () => {
  const h = makeWindow({ innerHeight: 800, vvHeight: 800 })
  const m = await freshModule(h.win)
  m.installKeyboardInset()

  h.resize({ vvHeight: 800 - KEYBOARD })
  h.resize({ vvHeight: 800 - 30 })

  assert.equal(m.kb.keyboardHeight.value, 0, '30px <= CLOSE_THRESHOLD(60)，判为已收起')
  const st = readKbState(h.win.document)
  assert.equal(st.open, false)
  assert.equal(st.inset, '0px', '收起后 --kb-inset 必须归零，否则布局永久缺一块')
  await settle()
})

// ---------------------------------------------------------------------------
// 5. 承重的一条：聚焦字段必须被顶到键盘上沿之上
// ---------------------------------------------------------------------------

test('聚焦字段底部越过滚动容器下沿时，被顶回可见区（键盘不遮输入框）', async () => {
  const h = makeWindow({ innerHeight: 800, vvHeight: 800 })
  const doc = h.win.document

  // 长表单页最后一个字段：容器可视区 0..500，输入框落在 460..520，
  // 下沿 520 越过容器下沿 500 —— 正是「被键盘盖住」的形态
  const scroller = doc.createElement('div')
  const input = doc.createElement('textarea')
  scroller.appendChild(input)
  doc.body.appendChild(scroller)
  makeScrollable(h.win, scroller)
  input.getBoundingClientRect = () => ({ top: 460, bottom: 520, left: 0, right: 300 })

  const scrolls = []
  scroller.scrollBy = (arg) => scrolls.push(arg)

  const m = await freshModule(h.win)
  m.installKeyboardInset()
  input.focus()
  h.resize({ vvHeight: 800 - KEYBOARD })
  await settle()

  assert.ok(scrolls.length > 0, '聚焦字段越过下沿时必须发生一次滚动，否则键盘会盖住它')
  // 位移量 = 字段底 520 + 呼吸间距 12 - 容器底 500 = 32
  const scrolled = scrolls.filter((s) => s.top)
  assert.ok(scrolled.length > 0, '应发生一次纵向滚动')
  assert.equal(Math.round(scrolled[scrolled.length - 1].top), 32, '位移量应正好是「越过的那截 + 12px 呼吸间距」')
})

test('聚焦字段本来就在容器内可见时不做多余滚动', async () => {
  const h = makeWindow({ innerHeight: 800, vvHeight: 800 })
  const doc = h.win.document

  const scroller = doc.createElement('div')
  const input = doc.createElement('input')
  scroller.appendChild(input)
  doc.body.appendChild(scroller)
  makeScrollable(h.win, scroller)
  input.getBoundingClientRect = () => ({ top: 100, bottom: 140, left: 0, right: 300 })

  const scrolls = []
  scroller.scrollBy = (arg) => scrolls.push(arg)

  const m = await freshModule(h.win)
  m.installKeyboardInset()
  input.focus()
  h.resize({ vvHeight: 800 - KEYBOARD })
  await settle()

  assert.equal(scrolls.length, 0, '字段已完整可见，不该把页面推走（无谓跳动比不滚动更糟）')
})

// ---------------------------------------------------------------------------
// 6. 覆盖范围：非文本控件不该被当输入框顶来顶去
// ---------------------------------------------------------------------------

test('聚焦到按钮等非文本控件时不触发 reveal', async () => {
  const h = makeWindow({ innerHeight: 800, vvHeight: 800 })
  const doc = h.win.document

  const scroller = doc.createElement('div')
  const btn = doc.createElement('button')
  scroller.appendChild(btn)
  doc.body.appendChild(scroller)
  makeScrollable(h.win, scroller)
  btn.getBoundingClientRect = () => ({ top: 460, bottom: 520, left: 0, right: 300 })

  const scrolls = []
  scroller.scrollBy = (arg) => scrolls.push(arg)

  const m = await freshModule(h.win)
  m.installKeyboardInset()
  btn.focus()
  h.resize({ vvHeight: 800 - KEYBOARD })
  await settle()

  assert.equal(scrolls.length, 0, '按钮不是输入框，键盘也不会盖它，不该动滚动')
})
