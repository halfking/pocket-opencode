/**
 * handler-wiring.test.mjs — 「定义了却没接线」的处理器卡口。
 *
 * ## 它防的是哪一种事故
 *
 * 2026-10-06 设备实跑 UI-07（下拉刷新）时撞上：
 * `components/interactive/PullToRefresh.vue` 里
 * `handleTouchStart` / `handleTouchMove` / `handleTouchEnd` 三个函数
 * **写得完整、注释详尽、逻辑全对，却一个都没绑到模板上**；
 * `onMounted` 只绑了 `scroll`。
 *
 * ⇒ 后果不是报错，是**整套功能从未发生过**：橡皮筋、阈值、甩动判定、触觉、
 *   指示器三态全是死代码，「正在同步邮件…」永远不会出现。
 *   而 27 条 `continuousList` 单测 + `pull-gesture` 数学单测**全绿** ——
 *   它们测的是「函数算得对不对」，没人测「函数有没有被接上」。
 *   更糟的是量具也空过了：静止态与「手势没生效」时指示器文案**完全一样**。
 *
 * 这与 `check:dead-api`（导出了但无调用方的 API 客户端）是同一族：
 * **编译通过、类型通过、gates 全绿、运行时也不报错 —— 只是那件事没发生过。**
 *
 * ## 做法：棘轮，不是清零
 *
 * 同 `check:dead-api` 的理由：`WaveformVisualizer.vue` 的 `handleClick` 是
 * **可拖动波形旧设计的残留**（现役用法是录音电平条，且 `duration<=0` 时
 * 该函数直接 return），清掉属于另一个 PR 的事。
 * 本卡口保证**不再新增**，并把存量钉成**可核对的基线**。
 *
 * ## 一个刻意的设计：注释里的引用**不算**接线
 *
 * `PullToRefresh.vue` 的注释原文写着「模板上的 @touchmove 默认非 passive」
 * —— 描述的正是一个**不存在的绑定**。若把注释计入引用，
 * 这个门禁对真事故会完全失灵。
 * ⇒ 先剥注释再数引用。这也是本门禁与「grep 一下 handler 名字出现了没」的根本区别。
 *
 * Run: node --test src/components/__tests__/handler-wiring.test.mjs
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { readFileSync, readdirSync } from 'node:fs'
import { join, dirname, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { blankComments } from '../../styles/__tests__/style-scan-utils.mjs'

const SRC = join(dirname(fileURLToPath(import.meta.url)), '..', '..')

/** 已知的存量未接线项。每条写清「为什么它可以不接线」，空理由等于没登记。 */
const BASELINE = {
  'components/business/WaveformVisualizer.vue :: handleClick': {
    reason: '可拖动波形的旧设计残留。现役两处用法（NoteListView / MeetingStudioMenu）都把它当**录音电平条**（is-recording、120×28、show-time=false），且该函数在 props.duration<=0 时直接 return，绑上也不会有行为。⚠️ 附带一个待属主拍板的小缺陷：该元素 CSS 上有 cursor:pointer（UI 在暗示可点）但点了没反应——是改成可跳转还是去掉 pointer，属产品取舍。',
  },
}

/**
 * 剥注释。分两步，各有各的必要：
 *
 * ① HTML 模板注释 `<!-- -->`：**必须**剥。本轮那个真事故里，
 *    `PullToRefresh.vue` 的注释原文就在描述「模板上的 @touchmove」——
 *    注释里提到不算接线，否则这个门禁对真事故完全失灵。
 *
 * ② 其余注释：直接复用 `style-scan-utils.mjs` 的 `blankComments`，
 *    **不要再手写一份**。理由是本轮实吃两次：
 *      · 第一版把块注释折成单个空格 ⇒ 吃掉换行 ⇒ 后面的行注释规则
 *        从第一个 `//` 吃到文件尾，**模板区整个消失**。
 *      · 第二版修好换行，仍把 4 个已接线的处理器误报成未接线 ——
 *        因为 `accept="image/*"` 属性值里那一对「斜杠+星号」被当成块注释起点，
 *        从那里一路找到很后面的配对定界符才收，**196 行被抹平**。
 *        这正是 `blankComments` 当初被写出来的原因（它的注释里记着
 *        「NoteEditView.vue 里 7658 个字符的真实 CSS 被整段抹成空格」），
 *        它有引号感知：引号紧跟在 `=` / `:` / `(` 后才进入字符串态。
 *    ⇒ 同一个坑在这个仓里已经踩过一次并修好了，**复用，不要重造**。
 *
 * ⚠️ 本函数上面的 JSDoc 里**不能**原样写出「斜杠星号 … 星号斜杠」那对定界符：
 * 它会让这个块注释在这里就闭合，文件从下一行起按 JS 解析，
 * 报出来的是 `SyntaxError: Unexpected token '*'`，位置指向注释内部 ——
 * 症状完全不像「注释写坏了」。第一版真踩了。
 */
export function stripComments(src) {
  return blankComments(src.replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, ' ')))
}

/** 找出 `name` 的定义处之后，函数体覆盖的区间 [start, end)。 */
function bodySpan(text, name) {
  const defRe = new RegExp(`(?:const|let|function)\\s+${name}\\b`)
  const m = defRe.exec(text)
  if (!m) return null
  const braceAt = text.indexOf('{', m.index)
  if (braceAt < 0) return null // 简洁箭头体（`() => expr`）无法界定，按「算有引用」处理
  let depth = 0
  for (let i = braceAt; i < text.length; i += 1) {
    if (text[i] === '{') depth += 1
    else if (text[i] === '}') {
      depth -= 1
      if (depth === 0) return [braceAt, i]
    }
  }
  return null
}

/**
 * 找出一份 .vue 文本里「定义了却全文件再无引用」的 `handle*` / `on*` 处理器。
 *
 * @returns {string[]} `handleXxx` 形式的函数名
 *
 * 为什么只收 `handle*`/`on*` 前缀：这两类是「事件处理器」的命名惯例，
 * 它们的失效模式最隐蔽（写了、注释也写了、就是没绑）。
 * 收窄前缀是为了**少误报** —— 判据一旦吵，所有人就不看了。
 */
export function unwiredHandlers(src) {
  const text = stripComments(src)
  const defs = new Set()
  for (const m of text.matchAll(/(?:const|let|function)\s+((?:handle|on)[A-Z][A-Za-z0-9_]*)\s*(?:=|\()/g)) {
    defs.add(m[1])
  }
  const out = []
  for (const name of defs) {
    const hits = [...text.matchAll(new RegExp(`\\b${name}\\b`, 'g'))]
    if (hits.length <= 1) { out.push(name); continue }
    // ⚠️ **自引用不是接线**：递归处理器 `function f(n){ return f(n-1) }`
    // 会出现两次，若按「出现 ≥ 2 就算接上」判定，它会借自引用躲过本门禁。
    // ⇒ 排除「定义处本身」与「函数体区间内」的出现，只看**外部**引用。
    //   ⚠️ 定义处必须单独排除：它位于函数体区间**之前**（bodySpan 从 `{` 起算），
    //   不排掉的话 outside 恒 ≥ 1，递归处理器永远漏过（变异 5 当场抓到）。
    const span = bodySpan(text, name)
    if (!span) continue               // 界定不出函数体 ⇒ 保守当作有引用（宁可漏报不可误报）
    const defAt = text.indexOf(`${name}`)
    const outside = hits.filter((h) =>
      (h.index < span[0] || h.index >= span[1]) && h.index !== defAt)
    if (outside.length === 0) out.push(name)
  }
  return out
}

function walkVue(dir, acc = []) {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    if (e.name === 'node_modules' || e.name.startsWith('.')) continue
    const p = join(dir, e.name)
    if (e.isDirectory()) walkVue(p, acc)
    else if (e.name.endsWith('.vue')) acc.push(p)
  }
  return acc
}

/** 全仓扫描，返回 Map(`相对路径 :: handler` → 无)。 */
function scanAll(files = walkVue(SRC)) {
  const found = new Map()
  for (const f of files) {
    const rel = f.slice(SRC.length + 1).split(sep).join('/')
    for (const h of unwiredHandlers(readFileSync(f, 'utf8'))) found.set(`${rel} :: ${h}`, null)
  }
  return found
}

const ALL = scanAll()

// ===========================================================================
// 量具自证：判据的输入集合必须等于它声称的集合
// ===========================================================================

test('【量具自证】扫到了足够多的处理器（防解析器退化成 0 命中）', () => {
  let defined = 0
  for (const f of walkVue(SRC)) defined += unwiredHandlers(readFileSync(f, 'utf8')).length
  const allDefined = [...ALL.keys()].length
  assert.ok(allDefined >= 0, 'sanity')
  // 数量下限：把「扫到的东西太少」这件事本身判红，而不是让零违规静默通过。
  const total = countDefined()
  assert.ok(total >= 50, `全仓只扫到 ${total} 个事件处理器，说明扫描器退化了；本门禁的「零新增」在没量到东西时没有意义`)
  assert.ok(defined >= 0 && allDefined <= total, '内部一致性')
})

function countDefined() {
  let n = 0
  for (const f of walkVue(SRC)) {
    const text = stripComments(readFileSync(f, 'utf8'))
    for (const _ of text.matchAll(/(?:const|let|function)\s+((?:handle|on)[A-Z][A-Za-z0-9_]*)\s*(?:=|\()/g)) n += 1
  }
  return n
}

test('【量具自证 · 阳性对照】PullToRefresh 的三个手势处理器必须都被接上（证明扫描器看得见「接线」）', () => {
  // 这是本轮那个真事故的**回归靶子**。如果哪天有人把 @touchstart 等绑定
  // 当成冗余删掉，下面主判据也会红，但这条会**先**红且指名道姓。
  const f = join(SRC, 'components/interactive/PullToRefresh.vue')
  const bad = unwiredHandlers(readFileSync(f, 'utf8'))
  assert.deepEqual(bad, [],
    'PullToRefresh 的手势处理器又没被接上了 ⇒ 整套下拉刷新失效（2026-10-06 设备实跑抓到过）')
})

// ===========================================================================
// 主判据
// ===========================================================================

test('不得新增「定义了却没接线」的处理器（棘轮：存量见 BASELINE）', () => {
  const fresh = [...ALL.keys()].filter((k) => !(k in BASELINE))
  assert.deepEqual(fresh, [],
    '这些事件处理器写了却没有任何真实引用（注释里提到不算）⇒ 那段功能从未发生过。' +
    '要么把它接到模板上，要么删掉；确实不需要的话登记进 BASELINE 并写清理由。')
})

test('BASELINE 不得有陈旧条目（指向已删除或已接线的处理器）', () => {
  const stale = Object.keys(BASELINE).filter((k) => !ALL.has(k))
  assert.deepEqual(stale, [],
    'BASELINE 里有陈旧条目：对应处理器已被删除或已接线 ⇒ 请从基线里删掉它（否则基线会越养越宽松）')
})

test('BASELINE 每条都必须写了理由（空理由等于没登记）', () => {
  const noReason = Object.entries(BASELINE)
    .filter(([, v]) => !v.reason || !v.reason.trim())
    .map(([k]) => k)
  assert.deepEqual(noReason, [], 'BASELINE 条目缺少理由')
})

// ===========================================================================
// 变异自测：证明上面几条判据**有牙**
// ===========================================================================

test('【变异 1 · 必须转红】把 @touchstart 绑定删掉（复现本轮那个真事故）', () => {
  const src = readFileSync(join(SRC, 'components/interactive/PullToRefresh.vue'), 'utf8')
  const mutated = src.replace(/\s*@touchstart="handleTouchStart"/, '')
  assert.notEqual(mutated, src, '变异没生效 —— 源文件里已经没有 @touchstart 绑定，本用例失去意义')
  assert.ok(unwiredHandlers(mutated).includes('handleTouchStart'),
    '抽掉 @touchstart 后 handleTouchStart 必须变成「未接线」')
})

test('【变异 2 · 必须转红】处理器只在【注释里】被提到 —— 这正是真事故的形状', () => {
  // 若把注释计入引用，本门禁对 PullToRefresh 那次事故会完全失灵。
  const fake = `<template><div @click="onFoo">x</div></template>
<script setup>
function handleBar() { return 1 }
// 这里原本绑过 @click="handleBar"，后来被删了
</script>`
  assert.ok(unwiredHandlers(fake).includes('handleBar'),
    '注释里提到不算接线；本门禁必须抓这种「注释描述着不存在的绑定」')
})

test('【变异 3 · 必须保持绿】正常接线的处理器不得被报出（负对照）', () => {
  const ok = `<template><div @click="onFoo" @touchend="handleBar">x</div></template>
<script setup>
function handleBar() { return 1 }
function onFoo() { return 2 }
</script>`
  assert.deepEqual(unwiredHandlers(ok), [], '接了线的处理器被误报 ⇒ 本门禁只会一律判红，那是噪音不是判据')
})

test('【变异 4 · 必须保持绿】脚本内的其它引用也算接线（不只认模板绑定）', () => {
  // 处理器也可能被 addEventListener / 传给子组件 / defineExpose 用到，
  // 那同样是「接上了」。只认模板绑定会误报。
  const ok = `<template><div>x</div></template>
<script setup>
function handleBar() { return 1 }
onMounted(() => { el.value.addEventListener('click', handleBar) })
</script>`
  assert.deepEqual(unwiredHandlers(ok), [], 'addEventListener 也是接线，不该被报出')
})

test('【变异 5 · 必须转红】递归函数不应被误判成「有引用」', () => {
  // 自引用算「出现 2 次」，但那**不是**外部接线。
  // 这条把自引用排除掉，避免递归处理器借自引用躲过本门禁。
  const rec = `<template><div>x</div></template>
<script setup>
function handleRec(n) { return n > 0 ? handleRec(n - 1) : 0 }
</script>`
  const bad = unwiredHandlers(rec)
  assert.ok(bad.includes('handleRec'), '自引用不是接线：递归处理器仍应被判为未接线')
})
