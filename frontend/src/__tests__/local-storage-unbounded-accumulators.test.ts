// local-storage-unbounded-accumulators.test.ts — §874：谁在**只写不删**。
//
// §873 找到了第一个只写不删的累加器（邮件正文缓存）并修掉了它。
// 但那一次只查了**一个**文件 —— 而教训恰恰是「枚举这一族的全部成员」。
// ⇒ 本节做全仓对账。
//
// D44 `utils/corrupt-preserve.ts` 的 `preserveCorruptRaw`：
//   键名末尾原来只有 `Date.now()`，两个后果：
//   ① **无上限、永不删除** —— 每一次「读到损坏」就复制一整份原始数据
//      （`aiChatStore` 那边是**最多 40 轮对话全文**）永久留下。
//      §873/D43 刚证明配额有限且已被邮件正文吃掉大头 ⇒ 这是**第二个**
//      只写不删的累加器，打的是同一个器官。
//   ② **同毫秒碰撞** —— 同一个 `ns:kind:scope` 在同一毫秒内触发两次，
//      键名相同 ⇒ 后写的**静默覆盖**先写的证据。
//      这不是理论：`loadSettings`（读侧）与 `applyChatSettings`（写侧）
//      留的是**同一个 kind**、同一个源 key。
//
// ⭐ 本判据还钉一条**跨节**断言：已知的两个累加器**都要**有上限 ——
//   只盯住其中一个，下一个人加回另一个就没人拦。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = join(HERE, '..')
const read = (rel: string) => readFileSync(join(SRC, rel), 'utf8')

/** ⚠ HTML 注释必须**先**去掉（§868 记的事故）。 */
const strip = (t: string): string =>
  t.replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, ' '))
   .replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
   .replace(/(^|[^:])\/\/[^\n]*/g, '$1')

const PRESERVE = strip(read('utils/corrupt-preserve.ts'))
const CACHE = strip(read('features/email/email-body-cache.ts'))

/** 取函数体：本文件的函数全是顶层声明，要求 `function` 关键字（§873 记的事故）。 */
function fnBody(rawCode: string, name: string): string {
  const code = strip(rawCode)
  const m = new RegExp(
    `(?:^|\\n)\\s*(?:export\\s+)?(?:async\\s+)?function\\s+${name}\\s*(?:<[^>]*>)?\\s*\\(`,
  ).exec(code)
  if (!m) return ''
  let d = 0
  let after = -1
  for (let i = m.index + m[0].length - 1; i < code.length; i++) {
    if (code[i] === '(') d++
    else if (code[i] === ')') { d--; if (d === 0) { after = i + 1; break } }
  }
  if (after < 0) return ''
  const start = code.indexOf('{', after)
  if (start < 0) return ''
  let depth = 0
  for (let i = start; i < code.length; i++) {
    if (code[i] === '{') depth++
    else if (code[i] === '}') { depth--; if (depth === 0) return code.slice(start, i + 1) }
  }
  return ''
}

describe('§874 · 尺子自证', () => {
  it('★★ 文件读到了', () => {
    assert.ok(PRESERVE.includes('preserveCorruptRaw'), '没读到 preserveCorruptRaw')
    assert.ok(CACHE.includes('evictOverBudget'), '没读到 §873 的淘汰函数')
  })
})

describe('§874 · D44 损坏留证必须有数量上限且真删', () => {
  it('★★★ 必须声明留证份数上限', () => {
    assert.match(PRESERVE, /const\s+PRESERVE_MAX_PER_KIND\s*=\s*\d/,
      '没有 PRESERVE_MAX_PER_KIND ⇒ 留证键无上限、永不删除')
  })

  it('★★★ 写完必须记账并淘汰（写成功 ≠ 完事）', () => {
    const body = fnBody(PRESERVE, 'preserveCorruptRaw')
    assert.ok(body, '找不到 preserveCorruptRaw')
    assert.match(body, /enforcePreserveBudget/, '留证写完后没有触发预算检查（§873 的教训）')
  })

  it('★★★ 淘汰必须真的 removeItem（只截索引等于没释放额度）', () => {
    const body = fnBody(PRESERVE, 'enforcePreserveBudget')
    assert.ok(body, '找不到 enforcePreserveBudget')
    assert.match(body, /removeItem/, '淘汰没有真正删除旧证据')
    assert.match(body, /console\.warn/, '淘汰没有任何日志 ⇒ 事后无法判断证据被丢过')
  })

  it('★★★ 键名必须能区分同一毫秒内的两次留证', () => {
    const body = fnBody(PRESERVE, 'preserveCorruptRaw')
    assert.ok(body, '找不到 preserveCorruptRaw')
    assert.doesNotMatch(body, /\$\{Date\.now\(\)\}`/,
      '键名末尾只有 Date.now() ⇒ 同一毫秒的两次留证会**静默覆盖**（§874/D44）')
    assert.match(body, /\$\{Date\.now\(\)\}-\$\{preserveSeq\}/,
      '键名没有单调序号 ⇒ 同毫秒碰撞未处理')
  })
})

describe('§874 · 跨节：两个已知累加器都必须在册', () => {
  it('★★★ §873 的邮件正文缓存仍然有上限（别修好一个又放掉另一个）', () => {
    assert.match(CACHE, /const\s+MAX_ENTRIES\s*=\s*\d/, '§873 的条数上限没了')
    assert.match(CACHE, /const\s+MAX_TOTAL_CHARS\s*=\s*\d/, '§873 的字节上限没了')
  })

  it('★★ 两个累加器的上限都还在（一条判据钉住两处）', () => {
    const bounded = [PRESERVE, CACHE].filter((s) =>
      /const\s+(PRESERVE_MAX_PER_KIND|MAX_ENTRIES)\s*=\s*\d/.test(s))
    assert.equal(bounded.length, 2, '有累加器失去了数量上限 —— 只盯住其中一个，下一个人加回另一个就没人拦')
  })
})

describe('§874 · 变异负控', () => {
  it('把预算检查删掉 ⇒ D44 立刻红', () => {
    const body = fnBody(PRESERVE, 'preserveCorruptRaw')
    assert.ok(body, '取不到 preserveCorruptRaw 的函数体')
    const defective = body.replace(/enforcePreserveBudget\(ns, kind\)/, 'void 0')
    assert.notEqual(defective, body, '变异没生效（锚点与真源码不匹配）')
    assert.doesNotMatch(defective, /enforcePreserveBudget/,
      '尺子坏了：删掉预算检查后判据仍判绿')
  })

  it('把淘汰改成「只丢引用不删键」⇒ D44 立刻红', () => {
    const body = fnBody(PRESERVE, 'enforcePreserveBudget')
    assert.ok(body, '取不到 enforcePreserveBudget 的函数体')
    const defective = body.replace(/localStorage\.removeItem\(k\)/, 'void k')
    assert.notEqual(defective, body, '变异没生效（锚点与真源码不匹配）')
    assert.doesNotMatch(defective, /removeItem/,
      '尺子坏了：不再删键后判据仍判绿')
  })

  it('把 §873 的上限抹掉 ⇒ 跨节判据立刻红', () => {
    const mutated = CACHE.replace(/const\s+MAX_ENTRIES\s*=\s*\d+/, 'const MAX_ENTRIES = Infinity')
    assert.notEqual(mutated, CACHE, '变异没生效')
    const bounded = [mutated, PRESERVE].filter((s) =>
      /const\s+(PRESERVE_MAX_PER_KIND|MAX_ENTRIES)\s*=\s*\d/.test(s))
    assert.equal(bounded.length, 1, '尺子坏了：抹掉 §873 的上限后跨节判据仍判绿')
  })
})
