// speaker-diarization.test.ts —— 说话人聚类判据的行为门。
//
// ══ 这道门以前不存在，而且是被上一道门的结论逼出来的 ══
// `speaker-diarization.ts` 原先 `import { cosineSimilarity } from './speaker-embedding'`，
// 而后者又 import 无扩展名的 './sherpa' ⇒ Node 里整条链路跑不起来。
// 现在判据依赖改指零 import 的 `speaker-features.ts`，这道门才写得出来。
//
// ══ 门守的四个真实缺陷（2026-10-06）══
// ① 阈值 0.72 与「时域包络」特征绑定。换成 log-mel 后**任意两段语音都 > 0.97**，
//    沿用 0.72 ⇒ 整场会议合并成一个人。
// ② `ingest-speech.ts` 的 `.catch(() => new Float32Array())`：
//    余弦对零向量恒 0 < 阈值 ⇒ **每次提取失败都新建一个说话人** ⇒
////    一个人被标成「说话人 1…30」。
// ③ `cosineSimilarity` 用 `min(len)` 截断比较：192 维 ECAPA 与 48 维特征
//    拿前 48 维比，实测同一人 0.2298 ⇒ 声纹库里注册过的人在 Web 上
//    每段都是新说话人。
// ④ 滑动平均按 `best.embedding.length` 遍历，长度不等时多读出 undefined
//    ⇒ NaN 污染该 profile 之后**每一次**比较。
//
// ⚠ 每条门都配了负控：反向对照 / 门自证 / 逐条件覆盖，
//   免得判据自身恒真或恒假。
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  AMBIGUITY_MARGIN,
  SAME_SPEAKER_MIN,
  SpeakerDiarizer,
  type SpeakerProfile,
} from './speaker-diarization.ts'
import { cosineSimilarity, isUsableEmbedding, logMelEmbedding } from './speaker-features.ts'
import { FEMALE, MALE, synthVoice, withNoise } from './__tests__/synth-voice.ts'

const maleVec = (seed: number, syl = 7) =>
  logMelEmbedding(synthVoice({ ...MALE, syllables: syl, seed }))
const femaleVec = (seed: number, syl = 8) =>
  logMelEmbedding(synthVoice({ ...FEMALE, syllables: syl, seed }))

/** 归一化的单位向量，用于构造受控的相似度。 */
function unit(v: number[]): Float32Array {
  const n = Math.hypot(...v)
  return new Float32Array(v.map((x) => x / n))
}

describe('speaker-diarization · 基础归并', () => {
  it('同一个人反复说话 ⇒ 同一个 profile，不新建', () => {
    const d = new SpeakerDiarizer()
    const a = d.identify(maleVec(1))
    const b = d.identify(maleVec(3, 9))
    const c = d.identify(maleVec(5, 6))
    assert.equal(b.profileId, a.profileId)
    assert.equal(c.profileId, a.profileId)
    assert.equal(b.isNew, false)
    assert.equal(d.getProfiles().length, 1)
  })

  it('两个不同的人 ⇒ 两个 profile，标签不同', () => {
    const d = new SpeakerDiarizer()
    const a = d.identify(maleVec(1))
    const b = d.identify(femaleVec(2))
    assert.notEqual(b.profileId, a.profileId)
    assert.equal(b.isNew, true)
    assert.notEqual(b.label, a.label)
    assert.equal(d.getProfiles().length, 2)
  })

  it('门自证：新增的人拿到递增编号，不与已有标签相撞', () => {
    const d = new SpeakerDiarizer()
    d.identify(maleVec(1))
    d.identify(femaleVec(2))
    const labels = d.getProfiles().map((p) => p.label)
    assert.deepEqual(new Set(labels).size, labels.length, `标签撞车：${labels.join(',')}`)
  })

  it('滑动平均后向量仍然归一化，且不含 NaN', () => {
    const d = new SpeakerDiarizer()
    d.identify(maleVec(1))
    d.identify(maleVec(3, 10))
    d.identify(maleVec(5, 6))
    const p = d.getProfiles()[0]
    assert.ok(isUsableEmbedding(p.embedding), '合并后必须仍是可用向量')
    const norm = Math.hypot(...Array.from(p.embedding))
    assert.ok(Math.abs(norm - 1) < 1e-5, `合并后范数应≈1，实际 ${norm}`)
  })
})

describe('speaker-diarization · 提取失败不再制造新说话人', () => {
  it('identify(null) 反复调用 ⇒ 同一个兜底标签，且 known=false', () => {
    // 旧行为：每次都新建 ⇒ 一个人 30 段话被标成「说话人 1…30」。
    const d = new SpeakerDiarizer()
    const a = d.identify(null)
    const b = d.identify(null)
    const c = d.identify(null)
    assert.equal(b.profileId, a.profileId)
    assert.equal(c.profileId, a.profileId)
    assert.equal(a.known, false, '「没区分出来」不许被当成「识别成功」')
  })

  it('零向量与空数组走同一条兜底路径', () => {
    const d = new SpeakerDiarizer()
    const a = d.identify(new Float32Array(0))
    const b = d.identify(new Float32Array(48))
    const c = d.identify(null)
    assert.equal(b.profileId, a.profileId)
    assert.equal(c.profileId, a.profileId)
  })

  it('兜底 profile 不进 getProfiles（否则它会占掉编号）', () => {
    const d = new SpeakerDiarizer()
    d.identify(null)
    d.identify(null)
    assert.equal(d.getProfiles().length, 0)
    const real = d.identify(maleVec(1))
    assert.equal(real.label, '说话人 1', '兜底不该把编号推走')
  })

  it('用户给兜底标签改名后要跟着变（否则改的是空气）', () => {
    const d = new SpeakerDiarizer()
    const a = d.identify(null)
    d.labelProfile(a.profileId, '张三')
    assert.equal(d.identify(null).label, '张三')
  })

  it('拿到真实特征后，兜底标签不会被误当成一次成功的判别', () => {
    const d = new SpeakerDiarizer()
    d.identify(null)
    const r = d.identify(maleVec(1))
    assert.equal(r.known, true)
    assert.notEqual(r.profileId, 'sp-unknown')
  })
})

describe('speaker-diarization · 跨维度不误判', () => {
  it('历史库里维度不符的 profile 被清扫，不会让每段都新建说话人', () => {
    const d = new SpeakerDiarizer()
    const stale: SpeakerProfile[] = [{
      id: 'vp-old', label: '张三', sampleCount: 3,
      embedding: new Float32Array(32).fill(0.1), // 旧版时域包络特征
    }]
    d.loadProfiles(stale)
    const ids = [1, 3, 5].map((s) => d.identify(maleVec(s)).profileId)
    assert.equal(new Set(ids).size, 1, `死重 profile 让每段都成了新说话人：${ids.join(',')}`)
  })

  it('loadProfiles 不丢弃维度不同但有效的 profile（原生 ECAPA 合法）', () => {
    // ⚠ 反向对照：清扫**只能**发生在「知道当前维度」之后。
    //   loadProfiles 时还不知道这次是不是原生设备，按固定维度筛会
    //   把合法的 192 维声纹一起删掉。
    const d = new SpeakerDiarizer()
    const dropped = d.loadProfiles([{
      id: 'vp-native', label: '李四', sampleCount: 1,
      embedding: new Float32Array(192).fill(0.05),
    }])
    assert.equal(dropped, 0)
  })

  it('loadProfiles 丢弃零向量行并报数', () => {
    const d = new SpeakerDiarizer()
    const dropped = d.loadProfiles([
      { id: 'a', label: '甲', sampleCount: 1, embedding: new Float32Array(0) },
      // ⚠ 必须给**非零**向量：全零向量按设计也算「提取失败写进库的行」。
      //   这里曾写成 `new Float32Array(48)`，实测 dropped=2 —— 门禁当场
      //   咬住了测试自己，也顺带证明 isUsableEmbedding 真的在生效。
      { id: 'b', label: '乙', sampleCount: 1, embedding: maleVec(2) },
    ])
    assert.equal(dropped, 1)
    assert.equal(d.getProfiles().length, 1)
    assert.equal(d.getProfiles()[0].id, 'b')
  })

  it('不同维度不参与 max 比较（-1 抢不走最优候选）', () => {
    const d = new SpeakerDiarizer()
    d.loadProfiles([
      { id: 'vp-x', label: '王五', sampleCount: 1, embedding: new Float32Array(192).fill(0.05) },
    ])
    const first = d.identify(maleVec(1))
    const second = d.identify(maleVec(3, 9))
    assert.equal(second.profileId, first.profileId, '不可比的 profile 抢走了 best，导致每段新建')
  })

  it('维度清扫只发生在内存里：库里那条仍可被读回', () => {
    // 清扫不回写数据库 ⇒ 换到原生设备时那条 192 维声纹还在，
    // 下次 loadProfiles 照样读得到。（否则等于替用户删了声纹。）
    const row: SpeakerProfile = {
      id: 'vp-native', label: '李四', sampleCount: 1,
      embedding: new Float32Array(192).fill(0.05),
    }
    const d = new SpeakerDiarizer()
    d.loadProfiles([row])
    d.identify(maleVec(1))
    assert.equal(d.getProfile('vp-native'), undefined, '本次会话内已被清扫')
    const fresh = new SpeakerDiarizer()
    assert.equal(fresh.loadProfiles([row]), 0, '重新载入时不该再丢')
  })

  it('清扫后所有 profile 与本次特征同维度（滑动平均的前置不变式）', () => {
    // mergeInto 里的维度守卫因此**不可达**（清扫已保证一致），
    // 所以这里断言的是那条真正的不变式，而不是去够那个守卫：
    // 一旦有人删掉 settleDimension，这道门会红。
    const d = new SpeakerDiarizer()
    d.loadProfiles([
      { id: 'vp-x', label: '王五', sampleCount: 1, embedding: new Float32Array(192).fill(0.05) },
      { id: 'vp-y', label: '赵六', sampleCount: 1, embedding: new Float32Array(32).fill(0.1) },
    ])
    d.identify(maleVec(1))
    for (const p of d.getProfiles()) {
      assert.equal(p.embedding.length, maleVec(1).length, `${p.id} 维度不一致`)
      assert.ok(isUsableEmbedding(p.embedding), `${p.id} 被写坏了`)
    }
  })
})

describe('speaker-diarization · 合并的保守性', () => {
  it('相似度不足 ⇒ 新建说话人（宁可多标签，不可错配人）', () => {
    // 阈值由特征标定，门禁只验证「它确实在起作用」：
    // 把阈值调到 1.0（任何真实语音都达不到）后，同一人也必须被拆开。
    const strict = new SpeakerDiarizer(1.0)
    const a = strict.identify(maleVec(1))
    const b = strict.identify(maleVec(3, 9))
    assert.notEqual(b.profileId, a.profileId, '阈值不生效：同一人没有被拆开')
  })

  it('只有一个可比候选时不做歧义否决（同一人前两段必须能合并）', () => {
    const d = new SpeakerDiarizer()
    const a = d.identify(maleVec(1))
    const b = d.identify(maleVec(3, 9))
    assert.equal(b.profileId, a.profileId, '单候选场景被误当成「有歧义」')
  })

  it('两个候选几乎并列 ⇒ 不合并（歧义规则有牙）', () => {
    const d = new SpeakerDiarizer()
    const base = unit([1, 2, 3, 4, 5, 6])
    const perturb = unit([6, -5, 4, -3, 2, -1])
    const plus = new Float32Array(6)
    const minus = new Float32Array(6)
    for (let i = 0; i < 6; i++) {
      plus[i] = base[i] + 0.05 * perturb[i]
      minus[i] = base[i] - 0.05 * perturb[i]
    }
    norm(plus); norm(minus)
    d.loadProfiles([
      { id: 'p1', label: '甲', sampleCount: 1, embedding: plus },
      { id: 'p2', label: '乙', sampleCount: 1, embedding: minus },
    ])
    const simA = cosineSimilarity(base, plus)
    const simB = cosineSimilarity(base, minus)
    assert.ok(Math.abs(simA - simB) < AMBIGUITY_MARGIN, `夹具没造出并列（${simA} vs ${simB}）`)
    const r = d.identify(base)
    assert.equal(r.isNew, true, '并列候选被强行并进了某一个 ⇒ 错配')
  })

  it('AMBIGUITY_MARGIN 是正数（门自证：间隔为 0 等于没有规则）', () => {
    assert.ok(AMBIGUITY_MARGIN > 0)
    assert.ok(SAME_SPEAKER_MIN > 0 && SAME_SPEAKER_MIN <= 1)
  })
})

describe('speaker-diarization · 噪声下的失败方向', () => {
  it('有环境声时，同一个人被拆开是可接受的；两个人被并在一起不可接受', () => {
    // 这道门锁的是**代价不对称**本身，不是识别精度。
    // 实测：底噪 0.001 起同一人相似度就跌破 0.99（见 SAME_SPEAKER_MIN 注释），
    // 所以真机在嘈杂会议室里**一定会过度拆分**——这是已知且刻意接受的。
    // 但把阈值调到任何位置都不该让「男声」和「女声」合并：
    // 那会让 A 的话被记到 B 名下，纪要与行动项全错。
    const d = new SpeakerDiarizer()
    const male = logMelEmbedding(withNoise(synthVoice({ ...MALE, syllables: 7, seed: 1 }), 0.003))
    const female = logMelEmbedding(withNoise(synthVoice({ ...FEMALE, syllables: 9, seed: 2 }), 0.003))
    const a = d.identify(male)
    const b = d.identify(female)
    assert.notEqual(b.profileId, a.profileId, `嘈杂环境下把两个不同的人并成了一个人`)
  })

  it('门自证：无噪声时两个人也必须是两个 profile（上一条不是恒真）', () => {
    const d = new SpeakerDiarizer()
    const a = d.identify(maleVec(1))
    const b = d.identify(femaleVec(2))
    assert.notEqual(b.profileId, a.profileId)
  })

  it('噪声再大也不许把两个不同的人合并（0.03 底噪）', () => {
    const d = new SpeakerDiarizer()
    const male = logMelEmbedding(withNoise(synthVoice({ ...MALE, syllables: 7, seed: 1 }), 0.03))
    const female = logMelEmbedding(withNoise(synthVoice({ ...FEMALE, syllables: 9, seed: 2 }), 0.03))
    assert.notEqual(d.identify(female).profileId, d.identify(male).profileId)
  })
})

function norm(v: Float32Array) {
  const n = Math.hypot(...Array.from(v)) || 1
  for (let i = 0; i < v.length; i++) v[i] /= n
}
