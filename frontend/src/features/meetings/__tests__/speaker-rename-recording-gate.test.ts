// speaker-rename-recording-gate.test.ts — §399：说话人改名入口必须保持**录音态门**。
//
// ★ 这道门钉的不是「改名能不能用」，而是**一道防线还在不在**
//
// `MeetingDetailView.vue` 的「👤 说话人」按钮带 `v-if="micOn && speakers.length"`。
// 正是这个 `v-if` 让整类缺陷**当前不可达**：
//
//   §268.3「历史段根本改不了」——非录音态根本不渲染入口，走不到；
//   E2a「跨会议串写」——`updateSegmentSpeaker` 的 SQL 是
//     `UPDATE local_meeting_segments SET speaker_label = ? WHERE id = ?`
//     ⚠ **只按 segId、不带 meeting 条件**，而 `segmentProfiles` 是进程级 Map、
//     只在 `freshSession` 时 clear（`recordingRuntime.ts:198/308`）。
//     录音态下 `this.segments` 与 `segmentProfiles` 同属当前会议，所以安全；
//     一旦放开入口，用户在会议 B 改名会**静默改掉会议 A 的转写**。
//
// §275/§276 为这一类做过变异台、登记过待拍板、并要求「段上落 profileId」
// 这个数据结构缺口。**若真按那套修法动数据结构，成本远高于先钉住这道门。**
//
// ⚠ 因此本门的意义是：**放开它的那一刻，就该同时处理 §276.6 的三个缺口。**
//   门变红不是「功能坏了」，而是「你正在打开三个已知缺陷」。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import path from 'node:path'
import fs from 'node:fs'
import { fileURLToPath } from 'node:url'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const VIEW = path.resolve(HERE, '..', 'MeetingDetailView.vue')
const src = fs.readFileSync(VIEW, 'utf8')

/** 从「👤 说话人」这一行往回找最近的 `<button`，取它的完整开标签。 */
function speakerButtonTag(text: string): string {
  const lines = text.split('\n')
  const at = lines.findIndex((l) => l.includes('👤 说话人'))
  if (at < 0) return ''
  for (let i = at; i >= 0 && i > at - 12; i--) {
    if (lines[i].includes('<button')) return lines.slice(i, at + 1).join('\n')
  }
  return ''
}

/** 判据本体：改名入口的 v-if 里必须有录音态。 */
function hasRecordingGate(tag: string): boolean {
  const m = /v-if\s*=\s*"([^"]*)"/.exec(tag)
  if (!m) return false
  return /(^|[^a-zA-Z])micOn([^a-zA-Z]|$)/.test(m[1])
}

describe('§399 · 说话人改名入口的录音态门（唯一防线）', () => {
  it('★★ 改名入口必须被 micOn 门住', () => {
    const tag = speakerButtonTag(src)
    assert.ok(tag, '找不到「👤 说话人」按钮 —— 若它被改名/移除，请同步更新本门与 docs §399')
    assert.ok(hasRecordingGate(tag),
      `「👤 说话人」按钮现在不受 micOn 门控（实际 v-if：${
        (/v-if\s*=\s*"([^"]*)"/.exec(tag) ?? [])[1] ?? '(无)'
      }）。\n` +
      '⚠ 放开它会同时打开 docs §276.6 的三个已知缺口：\n' +
      '   ① 跨会议串写（updateSegmentSpeaker 的 SQL 只按 segId，不带 meeting 条件）\n' +
      '   ② 无差别改写（循环对象 segmentProfiles 同时承担「指向」与「证明归属」两件事）\n' +
      '   ③ 历史段的说话人根本改不了（segId 不在本次会话的 Map 里，且无任何提示）\n' +
      '若确实要支持「录完再改名」，请先补段上的 profileId（§276.6），再改本门。')
  })

  it('★★ 负控：把 micOn 从 v-if 里拿掉，这条判据必须当场判红', () => {
    const mutant = ['<button', '  v-if="speakers.length"', '  class="speakers-btn"',
      '  @click="showSpeakers = true"', '>👤 说话人</button>'].join('\n')
    assert.equal(hasRecordingGate(mutant), false,
      '去掉 micOn 后判据仍然通过 ⇒ 它是恒真的，什么都没钉住')
    assert.equal(hasRecordingGate(speakerButtonTag(src)), true,
      '负控前提失败：原按钮也没被门住，这条判据分不开')
  })

  it('负向确认：SpeakerLabelSheet 只能由这个入口打开', () => {
    // 改名动作只有一条路：onSpeakerLabel → labelSpeaker。
    // 若将来新增第二个入口（例如详情页常驻一个改名按钮），本门会失效，
    // 所以这里把「入口唯一」一并钉住。
    const opens = src.match(/showSpeakers\s*=\s*true/g) ?? []
    assert.equal(opens.length, 1,
      `发现 ${opens.length} 处把 showSpeakers 置 true ⇒ 改名入口不再唯一，` +
      '请检查新增的入口是否也带录音态门，并更新本门。')
  })

  it('登记：改名写入的 SQL 仍不带 meeting 条件（这条是缺口本身，不是判据）', () => {
    // 缺口就摆在这里：E2a 一旦可达，「绝不碰别的会议」就守不住。
    // 本用例**不断言它应该带条件**（那是 §276.6 的数据结构缺口），
    // 只在它被修好时提醒更新本门。
    const STORE = path.resolve(HERE, '..', 'meetings-store.ts')
    const sql = fs.readFileSync(STORE, 'utf8')
    const has = /UPDATE\s+local_meeting_segments\s+SET\s+speaker_label\s*=\s*\?\s*WHERE[^;]*meeting_id/i.test(sql)
    assert.equal(has, false,
      'updateSegmentSpeaker 现在带 meeting_id 条件了 ⇒ §276.6 的缺口已补，' +
      '请更新 docs §399 与本用例（本用例的职责是登记缺口，不是守住它）。')
  })
})