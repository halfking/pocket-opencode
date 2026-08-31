/**
 * useScrollHideChrome — 滚动联动底部 chrome 引擎单测。
 * 运行：node --experimental-strip-types --test src/composables/__tests__/useScrollHideChrome.test.mjs
 *
 * 覆盖：跟手 1:1、比例吸附（隐/显）、快甩优先、顶部/底部边界强制展示、
 * suppress 程序化滚动抑制、pin 输入聚焦钉住、reveal/reset、maxHide=0 守卫。
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { createScrollHideChrome } from '../useScrollHideChrome.ts'

const SNAP_DELAY = 100
const SNAP_DURATION = 280
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

function create(maxHide = 120) {
  return createScrollHideChrome(() => maxHide)
}

/** 模拟一段连续滚动（事件间隔 16ms，模拟真机 60fps 滚动流） */
async function scrollBy(chrome, delta, steps = 1, scrollTopStart = 1000) {
  let top = scrollTopStart
  for (let i = 0; i < steps; i++) {
    top += delta
    chrome.reportScroll({ scrollTop: top, delta })
    if (i < steps - 1) await sleep(16)
  }
}

test('跟手 1:1：滚动增量直接映射隐藏量（clamp 到 maxHide）', async () => {
  const chrome = create(120)
  chrome.reportScroll({ scrollTop: 100, delta: 30 })
  assert.equal(chrome.hiddenOffset.value, 30)
  assert.equal(chrome.hidden.value, false, '跟手过程不改变吸附落定态')
  chrome.reportScroll({ scrollTop: 400, delta: 300 })
  assert.equal(chrome.hiddenOffset.value, 120)
  // 反向回弹同理
  chrome.reportScroll({ scrollTop: 390, delta: -10 })
  assert.equal(chrome.hiddenOffset.value, 110)
  // 落定态保持上一次吸附值，不随手翻动
  assert.equal(chrome.hidden.value, false)
})

test('比例吸附：位移 ≥35% 吸附全隐（快甩阈值未达，纯比例路径）', async () => {
  const chrome = create(120)
  // delta=43：43 < FLICK_HIDE(48)，排除快甩；43 ≥ 42(35%) 走比例隐藏
  chrome.reportScroll({ scrollTop: 143, delta: 43 })
  await sleep(SNAP_DELAY + 15)
  assert.equal(chrome.hiddenOffset.value, 120)
  assert.equal(chrome.snapping.value, true)
  assert.equal(chrome.hidden.value, true, '吸附全隐后落定态为 hidden')
  await sleep(SNAP_DURATION + 20)
  assert.equal(chrome.snapping.value, false)
})

test('比例吸附：位移 <35% 回弹全显', async () => {
  const chrome = create(120)
  // delta=30 < FLICK_HIDE(48) 且 < 42(35%)：吸附展示
  chrome.reportScroll({ scrollTop: 130, delta: 30 })
  await sleep(SNAP_DELAY + 15)
  assert.equal(chrome.hiddenOffset.value, 0)
  assert.equal(chrome.hidden.value, false)
})

test('快甩优先：120ms 内累计上甩 ≥32px 无视比例直接唤出', async () => {
  const chrome = create(120)
  await scrollBy(chrome, 100, 1)
  await sleep(SNAP_DELAY + 15)
  assert.equal(chrome.hiddenOffset.value, 120)

  // 上甩 -40（两次 -20）：offset 80 ≥ 42 本应保持隐藏，但快甩 -40 ≤ -32 唤出
  await scrollBy(chrome, -20, 2)
  await sleep(SNAP_DELAY + 15)
  assert.equal(chrome.hiddenOffset.value, 0)
})

test('快甩优先：120ms 内累计下甩 ≥48px 无视比例直接隐藏', async () => {
  const chrome = create(200) // 35% = 70
  // 两次 +30：offset 60 < 70 本应回弹展示，但快甩 +60 ≥ 48 隐藏
  await scrollBy(chrome, 30, 2)
  await sleep(SNAP_DELAY + 15)
  assert.equal(chrome.hiddenOffset.value, 200)
})

test('顶部（scrollTop≤1）强制展示', async () => {
  const chrome = create(120)
  await scrollBy(chrome, 100, 1)
  await sleep(SNAP_DELAY + 15)
  assert.equal(chrome.hiddenOffset.value, 120)

  chrome.reportScroll({ scrollTop: 0, delta: -3 })
  assert.equal(chrome.hiddenOffset.value, 0)
})

test('底部橡皮筋过度滚动强制展示', async () => {
  const chrome = create(120)
  await scrollBy(chrome, 100, 1)
  await sleep(SNAP_DELAY + 15)
  assert.equal(chrome.hiddenOffset.value, 120)

  chrome.reportScroll({ scrollTop: 500, delta: 3, overscrollBottom: true })
  assert.equal(chrome.hiddenOffset.value, 0)
})

test('suppress：程序化滚动窗口内不跟手也不吸附', async () => {
  const chrome = create(120)
  chrome.suppress(80)
  chrome.reportScroll({ scrollTop: 500, delta: 100 })
  assert.equal(chrome.hiddenOffset.value, 0, '抑制窗口内不参与跟手')
  await sleep(SNAP_DELAY + 80)
  assert.equal(chrome.hiddenOffset.value, 0, '抑制期内未武装吸附定时器')
  // 窗口过后恢复正常
  chrome.reportScroll({ scrollTop: 600, delta: 100 })
  assert.equal(chrome.hiddenOffset.value, 100)
})

test('setPinned：聚焦输入钉住展示，滚动不触发隐藏', async () => {
  const chrome = create(120)
  await scrollBy(chrome, 100, 1)
  await sleep(SNAP_DELAY + 15)
  assert.equal(chrome.hiddenOffset.value, 120)

  chrome.setPinned(true)
  assert.equal(chrome.hiddenOffset.value, 0, '钉住即唤出')

  await scrollBy(chrome, 50, 2)
  await sleep(SNAP_DELAY + 15)
  assert.equal(chrome.hiddenOffset.value, 0, '钉住期间滚动不隐藏')

  chrome.setPinned(false)
  await scrollBy(chrome, 50, 1)
  assert.equal(chrome.hiddenOffset.value, 50, '解除钉住后恢复跟手')
})

test('reveal：从底部唤出；已在展示态无操作', async () => {
  const chrome = create(120)
  chrome.reveal()
  assert.equal(chrome.snapping.value, false, '已展示时 reveal 不启动动画')

  await scrollBy(chrome, 100, 1)
  await sleep(SNAP_DELAY + 15)
  chrome.reveal()
  assert.equal(chrome.hiddenOffset.value, 0)
  assert.equal(chrome.snapping.value, true)
})

test('reset：清零隐藏态与动画标记', async () => {
  const chrome = create(120)
  await scrollBy(chrome, 100, 1)
  await sleep(SNAP_DELAY + 15)
  assert.equal(chrome.hiddenOffset.value, 120)

  chrome.reset()
  assert.equal(chrome.hiddenOffset.value, 0)
  assert.equal(chrome.snapping.value, false)
})

test('maxHide≤0（导航不渲染/无输入区）时全部 no-op', async () => {
  const chrome = createScrollHideChrome(() => 0)
  chrome.reportScroll({ scrollTop: 100, delta: 100 })
  assert.equal(chrome.hiddenOffset.value, 0)
  chrome.reveal()
  assert.equal(chrome.snapping.value, false)
})
