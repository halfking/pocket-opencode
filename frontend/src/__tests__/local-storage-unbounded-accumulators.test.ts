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
import { describe, it, beforeEach } from 'node:test'
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


// ======================================================================================
// §875 行为级判据（并入本文件，理由见下）
//
// ⚠ 并入而不是新开一个文件，是因为 frontend/scripts/run-mjs-tests.mjs 的
//   20000 字符 argv 上限：测试文件数已经到 376，再加一个文件就把
//   `npm run test:all` 顶红（实测 20002 > 20000）。
//   ⚠ 该 runner 自己在报错里指名了正解（「让全部测试都能用 glob 跑」），
//   但换 glob 属**构建口径**的决定 ⇒ 登记在简报第 94 条，不在本节代办。
// ======================================================================================

class FakeStorage {
  private map = new Map<string, string>()
  /** 非 null 时，任何超过该字节数的写入抛错 —— 用来模拟配额写满。 */
  quotaBytes: number | null = null
  failOnRemove = false

  get length(): number { return this.map.size }
  key(i: number): string | null { return [...this.map.keys()][i] ?? null }
  getItem(k: string): string | null { return this.map.get(k) ?? null }
  setItem(k: string, v: string): void {
    if (this.quotaBytes !== null) {
      const next = new Map(this.map)
      next.set(k, v)
      const bytes = [...next].reduce((n, [key, val]) => n + key.length + val.length, 0) * 2
      if (bytes > this.quotaBytes) {
        throw new DOMException('QuotaExceededError')
      }
    }
    this.map.set(k, v)
  }
  removeItem(k: string): void {
    if (this.failOnRemove) throw new DOMException('SecurityError')
    this.map.delete(k)
  }
  /** 测试用：当前存放的键，按插入序。 */
  keys(): string[] { return [...this.map.keys()] }
  /** 测试用：总字节（UTF-16 近似）。 */
  totalChars(): number {
    return [...this.map.values()].reduce((n, v) => n + v.length, 0)
  }
  clear(): void { this.map.clear() }
}

const fake = new FakeStorage()
;(globalThis as Record<string, unknown>).localStorage = fake

// ⚠ corrupt-preserve.ts **零 import**，可以直接在 node 里加载。
const { preserveCorruptRaw, AI_CHAT_CORRUPT_NS } =
  await import('../utils/corrupt-preserve.ts')

describe('§875 · 尺子自证', () => {
  it('★ 假 Storage 与真模块都加载到了', () => {
    assert.equal(typeof localStorage.setItem, 'function')
    assert.equal(typeof preserveCorruptRaw, 'function')
    assert.ok(AI_CHAT_CORRUPT_NS.startsWith('pocket:'), '命名空间常量读不到')
  })
})

describe('§875 · D44 行为验证：留证真的会被淘汰（旧的那份真的被删掉）', () => {
  beforeEach(() => { fake.clear(); fake.quotaBytes = null; fake.failOnRemove = false })

  it('★★★ 连写 10 次留证，磁盘上只剩上限份数', () => {
    const kept: string[] = []
    for (let i = 0; i < 10; i++) {
      const k = preserveCorruptRaw(AI_CHAT_CORRUPT_NS, 'settings', `payload-${i}`)
      assert.ok(k, `第 ${i} 次留证没有写下去`)
      kept.push(k)
    }
    const remaining = fake.keys().filter((k) => k.startsWith(`${AI_CHAT_CORRUPT_NS}:settings:`))
    assert.equal(remaining.length, 3,
      `磁盘上有 ${remaining.length} 份留证，期望 3 —— 上限没有在**行为**上生效`)
    // 留存的必须是**最新**的 3 份，而不是最旧或随机 3 份
    assert.deepEqual(remaining, kept.slice(-3),
      '留存的不是最新 3 份 ⇒ 淘汰把新旧判反了（文本判据发现不了这一类错误）')
  })

  it('★★ 同一个 `kind` 的淘汰不会误伤另一个 `kind`', () => {
    for (let i = 0; i < 6; i++) preserveCorruptRaw(AI_CHAT_CORRUPT_NS, 'conversations', `c-${i}`)
    for (let i = 0; i < 6; i++) preserveCorruptRaw(AI_CHAT_CORRUPT_NS, 'settings', `s-${i}`)
    const conv = fake.keys().filter((k) => k.includes(':conversations:'))
    const settings = fake.keys().filter((k) => k.includes(':settings:'))
    assert.equal(conv.length, 3, `conversations 剩 ${conv.length} 份`)
    assert.equal(settings.length, 3, `settings 剩 ${settings.length} 份`)
  })

  it('★★ 同毫秒内两次留证不会互相覆盖', () => {
    // ⚠ 这条钉的是 §874/D44 的第二个后果。文本判据只能看到「键名里有没有
    //   seq」，而**真正要证明的是**两��调用返回了两个不同的键。
    const a = preserveCorruptRaw(AI_CHAT_CORRUPT_NS, 'settings', 'AAA')
    const b = preserveCorruptRaw(AI_CHAT_CORRUPT_NS, 'settings', 'BBB')
    assert.notEqual(a, b, '两次留证拿到同一个键 ⇒ 后写的会静默覆盖先写的')
    assert.equal(fake.getItem(a!), 'AAA', '先写的那份证据被覆盖了')
    assert.equal(fake.getItem(b!), 'BBB')
  })

  it('★★ 无内容的留证仍然不占位（连字符那条是既有行为）', () => {
    assert.equal(preserveCorruptRaw(AI_CHAT_CORRUPT_NS, 'settings', null), null)
    assert.equal(fake.keys().length, 0)
  })

  it('★★ 写不下时返回 null 而不是抛错（调用方要能对用户说话）', () => {
    fake.quotaBytes = 64
    assert.equal(preserveCorruptRaw(AI_CHAT_CORRUPT_NS, 'settings', 'x'.repeat(200)), null)
    fake.quotaBytes = null
  })
})

describe('§875 · 变异负控（行为判据必须能被变异打红）', () => {
  beforeEach(() => { fake.clear(); fake.quotaBytes = null; fake.failOnRemove = false })

  it('★★ 对照组：同一个度量方式，遇到「没淘汰」的存储必须报 10', () => {
    // ⚠ 行为判据必须证明它**有牙**：把同样的度量代码用在一份
    //   绕开 preserveCorruptRaw、直接写入的存储上，必须数出 10 份。
    //   否则「只剩 3 份」这条断言可能只是因为别的原因（比如根本没写进去）。
    fake.clear()
    for (let i = 0; i < 10; i++) {
      fake.setItem(`${AI_CHAT_CORRUPT_NS}:settings:local:1000-${i}`, `p-${i}`)
    }
    const remaining = fake.keys().filter((k) => k.startsWith(`${AI_CHAT_CORRUPT_NS}:settings:`))
    assert.equal(remaining.length, 10, '对照组都数不对 ⇒ 这把尺子没有牙')
  })

  it('让 removeItem 失败 ⇒ 行为判据能看出「额度一点没释放」', () => {
    for (let i = 0; i < 6; i++) preserveCorruptRaw(AI_CHAT_CORRUPT_NS, 'settings', `p-${i}`)
    const before = fake.keys().filter((k) => k.startsWith(`${AI_CHAT_CORRUPT_NS}:settings:`)).length
    fake.failOnRemove = true
    for (let i = 0; i < 3; i++) preserveCorruptRaw(AI_CHAT_CORRUPT_NS, 'settings', `q-${i}`)
    const after = fake.keys().filter((k) => k.startsWith(`${AI_CHAT_CORRUPT_NS}:settings:`)).length
    assert.ok(after >= before,
      'delete 失败时份数不应该凭空减少（索引前进但键还在，是已知的可接受退化）')
    fake.failOnRemove = false
  })
})

// ======================================================================================
// §873/D43 的形状判据（邮件正文缓存）—— 并入本文件
//
// ⚠ 并入原因同 §875：`npm run test:all` 的 argv 上限已经顶到 376 个文件。
//   「累加器的预算」本来就该在一个文件里：形状（有没有上限/淘汰）
//   + 行为（真跑一遍是不是真的守住）是一件事的两面。
// ======================================================================================

const LIMIT_CONSTS = ['MAX_ENTRIES', 'MAX_TOTAL_CHARS']
/** §873/D43 严重度的依据：点一封会预取后两封 ⇒ 一次点击缓存 3 封正文。 */
const PREFETCH = strip(read('features/email/email-body-prefetch.ts'))

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
