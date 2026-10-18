// email-body-cache-budget.test.ts — §873：谁把 localStorage 填满的。
//
// §866–§872 六节都在防守「localStorage 写满」这个前提，但**没有人查过
// 是谁把它填满的**。本节去查那个被反复引用的文件
// （`email-body-cache.ts` 的文件头正是「Android WebView 总配额 5–10MB」
// 这个说法的出处）。
//
// D43（2026-10-18 取证）：
//   · 逐封写 key 之后，**删除路径只剩一条** —— `email-soft-delete.ts`，
//     即只有用户删掉那封邮件时才会清；
//   · 而 `EmailInboxView::open()` 每次点开一封会缓存**三封**正文
//     （当前 + `prefetchEmailBodySeries(ahead, …, 2)` 的后两封）。
//   按该文件自己写的「正文几十到几百 KB」算，一次点击约 300KB，
//   5–10MB 配额 ⇒ **点过十几到三十几封邮件，localStorage 就满了** ⇒
//   之后本仓**所有**写入静默失败：登录会话（§867/D33）、AI 对话历史
//   （§866/D30）、闪卡队列（§868/D36）、加密 salt（§867/D35）……
//
// ⚠ 而且该文件的注释明确声称问题已经解决（见本判据末尾的断言）——
//   分 key 只消除了「单次写入超配额」，同时制造了「逐封无限累积」。
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

const CACHE = strip(read('features/email/email-body-cache.ts'))
const PREFETCH = strip(read('features/email/email-body-prefetch.ts'))

/**
 * 取函数体。
 * ⚠⚠ 取件器事故累计第 6 次（这次叫「行首锚点不够」）：
 *   前几节用 `(?:^|\n)\s*` 锚行首来排除调用点。但本文件的函数行首是
 *   `export async function writeEmailBodyLocal(…)` —— `\s*` 吃不下
 *   `export ` ⇒ 正则匹配失败 ⇒ 退而匹配到**行首缩进的调用点**
 *   `readEmailBodyLocal` 里的 `writeEmailBodyLocal(id, …)` 调用。
 *   ⇒ 本文件的函数全是**顶层声明**，所以直接要求 `function` 关键字，
 *     从语法上就不可能匹配到调用点。
 */
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

const LIMIT_CONSTS = ['MAX_ENTRIES', 'MAX_TOTAL_CHARS']

describe('§873 · 尺子自证', () => {
  it('★★ 文件读到了', () => {
    assert.ok(CACHE.includes('writeEmailBodyLocal'), '没读到 writeEmailBodyLocal')
    assert.ok(CACHE.includes('readIndex'), '没读到索引读取')
  })
})

describe('§873 · D43 正文缓存必须有上限、有淘汰', () => {
  it('★★★ 必须声明**两个**上限（条数与字节，缺一不可）', () => {
    for (const k of LIMIT_CONSTS) {
      assert.ok(new RegExp(`const\\s+${k}\\s*=\\s*\\d`).test(CACHE),
        `没有 ${k} ⇒ 只限一个维度挡不住另一种极端（一封 500KB × N 封 / 几封超长正文吃光额度）`)
    }
  })

  it('★★★ 每次写入后必须记账并淘汰', () => {
    const body = fnBody(CACHE, 'writeEmailBodyLocal')
    assert.ok(body, '找不到 writeEmailBodyLocal')
    assert.match(body, /readIndex/, '写入后没有读索引 ⇒ 无法知道缓存里有什么')
    assert.match(body, /writeIndex/, '写入后没有更新索引 ⇒ 淘汰无从下手')
    assert.match(body, /evictOverBudget/, '写入后没有触发淘汰 ⇒ 缓存仍会无上限增长（§873/D43）')
  })

  it('★★★ 淘汰必须真的删正文（不只是把索引截短）', () => {
    const body = fnBody(CACHE, 'evictOverBudget')
    assert.ok(body, '找不到 evictOverBudget')
    assert.match(body, /removeItem/, '淘汰只改了索引、没删正文 ⇒ 额度一点没释放')
  })

  it('★★ 删除单封时必须同步删索引', () => {
    const body = fnBody(CACHE, 'clearEmailBodyLocal')
    assert.ok(body, '找不到 clearEmailBodyLocal')
    assert.match(body, /readIndex/, '删除单封没有读索引')
    assert.match(body, /splice/, '删除单封没有把它从索引里摘掉 ⇒ 白占淘汰名额')
  })

  it('★★ 索引本身写失败要说清后果（此后不再淘汰）', () => {
    const body = fnBody(CACHE, 'writeIndex')
    assert.ok(body, '找不到 writeIndex')
    assert.match(body, /console\.warn/, '索引写失败没有任何日志 ⇒ 会静默退化成「无上限增长」')
  })

  it('★★★ 那句「不再有超配额的悬崖」只允许出现在纠正它的注释里', () => {
    // 这句话是 §873/D43 被误判为「已解决」的直接原因。
    // ⚠ 注意：修复后它**仍然出现在文件里**（作为被引用的原话），
    //   所以不能简单 grep「不许出现」—— 那会把「诚实地引用并推翻它」
    //   也判成违规。判据钉的是：**每次出现都必须紧挨着纠正标记**。
    const raw = read('features/email/email-body-cache.ts')
    const phrase = '不再有「整体超配额」的悬崖'
    const at = [...raw.matchAll(new RegExp(phrase.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'g'))]
      .map((m) => m.index ?? -1)
    assert.ok(at.length > 0, '那句原话被整段删掉了 —— 请保留它并写清它为什么是错的，否则后人会重新踩')
    for (const i of at) {
      const ctx = raw.slice(Math.max(0, i - 400), i)
      assert.ok(/§873\/D43/.test(ctx),
        '「不再有超配额的悬崖」出现在一段**没有**纠正标记的注释里 ⇒ 后人会当成结论')
    }
  })

  it('★★ 预取确实会一次缓存多封（这是本节严重度的依据）', () => {
    assert.ok(PREFETCH.includes('writeCache'), '预取不再写缓存 ⇒ 本节的前提变了，请复核 D43 的严重度')
    const inbox = strip(read('features/email/EmailInboxView.vue'))
    assert.match(inbox, /prefetchEmailBodySeries/,
      '收件箱不再预取后两封 ⇒ 一次点击只缓存 1 封，D43 的严重度需重新评估')
  })
})

describe('§873 · 变异负控', () => {
  it('把淘汰调用删掉 ⇒ D43 立刻红', () => {
    const body = fnBody(CACHE, 'writeEmailBodyLocal')
    assert.ok(body, '取不到 writeEmailBodyLocal 的函数体')
    const defective = body.replace(/evictOverBudget\(store, ids\)/, 'void 0')
    assert.notEqual(defective, body, '变异没生效（锚点与真源码不匹配）')
    assert.doesNotMatch(defective, /evictOverBudget/,
      '尺子坏了：删掉淘汰调用后判据仍判绿')
  })

  it('把淘汰改成「只截索引不删正文」⇒ D43 立刻红', () => {
    const body = fnBody(CACHE, 'evictOverBudget')
    assert.ok(body, '取不到 evictOverBudget 的函数体')
    const defective = body.replace(/store\.removeItem\(PREFIX \+ victim\)/, 'void victim')
    assert.notEqual(defective, body, '变异没生效（锚点与真源码不匹配）')
    assert.doesNotMatch(defective, /removeItem/,
      '尺子坏了：淘汰不再删正文后判据仍判绿')
  })
})
