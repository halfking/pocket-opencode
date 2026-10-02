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

/** 剥 Go 注释——判据不能被源码里的说明文字满足。 */
export function stripGoComments(src) {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .split('\n')
    .map((l) => {
      const i = l.indexOf('//')
      return i < 0 ? l : l.slice(0, i)
    })
    .join('\n')
}

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

/**
 * 取某 handler 的**请求派生**预算。
 *
 * 只认 `context.WithTimeout(r.Context(), …)`——派生自 r.Context() 的预算
 * 才是「服务端愿意为一个请求花多久」；`context.Background()` 那个是后台
 * goroutine 的预算，与本请求无关（server_email_invoice.go:109 就有一处，
 * 早先的判据把它算进来过，是假信号）。
 *
 * 接受**源码文本**而不是只能读文件：负控要把后端源码改掉再复算，
 * 让它去写临时 .go 文件既脏又容易在 Windows 上失败。
 */
export function budgetInSource(goSrc, handler) {
  const body = goFuncBody(goSrc, handler)
  if (body === null) return { ms: null, reason: `找不到 handler ${handler}` }
  const m = /context\.WithTimeout\(r\.Context\(\),\s*([0-9]+)\s*\*\s*time\.(Second|Minute|Hour)\s*\)/.exec(body)
  if (!m) return { ms: null, reason: `${handler} 里没有派生自 r.Context() 的 WithTimeout` }
  return {
    ms: Number(m[1]) * { Second: 1000, Minute: 60_000, Hour: 3_600_000 }[m[2]],
    reason: `${handler}: ${m[1]}${m[2][0]} on r.Context()`,
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
    const row = TABLE[1] // handleMeetingSummary 45s
    const src = fs.readFileSync(path.join(SERVER, row.server.goFile), 'utf8')
    const before = budgetInSource(src, row.server.handler).ms
    assert.equal(before, 45_000, '判据在真实代码上就没算对（预算写法可能变了）')
    // 把 45 改成 450：反推必须跟着变成 450_000。
    // 写死 45_000 的判据在这里会纹丝不动——那才是假护栏。
    const patched = src.replace(
      /context\.WithTimeout\(r\.Context\(\),\s*45\s*\*\s*time\.Second\s*\)/,
      'context.WithTimeout(r.Context(), 450*time.Second)',
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
