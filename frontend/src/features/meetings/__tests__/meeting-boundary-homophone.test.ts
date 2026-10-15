// meeting-boundary-homophone.test.ts — §80 跨段人名纠正的判据。
//
// ★ 这道门为什么存在（缺陷是真网关实测出来的，不是推的）：
//
//     单段："跟章伟还有林兰开个客户评审会"        → 逐段纠正 2 条
//     切段："今天我们请到了章" / "伟来给…"        → 逐段纠正 1 条（「章伟」丢了）
//
//   而 LLM 精校**救不回来**（§80.3，`claude-haiku-4-5` 与 `glm-5.2` 各 3/3 逐字相同）：
//
//     [说话人 1] 今天我们请到了张      ← 字改对了（术语表生效）
//     [说话人 1] 伟来给大家讲一下产品路线 ← 但跨行合并不了
//
//   ⇒ 用户界面上是两个气泡，人名读起来是断的。
//
// 夹具来源：正文与 `meeting-roster-homophone.test.ts` 同源（§44.2 SenseVoice
// fp32 在 tts-meeting.wav 上的实测输出）。拼音值逐字抄自官方 lexicon.txt。
//
// 本门分三层：行为层（该修的真的修）/ 边界层（不该修的一个都不许动）/
// 接线层（planRefine 真的调了它，且 split 臂与 merged 臂产出语义一致）。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  repairSegmentBoundaryHomophones,
  repairRosterHomophones,
  type PinyinTable,
} from '../meeting-roster-homophone.ts'
import { planRefine } from '../meeting-refine-plan.ts'
import type { MeetingSegment } from '../meetings-store.ts'

/** 只收本夹具用到的字。值逐字抄自官方 lexicon.txt，不是猜的。 */
const TABLE: PinyinTable = {
  章: 'zhang1', 张: 'zhang1',
  伟: 'wei3', 伪: 'wei3',
  林: 'lin2', 岚: 'lan2', 兰: 'lan2', 蓝: 'lan2',
  王: 'wang2', 总: 'zong3', 综: 'zong3',
  今: 'jin1', 天: 'tian1', 我: 'wo3', 们: 'men5', 请: 'qing3',
  到: 'dao4', 了: 'le5', 来: 'lai2', 给: 'gei3', 大: 'da4', 家: 'jia1',
  讲: 'jiang3', 一: 'yi1', 下: 'xia4', 产: 'chan3', 品: 'pin3',
  路: 'lu4', 线: 'xian4', 也: 'ye3', 她: 'ta1', 负: 'fu4', 责: 'ze2',
  续: 'xu4', 约: 'yue1', 这: 'zhe4', 块: 'kuai4', 参: 'can1',
  会: 'hui4', 的: 'de5', 和: 'he2',
  与: 'yu3', 评: 'ping2', 审: 'shen3', 客: 'ke4', 跟: 'gen1', 有: 'you3',
  欧: 'ou1', 阳: 'yang2', 明: 'ming2', 华: 'hua2', 讲: 'jiang3',
}

const ROSTER = ['张伟', '林岚', '王总']

/** 含 3 字名的名单 ⇒ `ctx = maxLen-1 = 2` ⇒ 跨界窗口长 4，
 *  会出现**完全落在单侧内**的窗口。这条是 M1「窗口必须跨越边界」
 *  判定的唯一触发条件：名单全是 2 字名时 ctx=1、窗口长 2，那个判定恒真。 */
const LONG_ROSTER = ['张伟', '欧阳明华']

const seg = (text: string, speaker = '说话人 1'): MeetingSegment =>
  ({ id: `s-${text}`, meetingId: 'm1', text, startMs: 0, endMs: 1000, speakerLabel: speaker }) as MeetingSegment

describe('§80 · A 行为层：跨段人名必须被纠正，且落到正确的段', () => {
  it('章|伟 被切在边界上时，左段改成「张」', () => {
    const r = repairSegmentBoundaryHomophones(
      [seg('今天我们请到了章'), seg('伟来给大家讲一下产品路线')], ROSTER, TABLE,
    )
    assert.equal(r.texts[0], '今天我们请到了张')
    assert.equal(r.texts[1], '伟来给大家讲一下产品路线')
    assert.equal(r.fixes.length, 1)
  })

  it('★ 修正必须落在正确的段上 —— 这是本修法全部安全性的来源', () => {
    // 「林兰」首字在左段、尾字在右段：两边都要改，且不能互换
    const r = repairSegmentBoundaryHomophones(
      [seg('参会的有林'), seg('兰和产品团队', '说话人 2')], ROSTER, TABLE,
    )
    assert.equal(r.texts[0], '参会的有林')       // 「林」本来就是对的，不该被动
    assert.equal(r.texts[1], '岚和产品团队')
    const right = r.fixes.filter((f) => f.segment === 1)
    assert.equal(right.length, 1)
    assert.equal(right[0].from, '兰')
    assert.equal(right[0].to, '岚')
  })

  it('★ 左右两侧**都**是错字时，两侧各改各的，不串位', () => {
    // 窗口「章|伪」跨段，与名单里的「张伟」严格同音（zhang1 + wei3）。
    // 两边都是错的 ⇒ 修完之后左段末尾 + 右段开头必须恰好拼出「张伟」。
    // （本条第一版把右段断言成「不动」，是判据自己写错了：
    //   伪正是窗口的第二个字，本就该改。实跑的代码是对的。）
    const r = repairSegmentBoundaryHomophones(
      [seg('请到了章'), seg('伪来讲两句')], ROSTER, TABLE,
    )
    assert.equal(r.texts[0], '请到了张')
    assert.equal(r.texts[1], '伟来讲两句')
    assert.equal(r.texts[0].slice(-1) + r.texts[1].slice(0, 1), '张伟')
    assert.equal(r.fixes.length, 2, '左右各记一条')
    assert.deepEqual(r.fixes.map((f) => f.segment).sort(), [0, 1])
  })

  it('段内已能纠正的名字，不在这里被重复记一条（含 3 字名名单）', () => {
    // ★ 名单必须含 3 字名才能真正走到这条：ctx=2 ⇒ 跨界窗口长 4 ⇒
    //   它的第 0 个 2 字窗口「章伟」**完全落在左段内**，跨段判定若被去掉，
    //   这里就会凭空多出一条修正。名单全是 2 字名时该判定恒真（本条第一版栽在这）。
    const r = repairSegmentBoundaryHomophones(
      [seg('请章伟'), seg('欧阳来讲两句')], LONG_ROSTER, TABLE,
    )
    assert.equal(r.fixes.length, 0, '完整落在单段内的窗口归单段版管，这里必须为空')
    assert.deepEqual(r.texts, ['请章伟', '欧阳来讲两句'])
  })

  it('已经写对的名字跨段时原样不动', () => {
    const r = repairSegmentBoundaryHomophones(
      [seg('请到了张'), seg('伟来两句')], ROSTER, TABLE,
    )
    assert.deepEqual(r.texts, ['请到了张', '伟来两句'])
    assert.equal(r.fixes.length, 0)
  })

  it('★ 端到端：切段臂与合并臂产出**语义一致**（这条是本缺陷的判据）', () => {
    const common = { full: { text: null, applied: false }, participants: ROSTER, elapsedMs: 1000 }
    const split = planRefine({
      meetingId: 'm1', elapsedMs: 1000, ...common,
      segments: [
        seg('今天我们请到了章'),
        seg('伟来给大家讲一下产品路线'),
        seg('林兰也到了，她负责续约这块', '说话人 2'),
      ],
    })
    const merged = planRefine({
      meetingId: 'm1', elapsedMs: 1000, ...common,
      segments: [
        seg('今天我们请到了章伟来给大家讲一下产品路线'),
        seg('林兰也到了，她负责续约这块', '说话人 2'),
      ],
    })
    assert.equal(split.rosterFixes, merged.rosterFixes)
    assert.equal(split.refineSegments.map((s) => s.text).join(''),
      merged.refineSegments.map((s) => s.text).join(''))
    assert.equal(split.refineSegments[0].text, '今天我们请到了张')
    assert.equal(split.refineSegments[1].text, '伟来给大家讲一下产品路线')
  })
})

describe('§80 · B 边界层：不该动的一个都不许动', () => {
  it('名单里两个同音名字时不改（无法区分，改就是猜）', () => {
    // ★ 窗口必须是「**不在名单里**」的同音串，否则会先被边界 1 拦下，
    //   永远到不了边界 2（第一版用的「林蓝」恰在名单里，变异 M2 打不出红）。
    //   「张伪」不在名单、且与两个名单名同音；而名单里明明有正确写法「张伟」，
    //   所以任何改写都是错的方向。
    const r = repairSegmentBoundaryHomophones(
      [seg('参会有张'), seg('伪来两句', '说话人 2')], ['章伟', '张伟'], TABLE,
    )
    assert.equal(r.fixes.length, 0)
    assert.deepEqual(r.texts, ['参会有张', '伪来两句'])
  })

  it('名单里有任何名字拼音判不出来时整体放弃（表不全 ⇒ 无法证明候选唯一）', () => {
    // ★ 窗口自己**不能**含那个不可判定的字，否则 `pinyinOf` 先返回 null、
    //   根本走不到这条守卫（第一版就栽在这里，改用模块头注释里的反例）。
    //   名单 ['林岚','林蓝'] 而表缺「蓝」⇒ 无法证明「林兰」该改成哪一个 ⇒ 整体放弃。
    const partial: PinyinTable = { ...TABLE }
    delete (partial as Record<string, string>).蓝
    const r = repairSegmentBoundaryHomophones(
      [seg('参会有林'), seg('兰和蓝', '说话人 2')], ['林岚', '林蓝'], partial,
    )
    assert.equal(r.fixes.length, 0)
    assert.deepEqual(r.texts, ['参会有林', '兰和蓝'])
  })

  it('名单为空时原样返回', () => {
    const r = repairSegmentBoundaryHomophones([seg('今天请到了章'), seg('伟来两句')], [], TABLE)
    assert.equal(r.fixes.length, 0)
    assert.deepEqual(r.texts, ['今天请到了章', '伟来两句'])
  })

  it('窗口里含非汉字时不猜', () => {
    const r = repairSegmentBoundaryHomophones(
      [seg('今天请到了章，'), seg('2024 年的预算')], ROSTER, TABLE,
    )
    assert.equal(r.fixes.length, 0)
  })

  it('只有一个段时不做任何跨段判断', () => {
    const r = repairSegmentBoundaryHomophones([seg('跟章伟说')], ROSTER, TABLE)
    assert.equal(r.fixes.length, 0)
    assert.deepEqual(r.texts, ['跟章伟说'])
  })

  it('名单全是单字名时跨段不可能发生，直接返回', () => {
    const r = repairSegmentBoundaryHomophones(
      [seg('今天请'), seg('到了会议室')], ['王'], TABLE,
    )
    assert.equal(r.fixes.length, 0)
  })

  it('入参不被就地修改（纯函数）', () => {
    const segs = [seg('今天请到了章'), seg('伟来两句')]
    const snapshot = segs.map((s) => s.text)
    repairSegmentBoundaryHomophones(segs, ROSTER, TABLE)
    assert.deepEqual(segs.map((s) => s.text), snapshot)
  })
})

describe('§80 · C 接线层：planRefine 真的调了它，且与单段版互补不重叠', () => {
  it('接线在位：planRefine 的 rosterFixes 含跨段那一条', () => {
    const plan = planRefine({
      meetingId: 'm1', elapsedMs: 1000, full: { text: null, applied: false },
      participants: ROSTER,
      segments: [seg('今天请到了章'), seg('伟来两句')],
    })
    assert.equal(plan.rosterFixes, 1)
    assert.equal(plan.refineSegments[0].text, '今天请到了张')
  })

  it('单段版与跨段版在同一份输入上互补：单段版看不见跨段的，反之亦然', () => {
    const texts = ['今天请到了章', '伟来两句']
    const per = repairRosterHomophones(texts[0], ROSTER, TABLE)
    assert.equal(per.fixes.length, 0, '单段版看不见跨段的名字')
    const cross = repairSegmentBoundaryHomophones([{ text: texts[0] }, { text: texts[1] }], ROSTER, TABLE)
    assert.equal(cross.fixes.length, 1, '只有跨段版能看见')
  })

  it('顺序：先段内、后跨段（反了会让跨段扫描看到已改文本）', () => {
    // 「章伟林兰」：段内可修「林兰」，跨段可修「章伟」。
    // 若顺序反了，跨段扫描面对的是已改文本，窗口判定已不是同一件事。
    const plan = planRefine({
      meetingId: 'm1', elapsedMs: 1000, full: { text: null, applied: false },
      participants: ROSTER,
      segments: [seg('请到了章'), seg('伟和林兰都到', '说话人 2')],
    })
    assert.equal(plan.refineSegments[0].text, '请到了张')
    assert.equal(plan.refineSegments[1].text, '伟和林岚都到')
    assert.equal(plan.rosterFixes, 2, '两条都要算进去')
  })
})