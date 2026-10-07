/**
 * 门禁：列表视图的**错误态不得遮蔽已加载的行**（2026-10-05，UI-07e 实机缺陷）
 *
 * ## 这条契约是什么
 *
 * 带下拉刷新的列表页，刷新失败时**必须保留已经显示的行**。失败提示只能：
 *   · 在**无行可显**时接管整页（`EmptyState`），或
 *   · 在有行时降级为**非破坏性**提示（横幅 / toast），行留在屏幕上。
 *
 * ## 它是怎么来的（不是「我觉得应该有个」）
 *
 * `SessionListView.vue` 的模板曾是 `loading / error / 列表` **三选一**
 * （`v-else-if="error"` 排在列表的 `v-else` 之前）⇒ 一次刷新失败就把整份列表
 * 换成整页错误态。**实机读数**（`emulator-5554`，`matrix ui-07`）：
 * `失败前 4 行 → 失败后 0 行`；而同一时刻后端 `GET /api/sessions` 仍返回那 2 个会话
 * ⇒ **数据从未离开服务器，是渲染层把它藏了**。已修（`error` 加
 * `&& filteredSessions.length === 0` 守卫 + 有行时改挂 `.list-error-banner`）。
 *
 * ⚠️ 修完扫面发现**同一形状的第二处**：`EmailInboxView.vue` 的
 * `v-else-if="loadError"` 同样无条件接管整页，而 `load()` 是**初次加载与
 * 下拉刷新共用**的入口 ⇒ 一并修掉。
 *
 * ⚠️ **为什么现有门禁没抓到**：`continuousList` 那条「刷新失败保留旧行」是绿的，
 * 但它守的是**连续加载的 store 层**，而这个缺陷在**视图模板层**。
 * ⇒ 判据与被测对象不同源时，**判据绿不能当作缺陷不成立的证据**。
 *
 * ## 判据形态
 *
 * 约定式：**挂在 `EmptyState` 上的错误分支，其条件必须同时引用「行数」**。
 * 不按文件名钉死具体表达式（那会随重构漂移），而是扫全部 `<PullToRefresh>` 视图：
 *   · 谁用了下拉刷新，就必须登记；
 *   · 登记为 `enforced` 的，错误分支必须带 `.length` 守卫；
 *   · 登记为 `n/a` 的，**零个**这样的分支（且必须写明为什么 n/a 仍成立）。
 * 新增下拉刷新视图而不登记 ⇒ 红。
 *
 * Run: node --test src/styles/__tests__/list-error-failpath.test.mjs
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { readFileSync, readdirSync } from 'node:fs'
import { join, dirname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..', '..')
const SRC = join(ROOT, 'src')

// ⚠️ ROOT 的层数是从 `topbar-chrome-gate.test.mjs` 抄的：**少写一层 `..` 时
// 不会抛错，只会枚举到一个不存在的目录或空集**，整份门禁静默变成「零违规」。
// 所以下面那条「量具自证」里硬性断言了枚举到的视图数 ≥5 —— 量具坏掉要能出声。

/** 只取 `<script` 之前的部分当模板区。
 *  ⚠️ 不能用「第一个 `</template>`」——视图里到处是 `<template v-else>` 块。 */
function templateOf(source) {
  const i = source.search(/<script\b/)
  return i === -1 ? source : source.slice(0, i)
}

function walk(dir, out = []) {
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, e.name)
    if (e.isDirectory()) walk(p, out)
    else if (e.name.endsWith('.vue')) out.push(p)
  }
  return out
}

/** 所有在**视图**里用 <PullToRefresh 的文件（组件自身不算）。 */
const pullViews = () =>
  walk(SRC)
    .filter((p) => !p.endsWith('components/interactive/PullToRefresh.vue'))
    .map((p) => ({ file: relative(SRC, p).split('\\').join('/'), text: readFileSync(p, 'utf8') }))
    .filter((v) => v.text.includes('<PullToRefresh'))

/**
 * 找「挂在 EmptyState 上的错误分支」。
 *
 * ⚠️⚠️ 条件要从 `<EmptyState` **向下**扫同一个开标签的属性，**不能往上回溯**。
 * Vue 模板里 `v-else-if` 是紧跟在 `<EmptyState` **下一行**的属性
 * ```
 *   <EmptyState
 *     v-else-if="error && filteredSessions.length === 0"
 * ```
 * 我第一版往上找，量到的**全是上一个元素的条件** ——
 * 于是 ① 报出一条根本不存在的违规（把上一段横幅的 `v-if="error"` 认到了空态上），
 * ② 更糟：加了「空行处停下」的边界后，**真正的错误分支一条都没被检查**，整条门禁变假绿，
 * 而 M1 立刻抓到了这一点。**归属必须落在被测对象自己那段里，方向也要对。**
 *
 * @returns {{file:string, cond:string, line:number}[]}
 */
function unguardedErrorEmptyStates(views) {
  const out = []
  for (const v of views) {
    const lines = templateOf(v.text).split('\n')
    for (let i = 0; i < lines.length; i += 1) {
      if (!/<EmptyState\b/.test(lines[i])) continue
      // 从本行起向下扫，直到这个开标签闭合（`>`）
      let cond = null
      for (let k = i; k < lines.length && k <= i + 12; k += 1) {
        const m = /\bv-(?:else-)?if="([^"]*)"/.exec(lines[k])
        if (m) { cond = m[1]; break }
        if (k > i && /^\s*>/.test(lines[k])) break       // 开标签闭合，后面都不是它的属性
        if (k > i && lines[k].trim() === '') break
      }
      if (!cond) continue
      if (!/error/i.test(cond)) continue
      if (/\.length\s*(===|==|>|!==|!=)/.test(cond)) continue   // 有「无行」守卫
      out.push({ file: v.file, cond, line: i + 1 })
    }
  }
  return out
}

/**
 * 登记表：每个下拉刷新视图都要在这里表态。
 * `enforced` = 它的错误分支必须带 `.length` 守卫；`n/a` = 该视图不存在这种分支。
 */
const FAILPATH_VIEWS = [
  {
    file: 'features/sessions/SessionListView.vue',
    state: 'enforced',
    why: '实机缺陷原址：error 无条件接管整页，刷新失败即清空已显示的行（UI-07e）',
  },
  {
    file: 'features/email/EmailInboxView.vue',
    state: 'enforced',
    why: '与上条同形状的第二处：load() 为初载与下拉刷新共用入口，loadError 无条件接管整页',
  },
  {
    file: 'features/email/EmailFolderListView.vue',
    state: 'n/a',
    why: '唯一的 error 条件是 BottomSheet 内的 createError（表单字段级提示），不接管整页',
  },
  {
    file: 'features/tasks/TasksView.vue',
    state: 'n/a',
    why: 'accStore.error 渲染在弹窗内的 .acc-error，是行内提示，不接管整页',
  },
  {
    file: 'features/meetings/MeetingListView.vue',
    state: 'n/a',
    why: '模板里没有任何 error 参与的条件（空态只由 meetings.length === 0 决定）',
  },
  // ↓ demo/首页类：用了下拉刷新，但**整页没有 EmptyState**，也就没有「错误态接管整页」这回事
  { file: 'pages/Home.vue', state: 'n/a', why: '首页外壳，模板里 0 个 EmptyState' },
  { file: 'pages/HomePage.vue', state: 'n/a', why: '同上：0 个 EmptyState' },
  { file: 'pages/ComponentDemo.vue', state: 'n/a', why: '组件演示页，0 个 EmptyState' },
  { file: 'pages/OptimizedDemo.vue', state: 'n/a', why: '同上：0 个 EmptyState' },
]

// ---------------------------------------------------------------------------
// 判据本体
// ---------------------------------------------------------------------------

test('错误态不得遮蔽已加载的行：挂在 EmptyState 上的错误分支必须带「无行」守卫', () => {
  const bad = unguardedErrorEmptyStates(pullViews())
  assert.deepEqual(
    bad.map((b) => `${b.file}:${b.line}（条件：${b.cond}）`),
    [],
    '这些 EmptyState 的错误分支没有「行数」守卫 ⇒ 刷新失败时它们会替换掉已经显示的行。' +
    '约定：接管整页只在**无行可显**时允许；有行时改挂非破坏性提示（见 SessionListView 的 .list-error-banner）。',
  )
})

test('下拉刷新视图必须全部登记（新增视图不登记就红）', () => {
  const known = new Set(FAILPATH_VIEWS.map((v) => v.file))
  const files = pullViews().map((v) => v.file)
  assert.deepEqual(files.filter((f) => !known.has(f)), [],
    '这些视图用了 <PullToRefresh> 但没在 FAILPATH_VIEWS 登记 ⇒ 没人确认过它们的失败路径')
  assert.deepEqual([...known].filter((f) => !files.includes(f)), [],
    'FAILPATH_VIEWS 里有陈旧条目：对应视图已删除或不再用下拉刷新了')
})

test('登记表每条都必须写了理由（空理由等于没登记）', () => {
  const bad = FAILPATH_VIEWS.filter((v) => !v.why || !v.why.trim() || v.state !== 'enforced' && v.state !== 'n/a')
  assert.deepEqual(bad.map((v) => `${v.file}（state=${v.state}）`), [],
    'FAILPATH_VIEWS 条目缺理由，或 state 取值不是 enforced / n/a')
})

test('登记为 n/a 的视图必须**当下仍然**没有这类分支（n/a 会悄悄过期）', () => {
  const byFile = new Map(pullViews().map((v) => [v.file, v]))
  const na = FAILPATH_VIEWS.filter((v) => v.state === 'n/a')
  const becameApplicable = []
  for (const v of na) {
    const src = byFile.get(v.file)
    if (!src) continue
    for (const b of unguardedErrorEmptyStates([src])) {
      becameApplicable.push(`${v.file}:${b.line}（条件：${b.cond}）—— 原登记理由：${v.why}`)
    }
  }
  assert.deepEqual(becameApplicable, [],
    '登记为 n/a 的视图长出了「会遮蔽已加载行」的错误分支 ⇒ 它现在属于 enforced，须改登记并修代码')
})

// ---------------------------------------------------------------------------
// 量具自证 + 变异自测
// ---------------------------------------------------------------------------

test('【量具自证】枚举器确实采到了全部下拉刷新视图与两处 enforced（防退化成 0 命中）', () => {
  const files = pullViews().map((v) => v.file)
  assert.ok(files.length >= 5,
    `只采到 ${files.length} 个下拉刷新视图（期望 ≥5）⇒ 棘轮覆盖不全，扫不到新增的视图`)
  const enforced = FAILPATH_VIEWS.filter((v) => v.state === 'enforced')
  assert.equal(enforced.length, 2,
    `enforced 应为 2 处（SessionListView / EmailInboxView），实际 ${enforced.length} ⇒ 登记表与现实脱节`)
  // 采到的空态分支必须真的能被枚举到，否则「零违规」是恒真
  const all = unguardedErrorEmptyStates(pullViews())
  assert.equal(all.length, 0, '未改动时就不该有违规分支，否则下面变异的红来自「本来就红」')
  // 反向自证：守卫确实能被识别（把守卫去掉就应被枚举到）
  const probe = [{
    file: '__probe__/x.vue',
    text: '<template><EmptyState\n  v-else-if="loadError"\n  icon="⚠️"\n/></template><script setup lang="ts"></script>',
  }]
  assert.equal(unguardedErrorEmptyStates(probe).length, 1,
    '注入一个无守卫的错误空态却枚举不到 ⇒ 枚举器坏了，「零违规」毫无意义')
})

test('【变异 M1 · 必须转红】把 SessionListView 的「无行」守卫拿掉（复现 UI-07e 那个真缺陷）', () => {
  const views = pullViews().map((v) => (v.file !== 'features/sessions/SessionListView.vue' ? v : {
    ...v,
    text: v.text.replace('v-else-if="error && filteredSessions.length === 0"', 'v-else-if="error"'),
  }))
  const bad = unguardedErrorEmptyStates(views)
  assert.ok(bad.some((b) => b.file === 'features/sessions/SessionListView.vue'),
    '拿掉守卫却没判红 ⇒ 本门禁无牙')
})

test('【变异 M2 · 必须转红】把 EmailInboxView 的守卫拿掉（同一形状的第二处）', () => {
  const views = pullViews().map((v) => (v.file !== 'features/email/EmailInboxView.vue' ? v : {
    ...v,
    text: v.text.replace('v-else-if="loadError && shownEmails.length === 0"', 'v-else-if="loadError"'),
  }))
  assert.ok(unguardedErrorEmptyStates(views).some((b) => b.file === 'features/email/EmailInboxView.vue'),
    '第二处没被判红 ⇒ 判据只认得我登记的那一处，不是「扫全部视图」')
})

test('【变异 M3 · 必须转红】往一个登记为 n/a 的视图里注入无守卫错误空态（证明规则不只认我改过的文件）', () => {
  const target = 'features/tasks/TasksView.vue'
  const views = pullViews().map((v) => (v.file !== target ? v : {
    ...v,
    text: v.text.replace('<script', `<template><EmptyState v-else-if="loadError" icon="⚠️" :title="loadError" /></template>\n<script`),
  }))
  assert.ok(unguardedErrorEmptyStates(views).some((b) => b.file === target),
    '注入到未改过的视图却没判红 ⇒ 规则被写成了「只查那两个文件」')
})

test('【变异 M4 · 必须保持绿】行内（非 EmptyState）错误提示不受管辖（避免过度管辖）', () => {
  const views = pullViews().map((v) => (v.file !== 'features/tasks/TasksView.vue' ? v : {
    ...v,
    text: v.text.replace('<script', `<template><p v-if="loadError" class="err">{{ loadError }}</p></template>\n<script`),
  }))
  assert.equal(unguardedErrorEmptyStates(views).length, 0,
    '行内错误提示被误报 ⇒ 本门禁只会「一律判红」，那不是判据而是噪音')
})
