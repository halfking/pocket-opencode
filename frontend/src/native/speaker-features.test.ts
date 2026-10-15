// speaker-features.test.ts —— 说话人特征（log-mel）的行为门。
//
// ══ 为什么这道门以前不存在 ══
// `speaker-embedding.ts` 顶部是 `import { sherpa } from './sherpa'`——**无扩展名**
// ⇒ Node 的 ESM 解析器直接 ERR_MODULE_NOT_FOUND ⇒ 整条说话人链路在 Node 里
// 一行都跑不起来 ⇒ 从来没有任何门守着它。
// 现在特征与判据都落在零 import 的 `speaker-features.ts` 里，这道门才写得出来。
//
// ══ 门守的是什么缺陷（2026-10-06 实测）══
// 旧实现 `spectrumEmbedding` 取的是**时域幅度包络**（32 个时间块的平均绝对值），
// 对频谱完全不敏感 ⇒ 对音色（共振峰）零敏感。实测：
//   同一包络、基频差 2.4 倍（男110Hz vs 女260Hz）→ cosine 0.998473（判成同一人）
//   不同的人、不同包络                     → cosine 0.241667（判成不同人）
// 它实际在测「这两段话**的响度节奏**像不像」，产品要的是「这是不是**同一个嗓音**」。
//
// 下面两条门是**方向性**的：把特征改回时域包络，它们立刻转红；
// 反过来只测「同人 vs 异人的区间顺序」是不够的——旧实现在我第一版夹具上
// 也可能侥幸满足那个顺序（它测的也是包络），所以必须用下面这两条。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  blobToEmbedding,
  cosineSimilarity,
  embeddingToBlob,
  isUsableEmbedding,
  logMelEmbedding,
  EMBED_DIM,
  MEL_BANDS,
} from './speaker-features.ts'
import { FEMALE, MALE, synthVoice } from './__tests__/synth-voice.ts'
import { SAME_SPEAKER_MIN } from './speaker-diarization.ts'

/** 阈值必须留出余量：判据断言的是「会不会被合并」，不是精确相似度。 */
const MERGE = SAME_SPEAKER_MIN
const SPLIT = SAME_SPEAKER_MIN

describe('speaker-features · 音色判别（核心回归）', () => {
  it('门自证：夹具与特征都能跑，且不是恒零', () => {
    const e = logMelEmbedding(synthVoice({ ...MALE, syllables: 7, seed: 1 }))
    assert.equal(e.length, EMBED_DIM)
    assert.ok(isUsableEmbedding(e), '合成语音必须算出可用特征，否则下面全是恒真')
    assert.ok(
      e.some((x) => Math.abs(x) > 1e-6),
      '特征不能全为零——否则后面的相似度恒为 0，所有断言一起失效',
    )
  })

  it('同一个幅度包络、音色完全不同的两个人 ⇒ 必须判为不同说话人', () => {
    // 同一个 syllables/seed ⇒ 包络逐点相同；只有 f0 与共振峰不同。
    // 旧实现下这里是 0.998473 ≥ 阈值 ⇒ 两人被并成一个人。
    const a = logMelEmbedding(synthVoice({ ...MALE, syllables: 7, seed: 1 }))
    const b = logMelEmbedding(synthVoice({ ...FEMALE, syllables: 7, seed: 1 }))
    const sim = cosineSimilarity(a, b)
    assert.ok(
      sim < SPLIT,
      `音色不同却被判成同一人（cosine=${sim.toFixed(6)} ≥ ${SPLIT}）——特征退化成时域包络了`,
    )
  })

  it('同一个人、说话节奏完全不同 ⇒ 必须判为同一人', () => {
    // 音节数与包络种子都不同 ⇒ 时域包络差异极大。
    // 旧实现下这里是 0.24 上下 ⇒ 一个人被拆成好几个说话人。
    const a = logMelEmbedding(synthVoice({ ...MALE, syllables: 7, seed: 1 }))
    const b = logMelEmbedding(synthVoice({ ...MALE, syllables: 10, seed: 4 }))
    const sim = cosineSimilarity(a, b)
    assert.ok(
      sim >= MERGE,
      `同一人被拆开了（cosine=${sim.toFixed(6)} < ${MERGE}）——特征里没有音色信息`,
    )
  })

  it('可分性：同人的最低相似度必须高于异人的最高相似度', () => {
    const male = [1, 3, 5].map((seed, i) =>
      logMelEmbedding(synthVoice({ ...MALE, syllables: 6 + i * 2, seed })),
    )
    const female = [2, 4, 6].map((seed, i) =>
      logMelEmbedding(synthVoice({ ...FEMALE, syllables: 7 + i * 2, seed })),
    )
    const same = [cosineSimilarity(male[0], male[1]), cosineSimilarity(male[0], male[2]), cosineSimilarity(male[1], male[2]),
      cosineSimilarity(female[0], female[1]), cosineSimilarity(female[0], female[2]), cosineSimilarity(female[1], female[2])]
    const diff = male.flatMap((m) => female.map((f) => cosineSimilarity(m, f)))
    const minSame = Math.min(...same)
    const maxDiff = Math.max(...diff)
    console.log(`      同人 ${minSame.toFixed(4)}~${Math.max(...same).toFixed(4)} / 异人 ${minDiff2(diff)}~${maxDiff.toFixed(4)}`)
    function minDiff2(a: number[]) { return Math.min(...a).toFixed(4) }
    assert.ok(
      minSame > maxDiff,
      `两个区间重叠：同人最低 ${minSame.toFixed(4)} ≤ 异人最高 ${maxDiff.toFixed(4)}`,
    )
  })

  it('阈值落在实测的两个区间之间（阈值与特征不许脱钩）', () => {
    const male = [1, 3, 5].map((seed, i) =>
      logMelEmbedding(synthVoice({ ...MALE, syllables: 6 + i * 2, seed })),
    )
    const female = [2, 4, 6].map((seed, i) =>
      logMelEmbedding(synthVoice({ ...FEMALE, syllables: 7 + i * 2, seed })),
    )
    const same = [cosineSimilarity(male[0], male[1]), cosineSimilarity(male[0], male[2]), cosineSimilarity(male[1], male[2]),
      cosineSimilarity(female[0], female[1]), cosineSimilarity(female[0], female[2]), cosineSimilarity(female[1], female[2])]
    const diff = male.flatMap((m) => female.map((f) => cosineSimilarity(m, f)))
    assert.ok(
      SAME_SPEAKER_MIN > Math.max(...diff),
      `阈值 ${SAME_SPEAKER_MIN} 没高于异人上界 ${Math.max(...diff).toFixed(4)} ⇒ 会把不同的人合并`,
    )
    assert.ok(
      SAME_SPEAKER_MIN <= Math.min(...same),
      `阈值 ${SAME_SPEAKER_MIN} 高于同人下界 ${Math.min(...same).toFixed(4)} ⇒ 会把同一个人拆开`,
    )
  })

  it('音量差 12dB 不影响身份判定（CMVN）', () => {
    const loud = logMelEmbedding(synthVoice({ ...MALE, syllables: 7, seed: 1, gain: 0.03 }))
    const quiet = logMelEmbedding(synthVoice({ ...MALE, syllables: 9, seed: 2, gain: 0.03 / 4 }))
    const sim = cosineSimilarity(loud, quiet)
    assert.ok(
      sim >= MERGE,
      `音量差把同一个人拆开了（cosine=${sim.toFixed(6)}）——减均值归一被去掉了`,
    )
  })

  it('向量均值为 0（CMVN 真正保证的那条性质）', () => {
    // ⚠ 上面那条**区分不出** CMVN：撤掉减均值后，音量 ÷2/÷4/÷8 时相似度
    //   只降 0.000~0.003，要到 ÷64 才跌破阈值。所以这里断言 CMVN 自身的
    //   不变式 —— 「特征只编码谱的形状，不编码绝对电平」。
    //   撤掉减均值后各维会整体停在 ≈ -21，本门立刻转红。
    const e = logMelEmbedding(synthVoice({ ...FEMALE, syllables: 8, seed: 3 }))
    const mean = Array.from(e).reduce((a, b) => a + b, 0) / e.length
    assert.ok(Math.abs(mean) < 1e-6, `向量均值应≈0，实际 ${mean}`)
  })

  it('逐帧 CMVN 把「同人下界 − 异人上界」撑开 4 倍', () => {
    // 这道门测的是**逐帧** CMVN（每帧减自己的 24 维均值），
    // 与上面那条全局 CMVN 不是一回事：撤掉它，上面那条仍然是绿的。
    //
    // 实测（合成语音，3 男 + 3 女各 3 句）：
    //   撤掉逐帧 CMVN → 同人 ≥0.9999，异人 <0.9848，窗口 0.0151
    //   保留         → 同人 ≥0.9989，异人 <0.9325，窗口 0.0664
    //
    // 它去掉的是**帧级**的电平/谱倾斜差异，也就是「麦克风离嘴的距离、
    // 这一秒说话响一点」这类与音色无关的变化。
    const male = [1, 3, 5].map((s, i) => logMelEmbedding(synthVoice({ ...MALE, syllables: 6 + i * 2, seed: s })))
    const female = [2, 4, 6].map((s, i) => logMelEmbedding(synthVoice({ ...FEMALE, syllables: 7 + i * 2, seed: s })))
    const same = [
      cosineSimilarity(male[0], male[1]), cosineSimilarity(male[0], male[2]), cosineSimilarity(male[1], male[2]),
      cosineSimilarity(female[0], female[1]), cosineSimilarity(female[0], female[2]), cosineSimilarity(female[1], female[2]),
    ]
    const diff = male.flatMap((m) => female.map((f) => cosineSimilarity(m, f)))
    const window = Math.min(...same) - Math.max(...diff)
    console.log(
      `      分离窗口 = ${window.toFixed(4)}（同人≥${Math.min(...same).toFixed(4)} / 异人<${Math.max(...diff).toFixed(4)}）`,
    )
    assert.ok(
      window > 0.04,
      `分离窗口只有 ${window.toFixed(4)} —— 逐帧 CMVN 疑似被去掉（撤掉后约 0.015）`,
    )
  })
})

describe('speaker-features · 无信息向量的识别', () => {
  it('空数组 / 零向量 / NaN / null 一律判为不可用', () => {
    assert.equal(isUsableEmbedding(null), false)
    assert.equal(isUsableEmbedding(undefined), false)
    assert.equal(isUsableEmbedding(new Float32Array(0)), false)
    assert.equal(isUsableEmbedding(new Float32Array(48)), false, '全零必须判为不可用')
    const nan = new Float32Array(48).fill(0.5)
    nan[7] = NaN
    assert.equal(isUsableEmbedding(nan), false, '含 NaN 必须判为不可用')
    assert.equal(isUsableEmbedding(new Float32Array(48).fill(0.5)), true)
  })

  it('门自证：全零向量若被判为可用，这道门就恒红了', () => {
    // 反向对照：isUsableEmbedding 不能恒假，否则它把真向量也拒了。
    assert.ok(isUsableEmbedding(logMelEmbedding(synthVoice({ ...FEMALE, syllables: 8, seed: 9 }))))
  })

  it('短于一帧的输入算出零向量，而不是一个诡异的特征', () => {
    const tiny = logMelEmbedding(new Float32Array(64))
    assert.equal(tiny.length, EMBED_DIM)
    assert.equal(isUsableEmbedding(tiny), false)
  })

  it('整段静音不得产出 NaN', () => {
    // 这是一条**回归网**而不是带牙的门：`used >= 1` 由「gate 取自 energy
    // 自身的分位数」构造保证，所以曾经写的 `if (used === 0) return out`
    // 守卫变异掉之后这里仍是绿的 —— 正因如此它被当作死代码删了。
    // 留着这道断言是为了防止将来有人把分位数改成会让 used 归零的算法。
    // NaN 与「全零」不同：全零能被 isUsableEmbedding 挡在聚类之外，
    // 而 NaN 会一路渗进 profile 的滑动平均，之后**永久**污染每一次比较。
    const silence = logMelEmbedding(new Float32Array(16000))
    assert.ok(
      Array.from(silence).every(Number.isFinite),
      `静音输入产出了非有限值：${Array.from(silence).filter((x) => !Number.isFinite(x)).length} 个`,
    )
  })
})

describe('speaker-features · 跨维度比对', () => {
  it('维度不同返回 -1（不可比），而不是截断比较', () => {
    const a = new Float32Array([1, 0, 0])
    const b = new Float32Array([1, 0, 0, 0, 0])
    assert.equal(cosineSimilarity(a, b), -1)
  })

  it('-1 必须与「正交的 0」区分得开', () => {
    // 门自证：若不可比也返回 0，下面这条就无法与「相似度为 0」区分，
    // 调用方就分不清该「跳过」还是该「新建说话人」。
    const orth = new Float32Array([0, 1])
    const a = new Float32Array([1, 0])
    const b = new Float32Array([1, 0, 0])
    assert.equal(cosineSimilarity(a, orth), 0)
    assert.notEqual(cosineSimilarity(a, b), 0)
  })

  it('原生 ECAPA 向量与 Web 特征不被误判为同一人', () => {
    const web = logMelEmbedding(synthVoice({ ...MALE, syllables: 7, seed: 1 }))
    const ecapa = new Float32Array(192)
    for (let i = 0; i < 192; i++) ecapa[i] = Math.sin(i * 0.7) / 3
    const norm = Math.hypot(...Array.from(ecapa))
    for (let i = 0; i < 192; i++) ecapa[i] /= norm
    assert.equal(cosineSimilarity(ecapa, web), -1)
  })
})

describe('speaker-features · 序列化', () => {
  it('blobToEmbedding 对非 4 整除长度不抛 RangeError', () => {
    assert.doesNotThrow(() => blobToEmbedding(new Uint8Array(11)))
    assert.equal(blobToEmbedding(new Uint8Array(11)).length, 2)
    assert.equal(blobToEmbedding(new Uint8Array(3)).length, 0)
  })

  it('blobToEmbedding 对未按 4 对齐的视图也不抛（这才是真正的旧缺陷）', () => {
    // ⚠ 实测结论，纠正了一个错误判断：
    //   「byteLength/4 是小数会抛 Invalid typed array length」是**错的**
    //   （V8 直接截断，byteLength=11 照样返回 length=2）。
    //   真正会炸的是**偏移未对齐**：byteOffset=3 时抛
    //   `RangeError: start offset of Float32Array should be a multiple of 4`，
    //   而这行在 loadSpeakerProfiles 的 map 里 ⇒ 一次错位就让整场
    //   录音的说话人列表加载失败，外层 catch 把它吞成「空库」。
    const backing = new Uint8Array(20)
    const misaligned = new Uint8Array(backing.buffer, 3, 12)
    assert.equal(misaligned.byteOffset % 4, 3, '夹具没造出未对齐的视图')
    assert.doesNotThrow(() => blobToEmbedding(misaligned))
    assert.equal(blobToEmbedding(misaligned).length, 3)
  })

  it('门自证：未对齐视图在旧写法下真的会抛（否则上面那道门是假的）', () => {
    const backing = new Uint8Array(20)
    const misaligned = new Uint8Array(backing.buffer, 3, 12)
    assert.throws(
      () => new Float32Array(misaligned.buffer, misaligned.byteOffset, misaligned.byteLength / 4),
      RangeError,
      '旧写法没抛 ⇒ 说明这道门测不到真缺陷',
    )
  })

  it('往返一致', () => {
    const v = logMelEmbedding(synthVoice({ ...FEMALE, syllables: 8, seed: 2 }))
    const back = blobToEmbedding(embeddingToBlob(v))
    assert.equal(back.length, v.length)
    assert.ok(Math.abs(cosineSimilarity(v, back) - 1) < 1e-6)
  })
})

describe('speaker-features · 结构自证', () => {
  it('维度 = mel 带数 × 2（均值 + 标准差）', () => {
    assert.equal(EMBED_DIM, MEL_BANDS * 2)
  })
})
