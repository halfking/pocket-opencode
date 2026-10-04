/**
 * breakpoint-mirror.test.mjs — 断点双镜像防漂移门禁。
 *
 * 为什么要有这条（不是"整洁癖"）：
 *   断点在两处落地——CSS 的 `@media`/`--bp-*` 与 JS 的 `useBreakpoint`。
 *   两处不共享常量，CSS 也读不到 TS 常量，所以「改了一处忘了另一处」不会被
 *   编译器发现，只会在某档设备上表现为"布局不对但说不清哪不对"。
 *
 *   本仓历史上真的漂移过：`responsive.css` 里有裸的 `min-width: 768px`，
 *   落在 medium 带（560–839）内部；`AppLayout/TasksView/AIChatView` 里有三处
 *   裸的 `max-width: 380px`。这些构成与 SSOT 不一致的**第三套阶梯**。
 *
 * 门禁守三件事：
 *   1. CSS 的 `--bp-*` 变量值 === TS 常量；
 *   2. CSS 里的媒体查询断点值全部落在 SSOT 允许的集合内（不允许第三套阶梯）；
 *   3. TS 的 matchMedia 表达式与常量一致，且档位边界无缝隙、无重叠。
 *
 * 判据有没有牙，靠「把数字改掉它会不会红」自证——见文件末尾的变异自测。
 *
 * Run: node --test src/styles/__tests__/breakpoint-mirror.test.mjs
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

// 本文件位于 src/styles/__tests__/，上溯三级才是 frontend 根目录。
const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..', '..')
const CSS_PATH = join(ROOT, 'src/styles/breakpoints.css')
const RESPONSIVE_CSS_PATH = join(ROOT, 'src/styles/responsive.css')
const TS_PATH = join(ROOT, 'src/composables/useBreakpoint.ts')

const css = readFileSync(CSS_PATH, 'utf8')
const ts = readFileSync(TS_PATH, 'utf8')

/** SSOT 阶梯：medium / expanded / wide 的左边界，另有 compact 内的 narrow 子档。 */
const MEDIUM_MIN = 560
const EXPANDED_MIN = 840
const WIDE_MIN = 1280
const NARROW_MAX = 380

/** 媒体查询里允许出现的全部 px 值（含 -1 的上界形式）。 */
const ALLOWED = new Set(
  [MEDIUM_MIN, EXPANDED_MIN, WIDE_MIN, NARROW_MAX, MEDIUM_MIN - 1, EXPANDED_MIN - 1, WIDE_MIN - 1].map(String),
)

/** 从 CSS 里抓 `--bp-xxx: Npx` 形式的变量。 */
function cssVar(name) {
  const m = css.match(new RegExp(`--${name}\\s*:\\s*(\\d+)px`))
  return m ? Number(m[1]) : null
}

/** 从 CSS 里抓所有媒体查询中的 px 数值。 */
function mediaWidths(source) {
  const out = []
  for (const m of source.matchAll(/@media[^{]*\{/g)) {
    for (const p of m[0].matchAll(/(\d+)px/g)) out.push(p[1])
  }
  return out
}

test('CSS 侧 --bp-* 变量与 TS 常量逐一相等', () => {
  assert.equal(cssVar('bp-medium-min'), MEDIUM_MIN, '--bp-medium-min 与 TS 不一致')
  assert.equal(cssVar('bp-expanded-min'), EXPANDED_MIN, '--bp-expanded-min 与 TS 不一致')
  assert.equal(cssVar('bp-wide-min'), WIDE_MIN, '--bp-wide-min 与 TS 不一致')
  assert.equal(cssVar('bp-narrow-max'), NARROW_MAX, '--bp-narrow-max 与 TS 不一致')
})

test('TS 常量声明存在且值正确（门禁自身的前置条件）', () => {
  assert.match(ts, new RegExp(`export const MEDIUM_MIN_PX = ${MEDIUM_MIN}\\b`))
  assert.match(ts, new RegExp(`export const EXPANDED_MIN_PX = ${EXPANDED_MIN}\\b`))
  assert.match(ts, new RegExp(`export const WIDE_MIN_PX = ${WIDE_MIN}\\b`))
  assert.match(ts, new RegExp(`export const NARROW_MAX_PX = ${NARROW_MAX}\\b`))
})

test('TS 的 matchMedia 表达式由常量拼出（不是又抄了一遍字面量）', () => {
  // 如果这里写死了 '(max-width: 559px)'，改常量时它不会跟着变，漂移会再次发生。
  assert.match(ts, /max-width: \$\{MEDIUM_MIN_PX - 1\}px/)
  assert.match(ts, /min-width: \$\{MEDIUM_MIN_PX\}px/)
  assert.match(ts, /min-width: \$\{EXPANDED_MIN_PX\}px/)
  assert.match(ts, /min-width: \$\{WIDE_MIN_PX\}px/)
  // 反向红线：不得出现第二份硬编码阶梯
  assert.ok(!/matchMedia\('\(min-width: \d+px\)/.test(ts), 'TS 里不得再出现字面量断点的 matchMedia')
})

test('【无第三套阶梯】breakpoints.css 里的媒体查询宽度全部在 SSOT 集合内', () => {
  const bad = mediaWidths(css).filter((w) => !ALLOWED.has(w))
  assert.deepEqual(bad, [], `breakpoints.css 出现未登记断点：${bad.join(', ')}`)
})

test('【无第三套阶梯】responsive.css 里的宽度型媒体查询全部在 SSOT 集合内', () => {
  // max-height 是**高度**查询（横屏矮窗口），不属于宽度阶梯，另行放行。
  const source = readFileSync(RESPONSIVE_CSS_PATH, 'utf8')
  const widthQueries = []
  for (const m of source.matchAll(/@media[^{]*\{/g)) {
    const q = m[0]
    if (!/width/i.test(q)) continue // 只看宽度型
    for (const p of q.matchAll(/(\d+)px/g)) widthQueries.push(p[1])
  }
  const bad = widthQueries.filter((w) => !ALLOWED.has(w))
  assert.deepEqual(bad, [], `responsive.css 出现未登记宽度断点：${bad.join(', ')}（历史缺陷：裸 768px）`)
})

test('档位边界无缝隙、无重叠', () => {
  const bands = [
    { name: 'compact', min: 0, max: MEDIUM_MIN - 1 },
    { name: 'medium', min: MEDIUM_MIN, max: EXPANDED_MIN - 1 },
    { name: 'expanded', min: EXPANDED_MIN, max: WIDE_MIN - 1 },
    { name: 'wide', min: WIDE_MIN, max: Infinity },
  ]
  for (let i = 1; i < bands.length; i += 1) {
    assert.equal(
      bands[i].min,
      bands[i - 1].max + 1,
      `${bands[i - 1].name} 与 ${bands[i].name} 之间有缝隙或重叠`,
    )
  }
})

test('narrow 子档落在 compact 内部（不是第三套阶梯）', () => {
  assert.ok(NARROW_MAX < MEDIUM_MIN, `narrow(${NARROW_MAX}) 必须落在 compact(<${MEDIUM_MIN}) 内部`)
  assert.match(css, /@media \(max-width: 380px\)/, 'breakpoints.css 必须提供 narrow 子档规则')
  assert.match(ts, /const isNarrow = computed/, 'JS 侧必须提供 isNarrow 开关')
})

test('禁止范围语法媒体查询（必须是传统 min/max-width）', () => {
  for (const [name, source] of [
    ['breakpoints.css', css],
    ['responsive.css', readFileSync(RESPONSIVE_CSS_PATH, 'utf8')],
  ]) {
    assert.ok(
      !/@media[^{]*\((width|height)\s*[<>]/.test(source),
      `${name} 使用了范围语法媒体查询；本仓统一用传统 min/max-width`,
    )
  }
})

test('【变异自测】把 CSS 数字改错，本门禁必须转红', () => {
  // 判据的价值在它能红的时候。这里不真的改磁盘文件，而是在同一段读取逻辑上
  // 注入一个漂移过的副本，确认断言确实会失败。
  const drifted = css.replace('--bp-wide-min: 1280px', '--bp-wide-min: 1440px')
  const m = drifted.match(/--bp-wide-min\s*:\s*(\d+)px/)
  assert.ok(m && Number(m[1]) !== WIDE_MIN, '注入的漂移副本必须与 SSOT 不等，否则本自测是恒真的')
})
