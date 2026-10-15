// meeting-roster-homophone.test.ts
//
// 夹具**不是自拟措辞**：正文取自 §44.2 SenseVoice fp32 在
// `/tmp/opstt/tts-meeting.wav` 上的**实测输出**，拼音逐字抄自官方
// `lexicon.txt`（sherpa-onnx release，hr-files）。见 §44.2 / §45。
//
// ★ 本文件第一版有 4 处「变异仍绿」，根因全是同一个：**夹具太单薄，
//   多条代码路径从未被走到**（见每节的 ⚠ 注释）。这不是门的失败，
//   是「以为守住了、其实没走到」——所以每条边界都配了**能触达它**的用例。
//
// 拼音表由本测试注入：这里验证的是**算法**，不是表的覆盖率。
// 全量汉字表进包属打包决策，不在本门范围。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  repairRosterHomophones, pinyinOf, isHan, type PinyinTable,
} from '../meeting-roster-homophone.ts'

/** 只收本夹具用到的字。值逐字抄自官方 lexicon.txt，不是猜的。 */
const TABLE: PinyinTable = {
  林: 'lin2', 岚: 'lan2', 兰: 'lan2',
  张: 'zhang1', 伟: 'wei3', 伪: 'wei3',
  王: 'wang2', 总: 'zong3', 综: 'zong3',
  玄: 'xuan2', 戒: 'jie4', 悬: 'xuan2', 借: 'jie4',
  弓: 'gong1', 头: 'tou2', 公: 'gong1', 投: 'tou2',
  载: 'zai4', 传: 'chuan2', 感: 'gan3', 器: 'qi4',
  下: 'xia4', 三: 'san1', 周: 'zhou1', 到: 'dao4',
  蓝: 'lan2',
}

/** §44.2 SenseVoice 实测输出，原文照抄。 */
const ASR_TEXT =
  '下周三下午三点跟王总还有林兰开个客户评审会要准备悬借芯片和机载传感器的对比表' +
  '张伟负责公投安装的预算确认'

/** ⚠ 多条修正的夹具——第一版只有一条修正，导致「从后往前替换」从未被验到。
 *  三处错全在名单里且严格同音；「王综」还出现两次。 */
const MULTI_TEXT = '下周三下午三点跟王综说，王综和林兰都要到。张伪负责预算。'
const MULTI_ROSTER = ['张伟', '林岚', '王总']

describe('ASR 阶段的人名同音错：必须能修', () => {
  it('名单里的「林岚」被 ASR 听成「林兰」→ 修正回名单写法', () => {
    const r = repairRosterHomophones(ASR_TEXT, ['张伟', '林岚', '王总'], TABLE)
    assert.equal(r.fixes.length, 1, `应恰好修 1 处，实际 ${JSON.stringify(r.fixes)}`)
    assert.equal(r.fixes[0].from, '林兰')
    assert.equal(r.fixes[0].to, '林岚')
    assert.ok(r.text.includes('跟王总还有林岚开个客户评审会'), r.text)
  })

  it('修正后文本与原文的差异**只有**那两字', () => {
    const r = repairRosterHomophones(ASR_TEXT, ['张伟', '林岚', '王总'], TABLE)
    const diffAt = [...r.text].findIndex((c, i) => c !== ASR_TEXT[i])
    assert.ok(diffAt >= 0, '应确实改了内容')
    assert.equal(r.text.slice(0, diffAt), ASR_TEXT.slice(0, diffAt))
    assert.equal(r.text.slice(diffAt + 2), ASR_TEXT.slice(diffAt + 2))
  })

  it('★ 同一错词出现两次，两处都要修（且下标不串位）', () => {
    const r = repairRosterHomophones(MULTI_TEXT, MULTI_ROSTER, TABLE)
    assert.equal(r.text, '下周三下午三点跟王总说，王总和林岚都要到。张伟负责预算。')
    assert.equal(r.fixes.length, 4, `王综×2 + 林兰×1 + 张伪×1 = 4，实际 ${JSON.stringify(r.fixes)}`)
  })

  it('★ 多条修正按出现顺序返回（对外可读）', () => {
    const r = repairRosterHomophones(MULTI_TEXT, MULTI_ROSTER, TABLE)
    const ats = r.fixes.map((f) => f.at)
    assert.deepEqual(ats, [...ats].sort((a, b) => a - b), `fixes 应按出现顺序：${JSON.stringify(ats)}`)
  })
})

describe('三条不可越过的边界（误伤比漏修更糟）', () => {
  it('名单里没有该名字 → 一个字都不改', () => {
    const r = repairRosterHomophones(ASR_TEXT, ['张伟', '王总'], TABLE)
    assert.equal(r.fixes.length, 0)
    assert.equal(r.text, ASR_TEXT)
  })

  it('名单里**同时**有同音两人（林岚 + 林蓝），且错词不在名单 → 不改', () => {
    // ⚠ 第一版这个用例写成「名单里有 林岚 + 林兰，文本里是 林兰」——
    //   但那样错词**本身就在名单里**，会被 nameSet 检查先拦下，
    //   「同音多人则不改」那条分支**一次都没被执行到**（变异 V1 仍绿）。
    //   现在让错词不在名单里，才真正走到 cands.length !== 1。
    const r = repairRosterHomophones('林兰今天没来', ['林岚', '林蓝'], TABLE)
    assert.equal(r.fixes.length, 0, `同音多人时不该猜：${JSON.stringify(r.fixes)}`)
    assert.equal(r.text, '林兰今天没来')
  })

  it('同音只有一个人时**仍会修**（证明上一条不是因为「一律不改」而绿）', () => {
    const r = repairRosterHomophones('林兰今天没来', ['林岚'], TABLE)
    assert.equal(r.text, '林岚今天没来', '单候选必须修——否则上一条就是假绿')
  })

  it('非人名专名「悬借」「公投」保持原样，且不得被"纠正"成同音的另一种写法', () => {
    // ⚠ 第一版把语义写反了：写成 `!text.includes('悬借')`，
    //   而正确语义是「保持原样」。判据红了先问量具——算法是对的，断言反了。
    const r = repairRosterHomophones(ASR_TEXT, ['张伟', '林岚', '王总'], TABLE)
    assert.ok(r.text.includes('悬借芯片'), `「悬借芯片」应原样保留：${r.text}`)
    assert.ok(r.text.includes('公投安装'), `「公投安装」应原样保留：${r.text}`)
    assert.ok(!r.text.includes('玄戒'), `不该引入名单外的「玄戒」：${r.text}`)
    assert.ok(!r.text.includes('弓头'), `不该引入名单外的「弓头」：${r.text}`)
  })

  it('同音专名在**名单里**时才会被改（对照上一条，证明判据在读名单）', () => {
    const r = repairRosterHomophones(ASR_TEXT, ['张伟', '林岚', '王总', '玄戒', '弓头'], TABLE)
    assert.ok(r.text.includes('玄戒芯片'), `名单里有「玄戒」就应被改对：${r.text}`)
    assert.ok(r.text.includes('弓头安装'), `名单里有「弓头」就应被改对：${r.text}`)
  })

  it('名单里出现的正确写法本身绝不被改', () => {
    const r = repairRosterHomophones('张伟和林岚都在', ['张伟', '林岚'], TABLE)
    assert.equal(r.text, '张伟和林岚都在')
  })
})

describe('不可判定就老实不动', () => {
  it('★ 名单里有拼音判不了的名字 ⇒ **整体放弃修正**（表不全不得当作只有一个候选）', () => {
    // 这条锁的是变异验证里发现的真实缺陷：旧实现把「林蓝」静默 filter 掉，
    // 于是「林兰」被自信地改成「林岚」——而真答案可能是「林蓝」。
    const r = repairRosterHomophones('林兰今天没来', ['林岚', '林萌'], TABLE) // 「萌」不在表里
    assert.equal(r.fixes.length, 0, `表不全时不得猜：${JSON.stringify(r.fixes)}`)
    assert.equal(r.text, '林兰今天没来')
  })

  it('★ 是汉字但拼音表里没有 ⇒ 整串判不了，不改（不猜）', () => {
    // ⚠ 第一版用 'Ω' 当缺字样例，但它不在 CJK 区，**先被 isHan 拦下**，
    //   缺字分支从未被执行（变异 V4 仍绿）。'鑫' 在 CJK 区且不在表里，
    //   才真正走到「查不到 ⇒ 返回 null」那条。
    assert.equal(isHan('鑫'), true, '前置：鑫必须是汉字，否则测不到缺字分支')
    assert.equal(pinyinOf('林鑫', TABLE), null, '缺字必须返回 null 而不是部分结果')
    const r = repairRosterHomophones('林鑫兰今天来', ['林岚'], TABLE)
    assert.equal(r.text, '林鑫兰今天来', `含缺字时不得改动：${r.text}`)
  })

  it('★ 非汉字即使**误进了拼音表**也必须被拦（isHan 与 !p 是两道独立防线）', () => {
    // ⚠ 第一版只测了「非汉字不在表里」，而那时 isHan 与 `if (!p)` 是**冗余的**：
    //   删掉 isHan 仍会绿（变异 V5）。要判别 isHan，就得让表里**真的有**非汉字键。
    const tainted: PinyinTable = { ...TABLE, Ω: 'lin2', '1': 'lan2' }
    assert.equal(isHan('Ω'), false)
    assert.equal(pinyinOf('林Ω', tainted), null, '非汉字即便在表里也必须判不了')
    const r = repairRosterHomophones('林Ω', ['林岚'], tainted)
    assert.equal(r.text, '林Ω', '含非汉字窗口时不得替换')
  })

  it('非汉字不参与拼音判定', () => {
    assert.equal(pinyinOf('林Ω', TABLE), null, '非汉字必须返回 null')
    const r = repairRosterHomophones('Lin2 Lan 2026-10-06', ['林岚'], TABLE)
    assert.equal(r.text, 'Lin2 Lan 2026-10-06')
  })

  it('空名单 / 空文本 / 名单全是表外字 ⇒ 原样返回', () => {
    assert.equal(repairRosterHomophones(ASR_TEXT, [], TABLE).text, ASR_TEXT)
    assert.equal(repairRosterHomophones('', ['林岚'], TABLE).text, '')
    assert.equal(repairRosterHomophones('林兰', ['鑫'], TABLE).text, '林兰')
  })

  it('isHan 边界', () => {
    assert.equal(isHan('林'), true)
    assert.equal(isHan('a'), false)
    assert.equal(isHan('。'), false)
    assert.equal(isHan(''), false)
  })
})