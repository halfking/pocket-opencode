/**
 * truncateCodeUnits 的调用点判据（§259）。
 *
 * §251/§252 造好了 helper（`src/utils/text.ts`，中立层），本文件钉的是
 * **谁在调它**。§257 M4 与 §258 M1-M6 的教训是同一个：
 * helper 本身被测 ≠ 调用点被测；§258 的 6 条变异里调用点全部改回字节切片，
 * 而包内判据 6/6 全绿。
 *
 * 三层：
 *   ① helper 本身的行为（纯函数）；
 *   ② **真跑** `relatedQueryFromTranscript` / `wikipediaSearchUrl`，语料真能触发切点；
 *   ③ `NoteCard.vue` 只能停在源码级——Vue SFC 没法被 node 直接 import，
 *      这一点必须写明，否则下一个人会以为这条也是行为级的。
 *
 * Run: node --test src/utils/__tests__/truncate-code-units-callsites.test.mjs
 */
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

import { relatedQueryFromTranscript, wikipediaSearchUrl } from '../../features/meetings/meeting-related.ts'
import { truncateCodeUnits } from '../text.ts'

const NOTE_CARD = new URL('../../components/business/NoteCard.vue', import.meta.url)

/** 末尾是**落单高位代理**——`slice(0, n)` 唯一能造出来的脏尾巴。 */
function endsWithLoneHighSurrogate(s) {
  return s.length > 0 && /[\uD800-\uDBFF]$/.test(s)
}

/**
 * 字符串里是否存在**未配对**的代理单元。
 *
 * ⚠ 别用 `/[\uD800-\uDFFF]/` 判断：那个字符类匹配**所有**代理单元，
 * 而 emoji 本身就是代理对（U+1F525 占 D83D+DD25）⇒ 对任何正常 emoji 都会误报。
 * 这条误报是变异 M2 重跑时当场撞出来的（正确实现被判红）。
 */
function hasLoneSurrogate(s) {
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i)
    if (c >= 0xd800 && c <= 0xdbff) {
      const n = s.charCodeAt(i + 1)
      if (n < 0xdc00 || n > 0xdfff) return true
      i++
    } else if (c >= 0xdc00 && c <= 0xdfff) {
      return true
    }
  }
  return false
}

/**
 * 语料：**必须真的让切点落在代理对中间**，否则量的是「什么都没发生」
 *（§253 与 §258 各自踩过一次）。
 *
 * 构造：`'a' + '🔥'×20`，长度 41 码元。
 *   - 前置 1 个 ASCII 是为了**错开奇偶**：emoji 从下标 1 开始占 (1,2)(3,4)…(39,40)，
 *     于是下标 39 正好是**高位代理** ⇒ `slice(0,40)` 必然切开一个 emoji。
 *   - 只用 1 个 ASCII 是为了不触发 `/[A-Za-z][A-Za-z0-9]{1,}/`（要求 ≥2 个字母），
 *     于是 tokens 为空、`kept` 为空 ⇒ 走 `|| clause` 兜底，
 *     这正是**唯一**能把代理对带进这条路径的分支。
 *   - emoji 不匹配 `[\u4e00-\u9fff]{2,4}`，所以不会被 token 过滤器洗掉。
 */
const TRIGGER = 'a' + '🔥'.repeat(20)

describe('语料自证（防「判据恒绿」）', () => {
  it('TRIGGER 在 n=40 下确实会切出落单代理', () => {
    // 少了这一条，下面所有「不该以落单代理结尾」的断言都可能是因为
    // **语料根本没触发**而恒真——§253 与 §258 各踩过一次这个坑。
    assert.equal(TRIGGER.length, 41, `TRIGGER 应为 41 码元，实际 ${TRIGGER.length}`)
    assert.ok(
      endsWithLoneHighSurrogate(TRIGGER.slice(0, 40)),
      '语料+上限没切在代理对中间 ⇒ 本文件的行为级断言会全部恒绿',
    )
  })

  it('TRIGGER 确实会走 `|| clause` 兜底（tokens 为空）', () => {
    // 这条兜底是代理对唯一能进入 relatedQueryFromTranscript 的路径；
    // 若语料哪天被改成含 CJK/英文词，这里会先红，提示覆盖已失效。
    assert.equal(
      /[A-Za-z][A-Za-z0-9]{1,}|[\u4e00-\u9fff]{2,4}/.exec(TRIGGER),
      null,
      'TRIGGER 不再是无 token 的纯 emoji 串 ⇒ 兜底分支不再被覆盖',
    )
  })
})

describe('truncateCodeUnits 本身', () => {
  it('切在代理对中间时丢掉那个落单高位代理', () => {
    const cut = truncateCodeUnits(TRIGGER, 40)
    assert.equal(cut.length, 39, '应回退 1 个码元')
    assert.ok(!endsWithLoneHighSurrogate(cut), `不该以落单代理结尾：${JSON.stringify(cut)}`)
  })

  it('边界正好时不回退（良性改动不能被误伤）', () => {
    const even = '🔥'.repeat(20) // 40 码元，切点刚好在代理对之间
    assert.equal(truncateCodeUnits(even, 40), even)
  })

  it('短于上限时原样返回', () => {
    assert.equal(truncateCodeUnits('abc', 40), 'abc')
    assert.equal(truncateCodeUnits('', 40), '')
  })

  it('ASCII 不受影响（§245 【C】那条性质）', () => {
    assert.equal(truncateCodeUnits('x'.repeat(50), 10), 'x'.repeat(10))
  })
})

describe('调用点：relatedQueryFromTranscript（产物会进 URL 查询串）', () => {
  it('兜底分支上的代理对不会漏到结果里', () => {
    const got = relatedQueryFromTranscript([TRIGGER])
    assert.ok(got.length > 0, '语料没走到兜底分支就会让这条判据恒绿，必须防')
    assert.ok(
      !endsWithLoneHighSurrogate(got),
      `结果以落单代理结尾 ⇒ 进 URLSearchParams 会被静默换成 U+FFFD：${JSON.stringify(got)}`,
    )
  })

  it('既有语义没被改坏（正常中文会议转写仍然取 token）', () => {
    assert.equal(
      relatedQueryFromTranscript(['今天评审第三季度预算，李四下周提交方案。']),
      '第三季度 预算',
    )
    assert.equal(relatedQueryFromTranscript([]), '')
  })
})

describe('调用点：wikipediaSearchUrl', () => {
  it('解码回来的 search 参数既不含 U+FFFD 也不含落单代理', () => {
    const url = wikipediaSearchUrl(TRIGGER)

    // ⚠ 这里踩过一次恒真判据：第一版断言「URL 里不含 %ED%A0%BD」，
    //   而 URLSearchParams 会**先**把落单代理静默换成 %EF%BF%BD（§258.1 实测）
    //   ⇒ 那个字节序列**永远不会出现**，断言恒真。
    //   第二版断言「不含两个连续的 %EF%BF%BD」，而落单代理只产生**一个**
    //   ⇒ 同样恒真。变异 M2 两条都放行，当场把它咬了出来。
    // 正确的观测量是**解码后的语义**：search 参数里不许出现替换字符或代理对。
    const search = new URL(url).searchParams.get('search')
    assert.ok(!search.includes('�'),
      `search 参数被静默替换成 U+FFFD（查询已失真）：${encodeURIComponent(search)}`)
    assert.ok(!hasLoneSurrogate(search),
      `search 参数含未配对代理：${encodeURIComponent(search)}`)
    // 反证：这份语料在正确实现下**应该**保留下 19 个完整 emoji
    assert.equal(search, TRIGGER.slice(0, 39), '正确实现应回退 1 个码元后原样保留其余 emoji')
  })

  it('空输入仍返回空串', () => {
    assert.equal(wikipediaSearchUrl('  '), '')
  })
})

describe('调用点：NoteCard.vue（只能停在源码级）', () => {
  const src = readFileSync(fileURLToPath(NOTE_CARD), 'utf8')

  it('先自证锚点还在——否则下面那条会因文件变形而假绿', () => {
    assert.match(src, /const truncatedContent = computed\(/, 'NoteCard.vue 结构变了，本判据的锚点失效')
    assert.match(src, /const maxLength = 120/, 'maxLength 不在 120，本判据的前提失效')
  })

  it('卡片预览不再用 substring 按码元硬切', () => {
    assert.ok(!/truncatedContent[\s\S]{0,400}?\.substring\(0,/.test(src),
      'NoteCard 的预览截断又变回 substring(0, N) 了')
    assert.match(src, /truncateCodeUnits\(props\.note\.content, maxLength\)/,
      'NoteCard 的预览截断没走 truncateCodeUnits')
  })
})