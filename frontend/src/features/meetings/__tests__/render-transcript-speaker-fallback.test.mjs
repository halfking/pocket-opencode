// render-transcript-speaker-fallback.test.mjs — §427：说话人必须有兜底。
//
// ── 缺陷 ──
//
// `renderTranscript`（meeting-dedup.ts）此前是全仓**唯一**没有说话人兜底的
// transcript 渲染出口：
//
//     lines.push(`[${s.speakerLabel}] ${step.text}`)      // 无兜底
//
// 实跑（修复前）：
//
//     renderTranscript  → [张伟] 今天… / [null] 整段重转的内容 / [undefined] 未标…
//     另外两个出口      → [张伟] 今天… / [说话人] 整段重转的内容 / [说话人] 未标…
//
// `speakerLabel` 为 null **真会出现**：`buildRefineBaseSegments` 的第三条路径
// （采纳整段重转但说话人挂不回去）显式造 `speakerLabel: null`
// （meeting-final-transcript.ts:399）。而本函数有 5 个出口：
//
//   ① `local_meetings.transcript` 派生列 —— **用户可见**
//   ② ③ api/meetings.ts 的 fallbackSummarize / fallbackRecommend 提示词
//   ④ ⑤ fallbackRefine 提示词（§54 说它是**高频**路径：网关超时必走）
//
// ⇒ 字面量 `null` / `undefined` 会进用户看到的转写，也会进提示词
//   （模型会被告知说话人字面叫 "null"）。
//
// ── 为什么不是「再加一处兜底」，而是消除第二份实现 ──
//
// 漂移的根因是**同一个量在两处各算一遍**：Go `segmentsToText` 有兜底、
// `MeetingDetailView.onSummarize` 那份手搓 join 有兜底、只有真身没有。
// ⇒ 本轮既给真身补上兜底，也把那份手搓 join 改成直接调用真身。
//
// ── 既有门覆盖到哪（先查过，避免重复门）──
//
// `render-transcript-manual-origin.test.mjs`（§401）在**去重**那一维很完整
// （人工段豁免 / ASR 仍被裁 / 两出口逐字一致 / origin 形态自证，共 7 条），
// 但它的所有夹具 speakerLabel 都是 '李四'/'我' —— **说话人这一维零覆盖**。
// ⇒ 是真缺口，不是重复门。

import { test, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

import { renderTranscript } from '../meeting-dedup.ts'
import { renderImpl } from './_render-impl.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const DEDUP_SRC = join(here, '..', 'meeting-dedup.ts')
const VIEW_SRC = join(here, '..', 'MeetingDetailView.vue')
const GO_SRC = join(here, '..', '..', '..', '..', '..', 'backend', 'internal', 'server', 'server_meeting.go')

/** 兜底词沿用本仓另外两处已经在用的那个，不新造。 */
/**
 * 按「先配平参数表、再配平花括号」取函数体。
 *
 * ⚠ 第一版直接 `indexOf('{', i)` ⇒ 踩到 `Array<{...}>` 里的那个 `{`
 *   ⇒ 取出来的「函数体」根本不是函数体（判据自证失败，查出来的）。
 *   TS 的签名是多行的、类型里可能带花括号，所以必须先配平参数表。
 */
function funcBody(src, marker) {
  const i = src.indexOf(marker)
  if (i < 0) return null
  const p = src.indexOf('(', i)
  if (p < 0) return null
  let paren = 0
  let afterParams = -1
  for (let k = p; k < src.length; k++) {
    if (src[k] === '(') paren++
    else if (src[k] === ')') {
      paren--
      if (paren === 0) { afterParams = k + 1; break }
    }
  }
  if (afterParams < 0) return null
  const open = src.indexOf('{', afterParams)
  if (open < 0) return null
  let depth = 0
  for (let k = open; k < src.length; k++) {
    if (src[k] === '{') depth++
    else if (src[k] === '}') {
      depth--
      if (depth === 0) return src.slice(open, k + 1)
    }
  }
  return null
}

const stripComments = (t) => t
  .replace(/\/\*[\s\S]*?\*\//g, '')
  .split('\n').map((l) => { const c = l.indexOf('//'); return c < 0 ? l : l.slice(0, c) }).join('\n')

const FALLBACK = '说话人'

const seg = (speakerLabel, text, o = {}) => ({
  speakerLabel,
  text,
  startMs: o.startMs ?? 0,
  endMs: o.endMs ?? 1000,
  origin: o.origin ?? 'asr',
})

describe('§427 · renderTranscript 的说话人兜底', () => {
  it('#1 三种「没有说话人」都必须兜底（null / undefined / 空串）', () => {
    const out = renderTranscript([
      seg(null, '整段重转的内容'),
      seg(undefined, '未标说话人的段', { startMs: 1000, endMs: 2000 }),
      seg('', '空串说话人', { startMs: 2000, endMs: 3000 }),
    ])
    for (const bad of ['null', 'undefined']) {
      assert.ok(
        !out.includes(`[${bad}]`),
        `输出里出现了字面量 [${bad}] —— 这正是修复前的实测读数。输出：\n${out}`,
      )
    }
    assert.equal(out.split('\n').length, 3, `有段被吞了（兜底不该影响裁剪）：\n${out}`)
    for (const line of out.split('\n')) {
      assert.ok(line.startsWith(`[${FALLBACK}] `), `兜底词不一致：${line}`)
    }
  })

  it('#2 纯空白也要兜底（trim 后为空）', () => {
    const out = renderTranscript([seg('   ', '空白说话人')])
    assert.equal(out, `[${FALLBACK}] 空白说话人`)
  })

  it('#3 ★ 阴性对照：真名必须原样保留（否则「永远兜底」也能满足 #1）', () => {
    const out = renderTranscript([
      seg('张伟', '今天下午三点开评审会'),
      seg('我', '我来做对比表', { startMs: 1000, endMs: 2000 }),
      seg(' 王总 ', '王总拍板', { startMs: 2000, endMs: 3000 }),
    ])
    assert.ok(out.includes('[张伟] '), `真名被吞了：\n${out}`)
    assert.ok(out.includes('[我] '), `真名被吞了：\n${out}`)
    // 兜底只对「空」生效，不该顺手 trim 掉真名两边的空白。
    assert.ok(out.includes('[ 王总 ] '), `真名被 trim 了：\n${out}`)
    assert.ok(!out.includes(FALLBACK), `不该出现兜底词：\n${out}`)
  })

  it('#4 前端只剩一份实现：onSummarize 不再手搓 join', () => {
    const src = readFileSync(VIEW_SRC, 'utf8')
    const i = src.indexOf('async function onSummarize')
    assert.ok(i > 0, '找不到 onSummarize')
    // ⚠ 不用魔法窗口取函数体（§426.7 的同款雷）：按下一个顶层声明切。
    const rest = src.slice(i)
    const j = rest.search(/\n(?:async function|function|const \w+ = (?:async )?\()/)
    const fn = j > 0 ? rest.slice(0, j) : rest
    assert.ok(
      fn.includes('renderTranscript('),
      'onSummarize 没有调用 renderTranscript —— transcript 又多了一份实现，兜底就会再漂一次',
    )
    assert.ok(
      !/segments\s*(?:\.value|\w*)\s*(?:\.map|\.flatMap)?\s*[\(\.]/.test(fn)
        || !fn.includes('speakerLabel'),
      'onSummarize 里仍有手搓的 `[${speakerLabel} …]` 拼接 —— 必须复用真身',
    )
  })

  it('#5 ★ 跨语言对齐：Go 的 segmentsToText 必须用同一个兜底词', () => {
    const go = readFileSync(GO_SRC, 'utf8')
    const body = funcBody(go, 'func segmentsToText')
    assert.ok(body && body.length > 50, `取不到 segmentsToText 的函数体（${body?.length ?? 0} 字）`)
    assert.ok(
      body.includes(`"${FALLBACK}"`),
      `Go 侧没有用同一个兜底词「${FALLBACK}」—— 同一份转写在前后端会显示成两个说话人`,
    )
  })

  it('#6 形态自证：兜底确实长在**渲染实现**里', () => {
    const src = readFileSync(DEDUP_SRC, 'utf8')
    // ⚠ §931：渲染实现被重构成「状态机 + 薄包装」⇒ 兜底词落在
    //   `speakerPrefix` 与 `TranscriptRenderState.append` 里，而
    //   `renderTranscript` 本身只剩三行包装 ⇒ **只改指向，不动语义**：
    //   扫的区间从「renderTranscript 的函数体」改成「**整个渲染实现**」
    //   （共享提取器见 `_render-impl.mjs` ⇒ 抄两份必然漂移）。
    // ⚠ 语义**没变**：问的仍然是「兜底词在不在**会产出正文的那段代码**里」。
    const body = renderImpl(src)
    assert.ok(body && body.length > 200, `取不到渲染实现的区间（${body?.length ?? 0} 字）`)
    // ⚠ 先剥注释再查：修复时写的解释注释里**逐字包含** `[${s.speakerLabel}]`。
    const code = stripComments(body)
    assert.ok(
      code.includes(`'${FALLBACK}'`),
      '渲染实现里找不到兜底词 —— 判据量的是代码不是注释',
    )
    assert.ok(
      !/`\[\$\{s\.speakerLabel\}\]/.test(code),
      '代码里仍有无兜底的 `[${s.speakerLabel}]` 拼接（注释不算）',
    )
    // ★ 负控：把「无兜底」那种写法塞回去 ⇒ 上面那条正则**必须**能抓住它
    //   （否则这道门是恒绿的 —— §911 的教训）。
    const bad = code + '\nlines.push(`[${s.speakerLabel}] ${step.text}`)\n'
    assert.ok(
      /`\[\$\{s\.speakerLabel\}\]/.test(bad),
      '负控前提失效：塞回去的无兜底写法没被上面那条正则捕获 ⇒ 这道门没有牙',
    )
  })
})