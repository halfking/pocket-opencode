/**
 * 发票合计字段的**跨语言**契约：Go 线上发的键名 == 前端读的键名。
 *
 * ## 这个护栏防的是什么（2026-10-03 真机实测）
 *
 * Redmi 真机 /email/invoices 顶部合计金额显示 **¥NaN**，同一屏「共 4 张」正常。
 * 逐层量出来的原因（原始响应体，真机 + 页面上下文 fetch）：
 *
 *     "amount":3500, "currency":"CNY",
 *     "amounts":[{"Currency":"CNY","Amount":3500,"Count":1}]   ← 首字母大写
 *
 * `email.CurrencyTotal` 没有 json tag，encoding/json 按字段名原样输出；
 * `resolveSummaryGroups` 读 `a.currency` / `a.amount` → 两个都 undefined →
 * `round2(undefined)` = NaN。
 *
 * ## 为什么仓库原有护栏全绿
 *
 * - `invoice_total_parity_test.go`：钉「三处实现的**数值**一致」，三处都在 Go
 *   内部比对，**从不序列化**。数值对得上 ≠ 线上键名对得上。
 * - `invoice-totals-chain.test.mjs`：12 条行为用例，但夹具是**手写的
 *   camelCase**。它验证的是「若服务端按 camelCase 发，前端会不会用」，而不是
 *   「服务端发的到底是不是 camelCase」——夹具与被测对象出自同一个假设。
 *
 * 结果：整条链上**没有任何一条用例断言过响应体里的字段名**。
 *
 * ## 本文件的分工
 *
 * 本文件读源码对源码，成本低、能同时盯住两侧改名；它天生会被注释满足
 * （§7dm 的教训），所以**运行时那一半**放在
 * `backend/internal/server/server_email_invoice_wire_keys_test.go`：真 handler +
 * 真 store 出线上字节。两边合起来才没有盲区。
 *
 * ## 负控（实测过）
 *
 * 1. 删掉 CurrencyTotal 的 json tag → 本文件「必须是首字母小写」与
 *    「前端读的每个键都必须在 Go 的 tag 里」同时转红；同批
 *    server_email_invoice_wire_keys_test.go 两条用例也转红，而
 *    invoice_total_parity_test.go 仍全绿（它不碰序列化）。
 * 2. 把 Go 的 tag 改成 `Currency`（只改服务端）→ 「前端读的每个键都必须在
 *    Go 的 tag 里」转红。
 * 3. 把 invoice-money.ts 读的键改成 `a.money`（只改前端）→ 同一判据转红。
 */

import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { resolveSummaryGroups, summaryMoney } from '../invoice-money.ts'

const FEAT = path.join(path.dirname(fileURLToPath(import.meta.url)), '..')
const REPO = path.join(FEAT, '..', '..', '..', '..')
const readAbs = (p) => fs.readFileSync(path.join(REPO, p), 'utf8')

/** 取出 `func (s *Server) handleEmailInvoices(` 到下一个顶层 `default:` 之间的源码。 */
function handlerSource() {
  const src = readAbs('backend/internal/server/server_email_invoice.go')
  const start = src.indexOf('func (s *Server) handleEmailInvoices(')
  assert.notEqual(start, -1, '找不到 handleEmailInvoices —— 判据不能靠「没找到就当没违规」过关')
  const end = src.indexOf('\n\tdefault:', start)
  assert.notEqual(end, -1, 'handleEmailInvoices 结束位置变了，判据需要跟着更新')
  return src.slice(start, end)
}

/** CurrencyTotal 结构体块（不是全文搜字段名——那会被注释和别的结构体满足）。 */
function currencyTotalBlock() {
  const src = readAbs('backend/internal/email/ledger.go')
  const start = src.indexOf('type CurrencyTotal struct {')
  assert.notEqual(start, -1, 'ledger.go 里找不到 CurrencyTotal')
  const end = src.indexOf('\n}', start)
  assert.notEqual(end, -1, 'CurrencyTotal 结构体块没有正常结束')
  return src.slice(start, end)
}

/** struct 块里所有 json tag 的键名。 */
function goTagKeys() {
  return [...currencyTotalBlock().matchAll(/json:"(\w+)"/g)].map((m) => m[1])
}

/**
 * 取一个 `export function` 的函数体。
 *
 * 结尾必须匹配**独占一行**的 `}`（`\n}\r?\n`），不能用 `\n}`：参数类型块的
 * 收尾是 `})` ——第一版就是在这里把范围切在了参数块里，于是「前端读的键」
 * 一条都没抓到。判据扫错范围时必须响亮地红，并说清是范围问题。
 */
function functionBody(src, signature) {
  const start = src.indexOf(signature)
  assert.notEqual(start, -1, `找不到 ${signature}`)
  const m = /\n\}\r?\n/.exec(src.slice(start))
  assert.ok(m, `${signature} 的结尾没找到独占一行的 } —— 判据的范围切片失效了`)
  return src.slice(start, start + m.index)
}

/** invoiceTotalsFrom 函数体（转发层从响应里读的键）。 */
function totalsFromBody() {
  const body = functionBody(readAbs('frontend/src/features/email/invoice-money.ts'),
    'export function invoiceTotalsFrom(')
  assert.ok(/return\s*\{/.test(body),
    'invoiceTotalsFrom 的切片里没有 return —— 范围切错了，不是「它没读字段」')
  return body
}

/** normalizeAmounts 函数体（判定层从 amounts[] 元素上读的键）。 */
function normalizeAmountsBody() {
  const body = functionBody(readAbs('frontend/src/features/email/invoice-money.ts'),
    'function normalizeAmounts(')
  assert.ok(/for\s*\(/.test(body),
    'normalizeAmounts 的切片里没有循环体 —— 范围切错了，不是「它没读字段」')
  return body
}

// ---------------------------------------------------------------------------
// 1. Go 侧：tag 必须存在且是小驼峰
// ---------------------------------------------------------------------------
test('CurrencyTotal 的每个字段都必须有 json tag，且键名首字母小写', () => {
  const keys = goTagKeys()
  assert.deepEqual(
    keys.slice().sort(),
    ['amount', 'count', 'currency'],
    `线上键名 = ${JSON.stringify(keys)}。少了 tag（或写成大写）时 encoding/json 会按字段名` +
      '原样输出，前端读 a.currency / a.amount 读到 undefined → 页面合计显示 ¥NaN。' +
      '注意 invoice_total_parity_test.go 此时仍全绿——它不碰序列化。',
  )
  const goFields = [...currencyTotalBlock().matchAll(/^\t([A-Z]\w*)\s+\S/gm)].map((m) => m[1])
  assert.equal(keys.length, goFields.length,
    `${goFields.length} 个字段只有 ${keys.length} 个 json tag：${JSON.stringify(goFields)}`)
})

// ---------------------------------------------------------------------------
// 2. 跨语言：前端读的每个键都必须在 Go 的 tag 里
// ---------------------------------------------------------------------------
test('前端从 amounts[] 元素上读的每个键，都必须是 Go CurrencyTotal 的 tag', () => {
  const tags = new Set(goTagKeys())
  // 只取 `??` 左侧那个（首选拼写）。右侧是灰度期兜底的大写拼写，
  // 它不应该成为契约——契约由 Go 的 tag 决定。
  const preferred = [...normalizeAmountsBody().matchAll(/\ba\.(\w+)\s*\?\?/g)].map((m) => m[1])
  assert.ok(preferred.length > 0,
    '没抓到任何 `a.<key> ??` 读取 —— 判据扫不到东西时会静默放行（空 grep 只回答了' +
    '「模式问的那个问题」）。请同步更新本判据。')
  for (const k of preferred) {
    assert.ok(tags.has(k),
      `前端读 a.${k}，但 Go CurrencyTotal 的 tag 里没有 "${k}"（tag = ${JSON.stringify([...tags])}）。` +
        '两侧只改一边就会在这里变红。')
  }
})

test('灰度期兜底拼写必须正好是首选拼写的大写形式（不多不少一种）', () => {
  const body = normalizeAmountsBody()
  const pairs = [...body.matchAll(/\ba\.(\w+)\s*\?\?\s*a\.(\w+)/g)].map((m) => [m[1], m[2]])
  assert.ok(pairs.length > 0, '没抓到 `a.x ?? a.X` 的兜底读取')
  for (const [lower, upper] of pairs) {
    assert.equal(upper, lower[0].toUpperCase() + lower.slice(1),
      `a.${lower} ?? a.${upper}：兜底拼写应当是首选拼写的大写形式（服务端无 tag 时的线上形状）`)
  }
})

// ---------------------------------------------------------------------------
// 3. 跨语言：handler 写出的顶层键 ⊇ 转发层读的键
// ---------------------------------------------------------------------------
test('handleEmailInvoices 写出的顶层字段必须覆盖 invoiceTotalsFrom 读的全部字段', () => {
  const written = new Set([...handlerSource().matchAll(/"(\w+)":/g)].map((m) => m[1]))
  const read = [...totalsFromBody().matchAll(/\bres\.(\w+)/g)].map((m) => m[1])
  assert.ok(read.length > 0, '没抓到 invoiceTotalsFrom 的 res.<key> 读取')
  for (const k of read) {
    assert.ok(written.has(k),
      `转发层读 res.${k}，但 handleEmailInvoices 没有写 "${k}"（写了 ${JSON.stringify([...written])}）。` +
        '少一个字段就是错账：currency 丢 → 外币被当 CNY；amounts 丢 → 合计从全量缩成当前页。')
  }
})

// ---------------------------------------------------------------------------
// 4. 行为：真机观测到的两种形状都不能渲染出 NaN
// ---------------------------------------------------------------------------
const page = [
  { amount: 3500, currency: 'CNY', status: 'downloaded', fileName: 'a.pdf' },
]

test('修正后的线上形状（camelCase）必须渲染出真实金额', () => {
  const groups = resolveSummaryGroups(
    { total: 4, filed: 0, amount: 3500, currency: 'CNY', amounts: [{ currency: 'CNY', amount: 3500, count: 1 }] },
    page,
  )
  assert.equal(summaryMoney(groups), '¥3,500.00')
})

test('真机实际收到的旧形状（PascalCase）不得渲染成 ¥NaN', () => {
  // 这就是 2026-10-03 真机上的原始载荷（无 tag 的 CurrencyTotal）。
  const groups = resolveSummaryGroups(
    { total: 4, filed: 0, amount: 3500, currency: 'CNY', amounts: [{ Currency: 'CNY', Amount: 3500, Count: 1 }] },
    page,
  )
  const text = summaryMoney(groups)
  assert.ok(!/NaN/.test(text),
    `旧形状渲染成 ${text} —— 这正是真机上的症状。兼容分支必须读大写拼写。`)
  assert.equal(text, '¥3,500.00')
})

test('形状完全不可信时整组作废并落回标量，而不是逐项跳过或显示 NaN', () => {
  const groups = resolveSummaryGroups(
    // amounts 里混了一个没有金额的条目：逐项跳过会让合计少算却看起来正常。
    { total: 4, filed: 0, amount: 4780, currency: 'CNY', amounts: [{ currency: 'CNY', amount: 3500 }, { currency: 'USD' }] },
    page,
  )
  const text = summaryMoney(groups)
  assert.ok(!/NaN/.test(text), `渲染成 ${text}`)
  // 落回 amount + currency（4780 是服务端在这个响应里给的标量）
  assert.equal(text, '¥4,780.00', '不可信的 amounts 应整组作废并落回标量，而不是留下半截合计')
})
