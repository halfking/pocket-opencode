/**
 * unwired-exports.test.mjs — 「导出了但全仓无人引用」的 composable / shell 导出卡口。
 *
 * ## 它是 `handler-wiring.test.mjs` 的**粗粒度姊妹**
 *
 * 那一条查的是「事件处理器定义了却没绑到模板」；
 * 这一条查的是「整支 composable 写完了却没有任何消费者」。
 * 失败形态是同一句：**编译通过、类型通过、gates 全绿、运行时也不报错
 * —— 只是那件事从来没发生过。**
 *
 * 2026-10-06 设备实跑 UI-07 时在**函数级**抓到 `PullToRefresh.vue` 的三个
 * `handleTouch*` 未绑定，顺着同一条线往粗粒度扫，发现**模块级**也有一整簇：
 *
 * | 模块 | 行数 | 用途（源码头注释） | 消费者 | 单测 |
 * | --- | ---: | --- | --- | --- |
 * | `useViewport.ts` | 101 | 视口和方向感知 | 0 | 0 |
 * | `useLongPress.ts` | 68 | 长按手势（任务卡片上下文菜单等） | 0 | 0 |
 * | `usePullDownClose.ts` | 111 | 下拉手势关闭 BottomSheet / Modal | 0 | 0 |
 * | `useRealtimeList.ts` | 137 | 实时列表订阅 | 0 | 0 |
 * | `useDetailFlow.ts` | 204 | typed detail-page flow | 0 | 0 |
 * | `useVoiceRecording.ts` | 104 | 语音输入 | 0 | 0 |
 *
 * 合计 **725 行既无消费者、也无测试**的代码。
 *
 * ## 做法：棘轮，不是清零
 *
 * 与 `check:dead-api`（`src/api/` 的同类卡口）同一惯例：
 * 这些很可能是**有意的脚手架**（下一步就要接），一次清干净属于另一个 PR 的事。
 * 本卡口保证**不再新增**，并把存量钉成可核对的基线。
 * ⇒ 基线条目写的是「未接线」这个事实，**不是**「已确认要删」。
 *
 * ## 为什么「只被测试引用」不算违规
 *
 * `MAX_PULL_RATIO`、`NARROW_MAX_PX` 这类常量**只有单测和门禁在用**，
 * 它们是刻意导出的（门禁要钉住取值），不是死代码。
 * ⇒ 只有**连测试都不引用**的才进本卡口的违规集。
 *
 * Run: node --test src/composables/__tests__/unwired-exports.test.mjs
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { readFileSync, readdirSync } from 'node:fs'
import { join, dirname, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { blankComments } from '../../styles/__tests__/style-scan-utils.mjs'

const SRC = join(dirname(fileURLToPath(import.meta.url)), '..', '..')

/** 扫描范围：composables/ 与 lib/shell/（都是「能力」而非「数据」的所在地）。 */
const SCAN_ROOTS = ['composables', join('lib', 'shell')]

/**
 * 已确认的存量。按 `相对路径 :: 符号` 登记。
 * 理由只陈述**事实**（谁没在用、这意味着什么），不替属主做「删还是接」的判断。
 */
const BASELINE = {
  'composables/useViewport.ts :: useViewport':
    '101 行。视口/方向感知 composable，生产侧 0 消费者、0 单测。与 useBreakpoint 的 isNarrow「无组件使用」同族。',
  'composables/useLongPress.ts :: useLongPress':
    '68 行。长按手势检测（注释写明用于「任务卡片上下文菜单」），生产侧 0 消费者、0 单测。',
  'composables/usePullDownClose.ts :: usePullDownClose':
    '111 行。下拉手势关闭 BottomSheet/Modal。与 PullToRefresh 是同一手势族，但两个都未接线。',
  'composables/useRealtimeList.ts :: useRealtimeList':
    '137 行。实时列表订阅。',
  'composables/useRealtimeList.ts :: useNotesRealtime':
    '137 行同一模块的笔记变体。',
  'composables/useRealtimeList.ts :: useEmailsRealtime':
    '137 行同一模块的邮件变体。',
  'composables/useDetailFlow.ts :: useDetailFlow':
    '204 行。typed detail-page flow（注释标注为「optimization v4 的 PR10」），像是做到一半停下的脚手架。',
  'composables/useVoiceRecording.ts :: useVoiceRecording':
    '104 行。语音输入 composable。',
  'composables/useAutoGrowTextarea.ts :: useAutoGrow':
    'textarea 自增高。⚠️ 同名文件里另有别的导出在用，容易误以为它被用了 —— 必须按符号名精确核。',
  'composables/useFsrs.ts :: emptyFsrsCard':
    'FSRS 空卡构造。仅定义处引用。',
  'composables/scroll-chrome.ts :: isChromeBlankTap':
    'chrome 空白区点击判定。仅定义处引用（同一文件内其它符号在用）。',
  'composables/pull-gesture.ts :: pullOffset':
    '橡皮筋核心公式。与同文件其它 5 个常量不同 —— 那 5 个**有单测引用**（门禁钉着），只有这个连测试都没有。',
  'composables/useApprovalAlerts.ts :: ALERT_AFTER_MS':
    '审批提醒延时阈值。仅定义处引用。',
  'composables/useAttachments.ts :: MAX_DATA_URL_CHARS':
    '附件体积上限阈值。仅定义处引用。',
  'composables/useAttachments.ts :: MAX_TOTAL_PAYLOAD_CHARS':
    '附件总体积上限阈值。仅定义处引用。',
  'composables/useScrollHideChrome.ts :: CHROME_SNAP_DURATION_MS':
    '同文件另两个常量（BOUNCE_GUARD_MS / EDGE_REVEAL_DELAY_MS）**有单测引用**，只有这个没有。',
  'lib/shell/index.ts :: HYPER_PROTOCOL_VERSION':
    'Hyper 协议版本号常量。版本协商本身不依赖它（见 navigationContext），它是给外部/诊断用的声明。',
  'composables/use-list-sentinel.ts :: useListSentinel':
    'IntersectionObserver 哨兵（rootMargin 120px），**全仓 0 个真实消费者**。' +
    '⚠️ 2026-10-06 订正：原文写「同一件事有两套并行实现，⚠️ 接线前要先定到底用哪套」——' +
    '**那是迁移前的状态，已经过期**（同一条理由里引的 `useListScene` 也与本条目的 `useListSentinel` 不是同一个符号）。' +
    '剥注释后逐文件核对：三个列表页的真实消费者是 `useContinuousList`（3/3 已迁，迁移落在 2026-10-04 / 10-06），' +
    '这三页里残留的 `useListSentinel` 字样全是**迁移注释**，不是接线。' +
    '本文件剩 20 行（17 行有效代码）：继续挂在棘轮里即可，清不清是属主对一段 20 行墓碑的决定，不影响本卡口。',
  'composables/useSessionState.ts :: useSessionState':
    'typed mobile session state surface（注释标注「optimization v4 的 PR7」）。' +
    '⚠️ 它的**唯一**另一处出现是单测里的一句注释 `// … (matches aggregate() in useSessionState)`，' +
    '剥掉注释后引用数为 0 ⇒ 真的没被用。（我写门禁前的一次临时扫描把它算成「有测试引用」，' +
    '正是因为那条引用在注释里 —— 本卡口剥注释后给出的才是对数。）',
  'lib/shell/dockCoordinator.ts :: focusViewport':
    '吸顶层级的视口换算（纯几何）。**只有 `index.ts` 的桶再导出**在引用它。',
  'lib/shell/dockCoordinator.ts :: LAYER_BODY':
    '层级常量。同样**只有桶再导出**。同模块的 dockCoordinator 单测与 AppLayout 在用的其它导出都不受影响。',
}

function walk(dir, acc = [], filterFn = () => true) {
  let entries
  try {
    entries = readdirSync(dir, { withFileTypes: true })
  } catch {
    return acc
  }
  for (const e of entries) {
    if (['node_modules', 'dist', 'android', 'ios', '__tests__'].includes(e.name)) continue
    const p = join(dir, e.name)
    if (e.isDirectory()) walk(p, acc, filterFn)
    else if (/\.(ts|mjs)$/.test(e.name) && filterFn(e.name)) acc.push(p)
  }
  return acc
}

/**
 * 全仓可读的源文件（含测试 —— 测试的引用也要算数）。
 *
 * ⚠️ **必须排除本文件自己**（2026-10-06 实吃）。
 * 本文件的 `BASELINE` 与「阴性对照」用例里**逐字写着**那些死符号的名字
 * （`'composables/useViewport.ts :: useViewport'`），扫描器会把它们数成引用 ——
 * 于是 6 支 composable 全部「复活」，阴性对照当场转红。
 * 这与「递归处理器借自引用躲过门禁」是同一族，只是方向相反：
 * 那次是**假引用**让门禁漏报，这次是**真引用**（我自己）让门禁失灵。
 * ⇒ 任何以「符号名列表」驱动判据的门禁，都得先把自己从语料里剔掉。
 */
const SELF = new Set(['unwired-exports.test.mjs', 'handler-wiring.test.mjs'])

const ALL_SRC = (() => {
  const out = []
  const rec = (d) => {
    for (const e of readdirSync(d, { withFileTypes: true })) {
      if (['node_modules', 'dist', 'android', 'ios'].includes(e.name)) continue
      const p = join(d, e.name)
      if (e.isDirectory()) rec(p)
      else if (/\.(ts|vue|mjs)$/.test(e.name) && !SELF.has(e.name)) out.push(p)
    }
  }
  rec(SRC)
  return out
})()

/**
 * 剥掉**桶文件里的再导出**（`export { X } from './x.ts'` / `export * from`）。
 *
 * ⚠️ 2026-10-06：本卡口第一版把这种再导出当成了**消费者**，于是
 *   `lib/shell/dockCoordinator.ts` 的 `focusViewport` / `LAYER_BODY`
 *   —— 只在 `index.ts` 里被转手一次、**没有任何地方真正调用** ——
 *   被判成「有引用、活着」。
 * ⇒ 一行 `export { X } from` 就能让一个死符号对任何「有没有被引用」的
 *   静态判据隐形，包括这一条。**转手不是消费。**
 *
 * 为什么必须连换行一起涂掉：再导出常写成多行
 * （`export {\n  a,\n  b,\n} from './x.ts'`），逐行处理会漏。
 * 涂成空格而非删除，是为了不改变行号/长度，便于其它按行定位的判据复用。
 */
const stripReexports = (txt) =>
  txt.replace(/export\s+(?:type\s+)?(?:\*|\{[^}]*\})[\s\S]*?from\s+['"][^'"]+['"]\s*;?/g,
    (m) => m.replace(/[^\n]/g, ' '))

const SOURCES = ALL_SRC.map((f) => [f, stripReexports(blankComments(readFileSync(f, 'utf8')))])

/** 被扫描模块的导出符号。 */
function collectTargets() {
  const out = []
  for (const root of SCAN_ROOTS) {
    for (const f of walk(join(SRC, root))) {
      const rel = f.slice(SRC.length + 1).split(sep).join('/')
      const txt = readFileSync(f, 'utf8')
      const push = (name) => out.push({ file: f, rel, name })
      for (const m of txt.matchAll(/export\s+(?:async\s+)?function\s+([A-Za-z_$][\w$]*)/g)) push(m[1])
      for (const m of txt.matchAll(/export\s+const\s+([A-Za-z_$][\w$]*)\s*=/g)) push(m[1])
      for (const m of txt.matchAll(/export\s+class\s+([A-Za-z_$][\w$]*)/g)) push(m[1])
    }
  }
  return out
}

/** 该符号在**其它文件**里被引用了几次（注释已剥，`.vue` 的模板也计入）。 */
function refCount(name, selfFile) {
  const re = new RegExp(`\\b${name.replace(/\$/g, '\\$')}\\b`, 'g')
  let n = 0
  for (const [f, txt] of SOURCES) {
    if (f === selfFile) continue
    n += (txt.match(re) || []).length
  }
  return n
}

const TARGETS = collectTargets()
const DEAD = TARGETS.filter((t) => refCount(t.name, t.file) === 0).map((t) => `${t.rel} :: ${t.name}`)
const DEAD_SET = new Set(DEAD)

// ===========================================================================
// 量具自证
// ===========================================================================

test('【量具自证】扫描到了足够多的导出符号（防解析器退化成 0 命中）', () => {
  assert.ok(TARGETS.length >= 80,
    `只扫到 ${TARGETS.length} 个导出符号，扫描器可能退化了；本卡口的「零新增」在没量到东西时没有意义`)
  assert.ok(new Set(TARGETS.map((t) => t.rel)).size >= 10,
    '被扫描的模块太少，扫描范围可能写错了')
})

test('【量具自证 · 阳性对照】useBreakpoint 的常量必须被判为「有引用」', () => {
  // NARROW_MAX_PX 只有门禁/单测在用，但它**有**引用 ⇒ 不得进违规集。
  // 这一条证明本卡口不是「凡导出即判红」。
  const key = 'composables/useBreakpoint.ts :: NARROW_MAX_PX'
  assert.ok(TARGETS.some((t) => `${t.rel} :: ${t.name}` === key), '没扫到 NARROW_MAX_PX，扫描范围不对')
  assert.ok(!DEAD_SET.has(key), '只被测试引用的常量被判成了死代码 —— 本卡口会变成「凡导出即判红」的噪音')
})

test('【量具自证 · 阴性对照】本轮实测的那几支 composable 必须被判为死', () => {
  for (const key of [
    'composables/useViewport.ts :: useViewport',
    'composables/useLongPress.ts :: useLongPress',
    'composables/usePullDownClose.ts :: usePullDownClose',
    'composables/useRealtimeList.ts :: useRealtimeList',
    'composables/useDetailFlow.ts :: useDetailFlow',
    'composables/useVoiceRecording.ts :: useVoiceRecording',
  ]) {
    assert.ok(DEAD_SET.has(key), `${key} 应被判为「全仓无引用」却没判出来 —— 扫描器退化了`)
  }
})

// ===========================================================================
// 主判据（棘轮）
// ===========================================================================

test('不得新增「导出了但全仓无人引用」的符号（存量见 BASELINE）', () => {
  const fresh = DEAD.filter((k) => !(k in BASELINE))
  assert.deepEqual(fresh, [],
    '这些导出符号全仓（含单测与 .vue 模板）没有任何引用 ⇒ 那份能力从未被使用过。' +
    '要么接上消费者，要么删掉；确定保留的话登记进 BASELINE 并写清理由。')
})

test('BASELINE 不得有陈旧条目（指向已删除或已接线的符号）', () => {
  const stale = Object.keys(BASELINE).filter((k) => !DEAD_SET.has(k))
  assert.deepEqual(stale, [],
    'BASELINE 里有陈旧条目：对应符号已删除或已获得消费者 ⇒ 请从基线删掉（否则基线会越养越宽松）')
})

test('BASELINE 每条都必须写了理由（空理由等于没登记）', () => {
  const noReason = Object.entries(BASELINE)
    .filter(([, v]) => !v || !v.trim())
    .map(([k]) => k)
  assert.deepEqual(noReason, [], 'BASELINE 条目缺少理由')
})

// ===========================================================================
// 变异自测
// ===========================================================================

test('【变异 1 · 必须保持绿】refCount 必须能区分「有引用」与「无引用」（证明它真在数）', () => {
  // 第一版这条变异写错了：它把「定义文件」换成一个不存在的路径，
  // 但真实文件仍在语料里、仍被当作「其它文件」，于是计数照样 >0。
  // ⇒ 变异必须落在**语料**上，而不是落在参数上。
  const dead = TARGETS.find((t) => t.name === 'useViewport')
  const used = TARGETS.find((t) => t.name === 'useListScene')
  assert.ok(dead && used, '没找到对照符号（扫描范围变了？）')
  assert.equal(refCount('useViewport', dead.file), 0, 'useViewport 全仓无人引用，计数应是 0')
  assert.ok(refCount('useListScene', used.file) > 0,
    'useListScene 被 TasksView / SessionListView 等真实使用，计数必须 >0 —— 否则这个计数器恒为 0')
})

test('【变异 2 · 必须保持绿】注释里提到不算引用（否则「说明文档写得很全」会变成活着的假象）', () => {
  // blankComments 已剥注释；这里验证它对 .ts 同样生效。
  const withComment = 'const a = 1\n// see also: mySpecialHook\nconst b = 2\n'
  const stripped = blankComments(withComment)
  assert.ok(!stripped.includes('mySpecialHook'), '注释里的符号名没被剥掉 —— 会把「只在注释里提到」误判成有引用')
})

test('【变异 3 · 必须转红】BASELINE 的键拼错时「无陈旧条目」必须报警（防基线名不副实）', () => {
  const fakeBaseline = { 'composables/nope.ts :: nothing': '假的' }
  const stale = Object.keys(fakeBaseline).filter((k) => !DEAD_SET.has(k))
  assert.deepEqual(stale, ['composables/nope.ts :: nothing'],
    '「无陈旧条目」这条判据自己坏了')
})

// ===========================================================================
// 桶文件洞（2026-10-06 补）
// ===========================================================================

test('【量具自证】桶文件里的再导出**不算**消费者（否则死符号对门禁隐形）', () => {
  // 阳性：`dockCoordinator.ts` 自己的定义处不算引用；`index.ts` 的转手也不算。
  //   ⇒ 它的引用数必须是 0，才配进 BASELINE。
  const t = TARGETS.find((x) => x.name === 'focusViewport')
  assert.ok(t, '没扫到 focusViewport，扫描范围变了？')
  assert.equal(refCount('focusViewport', t.file), 0,
    'focusViewport 被判为「有引用」⇒ 桶再导出仍被当成了消费者，本卡口的洞没补上')
  // 前提自证：**不带**剥桶那一步时，它确实是「有引用」的 ——
  //   否则这条会变成「本来就没人引用，我这条判据什么都没证明」。
  const rawIdx = ALL_SRC.findIndex((f) => f.endsWith(join('lib', 'shell', 'index.ts')))
  assert.ok(rawIdx >= 0, '没找到 lib/shell/index.ts')
  const barrelRaw = blankComments(readFileSync(ALL_SRC[rawIdx], 'utf8'))
  assert.ok(/\bfocusViewport\b/.test(barrelRaw),
    '前提没了：index.ts 里已经不再转手 focusViewport ⇒ 本条失去了「桶伪装成消费者」的对照意义')
})

test('【变异 4 · 必须保持绿】stripReexports 对单行/多行/星号三种写法都生效', () => {
  const one = "import x from 'a'\nexport { focusViewport } from './dockCoordinator.ts'\nconst y = 1\n"
  assert.ok(!/focusViewport/.test(stripReexports(one)), '单行具名再导出没被剥掉')
  const many = "const z = 0\nexport {\n  focusViewport,\n  LAYER_BODY,\n} from './dockCoordinator.ts'\nconst w = 2\n"
  assert.ok(!/focusViewport/.test(stripReexports(many)), '多行具名再导出没被剥掉')
  const star = "const v = 0\nexport * from './dockCoordinator.ts'\n"
  assert.ok(!/from\s+'\.\/dockCoordinator\.ts'/.test(stripReexports(star)), '星号再导出没被剥掉')
  // 反证：不能把**正常代码**也吃掉
  const keep = "const a = 1\nconst focusViewport = 2\n// export { focusViewport } from 'x'\n"
  assert.ok(/const focusViewport = 2/.test(stripReexports(keep)), '剥桶把真实代码也吃掉了')
})
