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
// ★ 读入口归一 CRLF → LF（2026-10-22 Windows 实测修掉）
//
//   本仓的 .ts/.vue 在 Windows 检出是 CRLF（core.autocrlf=true），
//   而下面这些用例的「变异锚点」正则里写死了 \n ⇒ 锚点匹配不上
//   ⇒ 变异根本没发生 ⇒ 断言报「变异没生效（锚点与真源码不匹配）」
//   ⇒ 该报红的那条反而报红成「锚点没命中」，看起来像源码变了，其实没变。
//   ⇒ Linux 上源文件是 LF ⇒ 锚点命中 ⇒ **Linux 一直绿**。
//
//   归一在**读入口**（一处盖住全部锚点），不是逐条改正则
//   —— 与 D6–D10、§88 同族纪律，也与仓内既有先例一致：
//   src/features/email/__tests__/account-read-path-normalization.test.mjs:46-48。

const read = (rel: string) => readFileSync(join(SRC, rel), 'utf8').replace(/\r\n/g, '\n')

/** ⚠ 顺序即语义：HTML 注释必须**先**去掉（理由见 HUB 的注释）。 */
const strip = (t: string): string =>
  t.replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, ' '))
   .replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
   .replace(/(^|[^:])\/\/[^\n]*/g, '$1')

const CARDS = strip(read('stores/flashcards.ts'))
const APPLY = strip(read('native/config-sync/apply.ts'))
const RUNTIME = strip(read('native/config-sync/runtime.ts'))
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

/**
 * 取函数体：支持 `function 名(` 与对象方法简写 `名() {` 两种写法。
 * ⚠⚠ 取件器翻车记录（本会话累计第 4 次）：
 *   ① 只认 `function 名(`，认不出 Pinia actions 的方法简写 `名() {`；
 *   ② 前置字符限定在 行首/空白/`{`/`,` **仍然不够** ——
 *      `return applyChatSettings(...)` 前面就是一个空格，
 *      而**调用点出现在定义之前**（applyRemoteSetting 在文件上方）
 *      ⇒ fnBody 抓到了调用点、返回了它所在的函数体 ⇒ 判据报「找不到 catch」。
 * ⇒ 修法：把匹配**锚到行首**（可有缩进）。这同时挡掉 `this.名(`、
 *   `return 名(`、`await 名(` 等一切调用点。
 */
function fnBody(rawCode: string, name: string): string {
  const code = strip(rawCode)
  const m = new RegExp(
    `(?:^|\\n)\\s*(?:async\\s+)?(?:function\\s+)?${name}\\s*(?:<[^>]*>)?\\s*\\(`,
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

/**
 * §869：远端配置下发的**写侧**。
 *
 * 结构前提（取证所得，必须先知道才能判断对错）：
 *   `writeLocalIfNewer` 把记录写进 **SQLite**（`local_user_settings`，dirty = 0），
 *   而 `applyRemoteSetting` 把它投影到 **localStorage**。
 *   ⇒ 两套存储。投影写失败时 DB 仍被标记为「已是最新」⇒ **永不重试**。
 *
 * D37 applyChatSettings 的写侧 catch 是 `// ignore` —— 它守着的正是
 *      §859/D24 刚修好的那份数据（读侧已会留证，写侧却静默丢弃，两头不对称）。
 * D38 applyAppPrefs 用一个**大 try** 包住全部字段 ⇒ 第 1 个字段写失败，
 *      后面两个写入与 `<html data-theme>` 更新**全都不会执行**（连坐），
 *      而 catch 里只有一句「localStorage may be unavailable」，零可见性。
 */

/** D37：写失败不许是 `// ignore`，且必须说清丢的是什么。 */
function remoteChatWriteTells(src: string) {
  const body = fnBody(src, 'applyChatSettings')
  if (!body) return { ok: false, why: '找不到 applyChatSettings' }
  const catchAt = body.indexOf('catch')
  if (catchAt < 0) return { ok: false, why: 'applyChatSettings 没有 catch' }
  const tail = body.slice(catchAt)
  if (/ignore/i.test(tail)) return { ok: false, why: '写失败仍写着 ignore ⇒ 远端 AI 对话设置被静默丢弃' }
  if (!/console\.warn/.test(tail)) return { ok: false, why: '写失败没有任何日志 ⇒ 事后无法判断这条路是否已被关掉' }
  if (!/return false/.test(tail)) return { ok: false, why: '写失败没有返回失败标记 ⇒ 调用方无从判断' }
  return { ok: true }
}

/**
 * D38：**任一写入失败不得拖累其余写入**。
 *
 * ⛔ 机械性质的写法比想象的难选：第一版查「相邻两个 setItem 之间有没有 try」，
 *   旧代码读数确实是 0，但那只在**所有写入都是裸的**时候成立；
 *   一旦重构出一个 `persist()` 辅助函数，它自己的 try 就落在两个裸写入**之前**，
 *   于是「连坐形态」照样判绿 —— 量具坏了。已换成直接数：
 *   **任何一个 try 块里不得同时出现两个及以上写入点。**
 *
 * 用 §867 探针验证过的花括号栈（从函数体开头一路维护），
 * 不做任何「找函数边界」之外的定位。
 */
function writesAreIndependent(src: string) {
  const body = fnBody(src, 'applyAppPrefs')
  if (!body) return { ok: false, why: '找不到 applyAppPrefs' }
  const WRITE = 'localStorage.setItem'
  if (!body.includes(WRITE)) return { ok: false, why: 'applyAppPrefs 里找不到写入点' }
  const stack: { kind: string; writes: number }[] = []
  for (let i = 0; i < body.length; i++) {
    const ch = body[i]
    if (ch === '{') {
      const kind = /\btry\s*$/.test(body.slice(0, i)) ? 'try' : 'block'
      stack.push({ kind, writes: 0 })
      continue
    }
    if (ch === '}' && stack.length) {
      const popped = stack.pop()
      if (popped && popped.kind === 'try' && popped.writes >= 2) {
        return { ok: false, why: `有一个 try 块里同时包了 ${popped.writes} 个写入 ⇒ 一次失败会连坐其余写入（§869/D38）` }
      }
      continue
    }
    if (body.startsWith(WRITE, i)) {
      for (const frame of stack) if (frame.kind === 'try') frame.writes++
      i += WRITE.length - 1
    }
  }
  return { ok: true }
}

/** D38：失败的字段必须回报给调用方（否则「永不重试」这件事连日志都没有）。 */
function applyReportsFailures(src: string, runtimeSrc: string) {
  const sig = /export function applyRemoteSetting\([^)]*\)\s*:\s*string\[\]/.exec(src)
  if (!sig) return { ok: false, why: 'applyRemoteSetting 没有返回失败字段名 ⇒ 调用方无从知道远端下发是否落地' }
  const body = fnBody(src, 'applyAppPrefs')
  if (!body) return { ok: false, why: '找不到 applyAppPrefs' }
  if (!/failed\.push/.test(body)) return { ok: false, why: 'applyAppPrefs 没有收集失败的字段名' }
  // 调用方必须消费返回值并留痕
  if (!/const failed = await applyRemoteSetting/.test(runtimeSrc)) {
    return { ok: false, why: 'runtime 的 pull 路径没有接住返回值' }
  }
  if (!/failed\.length > 0/.test(runtimeSrc) || !/console\.warn/.test(runtimeSrc)) {
    return { ok: false, why: 'runtime 没有为「远端设置未完全落地」留任何日志' }
  }
  return { ok: true }
}

const ruler869 = (src: string, rt: string) => ({
  chat: remoteChatWriteTells(src),
  indep: writesAreIndependent(src),
  report: applyReportsFailures(src, rt),
})

describe('§869 · D37/D38 远端配置下发的写侧必须说清「没生效」', () => {
  it('★★ 先证明这把尺子在真源码上是绿的', () => {
    const v = ruler869(APPLY, RUNTIME)
    assert.ok(v.chat.ok, v.chat.why)
    assert.ok(v.indep.ok, v.indep.why)
    assert.ok(v.report.ok, v.report.why)
  })

  it('★★★ 写失败不许是 ignore（D37）', () => {
    const v = ruler869(APPLY, RUNTIME)
    assert.ok(v.chat.ok, v.chat.why)
  })

  it('★★★ 一个字段写失败不得连坐其余写入（D38）', () => {
    const v = ruler869(APPLY, RUNTIME)
    assert.ok(v.indep.ok, v.indep.why)
  })

  it('★★★ 未落地必须回报并留痕（否则 DB 已标干净、投影永不重试）', () => {
    const v = ruler869(APPLY, RUNTIME)
    assert.ok(v.report.ok, v.report.why)
  })

  it('把 chat 设置的写失败改回 `// ignore` ⇒ D37 立刻红', () => {
    const body = fnBody(APPLY, 'applyChatSettings')
    assert.ok(body, '取不到 applyChatSettings 的函数体')
    const mutatedBody = body.replace(/catch \(err\) \{[\s\S]*?\n    return false\n  \}/, 'catch {\n    // ignore\n  }')
    assert.notEqual(mutatedBody, body, '变异没生效（锚点与真源码不匹配）')
    const mutated = APPLY.replace(body, mutatedBody)
    assert.equal(remoteChatWriteTells(mutated).ok, false, '尺子坏了：把缺陷形态喂回去竟然还判绿')
  })

  it('把 app_prefs 改回「一个大 try 包全部字段」⇒ D38 立刻红', () => {
    // ⚠⚠ 这一条我写错过两版，两版都是**变异没做到它声称的动作**，于是
    //   尺子判绿、报错却说「尺子坏了」——反过来冤枉了量具：
    //   ① 第一版：只把两个 persist 调用换成裸 setItem ⇒ 它们并不在**同一个**
    //      try 里，所以「一个 try 包多个写入」这个性质根本没被触发；
    //   ② 第二版：查「相邻写入之间有没有 try」，读数在重构出 persist() 后
    //      就失去意义了。
    // ⇒ 现在直接把函数体换成**真实的缺陷形态**（一个大 try 包全部写入）。
    const body = fnBody(APPLY, 'applyAppPrefs')
    assert.ok(body, '取不到 applyAppPrefs 的函数体')
    const defective = body.replace(
      /const failed[\s\S]*$/,
      `try {
    if (theme) localStorage.setItem('app_theme', theme)
    if (prefs.locale) applyRemoteLocale(prefs.locale)
    if (deviceTier) localStorage.setItem('pocket_device_tier', deviceTier)
    if (sttPref) localStorage.setItem('pocket_stt_pref', sttPref)
    if (theme && typeof document !== 'undefined') {
      const root = document.documentElement
      if (theme === 'system') root.removeAttribute('data-theme')
      else root.setAttribute('data-theme', theme)
    }
  } catch {
    // localStorage may be unavailable
  }
  return failed
}`,
    )
    assert.notEqual(defective, body, '变异没生效（锚点与真源码不匹配）')
    assert.equal(writesAreIndependent(APPLY.replace(body, defective)).ok, false,
      '尺子坏了：改回连坐形态后仍然判绿')
  })
})
