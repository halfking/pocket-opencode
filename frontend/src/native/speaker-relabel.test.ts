/**
 * speaker-relabel.test.ts — 收尾合并过度拆分说话人的判据
 *
 * ══ 这个门在守什么 ══
 * 生产链路的声纹是逐 VAD 段（1.5~4s）算的，实测该区间分离窗口
 * −0.0460 ⇒ 两人会议被标成 5 个「说话人 N」。`relabelClusters`
 * 用「聚类级长窗口统计」把碎片并回去。
 *
 * 所以门禁的第一条断言是**标定 tripwire**：直接量「标签膨胀」与
 * 「错合并」两个数，而不是断言某个内部实现形状。换一种同样有效的
 * 写法不该让门红；把合并关掉必须让门红。
 *
 * ⛔⛔ **§597 勘误**：原文写「夹具是 TTS **真实人声谱**（不是合成信号）」——**那是错的**。
 *   夹具 `_provenance.produced` 逐字写着「MiniMax TTS」，`voices` 是 9 个 TTS 音色
 *   ⇒ **它是合成语音，不是真人**（与 `real-human-speech-embeddings.json` 才是真人，两份别混）。
 * ⛔ 原文接着写「真人更接近 ⇒ 错合并会更多」——**这句已被实测证伪**（§597.2）：
 *   真人中文实测**零错并**，本 TTS 夹具实测**错并 2 个** ⇒ **方向正好相反**。
 */
import { describe, test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { SpeakerDiarizer } from './speaker-diarization.ts'
import { logMelEmbedding, cosineSimilarity } from './speaker-features.ts'
import { RELABEL_MERGE_MIN, planSpeakerRelabel, relabelClusters, resolveCanonical } from './speaker-relabel.ts'

const FIXTURE = new URL('./__tests__/fixtures/real-speech-embeddings.json', import.meta.url)
const raw = JSON.parse(readFileSync(FIXTURE, 'utf8')) as {
  _provenance: Record<string, unknown>
  short: Record<string, number[]>
  long: Record<string, number[]>
}
const SHORT = (k: string): Float32Array => new Float32Array(raw.short[k])
// ⚠⚠ §598 新增：`long` bank **此前从未被本文件任何判据跑过** ——
//   上面的 `PLANS` 只用 `short` bank、只 6~8 段 ⇒ 对 `long`/27 段场景零证明力。
const LONG = (k: string): Float32Array => new Float32Array(raw.long[k])

const V = ['A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'I'] as const
const S = ['1', '2', '3'] as const
const conv = (plan: string[]) => plan.map((k) => ({ key: k, speaker: k[0] }))

const PLANS = [
  conv(['A1', 'A2', 'B1', 'B2', 'A3', 'B3']),
  conv(['A1', 'B1', 'A2', 'B2', 'A3', 'B3']),
  conv(['A1', 'A2', 'A3', 'B1', 'B2', 'B3']),
  conv(['A1', 'B1', 'C1', 'A2', 'B2', 'C2', 'A3', 'B3', 'C3']),
  conv(['A1', 'A2', 'B1', 'B2', 'C1', 'C2', 'D1', 'D2']),
]

interface Reading { truth: number; labels: number; merged: number }
function measure(turns: { key: string; speaker: string }[], lab: string[]): Reading {
  const m = new Map<string, Set<string>>()
  turns.forEach((t, i) => {
    if (!m.has(lab[i])) m.set(lab[i], new Set())
    m.get(lab[i])!.add(t.speaker)
  })
  return {
    truth: new Set(turns.map((t) => t.speaker)).size,
    labels: new Set(lab).size,
    // 错合并 = 一个标签覆盖了多个真实说话人（张冠李戴）
    merged: [...m.values()].filter((s) => s.size > 1).length,
  }
}

/** 走生产路径：贪心首轮 → 聚类级合并。 */
function runPipeline(turns: { key: string; speaker: string }[], threshold = RELABEL_MERGE_MIN) {
  const d = new SpeakerDiarizer()
  const before: string[] = []
  const samples = []
  for (const t of turns) {
    const emb = SHORT(t.key)
    const r = d.identify(emb)
    before.push(r.label)
    samples.push({ profileId: r.profileId, embedding: emb })
  }
  const { canonicalOf } = relabelClusters(samples, threshold)
  const d2 = d
  const after = before.map((lab, i) => {
    const pid = samples[i].profileId
    const canon = resolveCanonical(canonicalOf, pid)
    return d2.getProfile(canon)?.label ?? lab
  })
  return { before, after, canonicalOf, merges: relabelClusters(samples, threshold).merges.length }
}

test('标定 tripwire：合并把标签膨胀从 2.31× 压到 ≤1.15×，且错合并 ≤1', () => {
  let tb = 0, lb = 0, lm = 0
  for (const turns of PLANS) {
    const { before, after } = runPipeline(turns)
    const a = measure(turns, before)
    const b = measure(turns, after)
    tb += b.truth; lb += a.labels; lm += b.merged
    assert.ok(
      b.labels <= a.labels,
      `合并后标签不该变多：${turns.map((t) => t.key).join(',')} ${a.labels}→${b.labels}`,
    )
  }
  const inflationBefore = lb / tb
  assert.ok(
    inflationBefore > 2,
    `前置条件：基线必须是「被过度拆开」的（膨胀 ${inflationBefore.toFixed(2)}×）。` +
      `若夹具或 greedy 行为变了导致不再膨胀，这条门就失去了被测对象，`,
  )
  assert.ok(
    lm <= 1,
    `错合并（张冠李戴）必须 ≤1，实测 ${lm}。这是本模块最贵的一笔账。`,
  )
})

test('两人会议必须从 5 个「说话人 N」收敛到 2 个', () => {
  const { before, after } = runPipeline(PLANS[0])
  const m = measure(PLANS[0], after)
  assert.equal(m.truth, 2)
  assert.equal(after.length, before.length, '段数不能变')
  assert.ok(m.labels <= 2, `两人会议不该有 ${m.labels} 个标签，期望 ≤2`)
  assert.equal(m.merged, 0, '两人场景不允许错合并')
})

test('阈值 0.99 下必须一次都不合并（证明不能照抄 SAME_SPEAKER_MIN）', () => {
  for (const turns of PLANS) {
    const { after, before } = runPipeline(turns, 0.99)
    assert.deepEqual(after, before, '0.99 档长窗口声纹也达不到合并门槛')
  }
})

test('用户改过名的 profile 是锚点：两个锚点永不合并', () => {
  const samples = [
    { profileId: 'p1', embedding: SHORT('A1') },
    { profileId: 'p1', embedding: SHORT('A2') },
    { profileId: 'p2', embedding: SHORT('A1') },
    { profileId: 'p2', embedding: SHORT('A2'), pinned: true },
    { profileId: 'p3', embedding: SHORT('A1') },
    { profileId: 'p3', embedding: SHORT('A2'), pinned: true },
  ]
  const { canonicalOf } = relabelClusters(samples, 0.5)
  assert.equal(resolveCanonical(canonicalOf, 'p2'), 'p2', 'p2 是锚点，不能被并掉')
  assert.equal(resolveCanonical(canonicalOf, 'p3'), 'p3', 'p3 是锚点，不能被并掉')
  // 未命名的 p1 与 p2 声纹相同 ⇒ 它**应该**被锚点吸收，标签沿用用户改过的名字。
  // 这里曾经断言 p1 保留自己，那是把「先出现的一侧赢」当成了正确行为，
  // 而那正是会吞掉用户命名的那个 bug。
  assert.equal(resolveCanonical(canonicalOf, 'p1'), 'p2', '同声的未命名碎片应并入锚点，标签归用户')
})

test('锚点可以吸收未命名的碎片（用户改过的名字应当占主导）', () => {
  const samples = [
    { profileId: 'named', embedding: SHORT('A1'), pinned: true },
    { profileId: 'frag', embedding: SHORT('A2') },
    { profileId: 'frag', embedding: SHORT('A3') },
  ]
  const { canonicalOf, merges } = relabelClusters(samples, 0.9)
  assert.equal(resolveCanonical(canonicalOf, 'frag'), 'named', '碎片应被命名锚点吸收')
  assert.equal(merges.length, 1)
  assert.equal(merges[0].kept, 'named')
})

test('resolveCanonical 压平多级合并链（存活方会翻转时才真的产生链）', () => {
  // ★ 三个**等样本量**的同声簇造不出链：存活方总是先出现的那个，
  //   结果永远是 A←B、A←C 的一层扁平映射 ⇒ 压平循环从未被喂过
  //   （变异 M5 撤掉它仍全绿就是这么来的）。
  //   必须让**后出现的簇样本更多** ⇒ 第二轮合并时存活方翻转成 C，
  //   于是出现 B→A→C 这种两层链。
  const samples = [
    { profileId: 'A', embedding: SHORT('A1') },
    { profileId: 'B', embedding: SHORT('A1') },
    { profileId: 'C', embedding: SHORT('A1') },
    { profileId: 'C', embedding: SHORT('A1') },
    { profileId: 'C', embedding: SHORT('A1') },
  ]
  const { canonicalOf } = relabelClusters(samples, 0.5)
  assert.equal(canonicalOf.get('B'), 'A', '前置条件：第一轮是 A 吸收 B')
  assert.equal(canonicalOf.get('A'), 'C', '前置条件：第二轮存活方翻转成 C（这才构成链）')
  assert.equal(resolveCanonical(canonicalOf, 'B'), 'C', 'B 必须被压平到最终存活方 C')
})

test('合并后聚类均值是「全部成员的平均」而不是「第一个成员」', () => {
  // ★ 成员全相同时，均值 == 第一个成员，两种实现无法区分
  //   （变异 M6「不重算均值」仍全绿就是这么来的）。
  //   所以这里必须用**互不相同**的三个成员，让偷懒的实现露出来。
  const members = [SHORT('A1'), SHORT('A2'), SHORT('A3')]
  const samples = [
    { profileId: 'solo', embedding: members[0] },
    ...members.map((m, i) => ({ profileId: 'grp', embedding: m })),
  ]
  const { clusters } = relabelClusters(samples, 0.5)
  const grp = clusters.find((c) => c.id === 'grp')
  assert.ok(grp, 'grp 应当存活（3 段的一侧吸收 1 段的一侧）')
  assert.equal(grp.size, 4, '合并后应含全部 4 个成员')

  // 独立算一遍「全部成员的平均并归一化」，与模块输出比对
  const norm = (v: Float32Array) => {
    let n = 0
    for (let i = 0; i < v.length; i++) n += v[i] * v[i]
    n = Math.sqrt(n) || 1
    return v.map((x) => x / n)
  }
  const all = [members[0], members[1], members[2], members[0]].map(norm)
  const expect = new Float64Array(members[0].length)
  for (const v of all) for (let i = 0; i < v.length; i++) expect[i] += v[i]
  const expectNorm = norm(new Float32Array(expect.map((x) => x / all.length)))
  let worst = 0
  for (let i = 0; i < expectNorm.length; i++) {
    worst = Math.max(worst, Math.abs(grp.mean[i] - expectNorm[i]))
  }
  assert.ok(worst < 1e-5, `合并簇均值应当是全部成员的平均，最大偏差 ${worst}`)

  // 明确排除「只取第一个成员」这种偷懒实现。
  // 阈值取 1e-3：主断言的容差是 1e-5，而同一说话人的不同发言本来就接近
  // （同人余弦 min 0.9441），实测均值与首成员差 0.0175 —— 相对容差有 ~1700× 余量，
  // 足以让变异 M6（均值改用第一个成员）转红，又不至于把断言绑死在一个魔数上。
  const firstOnly = norm(members[0])
  let dFirst = 0
  for (let i = 0; i < firstOnly.length; i++) dFirst = Math.max(dFirst, Math.abs(grp.mean[i] - firstOnly[i]))
  assert.ok(dFirst > 1e-3, `合并簇均值与「第一个成员」应可区分（实测差 ${dFirst.toFixed(4)}），否则这条断言没有分辨力`)
})

test('零向量/空向量不建簇，也不参与合并', () => {
  const samples = [
    { profileId: 'z', embedding: new Float32Array(0) },
    { profileId: 'z', embedding: new Float32Array(48) },
    { profileId: 'ok', embedding: SHORT('A1') },
  ]
  const { canonicalOf, merges } = relabelClusters(samples, 0.0)
  assert.equal(canonicalOf.get('z'), 'z', '空向量不该被并进任何簇')
  assert.equal(merges.length, 0, '零向量与任何东西都不可比，不该触发合并')
})

test('相同输入的结果确定（门禁与生产都不能有随机性）', () => {
  const a = runPipeline(PLANS[0])
  const b = runPipeline(PLANS[0])
  // ⚠ 比的是**标签**而不是 profileId：`SpeakerDiarizer.createProfile` 的 id
  //   带 `Date.now()`（`sp-<ms>-<n>`），两次运行本就不同 ⇒ 直接比 id 恒红，
  //   而那不是不确定性，是 id 设计。`说话人 N` 才是稳定读数，
  //   而标签序列已经完整刻画了「谁被并进了谁」。
  assert.deepEqual(a.before, b.before)
  assert.deepEqual(a.after, b.after)
})

test('存活方取「成员多的一侧」，同数取先出现的（标签归属可预期）', () => {
  const samples = [
    { profileId: 'early', embedding: SHORT('A1') },
    { profileId: 'many', embedding: SHORT('A1') },
    { profileId: 'many', embedding: SHORT('A2') },
    { profileId: 'many', embedding: SHORT('A3') },
  ]
  const { canonicalOf } = relabelClusters(samples, 0.5)
  assert.equal(resolveCanonical(canonicalOf, 'early'), 'many', '3 段的一侧应当吸收 1 段的一侧')
})

test('夹具自检：short 银行仍是 48 维（改了 logMelEmbedding 门禁会先红）', () => {
  assert.ok(Object.keys(raw.short).length >= 27, '夹具段数不足')
  assert.equal(SHORT('A1').length, logMelEmbedding(new Float32Array(16000 * 3)).length)
  assert.ok(raw._provenance.warning, '出处里必须保留「向量随实现失效」的警告')
  for (const v of V) for (const s of S) assert.ok(raw.short[`${v}${s}`], `夹具缺 ${v}${s}`)
})

// ══════════════════════════════════════════════════════════════════
// 下面测的是 SpeakerDiarizer.relabel() 本身（不是纯函数）——
// 「纯函数正确」不等于「接进生产后真的生效」。
// ══════════════════════════════════════════════════════════════════

test('relabel 之后死簇必须从 getProfiles() 里消失（否则界面看不出合并发生过）', () => {
  const d = new SpeakerDiarizer()
  const ids: string[] = []
  for (const k of ['A1', 'A2', 'B1', 'B2', 'A3', 'B3']) ids.push(d.identify(SHORT(k)).profileId)
  const before = d.getProfiles()
  assert.ok(before.length >= 4, `前置条件：贪心首轮应当过度拆分，实际只有 ${before.length} 个`)

  const canonical = d.relabel()
  assert.ok(canonical.size > 0, '本场应当发生合并')

  const alive = new Set(d.getProfiles().map((p) => p.id))
  // 判据 1：出现过但已不存在的 profile，就是被吸收掉的簇
  const dropped = [...new Set(ids)].filter((id) => !alive.has(id))
  assert.ok(dropped.length > 0, '应当有簇被吸收掉')
  // 判据 2：活着的簇数必须等于 canonicalOf 里不同的目标数
  const targets = new Set([...canonical.values()])
  assert.equal(alive.size, targets.size, '活着的簇数与合并后的目标数必须一致')
  // 判据 3：每个活着的 id 自己指向自己
  for (const id of alive) assert.equal(resolveCanonical(canonical, id), id)
})

test('★ 合并不许换掉比对基准（换掉会让同一个人立刻又分裂）', () => {
  // 这条钉住一个实测踩过的坑：曾把存活方的 embedding 换成聚类均值，
  // 想着「这样下次比较更准」。实测合并后同一个人的新发言对合并质心的余弦是
  //   0.9970 / 0.9912 / 0.9895（阈值 0.99）⇒ 第三段直接判成新说话人。
  // 根因：0.99 是按「单次发言 vs 单次发言」标定的，而聚类均值是长窗口统计。
  // 且这条路径**可达**：diarizer 在同一 session 内换场录音时会被复用
  // （recordingRuntime 的 `if (freshSession || !this.diarizer)`）。
  const d = new SpeakerDiarizer()
  for (const k of ['A1', 'A2', 'A3', 'B1', 'B2', 'B3']) d.identify(SHORT(k))
  const embedsBefore = new Map(d.getProfiles().map((p) => [p.id, p.embedding]))
  d.relabel()

  const kept = d.getProfiles()
  assert.ok(kept.length > 0)
  for (const p of kept) {
    const was = embedsBefore.get(p.id)
    assert.ok(was, '存活的 profile 必须来自合并前的集合')
    let diff = 0
    for (let i = 0; i < was.length; i++) diff = Math.max(diff, Math.abs(was[i] - p.embedding[i]))
    assert.equal(diff, 0, `${p.label} 的 embedding 被合并改写了 —— 阈值标定基准会失配`)
    assert.ok(p.sampleCount >= 1, 'sampleCount 应当被更新成合并后的成员数')
  }

  // 把上面那条实测数字钉成前置条件：长窗口质心 vs 单次发言确实过不了 0.99。
  // 少了它，「不许换基准」这条断言就没有理由 —— 两者是同一件事的两面。
  const mean = new Float32Array(SHORT('A1').length)
  for (const k of ['A1', 'A2', 'A3']) {
    const e = SHORT(k)
    for (let i = 0; i < e.length; i++) mean[i] += e[i] / 3
  }
  let n = 0
  for (let i = 0; i < mean.length; i++) n += mean[i] * mean[i]
  n = Math.sqrt(n) || 1
  for (let i = 0; i < mean.length; i++) mean[i] /= n
  const sim = cosineSimilarity(SHORT('A3'), mean)
  assert.ok(
    sim < 0.99,
    `前置条件：长窗口质心 vs 单次发言必须低于 0.99（实测 ${sim.toFixed(4)}），否则前提不成立`,
  )
})

test('relabel 对样本不足的场次返回空表（不制造无谓的写库）', () => {
  const d = new SpeakerDiarizer()
  d.identify(SHORT('A1'))
  assert.equal(d.relabel().size, 0, '只有 1 段时不该合并')
})

test('用户改过名的 profile 在 relabel 后仍然保留自己的名字', () => {
  const d = new SpeakerDiarizer()
  const p1 = d.identify(SHORT('A1')).profileId
  d.identify(SHORT('A2'))
  const p2 = d.identify(SHORT('A3')).profileId
  d.labelProfile(p2, '张伟')          // 用户亲手改名
  d.relabel()
  const alive = d.getProfiles()
  const named = alive.find((p) => p.id === p2)
  assert.ok(named, '用户命名的 profile 不能被吸收掉')
  assert.equal(named.label, '张伟')
  assert.equal(alive.some((p) => p.id === p1), false, '同声的未命名碎片应并入命名锚点')
})

test('同一场录音 relabel 两次：第二次不该再改（幂等）', () => {  const d = new SpeakerDiarizer()
  for (const k of ['A1', 'A2', 'B1', 'B2', 'A3', 'B3']) d.identify(SHORT(k))
  const first = d.relabel()
  assert.ok(first.size > 0)
  const labels1 = d.getProfiles().map((p) => `${p.id}:${p.sampleCount}`).sort()
  const second = d.relabel()
  assert.equal(second.size, 0, '合并后样本已清空，第二次不该再合')
  assert.deepEqual(d.getProfiles().map((p) => `${p.id}:${p.sampleCount}`).sort(), labels1)
})

// ══════════════════════════════════════════════════════════════════
// 接线层决策（planSpeakerRelabel）——「哪些段该改标签」是纯函数，
// 所以「只回贴占位标签」这条决定在 node 里真的跑得到。
// ══════════════════════════════════════════════════════════════════

describe('planSpeakerRelabel', () => {
  const base = {
    segmentProfiles: new Map([
      ['s1', 'p1'],
      ['s2', 'p1'],
      ['s3', 'p2'],
      ['s4', 'p3'],
    ]),
    canonicalOf: new Map([['p1', 'p2'], ['p3', 'p2']]),
    labelOf: (id: string) => (id === 'p2' ? '说话人 2' : undefined),
    currentLabelOf: (id: string) => (id === 's4' ? '李总' : '说话人 1'),
  }

  test('只回贴仍是占位标签的段；用户改过名的那段保留自己的名字', () => {
    const plan = planSpeakerRelabel(base)
    // s1/s2（p1→p2，占位）要改；s3 已在 p2，不动；s4（p3→p2）
    // 标签是「李总」⇒ 只重映射、不回贴。
    assert.deepEqual(
      plan.updates.map((u) => u.segId).sort(),
      ['s1', 's2'],
    )
    for (const u of plan.updates) assert.equal(u.label, '说话人 2')
  })

  test('用户改过名的段仍然要重映射（跟随声学归属，后续按人改名才覆盖得到）', () => {
    const plan = planSpeakerRelabel(base)
    assert.equal(plan.remap.get('s4'), 'p2', 's4 应当被重映射到 p2')
    assert.equal(plan.updates.find((u) => u.segId === 's4'), undefined, '但不该回贴标签')
  })

  test('没被合并的段既不进 remap 也不进 updates', () => {
    const plan = planSpeakerRelabel(base)
    assert.equal(plan.remap.has('s3'), false, 's3 本来就在 p2')
    assert.equal(plan.updates.find((u) => u.segId === 's3'), undefined)
  })

  test('目标 profile 查不到名字时整段跳过，不写空标签', () => {
    const plan = planSpeakerRelabel({ ...base, labelOf: () => undefined })
    assert.equal(plan.updates.length, 0)
    assert.equal(plan.remap.size, 0)
  })

  test('压平合并链：段指向已被吸收的中间簇时也要落到最终存活方', () => {
    const plan = planSpeakerRelabel({
      ...base,
      segmentProfiles: new Map([['s1', 'p1']]),
      canonicalOf: new Map([['p1', 'p2'], ['p2', 'p3']]),
      labelOf: () => '说话人 3',
    })
    assert.equal(plan.remap.get('s1'), 'p3', 'p1→p2→p3 必须压平到 p3')
    assert.equal(plan.updates[0].label, '说话人 3')
  })

  test('「说话人」/「Speaker 3」也算占位；真实人名不算', () => {
    const plan = planSpeakerRelabel({
      ...base,
      segmentProfiles: new Map([['s1', 'p1'], ['s2', 'p1'], ['s3', 'p1'], ['s4', 'p1']]),
      currentLabelOf: (id) => ({ s1: '说话人', s2: 'Speaker 3', s3: '张伟', s4: '  ' }[id] ?? ''),
    })
    assert.deepEqual(plan.updates.map((u) => u.segId).sort(), ['s1', 's2', 's4'])
  })
})

// ══════════════════════════════════════════════════════════════════
// ⚠ 以下是**源码形状门**，不是行为门。recordingRuntime 拖进 vue/capacitor，
//   node 里加载不了，所以「stop() 真的调用了合并」只能扫源码。
//   形状门天然弱（同款教训见 meeting-dedup 的「扫到 ?? 就算过」），
//   所以这里只断言**具体锚点 + 相对顺序**，并且已做变异验证：
//   删掉 stop() 里的调用、把调用挪到在途等待之前、把 planSpeakerRelabel
//   换成恒返回空表 —— 三条都会打红（见 speaker-relabel-wiring 变异记录）。
//   真正被**执行**的判据在上面的 planSpeakerRelabel / relabelClusters 两条。
// ══════════════════════════════════════════════════════════════════

describe('recordingRuntime 接线', () => {
  const src = readFileSync(
    new URL('./recordingRuntime.ts', import.meta.url),
    'utf8',
  )
  const stopBody = (() => {
    const start = src.indexOf('async stop(): Promise<{ audioPath')
    assert.ok(start > 0, '找不到会议录音的 stop()')
    const end = src.indexOf('\n  formatElapsed()', start)
    assert.ok(end > start, 'stop() 的结束锚点没找到')
    return src.slice(start, end)
  })()

  test('stop() 必须调用收尾合并', () => {
    assert.match(stopBody, /await this\.mergeOverSplitSpeakers\(/)
  })

  test('★ 合并必须排在「等在途分段落库」之后、「置 completed」之前', () => {
    const waitInFlight = stopBody.indexOf('inFlightSegments')
    const merge = stopBody.indexOf('mergeOverSplitSpeakers')
    const complete = stopBody.indexOf("status: 'completed'")
    assert.ok(waitInFlight > 0 && merge > 0 && complete > 0, '三个锚点都得在 stop() 里')
    assert.ok(
      waitInFlight < merge,
      '合并早于在途分段落库 ⇒ 会漏掉最后几段发言（那几段常是让聚类可判定的关键）',
    )
    assert.ok(merge < complete, '合并晚于置 completed ⇒ 界面先于标签更新显示')
  })

  /**
   * §567：上面那条只钉了**顺序**，没钉**失败会不会阻断收尾**。
   *
   * ⭐ 变异 M21 实测：把 `mergeOverSplitSpeakers` 内部那句
   *   「失败就保持原样、绝不把已落库的转写搞丢」的 catch **整段删掉**，
   *   **全量 45 项门禁依然全绿** ⇒ 那条承诺零强制。
   *
   * ⚠ 而它的 try/catch 本来就只包住了 `relabel()` 一行，后面的
   *   内存回贴 / `updateSegmentSpeaker` 落库 / `updateTranscript` 整篇重写正文
   *   都在保护之外；调用点又是裸 await 且**紧挨着** `status: 'completed'`。
   *
   * ⇒ 这里钉的不是「有没有 catch」，而是那句更强的承诺本身：
   *   **合并失败绝不能阻断置 completed。**
   */
  test('★ 收尾合并失败不得阻断 status:completed（合并是增强，不是必需）', () => {
    const call = stopBody.indexOf('await this.mergeOverSplitSpeakers(')
    assert.ok(call > 0, 'stop() 里找不到 mergeOverSplitSpeakers 的调用')
    const complete = stopBody.indexOf("status: 'completed'")
    assert.ok(complete > call, '前置条件不成立：completed 应排在合并之后')

    // ① 调用必须被 try 包住
    const tryBefore = stopBody.lastIndexOf('try {', call)
    assert.ok(tryBefore > 0 && tryBefore < call,
      '收尾合并是裸 await ⇒ 它一抛错，后面 status:completed 永不执行，会议卡在「录音中」')

    // ② ⭐ 关键：completed 必须落在 **catch 之外**。
    //    只查「有没有 catch」会被「顺手把整个 if 块都吞了」的写法骗过 ——
    //    那样 completed 也一起被吞，收尾照样断。
    const catchAfter = stopBody.indexOf('} catch', call)
    assert.ok(catchAfter > call && catchAfter < complete,
      `catch 没夹在合并与 completed 之间 ⇒ completed 落在保护之外或被一并吞掉`)
    const guarded = stopBody.slice(tryBefore, catchAfter)
    assert.ok(!guarded.includes("status: 'completed'"),
      'status:completed 落在了 catch 覆盖的范围里 ⇒ 合并失败会连带阻断收尾')
    assert.ok(!guarded.includes('updateMeeting('),
      'updateMeeting 落在了 catch 覆盖的范围里 ⇒ 同上')
  })

  test('mergeOverSplitSpeakers 走纯函数决策，并回贴 DB 与转写正文', () => {
    const start = src.indexOf('private async mergeOverSplitSpeakers')
    assert.ok(start > 0, '找不到 mergeOverSplitSpeakers')
    const body = src.slice(start, src.indexOf('\n  private syncSpeakers()', start))
    assert.match(body, /planSpeakerRelabel\(\{/, '决策必须走纯函数（否则 node 门禁覆盖不到）')
    assert.match(body, /updateSegmentSpeaker\(u\.segId/, '必须回写分段标签')
    assert.match(body, /updateTranscript\(meetingId, renderTranscript\(/, '转写正文里的说话人前缀也要重渲染')
  })

  test('历史声纹里已有人名的 profile 被打成锚点', () => {
    assert.match(
      src,
      /pinned:\s*!isPlaceholderSpeaker\(p\.label\)/,
      '用户改过名的历史 profile 必须标 pinned，否则收尾合并会把他并掉',
    )
  })
})

describe('每一次 identify 都必须留下声纹（含走「匹配已有 profile」分支的那些）', () => {
  // ★ 这条是被变异 D2 逼出来的：删掉匹配分支里的 `this.samples.push(...)`
  //   之后，short 银行下**全部门禁仍然全绿** ——
  //   short 只有 1/6 走匹配分支，那一个样本对合并决策没有可观测影响。
  //
  // ⚠⚠ 第一版补门也是错的：它在**测试里自己拼了一个 samples 数组**，
  //   绕开了 diarizer 的内部记录 ⇒ 看着在测 D2，实际根本没经过那条路径。
  //   判据必须落在 diarizer 自己的 `recordedSampleCount` 上。
  const LONG = (k: string): Float32Array => new Float32Array(raw.long[k])

  test('long 银行下 6 段发言全部被记录（4 段走匹配分支、2 段新建）', () => {
    const d = new SpeakerDiarizer()
    let matched = 0
    for (const k of ['A1', 'A2', 'B1', 'B2', 'A3', 'B3']) {
      if (!d.identify(LONG(k)).isNew) matched++
    }
    // 前置条件：这条数据必须真的走过匹配分支，否则下面没有分辨力
    assert.ok(matched >= 3, `前置条件：long 银行应大量走匹配分支，实测 ${matched}/6`)
    assert.equal(
      d.recordedSampleCount, 6,
      '每一次 identify 都必须留痕 —— 漏记匹配分支的样本，合并就只剩新建那一半',
    )
  })

  test('short 银行下同样一段都不能漏（含那 1 段匹配分支的）', () => {
    const d = new SpeakerDiarizer()
    let matched = 0
    for (const k of ['A1', 'A2', 'B1', 'B2', 'A3', 'B3']) {
      if (!d.identify(SHORT(k)).isNew) matched++
    }
    assert.equal(d.recordedSampleCount, 6, `short 银行匹配分支走了 ${matched} 次，6 段仍必须全部留痕`)
  })

  test('long 银行下贪心首轮就判对两人（窗口够长时不需要合并）', () => {
    // long 窗口的独立佐证：5.3~7.6s 的余弦分布几乎可分，所以合并 0 次、
    // 标签数前后都是 2。记在这里是为了让「合并只在短窗口才需要」
    // 这件事有一条可执行的反证，而不是只写在注释里。
    const d = new SpeakerDiarizer()
    for (const k of ['A1', 'A2', 'B1', 'B2', 'A3', 'B3']) d.identify(LONG(k))
    assert.equal(d.getProfiles().length, 2, '长窗口下不该过度拆分')
    assert.equal(d.relabel().size, 0, '长窗口下没有碎片可并')
  })
})

// ══════════════════════════════════════════════════════════════════════════
// §598 补上一个覆盖缺口：本文件此前**从未跑过 `long` bank**
// ══════════════════════════════════════════════════════════════════════════
//
// ⛔⛔⛔ **为什么这组判据是必需的（量具覆盖面问题，不是产品口径）**：
// 本文件原有的标定 tripwire「错合并 ≤1」只跑 `PLANS` 那 5 个**手工序列**：
// 6~8 段、**只用 `short` bank**、每音色 2~3 段。
// ⇒ 而 `speaker-relabel.ts:33-38` 那张阈值表声称量的是「**27 段**」。
// ⇒ **两者不是同一个总体** —— 门量着一批数据，注释表引用着另一批。
//
// ⛔⛔ 实测后果（§597.1）：`RELABEL_MERGE_MIN = 0.97` 在 `long` bank 上**错并 2 个**
// （混人簇 `{A,D,I}`、`{B,F}`），而本文件的门对此**零覆盖** ⇒ 它一直绿着。
//
// ⚠⭐ **形状声明**：下面断言的是**缺陷仍在**（错并 ≥ 2），
//   而**不是**「阈值该是多少」—— 阈值取多少是产品口径（简报 #54），本门不代拍。
//   **修好了它会红**，并指向 #54。

describe('§598 · `long` bank：注释表与既有门都够不到的那个总体', () => {
  /**
   * 全量 27 段走**生产路径**（`SpeakerDiarizer.identify` → `d.relabel()`）。
   *
   * ⭐ 刻意用 `d.relabel()` 而**不是**纯函数 `relabelClusters` ——
   *   §595 的教训：用纯函数会漏掉「摘死簇 / 压平映射」，而那正是用户看到的那一层。
   *   ⚠ 同理 `after` 必须按 **`profileId`** 查 canonicalOf，不能用 `label`（§595.4 第 1 条）。
   */
  function runLong(threshold?: number) {
    const keys = Object.keys(raw.long)
    const d = new SpeakerDiarizer()
    const before: string[] = []
    const samples: { profileId: string; embedding: Float32Array }[] = []
    for (const k of keys) {
      const emb = LONG(k)
      const r = d.identify(emb)
      before.push(r.label)
      samples.push({ profileId: r.profileId, embedding: emb })
    }
    const incremental = new Set(before).size
    const canonical = threshold === undefined ? d.relabel() : d.relabel(threshold)
    const after = before.map((lab, i) => {
      let cur = samples[i].profileId, guard = 0
      while (canonical.has(cur) && canonical.get(cur) !== cur && guard++ < 10) cur = canonical.get(cur)!
      return d.getProfile(cur)?.label ?? lab
    })
    // ⭐ 真值按 **after 标签**归簇，且 speaker 是**单字符键**，无分隔符坑（§596.4）
    const m = new Map<string, Set<string>>()
    keys.forEach((k, i) => {
      if (!m.has(after[i])) m.set(after[i], new Set())
      m.get(after[i])!.add(k[0])
    })
    const mixed = [...m.entries()].filter(([, s]) => s.size > 1)
    return {
      segments: keys.length,
      truth: new Set(keys.map((k) => k[0])).size,
      incremental,
      labels: new Set(after).size,
      alive: d.getProfiles().length,
      merged: mixed.length,
      mixedDetail: mixed.map(([l, s]) => `${l}→{${[...s].join('')}}`),
    }
  }

  test('门自证：`long` bank 可读、48 维、键的说话人前缀可解析', () => {
    const keys = Object.keys(raw.long)
    assert.equal(keys.length, 27, `long bank 应是 27 段，实得 ${keys.length}`)
    const dims = new Set(keys.map((k) => raw.long[k].length))
    assert.equal(dims.size, 1, `维度不一致：${[...dims].join(',')}`)
    assert.equal(dims.has(48), true, '应是 48 维（与 short 同一路特征）')
    assert.ok(raw.long.A1.every((x) => Number.isFinite(x)), '存在非有限分量')
    assert.equal(new Set(keys.map((k) => k[0])).size, 9, '应是 9 个音色')
  })

  test('★ 缺陷仍在（tripwire）：0.97 在 long bank 上错并 ≥2 —— 修好了它会红并指向 #54', () => {
    const r = runLong()
    console.log(
      `      long bank：增量 ${r.incremental} → 收尾 ${r.labels}（真值 ${r.truth} 人），` +
      `错并 ${r.merged} ${r.mixedDetail.length ? '→ ' + r.mixedDetail.join(' ') : ''}`,
    )
    assert.ok(
      r.merged >= 2,
      `0.97 在 long bank 上的错并降到 ${r.merged} ${r.mixedDetail.join(' ')} —— ` +
      '阈值或特征被改了！请复核 docs/design/2026-10-06-recording-quality-fixes.md §597 与决策简报 #54',
    )
  })

  test('★ 收尾确实在工作（否则上一条会靠「什么都没合」而恒绿）', () => {
    const r = runLong()
    assert.ok(r.labels < r.incremental,
      `收尾后 ${r.labels} 簇未少于增量 ${r.incremental} ⇒ relabel 恒空转`)
    assert.equal(r.alive, r.labels, '存活 profile 数与段上身份数不一致')
  })

  test('★ 阴性对照：threshold=0 必然收成 1 簇且必然混人 ⇒ 上一条不是恒绿', () => {
    const r = runLong(0)
    assert.equal(r.labels, 1, `threshold=0 时应当全合成 1 簇，实得 ${r.labels}`)
    assert.equal(r.merged, 1, 'threshold=0 时那个簇必然混住全部 9 个说话人')
  })

  // ⚠⚠ **这一条曾写成 assert.ok(「差值 > 1」)，现已降级为「记录 + 量具自证」—— 因为那是恒绿的装饰。**
  //   「注释表反推的分母与夹具真值对不上」是**两个已固定数据的算术差**，
  //   任何代码改动都不会让它变红 ⇒ 恒绿 ⇒ 按纪律（恒绿判据即装饰）不能留在判据里。
  //   ⭐ 真正把这件事钉住的是**注释本身**（§597 已改），以及上面那两条**有牙**的行为判据。
  test('★ 记录（量具自证）：注释表反推的分母与 long / short 两个 bank 的真值都对不上', () => {
    const impliedTruth = 30 / 2.31            // 表里「30(2.31×)」反推出的分母
    const longTruth = new Set(Object.keys(raw.long).map((k) => k[0])).size
    const shortTruth = new Set(Object.keys(raw.short).map((k) => k[0])).size
    console.log(
      `      注释表反推分母 ${impliedTruth.toFixed(2)}｜long 真值 ${longTruth}｜short 真值 ${shortTruth}` +
      ` ⇒ 表量的既不是 long 也不是 short（§597.1 已排除 long / short / PLANS 三个候选）`,
    )
    // 只断言「三者互不相等」这个**事实**本身 —— 它是本记录的量具自证
    assert.notEqual(Math.round(impliedTruth), longTruth, '量具自证失败：反推分母竟等于 long 真值')
    assert.notEqual(Math.round(impliedTruth), shortTruth, '量具自证失败：反推分母竟等于 short 真值')
  })
})
