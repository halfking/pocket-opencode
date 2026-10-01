/**
 * 手工「收信整理」结果提示的验证。
 *
 * Run: node --experimental-strip-types --test src/features/email/invoice-pipeline-toast.test.ts
 *
 * 为什么需要它：runPipeline 原来无条件 toast.success，但 POST
 * /api/email/pipeline/run 是**会失败**的——5 个账户 IMAP 全部超时、垃圾箱
 * MOVE 被服务器拒绝、发票下载失败，这些都进 rep.errors。结果「整轮失败」
 * 和「一切正常」在界面上长得一模一样，只能去翻日志才知道出了事。
 *
 * 这里分两层：
 *   1. 纯函数 pipelineToast 的判定（不是「跑过 happy path」）；
 *   2. 接线护栏——纯函数绿着不能证明 runPipeline 真的在用它。
 *      判据匹配的是**调用**且先去掉注释，否则把接线整行注释掉也能满足。
 */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { describe, it } from 'node:test'
import { pipelineToast } from './invoice-list.ts'

describe('pipelineToast', () => {
  it('只有一条错误时也必须报失败', () => {
    // 这条不是冗余：判据一旦写成 `errs.length > 1`，只报失败的地方
    // （最常见形态：一个账户 IMAP 超时）就会被误判成成功。
    // 2026-10-02 的负控正是靠这一条才抓出来的。
    const t = pipelineToast({ newEmails: 4, errors: ['IMAP 超时 (2.5s): imap.163.com'] })
    assert.equal(t.kind, 'error', '单条失败不等于成功')
    assert.ok(t.text.includes('1 处失败'), `应报出失败条数，实际：${t.text}`)
    assert.ok(t.text.includes('IMAP 超时 (2.5s): imap.163.com'), `应带上首条原因，实际：${t.text}`)
  })

  it('有 errors 时报失败，并给出条数与首条原因', () => {
    const t = pipelineToast({
      newEmails: 12,
      errors: ['IMAP 超时 (2.5s): imap.exmail.qq.com', '发票下载失败: id=abc'],
    })
    assert.equal(t.kind, 'error')
    assert.ok(t.text.includes('2 处失败'), `应报出失败条数，实际：${t.text}`)
    assert.ok(t.text.includes('IMAP 超时 (2.5s): imap.exmail.qq.com'), `应带上首条原因，实际：${t.text}`)
  })

  it('全部失败时仍只显示首条，但条数如实', () => {
    const t = pipelineToast({ errors: ['a', 'b', 'c'] })
    assert.equal(t.kind, 'error')
    assert.ok(t.text.includes('3 处失败'), `条数必须如实，实际：${t.text}`)
    assert.ok(!t.text.includes('b'), '只显示首条，不铺开全部')
  })

  it('errors 为空数组时算成功', () => {
    const t = pipelineToast({ newEmails: 3, errors: [] })
    assert.equal(t.kind, 'success')
  })

  it('errors 字段整个缺失时也算成功（后端可能不下发该字段）', () => {
    const t = pipelineToast({ newEmails: 3 })
    assert.equal(t.kind, 'success')
  })

  it('成功时把提醒与发票计数也说出来，不能只有新邮件数', () => {
    const t = pipelineToast({
      newEmails: 5,
      remindersSent: 2,
      invoices: { downloaded: 3, pending: 1 },
    })
    assert.equal(t.kind, 'success')
    for (const want of ['新邮件 5', '提醒 2', '发票 3 下载', '1 待重试']) {
      assert.ok(t.text.includes(want), `成功提示应包含「${want}」，实际：${t.text}`)
    }
  })

  it('计数字段缺失时补 0，而不是 NaN/undefined', () => {
    const t = pipelineToast({})
    assert.equal(t.kind, 'success')
    for (const bad of ['NaN', 'undefined', 'null']) {
      assert.ok(!t.text.includes(bad), `成功提示不应出现「${bad}」，实际：${t.text}`)
    }
  })

  it('失败优先于成功计数：即使 downloaded>0 也必须报失败', () => {
    const t = pipelineToast({ invoices: { downloaded: 3, pending: 0 }, errors: ['boom'] })
    assert.equal(t.kind, 'error', '部分成功不等于成功，下载到几张发票都要先把失败说出来')
  })
})

// ---------- 接线护栏 ----------

const src = readFileSync(new URL('./use-invoice-list.ts', import.meta.url), 'utf8')

/** 去掉注释后再匹配，避免「把接线注释掉」也能满足断言。 */
const stripComments = (t: string): string =>
  t
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/[^\n]*/g, '$1 ')

const code = stripComments(src)

/** 压掉换行与引号差异，让下面的断言对「if 带不带花括号」两种写法都成立。 */
const flat = code.replace(/\s+/g, ' ').replace(/"/g, "'")

describe('pipelineToast wiring', () => {
  it('imports pipelineToast as a real code reference', () => {
    assert.match(
      code,
      /import\s*\{[^}]*\bpipelineToast\b[^}]*\}\s*from\s*['"][^'"]*invoice-list['"]/,
      'use-invoice-list.ts 必须 import pipelineToast',
    )
  })

  it('actually calls pipelineToast (not just imports it)', () => {
    assert.match(code, /pipelineToast\s*\(/, 'pipelineToast 必须被真的调用')
  })

  it('runPipeline no longer reports success unconditionally', () => {
    // 旧实现是 `if (errs.length > 0) ... else ...`；现在判定在纯函数里，
    // 调用点必须按 kind 分流，否则纯函数白写。两种 if 写法都要认：
    //   if (t.kind === 'error') toast.error(x)
    //   else toast.success(x)
    //   if (t.kind === 'error') { toast.error(x) } else { toast.success(x) }
    const ifThenElse =
      /if\s*\(\s*t\.kind\s*===\s*'error'\s*\)\s*(?:\{[^}]*toast\.error\([^)]*\)[^}]*\}|toast\.error\([^)]*\))\s*else\s*(?:\{[^}]*toast\.success\([^)]*\)|toast\.success\([^)]*\))/
    assert.match(
      flat,
      ifThenElse,
      'runPipeline 必须按 pipelineToast 的 kind 分流：失败→toast.error，否则→toast.success',
    )
  })
})
