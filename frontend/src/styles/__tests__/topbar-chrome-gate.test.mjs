/**
 * topbar-chrome-gate.test.mjs — 顶部 chrome 让位 / 不被压缩门禁。
 *
 * 与 `bottom-chrome-gate.test.mjs` 是**严格对称的两半**：那份管底部
 * （谁该给底栏让位），这份管顶部。两边必须同时存在，否则就是
 * 「底栏有守卫、顶栏裸奔」—— 本文件就是 2026-10-06 补上的那一半。
 *
 * 两条契约，各自对应一个**已定位的根因**：
 *
 * ## 契约 A：.top-bar 不得被 flex 压缩
 *
 * `.app-layout` 是 `display:flex; flex-direction:column`，`.top-bar` 是它的
 * flex item。`.top-bar` 声明了 `height: var(--topbar-height)`（48px），
 * **但 `height` 只声明基准尺寸，不阻止压缩**（CSS Flexbox §9.7：flex item 的
 * 用尺寸可以偏离其指定尺寸），而 `flex-shrink` 的初始值就是 `1`。
 *
 * ⇒ 矮视口下压缩按基准尺寸**按比例分摊**，顶栏跟着缩。设备实测：
 *     竖屏 914px 高 → h=48 ✓
 *     横屏 411px 高 → h=45 ✗
 *     分屏 500px 高 → h=45 ✗
 *   判据：scripts/device-matrix.mjs 的 UI-06d（token 值 vs 实测高度）。
 *
 * ⚠️ 这条最容易被当成「冗余声明」在 code review 里被删掉 —— 它看起来什么
 * 都没做。注释已写在 AppLayout.vue 里，门禁是它的第二道保险。
 *
 * ## 契约 B：用 --topbar-height 做 fixed 顶定位的元素必须让开 --app-safe-top
 *
 * `position: fixed` 的包含块是**视口**（本文件下方有自证：祖先链静止态没有
 * transform/filter/contain），而顶栏的**底边**在 `--app-safe-top + --topbar-height`
 * —— 安全区是 body 的 padding-top 顶下来的（styles.css:88），顶栏自己**不加**
 * 内边距（AppLayout.vue 的注释明写「再加会双重下移」）。
 *
 * ⇒ 只写 `top: calc(var(--topbar-height) + 8px)` 的元素，在有状态栏的设备上
 *   会与顶栏重叠。模拟器实测安全区 24px、顶栏 48px ⇒ 重叠 16px，
 *   且 `--z-fab`(60) > `--z-sticky`(50)，是浮层**画在上面**而不是被盖住。
 *
 * ⚠️ 这条**当前无可见症状**，但原因比「只在录音时挂载」复杂（2026-10-05 订正）：
 * `.summary-panel` 全仓有**两个**实现，两套不同的定位契约 ——
 *   ① `features/meetings/LiveSummaryPanel.vue:83` `position: fixed;
 *      top: calc(var(--topbar-height) + var(--app-safe-top) + var(--space-2))`
 *      —— **本门禁守的就是这一条**。它只在录音激活时挂载
 *      （`SessionLiveRecordPanel` 的 `v-if="liveRecord.active"`），
 *      而矩阵跑的路由永远不渲染它 ⇒ 属「潜伏」。
 *   ② `features/sessions/SessionSummaryRail.vue:145` `position: absolute; top: 0`
 *      —— **不在本门禁的守护范围内**，但它**结构上不可能压顶栏**：
 *      它的包含块是 `.summary-root { position: relative }`，而 `.summary-root`
 *      位于页面主体内、顶栏**之下** ⇒ `top: 0` = 顶栏底边。
 *      它在 `#/sessions` 上**可达**（`:92` 的收起条 `@click="open = true"`），
 *      但需要该会话有消息，而 `#/sessions` 当前 0 行。
 * ⇒ 所以「`.summary-panel` 只在录音激活时挂载」这句是**不准确的**（漏了 ②），
 *   而 `.alert-toast` 那半句仍然成立：`MeetingAlertToast.vue` 剥注释后
 *   **外部引用为 0**，是真不可达。两者混成一句会把「不可达」和「靠结构安全」搞混。
 *
 * ✅ **本段那个「债」已于 2026-10-05 还掉**（见文件末尾「契约 C」）：
 *   ② 改 `fixed`、`.summary-root` 丢掉 `position: relative`、以及未来的
 *   第三个实现 / 变体，现在都会被契约 C 判红（真实文件变异已验证：把 ② 改成
 *   `position: fixed` ⇒ 门禁红，指名 `SessionSummaryRail.vue:144`）。
 *   ⚠️ 写这段时还漏了一件事，契约 C 一上手就撞上：**修饰类
 *   `.summary-panel--expanded` 声明了 `top: 0`，把基类那个让开安全区的 top
 *   顶掉了**；契约 B 抓不到它（修饰类无 `position` 声明、`top` 也不引用
 *   `--topbar-height` ⇒ 被 `safeTopOffenders` 整条跳过）。
 *   ⇒ 「凡是 .summary-panel 就受契约 B 保护」是**错的**：变体可以静默丢掉安全区。
 *   该变体现已登记为「全屏 sheet 态」（`100vw`/`100%` + `--z-sheet`(1300)
 *   > 顶栏 `--z-sticky`(50)），依据与依据所依赖的声明都写进了登记表；
 *   **「展开态是否应当盖住顶栏」仍待属主确认**。
 *
 * Run: node --test src/styles/__tests__/topbar-chrome-gate.test.mjs
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { collectStyleRules } from './style-scan-utils.mjs'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..', '..')
const SRC = join(ROOT, 'src')
const tokensCss = readFileSync(join(SRC, 'styles/tokens.css'), 'utf8')

/** 判据的基准值从 token 读，不写死 —— token 改了门禁必须跟着变。 */
const TOPBAR_H = (() => {
  const m = /--topbar-height:\s*(\d+)px/.exec(tokensCss)
  if (!m) throw new Error('tokens.css 里找不到 --topbar-height，本门禁的基准值失效')
  return Number(m[1])
})()

const ALL_RULES = collectStyleRules(SRC)

/**
 * ⚠️ 必须**按文件 + 选择器**定位，不能只按选择器。
 * 只按选择器匹配时，别处一个同名 `.app-layout`（本仓确实有一个
 * `display:grid` 的）会抢先命中，判据量到的根本不是被验对象 ——
 * 这是「观测量落在同名替身上」，比找不到还更容易骗过人（因为它有值）。
 */
const LAYOUT_FILE = 'app/AppLayout.vue'
const findInAppLayout = (selector) =>
  ALL_RULES.find((r) => r.file === LAYOUT_FILE && r.selector === selector)

/** 深拷贝式改写规则表：变异只改内存副本，**绝不碰磁盘上的被验对象**。 */
const cloneRules = (mapFn) => ALL_RULES.map((r) => mapFn(r))

// ---------------------------------------------------------------------------
// 契约 A：.top-bar 不得被 flex 压缩
// ---------------------------------------------------------------------------

/**
 * 必须不被 flex 压缩的顶栏 chrome —— **登记到具体出处**（文件 + 选择器），
 * 不钉字符串、不按选择器全局找。
 *
 * ⚠️ 第一版这里写的是「取规则里 selector === 父容器的那条，再看它有没有
 * `height: <token>`」。那是**判据自己的 fail-open**：父容器 `.app-layout` 的
 * `height: 100%` 里没有 `--topbar-height` ⇒ 整条规则被跳过 ⇒ 零违规 ⇒ 永远绿。
 * 而真正要验的 `.top-bar` 是父容器的**子项**，压根没被取到。
 * ⇒ 变异 A1/A2 当场抓到了它（契约自己没牙，变异是唯一能发现这件事的东西）。
 *
 * 现在改成登记表：要验哪些元素**写在表里**，判据按 (file, selector) 精确取。
 * 顺带解决「改名后无人看守」——取不到就报「没找到」，而不是静默放行。
 */
const NO_SHRINK = [
  {
    file: 'app/AppLayout.vue',
    selector: '.top-bar',
    why: '吸顶条：高度由 token --topbar-height 定死，压缩它等于让 token 失效',
  },
]

/**
 * 判据本体。抽出来是为了能被变异自测直接调用。
 *
 * @param {{selector:string, decls:object}[]} rules 规则表
 * @param {{targets:typeof NO_SHRINK, heightToken:string, flexParent:{selector:string, decls:object}}} ctx
 */
function flexShrinkOffenders(rules, { targets, heightToken, flexParent }) {
  const bad = []
  for (const t of targets) {
    const r = rules.find((x) => x.file === t.file && x.selector === t.selector)
    if (!r) {
      bad.push(`${t.file} 里找不到 ${t.selector}（改名或挪走了？本条已无被验对象，需重新登记）`)
      continue
    }
    // 前提自证：用尺寸 token 声明定高，才轮到「不许被压」这条判据。
    if (!r.decls.height || !r.decls.height.includes(heightToken)) {
      bad.push(`${t.file}:${r.line} ${t.selector} 的 height=${r.decls.height ?? '（未声明）'} 不再用 ${heightToken} ⇒ 「不许被压」这条已不适用`)
      continue
    }
    const shrink = r.decls['flex-shrink']
    if (shrink === '0') continue
    bad.push(
      `${t.file}:${r.line}（${t.selector}）height=${r.decls.height} 但 flex-shrink=${shrink ?? '（未声明，初始值 1）'}` +
      ` ⇒ 它是 ${flexParent.selector}（${flexParent.decls.display}/${flexParent.decls['flex-direction']}）的 flex item，` +
      `矮视口下会被按比例压扁；${t.why}`,
    )
  }
  return bad
}

// ---------------------------------------------------------------------------
// 契约 B：用 --topbar-height 做 fixed 顶定位的元素必须让开 --app-safe-top
// ---------------------------------------------------------------------------

const usesTopbarTop = (rules) =>
  rules.filter((r) => r.decls.position === 'fixed' && /--topbar-height/.test(r.decls.top || ''))

/**
 * 判据本体。约定形态：`top` 表达式必须**同时**引用 `--topbar-height` 与
 * `--app-safe-top`。
 *
 * 为什么只查「有没有引用 token」而不算 px：`--app-safe-top` 是
 * `max(env(safe-area-inset-top,0px), var(--android-safe-top,0px))`，
 * 静态环境里恒为 0，算出来的数在真机上必然偏小 ⇒ 拿静态数字当判据，
 * 会对真机上的真缺陷放行。约定式判定与设备无关，且对「忘了写安全区」有牙。
 */
function safeTopOffenders(rules) {
  const bad = []
  for (const r of rules) {
    if (r.decls.position !== 'fixed') continue
    const top = r.decls.top || ''
    if (!/--topbar-height/.test(top)) continue
    if (!/--app-safe-top/.test(top)) {
      bad.push(`${r.file}:${r.line}（${r.selector}）top=${top}（引用了 --topbar-height 却没有让开 --app-safe-top）`)
    }
  }
  return bad
}

// ===========================================================================
// 自证：判据的输入集合必须等于它声称的集合
// ===========================================================================

test('【量具自证】AppLayout 的父容器确实是 flex column（契约 A 的前提）', () => {
  const layout = findInAppLayout('.app-layout')
  assert.ok(layout, `找不到 ${LAYOUT_FILE} 的 .app-layout 规则 —— 契约 A 的前提没了`)
  assert.equal(layout.decls.display, 'flex', '.app-layout 不再是 flex 容器')
  assert.equal(layout.decls['flex-direction'], 'column')
})

test('【量具自证】.top-bar 确实用 token 声明了定高（契约 A 的前提）', () => {
  const bar = findInAppLayout('.top-bar')
  assert.ok(bar, `找不到 ${LAYOUT_FILE} 的 .top-bar 规则 —— 扫描器可能退化了`)
  assert.match(bar.decls.height || '', /--topbar-height/,
    '.top-bar 不再用 --topbar-height 声明高度 ⇒ 契约 A 已不适用（要么改判据，要么这里坏了）')
  assert.equal(TOPBAR_H, 48, `--topbar-height 实读为 ${TOPBAR_H}px；判定文案与告警都按它写，改 token 请一并改文案`)
})

test('【量具自证】至少采到 2 条用 --topbar-height 做 fixed 顶定位的规则（防解析器退化成 0 命中）', () => {
  const uses = usesTopbarTop(ALL_RULES)
  assert.ok(
    uses.length >= 2,
    `只采到 ${uses.length} 条（${uses.map((u) => `${u.file}:${u.line}`).join(', ')}）。` +
    `0~1 条说明扫描器坏了：本门禁的「零违规」在没量到东西时毫无意义。`,
  )
})

test('【量具自证】.summary-panel 的祖先链静止态没有 transform/filter/contain', () => {
  // 这是契约 B 的**硬前提**：只要任一祖先带 transform/filter/contain/will-change，
  // `position:fixed` 的包含块就不再是视口，而会变成那个祖先的 padding box ——
  // 那时「top 与顶栏重叠」的算术全部作废，判据的前提消失。
  //
  // ⚠️ 这里能静态证明的只是**已知祖先选择器**上没有 transform；写全链需要
  // 运行时遍历 DOM。本用例是「已知的那些别退化」，不是「全链无 transform」。
  const ancestors = ['.app-layout', '.content', '.slr']
  const offenders = []
  for (const sel of ancestors) {
    for (const r of ALL_RULES.filter((x) => x.selector === sel)) {
      for (const prop of ['transform', 'filter', 'backdrop-filter', 'will-change', 'contain', 'perspective']) {
        if (r.decls[prop]) offenders.push(`${r.file}:${r.line} ${sel} { ${prop}: ${r.decls[prop]} }`)
      }
    }
  }
  assert.deepEqual(offenders, [],
    `这些祖先带 ${offenders.join('、')} ⇒ fixed 的包含块不再是视口，契约 B 的算术前提消失`)
})

// ===========================================================================
// 契约 A
// ===========================================================================

test('契约 A：.top-bar 必须声明 flex-shrink: 0（否则矮视口下被压扁 3px）', () => {
  const layout = findInAppLayout('.app-layout')
  assert.deepEqual(
    flexShrinkOffenders(ALL_RULES, { targets: NO_SHRINK, heightToken: '--topbar-height', flexParent: layout }),
    [],
    '顶栏会被 flex 压缩。设备实测（scripts/device-matrix.mjs UI-06d）：' +
    '横屏/分屏视口下 h=45px，而 --topbar-height=48px。',
  )
})

test('契约 A 登记表每条都必须写了理由（空理由等于没登记）', () => {
  const noWhy = NO_SHRINK.filter((t) => !t.why || !t.why.trim()).map((t) => `${t.file} ${t.selector}`)
  assert.deepEqual(noWhy, [], 'NO_SHRINK 条目缺少理由')
})

test('契约 A 登记表不得有陈旧条目（指向已不存在或已改名的选择器）', () => {
  const stale = NO_SHRINK.filter((t) => !ALL_RULES.some((r) => r.file === t.file && r.selector === t.selector))
  assert.deepEqual(stale.map((t) => `${t.file} ${t.selector}`), [],
    'NO_SHRINK 里有陈旧条目：对应规则已删除或选择器改名了')
})

// ===========================================================================
// 契约 B
// ===========================================================================

test('契约 B：用 --topbar-height 做 fixed 顶定位的元素必须让开 --app-safe-top', () => {
  assert.deepEqual(
    safeTopOffenders(ALL_RULES),
    [],
    '这些 fixed 浮层只按 --topbar-height 定位，忽略了顶栏被安全区顶下来的那段。' +
    '有状态栏的设备上（模拟器实测安全区 24px）它们会压在顶栏上 16px，' +
    '且 --z-fab(60) > --z-sticky(50)，是浮层画在上面、不是被盖住。',
  )
})

// ===========================================================================
// 变异自测：证明上面两条判据**有牙**（不是恒真）
// ===========================================================================

test('【变异 A1 · 必须转红】把 .top-bar 的 flex-shrink 拿掉（复现本轮那个真缺陷）', () => {
  const layout = findInAppLayout('.app-layout')
  const mutated = cloneRules((r) => {
    if (r.file !== LAYOUT_FILE || r.selector !== '.top-bar') return r
    const decls = { ...r.decls }
    delete decls['flex-shrink']
    return { ...r, decls }
  })
  const hits = flexShrinkOffenders(mutated, { targets: NO_SHRINK, heightToken: '--topbar-height', flexParent: layout })
  assert.ok(hits.length, '抽掉 flex-shrink:0 后必须判红，否则契约 A 是恒真的')
  assert.match(hits[0], /初始值 1/, '应指出「未声明 ⇒ 初始值 1」，而不是笼统说不合规')
})

test('【变异 A2 · 必须转红】flex-shrink 写成非 0 值仍要判红（别放过 0.5）', () => {
  const layout = findInAppLayout('.app-layout')
  const mutated = cloneRules((r) =>
    (r.file === LAYOUT_FILE && r.selector === '.top-bar')
      ? { ...r, decls: { ...r.decls, 'flex-shrink': '0.5' } }
      : r)
  assert.ok(
    flexShrinkOffenders(mutated, { targets: NO_SHRINK, heightToken: '--topbar-height', flexParent: layout }).length,
    'flex-shrink: 0.5 一样会被压缩，必须判红 —— 只认字符串 "0" 的判据在 0.5 上是假牙',
  )
})

test('【变异 A3 · 必须转红】登记表指向的选择器不存在时必须报「无被验对象」，不得静默放行', () => {
  const layout = findInAppLayout('.app-layout')
  const mutated = cloneRules((r) =>
    (r.file === LAYOUT_FILE && r.selector === '.top-bar') ? { ...r, selector: '.topbar-v2' } : r)
  const hits = flexShrinkOffenders(mutated, { targets: NO_SHRINK, heightToken: '--topbar-height', flexParent: layout })
  assert.ok(hits.length, '改名后若静默放行，这条门禁从此就没人看守了')
  assert.match(hits[0], /无被验对象|找不到/, `应说明是被测对象消失了，实际：${hits[0]}`)
})

test('【变异 A4 · 必须保持绿】把登记表指向的元素从规则表整体删掉前，先确认判据抓的是它本人', () => {
  // 负对照：登记表里塞一个**确实合规**的替身，判据必须放行 ——
  // 否则「一律判红」就不是判据而是噪音。
  const layout = findInAppLayout('.app-layout')
  const ok = { file: 'x.vue', line: 1, selector: '.ok-bar', decls: { height: 'var(--topbar-height)', 'flex-shrink': '0' } }
  assert.deepEqual(
    flexShrinkOffenders([ok], {
      targets: [{ file: 'x.vue', selector: '.ok-bar', why: '替身' }],
      heightToken: '--topbar-height', flexParent: layout,
    }),
    [],
    '合规写法被误报 ⇒ 本门禁只会「一律判红」',
  )
})

test('【变异 B1 · 必须转红】注入只按 --topbar-height 定位的 fixed 元素（复现本轮那个潜伏缺陷）', () => {
  const injected = [...ALL_RULES, {
    file: 'injected/probe.vue', line: 1, selector: '.probe',
    decls: { position: 'fixed', top: 'calc(var(--topbar-height, 48px) + var(--space-2))' },
  }]
  assert.ok(
    safeTopOffenders(injected).some((b) => b.startsWith('injected/probe.vue')),
    '注入的「忘了让开安全区」必须被抓到，否则契约 B 是恒真的',
  )
})

test('【变异 B2 · 必须保持绿】注入让开了安全区的 fixed 元素（负对照）', () => {
  const injected = [...ALL_RULES, {
    file: 'injected/ok.vue', line: 1, selector: '.ok',
    decls: { position: 'fixed', top: 'calc(var(--topbar-height, 48px) + var(--app-safe-top) + var(--space-2))' },
  }]
  assert.ok(
    !safeTopOffenders(injected).some((b) => b.startsWith('injected/ok.vue')),
    '合规写法被误报 ⇒ 本门禁只会「一律判红」，那不是判据而是噪音',
  )
})

test('【变异 B3 · 必须保持绿】非 fixed 的元素不受本门禁管辖', () => {
  const injected = [...ALL_RULES, {
    file: 'injected/sticky.vue', line: 1, selector: '.sticky-probe',
    decls: { position: 'sticky', top: 'calc(var(--topbar-height) + var(--space-2))' },
  }]
  assert.ok(
    !safeTopOffenders(injected).some((b) => b.startsWith('injected/sticky.vue')),
    'sticky 相对滚动容器定位，语义不同 —— 把 sticky 也判红是过度管辖',
  )
})

// ---------------------------------------------------------------------------
// 契约 C：`.summary-panel` 的每个实现与每个变体都必须登记「靠什么躲开顶栏」
// ---------------------------------------------------------------------------

/**
 * 这条契约是被两件事逼出来的，都不是「我觉得应该有个门禁」：
 *
 * 1) **一条已记录但未守护的债**：契约 B 只管 `position: fixed` 的那一个
 *    `.summary-panel`，而全仓有**两个**同名实现。第二个 `SessionSummaryRail.vue`
 *    靠 `position: absolute` + 包含块在顶栏之下来躲开顶栏 —— 契约 B 完全看不见它。
 *    若日后有人把它改成 `fixed`，门禁不会报警。
 *
 * 2) **一个真实的洞（2026-10-05 写这条时当场发现）**：`LiveSummaryPanel` 的
 *    `.summary-panel--expanded` 覆写了 `top: 0`，把基类里那个让开安全区的
 *    `top: calc(--topbar-height + --app-safe-top + --space-2)` **顶掉了**。
 *    契约 B 抓不到它，因为该修饰类**没有 `position` 声明**（继承 fixed）
 *    且 `top` **不引用** `--topbar-height` ⇒ 被 `safeTopOffenders` 整条跳过。
 *    ⇒ 结论：**「凡是 .summary-panel 就受契约 B 保护」是错的**，
 *    修饰类可以静默丢掉安全区。这条契约就是为了把那个洞堵上。
 *
 * 判定形态刻意**按实现登记**而不是「扫所有同名类」：同名不同义的两个类
 * 靠**不同的机制**躲开顶栏，塞进同一条规则就会在「机制不同」处误判。
 * 棘轮那两条负责把未来的**第三个实现 / 变体**逼进登记表并补理由。
 */

const ABSENT = Symbol('absent')

/** `.summary-panel` 基类实现 —— 每个都必须登记它靠什么躲开顶栏。 */
const PANEL_IMPLS = [
  {
    file: 'features/meetings/LiveSummaryPanel.vue',
    selector: '.summary-panel',
    expect: 'fixed',
    why: '包含块是视口 ⇒ 必须让开安全区，由契约 B 守；top 已含 --app-safe-top',
  },
  {
    file: 'features/sessions/SessionSummaryRail.vue',
    selector: '.summary-panel',
    expect: 'absolute',
    // absolute 靠「包含块在顶栏之下」躲开，不是靠 top 表达式 —— 这两个字段要一起验
    containingBlock: { selector: '.summary-root', position: 'relative' },
    why: '包含块是 .summary-root{position:relative}，它位于页面主体内、顶栏之下 ⇒ top:0 即顶栏底边',
  },
]

/**
 * `.summary-panel--*` 变体 —— 变体可以改 `top`，但**必须登记，且必须写下
 * 它的依据所依赖的那些声明**。改掉那些声明 ⇒ 依据不再成立 ⇒ 必须重新表态。
 */
const PANEL_VARIANTS = [
  {
    file: 'features/meetings/LiveSummaryPanel.vue',
    selector: '.summary-panel--expanded',
    // 依据：它把面板变成**全屏 sheet**（100vw × 100%），并抬到 --z-sheet(1300)，
    // 远高于顶栏的 --z-sticky(50) ⇒ 「盖住顶栏」是全屏态的固有结果，不是漏算。
    // ⚠️ 待属主确认：展开态**确实应该**盖住顶栏吗？若答案是否，这条要改成修产品。
    mustDeclare: {
      top: '0',
      width: '100vw',
      height: '100%',
      'z-index': 'var(--z-sheet)',
    },
    why: '全屏 sheet 态：100vw/100% + --z-sheet(1300) > 顶栏 --z-sticky(50) ⇒ 盖住顶栏是固有结果（待属主确认）',
  },
]

/**
 * 判据本体。抽出来是为了能被下面的变异自测直接调用。
 *
 * @param {{file:string,selector:string,decls:object}[]} rules 规则表
 * @param {typeof PANEL_IMPLS} registry
 */
function panelImplOffenders(rules, registry) {
  const bad = []
  for (const t of registry) {
    const r = rules.find((x) => x.file === t.file && x.selector === t.selector)
    if (!r) {
      bad.push(`${t.file} 里找不到 ${t.selector}（改名或挪走了？本条已无被验对象，需重新登记）`)
      continue
    }
    if (r.decls.position !== t.expect) {
      bad.push(
        `${t.file}:${r.line}（${t.selector}）position=${r.decls.position ?? '（未声明）'}` +
        ` 而登记的是 ${t.expect} ⇒ 它换了「躲开顶栏」的机制，本条理由已不成立：${t.why}`,
      )
      continue
    }
    // absolute 的安全性完全押在包含块上 ⇒ 包含块必须同时存在且是 relative
    if (t.containingBlock) {
      const cb = rules.find((x) => x.file === t.file && x.selector === t.containingBlock.selector)
      if (!cb) {
        bad.push(`${t.file} 里找不到包含块 ${t.containingBlock.selector} ⇒ ${t.selector} 的 absolute 定位基准消失`)
        continue
      }
      if (cb.decls.position !== t.containingBlock.position) {
        bad.push(
          `${t.file}:${cb.line}（${t.containingBlock.selector}）position=${cb.decls.position ?? '（未声明）'}` +
          ` 而登记要求 ${t.containingBlock.position} ⇒ 包含块不再定位，${t.selector} 的 top 会回退到更外层` +
          `（若外层是视口，就变成契约 B 要抓的那类缺陷）：${t.why}`,
        )
      }
    }
  }
  return bad
}

/** 变体依据的守门：依据依赖的声明被动过 ⇒ 依据失效，必须重新表态。 */
function panelVariantOffenders(rules, registry) {
  const bad = []
  for (const t of registry) {
    const r = rules.find((x) => x.file === t.file && x.selector === t.selector)
    if (!r) {
      bad.push(`${t.file} 里找不到变体 ${t.selector}（改名或挪走了？需重新登记）`)
      continue
    }
    for (const [prop, want] of Object.entries(t.mustDeclare)) {
      const got = r.decls[prop] ?? ABSENT
      if (got !== want) {
        bad.push(
          `${t.file}:${r.line}（${t.selector}）的 ${prop}=${got === ABSENT ? '（未声明）' : got}` +
          ` 而登记的依据要求 ${want === ABSENT ? '（未声明）' : want}` +
          ` ⇒ 「${t.why}」这条依据已不成立，必须重新表态`,
        )
      }
    }
  }
  return bad
}

/** 枚举器：把基类与变体分开数（`\b` 会把 `--expanded` 也算进基类，必须用精确匹配）。 */
const panelBaseRules = (rules) =>
  rules.filter((r) => r.selector === '.summary-panel')
const panelVariantRules = (rules) =>
  rules.filter((r) => /(^|[\s,>+~])\.summary-panel--[A-Za-z0-9_-]+/.test(r.selector || ''))

test('契约 C：.summary-panel 的每个实现都必须仍按登记的机制躲开顶栏', () => {
  assert.deepEqual(
    panelImplOffenders(ALL_RULES, PANEL_IMPLS),
    [],
    '登记过的 .summary-panel 实现换了定位机制或丢了包含块 ⇒ 它不再受任何契约保护',
  )
})

test('契约 C：已登记变体所依赖的声明不得被悄悄改掉（依据失效须重新表态）', () => {
  assert.deepEqual(
    panelVariantOffenders(ALL_RULES, PANEL_VARIANTS),
    [],
    '变体不再满足登记时写下的依据 ⇒ 它现在是「没人表态过」的状态',
  )
})

test('契约 C 登记表不得有陈旧条目（指向已不存在或已改名的选择器）', () => {
  const stale = [
    ...PANEL_IMPLS.filter((t) => !ALL_RULES.some((r) => r.file === t.file && r.selector === t.selector)),
    ...PANEL_VARIANTS.filter((t) => !ALL_RULES.some((r) => r.file === t.file && r.selector === t.selector)),
  ]
  assert.deepEqual(stale.map((t) => `${t.file} ${t.selector}`), [],
    '登记表里有陈旧条目：对应规则已删除或选择器改名了')
})

test('契约 C 登记表每条都必须写了理由（空理由等于没登记）', () => {
  const noWhy = [...PANEL_IMPLS, ...PANEL_VARIANTS]
    .filter((t) => !t.why || !t.why.trim())
    .map((t) => `${t.file} ${t.selector}`)
  assert.deepEqual(noWhy, [], '登记表条目缺少理由')
})

test('契约 C 棘轮：不得新增未登记的 .summary-panel 实现或变体', () => {
  const knownBase = new Set(PANEL_IMPLS.map((t) => `${t.file} ${t.selector}`))
  const knownVar = new Set(PANEL_VARIANTS.map((t) => `${t.file} ${t.selector}`))
  const unregistered = [
    ...panelBaseRules(ALL_RULES)
      .filter((r) => !knownBase.has(`${r.file} ${r.selector}`))
      .map((r) => `${r.file}:${r.line} 实现（${r.selector}）`),
    ...panelVariantRules(ALL_RULES)
      .filter((r) => !knownVar.has(`${r.file} ${r.selector}`))
      .map((r) => `${r.file}:${r.line} 变体（${r.selector}）`),
  ]
  assert.deepEqual(unregistered, [],
    '这些 .summary-panel 实现/变体没有登记「靠什么躲开顶栏」：同名类各自用不同机制躲开顶栏，' +
    '不进登记表就等于没人守。新增请补登记并写清 why。')
})

// ---------------------------------------------------------------------------
// 契约 C 的量具自证 + 变异自测
// ---------------------------------------------------------------------------

test('【量具自证】枚举器确实采到了全部 .summary-panel 实现与变体（防退化成 0 命中）', () => {
  const baseFiles = new Set(panelBaseRules(ALL_RULES).map((r) => r.file))
  assert.ok(
    baseFiles.size >= 2,
    `只采到 ${baseFiles.size} 个文件的 .summary-panel 基类（期望 ≥2）。` +
    '采不到就意味着棘轮永远绿 = 恒真判据',
  )
  const variants = panelVariantRules(ALL_RULES)
  assert.ok(
    variants.length >= 1,
    '一个 .summary-panel--* 变体都没采到 ⇒ 变体棘轮恒真，而变体正是能静默丢掉安全区的那条路',
  )
})

test('【变异 C1 · 必须转红】把 SessionSummaryRail 的 .summary-panel 改成 fixed（正是本条要守的债）', () => {
  const mutated = cloneRules((r) => {
    if (r.file !== 'features/sessions/SessionSummaryRail.vue' || r.selector !== '.summary-panel') return r
    return { ...r, decls: { ...r.decls, position: 'fixed' } }
  })
  assert.ok(
    panelImplOffenders(mutated, PANEL_IMPLS).some((b) => b.includes('SessionSummaryRail')),
    '把那个 absolute 实现改成 fixed 后本门禁没有报警 ⇒ 契约 C 无牙，' +
    '而它在新形态下恰好就是契约 B 要抓的「fixed 却没让开安全区」那一类',
  )
})

test('【变异 C2 · 必须转红】拿掉包含块的 position: relative（absolute 的安全性全押在它上面）', () => {
  const mutated = cloneRules((r) => {
    if (r.file !== 'features/sessions/SessionSummaryRail.vue' || r.selector !== '.summary-root') return r
    const decls = { ...r.decls }
    delete decls.position
    return { ...r, decls }
  })
  assert.ok(
    panelImplOffenders(mutated, PANEL_IMPLS).some((b) => b.includes('.summary-root')),
    '包含块不再定位却没报警 ⇒ 契约 C 只看了 panel 自己，漏了它的定位基准',
  )
})

test('【变异 C3 · 必须转红】变体静默丢掉全屏依据（把 100vw 改回半屏，依据即失效）', () => {
  const mutated = cloneRules((r) => {
    if (r.file !== 'features/meetings/LiveSummaryPanel.vue' || r.selector !== '.summary-panel--expanded') return r
    return { ...r, decls: { ...r.decls, width: '50vw' } }
  })
  assert.ok(
    panelVariantOffenders(mutated, PANEL_VARIANTS).some((b) => b.includes('width')),
    '变体不再全屏却没有报警 ⇒ 「盖住顶栏是全屏固有结果」这条依据被无声作废，' +
    '此时它就是一个真的漏算缺陷却没人拦',
  )
})

test('【变异 C4 · 必须转红】新增一个未登记的 .summary-panel--* 变体（证明变体棘轮有牙）', () => {
  const injected = [...ALL_RULES, {
    file: 'injected/panel.vue', line: 1, selector: '.summary-panel--compact',
    decls: { top: '0', width: '100vw' },
  }]
  const knownVar = new Set(PANEL_VARIANTS.map((t) => `${t.file} ${t.selector}`))
  const unregistered = panelVariantRules(injected)
    .filter((r) => !knownVar.has(`${r.file} ${r.selector}`))
    .map((r) => `${r.file}:${r.line}（${r.selector}）`)
  assert.ok(
    unregistered.some((b) => b.includes('injected/panel.vue')),
    '注入一个未登记的变体竟然通过了 ⇒ 变体棘轮恒真，未来的变体会静默溜过去',
  )
})

test('【变异 C5 · 必须转红】新增一个未登记的 .summary-panel 基类实现（证明实现棘轮有牙）', () => {
  const injected = [...ALL_RULES, {
    file: 'injected/panel.vue', line: 1, selector: '.summary-panel',
    decls: { position: 'absolute', top: '0' },
  }]
  const knownBase = new Set(PANEL_IMPLS.map((t) => `${t.file} ${t.selector}`))
  const unregistered = panelBaseRules(injected)
    .filter((r) => !knownBase.has(`${r.file} ${r.selector}`))
    .map((r) => `${r.file}:${r.line}（${r.selector}）`)
  assert.ok(
    unregistered.some((b) => b.includes('injected/panel.vue')),
    '注入一个未登记的实现竟然通过了 ⇒ 实现棘轮恒真，未来的第三个实现会静默溜过去',
  )
})

test('【变异 C6 · 必须保持绿】未改动时契约 C 必须零违规（否则上面五条的「转红」没有意义）', () => {
  assert.deepEqual(panelImplOffenders(ALL_RULES, PANEL_IMPLS), [],
    '未变异就已经违规 ⇒ 变异 C1/C2 的红来自「本来就红」，不是判据咬住了变异')
  assert.deepEqual(panelVariantOffenders(ALL_RULES, PANEL_VARIANTS), [],
    '未变异就已经违规 ⇒ 变异 C3 的红来自「本来就红」，不是判据咬住了变异')
})
