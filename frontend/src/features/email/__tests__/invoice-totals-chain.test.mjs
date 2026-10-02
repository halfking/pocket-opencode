/**
 * 发票合计的「不许丢字段」链路（§7do）。
 *
 * ## 这条链路是什么
 *
 * ```
 * 服务端 server_email_invoice.go:55-57
 *     "amount" / "currency" / "amounts"   ← 三件套一起发
 *        ↓
 * 转发层 invoice-list-pull.ts  →  invoiceTotalsFrom(res)
 *        ↓
 * 判定层 use-invoice-list.ts applySummary  →  resolveSummaryGroups(totals, list)
 *        ↓
 * 展示   summaryMoney(groups)
 * ```
 *
 * 修复前，转发层只发 `{total, filed, amount}`，把 `currency` 和 `amounts`
 * **吃掉了**。于是两种错账：
 *
 *  - **单一外币**：currency 丢失 → `normalizeCurrency(undefined)` 兜底成 CNY
 *    → 100 USD 的发票列表合计渲染成「¥100.00」。这正是 §7dk 在服务端修掉的
 *    那一类错账，在客户端**下面一层**又长出来一次。
 *  - **多币种**：amounts 丢失、且服务端此时 amount=0 → 退回
 *    `sumByCurrency(当前页)` → 合计从「全量」缩成「这一页」，翻页时数字会跳。
 *
 * 本文件用**行为断言**覆盖整条链路：喂进服务端那种响应，断言最终 groups。
 * 不去正则刨 `invoice-list-pull.ts` 的源码——刨出来的片段会带 TS 类型注解，
 * 而且源码匹配能被注释满足（§7dm 的教训：注释满足了对 bug 的检查）。
 *
 * 另有一条**结构断言**钉住「转发层必须走 invoiceTotalsFrom」，
 * 防止有人把转发层改回字面量而行为断言仍然全绿（转发层依赖 Capacitor，
 * 没法在 node --test 里直接 import）。
 */

import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  invoiceTotalsFrom, resolveSummaryGroups, summaryMoney, normalizeCurrency,
} from '../invoice-money.ts'

const FEAT = path.join(path.dirname(fileURLToPath(import.meta.url)), '..')
const readSrc = (p) => fs.readFileSync(path.join(FEAT, p), 'utf8')

// ---------------------------------------------------------------------------
// 转发层：三个字段一个都不能少
// ---------------------------------------------------------------------------
test('转发层必须把 amount / currency / amounts 三个字段都带下去', () => {
  const res = { total: 3, filed: 1, amount: 3500, currency: 'USD' }
  const t = invoiceTotalsFrom(res)
  assert.equal(t.amount, 3500)
  assert.equal(t.currency, 'USD', 'currency 丢失 -> 单一外币会被当成 CNY（错账）')
  assert.ok('amounts' in t, 'amounts 字段必须存在（哪怕是 undefined），少转发就是错账')
})

test('多币种响应（amount=0、amounts 非空）必须完整转发', () => {
  const res = {
    total: 5,
    filed: 2,
    amount: 0,
    currency: '',
    amounts: [
      { currency: 'CNY', amount: 1280, count: 3 },
      { currency: 'USD', amount: 3500, count: 2 },
    ],
  }
  const t = invoiceTotalsFrom(res)
  assert.equal(t.amount, 0, '服务端在多币种时给 amount=0，前端不得自行改成裸求和')
  assert.equal(t.amounts.length, 2)
  assert.deepEqual(t.amounts.map((a) => a.currency), ['CNY', 'USD'])
})

test('字段缺失时兜底为 0 / undefined，而不是抛错或造出假的单币种', () => {
  const t = invoiceTotalsFrom({})
  assert.equal(t.total, 0)
  assert.equal(t.filed, 0)
  assert.equal(t.amount, 0)
  assert.equal(t.currency, undefined)
  assert.equal(t.amounts, undefined)
})

// ---------------------------------------------------------------------------
// 整条链路：服务端响应 -> 展示字符串
// ---------------------------------------------------------------------------
test('单一外币：合计显示 $，不是 ¥（这就是修复前被吃掉的 currency）', () => {
  const serverRes = { total: 3, filed: 1, amount: 3500, currency: 'USD' }
  const groups = resolveSummaryGroups(invoiceTotalsFrom(serverRes), [])
  assert.deepEqual(groups, [{ currency: 'USD', amount: 3500 }])
  assert.equal(summaryMoney(groups), '$3,500.00')
  assert.ok(!summaryMoney(groups).includes('¥'), 'USD 合计被渲染成人民币就是错账')
})

test('多币种：逐币种拼，绝不给一个裸数字', () => {
  const serverRes = {
    total: 5,
    filed: 2,
    amount: 0,
    currency: '',
    amounts: [
      { currency: 'CNY', amount: 1280, count: 3 },
      { currency: 'USD', amount: 3500, count: 2 },
    ],
  }
  const groups = resolveSummaryGroups(invoiceTotalsFrom(serverRes), [])
  assert.equal(groups.length, 2)
  assert.equal(summaryMoney(groups), '¥1,280.00 + $3,500.00')
  // 关键：绝不能出现 1280+3500=4780 这样一个数
  assert.ok(!summaryMoney(groups).includes('4,780'))
})

test('多币种时用 amounts（服务端全量），而不是退回当前页的分组求和', () => {
  // 当前页只有一张 USD；服务端说全量是 CNY 1280 + USD 3500。
  // 若 amounts 被吃掉，就会退化成「只有 USD 3500」——合计从全量缩成一页。
  const page = [{ amount: 3500, currency: 'USD' }]
  const serverRes = {
    total: 5, filed: 2, amount: 0, currency: '',
    amounts: [
      { currency: 'CNY', amount: 1280, count: 3 },
      { currency: 'USD', amount: 3500, count: 2 },
    ],
  }
  const groups = resolveSummaryGroups(invoiceTotalsFrom(serverRes), page)
  assert.equal(groups.length, 2, '丢了 amounts 就会退化成只有一页的分组')
  assert.equal(summaryMoney(groups), '¥1,280.00 + $3,500.00')
})

test('服务端没给合计时，才退回当前页自行分组', () => {
  const page = [
    { amount: 100, currency: 'CNY' },
    { amount: 20, currency: 'USD' },
  ]
  const groups = resolveSummaryGroups(invoiceTotalsFrom({}), page)
  assert.equal(summaryMoney(groups), '¥100.00 + $20.00')
})

test('服务端给了 amounts 就以它为准，哪怕当前页完全对不上（分页会变，全量不会）', () => {
  const serverRes = {
    total: 100, filed: 50, amount: 999999, currency: 'CNY',
    amounts: [{ currency: 'CNY', amount: 12345, count: 100 }],
  }
  const page = [{ amount: 1, currency: 'CNY' }]
  const groups = resolveSummaryGroups(invoiceTotalsFrom(serverRes), page)
  assert.equal(summaryMoney(groups), '¥12,345.00', 'amounts 优先于 amount，也优先于本地页')
})

test('currency 为空串时兜底成 CNY（与服务端 currencyOrDefault 同源）', () => {
  assert.equal(normalizeCurrency(''), 'CNY')
  assert.equal(normalizeCurrency(undefined), 'CNY')
  const groups = resolveSummaryGroups({ amount: 88, currency: '' }, [])
  assert.deepEqual(groups, [{ currency: 'CNY', amount: 88 }])
})

// ---------------------------------------------------------------------------
// 结构断言：转发层必须真的走 invoiceTotalsFrom
// ---------------------------------------------------------------------------
// 行为断言只覆盖 invoiceTotalsFrom **本身**。若有人把 invoice-list-pull.ts
// 改回字面量 `{total, filed, amount}`，上面全部仍然全绿 —— 那正是修复前的
// 状态。这条断言补上那一段。
test('结构：转发层用 invoiceTotalsFrom，而不是手写字面量', () => {
  const src = readSrc('invoice-list-pull.ts')
  assert.match(src, /totals: invoiceTotalsFrom\(res\)/, '转发层必须走 invoiceTotalsFrom')
  assert.ok(
    !/totals:\s*\{\s*total:[^}]*amount:\s*res\.amount/.test(src),
    '转发层又退回了手写字面量 —— currency/amounts 会被静默吃掉',
  )
})

test('结构：applySummary 的合计判定走 resolveSummaryGroups', () => {
  const src = readSrc('use-invoice-list.ts')
  assert.match(src, /resolveSummaryGroups\(totals, list\)/)
  const body = src.match(/function applySummary\([\s\S]*?\n  \}/)?.[0] ?? ''
  assert.ok(body, '未找到 applySummary')
  assert.ok(
    !/totals\?\.amounts\?\.length[\s\S]*?totals\?\.amount[\s\S]*?sumByCurrency\(list\)/.test(body),
    'applySummary 里仍有内联的优先级判定，与 resolveSummaryGroups 分叉了',
  )
})
