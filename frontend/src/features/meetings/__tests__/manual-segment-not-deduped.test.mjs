// manual-segment-not-deduped.test.mjs — §132：人工追加的段不得被相邻去重裁成碎片。
//
// ── 这道门守的是什么 ──
//
// 用户听错一个人名，用界面提供的唯一方式（`TranscriptSegmentList` 底部的
// 「补一句转写」）追加一句改正。这句追加与原句相邻、且仅一字之差，
// 于是被 `dedupeSegments` 当成了「同音频的重转写」：
//
//   输入：张薇下周三交对比表  +  张伟下周三交对比表
//   实测：["张薇下周三交对比表", "伟"]        ← 用户的改正句被裁成一个字
//
// 而**原来的错句一个字没动**，且这两行都会继续喂给 `summarizeMeeting`、
// 进 `action_items`、进待办。⇒ 这是在教用户用一个破坏数据的方式去改字。
//
// ── 根因与修法 ──
//
// 根因：`MeetingSegment` 没有任何 provenance 字段，人工段与 ASR 段同形
// （`confidence` 写死 0.7，不具区分力），去重自然无从分辨。
// 修法：`origin: 'asr' | 'manual'`（必填 + 落库），`buildUtteranceSegment`
// 标 'manual'，`dedupeSegments` 不裁人工段。
//
// ── 三条设计约束 ──
//
// 1. **夹具由真实生产者构造**：用 `buildUtteranceSegment()` 生成追加段，
//    而不是手写一个 `as any` 对象 —— 否则门测的是「我假设的形状」而不是实际形状。
// 2. **反向保护**：去重存在的理由是用户报的「录音片段重复」（§19），
//    本次修复只能**排除人工段**，绝不能削弱它对 ASR 段的效果。
//    没有这条，修复完全可以用「把去重关掉」来让主判据变绿。
// 3. **豁免已清空**：修复落地后把 ALLOW 留在这里就是死代码，
//    它会让人以为缺陷还在。第四条用例专门守这件事。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { dedupeSegments } from '../meeting-dedup.ts'
import { buildUtteranceSegment } from '../meeting-utterance.ts'

/** 构造一个 ASR 段。 */
function asrSeg(id, text, startMs) {
  return {
    id,
    meetingId: 'm1',
    speakerLabel: '张薇',
    lang: 'zh',
    confidence: 0.86,
    startMs,
    endMs: startMs + 1200,
    text,
    origin: 'asr',
  }
}

/** 用**真实生产者**造出人工追加段。 */
function manualSeg(id, text, startMs) {
  const built = buildUtteranceSegment({ meetingId: 'm1', text, startMs })
  assert.ok(built, 'buildUtteranceSegment 返回了空 —— 夹具构造失败，先修判据')
  return { id, ...built }
}

/**
 * 修复前的豁免登记。**修复已落地，这里必须为空。**
 *
 * ⚠ 保留这张表 + 第四条用例，是为了「修复被回退」时立刻报红：
 * 谁把 origin 标记删了，ALLOW 不该被顺手加回来。
 */
const ALLOW_NO_PROVENANCE = new Map()

describe('§132 · 人工追加的转写段不得被相邻去重裁剪', () => {
  it('量具自证：真实生产者确实标了 manual（修复落在生产者上，不在测试里）', () => {
    const built = manualSeg('m1', '张伟下周三交对比表', 1000)
    assert.equal(built.origin, 'manual',
      'buildUtteranceSegment 没有标 manual ⇒ 修复被回退（或只改了一半）')
  })

  it('主判据：人工追加段经去重后必须原样保留', () => {
    if (ALLOW_NO_PROVENANCE.size > 0) {
      assert.fail('豁免表不为空 —— §132 已修复，不该再有豁免；请删掉 ALLOW_NO_PROVENANCE')
    }
    const out = dedupeSegments([
      asrSeg('s1', '张薇下周三交对比表', 1000),
      manualSeg('s2', '张伟下周三交对比表', 3000),
    ]).map((s) => s.text)
    assert.ok(out.includes('张伟下周三交对比表'),
      `用户的改正句被去重裁掉了：${JSON.stringify(out)}`)
    assert.ok(out.includes('张薇下周三交对比表'),
      `原句也被改动了：${JSON.stringify(out)}`)
  })

  it('人工段仍要成为后续重叠的基准（它就是屏幕上显示的正文）', () => {
    // 人工行不参与裁剪，但下一段 ASR 的重叠仍要拿**它**去对齐 ——
    // 否则等于假装那一行不存在，ASR 重复会重新从人工段旁边长出来。
    const out = dedupeSegments([
      manualSeg('m1', '今天下午三点开产品评审会', 1000),
      asrSeg('a2', '今天下午三点开产品评审会请提前十分钟到场', 3000),
    ]).map((s) => s.text)
    assert.equal(out[1], '请提前十分钟到场',
      `人工段之后的 ASR 重叠没有被消解：${JSON.stringify(out)}`)
  })

  it('★ 反向保护：ASR 段之间的真重叠仍必须被消解', () => {
    // 这是 §19 用户实测「录音片段重复」那个缺陷的门。
    // 「把去重关掉」是让主判据变绿的最省事做法，必须有独立判据拦住。
    //
    // ⚠ 断言锚点是被**实测**校准的，不是照想象写的：
    // 首版把「今天今天」放进**段内**，并断言 `out.some(t => !t.includes(...))` ——
    // 那条判据在**去重关掉时也成立**（第二段本来就没有重复），属于恒真判据；
    // 而且段内重复本就不归这套算法管（见 meeting-dedup.ts 的 ⚠ 注释）。
    // 实测真实行为：跨段重叠被裁掉的是**后一段**，段内重复不动。
    const segs = [
      asrSeg('a1', '今天今天下午三点开产品评审会', 1000),
      asrSeg('a2', '今天下午三点开产品评审会请提前十分钟到场', 3000),
    ]
    const out = dedupeSegments(segs).map((s) => s.text)
    const second = out[1] ?? ''
    assert.ok(second.length < segs[1].text.length,
      `ASR 重叠没有被消解：第 2 段长度 ${second.length} 未小于原始 ${segs[1].text.length} —— ${JSON.stringify(out)}`)
    assert.ok(second.includes('请提前十分钟到场'),
      `裁过头了（把新内容也吃掉）：${JSON.stringify(out)}`)
  })

  it('负控：两段完全不同的内容不得被误裁（守卫不是「一律裁」）', () => {
    const segs = [
      asrSeg('c1', '今天下午三点开产品评审会', 1000),
      asrSeg('c2', '预算按季度复盘一次', 3000),
    ]
    const out = dedupeSegments(segs).map((s) => s.text)
    assert.equal(out.length, 2, `不该被裁却只剩 ${out.length} 段：${JSON.stringify(out)}`)
    assert.ok(out.includes('预算按季度复盘一次'), `内容被吃掉：${JSON.stringify(out)}`)
  })

  it('未标注来源的段按 asr 处理（保守方向：去重照常生效）', () => {
    // 反向保护的正反面：万一将来有人忘了给某条路径标 origin，
    // 默认必须是**享受去重**而不是豁免 —— 否则等于悄悄放行了重复文本。
    const segs = [
      { ...asrSeg('u1', '今天下午三点开产品评审会', 1000), origin: undefined },
      asrSeg('u2', '今天下午三点开产品评审会请提前到场', 3000),
    ]
    const out = dedupeSegments(segs).map((s) => s.text)
    assert.ok((out[1] ?? '').length < segs[1].text.length,
      `未标注 origin 的段没有被去重：${JSON.stringify(out)}`)
  })
})