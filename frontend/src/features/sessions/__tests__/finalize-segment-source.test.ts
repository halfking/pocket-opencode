// finalize-segment-source.test.ts — §49：收尾时读的**必须是持久层分段**，不是录音态读数。
//
// 起因是一个比 §48 更深的根因：`useSessionLiveRecord.stop()` 原来写的是
//     await recorder.stop()          ← runtime 在这里把 activeMeetingId 置空
//     …
//     const segs = recorder.segments.value   ← 于是这里恒为 []
// 而 useMeetingRecorder 的 segments/elapsedMs 都按 activeMeetingId 圈定
// （`segments = computed(() => scoped.value ? rt.segments.value : [])`）。
//
// 后果不是「少几段」，是最坏那条分支：空 original ⇒ 说话人归因整体失效 ⇒
// 交给 LLM 的永远是一条 speakerLabel=null / endMs=0 的单段 ——
// §40 修的那个形态在生产里照旧发生，§48 的名单推导也拿不到任何说话人标签。
//
// ★ 这两道绿门为什么没抓到：它们的夹具都是**直接喂数组**，
//   不经过 useMeetingRecorder 的圈定逻辑 ⇒ 被测的那段状态在夹具里根本不存在。
//   所以这里一半用源码顺序判据（钉住「来源与 stop 无关」），
//   一半用**真实生产函数**跑出「空分段 ⇒ 归因失效」这条因果。
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'
import { attributeFullTranscript, type FullSegmentReceipt } from '../../meetings/meeting-final-transcript.ts'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..', '..')  // → frontend/src
const read = (rel: string) => fs.readFileSync(path.resolve(SRC, rel), 'utf8')
const hook = read('features/sessions/useSessionLiveRecord.ts')

const blank = (m: string) => m.replace(/[^\n]/g, ' ')
const strip = (s: string) => s
  .replace(/\/\*[\s\S]*?\*\//g, blank)
  .split('\n').map((l) => { const i = l.indexOf('//'); return i < 0 ? l : l.slice(0, i) }).join('\n')

/** 只取 stop() 函数体：起点 `async function stop()`，终点下一个函数声明。 */
function stopBody(src: string): string {
  const from = src.indexOf('async function stop(')
  assert.ok(from > 0, '找不到 stop()')
  const to = src.indexOf('async function toggle(', from)
  return src.slice(from, to > from ? to : src.length)
}

const body = strip(stopBody(hook))

describe('A：分段来源不得依赖 stop() 之后的录音态读数', () => {
  it('★ stop() 里不得读 recorder.segments（它在 stop() 之后恒为 []）', () => {
    assert.ok(
      !/recorder\s*\.\s*segments/.test(body),
      'stop() 仍在读 recorder.segments —— activeMeetingId 已被 stop() 置空，'
      + '这个 computed 恒返回 []，说话人归因与名单推导在生产里都是死的',
    )
  })

  it('★ stop() 里不得读 recorder.elapsedMs（同理恒为 0）', () => {
    assert.ok(
      !/recorder\s*\.\s*elapsedMs/.test(body),
      'stop() 仍在读 recorder.elapsedMs —— 同因恒为 0，兜底分段的 endMs 会变成 0',
    )
  })

  it('★ 分段必须来自持久层 getMeetingWithSegments', () => {
    assert.ok(
      /getMeetingWithSegments\s*\(/.test(body),
      '收尾分段没从持久层读 —— 在途分段是 stop() 期间才落库的，只有持久层拿得到',
    )
  })

  it('★ 时长必须来自 stop() 的返回值', () => {
    assert.ok(
      /const stopped = await recorder\.stop\(\)/.test(body) && /stopped\?\.durationMs/.test(body),
      'durationMs 只能由 stop() 的返回值给出；读录音态会得到 0',
    )
  })

  it('★ 负控：§49 修复前的原始写法，四条判据必须全部转红', () => {
    // 这不是构造的样本，是**修复前真实写在文件里的代码**。
    const before = strip(`
      async function stop() {
        await recorder.stop()
        active.value = false
        if (!meetingId.value) return
        const id = meetingId.value
        const segs = recorder.segments.value
        const baseSegments = adopt && finalText.text
          ? buildRefineBaseSegments({
              meetingId: id,
              original: segs,
              fullText: finalText.text,
              fullSegments: finalText.segments,
              elapsedMs: recorder.elapsedMs.value,
            })
          : segs
      }
    `)
    assert.ok(/recorder\s*\.\s*segments/.test(before), '负控样本构造错了：旧写法确实读了 recorder.segments')
    assert.ok(/recorder\s*\.\s*elapsedMs/.test(before), '负控样本构造错了：旧写法确实读了 recorder.elapsedMs')
    assert.ok(!/getMeetingWithSegments\s*\(/.test(before), '负控样本构造错了：旧写法没有读持久层')
    assert.ok(!/const stopped = await recorder\.stop\(\)/.test(before), '负控样本构造错了：旧写法没接返回值')
  })

  it('★ 负控：顺序写对但来源仍错，也要被判据抓住', () => {
    // 「在 stop() 之前读」看着像修好了，其实会漏掉 stop() 期间落库的在途分段。
    const readBefore = strip(`
      async function stop() {
        const segs = recorder.segments.value
        await recorder.stop()
      }
    `)
    assert.ok(
      /recorder\s*\.\s*segments/.test(readBefore) && !/getMeetingWithSegments/.test(readBefore),
      '提前读也不是正确修复（漏在途分段），判据必须仍然拦得住',
    )
  })
})

// ── B：用真实生产函数证明「空分段 ⇒ 归因失效」这条因果 ──────────────────────
//
// 这半边不是源码扫描，是真的把生产函数跑一遍。
// 它守的是「为什么必须修」—— 若哪天归因逻辑变了，这条会提醒重新评估。
describe('B：空分段会让说话人归因整体失效（这就是必须修的原因）', () => {
  const RECEIPTS: FullSegmentReceipt[] = [
    { index: 0, startSec: 0, endSec: 11.5, text: '这个季度的续费方案基本已经定了' },
    { index: 1, startSec: 11.5, endSec: 22, text: '没签的那两个走线下' },
    { index: 2, startSec: 22, endSec: 28, text: '评审会我准备对比表' },
  ]
  const REAL_SEGS = [
    { id: 's1', meetingId: 'm1', speakerLabel: '张伟', lang: 'zh', confidence: 0.9, startMs: 0, endMs: 11500, text: '这个季度的续费方案基本已经定了' },
    { id: 's2', meetingId: 'm1', speakerLabel: '李娜', lang: 'zh', confidence: 0.9, startMs: 11500, endMs: 22000, text: '没签的那两个走线下' },
    { id: 's3', meetingId: 'm1', speakerLabel: '张伟', lang: 'zh', confidence: 0.9, startMs: 22000, endMs: 28000, text: '评审会我准备对比表' },
  ]

  it('★ original 为空 ⇒ 归因返回 null（调用方落进「压成一条」兜底）', () => {
    assert.equal(
      attributeFullTranscript([], RECEIPTS),
      null,
      '样本前提被改动了：空 original 本该让归因失效。若这里变成非 null，说明归因逻辑变了，'
      + '§49 的必要性要重新评估',
    )
  })

  it('★ 对照：分段齐全时归因有效且保住说话人标签', () => {
    const out = attributeFullTranscript(REAL_SEGS, RECEIPTS)
    assert.ok(out, '有真实分段时归因必须有效（否则 B 组的前提不成立）')
    assert.deepEqual(
      out.map((s) => s.speakerLabel),
      ['张伟', '李娜', '张伟'],
      '说话人标签是这次修复的全部意义，不能丢',
    )
  })

  it('★ 一个标签都没有时同样失效（原分段里没有可挂的东西）', () => {
    const unlabeled = REAL_SEGS.map((s) => ({ ...s, speakerLabel: null }))
    assert.equal(attributeFullTranscript(unlabeled, RECEIPTS), null)
  })
})