// §401 — renderTranscript 的人工段豁免（origin）
//
// 缺陷：`MeetingSegment.origin` 是**必填**字段，`dedupeSegments` 认它，
// 而 `renderTranscript` 的入参类型里结构性没有它 ⇒ 人工段在 7 个出口被当成
// 「同音频的重转写」裁掉，而那 7 个出口正是 `local_meetings.transcript`
// 派生列与 `api/meetings.ts` 的 `fallbackRefine` prompt。
//
// ⭐ 界面上看不见：`MeetingDetailView.displaySegments` 走的是 `dedupeSegments`
//   ⇒ 分段列表完整。被裁的是喂 LLM 的那份，且 `fallbackRefine` 的注释自己
//   写着「这条路径的产出质量直接决定用户看到什么」。
//
// 判据分两层（缺一不可）：
//   行为层 —— 钉住「人工段一字不丢」+「ASR 段仍要被裁」+「两出口逐字一致」。
//             只钉前者的话，「整个函数放弃去重」也能满足。
//   形态层 —— 读**生产源码**确认 renderTranscript 体内真的引用了 origin。
//             ⚠ 上一轮 (§400) 的教训：反向保护若在测试里自己重算逻辑，
//               测的是测试自己，功能被整个关掉时照样绿。
import { test, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

import { renderTranscript, dedupeSegments } from '../meeting-dedup.ts'
import { renderImpl } from './_render-impl.mjs'

const here = dirname(fileURLToPath(import.meta.url))
// 本文件在 __tests__/ 下，被测模块与 api 都在上两级。
const DEDUP_SRC = join(here, '..', 'meeting-dedup.ts')
const API_SRC = join(here, '..', '..', '..', 'api', 'meetings.ts')

// 场景取自 meeting-utterance.ts:39-40 注释里记录的**真实读数**
// （「用户用来改正的相邻句子…裁掉尾巴（实测 张伟下周三交对比表 → 伟）」），
// 不是为触发缺陷而构造的数据：用户手敲的正是上一段的重复前缀，这很常见。
const MANUAL_TAIL = '张伟下周三交对比表'
const ASR_HEAD = '张伟'

const asr = (text, o = {}) => ({ speakerLabel: '李四', text, startMs: 0, endMs: 1000, origin: 'asr', ...o })
const manual = (text, o = {}) => ({ speakerLabel: '我', text, startMs: 1200, endMs: 4000, origin: 'manual', ...o })

describe('§401 · renderTranscript 人工段豁免', () => {
  it('#1 人工段在 renderTranscript 里一字不丢（修复前实测被裁成「下周三交对比表」）', () => {
    const out = renderTranscript([asr(ASR_HEAD), manual(MANUAL_TAIL)])
    assert.ok(
      out.includes(MANUAL_TAIL),
      `人工段被裁了。\n输入末段：${MANUAL_TAIL}\nrenderTranscript 输出：${JSON.stringify(out)}\n` +
      '⇒ origin 豁免没生效',
    )
  })

  it('#2 同一个输入，两个出口逐字一致（dedupeSegments 一直是对的，renderTranscript 曾经不是）', () => {
    const segs = [asr(ASR_HEAD), manual(MANUAL_TAIL)]
    const fromLLM = dedupeSegments(segs).map((s) => s.text).join('')
    const fromRender = renderTranscript(segs)
      .split('\n')
      .map((l) => l.replace(/^\[[^\]]*\] /, ''))
      .join('')
    assert.equal(fromRender, fromLLM, `两出口口径不一致：\nrender=${fromRender}\nllm   =${fromLLM}`)
  })

  it('#3 阴性对照：ASR 段仍然要被裁（否则「整个函数放弃去重」也能满足 #1）', () => {
    const out = renderTranscript([
      asr(ASR_HEAD, { endMs: 4000 }),
      // 时间轴上一段结束晚于下一段起点 ⇒ 确认重叠 ⇒ 该裁
      asr(MANUAL_TAIL, { startMs: 3000, endMs: 5000 }),
    ])
    assert.ok(
      !out.includes(MANUAL_TAIL),
      `ASR 段没有被去重——去重功能整体失效了。输出：${JSON.stringify(out)}`,
    )
  })

  it('#4 阴性对照：origin 缺省按 asr 处理（不得因为字段可选就把未知当人工段放过）', () => {
    const withOrigin = renderTranscript([
      { speakerLabel: 'A', text: ASR_HEAD, startMs: 0, endMs: 4000 },
      { speakerLabel: 'A', text: MANUAL_TAIL, startMs: 3000, endMs: 5000, origin: 'asr' },
    ])
    const withoutOrigin = renderTranscript([
      { speakerLabel: 'A', text: ASR_HEAD, startMs: 0, endMs: 4000 },
      { speakerLabel: 'A', text: MANUAL_TAIL, startMs: 3000, endMs: 5000 },
    ])
    assert.equal(withoutOrigin, withOrigin, 'origin 缺省与显式 asr 行为不一致')
    assert.ok(!withoutOrigin.includes(MANUAL_TAIL), 'origin 缺省时被当成了 manual')
  })
})

describe('§401 · 形态层（读生产源码，不在测试里重算）', () => {
  const src = readFileSync(DEDUP_SRC, 'utf8')

  it('#5 渲染实现里真的把 origin 传给了 dedupeStep', () => {
    // ⚠ §931：渲染实现被重构成「**状态机 + 薄包装**」⇒ origin 的传递
    //   落在 `TranscriptRenderState.append` 里，而 `renderTranscript` 本身
    //   只剩三行包装 ⇒ ⇒ **只改指向、不动语义**：扫的区间从
    //   「renderTranscript 的函数体」改成「**整个渲染实现**」
    //   （共享提取器 `_render-impl.mjs` ⇒ 抄两份必然漂移）。
    // ⚠ 语义**没变**：问的仍然是「origin 有没有被真的接上去」。
    // ⚠⚠ 区间**必须排除** `dedupeSegments`（它在本文件更靠前）⇒
    //   否则这条门会命中「那处本来就对」的 origin ⇒ **失去区分力**。
    const body = renderImpl(src)
    assert.ok(body && body.length > 200, `取不到渲染实现的区间（${body?.length ?? 0} 字）`)
    assert.match(
      body,
      /origin/,
      '渲染实现里没有任何 origin 引用 ⇒ 豁免没接上（#1 之所以能红是因为它）',
    )
    assert.match(
      body,
      /manual\s*:\s*s\.origin\s*===\s*['"]manual['"]/,
      '渲染实现没把 origin===\'manual\' 传给 dedupeStep 的 manual 入参',
    )
    // ★ 负控：把 manual 那行改掉 ⇒ 上面那条**必须**判红（否则这道门恒绿）。
    const bad = body.replace(
      /manual\s*:\s*s\.origin\s*===\s*['"]manual['"]/,
      'manual: false',
    )
    assert.notEqual(bad, body, '变异没落盘 ⇒ 这条负控不算数')
    assert.ok(
      !/manual\s*:\s*s\.origin\s*===\s*['"]manual['"]/.test(bad),
      '负控前提失效：改掉 manual 之后正则仍然匹配 ⇒ 这道门没有牙',
    )
  })

  it('#6 入参类型声明了 origin（否则调用方传了也被结构性丢弃）', () => {
    // ⚠ §931：入参类型现在叫 `TranscriptSegmentLike` —— 抽出来是为了让
    //   「整场渲染」与「增量渲染」**共用**同一份类型（§3 的教训：抄两份必然漂）。
    //   ⇒ 判据改为两件事**一起**判：
    //     ① 类型里**有** origin 字段；
    //     ② `renderTranscript` 的入参**用的就是它**
    //        （否则类型改了、函数没接上 ⇒ 又是「看似有、实则丢」）。
    const body = renderImpl(src)
    const typeAt = body.indexOf('export interface TranscriptSegmentLike')
    // ⚠⚠ §931：这里用 `>= 0`（**不是** `> 0`）—— 区间**就是**从这一行开始
    //   ⇒ 下标是 0 ⇒ 用 `> 0` 会把「找得到」误判成「找不到」。
    //   ⚠ 这**正是** `meeting-dedup.test.ts` 的 L 组早就写下的那条：
    //   「判据用 `at >= 0`（不是 `> 0`）：函数在下标 0 处也是找得到」。
    //   ⇒ 我在本节又踩了一次 ⇒ 登记为「已知的坑，写在注释里也会被踩」。
    assert.ok(typeAt >= 0, '找不到 TranscriptSegmentLike 类型声明')
    const typeBody = body.slice(typeAt, body.indexOf('\n}', typeAt))
    assert.match(
      typeBody,
      /origin\?:\s*['"]asr['"]\s*\|\s*['"]manual['"]/,
      '入参类型没有 origin 字段',
    )
    assert.match(
      body,
      /export function renderTranscript\(segments:\s*TranscriptSegmentLike\[\]\)/,
      'renderTranscript 的入参没用 TranscriptSegmentLike ⇒ 类型改了它也不接',
    )
    // ★ 负控：把类型里的 origin 去掉 ⇒ 上面那条**必须**判红（否则这道门恒绿）。
    const badType = typeBody.replace(
      /origin\?:\s*['"]asr['"]\s*\|\s*['"]manual['"]/,
      '',
    )
    assert.notEqual(badType, typeBody, '变异没落盘 ⇒ 这条负控不算数')
    assert.ok(
      !/origin\?:\s*['"]asr['"]\s*\|\s*['"]manual['"]/.test(badType),
      '负控前提失效：去掉 origin 之后正则仍然匹配 ⇒ 这道门没有牙',
    )
  })

  it('#7 直连 LLM 的那个出口确实走 renderTranscript（形态变了这条判据会提醒重审）', () => {
    const api = readFileSync(API_SRC, 'utf8')
    assert.match(
      api,
      /const transcript = renderTranscript\(segments\)/,
      'api/meetings.ts 的 fallbackRefine 已不再用 renderTranscript —— ' +
      '若换成了 dedupeSegments 更好，但本条的形态判据需随之更新，不能让它静默失真',
    )
  })

  it('#8 提取器自证：区间必须**含渲染实现**且**排除 dedupeSegments**（否则上面两条失去区分力）', () => {
    // ⚠⚠ §932：共享提取器 `_render-impl.mjs` 的锚点被 §932 的**泛型化**打断过
    //   （`createTranscriptRenderer<T …>()` ⇒ 锚点带左括号就永远匹配不上
    //   ⇒ 提取器返回空串 ⇒ 两条门以「取不到渲染实现的区间（0 字）」**假红**）。
    //   ⇒ 这里把「提取器**真的取到了该取的区间**」本身**钉成判据**
    //   ⇒ 下次再有人改函数签名，这一条会**先**红，并直接指出是提取器的锚点坏了。
    const body = renderImpl(src)
    assert.ok(body && body.length > 200, `取不到渲染实现的区间（${body?.length ?? 0} 字）`)
    // ① 必须**含**渲染实现本体
    assert.ok(
      body.includes('class TranscriptRenderState'),
      '区间里没有 TranscriptRenderState ⇒ 提取器的起点锚点失效了',
    )
    assert.ok(
      body.includes('createTranscriptRenderer'),
      '区间里没有 createTranscriptRenderer ⇒ 提取器的终点锚点失效了',
    )
    // ② 必须**排除** dedupeSegments（§401 这条门的区分力全靠这个排除）
    //    ⚠ 它在文件里更靠前 ⇒ 区间从 TranscriptSegmentLike 起 ⇒ 天然排除
    //    ⇒ 这里钉住这个不变量（否则将来有人把区间往前挪 ⇒ 门会命中「那处本来就对」的 origin）
    //
    // ⚠⚠ 锚点**不能带左括号**：`dedupeSegments` 本身也是**泛型**的
    //   （`export function dedupeSegments<T extends {…}>(…)`）⇒ 带括号就永远匹配不上
    //   ⇒ 这条断言会**恒真** ⇒ 门失去区分力而没人知道
    //   （与 §932 打断 `createTranscriptRenderer` 那个锚点是**同一个坑**。）
    assert.ok(
      !body.includes('export function dedupeSegments'),
      '区间把 dedupeSegments 包进来了 ⇒ §401 这条门会命中「那处本来就对」的 origin ⇒ 失去区分力',
    )
    // ★ 负控：把区间起点往前挪（模拟「有人把 dedupeSegments 圈进来」）⇒ 必须被判出来
    const at = src.indexOf('export function dedupeSegments')
    assert.ok(at >= 0, '负控前提失效：源码里找不到 dedupeSegments 的声明')
    const widened = src.slice(at)
    assert.notEqual(widened, src, '变异没落盘 ⇒ 这条负控不算数')
    assert.ok(
      widened.includes('export function dedupeSegments'),
      '负控前提失效：区间没真的变宽',
    )
    assert.ok(
      widened.length > body.length,
      '负控前提失效：变宽后的区间并没有比原区间长 ⇒ 负控没模拟到「圈进 dedupeSegments」',
    )
  })
})
