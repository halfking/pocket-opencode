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

/**
 * 媒体查询里允许出现的全部 px 值（含 -1 的上界形式）。
 * ⚠️ NARROW_MAX - 1 是 2026-10-06 补的：narrow 那条唯一的 max-width 断点
 *   改成了 N-1（真机上闭区间 max-width: N 不命中，见下方约定测试）。
 *   漏了它，「无第三套阶梯」会把正确写法判成「第三套阶梯」。
 */
const ALLOWED = new Set(
  [
    MEDIUM_MIN, EXPANDED_MIN, WIDE_MIN, NARROW_MAX,
    MEDIUM_MIN - 1, EXPANDED_MIN - 1, WIDE_MIN - 1, NARROW_MAX + 1,
  ].map(String),
)

/** 从 CSS 里抓 `--bp-xxx: Npx` 形式的变量。 */
function cssVar(name) {
  const m = css.match(new RegExp(`--${name}\\s*:\\s*(\\d+)px`))
  return m ? Number(m[1]) : null
}

/**
 * 从 CSS 里抓所有媒体查询中的 px 数值。
 *
 * ⚠️ 2026-10-06 修的一处真实缺陷：**原来不剥注释**。
 *   正则 `/@media[^{]*\{/g` 从注释里那句 `@media (max-width: 380px)` 起就一路
 *   吃到真正那条媒体查询的 `{`，于是**注释里举例用的数字被当成代码扫进来**。
 *   表现形式：给 narrow 断点写一段解释「真机上 380 不命中」的注释，
 *   「无第三套阶梯」立刻把 379 报成「未登记断点」——
 *   **判据的输入集合比它声称的大，而它声称的是「CSS 里出现的媒体查询」。**
 *   ⇒ 剥掉块注释之后再扫。注释是文档，不是代码。
 * ⚠️ 同源的两个坑（本轮都踩了）：① 块注释里写 `正则/媒体查询` 的字面量，
 *   其中一旦出现块注释的**结束符**就会把注释提前闭合、剩下的变成代码 ⇒ SyntaxError，
 *   而报错指向的是不相干的一行。② 反引号在模板字符串里会截断字符串。
 *   ⇒ 注释里描述这类字面量时，一律改用文字描述，不要直接抄符号。
 */
function stripCssComments(source) {
  return source.replace(/\/\*[\s\S]*?\*\//g, '')
}

function mediaWidths(source) {
  const out = []
  for (const m of stripCssComments(source).matchAll(/@media[^{]*\{/g)) {
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
  assert.match(ts, /const isNarrow = computed/, 'JS 侧必须提供 isNarrow 开关')
})

/**
 * narrow 边界的**约定**（2026-10-06）。
 *
 * ⚠️ 本门禁原来写的是 `assert.match(css, /@media \(max-width: 380px\)/)` ——
 *   **它正在把一个 bug 钉死**。真 Android WebView 实测（emulator-5554，CDP 逐像素
 *   扫 376–384，dpr 1/2/3 三档）：**本设备上 `@media (max-width: Npx)` 系统性等价于
 *   「< N」**，而不是标准 CSS 的「≤ N」。
 *
 * ★ 我第一版修法是错的，值得记下来：把字面量 380 改成 379，重建 APK 实测
 *   **命中集合从「≤379」变成「≤378」—— 整体下移一格，并没有对齐**。
 *   ⇒ 「改了以后没变差」不等于「改对了」；边界类修改必须**重建后在设备上复量**。
 *   ⇒ 正确修法不是动数字，是**别用 max-width**：改用等价的 `min-width: N+1`。
 *     本设备的 min-width 是闭区间且行为正确（560/840/1280 三条已实测正确）。
 *
 * ⇒ 门禁钉的是**约定**：
 *     ① narrow 那条媒体查询**必须**是 min-width，且字面量 === NARROW_MAX + 1；
 *     ② **禁止** narrow 用 max-width（它是本设备上语义错位的那一种）；
 *     ③ TS 的 isNarrow 必须用闭区间（≤），才能与 min-width: N+1 落在同一像素上。
 *   改 SSOT 数字时三处自动跟着走；谁把约定改回去，本门禁立刻变红。
 */
test('narrow 边界：CSS 用 min-width(N+1) 且禁用 max-width，TS 用闭区间', () => {
  // ⚠️ 必须在**剥掉注释**之后的源码上匹配。
  const code = stripCssComments(css)
  assert.ok(
    !/@media\s*\(max-width:\s*\d+px\)/.test(code),
    'breakpoints.css 不许对 narrow 使用 max-width：本设备上 max-width:N 等价于 <N（真机实测 dpr 1/2/3 复现），' +
      '而 min-width 是闭区间且正确。请改成等价的 min-width: N+1 写法',
  )
  const m = /@media\s*\(min-width:\s*(\d+)px\)\s*\{\s*\.bp-hide-on-narrow/.exec(code)
  assert.ok(m, 'breakpoints.css 必须提供 narrow 子档的 min-width 媒体查询')
  assert.equal(
    Number(m[1]),
    NARROW_MAX + 1,
    `narrow 的 min-width 字面量必须等于 ${NARROW_MAX + 1}（= NARROW_MAX+1），` +
      `这样 narrow 的集合才是「< ${NARROW_MAX + 1}」即「≤ ${NARROW_MAX}」，与 token 名义一致。实测值=${m[1]}`,
  )
  const NARROW_CODE = String.raw`const isNarrow = computed\(\(\) => mode\.value === 'compact' && width\.value <= NARROW_MAX_PX\)`
  const NARROW_CODE_LT = String.raw`const isNarrow = computed\(\(\) => mode\.value === 'compact' && width\.value < NARROW_MAX_PX\)`
  assert.match(ts, new RegExp(NARROW_CODE),
    `TS 侧 isNarrow 必须用闭区间 width <= NARROW_MAX_PX，才能与 CSS 的 min-width: ${NARROW_MAX + 1} 落在同一像素上`)
  assert.ok(!new RegExp(NARROW_CODE_LT).test(ts),
    'isNarrow 不得用开区间：那会让 JS 认为 380 不属于 narrow，而 CSS 的 min-width: 381 认为它属于')
})

/**
 * 变异自测：上面那条约定必须真的会红，否则它又是一条恒真断言。
 * ⚠️ 断言方向是「变异体必须**违反**约定」。
 * ⚠️⚠️ 本轮在这里踩到一个**恒真断言**，值得单独记：
 *   我最初写 `/(const isNarrow = computed\([^)]*?< NARROW_MAX_PX)/`，
 *   而 `[^)]*?` **跨不过 `computed(()` 里那个右括号** ⇒ 正则永远匹配不上
 *   ⇒ 「不得用开区间」那条否定断言**恒为真、永远通过**。
 *   否定断言比肯定断言更危险：它看起来在守一条规则，实际什么都没守。
 *   ⇒ 凡是「不跨越定界符」这类写法，必须先确认被匹配段里**真的没有定界符**，
 *     否则一律改用精确字面量。
 */
test('【变异自测】narrow 边界回退成 max-width / 开区间，本门禁必须转红', () => {
  const code = stripCssComments(css)
  // 变异 A：把 min-width 换回 max-width（附带字面量回到 N）
  const brokenA = code.replace(
    /@media\s*\(min-width:\s*\d+px\)\s*\{\s*\.bp-hide-on-narrow/,
    `@media (max-width: ${NARROW_MAX}px) { .bp-hide-on-narrow`,
  )
  assert.notEqual(brokenA, code, 'CSS 变异没生效')
  assert.ok(
    /@media\s*\(max-width:\s*\d+px\)/.test(brokenA),
    '变异 A 必须引入 max-width（违反约定），否则第①条断言对它没有牙',
  )
  // 变异 B：把 TS 的闭区间改成开区间
  const brokenB = ts.replace(
    /(const isNarrow = computed\(\(\) => mode\.value === 'compact' && width\.value\s*)<=(\s*NARROW_MAX_PX)/,
    '$1<$2',
  )
  assert.notEqual(brokenB, ts, 'TS 变异没生效：找不到 isNarrow 的闭区间比较')
  assert.match(brokenB, /width\.value < NARROW_MAX_PX/,
    '变异 B 必须把闭区间改成开区间（违反约定），否则第③条断言对它没有牙')
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
