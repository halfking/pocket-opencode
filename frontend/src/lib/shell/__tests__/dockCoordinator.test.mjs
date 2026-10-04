/**
 * dockCoordinator 契约测试（UI规范 07 §4）。
 *
 * 守住的核心：吸顶坐标是**算出来的**，不是写死的「顶栏 48 + Tab 40」。
 * 判别动作是「把常量换成别的数字，输出必须跟着变」——如果换任何数字输出不变，
 * 说明实现里藏了常量，这条判据就恒绿了。
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import {
  computeDockMetrics,
  dockTranslateY,
  hasUndocked,
  shouldDockTab,
  sortByLayer,
  LAYER_TABLE_HEADER,
  LAYER_TAB_TOOLBAR,
} from '../dockCoordinator.ts'

test('effectiveTop 取「滚动视口上沿」与「可见顶栏下沿」的较大者', () => {
  // 顶栏高于视口上沿（吸顶中）→ 用顶栏
  assert.equal(
    computeDockMetrics({ scrollViewportTop: 0, visibleTopbarBottom: 56 }).effectiveTop,
    56,
  )
  // 视口上沿更高（顶栏还没吸住）→ 用视口
  assert.equal(
    computeDockMetrics({ scrollViewportTop: 80, visibleTopbarBottom: 40 }).effectiveTop,
    80,
  )
})

test('tableTop = effectiveTop + 停靠 Tab 高度 + 工具栏高度', () => {
  const m = computeDockMetrics({
    scrollViewportTop: 0,
    visibleTopbarBottom: 56,
    dockedTabHeight: 44,
    regionToolbarHeight: 36,
  })
  assert.equal(m.effectiveTop, 56)
  assert.equal(m.tabTop, 56)
  assert.equal(m.tableTop, 56 + 44 + 36)
})

test('【防写死】改变顶栏高度必须改变输出（常量实现会在这里露馅）', () => {
  const a = computeDockMetrics({ scrollViewportTop: 0, visibleTopbarBottom: 48 }).tableTop
  const b = computeDockMetrics({ scrollViewportTop: 0, visibleTopbarBottom: 72 }).tableTop
  assert.notEqual(a, b, '吸顶坐标不得与顶栏实测高度无关')
})

test('safe-area 不被二次相加：坐标里没有额外的 inset 常量', () => {
  // 顶栏下沿已含状态栏 inset（实测值）。若实现再加一次 statusBar 常量，
  // 同一份输入在「顶栏已含 inset」与「不含」两种解释下会差一个常数。
  const withInsetInside = computeDockMetrics({ scrollViewportTop: 0, visibleTopbarBottom: 56 }).tableTop
  const raw = computeDockMetrics({ scrollViewportTop: 0, visibleTopbarBottom: 56 }).tableTop
  assert.equal(withInsetInside, raw)
  assert.equal(withInsetInside, 56, 'tabTop 必须就是顶栏实测下沿，不得叠加任何常量')
})

test('负的 Tab/工具栏高度被夹到 0，不产生反向偏移', () => {
  const m = computeDockMetrics({
    scrollViewportTop: 0,
    visibleTopbarBottom: 50,
    dockedTabHeight: -10,
    regionToolbarHeight: -5,
  })
  assert.equal(m.tableTop, 50)
})

test('shouldDockTab：头到达目标线且内容仍穿过下沿才停靠', () => {
  assert.equal(shouldDockTab(50, 300, 50), true)
  assert.equal(shouldDockTab(60, 300, 50), false, '头未到目标线不 dock')
  assert.equal(shouldDockTab(50, 50, 50), false, '内容不再穿过下沿则 undock')
})

test('hasUndocked：离开整个区域后允许应用左滑返回', () => {
  assert.equal(hasUndocked(100, 0, 100), true)
  assert.equal(hasUndocked(40, 300, 100), false, '还在区域内不得判 undock')
})

test('dockTranslateY：亚像素抖动归零，避免停靠时抖一下', () => {
  const el = { getBoundingClientRect: () => ({ top: 50.3 }) }
  assert.equal(dockTranslateY(el, 50), 0, '差 0.3px 视为已对齐')
  const far = { getBoundingClientRect: () => ({ top: 20 }) }
  assert.equal(dockTranslateY(far, 50), 30)
})

test('层级排序：正文 < 表头 < Tab/工具栏（不固化成单一 z-index）', () => {
  const regions = [
    { id: 'tab', el: {}, kind: 'tab', layer: LAYER_TAB_TOOLBAR, docked: true },
    { id: 'body', el: {}, kind: 'toolbar', layer: 0, docked: false },
    { id: 'th', el: {}, kind: 'table-header', layer: LAYER_TABLE_HEADER, docked: true },
  ]
  assert.deepEqual(sortByLayer(regions).map((r) => r.id), ['body', 'th', 'tab'])
})

test('sortByLayer 不就地改参数（调用方常把结果缓存做 diff）', () => {
  const regions = [
    { id: 'b', el: {}, kind: 'tab', layer: 20, docked: true },
    { id: 'a', el: {}, kind: 'tab', layer: 10, docked: true },
  ]
  const sorted = sortByLayer(regions)
  assert.equal(regions[0].id, 'b', '原数组顺序不得被改')
  assert.equal(sorted[0].id, 'a')
})
