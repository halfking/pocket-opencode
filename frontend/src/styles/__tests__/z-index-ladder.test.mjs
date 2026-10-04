/**
 * z-index-ladder.test.mjs — 层级阶梯门禁。
 *
 * `styles/tokens.css` 自己写着「全局唯一权威。新增浮层必须在此登记，
 * **禁止散落字面值**」。这句话在 2026-10-04 之前是**没有强制力的**——
 * 全仓实测 23 处裸数字 z-index、2 处引用未定义 token、3 处 fallback 与
 * token 取值冲突。逐条列在这里是因为它们各自都长得像「无害的本地值」：
 *
 *  ① 未定义 token（真缺陷）：`var(--z-popover, 30)`，而 `--z-popover`
 *     从未定义 ⇒ 实际取 30，**低于** `--z-sticky`(50) 与 `--z-bottom-nav`(70)，
 *     tooltip 被吸顶栏与底栏盖住。
 *  ② fallback 冲突：`var(--z-fab, 40)`（token 是 60）、`--z-bottom-nav, 20`
 *     （token 是 70）、`--z-sheet, 1000`（token 是 1300）。token 一旦
 *     没加载上，元素就落到**另一个层**——同一件事两处写法。
 *  ③ 裸数字：分两类。见下方 ALLOWLIST。
 *
 * 2026-10-06 补记：原先登记为「待目视核对」的 7 处全屏 `position: fixed`
 * 遮罩已**全部归位到 token**，不再需要目视核对——它们各自的源码注释白纸黑字
 * 写着「同 BottomSheet.vue」，即作者本意就是 sheet 层；而静态分析确认祖先链
 * `#app / .app-root / .app-layout / main / .content / .top-bar` **均不创建
 * 层叠上下文**，所以它们与底栏/吸顶栏同处一个根层叠上下文，40/50/80/999/1000
 * 全部偏低（Playwright 最小复现：z=40 时 `elementFromPoint` 返回 `header.top-bar`，
 * z=60 时返回 `div.bind-overlay`）。ALLOWLIST 里因此不再有任何「待目视核对」。
 *
 * ⚠️ ALLOWLIST 的 key 是**声明所在行**。往文件上方加一行注释就会让 key 漂移，
 *   于是「改了一处无关代码」会把本门禁变成红的。2026-10-06 实测：给
 *   `EmailInboxView.showLocal` 加一段 10 行注释，两个 key 从 667/730 漂到 684/747；
 *   同一文件迁移到内核（删掉近 100 行）后又漂到 664/727 —— **一次会话内咬了三次**。
 *   这**是门禁在工作**（不是误报），但也说明「声明行」不如「规则块起始行」稳——
 *   `bottom-chrome-gate.test.mjs` 用的就是后者，故它不受加注释影响。
 *
 * ★ 同源的第二类腐烂：**ALLOWLIST 的理由会与代码脱节**。行号对了不代表
 *   理由还对——本会话里 `EmailInboxView` 两条的理由是从迁移前的旧代码抄的，
 *   迁移后那两行早已换成 `.more-menu` 与 `.to-top`，理由却还写着
 *   「列表项内部标签 / 徽标的兄弟排序」。行号门禁只查「key 存在」，
 *   查不出「理由是否还对得上那个元素」。改动相邻代码时要顺手核对理由。
 *
 * 门禁守：
 *   1. 每个 `var(--z-*)` 都必须是**已定义**的 token（不再允许靠 fallback 蒙混）；
 *   2. 允许保留 fallback，但**必须与 token 取值相等**（不一致即红）；
 *   3. 裸数字 z-index 必须在 ALLOWLIST 里，且每条都带理由；
 *   4. 阶梯按 tokens.css 的**声明顺序**严格递增。
 *
 * ⚠️ 本门禁只管「谁盖谁」。元素**是否该给底栏让位**由
 * `bottom-chrome-gate.test.mjs` 管——两者互补，缺一不可。
 *
 * Run: node --test src/styles/__tests__/z-index-ladder.test.mjs
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { readFileSync, readdirSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..', '..')
const SRC = join(ROOT, 'src')
const tokensCss = readFileSync(join(SRC, 'styles/tokens.css'), 'utf8')

/** SSOT：token 名 → 取值，按声明顺序。 */
const TOKENS = new Map()
for (const m of tokensCss.matchAll(/(--z-[a-z-]+):\s*(\d+);/g)) {
  // matchAll 产出的是数组（RegExpMatchArray），**没有** .group() 方法。
  // 这里用下标取值；写成 m.group(1) 会得到 "m.group is not a function"。
  TOKENS.set(m[1], Number(m[2]))
}

/**
 * 允许的裸数字 z-index —— **每条都要写清为什么它不是全局层**。
 *
 * 判别标准：这个值是否要跟全局浮层（导航/弹层/吸顶栏）比较？
 *  - 否 ⇒ 组件自己 stacking context 内的兄弟排序，局部值合法。
 *  - 是 ⇒ 必须登记为 token。
 *
 * 2026-10-06：这里曾有 8 条「待目视核对」的全屏遮罩，现已全部归位（见文件头）。
 * 现在的每一条都是**局部兄弟排序**——即在组件自有 stacking context 内、
 * 不与全局 chrome 比较的合法局部值。
 */
const ALLOWLIST = {
  'components/interactive/SwipeableListItem.vue:191': '卡片内删除按钮在内容之上的兄弟排序（组件自有 stacking context）',
  'components/interactive/SwipeableListItem.vue:203': '同上，内容与按钮的局部前后关系',
  'components/interactive/PullToRefresh.vue:305': '下拉指示器在容器内的局部层级',
  'components/interactive/VoiceCommandAssistant.vue:325': '面板内子项的局部排序',
  'components/interactive/DualScreenLayout.vue:343': '.resize-handle 在面板内的局部排序（absolute，非 fixed）',
  'features/email/EmailInboxView.vue:664': '`.more-menu` 顶栏「更多」下拉菜单：absolute，需盖住同层的顶栏按钮（position: static，无层级）',
  'features/email/EmailInboxView.vue:727': '`.to-top` 回顶按钮：absolute 悬浮，需盖住列表内容（静态流，无层级）',
  'features/email/EmailSettingsView.vue:526': '设置行内部控件的兄弟排序',
  'features/settings/SettingsSTT.vue:604': 'STT 面板内局部排序',
  'styles.css:272': '路由过渡 enter 态的局部前后关系',
  'styles.css:279': '同上',
  'styles.css:286': '同上',
  'styles.css:292': '同上',
  'features/email/EmailSpamCleanupView.vue:233': '页内 sticky 小节标题，低于全局吸顶栏',
  'features/email/EmailAccountAddView.vue:264': '页内 sticky 小节标题，低于全局吸顶栏',
}

/**
 * 收集所有 z-index 使用点。
 *
 * ⚠️ 这里必须靠**父目录的 withFileTypes 列表**区分文件与目录。
 * 早期实现对每个条目都 `readdirSync(full)`，对**文件**会抛 ENOTDIR；
 * 于是 `catch { continue }` 把所有 .vue/.css 全跳过了，USES 恒为空 ——
 * 「0 处 z-index」这个假象让 ALLOWLIST 全部被判成陈旧条目。
 * 量具自己报的数要先自证非空。
 */
function collect() {
  const out = []
  const files = []
  const walk = (dir) => {
    let entries
    try {
      entries = readdirSync(dir, { withFileTypes: true })
    } catch {
      return
    }
    for (const e of entries) {
      if (['__tests__', 'node_modules', 'android', 'ios', 'dist'].includes(e.name)) continue
      const full = join(dir, e.name)
      if (e.isDirectory()) walk(full)
      else if (/\.(vue|css)$/.test(e.name)) files.push(full)
    }
  }
  walk(SRC)
  for (const full of files) {
    const rel = full.slice(SRC.length + 1)
    readFileSync(full, 'utf8')
      .split('\n')
      .forEach((line, i) => {
        const m = /z-index:\s*([^;]+)/.exec(line)
        if (!m) return
        out.push({ where: `${rel}:${i + 1}`, value: m[1].trim() })
      })
  }
  return out
}

const USES = collect()

test('tokens.css 的阶梯按声明顺序严格递增（防止有人插错位置）', () => {
  const seq = [...TOKENS.values()]
  assert.ok(seq.length >= 9, `token 数量异常：${seq.length}`)
  for (let i = 1; i < seq.length; i += 1) {
    assert.ok(
      seq[i] > seq[i - 1],
      `阶梯非递增：第 ${i} 项 ${seq[i - 1]} → ${seq[i]}（${[...TOKENS.keys()][i - 1]} → ${[...TOKENS.keys()][i]}）`,
    )
  }
})

test('① 每个 var(--z-*) 都必须是已定义的 token（不再靠 fallback 蒙混）', () => {
  const bad = USES.filter((u) => /^var\(--z-/.test(u.value)).filter((u) => {
    const name = /var\((--z-[a-z-]+)/.exec(u.value)[1]
    return !TOKENS.has(name)
  })
  assert.deepEqual(
    bad.map((b) => `${b.where} → ${b.value}`),
    [],
    '引用了未定义的 z token（曾经的真缺陷：--z-popover）',
  )
})

test('② 保留的 fallback 必须与 token 取值相等（不一致即「同一件事两处写法」）', () => {
  const bad = []
  for (const u of USES) {
    const m = /^var\((--z-[a-z-]+),\s*(\d+)\)$/.exec(u.value)
    if (!m) continue
    const [, name, fb] = m
    if (!TOKENS.has(name)) {
      bad.push(`${u.where} → ${u.value}（token 未定义）`)
      continue
    }
    if (Number(fb) !== TOKENS.get(name)) {
      bad.push(`${u.where} → ${u.value}（token ${name}=${TOKENS.get(name)}，fallback=${fb}）`)
    }
  }
  assert.deepEqual(bad, [], 'fallback 与 token 取值冲突')
})

test('③ 裸数字 z-index 必须在 ALLOWLIST 里，且每条都写了理由', () => {
  const bad = USES.filter((u) => /^\d+$/.test(u.value))
    .map((u) => u.where)
    .filter((w) => !ALLOWLIST[w])
  assert.deepEqual(
    bad,
    [],
    '出现未登记的裸数字 z-index；请登记进 tokens.css 的 token，或在 ALLOWLIST 里写明理由',
  )
})

test('ALLOWLIST 本身不得有陈旧条目（指向已不存在的行）', () => {
  const live = new Set(USES.map((u) => u.where))
  const stale = Object.keys(ALLOWLIST).filter((k) => !live.has(k))
  assert.deepEqual(stale, [], 'ALLOWLIST 里有陈旧条目：对应代码行已不存在或行号变了')
})

test('ALLOWLIST 里不得登记 token 化位置（那是重复登记）', () => {
  const dup = USES.filter((u) => /^var\(--z-/.test(u.value))
    .map((u) => u.where)
    .filter((w) => ALLOWLIST[w])
  assert.deepEqual(dup, [], '这些位置已用 token，不该同时出现在 ALLOWLIST')
})

test('【变异自测】注入一个未登记 z-index，本门禁必须转红', () => {
  const injected = [...USES, { where: 'injected/probe.vue:1', value: '4242' }]
  const bad = injected.map((u) => u.where).filter((w) => !ALLOWLIST[w])
  assert.ok(bad.includes('injected/probe.vue:1'), '注入的未登记值必须被抓到，否则本自测恒真')
})
