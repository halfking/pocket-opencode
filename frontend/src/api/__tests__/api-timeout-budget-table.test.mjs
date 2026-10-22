// api-timeout-budget-table.test.mjs
//
// 一张表守住一条不变式：**前端给某条路由的客户端超时，必须大于服务端自己
// 给它设的执行预算。** 反了就是「服务端成功了、客户端报失败」。
//
// 为什么要有这张表
// ----------------
// 2026-10-03 之前，这条不变式是**逐个手工核对**的——所以它漏了。
// 一次普查在长路由里找出五处违反：
//
//   /api/notes/{id}/summarize      客户端 30s  服务端 60s
//   /api/meetings/{id}/summary      客户端 30s  服务端 45s
//   /api/meetings/{id}/refine       客户端 30s  服务端 90s
//   /api/emails/invoices/extract    客户端 30s  服务端无上限（实测单封 >30s）
//   /api/stt/transcribe             客户端 120s 服务端 120s ← **相等即错**
//   /api/stt/probe                  客户端 120s 服务端 120s ← **相等即错**
//
// 「相等即错」值得单说：客户端计时从请求发出开始，服务端的从 handler 进来
// 开始，中间还隔着网络与鉴权。所以取相等值时客户端**实际总是先到点**，
// 于是「刚好用满预算」的那一档必然失败——而且它不总是复现，看起来像网络抖动。
//
// 手工核对的失效模式很清楚：下次有人加一条长路由，表不会自己更新。
// 所以这张表是**数据驱动**的——每行自己声明后端预算的推导方式与前端常量的
// 名字，新增一条长路由只要在这里加一行。
//
// 另外两条（pipeline 15 分钟 / 归类 900 秒）由
// email-long-request-budget.test.mjs 守，那两条的预算是从源码**反推**的
// （一个整体 WithTimeout、一个「单封 × 条数」的循环），推导方式与本表不同。
//
// 负控在文件末尾，全部实测转红。

import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const ts = require('typescript')

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..')                    // frontend/src
const SERVER = path.resolve(SRC, '..', '..', 'backend', 'internal', 'server')

/**
 * 剥 Go 注释 —— 判据不能被源码里的说明文字满足。
 *
 * ⚠⚠ §930：**不再自带一份**，改用共享的 `_wire-helpers.mjs` 那份（quote-aware）。
 *   本文件此前那份是「先做块注释的非贪婪配对、再按行首 `//` 切行」
 *   ⇒ **字符串字面量里的斜杠会被当成注释起点** ⇒ 会吞掉真代码。
 *   ⇒ 而本表要读的 `server.go`（§930 新增：long-lived 白名单）里
 *   **正好有**那种「路径里带通配符」的**字符串字面量** ⇒ 朴素的会被吃掉两行。
 *
 * ⚠ 写这段注释时踩过一次：正文里若出现**块注释的结束符**，会把自己的注释
 *   提前闭合 ⇒ 下一个词被当成代码 ⇒ SyntaxError。⇒ 注释里别写结束符。
 */
import { stripGoComments } from './_wire-helpers.mjs'
export { stripGoComments }

/** 取 Go 里 `func (s *Server) <name>(` 的函数体（大括号配平）。 */
export function goFuncBody(src, name) {
  const code = stripGoComments(src)
  const start = code.indexOf(`func (s *Server) ${name}(`)
  if (start < 0) return null
  const open = code.indexOf('{', start)
  if (open < 0) return null
  let depth = 0
  for (let i = open; i < code.length; i++) {
    if (code[i] === '{') depth++
    else if (code[i] === '}') {
      depth--
      if (depth === 0) return code.slice(open, i)
    }
  }
  return null
}

const TIME_UNITS = { Second: 1000, Minute: 60_000, Hour: 3_600_000 }

/**
 * 收集**函数体内**声明的时长常量/变量：名字 → 毫秒。
 *
 * 为什么需要它（2026-10-07 实测打出来的）
 * --------------------------------------
 * 这张表建起来时，判据只认内联字面量
 * `context.WithTimeout(r.Context(), 45 * time.Second)`。于是 2026-10-06 修
 * 「agent 回落从未跑成」时把预算提名为局部常量：
 *
 *     const summaryBudget = 45 * time.Second
 *     ctx, cancel := context.WithTimeout(r.Context(), summaryBudget)
 *
 * —— 这是**更地道的 Go 写法**（尤其是同一函数里有两个预算要区分时），
 *   但让本表两行直接反推失败。危险的走向不是「红」，而是有人把
 *   `assert.notEqual(s.ms, null)` 放宽成 warning：**整张表就此静默失去守护**，
 *   而表里的 5 行一条都不报错。
 *
 * ⚠ 只在**该 handler 函数体内**找常量，不做全文件/跨函数查找。
 *   找不到就近捡一个同名或别的预算，正是「判据放错层」——
 *   算出来的数看着合理，但量的不是这个请求的预算。
 *
 * ⚠ 输入是**已剥注释**的函数体（goFuncBody 内部就剥了），
 *   所以注释里的 `// const x = 45 * time.Second` 满足不了判据。
 *
 * ⚠ 认三种声明形态：`const x = …`、`var x = …`、`x := …`。
 *   少认一种的后果不是「报红」而是同一行静默失去守护——
 *   2026-10-07 变异验证时实测：`:=` 起初没被认，而它恰恰是 Go 局部
 *   最常见、也最容易被下一个人写出来的形态。
 *
 * ⚠ **不认裸赋值** `x = 45 * time.Second`：那是给别处声明的变量赋值，
 *   不能当作「本函数体内的预算声明」。
 */
export function localDurationConsts(body) {
  const out = new Map()
  const re =
    /\b(?:(?:const|var)\s+([A-Za-z_]\w*)\s*(?::[^=]+)?=|([A-Za-z_]\w*)\s*:=)\s*([0-9]+)\s*\*\s*time\.(Second|Minute|Hour)\b/g
  let m
  while ((m = re.exec(body)) !== null) {
    const name = m[1] ?? m[2]
    out.set(name, Number(m[3]) * TIME_UNITS[m[4]])
  }
  return out
}

/**
 * 取某 handler 的**请求派生**预算。
 *
 * 只认 `context.WithTimeout(r.Context(), …)`——派生自 r.Context() 的预算
 * 才是「服务端愿意为一个请求花多久」；`context.Background()` 那个是后台
 * goroutine 的预算，与本请求无关（server_email_invoice.go:109 就有一处，
 * 早先的判据把它算进来过，是假信号）。
 *
 * 第二个参数可以是**内联字面量**，也可以是**本函数体内声明的时长常量**
 * （见 localDurationConsts）。两者都解析不出 → 返回 null，
 * **绝不**从别处捡一个数来填。
 *
 * 接受**源码文本**而不是只能读文件：负控要把后端源码改掉再复算，
 * 让它去写临时 .go 文件既脏又容易在 Windows 上失败。
 */
/**
 * ★ §930：**包级**时长常量（名字 → 毫秒）。
 *
 * ⚠ 为什么必须单独一张表：本表的 `localDurationConsts` 只认**函数体内**的声明，
 *   而生产代码里有一批预算是**文件级**的 ——
 *   `fullTranscribeTimeout`（server_stt_stream.go:39，10 分钟）、
 *   `opsExecTimeout`（server_email_ops.go:224，90 秒）、
 *   `meetingAnalyzeTimeout`（server_meeting_analyze.go:39，90 秒）。
 *   ⇒ 没有这张表，**这些路由的服务端预算反推不出来** ⇒ 它们根本进不了这张表
 *   ⇒ ⇒ 表的覆盖面就停在「预算写成内联字面量或局部常量」的那些路由上。
 *
 * ⚠ 只认**包级**（顶层）声明：函数内的由 localDurationConsts 负责。
 * ⚠ 右侧不是字面量乘法就跳过 —— 宁可漏判也不猜。
 */
export function packageDurationConsts(goSrc) {
  const out = new Map()
  const code = stripGoComments(goSrc)
  const re = /^const\s+([A-Za-z_]\w*)\s*(?::[^=]+)?=\s*([0-9]+)\s*\*\s*time\.(Second|Minute|Hour)\b/gm
  let m
  while ((m = re.exec(code)) !== null) {
    out.set(m[1], Number(m[2]) * TIME_UNITS[m[3]])
  }
  return out
}

export function budgetInSource(goSrc, handler) {
  const body = goFuncBody(goSrc, handler)
  if (body === null) return { ms: null, reason: `找不到 handler ${handler}` }
  const m = /context\.WithTimeout\(r\.Context\(\),\s*(?:([0-9]+)\s*\*\s*time\.(Second|Minute|Hour)|([A-Za-z_]\w*))\s*\)/.exec(body)
  if (!m) return { ms: null, reason: `${handler} 里没有派生自 r.Context() 的 WithTimeout` }
  if (m[1] != null) {
    return {
      ms: Number(m[1]) * TIME_UNITS[m[2]],
      reason: `${handler}: ${m[1]}${m[2][0]} on r.Context()`,
    }
  }
  const name = m[3]
  const consts = localDurationConsts(body)
  if (consts.has(name)) {
    const ms = consts.get(name)
    return { ms, reason: `${handler}: ${name}=${ms}ms on r.Context()` }
  }
  // ⚠ §930：函数体内没有 ⇒ 找**包级**常量（fullTranscribeTimeout 那种）。
  //   此前这一步直接返回 null ⇒ 那类路由**根本进不了这张表**。
  const pkg = packageDurationConsts(goSrc)
  if (pkg.has(name)) {
    const ms = pkg.get(name)
    return { ms, reason: `${handler}: ${name}=${ms}ms on r.Context()（包级常量）` }
  }
  return {
    ms: null,
    reason: `${handler} 的预算用标识符 ${name} 传入，但函数体内与包级都没有它的时长常量声明` +
      `（本判据不跨函数捡数，也不猜）`,
  }
}

export function requestBudgetMs(goFile, handler) {
  return budgetInSource(fs.readFileSync(path.join(SERVER, goFile), 'utf8'), handler)
}

/** 取 TS 里 `export const NAME = <算术>` 的毫秒值。 */
export function constMs(tsFile, name) {
  const src = fs.readFileSync(path.join(SRC, tsFile), 'utf8')
  const sf = ts.createSourceFile('x.ts', src, ts.ScriptTarget.Latest, true)
  for (const st of sf.statements) {
    if (!ts.isVariableStatement(st)) continue
    for (const d of st.declarationList.declarations) {
      if (ts.isIdentifier(d.name) && d.name.text === name) return evalArith(d.initializer)
    }
  }
  return null
}

function evalArith(node) {
  if (!node) return null
  if (ts.isNumericLiteral(node)) return Number(node.text.replace(/_/g, ''))
  if (ts.isParenthesizedExpression(node)) return evalArith(node.expression)
  if (ts.isBinaryExpression(node)) {
    const l = evalArith(node.left)
    const r = evalArith(node.right)
    if (l === null || r === null) return null
    switch (node.operatorToken.kind) {
      case ts.SyntaxKind.PlusToken: return l + r
      case ts.SyntaxKind.MinusToken: return l - r
      case ts.SyntaxKind.AsteriskToken: return l * r
      case ts.SyntaxKind.SlashToken: return r === 0 ? null : l / r
      default: return null
    }
  }
  return null
}

/** 造一个 handler：预算经标识符 `summaryBudget` 传入，decl 是它上方的声明行。 */
function sampleWithDecl(decl) {
  return [
    'package server',
    'func (s *Server) handleMeetingSummary(w http.ResponseWriter, r *http.Request) {',
    decl,
    '    ctx, cancel := context.WithTimeout(r.Context(), summaryBudget)',
    '    _ = ctx',
    '    cancel()',
    '}',
  ].join('\n')
}

/**
 * 这张表就是那张「账单」。
 *
 * server 为 null 表示该 handler 根本没有整体上限（耗时全在下游 IMAP 上），
 * 这时只能给一个下限依据 —— 这里用 harvest 的 5 分钟作为同类操作的参照，
 * 并在 note 里写明为什么。
 */
const TABLE = [
  {
    route: 'POST /api/notes/{id}/summarize',
    tsFile: 'api/notes.ts',
    constName: 'NOTE_SUMMARIZE_TIMEOUT_MS',
    server: { goFile: 'server_assistant.go', handler: 'handleNoteSummarize' },
    note: '笔记即时总结——用户报的「没有即时总结」',
  },
  {
    route: 'POST /api/meetings/{id}/summary',
    tsFile: 'api/meetings.ts',
    constName: 'MEETING_SUMMARY_TIMEOUT_MS',
    server: { goFile: 'server_meeting.go', handler: 'handleMeetingSummary' },
    note: '会议摘要',
  },
  {
    route: 'POST /api/meetings/{id}/refine',
    tsFile: 'api/meetings.ts',
    constName: 'MEETING_REFINE_TIMEOUT_MS',
    server: { goFile: 'server_meeting.go', handler: 'handleMeetingRefine' },
    note: '事后精翻',
  },
  {
    route: 'POST /api/stt/probe',
    tsFile: 'api/stt-settings.ts',
    constName: 'STT_PROBE_TIMEOUT_MS',
    server: { goFile: 'server_stt_settings.go', handler: 'handleSTTProbe' },
    note: '设置页「试转」',
  },
  {
    // 2026-10-02 round7 补：这张表建起来时**漏了这一行**，而它恰好是提交说明里
    // 专门点名的「相等即错」那两条之一。实测负控：把 STT_TRANSCRIBE_TIMEOUT_MS
    // 退回修复前的 120_000（== 服务端 handleSttTranscribe 的预算），本文件 8 例全绿。
    // 常量没有任何测试引用 → 这条不变式在 STT 转写上是没人守的。
    route: 'POST /api/stt/transcribe',
    tsFile: 'api/stt.ts',
    constName: 'STT_TRANSCRIBE_TIMEOUT_MS',
    server: { goFile: 'server_assistant.go', handler: 'handleSttTranscribe' },
    note: '会议长录音转写（「相等即错」那一条）',
  },
  {
    // ⚠⚠ §930 新增：这一行此前**进不了这张表**，因为服务端预算是
    //   **包级常量**（fullTranscribeTimeout，server_stt_stream.go:39）⇒
    //   旧的反推只认函数体内声明 ⇒ 直接返回 null ⇒ 「反推失败」看起来像
    //   「这个路由没有预算」，其实只是**解析器少认一种形态**。
    //   ⇒ 现在包级常量也能反推了 ⇒ 这一行才写得出来。
    route: 'POST /api/stt/transcribe-full',
    tsFile: 'api/stt-settings.ts',
    constName: 'FULL_TRANSCRIBE_TIMEOUT_MS',
    server: { goFile: 'server_stt_stream.go', handler: 'handleSttTranscribeFull' },
    note: '整场录音的兜底全量转写（服务端 10 分钟，客户端 11 分钟）',
  },
  {
    // ⚠⚠ §930 新增：这一行是本节**修掉的那个缺陷**本身。
    //   服务端预算是 `context.WithTimeout(r.Context(), 120*time.Second)`
    //   （server_stt_settings.go:760）⇒ **120 秒**；
    //   而客户端此前吃的是**共用**的 LONG_REQUEST_TIMEOUT_MS = **120 秒**
    //   ⇒ ⇒ **相等即错** ⇒ 「刚好用满预算」的那一档必然失败。
    //   ⚠ 服务端 server.go 的 longLivedPaths 注释曾写「discover 90 秒」
    //   ⇒ 那是**过时的**（代码是 120 秒）⇒ 注释已一并更正。
    //   ⇒ 客户端已改用专用常量 STT_DISCOVER_TIMEOUT_MS = 150 秒。
    route: 'POST /api/stt/discover',
    tsFile: 'api/stt-settings.ts',
    constName: 'STT_DISCOVER_TIMEOUT_MS',
    server: { goFile: 'server_stt_settings.go', handler: 'handleSTTDiscover' },
    note: '重新扫描网关（「相等即错」那一条，§930 修）',
  },
]

describe('客户端超时 ≥ 服务端预算（表驱动）', () => {
  for (const row of TABLE) {
    it(`${row.route} 的客户端超时严格大于服务端预算（${row.note}）`, () => {
      const s = requestBudgetMs(row.server.goFile, row.server.handler)
      assert.notEqual(s.ms, null, `反推服务端预算失败：${s.reason}`)
      const c = constMs(row.tsFile, row.constName)
      assert.notEqual(c, null, `${row.tsFile} 里找不到 ${row.constName}`)
      assert.ok(
        c > s.ms,
        `${row.constName}=${c}ms 必须大于服务端 ${s.ms}ms（${s.reason}）；` +
        `否则前端会比服务端先放弃，界面报失败而后端已经把结果写好了`,
      )
    })
  }
})

describe('判据自检：负控必须转红', () => {
  it('把某行常量改回默认 30s 量级 → 该行转红', () => {
    const row = TABLE[0]
    assert.notEqual(constMs(row.tsFile, row.constName), 30_000, '常量本来就是 30s，样本无效')
    const server = requestBudgetMs(row.server.goFile, row.server.handler).ms
    const broken = 30_000
    assert.ok(
      broken <= server,
      '负控本该转红却判成了通过——判据没在读常量',
    )
  })

  it('后端把某个预算调大 → 表里的反推跟着变（证明不是写死的）', () => {
    const row = TABLE[1] // handleMeetingSummary 45s（2026-10-06 起是局部常量 summaryBudget）
    const src = fs.readFileSync(path.join(SERVER, row.server.goFile), 'utf8')
    const before = budgetInSource(src, row.server.handler).ms
    assert.equal(before, 45_000, '判据在真实代码上就没算对（预算写法可能变了）')
    // 把 45 改成 450：反推必须跟着变成 450_000。
    // 写死 45_000 的判据在这里会纹丝不动——那才是假护栏。
    // 锚点带常量名，避免误伤同文件里别处的 45 * time.Second。
    const patched = src.replace(
      /const\s+summaryBudget\s*=\s*45\s*\*\s*time\.Second/,
      'const summaryBudget = 450*time.Second',
    )
    assert.notEqual(patched, src, '负控样本没有真的改到预算（替换没命中）')
    assert.equal(
      budgetInSource(patched, row.server.handler).ms,
      450_000,
      '后端预算改了 10 倍而反推没跟着变——判据是写死的，假护栏',
    )
    // 而且改大之后，客户端那个常量就**不够**了，判据必须转红。
    const client = constMs(row.tsFile, row.constName)
    assert.ok(
      client <= 450_000,
      '后端预算涨到 450s 之后客户端仍被判为足够——余量逻辑坏了',
    )
  })

  it('预算是局部常量（不是内联字面量）→ 也能反推出同一个数', () => {
    // 本表覆盖的 5 行里有两行是字面量、一行是常量。两种形态都得算得对，
    // 否则「有人把字面量提名为常量」就会让这行静默失去守护。
    const row = TABLE[1]
    const src = fs.readFileSync(path.join(SERVER, row.server.goFile), 'utf8')
    const viaConst = budgetInSource(src, row.server.handler)
    const viaLiteral = budgetInSource(
      src.replace('const summaryBudget = 45 * time.Second', 'const summaryBudget = 45*time.Second'),
      row.server.handler,
    )
    assert.equal(viaConst.ms, 45_000, `常量形态反推失败：${viaConst.reason}`)
    assert.equal(viaLiteral.ms, 45_000, `字面量形态反推失败：${viaLiteral.reason}`)
  })

  // 2026-10-07 变异验证实测：`:=` 起初没被认，而它是 Go 局部最常见的写法。
  // 「反推失败」表现为整行红，看起来像产品坏了，其实只是解析器少认一种形态。
  for (const decl of ['const summaryBudget = 45 * time.Second', 'var summaryBudget = 45 * time.Second', 'summaryBudget := 45 * time.Second']) {
    it(`预算是 ${decl.split(' ')[0].replace(/summaryBudget$/, ':=').trim() || ':='} 声明 → 反推出 45000`, () => {
      const r = budgetInSource(sampleWithDecl(decl), 'handleMeetingSummary')
      assert.equal(r.ms, 45_000, `声明形态未被识别：${r.reason}`)
    })
  }

  it('裸赋值 `x = 45 * time.Second` 不得被当作声明（那不是本函数体的预算来源）', () => {
    const r = budgetInSource(sampleWithDecl('summaryBudget = 45 * time.Second'), 'handleMeetingSummary')
    assert.equal(r.ms, null, `裸赋值被当成了声明：${r.reason}`)
  })

  it('注释里的时长常量不得满足判据（判据必须剥注释）', () => {
    // 回归钉子：本会话已在别处踩过六次「源码扫描先剥注释」。
    // 这里造一个「注释里有 const、代码里没有」的样本——
    // 不剥注释的解析器会拿注释里的数把这一行判绿。
    const r = budgetInSource(sampleWithDecl('// const summaryBudget = 45 * time.Second'), 'handleMeetingSummary')
    assert.equal(r.ms, null, `注释里的常量被当成了真预算：${r.reason}`)
  })

  it('常量在别的函数里 → 返回 null，不跨函数捡数（判据不能放错层）', () => {
    // 如果解析退化成「全文件找同名常量」，这个样本会算出 45_000，
    // 而量到的其实不是这个 handler 的预算——数字看着合理，是最危险的一类假信号。
    const src = [
      'package server',
      'func (s *Server) otherHandler(w http.ResponseWriter, r *http.Request) {',
      '    const summaryBudget = 45 * time.Second',
      '    _ = summaryBudget',
      '}',
      ...sampleWithDecl('').split('\n'),
    ].join('\n')
    const r = budgetInSource(src, 'handleMeetingSummary')
    assert.equal(r.ms, null, `跨函数捡到了预算：${r.reason}`)
    assert.match(r.reason, /不跨函数捡数/)
  })

  it('派生自 context.Background() 的预算不算数（那是后台 goroutine 的）', () => {
    // server_email_invoice.go:109 有一处 context.Background() 的 30s。
    // 把它当成「这个 handler 的预算」会把预算算小，进而逼着前端放宽到
    // 没必要的长度——这类假信号比漏报更难发现。
    const src = fs.readFileSync(path.join(SERVER, 'server_email_invoice.go'), 'utf8')
    const r = budgetInSource(src, 'handleEmailInvoiceExtract')
    assert.equal(
      r.ms,
      null,
      'handleEmailInvoiceExtract 没有派生自 r.Context() 的 WithTimeout（它的耗时全在 IMAP 上），' +
      '判据不该从别处捡一个数来',
    )
  })

  it('handler 名不存在 → 反推返回 null 而不是 0（0 会被误当成「预算很短」）', () => {
    const r = requestBudgetMs('server_meeting.go', 'handleNoSuchHandler')
    assert.equal(r.ms, null)
    assert.match(r.reason, /找不到 handler/)
  })
})

// ══════════════════════════════════════════════════════════════════════
// §929 `/api/llm/chat` 的客户端超时预算
//
// ── 为什么单开一块（2026-10-03 那次普查的漏项）──
//
// 本文件头注释里记着那张 2026-10-03 的普查表：
//
//	/api/meetings/{id}/refine       客户端 30s  服务端 90s
//
// ⇒ 也就是说「客户端比服务端先放弃」这件事**当时就查出来了**，
//   而上面那张 TABLE 修的是**直接调用**那条路（`MEETING_REFINE_TIMEOUT_MS` = 150s）。
//
// ⚠⚠ **但兜底路径从来不在那张表里** ——
// 浏览器侧的兜底打的是 `/api/llm/chat`（`fallbackRefine` / `fallbackSummarize`），
// 而它的客户端超时**没人给** ⇒ 吃 `http()` 的默认 **30 s**。
//
// ⇒ ⇒ 而 `/api/llm/chat` 的**服务端有效预算不是 handler 的 WithTimeout**，
//   而是网关客户端的 `Transport.ResponseHeaderTimeout`：
//   `backend/internal/llmgateway/client.go:116` ⇒ **85 s**
//   （`Timeout: 90s` 是整体上限；响应头若拖到 90s，body 就没有时间可读了）。
//
// ⇒ ⇒ **30 s ≪ 85 s** ⇒ 客户端比服务端先放弃 55 s
//   ⇒ §54 实测：网关 auto 路由到 deepseek-v4-flash 时**首字节就要 24~142 s**
//   ⇒ ⇒ 兜底路径在生产上基本是「必然超时」的（后端 502 之后，前端兜底也超时）。
//
// ⭐ 顺带把**同族一次扫全**（§918/§921 的纪律）：
//   四个调用点（两条会议兜底 + 逐段翻译 + 邮件翻译）**全部**没有 timeoutMs。
// ══════════════════════════════════════════════════════════════════════

/**
 * 剥 TS/Vue 的注释。
 *
 * ⚠ quote-aware：字符串里的 `//`（例如 `'http://x'`）**不是**注释。
 *   本文件里已有的 `stripGoComments` 是行级的、对 Go 够用；TS 侧要另写一个。
 */
export function stripTsComments(src) {
  let out = ''
  let i = 0
  const n = src.length
  while (i < n) {
    const c = src[i]
    const c2 = src[i + 1]
    if (c === '/' && c2 === '/') {
      while (i < n && src[i] !== '\n') i++
      continue
    }
    if (c === '/' && c2 === '*') {
      i += 2
      while (i < n && !(src[i] === '*' && src[i + 1] === '/')) i++
      i += 2
      out += ' '
      continue
    }
    if (c === '"' || c === "'" || c === '`') {
      const quote = c
      out += c
      i++
      while (i < n) {
        if (src[i] === '\\') { out += src[i] + (src[i + 1] ?? ''); i += 2; continue }
        out += src[i]
        if (src[i] === quote) { i++; break }
        i++
      }
      continue
    }
    out += c
    i++
  }
  return out
}

/** 取 `{` 到配对 `}` 的那段文本（跳过字符串里的括号）。 */
function balancedObject(src, openIdx) {
  if (src[openIdx] !== '{') return null
  let depth = 0
  for (let i = openIdx; i < src.length; i++) {
    const c = src[i]
    if (c === '"' || c === "'" || c === '`') {
      const quote = c
      i++
      while (i < src.length) {
        if (src[i] === '\\') { i++; continue }
        if (src[i] === quote) break
        i++
      }
      continue
    }
    if (c === '{') depth++
    else if (c === '}') {
      depth--
      if (depth === 0) return src.slice(openIdx, i + 1)
    }
  }
  return null
}

/**
 * 找出**所有** `/api/llm/chat` 的调用点。
 *
 * ⚠ 匹配的是**调用形态** `http<...>('/api/llm/chat'` 而不是字符串本身
 *   ⇒ 注释里提到这个路径（到处都是）不会被算成调用点。
 * ⛔ 但注释里**也可能**写出调用形态 ⇒ 上面先剥注释。
 */
export function findLlmChatCallers(rootDir) {
  const out = []
  const walk = (dir) => {
    for (const n of fs.readdirSync(dir)) {
      const p = path.join(dir, n)
      const st = fs.statSync(p)
      if (st.isDirectory()) { walk(p); continue }
      if (!/\.(ts|vue|mjs)$/.test(n)) continue
      if (/\.test\.(ts|mjs)$/.test(n)) continue
      const raw = fs.readFileSync(p, 'utf8')
      if (!raw.includes('/api/llm/chat')) continue
      const code = stripTsComments(raw)
      const re = /http(?:<[^>]*>)?\(\s*'\/api\/llm\/chat'/g
      let m
      while ((m = re.exec(code)) !== null) {
        const obj = httpOptionsAfter(code, m.index + m[0].length)
        let timeoutMs = null
        let hasTimeout = false
        if (obj) {
          const tm = /timeoutMs:\s*([^,\n}]+)/.exec(obj)
          hasTimeout = !!tm
          if (tm) timeoutMs = tm[1].trim()
        }
        const line = code.slice(0, m.index).split('\n').length
        out.push({ file: path.relative(rootDir, p), line, hasTimeout, timeoutMs })
      }
    }
  }
  walk(rootDir)
  return out
}

/** 服务端 `/api/llm/chat` 的有效预算：网关客户端的 `ResponseHeaderTimeout`（毫秒）。 */
export function llmChatServerBudgetMs(goSrc) {
  const src = goSrc ?? fs.readFileSync(
    path.resolve(SRC, '..', '..', 'backend', 'internal', 'llmgateway', 'client.go'), 'utf8')
  const code = stripGoComments(src)
  const m = /ResponseHeaderTimeout:\s*([0-9]+)\s*\*\s*time\.Second/.exec(code)
  if (!m) return null
  return Number(m[1]) * 1000
}

/** 把调用点里的 `timeoutMs` 取值解析成毫秒（字面量或 http.ts 里的常量）。 */
export function resolveTimeoutMs(caller) {
  if (!caller.hasTimeout) return null
  const v = caller.timeoutMs
  if (/^[0-9_]+$/.test(v)) return Number(v.replace(/_/g, ''))
  const c = constMs('api/http.ts', v)
  return c
}


/**
 * ★ §930：取一次 http 调用**紧跟其后**的选项对象。
 *
 * ⚠⚠ 为什么不能写 `code.indexOf('{', afterRoute)`：
 *   `http('/api/emails/invoices/summary')` 这种**没有选项对象**的调用，
 *   indexOf 会一路往后找，捡到**后面另一条语句**的 `{` ⇒
 *   于是从那个不相关的对象里读出 `timeoutMs` ⇒ **假阳性**。
 *   （本节实测踩到过：枚举结果 18 条 vs 实际 17 条，多出来的那条是假的。）
 *   ⇒ ⇒ 必须先看**紧跟其后**（跳过空白与逗号）是不是 `{`。
 *
 * ⚠ 顺带：§929 那个 `findLlmChatCallers` 用的是同一个错误写法 ⇒
 *   当时没咬人（四条调用点都带选项对象）⇒ **但它是同一颗雷** ⇒ 一并修。
 */
export function httpOptionsAfter(code, endIdx) {
  let i = endIdx
  while (i < code.length && /[\s,]/.test(code[i])) i++
  if (code[i] !== '{') return null
  const open = i
  let depth = 0
  for (; i < code.length; i++) {
    const c = code[i]
    if (c === '"' || c === "'" || c === '`') {
      const q = c
      i++
      while (i < code.length) {
        if (code[i] === '\\') { i++; continue }
        if (code[i] === q) break
        i++
      }
      continue
    }
    if (c === '{') depth++
    else if (c === '}') {
      depth--
      if (depth === 0) return code.slice(open, i + 1)
    }
  }
  return null
}

describe('§929 `/api/llm/chat` 的客户端超时必须超过服务端有效预算', () => {
  it('★ 服务端有效预算能从源码反推出来（不是写死的数）', () => {
    const b = llmChatServerBudgetMs()
    assert.notEqual(b, null, '读不到 llmgateway 的 ResponseHeaderTimeout —— 判据失明（不许当成通过）')
    assert.equal(b, 85_000, `网关的 ResponseHeaderTimeout 变了：${b}ms ⇒ 客户端超时也得跟着改`)
  })

  it('★ 每个 `/api/llm/chat` 调用点都必须显式带 timeoutMs（默认 30 s ≪ 服务端 85 s）', () => {
    const callers = findLlmChatCallers(SRC)
    assert.ok(callers.length >= 4, `只扫到 ${callers.length} 个调用点 ⇒ 扫描口径坏了（应 ≥ 4）`)
    const missing = callers.filter((c) => !c.hasTimeout)
    assert.deepEqual(
      missing.map((c) => `${c.file}:${c.line}`),
      [],
      '这些调用点没给 timeoutMs ⇒ 吃 http() 的默认 30 s ⇒ 比服务端 85 s 早放弃 55 s',
    )
  })

  it('★ 每个调用点的 timeoutMs 都必须严格大于服务端有效预算', () => {
    const server = llmChatServerBudgetMs()
    assert.notEqual(server, null, '判据失明')
    const callers = findLlmChatCallers(SRC)
    const bad = []
    for (const c of callers) {
      const ms = resolveTimeoutMs(c)
      if (ms === null) { bad.push(`${c.file}:${c.line} ⇒ timeoutMs 解析不了（${c.timeoutMs}）`); continue }
      if (!(ms > server)) bad.push(`${c.file}:${c.line} ⇒ ${ms}ms ≤ 服务端 ${server}ms`)
    }
    assert.deepEqual(bad, [], `客户端超时必须严格大于服务端有效预算：\n${bad.join('\n')}`)
  })

  it('★ 调用点集合的条数被钉住（新增调用点要么带上 timeoutMs，要么改这个数）', () => {
    const callers = findLlmChatCallers(SRC)
    assert.equal(callers.length, 4,
      `现在有 ${callers.length} 个 \`/api/llm/chat\` 调用点（期望 4）：\n` +
      callers.map((c) => `  ${c.file}:${c.line}`).join('\n') +
      '\n⇒ 新增调用点必须显式带 timeoutMs，并把这里的条数一起改。')
  })
})

describe('§929 判据自检：负控必须转红', () => {
  it('把网关的 ResponseHeaderTimeout 调大 → 客户端 120 s 就不再够（证明判据在读源码）', () => {
    const real = llmChatServerBudgetMs()
    const src = fs.readFileSync(
      path.resolve(SRC, '..', '..', 'backend', 'internal', 'llmgateway', 'client.go'), 'utf8')
    const patched = src.replace(
      /ResponseHeaderTimeout:\s*85\s*\*\s*time\.Second/,
      'ResponseHeaderTimeout: 150 * time.Second',
    )
    assert.notEqual(patched, src, '负控样本没有真的改到预算（替换没命中）')
    const bigger = llmChatServerBudgetMs(patched)
    assert.equal(bigger, 150_000, '反推没跟着变 —— 判据是写死的，假护栏')
    const client = constMs('api/http.ts', 'LLM_CHAT_TIMEOUT_MS')
    assert.ok(client <= bigger,
      '网关预算涨到 150s 之后客户端仍被判为足够 —— 余量逻辑坏了')
  })

  it('把某个调用点的 timeoutMs 去掉 → 「必须显式带」那条转红', () => {
    const callers = findLlmChatCallers(SRC)
    const withTimeout = callers.filter((c) => c.hasTimeout)
    assert.ok(withTimeout.length > 0, '负控样本无效：所有调用点本来就没有 timeoutMs')
    const stripped = withTimeout.map((c) => ({ ...c, hasTimeout: false }))
    const missing = stripped.filter((c) => !c.hasTimeout)
    assert.ok(missing.length > 0, '负控失败：去掉 timeoutMs 后没有转红 ⇒ 判据形同虚设')
  })
})

// ══════════════════════════════════════════════════════════════════════
// §930 「长路由三段预算」的全路由普查
//
// ── 为什么有这一节 ────────────────────────────────────────────────────
//
// §929 修的是**一条**路由（/api/llm/chat）⇒ 它的服务端有效预算在网关客户端里
// ⇒ 客户端 30 s ≪ 服务端 85 s ⇒ 「服务端还在算、客户端先断开」。
//
// ⚠ 但那是**个案**。这条不变式真正的口径是三段预算：
//
//	① 客户端 timeoutMs
//	② 服务端 handler 自己的预算（r.Context() 派生的 WithTimeout）
//	③ 服务端 http.Server.WriteTimeout（30s，除非该路由在 long-lived 白名单里）
//
// ⇒ 必须是 **① > ②**，且 **② > 30s ⇒ ③ 必须被豁免**。
//
// ⚠⚠ 三段里**每一段**都曾出过问题，而它们的共同形态是
//   **「手工维护的清单」**（这张表、longLivedPaths、mustBeLongLived）——
//   §929 已经证明手工清单会漏（兜底路径从来没进过这张表）。
//
// ── 本节的三件事 ──────────────────────────────────────────────────────
//
// ① **客户端侧**（本文件）：把「谁在用长超时」从**手工**改成**机械枚举** ——
//    枚举 src/api/ 下每一个带 timeoutMs 的 http 调用点，
//    要求**每一个都在册** ⇒ 新增调用点忘了登记 ⇒ 当场红。
// ② **服务端侧**（Go 侧那道 AST 审计门禁）：它的解析器只认**内联字面量**
//    ⇒ 预算写成**命名常量**的 handler 对它**隐形**
//    ⇒ `opsExecTimeout`(90s) / `fullTranscribeTimeout`(10min) /
//    `meetingAnalyzeTimeout`(90s) 三个包级常量全部不被计入
//    ⇒ ⇒ 门禁一直绿着，而 `/api/emails/ops/sync` 真的没被豁免。
//    ⇒ 解析器已修 ⇒ 门禁随即变红 ⇒ 真实缺陷被暴露并修掉。
// ③ **判据的量具本身**：仓里有 **5 份**手写的 Go 剥注释器拷贝，
//    全都认不出**字符串字面量里的斜杠** ⇒ 会吞掉真代码
//    （server.go 里正好有那种字面量）⇒ 已统一到共享的那份（quote-aware）。
// ══════════════════════════════════════════════════════════════════════

/**
 * 枚举 `src/api/` 下**所有**带 timeoutMs 的 http 调用点。
 *
 * ⚠ 匹配的是**调用形态** `http<…>('/api/…'` 而不是字符串本身
 *   ⇒ 注释里提到这个路径（到处都是）不会被算成调用点。
 * ⚠ 扫之前**先剥注释**（复用 §929 那个 quote-aware 的 stripTsComments）。
 */
export function enumerateTimeoutCallSites(dir = path.join(SRC, 'api')) {
  const out = []
  const walk = (d) => {
    for (const n of fs.readdirSync(d)) {
      const p = path.join(d, n)
      const st = fs.statSync(p)
      if (st.isDirectory()) { walk(p); continue }
      if (!/\.ts$/.test(n) || /\.test\.ts$/.test(n)) continue
      const code = stripTsComments(fs.readFileSync(p, 'utf8'))
      const re = /http(?:<[^>]*>)?\(\s*['`]([^'`]+)['`]/g
      let m
      while ((m = re.exec(code)) !== null) {
        const obj = httpOptionsAfter(code, m.index + m[0].length)
        if (!obj) continue
        const tm = /timeoutMs:\s*([A-Za-z_]\w*)/.exec(obj)
        if (!tm) continue
        out.push({
          // ★ 正斜杠化（2026-10-22 Windows 实测修掉）：
          //   path.relative 在 Windows 上给反斜杠，而 LONG_ROUTE_CALL_SITES 存的是
          //   正斜杠相对路径（'api/notes.ts'）⇒ 逐条对不上 ⇒ 这道门恒红。
          //   ⇒ 与 meeting-dedup.test.ts 同款：归一在产出侧。
          file: path.relative(SRC, p).replace(/\\/g, '/'),
          route: m[1],
          constName: tm[1],
        })
      }
    }
  }
  walk(dir)
  return out
}

/**
 * 长路由调用点**全量在册清单**（§930）。
 *
 * ⚠ 这张清单是**机械枚举的对照物**：枚举结果必须与它**逐条相等**
 *   ⇒ 新增一个带 timeoutMs 的调用点而忘了登记 ⇒ 当场红。
 * ⚠ 键是 (file, route, constName)：`/api/llm/chat` 在 api/meetings.ts 里有
 *   **两个**调用点（摘要兜底 / 精校兜底）⇒ 键相同 ⇒ 这里**登记两条**
 *   ⇒ 比较用**多重集**（两边都带重复）⇒ 少登记一条立刻对不上。
 * ⚠ `constFile` = 常量**定义**在哪个文件（调用点文件与常量文件常常不是同一个：
 *   `LONG_REQUEST_TIMEOUT_MS` 定义在 api/http.ts，却有 6 个调用点在别的文件里）。
 *   省略时默认与 `file` 相同。
 */
const LONG_ROUTE_CALL_SITES = [
  { file: 'api/email.ts', route: '/api/email/backfill', constName: 'BACKFILL_TIMEOUT_MS' },
  { file: 'api/email.ts', route: '/api/emails/${encodeURIComponent(id)}/summarize', constName: 'LONG_REQUEST_TIMEOUT_MS', constFile: 'api/http.ts' },
  { file: 'api/email.ts', route: '/api/emails/sync', constName: 'LONG_REQUEST_TIMEOUT_MS', constFile: 'api/http.ts' },
  { file: 'api/email.ts', route: '/api/emails/classify', constName: 'CLASSIFY_TIMEOUT_MS' },
  { file: 'api/email.ts', route: '/api/emails/move', constName: 'LONG_REQUEST_TIMEOUT_MS', constFile: 'api/http.ts' },
  { file: 'api/email.ts', route: '/api/emails/ops/sync', constName: 'LONG_REQUEST_TIMEOUT_MS', constFile: 'api/http.ts' },
  { file: 'api/email.ts', route: '/api/emails/organize', constName: 'LONG_REQUEST_TIMEOUT_MS', constFile: 'api/http.ts' },
  { file: 'api/email.ts', route: '/api/emails/invoices/extract', constName: 'INVOICE_EXTRACT_TIMEOUT_MS' },
  { file: 'api/email.ts', route: '/api/email/pipeline/run', constName: 'PIPELINE_TIMEOUT_MS' },
  { file: 'api/meetings.ts', route: '/api/llm/chat', constName: 'LLM_CHAT_TIMEOUT_MS', constFile: 'api/http.ts' },
  { file: 'api/meetings.ts', route: '/api/llm/chat', constName: 'LLM_CHAT_TIMEOUT_MS', constFile: 'api/http.ts' },
  { file: 'api/notes.ts', route: '/api/notes/${id}/summarize', constName: 'NOTE_SUMMARIZE_TIMEOUT_MS' },
  { file: 'api/stt-settings.ts', route: '/api/stt/discover', constName: 'STT_DISCOVER_TIMEOUT_MS' },
  { file: 'api/stt-settings.ts', route: '/api/stt/transcribe-full', constName: 'FULL_TRANSCRIBE_TIMEOUT_MS' },
  { file: 'api/stt-settings.ts', route: '/api/stt/transcribe-incremental', constName: 'LONG_REQUEST_TIMEOUT_MS', constFile: 'api/http.ts' },
  { file: 'api/stt-settings.ts', route: '/api/stt/probe', constName: 'STT_PROBE_TIMEOUT_MS' },
  { file: 'api/stt.ts', route: '/api/stt/transcribe', constName: 'STT_TRANSCRIBE_TIMEOUT_MS' },
]

const siteKey = (x) => `${x.file}::${x.route}::${x.constName}`

describe('§930 长路由调用点全量在册（机械枚举 ⇒ 手工清单漏不掉）', () => {
  it('★ 枚举结果与在册清单逐条相等（新增调用点忘了登记 ⇒ 当场红）', () => {
    const found = enumerateTimeoutCallSites().map(siteKey).sort()
    const registered = LONG_ROUTE_CALL_SITES.map(siteKey).sort()
    assert.deepEqual(
      found, registered,
      '长路由调用点清单与在册清单对不上。\n'
      + `枚举到 ${found.length} 条：\n  ${found.join('\n  ')}\n`
      + `在册 ${registered.length} 条：\n  ${registered.join('\n  ')}\n`
      + '⇒ 新增一个带 timeoutMs 的调用点时，请把它登记进 LONG_ROUTE_CALL_SITES。',
    )
  })

  it('★ 在册清单的条数被钉住（防止「两边同时少一条」蒙混过关）', () => {
    assert.equal(
      enumerateTimeoutCallSites().length, 17,
      '枚举到的长路由调用点不是 17 条 ⇒ 扫描口径变了（或者有人增删了调用点）',
    )
    assert.equal(LONG_ROUTE_CALL_SITES.length, 17, '在册清单的条数变了')
  })

  it('★ 每一条的客户端超时都**严格大于**默认的 30 s（不许退回默认值）', () => {
    for (const s of LONG_ROUTE_CALL_SITES) {
      const ms = constMs(s.constFile ?? s.file, s.constName)
      assert.notEqual(ms, null, `${s.constFile ?? s.file} 里找不到 ${s.constName}`)
      assert.ok(
        ms > 30_000,
        `${s.file}::${s.route} 的 ${s.constName}=${ms}ms 不大于默认的 30 s ⇒ `
        + '它就是个普通请求，登记它没有意义',
      )
    }
  })
})

describe('§930 随手记的整段兜底转写：客户端预算必须大于服务端的 10 分钟', () => {
  it('★ recordingRuntime 用的是共享常量（不是自己写死的 10 分钟）', () => {
    const raw = fs.readFileSync(path.join(SRC, 'native', 'recordingRuntime.ts'), 'utf8')
    // ⚠ 先剥注释：预算参数前面可能被一段说明隔开（§930 那段注释就在中间）⇒
    //   不剥的话正则会停在注释上 ⇒ 读不到预算。
    const src = stripTsComments(raw)
    const at = src.indexOf('sttSettingsApi.transcribeFull(')
    assert.notEqual(at, -1, '找不到随手记的整段兜底转写')
    // ⚠ 按**圆括号配平**取那次调用的参数，而不是正则乱吞 ——
    //   `transcribeFull(` 里面还有嵌套调用（filenameForMimeType(blobType)），
    //   贪心/惰性正则都会取错位置（§930 实测踩过）。
    const open = src.indexOf('(', at)
    let depth = 0
    let close = -1
    for (let i = open; i < src.length; i++) {
      if (src[i] === '(') depth++
      else if (src[i] === ')') {
        depth--
        if (depth === 0) { close = i; break }
      }
    }
    assert.notEqual(close, -1, '配平失败：找不到 transcribeFull(...) 的右括号')
    // withTimeout 的第二个实参就在这次调用之后 ⇒ 取到下一个逗号为止。
    const after = src.slice(close + 1, close + 200)
    const budget = /^\s*,\s*([A-Za-z_]\w*|\d[\d_ ]*)/.exec(after)
    assert.notEqual(budget, null, `没解析出随手记兜底转写的客户端预算：\n${after.slice(0, 200)}`)
    assert.equal(
      budget[1], 'FULL_TRANSCRIBE_TIMEOUT_MS',
      '随手记兜底转写没有用共享常量 FULL_TRANSCRIBE_TIMEOUT_MS ⇒ '
      + '它自己写死了一个数 ⇒ 迟早与服务端的 10 分钟「相等即错」',
    )
  })

  it('★ 那个共享常量确实大于服务端的 fullTranscribeTimeout（10 分钟）', () => {
    const client = constMs('api/stt-settings.ts', 'FULL_TRANSCRIBE_TIMEOUT_MS')
    assert.notEqual(client, null, '读不到 FULL_TRANSCRIBE_TIMEOUT_MS')
    const server = requestBudgetMs('server_stt_stream.go', 'handleSttTranscribeFull')
    assert.notEqual(server.ms, null, `反推服务端预算失败：${server.reason}`)
    assert.ok(
      client > server.ms,
      `客户端 ${client}ms 必须大于服务端 ${server.ms}ms ⇒ 否则「相等即错」`,
    )
  })
})

describe('§930 判据自检：负控必须转红', () => {
  it('★ 新增一个没在册的调用点 → 在册性判据转红（枚举完整性）', () => {
    const found = enumerateTimeoutCallSites().map(siteKey)
    found.push('api/email.ts::/api/email/brand-new::LONG_REQUEST_TIMEOUT_MS')
    assert.notDeepEqual(
      found.sort(),
      LONG_ROUTE_CALL_SITES.map(siteKey).sort(),
      '负控本该转红却判成了通过 —— 新增长路由可以不登记就混过去',
    )
  })

  it('★ 去掉一个在册条目 → 在册性判据转红（清单不许偷偷变短）', () => {
    const shorter = LONG_ROUTE_CALL_SITES.slice(1)
    assert.notDeepEqual(
      enumerateTimeoutCallSites().map(siteKey).sort(),
      shorter.map(siteKey).sort(),
      '负控本该转红却判成了通过 —— 删掉一条登记就没人管了',
    )
  })

  it('★ 剥注释器不许吞掉「字符串字面量里的斜杠」（§930 亲手踩过两次）', () => {
    const goSrc = [
      'package server',
      '',
      'var longLivedPatterns = []string{',
      '\t"/api/meetings/*/transcribe",',
      '\t"/api/meetings/*/summary",',
      '}',
      '',
      '// 一条注释里写着 /api/learning/* 这种带通配符的路径',
      'const realBudget = 90 * time.Second',
    ].join('\n')
    const code = stripGoComments(goSrc)
    assert.match(code, /transcribe/, '剥注释把 long-lived 名单的一行吃掉了')
    assert.match(code, /summary/, '剥注释把 long-lived 名单的另一行吃掉了')
    assert.match(code, /realBudget\s*=\s*90/, '剥注释把真的代码吃掉了')
    assert.doesNotMatch(code, /learning/, '注释里的路径不该留在「代码」里')
  })
})
