/**
 * CSS 变量定义/引用一致性回归测试。
 *
 * 缺陷背景（真机 Redmi 14R 5G 实测）：
 *   RSS 页「新增源」按钮渲染成近白不可读——`.btn-primary { background: var(--accent) }`
 *   而 `--accent` 在整个前端从未定义过，声明失效后背景回退为透明，
 *   白字（color: white）落在浅色底上，对比度约 1.1:1，完全不可读。
 *   同类问题波及 RSS 三个组件与 AgentMarketView，共 13 处引用。
 *
 * 这类缺陷静态可见、影响面大、且不会被任何运行时错误暴露，
 * 因此用扫描守住：任何 var(--x) 都必须能在 CSS 或 setProperty 里找到定义。
 *
 * Run: node --test src/styles/__tests__/css-vars.test.mjs
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, dirname, extname, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..', '..')
const SRC = join(ROOT, 'src')
const SKIP_DIRS = new Set(['node_modules', '.git', 'dist', '__tests__', 'android', 'ios'])

/** 递归收集源码文件（.vue/.css/.ts）。 */
function collectFiles(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (SKIP_DIRS.has(name)) continue
    const full = join(dir, name)
    const st = statSync(full)
    if (st.isDirectory()) collectFiles(full, out)
    else if (['.vue', '.css', '.ts'].includes(extname(name))) out.push(full)
  }
  return out
}

const files = collectFiles(SRC)

/**
 * `--name:` 形式的定义。
 * 允许名字后跟引号/反引号，以覆盖 Vue style 绑定里的运行时注入：
 *   `{ '--fold-master-width': `${x}px` }`
 *   那种写法不是 CSS 声明，但同样让变量在运行时存在。
 */
const DEFINE_RE = /--([A-Za-z0-9_-]+)['"`]?\s*:/g
/**
 * `var(--name)` 形式的引用，同时捕获紧随其后的分隔符：
 *   `)` = 裸引用（未定义时整条声明失效 —— 真 bug）
 *   `,` = 带 fallback（未定义时用兜底值 —— 合法）
 */
const USE_RE = /var\(\s*--([A-Za-z0-9_-]+)\s*([,)])/g
/** JS 运行时注入的定义。 */
const SET_RE = /setProperty\(\s*['"](--[A-Za-z0-9_-]+)['"]/g

const defined = new Set()
/** 裸引用（无 fallback）的使用点 —— 这些才是会静默失效的。 */
const bare = new Map()
/** 带 fallback 的使用点，仅作统计。 */
const withFallback = new Set()

for (const file of files) {
  const text = readFileSync(file, 'utf8')
  for (const m of text.matchAll(DEFINE_RE)) defined.add(m[1])
  for (const m of text.matchAll(SET_RE)) defined.add(m[1].slice(2))
  for (const m of text.matchAll(USE_RE)) {
    const [, name, sep] = m
    if (sep === ',') {
      withFallback.add(name)
      continue
    }
    const rel = relative(ROOT, file).replace(/\\/g, '/')
    if (!bare.has(name)) bare.set(name, new Set())
    bare.get(name).add(rel)
  }
}

describe('CSS 变量一致性', () => {
  it('扫描到了足够的源码文件（防止路径写错导致空跑通过）', () => {
    assert.ok(files.length > 100, `只扫描到 ${files.length} 个文件，扫描范围可能失效`)
  })

  it('裸引用 var(--x)（无 fallback）的变量都有定义', () => {
    // 带 fallback 的未定义变量只是用兜底值，不会静默失效，不在此判据内。
    const missing = []
    for (const [name, where] of bare) {
      if (defined.has(name)) continue
      missing.push(`var(--${name}) 未定义，用在: ${[...where].slice(0, 3).join(', ')}`)
    }
    assert.equal(
      missing.length,
      0,
      `发现 ${missing.length} 处裸引用了未定义 CSS 变量（声明会整条失效）：\n  - ${missing.join('\n  - ')}`,
    )
  })

  it('回归护栏：--accent 不得被裸引用（RSS 不可读按钮的根因）', () => {
    const users = bare.get('accent')
    assert.equal(
      users ? [...users].length : 0,
      0,
      `--accent 又被裸引用了: ${[...(users ?? [])].join(', ')}；主色请用 --brand-primary，或补 fallback`,
    )
  })
})

/**
 * 硬编码等宽字体栈扫描。
 *
 * 背景：`--font-mono` 末尾补了 `var(--font-sans)` 作为 CJK 回退，
 * 等宽字体不含中文字形，一旦某处绕过 token 写死 `ui-monospace, monospace`，
 * 中文就会掉到浏览器默认字体，与正文风格不一致 —— 正是用户报的「字体不对」。
 *
 * 之前的手工扫描只查了裸 `monospace:` 声明，漏了两种高频写法：
 *   1. `font: 12px/1.5 ui-monospace, monospace` 简写
 *   2. `font-family: ui-monospace, SFMono-Regular, Menlo, monospace` 展开写法
 * 两者都真实存在于 ScheduledTaskDetailView 与 FlashcardEditView。
 */
const HARDCODE_MONO_RE = /font(?:-family)?\s*:[^;{}]*?\b(?:monospace|ui-monospace|SFMono-Regular|Menlo|Consolas)\b/g

describe('等宽字体必须走 --font-mono token', () => {
  it('没有硬编码的等宽字体栈', () => {
    const hits = []
    for (const file of files) {
      const text = readFileSync(file, 'utf8')
      for (const m of text.matchAll(HARDCODE_MONO_RE)) {
        hits.push(`${relative(ROOT, file).replace(/\\/g, '/')}: ${m[0].trim()}`)
      }
    }
    assert.equal(
      hits.length,
      0,
      `发现 ${hits.length} 处硬编码等宽字体栈（中文会掉回默认字体，与正文不一致）：\n  - ${hits.join('\n  - ')}`,
    )
  })
})

/**
 * UA 默认字体元素的接管。
 *
 * 2026-10-01 真机审计发现的**扫描盲区**：上面那条规则只查源码里写死的
 * font 声明，但浏览器 UA 样式表会给 `code` / `kbd` / `samp` / `pre`
 * 一个**裸 monospace 泛型**——源码里根本不出现这条 font-family。
 *
 * 真机证据（Redmi，/settings）：三个模型名是 `<code class="model-chip">`，
 * 而 `.model-chip` 只设了 font-size/padding/background/border-radius，
 * 于是 getComputedStyle().fontFamily === "monospace"：
 *   - 拿不到应用的等宽字体（JetBrains Mono / Fira Code）
 *   - 更拿不到 tokens.css 给 --font-mono 末尾补的 var(--font-sans) CJK 回退
 *   - 任何中文模型名 / 错误消息落进 <code> 都会字体错乱
 *
 * 也就是说：这一类"字体不对"是**任何源码扫描都抓不到**的，只能靠
 * 「全局重置是否存在」这条正向断言守住。
 */
describe('UA 默认等宽元素必须被接管', () => {
  const globalCss = readFileSync(join(ROOT, 'src', 'styles.css'), 'utf8')

  it('code/kbd/samp/pre 的 font-family 被显式设为 --font-mono', () => {
    const block = globalCss.match(/\b(?:code|kbd|samp|pre)\b[^{]*\{[^}]*\}/g)
    assert.ok(block && block.length > 0, 'styles.css 里没有针对 code/kbd/samp/pre 的重置')
    const hasFontFamily = block.some((b) => /font-family\s*:\s*var\(--font-mono\)/.test(b))
    assert.ok(
      hasFontFamily,
      '必须显式声明 font-family: var(--font-mono)，否则这些元素吃 UA 默认的裸 monospace 泛型，' +
      '既没有应用的等宽字体也没有 CJK 回退（真机 /settings 的模型名就是这样）',
    )
  })

  it('--font-mono 仍以 var(--font-sans) 收尾（CJK 回退不能被这次改动弄丢）', () => {
    const tokens = readFileSync(join(ROOT, 'src', 'styles', 'tokens.css'), 'utf8')
    const mono = tokens.match(/--font-mono\s*:\s*([\s\S]*?);/)
    assert.ok(mono, 'tokens.css 里找不到 --font-mono')
    assert.match(
      mono[1],
      /var\(--font-sans\)\s*$/,
      '--font-mono 必须以 var(--font-sans) 收尾，否则中文在等宽元素里掉回浏览器默认字体',
    )
  })
})

/**
 * 顶栏页面标题的字号必须走 token。
 *
 * 2026-10-02 普查发现：`.top-bar h1` 这个**外壳顶栏标题**只有 opencode 模块自己
 * 定义了字号，且三处都写死 20px；而设置页/邮件页的顶栏标题是
 * `.title { font-size: var(--text-lg) }` = 16px。两条规则除字号外**完全相同**
 * （flex:1 / font-weight:600 / 同一颜色），纯像素差 25%。
 * 用户在「会话」和「设置」之间切页会看到标题忽大忽小——这正是「字体不对」。
 *
 * 为什么锁「必须用 token」而不是「必须等于 16px」：刻度将来整体调档时
 * 改 token 一处即可；写成写死 16px 的规则会一改就红，久了就没人维护。
 */
const TOPBAR_H1_RE = /\.top-bar\s+h1\s*\{([^}]*)\}/g
// 捕获整条声明而不是只捕获数字：报错信息里要能直接看到 `font-size: 20px`。
// （第一版只捕获数字组，于是期望值写成 '20px' 而实际是 '20'，自检立刻报红。）
const RAW_PX_FONT_RE = /font-size\s*:\s*\d+(?:\.\d+)?px/

/**
 * 匹配前必须剥注释。
 *
 * 2026-10-02 实测踩到的坑：`.top-bar h1` 的规则里我加了一句解释性注释
 *   /* 走 token：与设置页的顶栏标题（`.title { font-size: var(--text-lg) }`）同源 * /
 * 那个注释里含一个 `}`，而规则体正则用的是 `\{([^}]*)\}` —— 它在注释的 `}` 处
 * 就截断了，于是**同一块里紧跟其后的 `font-size: 20px` 完全扫不到**，
 * 负控把违规塞回去护栏都不红。
 *
 * 也就是说：注释里的括号能让违规隐身。这是「注释能满足任何源码扫描断言」
 * 那一族的镜像——上次是注释让断言**误报**，这次是让断言**漏报**。
 * `//` 要求前一个字符不是 `:`，否则 `'https://'` 会被当成注释起点。
 */
function stripCssComments(src) {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/[^\n]*/g, '$1 ')
}

/** 从一段 CSS 文本里取出「顶栏标题写死了像素」的位置。 */
function findRawPxTopBarTitles(css) {
  const hits = []
  for (const m of stripCssComments(css).matchAll(TOPBAR_H1_RE)) {
    const f = m[1].match(RAW_PX_FONT_RE)
    if (f) hits.push(f[0])
  }
  return hits
}

describe('顶栏页面标题必须走字号 token', () => {
  it('检测器自检：认得出写死 px，也认得出走 token（防空跑）', () => {
    // 这条判据的失败面是「什么都没扫到 → 数组为空 → 绿」。
    // 真实违例可能恰好是 0 个，所以**不能**用「至少扫到 N 处」来防空跑，
    // 必须直接验检测器本身对合成样本的行为。
    assert.deepEqual(
      findRawPxTopBarTitles('.top-bar h1 { font-size: 20px; }'),
      ['font-size: 20px'],
      '检测器认不出写死 px 的顶栏标题',
    )
    assert.deepEqual(
      findRawPxTopBarTitles('.top-bar h1 { font-size: var(--text-lg); }'),
      [],
      '检测器把走 token 的写法误判成违规',
    )
    // 单行紧凑写法也必须认得（真实代码两种都有；注意回显的是原文，不做归一化）
    assert.deepEqual(findRawPxTopBarTitles('.top-bar h1{font-size:17px}'), ['font-size:17px'])
    // 对抗样本：注释里带 `}` 不得把同一规则块里后面的违规截断掉。
    // 这不是假想——仓库里真的因为这么一句注释让负控不红过一次。
    assert.deepEqual(
      findRawPxTopBarTitles(
        '.top-bar h1 {\n  /* 见 `.title { font-size: var(--text-lg) }` */\n  font-size: 20px;\n}',
      ),
      ['font-size: 20px'],
      '注释里的 } 把规则块截断了，违规隐身',
    )
    // 注释里写了 px 也不该算违规（否则「在注释里解释旧写法」就会永久判红）
    assert.deepEqual(
      findRawPxTopBarTitles('.top-bar h1 {\n  /* 此前是 font-size: 20px */\n  font-size: var(--text-lg);\n}'),
      [],
      '把注释里的旧写法误判成违规',
    )
  })

  it('仓库里没有写死像素的顶栏标题', () => {
    const hits = []
    for (const file of files) {
      const rel = relative(ROOT, file).replace(/\\/g, '/')
      for (const decl of findRawPxTopBarTitles(readFileSync(file, 'utf8'))) {
        hits.push(`${rel}: ${decl}`)
      }
    }
    assert.equal(
      hits.length,
      0,
      `发现 ${hits.length} 处顶栏标题写死了像素字号（与全局 --text-lg 漂移，切页时标题忽大忽小）：\n  - ${hits.join('\n  - ')}`,
    )
  })
})
