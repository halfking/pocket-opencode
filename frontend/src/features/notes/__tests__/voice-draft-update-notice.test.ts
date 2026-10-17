// voice-draft-update-notice.test.ts — §833：草稿正文**回填失败**不得无声。
//
// 缺陷：`NoteRecorderRuntime.runStop()` 在兜底转写之后回填正文时，
// `try { await voiceDraftSink.update(draftId, finalText) } catch {}` 的 catch
// 是**完全空的** —— 不 console、不写 this.error、不产出任何信号。
//
// ⚠ 它不是「录音丢了」那么简单（音频在草稿里好好躺着），后果是三条
//   **看起来都正常**的东西：
//   ① 草稿正文永久停在停止瞬间那版（settledText）；
//   ② 紧接着的 /summarize 读的是**库里那份正文** —— NoteListView 的
//      presentVoiceDraft(note, text) 只拿 text 做空值门槛，从不把它写回笔记
//      ⇒ 即时摘要是按不完整的正文生成的；
//   ③ 那份摘要读起来完全正常（它是好摘要，只是漏了最后几句）⇒ 无从分辨。
//
// ⚠ 与 §832 同族但更隐蔽：§832 是「什么都没留下」，这里是「留下了一条
//   看起来完整的草稿 + 一份看起来正常的摘要」。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import {
  describeVoiceDraftUpdateFailure,
  shouldBackfillVoiceDraft,
  VOICE_DRAFT_UPDATE_FAIL_TAG,
} from '../voice-draft-update-notice.ts'

const HERE = dirname(fileURLToPath(import.meta.url))
const RUNTIME = readFileSync(join(HERE, '../../../native/recordingRuntime.ts'), 'utf8')
const VIEW = readFileSync(join(HERE, '../NoteListView.vue'), 'utf8')

describe('§833 · 回填判据（行为，纯函数）', () => {
  it('★ 需要回填：草稿存在 + 新正文非空 + 与建库时不同', () => {
    assert.equal(shouldBackfillVoiceDraft({ draftId: 'n1', settledText: '前半句', finalText: '前半句后半句' }), true)
  })

  it('★★ 没草稿 ⇒ 不回填（那是 §832 的建草稿失败，不该在这里报错）', () => {
    for (const draftId of [null, undefined, '']) {
      assert.equal(shouldBackfillVoiceDraft({ draftId, settledText: '', finalText: '有字' }), false,
        `draftId=${String(draftId)} 竟被判成需要回填`)
    }
  })

  it('★★★ 正文为空 ⇒ 绝不能回填（会把正文擦成空白，比不回填更坏）', () => {
    for (const finalText of ['', '   ', '\n\t ']) {
      assert.equal(shouldBackfillVoiceDraft({ draftId: 'n1', settledText: '旧', finalText }), false,
        `空正文 ${JSON.stringify(finalText)} 竟被判成需要回填`)
    }
  })

  it('★★ 两边一样 ⇒ 不回填（否则一次无谓写库失败会假造一条告警）', () => {
    assert.equal(shouldBackfillVoiceDraft({ draftId: 'n1', settledText: '同一句', finalText: '同一句' }), false)
  })

  it('⚠ 负控：纯空白与非字符串不得被当成「有正文」', () => {
    assert.equal(shouldBackfillVoiceDraft({ draftId: 'n1', settledText: '', finalText: undefined as unknown as string }), false)
    assert.equal(shouldBackfillVoiceDraft({ draftId: 'n1', settledText: '', finalText: null as unknown as string }), false)
  })
})

describe('§833 · 文案层（行为，纯函数）', () => {
  const w = describeVoiceDraftUpdateFailure({ error: new Error('QuotaExceededError') })

  it('★★ 必须说清「正文没跟上」而不是「录音丢了」—— 音频还在，说错会让用户白重录', () => {
    assert.match(w, /正文/, '没说清是正文没跟上')
    assert.doesNotMatch(w, /录音.*(丢|没了|不见)/, '文案把后果说成了录音丢失 ⇒ 用户会白白重录一遍')
    assert.match(w, /音频/, '必须点明音频仍然在草稿里')
  })

  it('★★★ 必须说清那份 AI 总结是按不完整正文生成的（否则用户无从分辨摘要为何漏句）', () => {
    assert.match(w, /总结/, '没提 AI 总结 ⇒ 用户会以为总结没问题')
    assert.match(w, /不完整/, '没点明总结所依据的正文不完整')
  })

  it('★★ 必须给出可执行的动作（手动补字 / 对着音频核对 / 重录）', () => {
    assert.match(w, /草稿/, '没指向要去哪补')
    assert.match(w, /补|重录/, '只报错不给路')
  })

  it('负控：任意异常形态都不得产出 undefined / [object Object] / NaN', () => {
    for (const e of [null, undefined, 42, {}]) {
      const t = describeVoiceDraftUpdateFailure({ error: e })
      assert.doesNotMatch(t, /undefined|\[object Object\]|NaN/, `异常形态 ${String(e)} 产出了假读数：${t}`)
    }
  })

  it('★ 诊断前缀必须与 §832 的建草稿失败可区分（否则控制台里分不清是哪条路坏的）', () => {
    assert.match(VOICE_DRAFT_UPDATE_FAIL_TAG, /回填/, '诊断前缀没点明是回填')
    assert.doesNotMatch(VOICE_DRAFT_UPDATE_FAIL_TAG, /草稿没能保存/, '与 §832 的建草稿失败前缀撞了')
  })
})

// ── 接线层（源码扫描，⚠ 对「信号变死代码」零牙，只保证真的接上了）──
describe('§833 · 接线（扫描层，零牙已如实标注）', () => {
  /** 从「最终正文」那行到 pendingResult 之间的窗口 = 回填那一整段。 */
  const backfillWindow = (): string => {
    const from = RUNTIME.indexOf('const finalText = this.transcript.value.trim()')
    assert.ok(from > 0, '★ 阳性对照失败：找不到回填段的起点锚点，此刻本门是假阴性')
    const to = RUNTIME.indexOf('this.pendingResult =', from)
    assert.ok(to > from, '★ 阳性对照失败：找不到回填段的终点锚点，此刻本门是假阴性')
    return RUNTIME.slice(from, to)
  }

  it('★★ 回填失败必须同时诊断 + 对用户说话（原缺陷就是空 catch）', () => {
    const w = backfillWindow()
    assert.match(w, /voiceDraftSink!\.update\(/, '回填调用不见了')
    assert.match(w, /catch \(e\)/, '回填没有 catch')
    assert.match(w, /console\.error\(/, '回填失败没有进日志')
    assert.match(w, /this\.error\.value = describeVoiceDraftUpdateFailure\(/,
      '回填失败没有用纯函数决策出文案，更没有写 this.error ⇒ 用户看不到')
    assert.doesNotMatch(w, /catch \{/, '回填处仍存在空 catch（本次修复的正是它）')
    // ⚠ 上面这条是**冗余**断言，本轮变异实测没有独立命中它，理由据实记在这里：
    //   任何「掏空 catch 体」的写法都必然同时丢掉 console.error 与
    //   this.error 赋值 ⇒ 前两条 match 断言先拦下（变异 A2 实测：由
    //   「回填失败没有进日志」打红）。它留着的唯一价值是把「本次修的正是
    //   空 catch」这件事钉成字面量，代价为零；不把它算作独立守卫。
  })

  it('★★ 判据必须走纯函数（不能是调用点自己拼的 if）', () => {
    const w = backfillWindow()
    assert.match(w, /shouldBackfillVoiceDraft\(\{/, '回填判据没有走纯函数')
    assert.doesNotMatch(w, /if \(draftId && finalText && finalText !== settledText\)/,
      '判据又内联回调用点了 ⇒ 行为用例（shouldBackfillVoiceDraft）测不到真实行为')
  })

  it('★★★ 与 EMPTY_RECORDING_NOTICE 必须互斥，且任何一条空录音赋值都不得抢在回填告警之前', () => {
    // 互斥的依据：回填只在 finalText 非空时发生；空提示只在「无字」时触发。
    // 若哪天有人放宽了其中一条，这个顺序保证不会让两条文案互相覆盖（后者静默盖前者）。
    //
    // ⚠ 判别句：**锚在 `if (emptyNotice) …` 这个字面量上是不行的** —— 那是 §830
    //   同款陷阱（前提登记类断言指错位置）。本轮变异实测：从别的路径提前写
    //   `this.error.value = EMPTY_RECORDING_NOTICE` 时，那条断言**照样全绿**。
    // ⇒ 改成「枚举**所有**该常量的赋值点，逐个与回填告警比位置」。
    const backfillAt = RUNTIME.indexOf('this.error.value = describeVoiceDraftUpdateFailure(')
    assert.ok(backfillAt > 0, '★ 阳性对照失败：找不到回填告警的赋值行')
    const emptyAssignments = [...RUNTIME.matchAll(/this\.error\.value = EMPTY_RECORDING_NOTICE/g)].map((m) => m.index!)
    assert.ok(emptyAssignments.length > 0, '★ 阳性对照失败：找不到空录音提示的赋值行')
    for (const at of emptyAssignments) {
      assert.ok(at > backfillAt, `空录音提示在第 ${at} 字符处赋值，早于回填告警（第 ${backfillAt} 处）⇒ 两者可能互相覆盖`)
    }
  })

  it('★ 预落库失败的 catch 必须有诊断（它退回界面路径，§832 负责用户可见那一段）', () => {
    const from = RUNTIME.indexOf('draftId = await this.voiceDraftSink.create({')
    assert.ok(from > 0, '★ 阳性对照失败：找不到预落库调用，此刻本门是假阴性')
    const w = RUNTIME.slice(from, from + 700)
    assert.match(w, /catch \(e\)/, '预落库没有 catch')
    assert.match(w, /console\.error\(/, '预落库失败没有进日志')
    assert.match(w, /draftId = null/, '预落库失败不再退回界面路径')
  })

  it('★★ this.error 在停止后仍被渲染，且不被 apiError 二次加工（否则文案会被盖掉）', () => {
    assert.match(VIEW, /v-else-if="recError"/, '停止后 recError 没有单独的错误行')
    assert.match(VIEW, /const recordErrorText = computed\(\(\) => recError\.value \|\| ''\)/,
      'recordErrorText 不再直出 recError ⇒ 文案会被错误码映射盖掉（§222 已有前科）')
  })

  it('⚠ 前提登记：this.error 在下一次 start() 时会被清空（否则告警会一直挂着）', () => {
    const clears = RUNTIME.match(/this\.error\.value = ''/g) ?? []
    assert.ok(clears.length >= 2, `只在 ${clears.length} 处清空 error，需确认 start 路径仍在清`)
  })
})
