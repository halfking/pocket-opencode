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
  // ⚠ 2026-11-14 订正：旧写法 `src.indexOf('NOT_IMPLEMENTED_PLUGINS')` 命中的是
  //   **注释里的首次出现**（capabilities.ts:43 那段 2026-10-17 的订正注释），
  //   从那里 slice 到文件尾会把注释里的 `Sherpa` 字样全扫进去 ⇒ 恒 true。
  //   （Sherpa 的 Java 实现早在 2026-10-06 Phase 4 落地，表已清空。）
  //   改成只扫 `export const` 声明那一行起的对象字面量区域。
  const decl = src.indexOf('export const NOT_IMPLEMENTED_PLUGINS')
  if (decl < 0) return false
  const tail = src.slice(decl)
  const open = tail.indexOf('{')
  const close = tail.indexOf('}', open)
  if (open < 0 || close < 0) return false
  return /\bSherpa\b/.test(tail.slice(open, close + 1))
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
  // ⚠ 2026-11-14 形状切换：Sherpa 的 Java 实现已落地（Phase 4，2026-10-06），
  //   且会议分片链路真正接上了 audioPath（层3修复）——
  //   「本地优先可用」从伪命题变成了真命题，本门从「禁声称本地可用」
  //   切换到「不许把云端模型写死」+「不许重新声称已失效的云端型号」。
  //   旧的「必须写『实际 100% 走云端』」判据随骨架接上而撤销
  //   （门文件自己声明过：骨架接上时请把本门改成另一条形状）。
  it('前提自证：能力表不再把 Sherpa 登记为未实现（骨架已接上，本门按新形状运行）', () => {
    assert.equal(sherpaRegisteredUnimplemented(CAPS), false,
      'capabilities.ts 的 NOT_IMPLEMENTED_PLUGINS 里又出现了 Sherpa —— ' +
      '要么实现被回退了（那本门必须换回旧形状），要么表被改坏了，先查清再动这条门')
    // 文件头仍不得把云端写死成某一个型号（§406 判据在新形状下继续有效）
    assert.ok(!headerOf(STT).includes(STALE_CLOUD_CLAIM),
      '文件头仍把云端写死成某一个托管 Whisper 型号 —— §406 起云端模型由「用户 STT 设置 + 网关探测」解析')
    assert.ok(!headerOf(STT).includes(STALE_LOCAL_CLAIM),
      '文件头又回到了逐字的旧主张（含写死的 Paraformer）—— §406 的云端判据与本地现状都对不上，先查 git log')
  })

  it('★ 文件头不得把云端模型写死成某一个', () => {
    assert.ok(!headerOf(STT).includes(STALE_CLOUD_CLAIM),
      '文件头仍把云端写死成某一个托管 Whisper 型号 —— §406 起云端模型由「用户 STT 设置 + 网关探测」解析')
  })

  it('★★ 负控：骨架回退时（表里重新出现 Sherpa），「本地优先」判据必须重新长牙（不许单向门）', () => {
    // 形状切换是**可逆的**：实现回退 ⇒ 「本地优先可用」重新变成伪命题 ⇒
    // 旧判据（不许声称本地可用 + 必须如实写「走云端」）必须能重新咬人。
    // ⇒ 用合成夹具证明 sttHeaderIsHonest 在 skeleton regime 下仍在咬，
    //   而不是依赖仓里恰好是哪个 regime（§capabilities-stub-gate 同族纪律）。
    const reworded = `/**\n` +
      ` * 实现「设备端优先」策略：\n` +
      ` *   1. 先试 on-device sherpa 权重 —— native only，已支持。\n` +
      ` *   ⚠ 与此同时，实际 100% 走云端。\n */\nimport x from 'y'\n`
    assert.equal(sttHeaderIsHonest(headerOf(reworded), true), false,
      '骨架 regime 下，声称本地可用（哪怕补了「走云端」句）必须判不诚实 ⇒ 判据失效')
    const claimNoHonesty = `/**\n * 本地优先，云端兜底。\n */\nimport x from 'y'\n`
    assert.equal(sttHeaderIsHonest(headerOf(claimNoHonesty), true), false,
      '骨架 regime 下，声称本地可用却不说「实际 100% 走云端」必须判不诚实')
  })

  it('★★ 负控：把旧头放回去，判定必须立刻翻脸', () => {
    const stale = `/**\n * Implements the "local-first + cloud-fallback" strategy:\n` +
      ` *   1. ${STALE_LOCAL_CLAIM} — native only.\n` +
      ` *   2. ${'If unavailable or low-confidence, ' + STALE_CLOUD_CLAIM}\n */\n` +
      `import { sherpa } from '../native/sherpa'\n`
    assert.equal(sherpaRegisteredUnimplemented(CAPS), false, '负控前提失效：能力表里居然还有 Sherpa 登记')
    // 旧头里的云端型号写死句子在任何形状下都必须红 —— 这是本门保底判据
    assert.equal(sttHeaderIsHonest(headerOf(stale), sherpaRegisteredUnimplemented(CAPS)), false,
      '负控本身写错了：写死云端型号的旧头本该被判为不诚实')
    assert.equal(sttHeaderIsHonest(headerOf(STT), sherpaRegisteredUnimplemented(CAPS)), true,
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
  // ⚠ 2026-11-14 形状切换：Sherpa 表登记已清空（Phase 4 实现落地），
  //   本门按 `diarizationClaimIsHonest(src, sherpaIsSkeleton=false)` 的
  //   「骨架接上」形状运行 —— 负控仍用合成夹具证明判据在 skeleton regime
  //   下会咬（不许单向门）。
  it('前提自证：能力表不再登记 Sherpa 为未实现（ECAPA 侧判据按接上形状运行）', () => {
    assert.equal(sherpaRegisteredUnimplemented(CAPS), false,
      '能力表的 NOT_IMPLEMENTED_PLUGINS 里出现了 Sherpa —— ' +
      '要么实现被回退（本门须换回 skeleton 形状重查 diarization 文件头），要么表被改坏了，先查清再动')
  })

  it('★ 当前文件是诚实的（正向；骨架接上 ⇒ 任何提法都不构成谎言）', () => {
    const bad = ecapaClaimLines(DIAR)
    assert.deepEqual(bad, [],
      `speaker-diarization.ts 里这几行把原生 ECAPA 说成了可用：\n${bad.join('\n')}`)
    assert.match(DIAR, /NOT_IMPLEMENTED_PLUGINS/,
      '文件里找不到 NOT_IMPLEMENTED_PLUGINS ⇒ 它没告诉读者权威登记在哪，' +
      '下一个人还是会顺着「ECAPA」去找 AAR 集成步骤')
    assert.equal(diarizationClaimIsHonest(DIAR, sherpaRegisteredUnimplemented(CAPS)), true,
      '负控本身写错了：实现落地后当前文件（哪怕有「已具备」字样的订正注释）本该被判为诚实')
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
    //   2026-11-15 实核：当前命中共 2 处（第 36/41 行），分别位于 JSDoc
    //   与执行注释；代码确实先尝试原生再兜底，描述的是**尝试顺序**
    //   而不是可用性；`speaker-features.ts` 的是
    //   「若曾在支持 ECAPA 的设备上录过声纹」——反事实条件句。
    // ⛔ 若哪天要把这两处也纳入，先想清楚它们与本判据要禁的不是同一类句子。
    assert.equal(ecapaClaimLines(read('native', 'speaker-embedding.ts')).length, 2,
      'speaker-embedding.ts 的命中数变了 ⇒ 它已被改动，先核过再决定要不要纳入判据')
  })
})
