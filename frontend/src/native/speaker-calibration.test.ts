// speaker-calibration.test.ts —— 用**真实频谱特征**（不是时域包络）守住 SAME_SPEAKER_MIN。
//
// ⚠ 措辞说明：下面反复出现的「真实」指的是「相对旧的时域包络而言是真频谱」，
//   **不是**「真人会议录音」—— 夹具是 **TTS 音频**（见本文件末尾「夹具的局限」）。
//
// ══ 为什么这道门必须存在 ══
// 2026-10-07 之前，SAME_SPEAKER_MIN 是拿**合成语音**（基频 + 三个共振峰的
// 谐波叠加）标定的。合成数据给出的「同人 0.9999 / 异人 0.9848」让人以为
// 窗口很宽。
//
// 用 TTS（神经声码器，真实共振峰与发音动态）重测 9 音色 × 3 句后，
// 结论完全不同 —— 见下面三组实测。
//
// ══ 三组实测（2026-10-07，9 音色 × 3 句 = 同人 27 对 / 异人 324 对）══
//
//   片段长度 5.3~7.6 秒（接近真实 VAD 分段，silenceMs=1500 但自然语流常更短）
//     阈值 0.990 → 同人被拆开 7.4%  / 异人被误并 1.2%   ← 当前阈值，接近最优点
//     阈值 0.985 → 同人 0%       / 异人 3.4%
//     阈值 0.980 → 同人 0%       / 异人 7.1%
//
//   片段长度 3.0 秒（短片段，最坏情况）
//     阈值 0.990 → 同人被拆开 **66.7%** / 异人 0.3%
//     阈值 0.960 → 同人 7.4%     / 异人 13.9%
//
// ★ 结论一：0.99 对**真实长度的分段**是接近最优的，合成数据给出的
//   「同人 0.9999」严重高估了余量。
// ★ 结论二：**分段长度是主导因素**。3 秒片段下 2/3 的同人片段对被拆开，
//   而 VAD 用 1.5 秒静音就切段 ⇒ 短段是常态，不是例外。
//   这是当前设计的**已知上限**，不是阈值能解决的问题。
// ★ 结论三：无论怎么调阈值，「同人」与「异人」两个分布都**存在重叠**
//   （长片段下同人 min 0.9850 < 异人 90 分位 0.9645 之上的尾部），
//   所以单阈值聚类在真实多人会议里**原理上就不够**。要对齐需要
//   ① 已知参会人数约束下的强制分配，或 ② 服务端/本地真实声纹模型。
//
// ══ 夹具的局限（必须说清）══
// 向量是用 TTS 音频算出来的，**不是真人会议录音**。TTS 没有房间混响、
// 真实环境噪声与手机麦克风的频响。所以：
//   · 它足以否掉「合成夹具给出的乐观余量」这个具体错误结论；
//   · 它**不足以**代表真机表现。真机阈值仍需用 `ai-shell` 录音实测复核。
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { describe, it } from 'node:test'
import { SAME_SPEAKER_MIN, SpeakerDiarizer } from './speaker-diarization.ts'
import { RELABEL_MERGE_MIN } from './speaker-relabel.ts'
import { cosineSimilarity } from './speaker-features.ts'

interface Fixture {
  _provenance: Record<string, unknown>
  long: Record<string, number[]>
  short: Record<string, number[]>
}
const FIXTURE = JSON.parse(
  readFileSync(new URL('./__tests__/fixtures/real-speech-embeddings.json', import.meta.url), 'utf8'),
) as Fixture

function vec(bank: Record<string, number[]>): Record<string, Float32Array> {
  return Object.fromEntries(
    Object.entries(bank).map(([k, v]) => [k, new Float32Array(v)]),
  )
}
interface Stats { sameMin: number; sameMedian: number; diffP90: number; diffP99: number; diffMax: number; mergeRate: number; splitRate: number; nSame: number; nDiff: number }
function stats(bank: Record<string, number[]>): Stats {
  const E = vec(bank)
  const ks = Object.keys(E)
  const same: number[] = []
  const diff: number[] = []
  for (let x = 0; x < ks.length; x++) {
    for (let y = x + 1; y < ks.length; y++) {
      const s = cosineSimilarity(E[ks[x]], E[ks[y]])
      ;(ks[x][0] === ks[y][0] ? same : diff).push(s)
    }
  }
  const asc = same.slice().sort((a, b) => a - b)
  const dsc = diff.slice().sort((a, b) => a - b)
  const p = (arr: number[], q: number) => arr[Math.min(arr.length - 1, Math.floor(arr.length * q))]
  return {
    sameMin: asc[0],
    sameMedian: asc[Math.floor(asc.length / 2)],
    diffP90: p(dsc, 0.9),
    diffP99: p(dsc, 0.99),
    diffMax: dsc[dsc.length - 1],
    mergeRate: diff.filter((s) => s >= SAME_SPEAKER_MIN).length / diff.length,
    splitRate: same.filter((s) => s < SAME_SPEAKER_MIN).length / same.length,
    nSame: same.length,
    nDiff: diff.length,
  }
}
const LONG = stats(FIXTURE.long)
const SHORT = stats(FIXTURE.short)

describe('speaker-calibration · 夹具自证', () => {
  it('向量必须是有限的单位向量，且维度一致', () => {
    // 没有这条，一个被写坏的夹具会让下面所有统计安静地变成 NaN，
    // 而 NaN 的比较全部为 false ⇒ 断言恒真。
    const dims = new Set<number>()
    for (const bank of [FIXTURE.long, FIXTURE.short]) {
      for (const [k, arr] of Object.entries(bank)) {
        assert.ok(arr.length > 0, `${k} 是空向量`)
        assert.ok(
          arr.every((x) => Number.isFinite(x) && Math.abs(x) <= 1.000001),
          `${k} 含有非有限或越界分量：${arr.find((x) => !Number.isFinite(x))}`,
        )
        dims.add(arr.length)
      }
    }
    assert.equal(dims.size, 1, `维度不一致：${[...dims].join(',')}`)
  })

  it('分组统计自证：同人/异人两组的样本量符合 9 音色 × 3 句', () => {
    assert.equal(LONG.nSame, 27, '同人应是 9 个音色 × C(3,2)=3')
    assert.equal(LONG.nDiff, 324, '异人应是 C(9,2)×3 = 36×3')
    assert.notDeepEqual(
      [LONG.sameMedian, LONG.diffP90],
      [LONG.diffP90, LONG.sameMedian],
      '分位数被算反了',
    )
  })

  it('两个片段长度的夹具不是同一份数据（否则下面的对比没有意义）', () => {
    assert.notDeepEqual(FIXTURE.long.A1, FIXTURE.short.A1, '长短片段算出了同一个向量')
    assert.ok(SHORT.sameMin < LONG.sameMin, '短片段的同人相似度应当更低（帧数少 ⇒ 统计量更不稳）')
  })
})

describe('speaker-calibration · 阈值在真实频谱特征（TTS 夹具）上的安全性', () => {
  it('长片段：异人误并率 ≤ 2%（这是严重错误，必须压住）', () => {
    console.log(
      `      长片段：同人 min=${LONG.sameMin.toFixed(4)} 中位=${LONG.sameMedian.toFixed(4)}` +
      ` / 异人 p90=${LONG.diffP90.toFixed(4)} p99=${LONG.diffP99.toFixed(4)} max=${LONG.diffMax.toFixed(4)}` +
      ` ⇒ 误并 ${(LONG.mergeRate * 100).toFixed(1)}%、拆分 ${(LONG.splitRate * 100).toFixed(1)}%`,
    )
    assert.ok(
      LONG.mergeRate <= 0.02,
      `异人误并率 ${(LONG.mergeRate * 100).toFixed(1)}% 过高 —— 阈值 ${SAME_SPEAKER_MIN} 太松，会把不同的人记到同一个名字下`,
    )
  })

  it('长片段：同人拆开率 ≤ 15%（标签多但不丢内容）', () => {
    assert.ok(
      LONG.splitRate <= 0.15,
      `同人拆开率 ${(LONG.splitRate * 100).toFixed(1)}% 过高 —— 阈值 ${SAME_SPEAKER_MIN} 太紧`,
    )
  })

  it('长片段：同人相似度的中位数必须高于阈值（典型同一人要能匹配上）', () => {
    assert.ok(
      LONG.sameMedian >= SAME_SPEAKER_MIN,
      `同人相似度中位数 ${LONG.sameMedian.toFixed(4)} 低于阈值 ${SAME_SPEAKER_MIN}`,
    )
  })
})

describe('speaker-calibration · 已知上限（双向 tripwire）', () => {
  it('短片段（3 秒）下同人大比例被拆开 —— 这是已知缺陷，不是新回归', () => {
    console.log(
      `      短片段：同人 min=${SHORT.sameMin.toFixed(4)} 中位=${SHORT.sameMedian.toFixed(4)}` +
      ` ⇒ 拆开 ${(SHORT.splitRate * 100).toFixed(1)}%`,
    )
    assert.ok(
      SHORT.splitRate > 0.5,
      `短片段拆开率降到 ${(SHORT.splitRate * 100).toFixed(1)}% —— 特征变好了！` +
      '请重新标定 SAME_SPEAKER_MIN 并更新 docs/design/2026-10-07-speaker-diarization.md',
    )
  })

  it('短片段下异人误并率仍然很低（拆错的代价必须小于并错）', () => {
    assert.ok(
      SHORT.mergeRate <= 0.02,
      `短片段异人误并 ${(SHORT.mergeRate * 100).toFixed(1)}% —— 阈值需要下调，但那会换来更严重的错配`,
    )
  })

  it('分段长度是主导因素：3 秒明显比 5~8 秒更容易拆错', () => {
    assert.ok(
      SHORT.splitRate > LONG.splitRate * 2,
      `短片段拆开率 ${(SHORT.splitRate * 100).toFixed(1)}% 没有明显高于长片段 ` +
      `${(LONG.splitRate * 100).toFixed(1)}% —— 「分段长度是主导因素」这条结论不再成立，需重新核实`,
    )
  })
})

// ══════════════════════════════════════════════════════════════════════════
// §591：**真人**中文语料上的读数 —— TTS 夹具完全看不见这一类
// ══════════════════════════════════════════════════════════════════════════
//
// ⛔⛔ 为什么必须另配一套夹具：上面整个 describe 的夹具是 **TTS 干声**
//（文件头「夹具的局限」已自陈）。而 §591 实测发现，在**真人中文**音频上
//   `SAME_SPEAKER_MIN = 0.99` 让「同人被拆开」= **100%**
//   （1.5s 窗同人相似度上界只有 0.976，2.0s 窗 0.982，**全部低于阈值**）
// ⇒ 而上面那些判据在 TTS 夹具上**全绿**（长段拆分 7.4%、短段 66.7%）。
// ⇒ ⭐⭐⭐ **这不是判据写错了，是判据量的是另一个总体**：
//   TTS 与真人的同人相似度分布**中心位置不同**，阈值夹在两者之间。
//
// ⚠⚠ 语料边界（必须写在脸上）：**单样本** —— 1 个文件、4 人、56.86 秒。
//   它足以**证伪**「0.99 在真人短段上可用」，
//   ⛔ **不足以**支撑「换成某个阈值就是对的」。
//
// ⚠ 音频**不入仓**（沿用仓内既有约定，见
//   docs/design/2026-10-07-speaker-diarization.md:192-194：
//   「落仓库的是标定向量，不是音频本身 —— frontend/src 总共才 11 MB」）。
//   这里落的同样是**向量**（28 KB），出处逐字记在 `_provenance`。
//
// ⚠ 分段真值**不是本项目切的**：逐字取自 sherpa-onnx 官方 models 页上
//   pyannote-segmentation-3-0 + 3D-Speaker + FastClustering 的官方输出。

const HUMAN = JSON.parse(
  readFileSync(new URL('./__tests__/fixtures/real-human-speech-embeddings.json', import.meta.url), 'utf8'),
) as { _provenance: Record<string, unknown>; speakers: Record<string, number>; bank: Record<string, Record<string, number[]>> }

/** 向量键形如 `<spk>_<官方段号>_<窗内序号>` ⇒ 说话人取第一个字段。 */
function humanStats(bank: Record<string, number[]>) {
  const E = vec(bank)
  const ks = Object.keys(E)
  const same: number[] = []
  const diff: number[] = []
  for (let x = 0; x < ks.length; x++) {
    for (let y = x + 1; y < ks.length; y++) {
      const s = cosineSimilarity(E[ks[x]], E[ks[y]])
      ;(ks[x].split('_')[0] === ks[y].split('_')[0] ? same : diff).push(s)
    }
  }
  const asc = same.slice().sort((a, b) => a - b)
  const dsc = diff.slice().sort((a, b) => a - b)
  return {
    sameMin: asc[0],
    sameMax: asc[asc.length - 1],
    diffMin: dsc[0],
    diffMax: dsc[dsc.length - 1],
    splitRate: same.filter((s) => s < SAME_SPEAKER_MIN).length / same.length,
    mergeRate: diff.filter((s) => s >= SAME_SPEAKER_MIN).length / diff.length,
    nSame: same.length,
    nDiff: diff.length,
  }
}

describe('§591 · 真人中文语料：TTS 夹具量不到的那一半', () => {
  it('门自证：夹具可读、维度一致、说话人标注与键自洽', () => {
    const dims = new Set<number>()
    for (const bank of Object.values(HUMAN.bank)) {
      for (const arr of Object.values(bank)) {
        assert.ok(arr.length > 0, '存在空向量')
        assert.ok(arr.every((x) => Number.isFinite(x)), '存在非有限分量')
        dims.add(arr.length)
      }
    }
    assert.equal(dims.size, 1, `维度不一致：${[...dims].join(',')}`)
    assert.equal(dims.has(48), true, '真人夹具应是 48 维（与 TTS 夹具同一路特征）')
    // ⭐ 键里的说话人前缀必须等于 speakers 表，否则下面整段统计在数错东西。
    let checked = 0
    for (const bank of Object.values(HUMAN.bank)) {
      for (const k of Object.keys(bank)) {
        const [spk, seg] = k.split('_')
        assert.equal(Number(spk), HUMAN.speakers[seg],
          `向量键 ${k} 的说话人前缀与 speakers 表不一致 ⇒ 统计口径错了`)
        checked++
      }
    }
    assert.ok(checked > 0, '一个向量都没检查到 ⇒ 键的切分规则变了')
  })

  for (const win of ['1.5s', '2.0s']) {
    it(`★ 已知上限（tripwire）：${win} 窗下 0.99 会把「同一人」几乎全部拆开`, () => {
      const S = humanStats(HUMAN.bank[win])
      console.log(
        `      真人 ${win}: 同人 ${S.nSame}对 [${S.sameMin.toFixed(3)}~${S.sameMax.toFixed(3)}] / ` +
        `异人 ${S.nDiff}对 [${S.diffMin.toFixed(3)}~${S.diffMax.toFixed(3)}] ` +
        `⇒ 拆开 ${(S.splitRate * 100).toFixed(1)}%、误并 ${(S.mergeRate * 100).toFixed(1)}%`,
      )
      // ⭐ 形状与上面那条「特征变好了！」的 tripwire 同型：
      //   它断言的是**缺陷仍在**，而不是某个阈值该是多少 ——
      //   阈值取多少是产品口径（见简报 #54），本门不代拍。
      assert.ok(
        S.splitRate > 0.9,
        `真人 ${win} 窗下拆开率降到 ${(S.splitRate * 100).toFixed(1)}% —— 阈值或特征被改了！` +
        '请复核 docs/design/2026-10-06-recording-quality-fixes.md §591 并更新决策简报 #54',
      )
    })

    it(`★ 反向读数：${win} 窗下「同人」与「异人」两个区间不重叠`, () => {
      const S = humanStats(HUMAN.bank[win])
      assert.ok(
        S.sameMin > S.diffMax,
        `真人 ${win} 窗下区间重叠：同人下界 ${S.sameMin.toFixed(4)} ≤ 异人上界 ${S.diffMax.toFixed(4)}`,
      )
    })
  }

  it('★ 人与 TTS 的同人相似度中心位置不同（这才是 0.99 失效的原因）', () => {
    const H = humanStats(HUMAN.bank['1.5s'])
    // TTS 长片段（5.3~7.6s）的同人下界，与真人 1.5s 窗的同人上界比。
    // ⚠ 只断言「真人上界明显低于 TTS 下界」这一个方向；反方向不成立时也不要报错
    //   （段长不同，两者本就不可直接互换）。
    assert.ok(
      H.sameMax < LONG.sameMin,
      `真人 1.5s 同人上界 ${H.sameMax.toFixed(4)} 已不低于 TTS 长段同人下界 ` +
      `${LONG.sameMin.toFixed(4)} —— 「两个总体中心位置不同」这条前提需要重新核实`,
    )
  })
})

// ══════════════════════════════════════════════════════════════════════════
// §595：收尾 `relabel()` —— §591 / §594 都只量了**增量阶段**，整场读数是最大缺口
// ══════════════════════════════════════════════════════════════════════════
//
// ⛔⛔ **§594 的核心结论已被撤回**（原文保留，见设计文档 §594 的撤回标注）：
//   它断言「与 `speaker-diarization.ts:22-29` 自写的设计前提正面冲突，
//   实际落在『无限错拆』那一侧」—— 那是**只测了增量阶段**就外推到整场。
//   实测整场为 **10 → 8 个身份、零错并、膨胀 2.0×**（真值 4 人）
//   ⇒ 与 `speaker-relabel.ts:11` 自陈的「2.31× / 错并 0」**量级同型**，落在那侧。
// ✅ §594 仍然成立的部分：增量阶段无一对达标（真人 1.5s 窗同人上界 0.976 < 0.99）
//   ⇒ **实时路径确实每段新建**。上面 §591 那组判据钉的就是这个。
//
// ⭐⭐⭐⭐⭐ **本组判据存在的理由**（这是它与 §591 那组不可互相替代的地方）：
//   §591 只钉住「增量阶段会把真人拆开」。而整场还有**第二道机制**
//   —— `speaker-relabel.ts` 整个文件就是为「收尾把碎片并回去」写的，
//   且**早在 §591 之前就已存在**（见其文件头的实测表）。
//   ⇒ **增量红 ≠ 用户看到红**。若没有这组判据，
//   任何人把 `relabel()` 改坏、甚至让它**恒空转**（提前 return / 阈值写成 1.0），
//   整个门禁**全绿**。
//
// ⛔ **本组不代拍阈值**：断言的是「收尾做了什么」与「缺陷是否仍在」。
//   `RELABEL_MERGE_MIN` 该取多少是**产品口径**（简报 #54），本门只登记读数。
//
// ⚠⚠ **语料边界**：**单样本** —— 1 个文件、4 人、56.86 秒。
//   足以证伪「整场不会错拆」，⛔ 不足以支撑「换成某个阈值就是对的」。
//   音频**不入仓**（沿用仓内约定，见 docs/design/2026-10-07-speaker-diarization.md:192-194）。

const OFFICIAL_SEG = HUMAN.bank.segments

const VAD_FIX = JSON.parse(
  readFileSync(new URL('./__tests__/fixtures/real-human-vad-segments-embeddings.json', import.meta.url), 'utf8'),
) as {
  _provenance: Record<string, unknown>
  meta: { id: string; start: number; end: number; truth: number[] }[]
  segments: Record<string, number[]>
}

/** 键 `0_00` ⇒ 官方段号 `00` 的说话人（§591 夹具的键约定：`<spk>_<段号>`）。 */
function officialSpeakerOf(key: string): number {
  const seg = key.split('_')[1]
  return HUMAN.speakers[seg]
}

/**
 * 跑完整闭环：**增量 `identify` → `relabel()` 收尾 → 按真值归簇**。
 *
 * ⚠ 这里刻意用**生产代码**（`SpeakerDiarizer`）而不是纯函数 `relabelClusters`：
 *   `relabel()` 还负责摘死簇（`getProfiles()`）与压平映射（见 §595.1 接线核验）。
 *   只测纯函数会漏掉「合并做了、界面却列不出变化」这一类（`speaker-relabel.test.ts:255`
 *   已经有那条判据，但它用的是 TTS 夹具，**看不见真人短段上的行为**）。
 */
function closedLoop(
  bank: Record<string, number[]>,
  truthOf: (key: string) => number[],
  threshold?: number,
) {
  const d = new SpeakerDiarizer()
  const E = vec(bank)
  const rows = Object.keys(E).map((key) => {
    const r = d.identify(E[key])
    return { key, truth: truthOf(key), pid: r.profileId }
  })
  const incremental = new Set(rows.map((r) => r.pid)).size
  const canonical = threshold === undefined ? d.relabel() : d.relabel(threshold)
  // 压平（生产侧是 resolveCanonical，这里是等价的跟随链）
  const flat = (id: string) => {
    let c = id, guard = 0
    while (canonical.has(c) && canonical.get(c) !== c && guard++ < 10) c = canonical.get(c)!
    return c
  }
  const clusters = new Map<string, { members: string[]; truth: Set<number> }>()
  for (const r of rows) {
    const k = flat(r.pid)
    if (!clusters.has(k)) clusters.set(k, { members: [], truth: new Set<number>() })
    const c = clusters.get(k)!
    c.members.push(r.key)
    // ⭐⭐ 必须**逐个真值展平**（for…of），不能整数组 push：
    //   整数组 push 会让下面的 new Set 变成「对数组引用去重」⇒ 恒判「混人簇」。
    //   这正是 §595.4 记下的量具坑（第一版读出「零错并」、第二版读出「错并 1」）。
    for (const t of r.truth) c.truth.add(t)
  }
  const redirected = [...canonical.entries()].filter(([a, c]) => a !== c)
  return {
    incremental,
    canonical,
    redirected,
    clusters: [...clusters.values()],
    clusterCount: clusters.size,
    alive: d.getProfiles().length,
    mixed: [...clusters.values()].filter((c) => c.truth.size > 1),
  }
}

describe('§595 · 整场收尾 relabel：增量阶段测不到的那一半', () => {
  it('门自证：两个夹具可读、48 维、键与真值自洽，且 VAD 首段确实是 3 个人', () => {
    assert.equal(Object.keys(OFFICIAL_SEG).length, 10, '官方分段应是 10 段')
    assert.equal(VAD_FIX.meta.length, 6, '本仓 VAD 应是 6 段')
    for (const [name, bank] of [['官方分段', OFFICIAL_SEG], ['本仓 VAD', VAD_FIX.segments]] as const) {
      for (const [k, arr] of Object.entries(bank)) {
        assert.equal(arr.length, 48, `${name} 的 ${k} 应是 48 维`)
        assert.ok(arr.every((x) => Number.isFinite(x)), `${name} 的 ${k} 含非有限分量`)
      }
    }
    // ⭐ 官方分段的键前缀必须等于 speakers 表 —— 与 §591「门自证」同一口径
    for (const k of Object.keys(OFFICIAL_SEG)) {
      assert.equal(Number(k.split('_')[0]), officialSpeakerOf(k),
        `官方分段键 ${k} 的说话人前缀与 speakers 表不一致`)
    }
    // ⭐⭐ 本组的核心前提：**VAD 首段确实混了 3 个人**。
    //   若哪天它变成 1 个人，下面那条「错并在切段层」就不再成立。
    assert.deepEqual(VAD_FIX.meta[0].truth, [0, 1, 2],
      'VAD 首段应为 3 人（speaker 0/1/2）—— §593 的根因前提变了')
    assert.ok(VAD_FIX.meta[0].end - VAD_FIX.meta[0].start > 16,
      'VAD 首段应当还是那段 16 秒以上的合并段')
  })

  it('★ 收尾确实在工作：canonicalOf 里必须有非自映射（否则它恒空转）', () => {
    const r = closedLoop(OFFICIAL_SEG, (k) => [officialSpeakerOf(k)])
    assert.ok(r.redirected.length > 0,
      '真人分段上 relabel() 一条都没合并 ⇒ 它已经恒空转了（阈值被改坏 / 提前 return）。' +
      '请复核 docs/design/2026-10-06-recording-quality-fixes.md §595')
    assert.ok(r.canonical.size > 0, 'canonicalOf 是空表')
    // ⭐ 死簇必须真的从 profiles 里摘掉 —— 否则「合并做了、界面列不出变化」
    assert.equal(r.alive, r.clusterCount, '存活 profile 数与段上出现的身份数不一致')
    assert.ok(r.alive < r.incremental, '合并发生了，profiles 却没有变少')
    console.log(`      官方分段：增量 ${r.incremental} → 收尾 ${r.clusterCount}（合并 ${r.redirected.length} 次）`)
  })

  it('★ 收尾零错并（方向性安全断言，不涉及阈值口径）', () => {
    const r = closedLoop(OFFICIAL_SEG, (k) => [officialSpeakerOf(k)])
    assert.equal(r.mixed.length, 0,
      `收尾后出现 ${r.mixed.length} 个混人簇：` +
      r.mixed.map((c) => `[${c.members.join(',')}→{${[...c.truth].join(',')}}]`).join(' '))
    console.log(`      官方分段收尾 ${r.clusterCount} 簇全部为纯簇，真值 4 人`)
  })

  it('★ 缺陷仍在（tripwire）：整场仍比真值人数多，且这条会在修好后变红', () => {
    const r = closedLoop(OFFICIAL_SEG, (k) => [officialSpeakerOf(k)])
    const truth = new Set(Object.values(HUMAN.speakers)).size
    assert.ok(
      r.clusterCount > truth,
      `整场收尾后 ${r.clusterCount} 簇已经不高于真值 ${truth} 人 —— ` +
      '切段 / 特征 / 阈值 / 已知簇数里至少有一项被改了！' +
      '请复核 §595 与决策简报 #54，并同步 §591 的 0.99 口径',
    )
    console.log(`      整场 ${r.clusterCount} 簇 vs 真值 ${truth} 人 ⇒ 膨胀 ${(r.clusterCount / truth).toFixed(2)}×`)
  })

  it('★ 阴性对照：把阈值降到 0 必然收成 1 簇 ⇒ 上一条不是恒绿', () => {
    const r = closedLoop(OFFICIAL_SEG, (k) => [officialSpeakerOf(k)], 0)
    assert.equal(r.clusterCount, 1, `threshold=0 时应当全合成 1 簇，实得 ${r.clusterCount}`)
    assert.ok(r.mixed.length === 1, 'threshold=0 时那个簇必然是混人的（它装下了全部 4 个说话人）')
  })

  it('★ 本仓 VAD 路径那 1 个错并，且它**落在首段**（错并源于切段，不是聚类）', () => {
    const r = closedLoop(VAD_FIX.segments, (k) => VAD_FIX.meta.find((m) => m.id === k)!.truth)
    assert.equal(r.mixed.length, 1,
      `本仓 VAD 路径的混人簇数变成了 ${r.mixed.length}（${r.mixed.length === 0 ? '首段的 3 人被拆开了' : r.mixed.map((c) => `[${c.members}]`).join(' ')}）`)
    assert.deepEqual([...r.mixed[0].truth].sort(), [0, 1, 2],
      '唯一的混人簇应当正好装着首段那 3 个人')
    assert.deepEqual(r.mixed[0].members, ['v0'], '混人的应当只有首段 v0')
    console.log(`      VAD：增量 ${r.incremental} → 收尾 ${r.clusterCount}（真值 4 人），错并 1 个 = 首段 v0 的 3 个人`)
  })

  it('★ 聚类层结构上无法拆分：簇数只减不增，被吸收的 id 不再出现在 clusters 里', () => {
    const r = closedLoop(VAD_FIX.segments, (k) => VAD_FIX.meta.find((m) => m.id === k)!.truth)
    assert.ok(r.clusterCount <= r.incremental, '收尾后簇数反而变多了')
    const dropped = new Set(r.redirected.map(([, kept]) => kept).filter((k) => !r.canonical.has(k)))
    // 被吸收的一侧在 canonicalOf 里有非自映射，但**不会**作为独立簇活着
    for (const [from, to] of r.redirected) {
      assert.notEqual(from, to, 'canonicalOf 里出现了自映射却被算作重定向')
    }
    // ⭐ 关键结构性事实：`merges` 的每一项都是「一个吸收另一个」，
    //   结果里没有任何「一个变成多个」的通道 ⇒ 切段混进来的人**没有任何一层能拆开**。
    assert.ok(r.mixed[0].truth.size > 1,
      '前置条件失效：没有混人簇可断言')
    assert.equal(dropped.size, 0, '吸收方仍在 canonicalOf 里缺席 —— 映射方向反了？')
    console.log(`      重定向 ${r.redirected.length} 条，簇数 ${r.incremental} → ${r.clusterCount}（单调不增）`)
  })

  // ═══ §768 新增：把「簇」层面的判据补到「**标签**」层面 ═══════════════════
  //
  // 上面那条「混人簇 = 1」是在**簇**上断言的，而 `closedLoop` 把每个段的
  // truth 数组**展平**进它所属的簇 ⇒ `v0` 那个混人簇的 truth 是 `{0,1,2}`，
  // 于是 speaker_1 在**簇**这一层「出现了」。
  //
  // ⚠⚠ 但生产真正消费的是**标签**：一个段只能挂一个 `speakerLabel`
  //   （`MeetingSegment.speakerLabel` 是单值）。所以「出现在某个混人簇里」
  //   **不等于**「有自己的标签」。
  // ⇒ §766/§767 实测：VAD 路径 6 段 → **5 个标签**，而真值 4 人，
  //   `speaker_1` 的 5.91s（全场 10.4%）全部在 `v0` 内 ⇒ **他拿不到任何标签**
  //   ⇒ 真 `planRefine` 的 `roster` 里没有他 ⇒ 精校提示词 / 同音纠正 /
  //   `participantLeak` 三处都看不到他（详见设计文档 §766）。
  //
  // ⚠⚠⚠ 而本文件原有的 22 条断言**全部放行**这个状态：
  //   ① 「混人簇 = 1」在**簇**层面成立；② tripwire 只断言 `clusterCount > truth`，
  //      而 VAD 路径实测 `clusterCount = 5`、真值 4 ⇒ **恰好不触发**；
  //   ③ 更根本的是：**簇数 5 ≈ 真值 4 是三个缺陷互相抵消**（−1 人 + 1 幽灵 + 同一人被拆）
  //      ⇒ **任何只比聚合计数与真实人数的自检，都会被这个巧合放过。**
  // ⇒ 所以这条断言问的不是「簇数对不对」，而是「**每一位真实在场者是否都拿到了只属于他的标签**」。
  it('★ 缺陷仍在（tripwire）：有一位真实说话人**拿不到任何属于他自己的标签**（§766/§767）', () => {
    const r = closedLoop(VAD_FIX.segments, (k) => VAD_FIX.meta.find((m) => m.id === k)!.truth)
    const truth = new Set(VAD_FIX.meta.flatMap((m) => m.truth))
    // ⭐ 只有**纯簇**（truth.size === 1）才算「这个人拿到了只属于他的标签」；
    //   混人簇里的成员在标签层面会被压成同一个值 ⇒ 不能算。
    const owned = new Map<number, string[]>()
    for (const c of r.clusters) {
      if (c.truth.size !== 1) continue
      const spk = [...c.truth][0]
      owned.set(spk, [...(owned.get(spk) ?? []), ...c.members])
    }
    const missing = [...truth].filter((s) => !owned.has(s))
    assert.ok(
      missing.length > 0,
      `每一位真实说话人都拿到了自己的标签（${truth.size} 人 / ${r.clusterCount} 簇）—— ` +
      '「混人段里的人拿不到标签」这个缺陷**已修好**！请同步设计文档 §766/§767、' +
      '决策简报 A.11，并复核上面那条「错并 1 个」的断言是否也该改。',
    )
    console.log(
      `      真实 ${truth.size} 人，其中 ${missing.length} 人没有专属标签：speaker_${missing.join(' / speaker_')}；` +
      `簇数 ${r.clusterCount} ≈ 真值 ${truth.size} 属**巧合**（少 1 人 + 1 个混人幽灵 + 有人被拆）`,
    )
  })
})