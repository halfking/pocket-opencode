/**
 * Document 插件绑定的纯逻辑单测（2026-10-01 内置文档能力）。
 *
 * 覆盖 renderWidthForViewport 的分辨率夹取契约——它决定原生 PdfRenderer
 * 栅格化出的位图宽度，夹错了要么糊、要么把内存打爆，属于真机可回归的判据。
 *
 * hasNativeDocumentSupport 只在 android 为真：web/iOS/harmony 没有 PdfRenderer
 * 与 MediaStore 落盘实现，调用方必须回退（web/iOS 走 <iframe>）。
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'

const originalWindow = globalThis.window

function installWindow(dpr) {
  if (dpr === undefined) delete globalThis.window
  else globalThis.window = { devicePixelRatio: dpr }
}

function restoreWindow() {
  if (originalWindow === undefined) delete globalThis.window
  else globalThis.window = originalWindow
}

const doc = await import('../document.ts')

test('栅格化宽度在没有 window 时按 dpr=2 兜底', () => {
  installWindow(undefined)
  assert.equal(doc.renderWidthForViewport(360), 720)
  restoreWindow()
})

test('栅格化宽度按 devicePixelRatio 放大', () => {
  installWindow(3)
  assert.equal(doc.renderWidthForViewport(360), 1080)
  restoreWindow()
})

test('栅格化宽度下限 320：再窄的容器也要能认出字', () => {
  installWindow(1)
  assert.equal(doc.renderWidthForViewport(10), 320)
  restoreWindow()
})

test('栅格化宽度上限 2400：超大屏不把位图打到 OOM', () => {
  installWindow(3)
  assert.equal(doc.renderWidthForViewport(4000), 2400)
  restoreWindow()
})

test('异常 devicePixelRatio（0/NaN/undefined）不会产生非法宽度', () => {
  for (const dpr of [0, Number.NaN, undefined]) {
    installWindow(dpr)
    const w = doc.renderWidthForViewport(360)
    assert.ok(Number.isInteger(w) && w >= 320 && w <= 2400, `dpr=${dpr} -> ${w}`)
  }
  restoreWindow()
})

test('内置文档能力只在 Android 声明可用', () => {
  // node 端没有 Capacitor 平台信息，等价于 web：必须为 false，
  // 否则预览会误走 PdfRenderer 分支并在非 Android 平台静默失败。
  installWindow(undefined)
  assert.equal(doc.hasNativeDocumentSupport(), false)
  restoreWindow()
})
