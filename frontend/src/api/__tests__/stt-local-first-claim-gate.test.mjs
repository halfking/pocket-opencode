// stt-local-first-claim-gate.test.mjs — §415：`api/stt.ts` 的文件头**不得**声称
// 一条已被能力表证伪的本地路径可用，也**不得**把云端模型写死成某一个。
//
// ── 为什么这是缺陷而不只是「注释旧了」──
//
// 本仓对这类注释有既定立论（`backend/.../gateway_model_notes_gate_test.go` 开头）：
// **「注释一旦过期就会变成『不用再修了』的伪证据。」**
//
// 缺陷现场（`api/stt.ts` 文件头，此前逐字）：
//   1. Try on-device sherpa-onnx (Paraformer for Chinese) — native only.
//   2. If unavailable or low-confidence, fall back to Groq Whisper Large v3 Turbo
//
// 而 `lib/shell/capabilities.ts` 已把 `Sherpa` 登记进 `NOT_IMPLEMENTED_PLUGINS`：
// Android 侧 6 个 `@PluginMethod` 里 5 个只 `call.reject`、iOS 无 Swift 实现、
// assets 无 `.onnx` 模型 ⇒ **本地路径 100% 不可用，实际 100% 走云端**。
//
// 第 2 条同样过期：§406 起云端模型由「用户 STT 设置 + 网关实时探测」解析，
// 后端 `transcribe.go` 原文写着「Which cloud model answers is *not* hardcoded
// any more」—— 文件头写死某个模型名，会让人以为换模型要改这个文件。
//
// ── 这道门的形状 ──
//
// ⚠ 刻意**不**只钉「那句话在不在」，而是**跨文件交叉核对**：
// 先从 `capabilities.ts` 读出 Sherpa 是否被登记为未实现，
// **只有**在登记成立时才要求文件头如实说「走云端」。
// ⇒ 将来骨架真的接上时（指纹串消失、`capabilities-stub-gate` 转红），
//   这道门会自动切换到另一个形状，而不是变成一句永真的空话。
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, it } from 'node:test'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const SRC = path.resolve(HERE, '..', '..')
const read = (...p) => readFileSync(path.join(SRC, ...p), 'utf8')

const STT = read('api', 'stt.ts')
const CAPS = read('lib', 'shell', 'capabilities.ts')

/** `capabilities.ts` 把 Sherpa 登记进 NOT_IMPLEMENTED_PLUGINS 了没有。 */
function sherpaRegisteredUnimplemented(src) {
  const at = src.indexOf('NOT_IMPLEMENTED_PLUGINS')
  if (at < 0) return false
  return /\bSherpa\b/.test(src.slice(at))
}

/** 文件头 = 第一个块注释。⚠ 不能按 `\nimport ` 切：`stt.ts` 的 import 并不在文件顶部。 */
function headerOf(src) {
  const end = src.indexOf('*/')
  return end < 0 ? src.slice(0, 600) : src.slice(0, end + 2)
}

/** 旧头里那句「本地优先」的断言，用**拼接**构造 —— 判据文件里不留完整字面量。 */
const STALE_LOCAL_CLAIM = ['Try on-device sherpa-onnx', '(Paraformer for Chinese)'].join(' ')
/** 旧头里那个写死的云端型号，同样拼接。 */
const STALE_CLOUD_CLAIM = ['fall back to', 'Groq Whisper Large v3'].join(' ')

/**
 * 否定语境标记：出现任一个 ⇒ 这一行**不**构成「本地可用」的主张。
 *
 * ⚠ 这是本门第二层判据的关键。订正性注释**必须**能点名它订正的是什么
 * （「原文件头声称…」），所以不能用「头里不许出现本地/设备端这些词」这种一刀切，
 * 否则门会逼着人把订正写成看不懂的话、或者逼着人删证据。
 * ⇒ 只在**去掉否定行之后**才查是否还剩主张。
 */
const NEGATION = /不可用|未实现|不是|不会|没有|无任何|reject|骨架|仅|⚠|✗|已转红|前提失效|曾经|原文件头|原头|更正|订正/i

/** 主张本地路径的两个要素：提到本地/设备端，且声称它可用/优先。 */
const MENTIONS_LOCAL = /sherpa|本地|设备端|local[- ]first|\blocal\b/i
const ASSERTS_USABLE = /可用|能(用|跑|走)|已支持|支持|优先|先(试|走|用)|可用时|\btry\b|\bworks?\b|\bavailable\b/i

/** 在已剥掉否定语境的那些行里，是否还剩「本地路径可用」的主张。 */
function affirmativeLocalClaimLines(header) {
  return header
    .split('\n')
    .filter((line) => !NEGATION.test(line))
    .filter((line) => MENTIONS_LOCAL.test(line) && ASSERTS_USABLE.test(line))
}

/** 这道门要判的「如实陈述」，做成一个函数以便负控复用。 */
function sttHeaderIsHonest(header, sherpaIsSkeleton) {
  if (sherpaIsSkeleton) {
    if (header.includes(STALE_LOCAL_CLAIM)) return false   // 仍逐字声称本地可用
    // 第二层：换了措辞的同一主张（"设备端 sherpa 优先，native 可用"）也要红。
    if (affirmativeLocalClaimLines(header).length > 0) return false
    if (!/实际\s*100%\s*走云端/.test(header)) return false   // 必须说明现状
  }
  if (header.includes(STALE_CLOUD_CLAIM)) return false      // 写死了云端型号
  return true
}

describe('§415 · 行为层：文件头是否与能力表一致', () => {
  it('★ 能力表把 Sherpa 登记为未实现 ⇒ 文件头不得再声称 local-first 可用', () => {
    assert.ok(sherpaRegisteredUnimplemented(CAPS),
      'capabilities.ts 里找不到 NOT_IMPLEMENTED_PLUGINS 里的 Sherpa —— ' +
      '要么骨架真的接上了（那请把本门改成另一条形状），要么能力表被改坏了，先查清再动这条门')
    assert.ok(!headerOf(STT).includes(STALE_LOCAL_CLAIM),
      'api/stt.ts 的文件头仍断言本地优先可用 —— 而 Sherpa 在能力表里是骨架，实际 100% 走云端')
    assert.match(headerOf(STT), /实际\s*100%\s*走云端/,
      '文件头没有如实说明「当前实际 100% 走云端」⇒ 读者仍会以为有本地优先这一层')
  })

  it('★ 文件头不得把云端模型写死成某一个', () => {
    assert.ok(!headerOf(STT).includes(STALE_CLOUD_CLAIM),
      '文件头仍把云端写死成某一个托管 Whisper 型号 —— §406 起云端模型由「用户 STT 设置 + 网关探测」解析')
  })

  it('★★ 换措辞的「本地优先」也必须红（不能只钉旧原句）', () => {
    const leftover = affirmativeLocalClaimLines(headerOf(STT))
    assert.deepEqual(leftover, [],
      `当前头里这几行仍是「本地可用」的主张：\n${leftover.join('\n')}`)
    // 只换措辞、保留「100% 走云端」那句 ⇒ 必须仍然被抓住。
    const reworded = `/**\n` +
      ` * 实现「设备端优先」策略：\n` +
      ` *   1. 先试 on-device sherpa 权重 —— native only，已支持。\n` +
      ` *   ⚠ 与此同时，实际 100% 走云端。\n */\nimport x from 'y'\n`
    assert.equal(sttHeaderIsHonest(headerOf(reworded), true), false,
      '只钉旧原句的话，把同一句主张换个措辞就能绕过这道门')
  })

  it('★★ 负控：把旧头放回去，判定必须立刻翻脸', () => {
    const stale = `/**\n * Implements the "local-first + cloud-fallback" strategy:\n` +
      ` *   1. ${STALE_LOCAL_CLAIM} — native only.\n` +
      ` *   2. ${'If unavailable or low-confidence, ' + STALE_CLOUD_CLAIM}\n */\n` +
      `import { sherpa } from '../native/sherpa'\n`
    assert.equal(sherpaRegisteredUnimplemented(CAPS), true, '负控前提失效：能力表里已没有 Sherpa 登记')
    assert.equal(sttHeaderIsHonest(headerOf(stale), true), false,
      '负控本身写错了：旧头本该被判为不诚实')
    assert.equal(sttHeaderIsHonest(headerOf(STT), true), true,
      '负控本身写错了：当前头本该被判为诚实')
  })

  it('★★ 负控：骨架真的接上时，判定必须切换形状（不许永真）', () => {
    // Sherpa 不再被登记为未实现 ⇒ 这道门不该继续要求「100% 走云端」这句话。
    const connected = 'Sherpa 真接上了'   // 与 headless 情形相反的事实
    assert.equal(sherpaRegisteredUnimplemented(connected), false)
    // 骨架接上后，旧头里的「local-first 可用」就变成**真的**了 —— 门应放行。
    const localFirst = `/**\n * 1. ${STALE_LOCAL_CLAIM} — native only.\n */\nimport x from 'y'\n`
    assert.equal(sttHeaderIsHonest(headerOf(localFirst), false), true,
      '骨架接上后本地优先就成立了，门却仍然判它不诚实 ⇒ 门是恒假的')
  })
})

// ══════════════════════════════════════════════════════════════════════════
// §589：把覆盖面从「一个文件的一句话」扩到**同一类主张的另一个文件**
// ══════════════════════════════════════════════════════════════════════════
//
// 缺陷现场（`native/speaker-diarization.ts` 的 SAME_SPEAKER_MIN 标定注释，逐字）：
//   ② 换成真实声纹模型（原生 cap-sherpa ECAPA 已具备，Web 侧没有）。
// 而 `capabilities.ts` 已把 `Sherpa` 登记进 `NOT_IMPLEMENTED_PLUGINS`
// （Android 5 个方法恒 reject、iOS 无 Swift 实现、assets 无 .onnx），
// 且 `capabilities-stub-gate` 正对着 Java 源码指纹守着这件事。
// ⇒ **仓里两处对同一个能力给出互相矛盾的陈述**。
//
// ⚠⚠ 为什么本门原来没抓到：判据的**匹配面只有 `api/stt.ts` 的文件头**，
//   而且话题是 **ASR 本地优先**。「ECAPA 声纹模型可用」是**另一个话题的
//   另一个文件里的另一句话** ⇒ 对本门完全不可见。
//   ⇒ 结构性修法不是再加一句字面量，而是**把「同一类主张」纳入同一道门**。
//
// ⛔ 第一版判据（行级否定语境过滤）在这句话上**恒绿**，已废弃：
//   该行含「Web 侧**没有**」⇒ 被「否定语境」整行过滤掉 ⇒ 「ECAPA 已具备」活着。
//   ⇒ **一个说的不是同一件事的否定，不能当成对主张的免责。**
//   下面的 DISCLAIM 只收「与主张同句、且明确指向该能力」的免责标记。

const DIAR = read('native', 'speaker-diarization.ts')

const SUBJECT_ECAPA = /ECAPA/i
const ASSERTS_ECAPA_USABLE = /已具备|可用|已支持|支持|优先|能用|能跑|能走/
const DISCLAIMS_ECAPA_UNAVAILABLE =
  /恒\s*`?reject|NOT_IMPLEMENTED_PLUGINS|not integrated|未接|不可用|骨架|没有这条路|已删除|已登记为/

/** 「说了 ECAPA、声称它能用、且同句没有免责」的**行**。 */
function ecapaClaimLines(src) {
  return src
    .split('\n')
    .filter((l) => SUBJECT_ECAPA.test(l) && ASSERTS_ECAPA_USABLE.test(l) && !DISCLAIMS_ECAPA_UNAVAILABLE.test(l))
    .map((l) => l.trim())
}

/** 这道门要判的「ECAPA 主张是否诚实」，做成函数以便负控复用。 */
function diarizationClaimIsHonest(src, sherpaIsSkeleton) {
  if (!sherpaIsSkeleton) return true
  if (ecapaClaimLines(src).length > 0) return false
  // ⭐ 不只是「别说谎」：文件还得**知道权威登记在哪**，否则下一个人又会去搜 AAR 集成步骤。
  if (!/NOT_IMPLEMENTED_PLUGINS/.test(src)) return false
  return true
}

/** 缺陷现场那句谎，用拼接构造 —— 判据文件里不留完整字面量。 */
const STALE_ECAPA_CLAIM =
  '*   ② 换成真实声纹模型（原生 cap-sherpa ' + ['ECAPA', '已具备', 'Web 侧没有'].join('') + '）。\n'
/** ⚠ 必须待在**模块级**：三个负控都要复用它，声明在某个 it() 里会变成
 *  `ReferenceError: withLie is not defined`（作用域错误 masquerade 成判据失败）。 */
const WITH_STALE_ECAPA = 'export const X = 1\n' + STALE_ECAPA_CLAIM

describe('§589 · 声纹侧：不得声称原生 ECAPA 可用', () => {
  it('前提自证：能力表仍把 Sherpa 登记为「已注册但未实现」', () => {
    assert.ok(sherpaRegisteredUnimplemented(CAPS),
      '能力表里已没有 NOT_IMPLEMENTED_PLUGINS 的 Sherpa —— ' +
      '要么骨架真接上了（请把本门改成另一条形状），要么能力表被改坏了，先查清再动')
  })

  it('★ 当前文件是诚实的（正向）', () => {
    const bad = ecapaClaimLines(DIAR)
    assert.deepEqual(bad, [],
      `speaker-diarization.ts 里这几行把原生 ECAPA 说成了可用：\n${bad.join('\n')}`)
    assert.match(DIAR, /NOT_IMPLEMENTED_PLUGINS/,
      '文件里找不到 NOT_IMPLEMENTED_PLUGINS ⇒ 它没告诉读者权威登记在哪，' +
      '下一个人还是会顺着「ECAPA」去找 AAR 集成步骤')
  })

  it('★★ 负控：把缺陷现场那句谎放回去，判定必须立刻翻脸', () => {
    assert.ok(ecapaClaimLines(WITH_STALE_ECAPA).length > 0,
      '负控前提失效：判据连缺陷现场那句话都抓不住 ⇒ 这道门是恒绿的')
    assert.equal(diarizationClaimIsHonest(WITH_STALE_ECAPA, true), false,
      '负控本身写错了：那句谎本该被判为不诚实')
    assert.equal(diarizationClaimIsHonest(DIAR, true), true,
      '负控本身写错了：当前文件本该被判为诚实')
  })

  it('★★ 负控：同句的「否定」必须指向同一件事才算免责', () => {
    // ⭐ 这一句是本判据与第一版的**全部差别**：
    // 「Web 侧没有」说的是**另一个主体**，不能替 ECAPA 免责。
    assert.equal(diarizationClaimIsHonest(WITH_STALE_ECAPA, true), false,
      '「ECAPA 已具备，但 Web 侧没有」被当成了免责 ⇒ 判据退回行级否定过滤')
    // 对照：真的指向该能力的免责，同句出现就放行
    const disclaimed = '* ② 原生 cap-sherpa ECAPA 不可用：插件骨架恒 reject，已登记在册。\n'
    assert.equal(diarizationClaimIsHonest(disclaimed, true), false,
      '免责句缺 NOT_IMPLEMENTED_PLUGINS 时不该算诚实 —— 判据要求同时给出权威登记')
    assert.equal(diarizationClaimIsHonest(disclaimed + ' * 见 NOT_IMPLEMENTED_PLUGINS\n', true), true,
      '同句免责 + 给出权威登记 ⇒ 应判为诚实')
  })

  it('★★ 负控：骨架真接上时，判定必须切换形状（不许永假）', () => {
    assert.equal(diarizationClaimIsHonest(WITH_STALE_ECAPA, false), true,
      '骨架接上后「ECAPA 已具备」就是真的，门却仍判它不诚实 ⇒ 门是恒假的')
  })

  it('★ 扫描面说明：另外两处 ECAPA 提法已逐个核过，不在判据范围内', () => {
    // `speaker-embedding.ts` 的「优先原生 ECAPA」——代码确实先试原生再兜底，
    //   描述的是**尝试顺序**而不是可用性；`speaker-features.ts` 的是
    //   「若曾在支持 ECAPA 的设备上录过声纹」——反事实条件句。
    // ⛔ 若哪天要把这两处也纳入，先想清楚它们与本判据要禁的不是同一类句子。
    assert.equal(ecapaClaimLines(read('native', 'speaker-embedding.ts')).length, 1,
      'speaker-embedding.ts 的命中数变了 ⇒ 它已被改动，先核过再决定要不要纳入判据')
  })
})