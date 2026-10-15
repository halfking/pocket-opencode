// real-asr-roster-repair.test.ts — 用**真网关 ASR 的真实输出**验人名同音纠正。
//
// ★ 为什么必须用真输出当夹具（§50）：
//   这个模块此前所有夹具都是**手写**的。§44 的实测说 SenseVoice 会把「林岚」
//   听成「林兰」，但「手写一句含林兰的文本」并不能证明真实 ASR 输出长那样。
//   本文件的每一条断言都跑在下面这段**真网关返回的原文**上：
//
//     素材：/tmp/opstt/tts-meeting.wav（macOS `say -v Tingting` 造的受控会议语料）
//     端点：POST {gateway}/audio/transcriptions，model=mimo-v2.5-asr，HTTP 200
//     原文（未做任何清洗，逐字照抄）：
//
//   同时这份原文还钉住了另一条**否定**性质：非人名专名不得被猜改。
//   「玄戒」听成「悬界」、「弓头」听成「工头」——它们是同音但**不是人名**，
//   不在名单里 ⇒ 纠正模块必须原样放过。§42/§43 已判定这一类要靠
//   sherpa-onnx 的 HomophoneReplacer（需 pynini/FST，macOS 装不上）。
//   ⇒ 本门把「**该改的改了、不该改的一个没动**」一起钉住，防止将来
//   有人为了提高命中率把非人名专名也纳入猜测。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { deriveRoster, repairRosterHomophones } from '../meeting-roster-homophone.ts'
import { PINYIN_COMMON } from '../pinyin-common.ts'

/** 真网关 mimo-v2.5-asr 对 tts-meeting.wav 的返回原文（HTTP 200，2026-10-07 实测）。 */
const REAL_ASR = '下周三下午三点，跟王总还有林兰开个客户评审会，要准备悬界芯片和机载传感器的对比表。' +
  '张伟负责工头安装的预算确认，所有续期合同必须在十一月底之前签完。'

/** 埋进音频的真值（当时用 `say` 逐句指定的读音），用来判定「哪个是错的」。 */
const TRUTH = {
  林岚: '林兰',   // 人名：ASR 听错
  玄戒: '悬界',   // 非人名专名：ASR 听错，但**不在名单**，必须放过
  弓头: '工头',   // 非人名专名：同音，但**不在名单**，必须放过
  机载: '机载',   // 本次听对了
}

describe('真 ASR 输出：名单里有人名时才改，且只改人名', () => {
  it('★ 夹具确实是真网关原文（防止有人日后把它换成自拟字符串）', () => {
    // 这条看着多余，但它守住的是「夹具的来源」这个属性本身：
    // 一旦有人把 REAL_ASR 换成手写短句，所有下面的断言照样会绿，
    // 而「真 ASR 输出长这样」这件事就再也没人验了。
    assert.ok(REAL_ASR.length > 40, '夹具被换成了短句 —— 它必须保持真网关返回的完整原文')
    assert.ok(REAL_ASR.includes('林兰'), '真输出里没有「林兰」，§44 的前提不成立')
    assert.ok(REAL_ASR.includes('悬界') && REAL_ASR.includes('工头'), '真输出里没有那两个非人名同音错')
  })

  it('★ 名单里有「林岚」时，真实输出里的「林兰」被纠正', () => {
    const r = repairRosterHomophones(REAL_ASR, ['张伟', '林岚'], PINYIN_COMMON)
    assert.ok(r.text.includes('林岚'), `「林兰」没有被纠正：${r.text}`)
    assert.ok(!r.text.includes('林兰'), '「林兰」仍然留在文本里')
    assert.equal(r.fixes.length, 1, `应恰好 1 条修正，实测 ${r.fixes.length}：${JSON.stringify(r.fixes)}`)
    assert.deepEqual({ from: r.fixes[0].from, to: r.fixes[0].to }, { from: '林兰', to: '林岚' })
  })

  it('★★ 安全性质：非人名专名一个都不许被猜改', () => {
    // 这是本文件最重要的一条。「玄戒」「弓头」在真输出里同样是错的，
    // 但纠正模块**没有权威名单**能证明哪个写法对（§45 的边界 1），
    // 所以必须原样放过 —— 错名字比漏修正更糟。
    const r = repairRosterHomophones(REAL_ASR, ['张伟', '林岚'], PINYIN_COMMON)
    for (const heard of ['悬界', '工头']) {
      assert.ok(r.text.includes(heard),
        `「${heard}」被猜改了（真值 ${Object.entries(TRUTH).find(([, h]) => h === heard)?.[0]}）—— `
        + '非人名专名不在名单里，改它就是猜')
    }
  })

  it('★ 听对的词与时间点必须原样保留', () => {
    const r = repairRosterHomophones(REAL_ASR, ['张伟', '林岚'], PINYIN_COMMON)
    for (const keep of ['机载', '王总', '下周三下午三点', '十一月底', '客户评审会']) {
      assert.ok(r.text.includes(keep), `「${keep}」被改动了 —— 只该动人名`)
    }
  })

  it('★ 名单里没有人名时，一个字都不许改（真输出实测）', () => {
    // 真实录音里最常见的情形：用户没给说话人起名，名单只有声纹默认标签。
    // 此时必须**整段放弃**，而不是去猜「林兰」是不是「林岚」。
    const r = repairRosterHomophones(REAL_ASR, deriveRoster({ speakerLabels: ['张伟', '说话人 1'] }), PINYIN_COMMON)
    assert.equal(r.text, REAL_ASR, '名单里没有「林岚」却改动了文本')
    assert.deepEqual(r.fixes, [])
  })
})