// email-long-request-budget.test.mjs
//
// 锁住一条跨语言的不变式：**前端给长请求设的客户端超时，必须大于后端自己
// 给该请求设的执行预算。** 反了就是「服务端成功了、客户端报失败」。
//
// 2026-10-02 审计实测到的正是这一条：`emailApi.runPipeline()` 没有传
// timeoutMs，于是吃 http.ts 的默认 30s；而后端这一轮实测 1m30.67s
// （backend/internal/server/server.go 的 longLivedPaths 事故记录里写着）。
// 后果每次必现、且极具误导性：
//
//   - 30s 前端 abort → 界面报「操作失败」；
//   - 后端毫不知情，继续跑到 1m30s，把发票行建好、推完飞书；
//   - 用户以为没成 → 再点一次 → 第二个作业排队等 emailPipelineMu。
//
// 这与 2026-10-01 那次「后端 200 / 客户端空响应」是**同一个陷阱的两个
// 方向**：那次的病根在服务端 WriteTimeout（30s），这次在客户端 timeoutMs。
// 同一个 30s 数字、两端各掐一次，所以只修一端不够。
//
// 判定为什么要用 AST：`runPipeline` 的接线如果用「源码里出现过 PIPELINE_TIMEOUT_MS」
// 这种文本匹配，我自己在函数上方写的注释就能满足它——注释里就写着
// 「必须给足：后端实测 1m30s」。判据必须只认字符串/节点，天然免疫注释。
//
// 负控见文件末尾。

import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const ts = require('typescript')

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..', '..')          // frontend/src
const REPO = path.resolve(SRC, '..', '..')                 // 仓库根
const FRONTEND_API = path.join(SRC, 'api', 'email.ts')
const BACKEND_PIPELINE = path.join(REPO, 'backend', 'internal', 'server', 'server_email_pipeline.go')
const BACKEND_CLASSIFY = path.join(REPO, 'backend', 'internal', 'server', 'server_email_classify.go')
const BACKEND_CLASSIFY_GW = path.join(REPO, 'backend', 'internal', 'server', 'server_email_classify_gateway.go')

// ─────────────────────────────────────────────────────────────────────
// 纯函数：把「客户端超时 vs 服务端预算」抽出来，才能喂合成样本做负控。
// ─────────────────────────────────────────────────────────────────────

/**
 * 求 `export const NAME = <纯算术表达式>` 的值（毫秒）。
 * 只接受数字、_ 分隔符与 + - * / 括号，够用且不会执行任意代码。
 */
export function evalConstMs(src, name) {
  const sf = ts.createSourceFile('x.ts', src, ts.ScriptTarget.Latest, true)
  for (const st of sf.statements) {
    if (!ts.isVariableStatement(st)) continue
    for (const d of st.declarationList.declarations) {
      if (!ts.isIdentifier(d.name) || d.name.text !== name) continue
      const init = d.initializer
      if (!init) return null
      return evalArith(init)
    }
  }
  return null
}

function evalArith(node) {
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
 * 从 Go 源码里取 runEmailPipeline 的执行预算（毫秒）。
 *
 * 只认 `context.WithTimeout(ctx, N*time.Unit)` 这种**字面量**形式，且只在
 * runEmailPipeline / delegatePipeline 两个函数体里找——server_email_pipeline.go
 * 里还有别的 handler，混进来会让上限虚高到没人守得住。
 * 注释先剥掉，免得注释里的 `15*time.Minute` 被当成真预算。
 */
export function pipelineServerBudgetMs(goSrc) {
  const code = stripGoComments(goSrc)
  const wanted = ['runEmailPipeline', 'delegatePipeline']
  let max = null
  for (const fn of wanted) {
    const body = goFuncBody(code, fn)
    if (body === null) continue
    const re = /context\.WithTimeout\(\s*ctx\s*,\s*([0-9]+)\s*\*\s*time\.(Minute|Second|Hour)\s*\)/g
    let m
    while ((m = re.exec(body)) !== null) {
      const unit = { Second: 1000, Minute: 60_000, Hour: 3_600_000 }[m[2]]
      const ms = Number(m[1]) * unit
      if (max === null || ms > max) max = ms
    }
  }
  return max
}

function stripGoComments(src) {
  // Go 只有 // 行注释与 /* */ 块注释；不含字符串字面量的复杂场景，
  // 这里的输入是本仓库自有源码，逐行剥离足够且可预测。
  return src
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .split('\n')
    .map((l) => {
      const i = l.indexOf('//')
      return i < 0 ? l : l.slice(0, i)
    })
    .join('\n')
}

function goFuncBody(src, fnName) {
  const start = src.indexOf(`func (s *Server) ${fnName}(`)
  if (start < 0) return null
  const open = src.indexOf('{', start)
  if (open < 0) return null
  let depth = 0
  for (let i = open; i < src.length; i++) {
    if (src[i] === '{') depth++
    else if (src[i] === '}') {
      depth--
      if (depth === 0) return src.slice(open, i)
    }
  }
  return null
}

/**
 * 在 emailApi 对象字面量里找 `runPipeline(...)`，看它的函数体里
 * 是否**以属性形式**接了 signal 与 timeoutMs。
 *
 * 走 AST 而不是字符串包含，是为了让注释满足不了它。
 * @returns {{signal:boolean, timeoutMs:boolean, timeoutExpr:string|null}}
 */
export function pipelineWiring(src) {
  const sf = ts.createSourceFile('email.ts', src, ts.ScriptTarget.Latest, true)
  let found = null

  const visit = (node) => {
    if (found) return
    if (ts.isMethodDeclaration(node) && node.name && node.name.getText(sf) === 'runPipeline') {
      found = node
      return
    }
    ts.forEachChild(node, visit)
  }
  visit(sf)
  if (!found || !found.body) return { signal: false, timeoutMs: false, timeoutExpr: null }

  let signal = false
  let timeoutMs = false
  let timeoutExpr = null

  // runPipeline 的函数体是 `return http(path, { ...opts })`，所以要看
  // **传给 http 的那个对象字面量**的属性，而不是函数体里任意位置。
  const obj = findHttpOptionsObject(found.body)
  if (!obj) return { signal: false, timeoutMs: false, timeoutExpr: null }

  for (const p of obj.properties) {
    // 两种形态都要认：
    //   timeoutMs: PIPELINE_TIMEOUT_MS   → PropertyAssignment
    //   signal,                           → ShorthandPropertyAssignment（**简写**）
    // 早先只判 PropertyAssignment，于是 `signal,` 这种最常见的写法被判成
    // 「没接」，护栏自己报了假红。判据漏形态比误报更糟：它会让人把真缺陷
    // 当成噪声。
    let key = null
    if (ts.isPropertyAssignment(p)) key = p.name.getText(sf).replace(/['"]/g, '')
    else if (ts.isShorthandPropertyAssignment(p)) key = p.name.getText(sf)
    if (key === null) continue
    if (key === 'signal') signal = true
    if (key === 'timeoutMs' && ts.isPropertyAssignment(p)) {
      timeoutMs = true
      timeoutExpr = p.initializer.getText(sf)
    }
  }
  return { signal, timeoutMs, timeoutExpr }
}

function findHttpOptionsObject(body) {
  let found = null
  const visit = (node) => {
    if (found) return
    if (ts.isCallExpression(node) && node.expression.getText() === 'http') {
      const second = node.arguments[1]
      if (second && ts.isObjectLiteralExpression(second)) found = second
      return
    }
    ts.forEachChild(node, visit)
  }
  visit(body)
  return found
}

// ─────────────────────────────────────────────────────────────────────
// 第二条不变式：批量端点的预算 = 单次预算 × 批量条数
// ─────────────────────────────────────────────────────────────────────

/** 从 Go 源码里取形如 `context.WithTimeout(<x>, N*time.Unit)` 的毫秒数。 */
function withTimeoutMsIn(src, fnName) {
  const body = goFuncBody(stripGoComments(src), fnName)
  if (body === null) return null
  const m = /context\.WithTimeout\(\s*\w+\s*,\s*([0-9]+)\s*\*\s*time\.(Second|Minute|Hour)\s*\)/.exec(body)
  if (!m) return null
  return Number(m[1]) * { Second: 1000, Minute: 60_000, Hour: 3_600_000 }[m[2]]
}

/**
 * 归类的服务端最坏预算。
 *
 * 与 pipeline 不同，handleEmailClassify **没有整体超时**——它的预算是
 * 一个循环：每封先试 kxmemory（20s），失败再回落 LLM 网关（25s）。
 * 所以是 (kxmemory + 网关) × 默认 limit，不是某个字面量。
 */
export function classifyServerBudgetMs(classifyGo, gatewayGo) {
  const perKx = withTimeoutMsIn(classifyGo, 'classifyViaKxmemory')
  const perGw = withTimeoutMsIn(gatewayGo, 'classifyViaGateway')
  if (perKx === null || perGw === null) return null
  // 默认 limit 在 handleEmailClassify 里：`if limit <= 0 { limit = 20 }`
  const handler = goFuncBody(stripGoComments(classifyGo), 'handleEmailClassify')
  const m = handler && /limit\s*=\s*([0-9]+)/.exec(handler)
  if (!m) return null
  return (perKx + perGw) * Number(m[1])
}

/** 与 pipelineWiring 同形，只是找 classifyInbox。 */
export function classifyWiring(src) {
  const sf = ts.createSourceFile('email.ts', src, ts.ScriptTarget.Latest, true)
  let found = null
  const visit = (node) => {
    if (found) return
    if (ts.isMethodDeclaration(node) && node.name && node.name.getText(sf) === 'classifyInbox') {
      found = node
      return
    }
    ts.forEachChild(node, visit)
  }
  visit(sf)
  if (!found || !found.body) return { signal: false, timeoutMs: false, timeoutExpr: null }

  let obj = null
  const scan = (node) => {
    if (obj) return
    if (ts.isCallExpression(node) && node.expression.getText() === 'http') {
      const second = node.arguments[1]
      if (second && ts.isObjectLiteralExpression(second)) obj = second
      return
    }
    ts.forEachChild(node, scan)
  }
  scan(found.body)
  if (!obj) return { signal: false, timeoutMs: false, timeoutExpr: null }

  let signal = false
  let timeoutMs = false
  let timeoutExpr = null
  for (const p of obj.properties) {
    let key = null
    if (ts.isPropertyAssignment(p)) key = p.name.getText(sf).replace(/['"]/g, '')
    else if (ts.isShorthandPropertyAssignment(p)) key = p.name.getText(sf)
    if (key === 'signal') signal = true
    if (key === 'timeoutMs' && ts.isPropertyAssignment(p)) {
      timeoutMs = true
      timeoutExpr = p.initializer.getText(sf)
    }
  }
  return { signal, timeoutMs, timeoutExpr }
}

const classifyGo = fs.readFileSync(BACKEND_CLASSIFY, 'utf8')
const classifyGwGo = fs.readFileSync(BACKEND_CLASSIFY_GW, 'utf8')

describe('归类 客户端超时 vs 服务端最坏预算', () => {
  it('能从后端源码反推出「单封 × 条数」的最坏预算（反推不能空跑）', () => {
    const budget = classifyServerBudgetMs(classifyGo, classifyGwGo)
    assert.notEqual(budget, null, '没从 classify 源码反推出预算——判据失效，不是代码有问题')
    // 20s(kxmemory) + 25s(网关) × 20 封 = 900_000
    assert.equal(budget, 900_000, `反推出 ${budget}ms，与「(20+25) 秒 × 20 封」对不上`)
  })

  it('客户端超时严格大于服务端最坏预算', () => {
    const server = classifyServerBudgetMs(classifyGo, classifyGwGo)
    const client = evalConstMs(apiSrc, 'CLASSIFY_TIMEOUT_MS')
    assert.notEqual(client, null, 'api/email.ts 里找不到 CLASSIFY_TIMEOUT_MS')
    assert.ok(
      client > server,
      `CLASSIFY_TIMEOUT_MS=${client}ms 必须大于服务端最坏预算 ${server}ms；` +
      `否则前端会比服务端先放弃——而服务端 ctx 派生自 r.Context()，断连会把它` +
      `当场杀掉，一批邮件只能处理掉前两三封，用户却什么都看不出来`,
    )
  })

  it('classifyInbox 真的接上了那个常量（不是又退回通用 120s）', () => {
    const w = classifyWiring(apiSrc)
    assert.equal(w.timeoutMs, true, 'classifyInbox 没有传 timeoutMs')
    assert.equal(
      w.timeoutExpr,
      'CLASSIFY_TIMEOUT_MS',
      'classifyInbox 又用回了通用的 LONG_REQUEST_TIMEOUT_MS（120s < 900s）',
    )
    assert.equal(w.signal, true, 'classifyInbox 没有把 signal 传给 http()')
  })
})

describe('判据自检（归类部分）：负控必须转红', () => {
  it('客户端超时改回 120s → 预算判据转红', () => {
    const broken = apiSrc.replace(
      /export const CLASSIFY_TIMEOUT_MS = [^\r\n]*/,
      'export const CLASSIFY_TIMEOUT_MS = 120_000',
    )
    assert.notEqual(broken, apiSrc, '负控样本没有真的改到 CLASSIFY_TIMEOUT_MS（替换没命中）')
    assert.ok(
      evalConstMs(broken, 'CLASSIFY_TIMEOUT_MS') <= classifyServerBudgetMs(classifyGo, classifyGwGo),
      '负控本该转红却判成了通过——判据坏了',
    )
  })

  it('服务端把默认 limit 从 20 调到 50 → 预算判据跟着变大', () => {
    // 反向证明判据真的在读源码：改后端条数，预算必须从 900s 变成 2250s。
    // 写死 900_000 的判据在这里会纹丝不动——那才是假护栏。
    const broken = classifyGo.replace('limit = 20', 'limit = 50')
    assert.notEqual(broken, classifyGo, '负控样本没有真的改到 limit（替换没命中）')
    assert.equal(classifyServerBudgetMs(broken, classifyGwGo), 2_250_000)
  })

  it('注释里的 limit=20 / WithTimeout 不计入预算', () => {
    const inflated = classifyGo.replace(
      'func (s *Server) handleEmailClassify(',
      '// limit = 99\r\n// context.WithTimeout(ctx, 99*time.Hour)\r\nfunc (s *Server) handleEmailClassify(',
    )
    assert.notEqual(inflated, classifyGo, '负控样本没有插进注释（替换没命中）')
    assert.equal(
      classifyServerBudgetMs(inflated, classifyGwGo),
      classifyServerBudgetMs(classifyGo, classifyGwGo),
      '注释里的数字被算进去了——反推判据必须剥注释',
    )
  })

  it('classifyInbox 的 signal 被摘掉 → 接线判据转红', () => {
    const broken = mutateAfter(apiSrc, "'/api/emails/classify'",
      /\r?\n\s*signal,(?=\r?\n)/)
    assert.notEqual(broken, apiSrc, '负控样本没有真的删掉 signal（替换没命中）')
    assert.equal(classifyWiring(broken).signal, false)
  })
})

// ─────────────────────────────────────────────────────────────────────
// 真源断言
// ─────────────────────────────────────────────────────────────────────

const apiSrc = fs.readFileSync(FRONTEND_API, 'utf8')
const goSrc = fs.readFileSync(BACKEND_PIPELINE, 'utf8')

/**
 * 只在 anchor 之后的那段源码上做替换。
 *
 * 为什么要锚：不加锚的话，`replace(/\r?\n\s*signal,/)` 会命中文件里**第一处**
 * `signal,`——那是 backfill 的，不是 pipeline 的。于是负控改的是另一段代码，
 * 判据当然还判 pipeline 是好的，负控就成了永远绿的假货。踩过一次。
 */
function mutateAfter(src, anchor, re) {
  const at = src.indexOf(anchor)
  assert.notEqual(at, -1, `找不到锚点 ${anchor}——判据可能已随重构失效`)
  return src.slice(0, at) + src.slice(at).replace(re, '')
}

describe('pipeline 客户端超时 vs 服务端执行预算', () => {
  it('能从后端源码反推出 pipeline 的执行预算（反推本身不能空跑）', () => {
    const budget = pipelineServerBudgetMs(goSrc)
    assert.notEqual(budget, null, '没从 server_email_pipeline.go 反推出任何 context.WithTimeout 预算——判据失效，不是代码有问题')
    assert.ok(budget >= 60_000, `反推出的预算 ${budget}ms 小于 1 分钟，与「实测 1m30s」的记录矛盾`)
  })

  it('客户端超时严格大于服务端预算', () => {
    const server = pipelineServerBudgetMs(goSrc)
    const client = evalConstMs(apiSrc, 'PIPELINE_TIMEOUT_MS')
    assert.notEqual(client, null, 'api/email.ts 里找不到 PIPELINE_TIMEOUT_MS')
    assert.ok(
      client > server,
      `PIPELINE_TIMEOUT_MS=${client}ms 必须大于服务端预算 ${server}ms；` +
      `否则前端会比后端先放弃，界面报失败而邮件其实已经处理完了`,
    )
  })

  it('runPipeline 真的把 signal 传给了 http（否则无法强行终止）', () => {
    const w = pipelineWiring(apiSrc)
    assert.equal(w.signal, true, 'runPipeline 没有把 signal 传给 http()，需求「后台 api 可强行终止」落空')
  })

  it('runPipeline 真的把 timeoutMs 设成那个常量（不是别的数）', () => {
    const w = pipelineWiring(apiSrc)
    assert.equal(w.timeoutMs, true, 'runPipeline 没有传 timeoutMs，会退回 http.ts 的默认 30s')
    assert.equal(w.timeoutExpr, 'PIPELINE_TIMEOUT_MS')
  })
})

describe('判据自检：负控必须转红', () => {
  // 负控 1：把客户端超时改回 30s 量级（这正是本轮修掉的缺陷）。
  it('客户端超时被改小 → 预算判据转红', () => {
    const broken = apiSrc.replace(
      /export const PIPELINE_TIMEOUT_MS = [^\n]*/,
      'export const PIPELINE_TIMEOUT_MS = 30_000',
    )
    assert.notEqual(broken, apiSrc, '负控样本没有真的改到 PIPELINE_TIMEOUT_MS（替换没命中）')
    const server = pipelineServerBudgetMs(goSrc)
    const client = evalConstMs(broken, 'PIPELINE_TIMEOUT_MS')
    assert.ok(client <= server, '负控本该转红却判成了通过——判据坏了')
  })

  // 负控 2：把 timeoutMs 从 http 的选项里摘掉（只留注释里的常量名）。
  it('只有注释提到 timeoutMs（不接）→ 接线判据转红', () => {
    const broken = mutateAfter(apiSrc, "'/api/email/pipeline/run'",
      /\r?\n\s*timeoutMs: PIPELINE_TIMEOUT_MS,(?=\r?\n)/)
    assert.notEqual(broken, apiSrc, '负控样本没有真的删掉 timeoutMs（替换没命中）')
    const w = pipelineWiring(broken)
    assert.equal(w.timeoutMs, false, '负控本该转红却判成了通过——说明判据在读注释')
  })

  // 负控 3：注释里写着一个假的 WithTimeout 预算，不许被当成真预算。
  it('注释里的 WithTimeout 不计入服务端预算', () => {
    const inflated = goSrc.replace(
      'func (s *Server) runEmailPipeline(',
      '// context.WithTimeout(ctx, 99*time.Hour)\nfunc (s *Server) runEmailPipeline(',
    )
    assert.notEqual(inflated, goSrc, '负控样本没有插进注释（替换没命中）')
    assert.equal(
      pipelineServerBudgetMs(inflated),
      pipelineServerBudgetMs(goSrc),
      '注释里的预算被算进去了——反推判据必须剥注释',
    )
  })

  // 负控 4：把断连中止链路摘掉（signal 不接），终止能力判据必须转红。
  it('signal 被摘掉 → 终止能力判据转红', () => {
    const broken = mutateAfter(apiSrc, "'/api/email/pipeline/run'",
      /\r?\n\s*signal,(?=\r?\n)/)
    assert.notEqual(broken, apiSrc, '负控样本没有真的删掉 signal（替换没命中）')
    assert.equal(pipelineWiring(broken).signal, false)
  })
})
