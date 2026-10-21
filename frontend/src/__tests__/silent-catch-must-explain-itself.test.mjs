// silent-catch-must-explain-itself.test.mjs
//
// 「静默 catch」必须在同一块里写明为什么可以静默。
//
// 2026-10-02 普查：src 下 535 个 .ts/.vue 里共 119 处 catch 块剥掉注释后为空
// （空 catch 52 + 只含注释的 catch 67），其中 **118 处已带理由注释**，
// 只有 1 处（SettingsLLMGateway 的连通性测试后刷新配置）没有——
// 而那一处经查证确实是合理的（连通性已测成功，后续刷新失败不影响结论），
// 只是没把理由写下来。
//
// 所以本规则**不禁止空 catch**：仓库里 118 处都是有意为之（持久化、预取、
// TTS 缺失、引擎降级……），逐个改代码既无必要也有风险。
// 它只要求一件事：后来人读到这处静默时，能立刻知道「这是有意的还是漏的」。
// 一个没有理由的空 catch 与一个漏捕获的错误，在代码里长得一模一样。
//
// 防空跑用合成样本自检，而不是数真实命中：真实数量会随重构变化，
// 拿它当阈值会在「终于没有静默 catch 了」这个好状态下反而报红。

import assert from 'node:assert/strict'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { dirname, extname, join, relative } from 'node:path'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

// 本文件在 frontend/src/__tests__/，所以 ROOT 是 frontend（退两级），
// SRC 是 frontend/src。第一版写成退三级，直接扫到仓库根去，readdirSync ENOENT。
const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..', '..')
const SRC = join(ROOT, 'src')
const SKIP = new Set(['node_modules', '.git', 'dist', '__tests__', 'android', 'ios'])

function collect(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (SKIP.has(name)) continue
    const full = join(dir, name)
    if (statSync(full).isDirectory()) collect(full, out)
    else if (['.ts', '.vue'].includes(extname(name))) out.push(full)
  }
  return out
}

const LINE = '//'
const BLOCK_OPEN = '/*'

/**
 * 剥掉注释（行注释 + 块注释）。
 *
 * 第一版写成 `.split(LINE).join(' ')` —— 那只把 `//` 这个**标记**换成空格，
 * 注释正文原封不动：`' // note '` 变成 `'  note '`，trim 后非空，
 * 于是「只含注释的静默 catch」被判成「不静默」，自检直接报 0 !== 1。
 * 行注释必须连到行尾一起删。
 *
 * 只匹配「行首（可含空白）的 //」，避免把字符串里的 `https://` 当注释起点。
 * 静默 catch 的块体只可能是整行注释或纯空白，这个范围够用。
 */
function stripComments(src) {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/^[^\S\n]*\/\/[^\n]*/gm, ' ')
}

/**
 * 找出「静默 catch」：块体在剥掉注释后为空。
 *
 * 块体用 `[^{}]*` 限定，所以带嵌套语句/对象字面量的 catch 不会被匹配——
 * 那类本来就不是静默的。
 */
function silentCatches(css) {
  const out = []
  for (const m of css.matchAll(/catch\s*(?:\([^)]*\))?\s*\{([^{}]*)\}/g)) {
    const rawBody = m[1]
    if (stripComments(rawBody).trim().length === 0) {
      out.push({
        at: m.index,
        explained: rawBody.includes(LINE) || rawBody.includes(BLOCK_OPEN),
      })
    }
  }
  return out
}

describe('静默 catch 必须自带理由', () => {
  it('检测器自检：认得出静默、认得出有解释的、认得出真干活的', () => {
    // 没有理由的空 catch —— 必须被抓到且标记为未解释
    const bare = silentCatches('try { a() } catch (e) {}')
    assert.equal(bare.length, 1)
    assert.equal(bare[0].explained, false, '空 catch 应被判为「无理由」')

    // 只含注释 —— 仍是静默，但已解释
    const withNote = silentCatches(`try { a() } catch (e) { ${LINE} 引擎缺失，静默 }`)
    assert.equal(withNote.length, 1)
    assert.equal(withNote[0].explained, true, '带注释的静默 catch 应被判为「有理由」')

    // 真正干活的 catch —— 不该被算成静默
    const real = silentCatches('try { a() } catch (e) { report(e); }')
    assert.equal(real.length, 0, '有语句的 catch 不算静默')

    // 可选 catch 绑定写法 try/catch {}
    const bare2 = silentCatches('try { a() } catch {}')
    assert.equal(bare2.length, 1)
    assert.equal(bare2[0].explained, false)
  })

  it('仓库里没有「无理由的静默 catch」', () => {
    const files = collect(SRC)
    assert.ok(files.length > 200, `只扫到 ${files.length} 个文件，扫描范围可能失效`)
    const bad = []
    for (const f of files) {
      const rel = relative(ROOT, f).replace(/\\/g, '/')
      for (const c of silentCatches(readFileSync(f, 'utf8'))) {
        if (!c.explained) bad.push(`${rel}:${lineOf(f, c.at)}`)
      }
    }
    assert.equal(
      bad.length,
      0,
      `发现 ${bad.length} 处「无理由的静默 catch」——\n` +
        `  它和有理由的静默在代码里长得一模一样，后来人无法区分「有意为之」与「漏捕获」：\n` +
        `  - ${bad.join('\n  - ')}`,
    )
  })
})

function lineOf(file, index) {
  const text = readFileSync(file, 'utf8')
  let n = 1
  for (let i = 0; i < index && i < text.length; i++) if (text[i] === '\n') n++
  return n
}

// ═══════════════════════════════════════════════════════════════════════════
// §948：promise 形态的「拒绝被就地消化」
//
// 上面那条护栏只管 `try/catch` 的**块**。前端还有另一族同形的东西：
//     promise.catch(() => {})
// 它同样把一次拒绝吞掉，而且**长得一模一样**（没有错误、没有提示、没有痕迹）。
//
// 2026-10-21 普查（§948.0）：src 下 promise 形态的吞错点 **92 处**（49 个文件）
//   —— 42 处已在失败分支里留痕（console.warn/error），50 处连痕迹都没有；
//   而**同一时刻**的 `try/catch` 静默块是 **0 处无理由**（本文件上面那条护栏）。
// ⇒ 两族护栏必须**同构**：都不是禁止静默，而是要求**解释自己**。
//
// 处置口径（与 §947「要么被处理、要么被登记」同族）：
//   A（留痕型）：失败在别处看不见 ⇒ handler 体内必须有 console.warn/error。
//   B（静默型）：失败已被别处暴露 / 已被处理 / 设计上就允许 ⇒ 只需写明理由。
// 两种都必须**紧贴这处吞错**地写一句理由：handler 体内，或紧邻的上一行 `//` 注释。
//
// ⚠ 口径必须靠 **AST**（typescript 解析器），不能靠 grep。两处实测踩坑：
//   ① `catch(() => null)` 这一族用 `ts.isIdentifier` 判不出来 ——
//      `null` 在 TS AST 里是 **NullKeyword**（不是 Identifier）⇒ 整族被漏掉：
//      第一版量具只扫到 81 处，**漏了 8 处**。⇒ 「量出来的数偏小」的第一嫌疑
//      是量具漏了一族形态，不是仓库变干净了。
//   ② `.catch` 的**调用起始行**不等于 `catch` 关键字所在行
//      （`store.refresh()\n  .catch(...)` 这种形态差一行）⇒ 判定「紧邻上一行」
//      必须取 `catch` 标识符自己的行号。
// ═══════════════════════════════════════════════════════════════════════════

const requireTs = createRequire(join(ROOT, 'package.json'))
const ts = requireTs('typescript')

const PROMISE_SKIP = new Set(['node_modules', '.git', 'dist', '__tests__', '__mocks__', 'android', 'ios'])
const PROMISE_EXTS = ['.ts', '.tsx', '.mjs', '.js', '.vue']

function collectSources(dir, out = []) {
  for (const name of readdirSync(dir)) {
    if (PROMISE_SKIP.has(name)) continue
    const full = join(dir, name)
    if (statSync(full).isDirectory()) collectSources(full, out)
    else if (PROMISE_EXTS.includes(extname(name))) out.push(full)
  }
  return out
}

/** .vue 的 <script> 块（带**文件**行偏移）；其它文件整份当一块。 */
function scriptBlocks(file, raw) {
  if (!file.endsWith('.vue')) return [{ code: raw, lineOffset: 0 }]
  const out = []
  const re = /<script[^>]*>/g
  let m
  while ((m = re.exec(raw))) {
    const start = m.index + m[0].length
    const end = raw.indexOf('</script>', start)
    if (end < 0) continue
    out.push({ code: raw.slice(start, end), lineOffset: raw.slice(0, start).split('\n').length - 1 })
  }
  return out
}

/** 区间内的注释 token（用 TS 扫描器，不靠字符串里找 '//' —— URL 不是注释）。 */
function commentTokens(code, from, to) {
  const scanner = ts.createScanner(ts.ScriptTarget.Latest, false, ts.LanguageVariant.Standard, code)
  scanner.setTextPos(from)
  const out = []
  for (;;) {
    const kind = scanner.scan()
    if (kind === ts.SyntaxKind.EndOfFileToken) break
    const pos = scanner.getTokenPos()
    if (pos >= to) break
    if (kind === ts.SyntaxKind.SingleLineCommentTrivia || kind === ts.SyntaxKind.MultiLineCommentTrivia) {
      out.push(code.slice(pos, scanner.getTextPos()))
    }
  }
  return out
}

/**
 * 判一个 `.catch(h)` 是不是本节口径的「就地消化」，并给出它的分类。
 *
 * 口径（三选一）：
 *   ① 静默型 B：handler 是空块，或表达式体是 null / undefined / void x。
 *   ② 留痕型 A：handler 的语句只含 console.warn/error（可带结尾 return null）。
 *   ③ 不属本节口径：置状态、rethrow、命名 handler 等 —— 那些不是「吞掉」。
 *
 * ⚠ 表达式体判空必须同时认 NullKeyword 与 VoidExpression：
 *   `null` 不是 Identifier（TS AST 里是 NullKeyword），`void x` 也不是。
 */
function classifyCatchHandler(node, code, sf, lines) {
  const arg = node.arguments[0]
  if (!arg) return null
  // 命名 handler（catch(handleError)）不是「吞掉」⇒ 不属本节口径。
  if (!ts.isArrowFunction(arg) && !ts.isFunctionExpression(arg)) return null
  const body = arg.body
  let exprForm = false
  if (!ts.isBlock(body)) {
    const silentExpr =
      (ts.isIdentifier(body) && body.text === 'undefined') ||
      body.kind === ts.SyntaxKind.NullKeyword ||
      ts.isVoidExpression(body)
    if (!silentExpr) return null
    exprForm = true
  }

  const isTrace = (s) =>
    ts.isExpressionStatement(s) &&
    ts.isCallExpression(s.expression) &&
    ts.isPropertyAccessExpression(s.expression.expression) &&
    s.expression.expression.expression.getText(sf) === 'console' &&
    (s.expression.expression.name.text === 'warn' || s.expression.expression.name.text === 'error')
  const isNullReturn = (s) =>
    ts.isReturnStatement(s) &&
    s.expression &&
    (s.expression.kind === ts.SyntaxKind.NullKeyword ||
      (ts.isIdentifier(s.expression) && s.expression.text === 'undefined'))

  let traced = false
  if (!exprForm) {
    const stmts = body.statements.filter((s) => !ts.isEmptyStatement(s))
    if (stmts.length > 0) {
      if (!stmts.every((s) => isTrace(s) || isNullReturn(s))) return null
      traced = stmts.some(isTrace)
    }
  }

  // `catch` 关键字自己的行号（不是调用表达式的起始行 —— 两者可以差一行）。
  const { line } = sf.getLineAndCharacterOfPosition(node.expression.name.getStart(sf))
  const prevLine = (lines[line - 1] ?? '').trim()
  const inBody = exprForm ? false : commentTokens(code, body.getStart() + 1, body.getEnd()).length > 0
  const above = prevLine.startsWith('//')
  return { line: line + 1, cls: traced ? 'A' : 'B', explained: inBody || above, inBody, above }
}

/** 扫一份源码里的全部 promise 形态吞错点。 */
function scanPromiseSites(code, fileName) {
  const kind = fileName.endsWith('.tsx')
    ? ts.ScriptKind.TSX
    : fileName.endsWith('.ts')
      ? ts.ScriptKind.TS
      : ts.ScriptKind.JS
  const sf = ts.createSourceFile(fileName, code, ts.ScriptTarget.Latest, true, kind)
  const lines = code.split('\n')
  const sites = []
  const visit = (node) => {
    if (
      ts.isCallExpression(node) &&
      ts.isPropertyAccessExpression(node.expression) &&
      node.expression.name.text === 'catch'
    ) {
      const site = classifyCatchHandler(node, code, sf, lines)
      if (site) sites.push(site)
    }
    ts.forEachChild(node, visit)
  }
  visit(sf)
  return sites
}

/** 全仓扫描：返回 { 站点明细, 按文件分组 }。 */
function censusPromiseSites() {
  const files = collectSources(SRC)
  const byFile = {}
  const details = []
  for (const file of files) {
    const rel = relative(ROOT, file).replace(/\\/g, '/')
    const raw = readFileSync(file, 'utf8')
    for (const { code, lineOffset } of scriptBlocks(file, raw)) {
      for (const site of scanPromiseSites(code, file)) {
        const at = `${rel}:${site.line + lineOffset}`
        details.push({ at, ...site })
        ;(byFile[rel] ||= []).push(site.cls)
      }
    }
  }
  return { files, byFile, details }
}

/**
 * 台账：每个文件的吞错点**分类序列**（按行序）。
 *
 *   A = 留痕型（handler 体内有 console.warn/error）
 *   B = 静默型（handler 体内无留痕）
 *
 * 为什么要钉到「文件 → 序列」而不是只钉条数：只钉条数的话，
 * 「新增一处在**新文件**里」只要把计数 +1 就能混过去（§947 的同一条教训）。
 * 钉序列则连「哪一处是 A、哪一处是 B」都被钉住 ⇒ 改分类必须显式改台账。
 */
const PROMISE_LEDGER = {
  'src/app/AppLayout.vue': 'A',
  'src/composables/useApprovalAlerts.ts': 'A',
  'src/composables/useDetailFlow.ts': 'BBB',
  'src/composables/useHaptics.ts': 'B',
  'src/features/agents/AgentDetailView.vue': 'B',
  'src/features/agents/AgentEditView.vue': 'B',
  'src/features/agents/AgentLibraryView.vue': 'BB',
  'src/features/ai-chat/AIChatView.vue': 'B',
  'src/features/ai-chat/AgentSelectorSheet.vue': 'BB',
  'src/features/ai-chat/aiChatStore.ts': 'A',
  'src/features/email/EmailDetailView.vue': 'A',
  'src/features/email/EmailFolderPickerSheet.vue': 'B',
  'src/features/email/EmailInboxView.vue': 'B',
  'src/features/email/account-sync.ts': 'BA',
  'src/features/email/email-body-prefetch.ts': 'B',
  'src/features/email/email-fetch-host.ts': 'A',
  'src/features/email/email-fetch-run.ts': 'B',
  'src/features/email/use-invoice-list.ts': 'A',
  'src/features/finance/FinanceView.vue': 'B',
  'src/features/flashcards/CardBrowserView.vue': 'B',
  'src/features/flashcards/DeckOptionsView.vue': 'B',
  'src/features/flashcards/FlashcardDeckView.vue': 'B',
  'src/features/flashcards/FlashcardEditView.vue': 'AABBABB',
  'src/features/flashcards/FlashcardListView.vue': 'BB',
  'src/features/flashcards/FlashcardReviewView.vue': 'ABA',
  'src/features/flashcards/StatsView.vue': 'B',
  'src/features/marketplace/AgentMarketView.vue': 'BB',
  'src/features/marketplace/SkillMarketView.vue': 'BB',
  'src/features/marketplace/WorkbuddyView.vue': 'BB',
  'src/features/meetings/audio-parts.ts': 'B',
  'src/features/meetings/ingest-speech.ts': 'AA',
  'src/features/messages/MessagesHubView.vue': 'AAA',
  'src/features/notes/note-files.ts': 'BAA',
  'src/features/notes/notes-persist.ts': 'AAAA',
  'src/features/notifications/NotificationsView.vue': 'B',
  'src/features/scheduled-tasks/ScheduledTaskDetailView.vue': 'B',
  'src/features/scheduled-tasks/ScheduledTaskListView.vue': 'B',
  'src/features/sessions/useSessionLiveRecord.ts': 'A',
  'src/features/study/StudyHubView.vue': 'BA',
  'src/features/vault/VaultEntryView.vue': 'BB',
  'src/main.ts': 'AA',
  'src/native/config-sync/apply.ts': 'B',
  'src/native/local-db.ts': 'AAAAAAAAAA',
  'src/native/localNotifications.ts': 'B',
  'src/native/recording-voice-prompt.ts': 'BB',
  'src/native/recordingRuntime.ts': 'BB',
  'src/services/notificationDispatcher.ts': 'BB',
  'src/services/websocket-hub.ts': 'A',
  'src/stores/device.ts': 'AA',
  'src/stores/locale.ts': 'A',
  'src/stores/theme.ts': 'A',
}

/** 台账钉住的绝对条数（§947 的同一条纪律：须钉住绝对条数）。 */
const PROMISE_TOTAL = 92

describe('§948 · promise 形态的拒绝被就地消化', () => {
  it('★ 尺子自检：认得出静默 / 留痕 / 非本节口径，认得出理由，认不出假注释', () => {
    // ① 空 handler ⇒ 静默型 B，且**没有**解释
    const bare = scanPromiseSites('p.catch(() => {})', 'x.ts')
    assert.equal(bare.length, 1, '空 catch 没被认出来')
    assert.equal(bare[0].cls, 'B')
    assert.equal(bare[0].explained, false, '空 catch 不该被判成「已解释」')

    // ② handler 体内带注释 ⇒ 静默型 B，但**已解释**
    const noted = scanPromiseSites('p.catch(() => {\n  // 理由\n})', 'x.ts')
    assert.equal(noted.length, 1)
    assert.equal(noted[0].cls, 'B')
    assert.equal(noted[0].explained, true, '体内注释必须算「已解释」')

    // ③ `catch(() => null)` ⇒ 静默型 B（⚠ null 是 NullKeyword，不是 Identifier）
    const nullForm = scanPromiseSites('p.catch(() => null)', 'x.ts')
    assert.equal(nullForm.length, 1, 'catch(() => null) 没被认出来（NullKeyword 漏检）')
    assert.equal(nullForm[0].cls, 'B')

    // ④ 留痕型 A（体内有 console.warn）
    const traced = scanPromiseSites("p.catch((e) => {\n  // 理由\n  console.warn('x', e)\n})", 'x.ts')
    assert.equal(traced.length, 1, '带 console.warn 的 handler 必须被认成吞错点')
    assert.equal(traced[0].cls, 'A', '有 console.warn 的 handler 必须判成留痕型')

    // ⑤ 留痕型 + 结尾 return null（降级为 null 的形态）
    const tracedNull = scanPromiseSites("p.catch((e) => {\n  console.warn('x', e)\n  return null\n})", 'x.ts')
    assert.equal(tracedNull.length, 1, '带 return null 的留痕 handler 被误判成「不属本节口径」')
    assert.equal(tracedNull[0].cls, 'A')

    // ⑥ 置状态 / rethrow / 命名 handler ⇒ **不属本节口径**（那不是「吞掉」）
    assert.equal(scanPromiseSites('p.catch((e) => { err.value = e })', 'x.ts').length, 0,
      '置状态的 handler 不该算吞错点')
    assert.equal(scanPromiseSites('p.catch((e) => { throw e })', 'x.ts').length, 0,
      'rethrow 的 handler 不该算吞错点')
    assert.equal(scanPromiseSites('p.catch(handleError)', 'x.ts').length, 0,
      '命名 handler 不该算吞错点')

    // ⑦ 紧邻上一行的 `//` 注释 ⇒ 已解释（与体内注释等价）
    const above = scanPromiseSites('// 理由\np.catch(() => {})', 'x.ts')
    assert.equal(above.length, 1)
    assert.equal(above[0].explained, true, '紧邻上一行的注释必须算「已解释」')

    // ⑧ 空行隔开的注释**不算**（那属于别的语句）
    const blank = scanPromiseSites('// 理由\n\np.catch(() => {})', 'x.ts')
    assert.equal(blank.length, 1)
    assert.equal(blank[0].explained, false, '被空行隔开的注释不该算「已解释」')

    // ⑨ 字符串里的 '//' 不是注释（URL 形态）
    const url = scanPromiseSites("p.catch(() => { const u = 'https://x/y' })", 'x.ts')
    assert.equal(url.length, 0, '有语句的 handler 不该算吞错点')
    const urlOnly = scanPromiseSites("p.catch(() => {\n  // https://x/y\n})", 'x.ts')
    assert.equal(urlOnly.length, 1)
    assert.equal(urlOnly[0].explained, true, '注释里的 URL 不影响判定')
  })

  it('★ 扫面没塌缩（尺子没空转）', () => {
    const { files, details } = censusPromiseSites()
    assert.ok(files.length > 200, `只扫到 ${files.length} 个文件，扫描范围可能失效`)
    assert.equal(details.length, PROMISE_TOTAL,
      `吞错点实测 ${details.length} 处 ≠ 台账钉的 ${PROMISE_TOTAL} 处\n` +
        '  ⇒ 要么有人新增/删除了一处（须显式改台账并重新定性），要么量具漏扫了一族形态。')
  })

  it('★★ 每个吞错点都必须解释自己（理由在体内，或紧邻上一行）', () => {
    const { details } = censusPromiseSites()
    const bad = details.filter((d) => !d.explained)
    assert.equal(bad.length, 0,
      `发现 ${bad.length} 处「没解释自己的吞错点」——\n` +
        '  它和有理由的吞错在代码里长得一模一样，后来人无法区分「有意为之」与「漏捕获」：\n' +
        `  - ${bad.map((b) => b.at).join('\n  - ')}`)
  })

  it('★★★ 台账与实测双向相等（文件集合 + 每文件分类序列）', () => {
    const { byFile } = censusPromiseSites()
    const missing = Object.keys(PROMISE_LEDGER).filter((f) => !byFile[f])
    const extra = Object.keys(byFile).filter((f) => !PROMISE_LEDGER[f])
    const mismatch = Object.keys(PROMISE_LEDGER)
      .filter((f) => byFile[f] && byFile[f].join('') !== PROMISE_LEDGER[f])
      .map((f) => `  ${f}: 实测 ${byFile[f].join('')} ≠ 台账 ${PROMISE_LEDGER[f]}`)
    assert.equal(missing.length, 0,
      `台账里的这些文件已没有吞错点（代码改了）——请重新定性并删掉台账条目：\n  ${missing.join('\n  ')}`)
    assert.equal(extra.length, 0,
      `这些文件出现了台账未登记的吞错点——必须显式定性（A/B）并登记进台账：\n  ${extra.join('\n  ')}`)
    assert.equal(mismatch.length, 0,
      `这些文件的分类与台账不一致（多半是有人把某一处的留痕删了/加了）——\n${mismatch.join('\n')}`)
  })
})
