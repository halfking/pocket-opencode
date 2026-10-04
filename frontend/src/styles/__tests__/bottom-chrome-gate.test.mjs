/**
 * bottom-chrome-gate.test.mjs — 底部 chrome 让位门禁。
 *
 * 与 `z-index-ladder.test.mjs` 是**互补的两半**：那份管「谁盖谁」，
 * 这份管「谁该给底栏让位」。两者必须同时成立才正确：
 *
 *   - z-index 管错了 ⇒ 元素即便让开了底栏，也可能被别的层盖住；
 *   - bottom 管错了 ⇒ 元素 z-index 再大，也会**主动钻到**底栏底下。
 *
 * 2026-10-06 修的三个真缺陷都属第二类，且都不需要真机就能证明：
 *
 * | 元素 | 原 bottom | 自身高 | 底栏高(--bottom-chrome-height) | 后果 |
 * |---|---|---|---|---|
 * | MeetingDetailView .speakers-btn | 14px | 40px | 56px | **整颗按钮 100% 被盖住、点不到** |
 * | MeetingMicDock .mic | 14px | 56px | 56px | 42/56 = **75% 被盖住** |
 * | VaultEntryView .toast | 20px | ~40px | 56px | 下沿被压住 |
 *
 * 三者的 z-index 都是 `var(--z-fab)`(60) < `var(--z-bottom-nav)`(70)，
 * 底栏绘制在其上；而它们的 bottom 又只按 `--app-safe-bottom` 定位、
 * **完全没读 `--bottom-chrome-height`** ⇒ 几何与层级双重失守。
 *
 * 另修 3 处「现在对、但靠硬编码数字撑着」的位置（PkmEditor 80px、
 * RecordingPill 72px、DualScreenLayout 80px）：它们是在手工复刻
 * `--bottomnav-height`，底栏一加高就静默失效——与「fallback 与 token
 * 冲突」是同一类病：同一件事写了两处。
 *
 * ⚠️ 解析必须按 **`}` 切规则块**，不能「向上回看 N 行找 position」。
 * 早期扫描器用 12 行回看，于是把 `.actions-bar { position: sticky }`
 * 和 `border-bottom: 1px solid` 都算成了 fixed+bottom ⇒ 13 个命中里
 * 有一半是假的。`bottom` 的正则必须用**前导边界**（`[\s;{]`），
 * 否则 `border-bottom` 会被当成 `bottom`。
 *
 * **判据必须真解析，不能只匹配。** `calc(var(--bottom-chrome-height) - 20px)`
 * 引用了 token，纯模式匹配会放行，但算出来 36px < 56px 照样被底栏压住。
 * 故 `resolveBottomPx()` 把 calc() / max() / min() / var() / 裸 px
 * 一路折叠成数字再比较。三个坑（都曾让 4 条用例全红）：
 *   ① 按 +/- 切分会连 `--bottom-chrome-height` **内部的连字符**一起切开
 *      ⇒ 必须先把 var() 抽成占位符；
 *   ② 切分后各项带空白（`+  __V1__`）⇒ 匹配前要压掉；
 *   ③ `Toast.vue` 用 `max(var(--bottom-chrome-height), var(--composer-inset, 0px))`
 *      —— 正确写法，**不该**因解析不了就被当成缺陷，故支持折叠 max()/min()。
 *
 * Run: node --test src/styles/__tests__/bottom-chrome-gate.test.mjs
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { readFileSync, readdirSync } from 'node:fs'
import { join, dirname, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..', '..')
const SRC = join(ROOT, 'src')
const tokensCss = readFileSync(join(SRC, 'styles/tokens.css'), 'utf8')

/** 判据的阈值基准：底栏本体高度。ALLOWLIST 里的数字不得低于它。 */
const NAV_HEIGHT = (() => {
  const m = /--bottomnav-height:\s*(\d+)px/.exec(tokensCss)
  if (!m) throw new Error('tokens.css 里找不到 --bottomnav-height，本门禁的基准值失效')
  return Number(m[1])
})()

/** spacing token 表，供 resolveBottomPx 把 calc() 算成 px。 */
const SPACE_TOKENS = new Map()
for (const m of tokensCss.matchAll(/(--space-[a-z0-9]+):\s*(\d+(?:\.\d+)?)px/g)) {
  SPACE_TOKENS.set(m[1], Number(m[2]))
}

/**
 * 底部固定定位的豁免登记 —— 每条写清「为什么它不需要让位」。
 *
 * ⚠️ key 用的是**规则块起始行**（选择器所在行），不是某条声明的行号。
 * 改声明体内的值不会让 key 漂移，改选择器/加一行注释会——这比按
 * 声明行号登记稳，因为「加一行注释」比「改一个值」更常见。
 *
 * `isChrome: true`  = 它**就是** chrome 本身（基准，不适用）。
 * `minBottom: N`    = 硬编码数字，但必须 ≥ N（底栏高度）；底栏加高到超过 N
 *                     时这条会转红，逼人重新决策，而不是悄悄穿帮。
 */
const ALLOWLIST = {
  'components/BottomNav.vue:125': {
    isChrome: true,
    reason: '底栏本体：它就是 --bottom-chrome-height 的定义者，不能给自己让位',
  },
  'components/interactive/BottomNav.vue:68': {
    isChrome: true,
    reason: '同上，另一份底栏实现（交互式变体）',
  },
  'components/interactive/VoiceRecorder.vue:263': {
    minBottom: NAV_HEIGHT,
    reason: '转写浮层刻意远离底栏（150px），是浮在页面中上部的读数面板，不是贴边控件',
  },
}

/** 收集 .vue / .css 里的所有 CSS 规则块。 */
function collectFiles() {
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
  return files
}

/** 声明的正则：属性名必须由 [\s;{] 起头，避免 border-bottom 命中 bottom。 */
const DECL = /(?:^|[\s;{])([a-z-]+)\s*:\s*([^;{}]+)/g

/**
 * 按 `}` 平衡切出规则块，返回 { file, line, selector, decls }。
 * 嵌套（@media）天然被内层规则的 `}` 分开。
 */
function parseRules(text, file) {
  const lines = text.split('\n')
  const rules = []
  let depth = 0
  let buf = []
  let start = 0
  for (let i = 0; i < lines.length; i += 1) {
    const line = lines[i]
    if (depth === 0 && !line.includes('{')) {
      // 顶层选择器行 / 注释
      buf = [line]
      if (line.includes('{')) {
        start = i + 1
        depth += countBraces(line)
        buf = []
      }
      continue
    }
    if (depth === 0) {
      buf = [line]
      start = i + 1
      if (line.includes('{')) depth += countBraces(line)
      continue
    }
    buf.push(line)
    depth += countBraces(line)
    if (depth <= 0) {
      const body = buf.join('\n')
      const open = body.indexOf('{')
      const close = body.lastIndexOf('}')
      if (open !== -1 && close > open) {
        const selector = body.slice(0, open).trim().replace(/\/\*[\s\S]*?\*\//g, '').trim()
        const decls = {}
        for (const m of body.slice(open + 1, close).matchAll(DECL)) {
          if (!(m[1] in decls)) decls[m[1]] = m[2].trim()
        }
        rules.push({ file, line: start, selector, decls })
      }
      depth = 0
      buf = []
    }
  }
  return rules
}

const countBraces = (s) => (s.match(/\{/g) || []).length - (s.match(/\}/g) || []).length

/** 所有 position:fixed 且带 bottom 偏移的规则。 */
function collectFixedBottom() {
  const out = []
  for (const f of collectFiles()) {
    // 同 z-index-ladder.test.mjs：join() 在 Windows 上给的是反斜杠，
    // ALLOWLIST 的 key 是正斜杠，不归一化就会把「存在」判成「陈旧」。
    const rel = f.slice(SRC.length + 1).split(sep).join('/')
    for (const r of parseRules(readFileSync(f, 'utf8'), rel)) {
      if (r.decls.position !== 'fixed') continue
      if (!('bottom' in r.decls)) continue
      out.push({ where: `${rel}:${r.line}`, selector: r.selector, bottom: r.decls.bottom })
    }
  }
  return out
}

const USES = collectFixedBottom()

/**
 * 把 `bottom` 的声明**解析成 px**。
 *
 * 只做「引用了 --bottom-chrome-height」这一层检查是不够的——
 * `calc(var(--bottom-chrome-height) - 20px)` 引用了 token，照样会把元素
 * 塞回底栏底下，模式匹配看不见。必须真的把值算出来。
 *
 * ⚠️ `--app-safe-bottom` 按 **0** 算（浏览器 env() 取不到时就是 0），
 * 于是 `--bottom-chrome-height` = `--bottomnav-height`。这是**保守下界**：
 * 真机上 safe-area 只会让底栏更高，因此用 0 判断不会漏判真机上的遮挡。
 *
 * @returns {number|null} 解析不出时返回 null（交由调用方按「无法自证」处理，
 *   而不是假装合规——那正是本门禁一直在防的失败形态）。
 */
function resolveBottomPx(value) {
  let expr = value.trim()
  const calc = /^calc\((.*)\)$/s.exec(expr)
  if (calc) expr = calc[1].trim()

  // ⚠️ 必须先把 var(--name) 抽成占位符再做算术：`--bottom-chrome-height`
  // 里**本身就带连字符**，直接按 +/- 切分会把变量名切成 `--` / `bottom` /
  // `chrome` / `height)` 四段，解析恒失败（这个 bug 让首版 4 条用例全红）。
  const vars = []
  expr = expr.replace(/var\(\s*(--[a-z0-9-]+)\s*(?:,\s*([^)]*))?\)/g, (_m, name, fb) => {
    vars.push({ name, fb })
    return ` __V${vars.length - 1}__ `
  })

  const resolveVar = ({ name, fb }) => {
    if (name === '--bottom-chrome-height') return NAV_HEIGHT
    if (name === '--app-safe-bottom') return 0
    const tok = SPACE_TOKENS.get(name)
    if (tok !== undefined) return tok
    if (fb) {
      const f = parseFloat(fb)
      if (!Number.isNaN(f)) return f
    }
    return null
  }

  /** 把一段「若干项 +/- 组成」的表达式求和。不可解析返回 null。 */
  const sumOf = (src) => {
    let total = 0
    for (const rawTerm of src.split(/(?=[+-])/)) {
      // 切分后各项会带上多余空白（`+  __V1__`），先压掉再匹配。
      const term = rawTerm.replace(/\s+/g, '')
      if (!term) continue
      const ph = /^([+-]?)__V(\d+)__$/.exec(term)
      if (ph) {
        const v = resolveVar(vars[Number(ph[2])])
        if (v === null) return null
        total += ph[1] === '-' ? -v : v
        continue
      }
      const num = /^([+-]?)(\d+(?:\.\d+)?)px$/.exec(term)
      if (num) {
        total += num[1] === '-' ? -Number(num[2]) : Number(num[2])
        continue
      }
      return null
    }
    return total
  }

  // 折叠 max()/min()：`Toast.vue` 的 `max(var(--bottom-chrome-height),
  // var(--composer-inset, 0px))` 是正确写法，不该因为解析不了就被当成缺陷。
  // 反复取**最内层**（实参里不再含括号）替换成字面量，直到无可折叠。
  for (let guard = 0; guard < 20; guard += 1) {
    const m = /(max|min)\(([^()]*)\)/.exec(expr)
    if (!m) break
    const vals = m[2].split(',').map((a) => sumOf(a.trim()))
    if (vals.some((v) => v === null)) break
    const folded = m[1] === 'max' ? Math.max(...vals) : Math.min(...vals)
    expr = expr.slice(0, m.index) + `${folded}px` + expr.slice(m.index + m[0].length)
  }

  return sumOf(expr)
}

/** 判据本体：一条规则是否合规。抽出来是为了能被变异自测直接调用。 */
function offenders(uses) {
  const bad = []
  for (const u of uses) {
    const allow = ALLOWLIST[u.where]
    if (/bottom-chrome-height|composer-inset|kb-inset/.test(u.bottom)) {
      // 引用了 chrome token 还要**算一遍**——token 可以被减回去。
      const px = resolveBottomPx(u.bottom)
      if (px === null) {
        bad.push(`${u.where}（${u.selector}）→ ${u.bottom}（引用了 chrome token 但无法解析成 px，需人工确认）`)
      } else if (px + 1 < NAV_HEIGHT && !allow?.isChrome) {
        bad.push(`${u.where}（${u.selector}）→ ${u.bottom}（解析为 ${px}px，低于底栏 ${NAV_HEIGHT}px）`)
      }
      continue
    }
    if (allow?.isChrome) continue
    if (allow) {
      const nums = (u.bottom.match(/(\d+(?:\.\d+)?)px/g) || []).map((n) => parseFloat(n))
      if (nums.length && Math.max(...nums) >= allow.minBottom) continue
      bad.push(`${u.where}（${u.selector}）→ ${u.bottom}（豁免要求 ≥ ${allow.minBottom}px，实测 ${Math.max(...nums, 0)}px）`)
      continue
    }
    bad.push(`${u.where}（${u.selector}）→ ${u.bottom}`)
  }
  return bad
}

test('【量具自证】采集结果非空（防止解析器悄悄退化成 0 命中）', () => {
  assert.ok(
    USES.length >= 8,
    `position:fixed + bottom 的规则只采到 ${USES.length} 条，解析器可能坏了；本门禁的数字在没量到东西时没有意义`,
  )
  assert.ok(
    USES.some((u) => /bottom-chrome-height/.test(u.bottom)),
    '一条都没采到已知正确的参考实现（MeetingListView 等）⇒ 解析器一定坏了',
  )
  // 反向自证：border-bottom 不得被当成 bottom。
  assert.ok(
    !USES.some((u) => /border-bottom/.test(u.selector + u.bottom)),
    '把 border-bottom 误认成了 bottom',
  )
})

test('底部固定的浮层必须让开 --bottom-chrome-height（否则会被底栏盖住）', () => {
  assert.deepEqual(
    offenders(USES),
    [],
    '这些 position:fixed 元素没有让开底部导航。若其 z-index 又低于 --z-bottom-nav(70)，会被底栏直接压住。',
  )
})

test('【解析器自证 · max() 折叠生效】真实仓里最复杂的那条必须算出 ≥ 底栏高度', () => {
  // Toast.vue 用 `max(var(--bottom-chrome-height), var(--composer-inset, 0px))
  //   + var(--kb-inset, 0px) + var(--space-4)`，是全仓最复杂的 bottom 表达式。
  //   它若解析不出，会以「需人工确认」被判红——那条红是**解析器**的问题，
  //   不是产品的问题，所以这里显式钉住它的解析结果。
  const toast = USES.find((u) => u.where.startsWith('components/base/Toast.vue'))
  assert.ok(toast, '没找到 Toast.vue 的 bottom 规则')
  assert.match(toast.bottom, /max\(/, 'Toast 的 bottom 应含 max()，否则本用例失去意义')
  const px = resolveBottomPx(toast.bottom)
  assert.equal(px, NAV_HEIGHT + 0 + 0 + 14, `期望 max(56, 0) + 0 + 14 = 70px，实际 ${px}px`)
})

test('ALLOWLIST 不得有陈旧条目（指向已不存在的行）', () => {
  const live = new Set(USES.map((u) => u.where))
  const stale = Object.keys(ALLOWLIST).filter((k) => !live.has(k))
  assert.deepEqual(stale, [], 'ALLOWLIST 里有陈旧条目：对应规则已删除或行号变了')
})

test('ALLOWLIST 每条都必须写了理由（空理由等于没登记）', () => {
  const noReason = Object.entries(ALLOWLIST)
    .filter(([, v]) => !v.reason || !v.reason.trim())
    .map(([k]) => k)
  assert.deepEqual(noReason, [], 'ALLOWLIST 条目缺少理由')
})

test('【变异 1 · 必须转红】注入 bottom: 4px 的 fixed 元素', () => {
  const injected = [...USES, { where: 'injected/probe.vue:1', selector: '.probe', bottom: '4px' }]
  assert.ok(
    offenders(injected).some((b) => b.startsWith('injected/probe.vue:1')),
    '注入的 4px 贴底元素必须被抓到，否则「必须让开 chrome」这条是恒真的',
  )
})

test('【变异 2 · 必须保持绿】注入已让开 chrome 的 fixed 元素（负对照）', () => {
  const injected = [...USES, { where: 'injected/ok.vue:1', selector: '.ok', bottom: 'calc(var(--bottom-chrome-height) + 8px)' }]
  assert.ok(
    !offenders(injected).some((b) => b.startsWith('injected/ok.vue:1')),
    '已让开 chrome 的元素被误报 ⇒ 本门禁只会「一律判红」，那不是判据而是噪音',
  )
})

test('【变异 3 · 必须转红】引用了 chrome token 但把它减回去', () => {
  // 这条是「解析」能力的自证：纯模式匹配看到 --bottom-chrome-height 就放行，
  // 只有真的把 calc 算出来（56 − 20 = 36px < 56px）才会判红。
  const injected = [...USES, { where: 'injected/sub.vue:1', selector: '.sub', bottom: 'calc(var(--bottom-chrome-height) - 20px)' }]
  const hit = offenders(injected).find((b) => b.startsWith('injected/sub.vue:1'))
  assert.ok(hit, '「引用了 token 就放行」是模式匹配的假牙：减回去的值必须被抓到')
  assert.match(hit, /36px/, `应解析出 36px，实际：${hit}`)
})

test('【变异 4 · 必须保持绿】加一个 --space 间距仍合规（证明解析器会算 spacing token）', () => {
  // 与变异 3 配对：若解析器不认识 --space-*，变异 3 也会「因为别的原因」转红，
  // 两者一起才能证明算出来的是**真值**而不是巧合。
  const injected = [...USES, { where: 'injected/sp.vue:1', selector: '.sp', bottom: 'calc(var(--bottom-chrome-height) + var(--space-4))' }]
  assert.ok(
    !offenders(injected).some((b) => b.startsWith('injected/sp.vue:1')),
    '56 + 14 = 70px ≥ 56px，应当放行；不通过说明解析器没真正算 spacing token',
  )
})
