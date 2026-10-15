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

  it('#5 renderTranscript 体内真的把 origin 传给了 dedupeStep', () => {
    // 切出函数体，避免命中 dedupeSegments 里的 origin（那处本来就对）。
    const start = src.indexOf('export function renderTranscript')
    assert.ok(start > 0, '找不到 renderTranscript')
    const body = src.slice(start)
    assert.match(
      body,
      /origin/,
      'renderTranscript 函数体内没有任何 origin 引用 ⇒ 豁免没接上（#1 之所以能红是因为它）',
    )
    assert.match(
      body,
      /manual\s*:\s*s\.origin\s*===\s*['"]manual['"]/,
      'renderTranscript 没把 origin===\'manual\' 传给 dedupeStep 的 manual 入参',
    )
  })

  it('#6 入参类型声明了 origin（否则调用方传了也被结构性丢弃）', () => {
    const start = src.indexOf('export function renderTranscript')
    const sig = src.slice(start, src.indexOf('): string {', start))
    assert.match(sig, /origin\?:\s*['"]asr['"]\s*\|\s*['"]manual['"]/, 'renderTranscript 入参没有 origin 字段')
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
})
