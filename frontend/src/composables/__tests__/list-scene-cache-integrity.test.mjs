/**
 * 列表缓存与刷新的一致性（2026-10-02 审计）。
 *
 * 用户要求：「需要检查数据刷新与缓存是否正常」。
 *
 * ## 背景：KeepAlive 让「返回时重新拉数据」这件事不再自动发生
 *
 * App.vue 用 `<KeepAlive :include="LIST_CACHE_NAMES">` 缓存列表页。被缓存的视图
 * 在导航走时是**失活**而非卸载，所以 `onMounted` 不会重跑。要拿到最新数据，
 * 必须有一条激活期的刷新路径。合法的有三种：
 *
 *   1. `useListScene(scope, refresh)` —— 脏标记驱动（首选，见 list-scene-store）
 *   2. 自己写 `onActivated(...)` 拉数据
 *   3. 渲染的是共享 Pinia store 的响应式数据（详情页写同一个 store，列表自动跟随）
 *
 * 三种都没有 ⇒ 返回时看到旧数据。
 *
 * ## 本护栏抓到的真实 bug
 *
 * `TasksView` 在 LIST_CACHE_NAMES 里，但既没有 useListScene 也没有 onActivated，
 * 且 `accTasks` store 只提供 `{ lastTask, submitting }`（新建任务的 composer，
 * **不是列表 store**）；`/tasks/:id` 是独立路由，其 `TaskDetailView` 走裸 `api`
 * 且从不 markListDirty。⇒ 在详情页删掉任务后返回，列表里那条还在。
 * 已修：详情页三处写操作补 markListDirty('tasks')，列表页接 useListScene。
 *
 * ## 判据必须匹配「调用点」而不是「出现过这个词」
 *
 * 第一版用 `/onActivated/` 匹配，结果 TasksView 明明没有 onActivated 却判成有 ——
 * 因为我自己在注释里写了「之前没有任何 onActivated」，**注释里的裸词被当成了代码**。
 * 与密钥卡口那条「扫描器会匹配到自己的规则表」同源。现在一律要求紧跟 `(`。
 *
 * Run: node --test src/composables/__tests__/list-scene-cache-integrity.test.mjs
 */
import assert from 'node:assert/strict'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { dirname, join, extname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..', '..')
const SRC = join(ROOT, 'src')
const SKIP_DIRS = new Set(['node_modules', '.git', 'dist', 'android', 'ios'])

function collectVue(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (SKIP_DIRS.has(name)) continue
    const full = join(dir, name)
    if (statSync(full).isDirectory()) collectVue(full, out)
    else if (extname(name) === '.vue') out.push(full)
  }
  return out
}

const rel = (f) => relative(ROOT, f).replace(/\\/g, '/')
const vueFiles = collectVue(SRC)

/**
 * 剥掉注释再匹配。
 *
 * 两次都栽在这上面，值得单独记：
 *   1. 判据 `/onActivated/` 判 TasksView「有 onActivated」——其实没有，
 *      是我自己在注释里写了「之前没有任何 onActivated」。
 *   2. 修完又栽一次：把 `useListScene('tasks', handleRefresh)` 改成注释来做负控，
 *      断言 `/useListScene\(\s*'tasks'/` **照样匹配注释里的字面文本**，
 *      护栏没转红 —— 也就是说「把接线删掉只留注释」这种退化它抓不到。
 *
 * 结论：源码扫描型护栏若不剥注释，**注释可以满足任何断言**。
 * 代价：字符串里出现的 `//`（如 'https://'）会被行注释规则误伤，
 * 所以行注释的正则要求 `//` 前面不是 `:`。
 */
const stripComments = (t) =>
  t
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/[^\n]*/g, '$1 ')

const byText = new Map(vueFiles.map((f) => [rel(f), stripComments(readFileSync(f, 'utf8'))]))
const rawByText = new Map(vueFiles.map((f) => [rel(f), readFileSync(f, 'utf8')]))
const allText = [...byText.values()].join('\n')

const useListSceneSrc = stripComments(
  readFileSync(join(SRC, 'composables', 'use-list-scene.ts'), 'utf8'),
)
const cachedNames = [
  ...useListSceneSrc.match(/export const LIST_CACHE_NAMES = \[([\s\S]*?)\]/)[1].matchAll(/'([^']+)'/g),
].map((m) => m[1])

/** 被缓存但三种机制都没有的，逐条写明为什么它不会过期。 */
const NO_MECHANISM_REASON = new Map([
  [
    'EmailSummaryView',
    '只有 /email/summary 与 /email/summary/:date 两条路由，且**都渲染这个组件本身**' +
    '（router-mobile.ts:207 与 :213），不存在另一个会改数据的详情视图，' +
    '因此没有可过期的外部变更源。',
  ],
  [
    'FinanceView',
    '记账只有 /finance 一条路由（router-mobile.ts:180），没有 /finance/:id 详情路由，' +
    '也没有从别处进入的编辑页；所有写操作都发生在本视图内部，' +
    'KeepAlive 缓存的是它自己的最新状态。',
  ],
  [
    'SessionWorkspaceView',
    '纯壳：全文 151 行，无 ref / 无 onMounted / 无 api 调用，不持有任何数据。' +
    '会话数据由子组件 SessionConversationView 自己加载，' +
    '缓存这个壳没有任何可过期的状态。',
  ],
])

/** useListScene 的 scope 若没有 markListDirty 生产者，逐条写明为什么脏标记永远不会触发。 */
const NO_PRODUCER_REASON = new Map([
  [
    'email-invoices',
    '发票的详情是 InvoicePreviewSheet —— 同页 BottomSheet，**不是独立路由**，' +
    '列表数据无法从列表页之外被改动，所以永远不需要脏标记。',
  ],
  [
    'contacts',
    '/contacts/:id 的 ContactDetailView 是只读的：全文只有一个 load()，' +
    '没有任何写操作（已核对，无 update/remove/save 调用）。' +
    '⚠ 若将来给它加上编辑能力，必须同时补 markListDirty(\'contacts\')，' +
    '否则联系人列表会开始显示旧数据。',
  ],
  [
    'sessions',
    'SessionListView 是被缓存的 SessionWorkspaceView 的子组件（SessionWorkspaceView ' +
    '在 LIST_CACHE_NAMES 里，所以这份列表**确实**被缓存），但今天没有外部变更源：' +
    '唯一的写操作是删除，而它就发生在 SessionListView 内部并同步更新自己的 ' +
    'sessions.value；详情侧 SessionConversationView 不改会话元数据' +
    '（已核对，无 rename/archive/delete 调用）。' +
    '⚠ 这一条是「接了一半」：SessionListView:437 的注释已经承诺' +
    '「返回时只在详情侧登记过数据变更才刷新」，而详情侧并没有登记。' +
    '若将来在详情侧加会话改名/归档，必须同时补 markListDirty(\'sessions\')。',
  ],
])

describe('列表缓存与刷新的一致性', () => {
  it('判据与名单本身有效（防空跑通过）', () => {
    assert.ok(vueFiles.length > 100, `只扫描到 ${vueFiles.length} 个 .vue，范围可能失效`)
    assert.ok(
      cachedNames.length >= 10,
      `只从 LIST_CACHE_NAMES 解析出 ${cachedNames.length} 个名字，解析多半失效了`,
    )
    // 解析器必须真的认得 defineOptions 的写法，否则下面的对应关系会假通过
    const found = [...byText.values()].filter((t) => /defineOptions\(\{\s*name:\s*'/.test(t))
    assert.ok(found.length >= cachedNames.length,
      `defineOptions 判据只认出 ${found.length} 个，名单有 ${cachedNames.length} 个`)
  })

  it('白名单没有失效条目（视图被改名/删除时要立刻报出来）', () => {
    const stale = [...NO_MECHANISM_REASON.keys(), ...NO_PRODUCER_REASON.keys()].filter(
      (n) => !cachedNames.includes(n) && !allText.includes(n),
    )
    assert.equal(stale.length, 0, `豁免指向了不存在的名字：${stale.join(', ')}`)
  })

  it('LIST_CACHE_NAMES 里的每个名字都有对应的 defineOptions', () => {
    const missing = cachedNames.filter((n) => {
      const f = [...byText.entries()].find(([p]) => p.endsWith(`/${n}.vue`))
      return !f || !/defineOptions\(\{\s*name:\s*'/.test(f[1])
    })
    assert.equal(
      missing.length,
      0,
      `KeepAlive include 里的名字在源码中找不到 defineOptions({ name })：` +
      `${missing.join(', ')}；视图改名后缓存会静默失效（表现为滚动位置丢失 / 每次重进都重拉）`,
    )
  })

  it('每个被缓存的列表都有激活期刷新机制，否则返回时是旧数据', () => {
    const bad = []
    for (const name of cachedNames) {
      const f = [...byText.entries()].find(([p]) => p.endsWith(`/${name}.vue`))
      if (!f) continue
      const t = f[1]
      const hasScene = /useListScene\s*\(/.test(t)
      const hasActivated = /(?<![\w])onActivated\s*\(/.test(t)
      const hasStore = /from\s+['"][^'"]*\/stores\//.test(t)
      if (!hasScene && !hasActivated && !hasStore && !NO_MECHANISM_REASON.has(name)) {
        bad.push(`${f[0]}（useListScene/onActivated/共享 store 三者皆无）`)
      }
    }
    assert.equal(
      bad.length,
      0,
      `以下视图被 KeepAlive 缓存，却没有任何激活期刷新路径，` +
      `从详情页返回会看到旧数据：\n  - ${bad.join('\n  - ')}\n` +
      `修法：useListScene(scope, refresh) + 详情页 markListDirty(scope)，` +
      `或自写 onActivated，或改为渲染共享 store。确实不会过期的写进 NO_MECHANISM_REASON 并说明理由。`,
    )
  })

  it('每个 useListScene 的 scope 都有人登记脏标记', () => {
    const scopes = new Set(
      [...byText.values()].flatMap((t) => [...t.matchAll(/useListScene\(\s*'([^']+)'/g)].map((m) => m[1])),
    )
    const orphans = [...scopes].filter(
      (s) => !new RegExp(`markListDirty\\(\\s*'${s}'`).test(allText) && !NO_PRODUCER_REASON.has(s),
    )
    assert.equal(
      orphans.length,
      0,
      `这些 scope 被 useListScene 消费，却没有任何 markListDirty('scope') 登记：` +
      `${orphans.join(', ')}；脏标记永不触发，refresh 永远不会被调用`,
    )
  })

  it('回归护栏：任务列表的脏标记链路不能被拆掉', () => {
    // 本轮修的真实 bug：/tasks/:id 详情页三处写操作必须继续登记 'tasks'。
    const detail = [...byText.entries()].find(([p]) => p.endsWith('/tasks/TaskDetailView.vue'))
    assert.ok(detail, '找不到 TaskDetailView.vue')
    assert.match(detail[1], /markListDirty\('tasks'\)/,
      'TaskDetailView 必须继续 markListDirty(\'tasks\')，否则任务列表删除后仍显示旧条目')
    assert.match(detail[1], /api\.deleteTask\([\s\S]{0,300}markListDirty\('tasks'\)[\s\S]{0,120}router\.push/,
      '删除任务时必须**先**登记脏标记再 push，返回列表才会刷新')
    const list = [...byText.entries()].find(([p]) => p.endsWith('/tasks/TasksView.vue'))
    assert.match(list[1], /useListScene\(\s*'tasks'/,
      'TasksView 必须消费 \'tasks\' scope，否则激活期不会刷新')
  })
})
