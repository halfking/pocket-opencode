// segment-drop-notice.test.ts — §828：**「转写成功但没出字」的段落不得无声消失**。
//
// 缺陷（旧顺序）：
//   ingestSpeechBlob 里 `if (!sttResult.text.trim()) return null`
//   出现在 `saveAudioPart(...)` **之前** ⇒ 转写无字的那一段：
//     · 文字不建行（这是对的）
//     · **音频分片也没落盘**（这是错的）
//     · 没有计数、没有告警、调用点只在抛错时才出声
//   ⇒ 用户那句话从会议记录里消失，界面上没有任何痕迹，且**无法事后补救**。
//   ⚠§826 让它不可逆：Android 原生模式压根不产整段音频，分片是那段话的**唯一拷贝**。
//
// ⚠ 判据分两层，原因写在这里免得后来人当成冗余：
//   · **行为层**（`describeSegmentDrop` 纯函数，node 跑得到）—— 文案是否说清
//     「丢的是哪一段 / 丢的是什么 / 还能不能找回」。若这段决策只写在调用点，
//     判据只能扫 `console.warn` 的语法形状（§818 实测：改成 `void 0 && …` 照样全绿）。
//   · **接线层**（源码扫描）—— 顺序与传参。它对「信号变死代码」零牙，已如实标注。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { describeSegmentDrop } from '../segment-drop-notice.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
const INGEST = readFileSync(join(HERE, '../ingest-speech.ts'), 'utf8')

describe('§828 · 文案层（行为）', () => {
  const w = describeSegmentDrop({ seq: 7, startMs: 12_340, endMs: 18_900, engine: 'cloud' })

  it('★ 必须说清是哪一段（seq + 时间范围）', () => {
    assert.match(w, /第 7 段/, '没有序号 ⇒ 排查的人不知道丢的是哪句话')
    assert.match(w, /12340-18900ms/, '没有时间范围 ⇒ 没法回放定位')
  })

  it('★ 必须说清丢的是什么 —— 是「这一段的文字没进记录」', () => {
    assert.match(w, /文字没有进会议记录/, '文案没说清丢的是文字')
    assert.match(w, /空分段/, '没说清「不建空分段」是有意为之，读起来像出了故障')
  })

  it('★★ 必须说清还能不能找回 —— 音频分片已落盘，可事后重转', () => {
    assert.match(w, /音频分片已落盘/, '没承诺音频还在 ⇒ 读者会以为这段彻底没了')
    assert.match(w, /可事后重转|重转补回/, '没说清补救路径')
  })

  it('★ 必须指出排查方向（引擎/音质，而不是存储）', () => {
    assert.match(w, /引擎/, '没有排查方向')
    // ⚠ 判据刻意**不**用 /存储/ 做否定断言：第一版这么写，结果**正确文案也红** ——
    //   因为文案本来就要说「而不是查存储」。凡是否定断言，都要挑**不会与正确文本撞车**
    //   的那个具体形态（这里挑「落盘机制名」而不是「泛指概念」）。
    assert.doesNotMatch(w, /IndexedDB|配额不足/, '把读者指向存储实现细节是误导 —— 那不是这一类失败的原因')
  })

  it('负控：字段缺失时不得产出「NaNms」这类假读数', () => {
    const bad = describeSegmentDrop({
      seq: Number.NaN, startMs: Number.NaN, endMs: Number.NaN, engine: undefined,
    })
    assert.doesNotMatch(bad, /NaN/, `诊断里出现 NaN ⇒ 排查的人会以为数据本身坏了：${bad}`)
  })

  it('engine 缺省时文案仍然成立（不得出现「引擎 undefined」）', () => {
    const noEngine = describeSegmentDrop({ seq: 1, startMs: 0, endMs: 100 })
    assert.doesNotMatch(noEngine, /undefined/, noEngine)
    assert.match(noEngine, /第 1 段/)
  })
})

// ── 接线层（源码扫描，⚠ 对「信号变死代码」零牙，只保证顺序与传参没被搬走）──
describe('§828 · 接线（扫描层，零牙已如实标注）', () => {
  it('★★ 音频分片必须**先于**「转写没出字」的 return 落盘', () => {
    const save = INGEST.indexOf('saveAudioPart(')
    const drop = INGEST.indexOf('if (!sttResult.text.trim())')
    assert.ok(save > 0, '找不到 saveAudioPart 调用')
    assert.ok(drop > 0, '找不到空文本早退')
    assert.ok(save < drop,
      'saveAudioPart 仍在早退之后 ⇒ 转写无字的那一段连音频都没存 ⇒ 事后无法补救')
  })

  it('★ 空文本早退必须出声（决策走纯函数，调用点只剩 warn）', () => {
    const drop = INGEST.indexOf('if (!sttResult.text.trim())')
    const window = INGEST.slice(drop, drop + 500)
    assert.match(window, /describeSegmentDrop\(/, '早退分支没有调用纯函数决策')
    // ⚠⚠ 断言刻意钉在「**整条语句的形状**」上，而不是「窗口里出现过 console.warn」：
    //   第一版写的是后者，于是 §818 那个经典变异 `void 0 && console.warn(...)`
    //   **0 红** —— 它明明让信号变成了死代码。判据得对着最可能的那种变异长牙。
    assert.match(window, /^\s*console\.warn\(describeSegmentDrop\(/m,
      '必须是「这一条语句直接 warn 决策结果」；写成 void 0 && … 之类就等于把信号变成死代码')
    assert.match(window, /engine:\s*sttResult\.engine/, '没把实际引擎传进去 ⇒ 排查没有方向')
  })

  it('★ 分片落盘失败也不许完全无声（唯一一份音频拷贝丢了）', () => {
    const save = INGEST.indexOf('saveAudioPart(')
    const window = INGEST.slice(save, save + 400)
    assert.doesNotMatch(window, /\.catch\(\(\) => \{\}\)/,
      '分片落盘失败又被吞掉 ⇒ 那段话永久找不回来且无人知情')
    assert.match(window, /console\.warn\(/, '失败必须出声')
  })

  it('⚠ 前提登记：成功路径仍然要建行、仍然按 startMs 有序插入（别把本节修成回归）', () => {
    assert.match(INGEST, /const id = await saveSegment\(seg\)/, '成功路径的落库不见了')
    assert.match(INGEST, /opts\.segments\.splice\(at, 0, saved\)/,
      '有序插入不见了 —— 并发转写完成顺序 ≠ 说话顺序，会让 transcript 乱序')
  })
})
