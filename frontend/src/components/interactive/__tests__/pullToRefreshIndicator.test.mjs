/**
 * PullToRefresh 指示器的静止态几何（2026-10-03 模拟器 API 35 实测后补）。
 *
 * ## 复现的那一条
 *
 * 进入 App 首页（AI 工具）后，筛选 chip 区（`.work-filters`）上面压着一行
 * 半透明的灰字「下拉同步邮件」。CDP 实测的矩形直接坐实了重叠：
 *
 *     .refresh-indicator   top 165  bottom 221   （宽 168+，opacity 0.25）
 *     .work-filters        top 152  bottom 214
 *
 * 指示器是 `.refresh-content` 的**兄弟**节点，两者在 y=165~214 完全叠在一起。
 *
 * ## 根因
 *
 * `indicatorOffset = INDICATOR_HEIGHT - max(pullDistance, 0)`。静止时
 * `pullDistance = 0`，位移 = **+56px**——把一个 `top:0`、高 56px 的元素
 * 往下推 56px，正好推进内容区里。方向写反了。
 *
 * 配套的第二个问题：`indicatorOpacity = min(1, 0.25 + progress*0.75)`，
 * 静止时保底 **0.25**。位置错了还被这条下限「保证」看得见，于是半透明文字
 * 成了常驻。只修位置不修下限，静止时仍会留下一层 25% 残影。
 *
 * ## 判据
 *
 * 静止（pullDistance=0）时指示器必须**完全退到容器顶边之上**：
 * 位移 ≤ 0 且不透明度 = 0。两条都要，因为它们是同一个视觉缺陷的两半——
 * 位置与可见性任意一条不满足，用户就会看到文字压在内容上。
 *
 * 这组断言直接对**算式**求值，不启动组件：算式是纯 computed，
 * 把它当函数抽出来喂几个代表值，比渲染后量矩形更早暴露方向错误。
 */

import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { join } from 'node:path'
import test from 'node:test'
import assert from 'node:assert/strict'

const __dirname = fileURLToPath(new URL('.', import.meta.url))
const SRC_FILE = join(__dirname, '..', 'PullToRefresh.vue')

const INDICATOR_HEIGHT = 56

/**
 * 复刻组件里的两个算式。
 *
 * 直接抄写而不是 import：它们是 `<script setup>` 里的 computed，不是导出。
 * 抄写带来「和实现漂移」的风险，所以下面另有��条源码形状断言把两边钉在一起。
 */
function indicatorOffset(pullDistance) {
  return Math.min(0, Math.max(pullDistance, 0) - INDICATOR_HEIGHT)
}
/** progress = clamp(pullDistance / threshold, 0, 1) 的等价：这里只测端点。 */
function indicatorOpacity(pullDistance, threshold = 80) {
  const progress = Math.min(1, Math.max(pullDistance, 0) / threshold)
  return Math.min(1, progress * 1.4)
}

test('静止时指示器完全退到容器顶边之上（不得叠在内容上）', () => {
  const offset = indicatorOffset(0)
  assert.ok(
    offset <= -INDICATOR_HEIGHT,
    `静止时位移应为 -${INDICATOR_HEIGHT}（整块藏到顶边之上），实际 ${offset}。` +
      `位移为正会把指示器推进内容区——实测「下拉同步邮件」就叠在筛选 chip 上。`,
  )
})

test('静止时指示器不透明度为 0（位置对了也不能留残影）', () => {
  const op = indicatorOpacity(0)
  assert.equal(
    op,
    0,
    '静止时必须完全不可见。原来写死 min(1, 0.25 + progress*0.75) 保底 0.25，' +
      '于是半透明文字常驻在内容上——这是同一个视觉缺陷的第二半。',
  )
})

test('拉到阈值时指示器完整落进内容让出的缝里', () => {
  assert.equal(indicatorOffset(INDICATOR_HEIGHT), 0, '拉到满高时位移应为 0（完全露出）')
  assert.ok(indicatorOpacity(INDICATOR_HEIGHT) > 0.5, '此时应清晰可见')
})

test('超拉时钳在 0，不再继续下压到内容里', () => {
  assert.equal(indicatorOffset(300), 0, '超拉不得让位移变成正值把指示器推进内容区')
  assert.ok(indicatorOffset(100) <= 0, '中途任何一点都不得为正')
})

test('位移随下拉单调递增（跟手，不回退）', () => {
  const samples = [0, 10, 20, 30, 40, 50, 56, 80]
  for (let i = 1; i < samples.length; i++) {
    const prev = indicatorOffset(samples[i - 1])
    const cur = indicatorOffset(samples[i])
    assert.ok(
      cur >= prev,
      `下拉 ${samples[i]}px 时位移(${cur}) 不应小于 ${samples[i - 1]}px 时的位移(${prev})——` +
        '回退会造成指示器抖动',
    )
  }
})

test('源码形状与本文件的判据一致（防「实现改了、测试没改」）', async () => {
  const src = await readFile(SRC_FILE, 'utf8')

  assert.match(
    src,
    /indicatorOffset\s*=\s*computed\(\(\)\s*=>\s*\n?\s*Math\.min\(0,\s*Math\.max\(pullDistance\.value,\s*0\)\s*-\s*INDICATOR_HEIGHT\)/,
    'indicatorOffset 的算式与本文件复刻的不一致——实现改了，判据要一起改',
  )
  assert.doesNotMatch(
    src,
    /0\.25\s*\+\s*progress\.value\s*\*\s*0\.75/,
    '不透明度又带回了 0.25 保底：静止时会留一层 25% 残影压在内容上',
  )
})
