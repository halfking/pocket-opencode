/**
 * transcribe-full-wire-contract.test.ts — 后端 `/api/stt/transcribe-full`
 * 的 `segments` wire 形状 ↔ 前端 `attributeFullTranscript`。
 *
 * ★ 这份 fixture **不是手写的**：它由
 *   backend/internal/stt/full_wire_contract_test.go
 * 真实 `json.MarshalIndent` 产出（UPDATE_FIXTURE=1 时写入）。
 * 后端改 json tag → 那个 Go 测试立刻红 → 本文件读到的是新形状 → 本文件也红。
 * ⇒ 不存在「手抄漂移」这一类失效。
 *
 * ── 为什么这件事值得单独一道门（§39）──
 * §36 修的「把逐段回执挂回说话人」靠的是 `startSec` / `endSec`。
 * 静默失效路径：
 *
 *   Go 改名 → 前端拿到 undefined
 *   → toMs(undefined)：Number.isFinite(undefined) 为 false → 归 0
 *   → 每条回执都变成 [0, 0]，overlap 恒为 0
 *   → 不抛错、不告警，归属全部退化成「挂到第一个候选段」
 *
 * 即：改一个 json tag 的名字，§36 的整条修复**安静地变成摆设**，
 * 而 go test 与 npm run gates 全绿。所以判据必须钉「真的读到了那些键」。
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { attributeFullTranscript } from '../../features/meetings/meeting-final-transcript.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
const FIXTURE = join(HERE, 'fixtures/transcribe-full-segments.json')

type Receipt = { index: number; startSec: number; endSec: number; text?: string; error?: string }
const fixture = () =>
  JSON.parse(readFileSync(FIXTURE, 'utf8')) as { segments: Receipt[]; failed: number; succeeded: number }

/** 实时分段：时间范围与 fixture 的三段对齐，标签是声纹聚类给的。 */
const realtime = (): Array<{
  id: string; meetingId: string; speakerLabel: string | null
  lang: string; confidence: number; startMs: number; endMs: number; text: string
}> => [
  { id: 's1', meetingId: 'm1', speakerLabel: '张伟', lang: 'zh', confidence: 0.9, startMs: 0, endMs: 11500, text: '这个季度的续费方案basicly已经定了' },
  { id: 's2', meetingId: 'm1', speakerLabel: '李娜', lang: 'zh', confidence: 0.9, startMs: 11500, endMs: 22000, text: '没签的那两个走线下' },
  { id: 's3', meetingId: 'm1', speakerLabel: '张伟', lang: 'zh', confidence: 0.9, startMs: 22000, endMs: 28000, text: '评审会我准备对比表' },
]

describe('W · 契约：前端依赖的键，必须真的在 wire 上', () => {
  it('fixture 是后端真实 marshal 出来的（有三段，且第 2 段是失败段）', () => {
    const f = fixture()
    assert.equal(f.segments.length, 3, 'fixture 段数变了：先确认后端样本没被改动')
    assert.equal(f.segments[1].text, undefined, '第 2 段应是失败段（无 text 只有 error）')
    assert.ok(f.segments[1].error, '第 2 段应带 error')
  })

  it('★ startSec / endSec 存在且是 number（不是字符串、不是缺键）', () => {
    for (const s of fixture().segments) {
      for (const k of ['startSec', 'endSec'] as const) {
        assert.ok(k in s, `回执缺 ${k} —— 前端 toMs 会拿到 undefined 并静默归 0`)
        assert.equal(typeof (s as Record<string, unknown>)[k], 'number', `${k} 必须是 number`)
      }
      assert.ok(s.endSec > s.startSec, `第 ${s.index} 段时间区间非法`)
    }
  })

  it('第一段 startSec 真的被序列化出来（防 omitempty 把 0 吞掉）', () => {
    // startSec: 0 是合法且常见的值。若某次给它加了 omitempty，
    // 第一段会整个丢掉 start —— 而这与「字段名写错」的读数一模一样。
    assert.equal(fixture().segments[0].startSec, 0)
    assert.ok('startSec' in fixture().segments[0], 'startSec: 0 在 wire 上消失了')
  })
})

describe('W · 行为：把真实 wire 形状喂进去，归属必须正确', () => {
  it('★ 高精度文本挂到时间重叠最大的那一段，说话人不丢', () => {
    const out = attributeFullTranscript(realtime(), fixture().segments)
    assert.ok(out, '真实 wire 形状下不应返回 null')
    assert.equal(out.length, 3)
    assert.deepEqual(out.map((s) => s.speakerLabel), ['张伟', '李娜', '张伟'])
    assert.equal(out[0].text, '这个季度的续费方案基本上已经定了')
    assert.equal(out[1].text, '［第 2 段转写失败：upstream 503］')
    assert.equal(out[2].text, '没签的那两个走线下')
  })

  it('原分段里的 ASR 同音字错误被换成了高精度那份', () => {
    const out = attributeFullTranscript(realtime(), fixture().segments)
    assert.ok(out)
    const joined = out.map((s) => s.text).join('\n')
    assert.ok(!joined.includes('basicly'), `应含纠错后的文本：${joined}`)
    assert.ok(joined.includes('基本上已经定了'))
  })

  it('★ 负控：wire 键被改名后，这份判据必须失效（证明它真在读 wire）', () => {
    // 模拟「后端把 startSec/endSec 改名」——Go 侧若没同步前端，
    // toMs(undefined) = Number.isFinite(undefined) ? … : 0 ⇒ 三条回执全变 [0,0]
    // ⇒ overlap 恒不 > 0，score 退化成「离 0 最近的那段」= 第 0 段。
    //
    // ★ 退化形态**不是**「结果变空」，而是「全场正文都挂到第一个人身上」：
    //   第 0 段拿到三条回执（并覆盖掉自己那段带 ASR 错字的原文本），
    //   第 1、2 段空 ⇒ 各自退回自己的原文本。
    // 这样的输出**看起来是正常的**（有文字、不报错、说话人标签都在），
    // 正是最危险的一类 ⇒ 判据必须钉住 wire 键名，不能只钉「有归属」。
    const renamed = fixture().segments.map((s) => ({
      ...s,
      startSec: undefined as unknown as number,
      endSec: undefined as unknown as number,
    }))
    const out = attributeFullTranscript(realtime(), renamed)
    assert.ok(out, '改名后仍应返回数组（不抛错、不告警）')

    // 退化：第 0 段吞掉全场
    assert.ok(
      out[0].text.includes('基本上已经定了') && out[0].text.includes('没签的那两个走线下'),
      `改名后第 0 段应吞掉全部回执正文，实际：${out[0].text}`,
    )
    assert.ok(
      !out[0].text.includes('basicly'),
      '第 0 段自己的原文本应已被回执覆盖 —— 若它还在，说明退化形态与预期不同，需重判',
    )
    // 其余两段退回各自原文本（空 bucket 的兜底）
    assert.equal(out[1].text, '没签的那两个走线下')
    assert.equal(out[2].text, '评审会我准备对比表')
    // 而说话人标签**一个都没少** —— 缺陷的可观测形状就是「标签齐全、归属全错」。
    assert.deepEqual(out.map((s) => s.speakerLabel), ['张伟', '李娜', '张伟'])
  })
})
