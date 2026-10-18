// local-storage-write-truthfulness.test.ts — §868：写失败时，**说出口的话必须是真的**。
//
// §866/§867 判的是「有没有 try」「有没有 warn」；本节判的是第三件事：
//   **失败分支的措辞与后果是否匹配**。
//
// 缺陷背景：
//   D36 flashcards 的两个写函数各带一句注释，而两句都只对一半的世界成立：
//     writeCache  「隐私模式 / 配额耗尽：缓存失败不应阻塞视图」
//     writeOutbox `/* ignore */`
//   writeOutbox 不是缓存，它是**离线队列**。失败链：
//     enqueue() 先塞内存 outbox → persistOutbox() 写不进去 →
//     `if (online) flushOutbox()` 也不报错 ⇒ 用户看到「已入队」；
//     **离线时**（正是 outbox 存在的唯一理由）它只活在内存 ⇒
//     下次 loadFromCache 读回旧队列 ⇒ **这条复习事件永久丢失**，
//     而 applyReviewLocally 已经把 FSRS 状态改进内存了。
//
// ⛔ 本判据只钉**性质**，不钉某一句文案：
//   「不得把队列的失败归因成缓存的失败」比「必须出现某个字符串」更耐改。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const HERE = dirname(fileURLToPath(import.meta.url))
const SRC = join(HERE, '..')
const read = (rel: string) => readFileSync(join(SRC, rel), 'utf8')

/** ⚠ 顺序即语义：HTML 注释必须**先**去掉（理由见 HUB 的注释）。 */
const strip = (t: string): string =>
  t.replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, ' '))
   .replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
   .replace(/(^|[^:])\/\/[^\n]*/g, '$1')

const CARDS = strip(read('stores/flashcards.ts'))
/**
 * ⚠⚠ strip 的顺序陷阱（本节踩到）：
 *   HTML 注释**必须先**去掉，再做块注释那一步。否则文件头注释里出现的
 *   字面路径（如 `StudyHubView.vue` 里的 `/api/learning/*`）会启动一个
 *   长达 10304 字符的「块注释」，把 `<template>` 一起吃掉
 *   —— 读数变成「模板里没有 persistFailed」，看起来像**代码没写**。
 *   ⚠ 因此本判据一律**先抽模板、再 strip**（与 §866 的判据同序）。
 */
const HUB = read('features/study/StudyHubView.vue')
const HUB_TPL = strip(/<template>([\s\S]*)<\/template>/.exec(HUB)?.[1] ?? '')

/** 取函数体：支持 `function 名(` 与对象方法简写 `名() {` 两种写法。 */
function fnBody(rawCode: string, name: string): string {
  const code = strip(rawCode)
  const m = new RegExp(
    `(?:^|[\\s{,])(?:async\\s+)?(?:function\\s+)?${name}\\s*(?:<[^>]*>)?\\s*\\(`,
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

/** 失败分支的措辞是否诚实：钉性质，不钉文案。 */
function failureIsTruthful(src: string, name: string, opts: { mustNotCite?: string[] } = {}) {
  const body = fnBody(src, name)
  if (!body) return { ok: false, why: `找不到 ${name}` }
  const catchAt = body.indexOf('catch')
  if (catchAt < 0) return { ok: false, why: `${name} 没有 catch` }
  const tail = body.slice(catchAt)
  if (!/return false/.test(tail)) return { ok: false, why: `${name} 的失败没有返回失败标记 ⇒ 调用方无从判断` }
  if (!/console\.warn/.test(tail)) return { ok: false, why: `${name} 的失败没有任何日志` }
  // ⚠ 本节的核心性质：写失败**不许**拿「缓存/不应阻塞视图」这类理由开脱
  for (const w of opts.mustNotCite ?? []) {
    if (new RegExp(w).test(tail)) {
      return { ok: false, why: `${name} 的失败用「${w.replace(/[.*+?^${}()|[\]\\]/g, '')}」开脱 —— 那是**别的**落点的理由` }
    }
  }
  if (/\/\* ignore \*\//.test(tail)) return { ok: false, why: `${name} 的失败写着 ignore ⇒ 它不是缓存，丢的是用户数据` }
  return { ok: true }
}

const ruler = (src: string) => ({
  cards: failureIsTruthful(src, 'writeCache'),
  outbox: failureIsTruthful(src, 'writeOutbox', { mustNotCite: ['ignore', '缓存', '不应阻塞'] }),
})

describe('§868 · 尺子自证', () => {
  it('★ 两个文件都读到了', () => {
    assert.ok(CARDS.includes('writeOutbox'), '没读到 flashcards.writeOutbox')
    assert.ok(HUB_TPL.includes('</template>') || HUB_TPL.length > 0, '没读到 StudyHubView 模板')
  })

  it('★ 先证明这把尺子在真源码上是绿的', () => {
    const v = ruler(CARDS)
    assert.ok(v.cards.ok, v.cards.why)
    assert.ok(v.outbox.ok, v.outbox.why)
  })
})

describe('§868 · D36 队列写失败必须与缓存写失败分开说', () => {
  it('★★★ 失败必须返回失败标记并留日志（两个写函数都要）', () => {
    const v = ruler(CARDS)
    assert.ok(v.cards.ok, v.cards.why)
    assert.ok(v.outbox.ok, v.outbox.why)
  })

  it('★★★ store 必须把失败置位并导出（否则视图拿不到）', () => {
    // ⚠ `ref` 赋值是 `x.value = true`，少写 `.value` 就是量具自己错了。
    assert.match(CARDS, /outboxPersistFailed(?:\.value)?\s*=\s*true/, 'outbox 写失败没有置位')
    assert.match(CARDS, /outboxPersistFailed(?:\.value)?\s*=\s*false/, 'outbox 写成功后不复位 ⇒ 一次偶发失败会永久挂着')
    assert.match(CARDS, /cachePersistFailed(?:\.value)?\s*=\s*true/, 'cache 写失败没有置位')
    // 取**最后一个** `  return {`（导出块），而不是第一个 return {
    const retAt = CARDS.lastIndexOf('  return {')
    assert.ok(retAt > 0, '找不到 store 的导出块')
    assert.match(
      CARDS.slice(retAt, retAt + 1200),
      /\n\s+persistFailed,/,
      'store 没导出 persistFailed ⇒ 视图无从判断',
    )
  })

  it('★★★ 复习入口必须有提示，且落在所有空态互斥链之外', () => {
    const at = HUB_TPL.indexOf('store.persistFailed')
    assert.ok(at > 0, '复习入口没有写失败提示的出口')
    const chainAt = HUB_TPL.indexOf('store.loading')
    assert.ok(chainAt > 0, '模板结构变了')
    assert.ok(at < chainAt, '提示落在加载/空态互斥链之内 ⇒ 下一次成功渲染会把它吞掉')
  })

  it('★★ 提示措辞不得暗示「已经保存」', () => {
    const notice = /v-if="store\.persistFailed"[\s\S]{0,400}?<\/p>/.exec(HUB_TPL)?.[0] ?? ''
    assert.ok(notice, '取不到提示块')
    assert.match(notice, /丢|没能|不会/, '提示没说清「会丢」')
    assert.doesNotMatch(notice, /已保存|保存成功|已存储/,
      '提示里出现了暗示「已经保存」的措辞 ⇒ 那正是本节要消灭的那种撒谎')
  })
})

describe('§868 · 变异负控', () => {
  it('把 outbox 的失败改回 `/* ignore */` ⇒ 第一组立刻红', () => {
    // ⚠⚠ 变异必须**只**打在 writeOutbox 上：writeCache 的 catch 长得一模一样
    //   （同样 `catch (err) { … return false }`），非全局 replace 会打在
    //   **前一个**函数上 ⇒ 被测函数原封不动 ⇒ 尺子判绿 ⇒ 「尺子坏了」，
    //   而读数看起来像「判据抓不到这个缺陷」。
    const body = fnBody(CARDS, 'writeOutbox')
    assert.ok(body, '取不到 writeOutbox 的函数体')
    const mutatedBody = body.replace(
      /catch \(err\) \{[\s\S]*?\n    return false\n  \}/,
      'catch {\n    /* ignore */\n  }',
    )
    assert.notEqual(mutatedBody, body, '变异没生效（锚点与真源码不匹配）')
    const mutated = CARDS.replace(body, mutatedBody)
    const v = ruler(mutated)
    assert.equal(v.outbox.ok, false, '尺子坏了：把缺陷形态喂回去竟然还判绿')
  })

  it('把提示挪进加载互斥链之内 ⇒ 第三组立刻红', () => {
    const block = /<p\n      v-if="store\.persistFailed"[\s\S]*?<\/p>/.exec(HUB_TPL)?.[0] ?? ''
    assert.ok(block, '取不到提示块')
    // ⚠ 变异方向：必须插到 `v-if="store.loading"` 的**后面**（即落在互斥链之内）。
    //   第一版写成插在它**前面** ⇒ at < chainAt 依然成立 ⇒ 尺子判绿。
    //   第二版忘了**先摘掉原块** ⇒ 变成「复制」，`indexOf` 先命中顶部那块
    //   ⇒ 同样判绿。⇒ 负控必须是**移动**，不是新增。
    const anchor = '<div v-if="store.loading" class="state">'
    assert.ok(HUB_TPL.includes(anchor), '变异锚点与真源码不匹配')
    const withoutBlock = HUB_TPL.replace(block, '')
    assert.ok(!withoutBlock.includes('store.persistFailed'), '原块没摘干净')
    const mutated = withoutBlock.replace(anchor, `${anchor}\n${block}`)
    assert.notEqual(mutated, HUB_TPL, '变异没生效')
    const at = mutated.indexOf('store.persistFailed')
    const chainAt = mutated.indexOf('store.loading')
    assert.ok(at > chainAt, '尺子坏了：挪进互斥链之后仍然判绿')
  })
})
