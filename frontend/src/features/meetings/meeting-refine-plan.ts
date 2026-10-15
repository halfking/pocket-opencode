/**
 * meeting-refine-plan.ts — 收尾链的**纯决策层**（§52）。
 *
 * ★ 为什么要单独成文件，而不只是从 `finalizeRecording` 里拆个函数出来：
 *   拆函数不够。`meeting-recording-finalize.ts` 的**顶层 import** 会拖进
 *   `api/stt-settings` → 浏览器/HTTP 代码，node 里 `import` 它直接
 *   ERR_MODULE_NOT_FOUND（实测踩过）。
 *   ⇒ 纯函数放在那个文件里，它仍然一行行为门都写不了。
 *   所以：**决策层不许 import 任何 I/O**，这是它能被真输入直接验的前提。
 *
 * ★ 这里的每一个决策都对应一个真实事故（都不是假想）：
 *   · 采纳判定          —— 整段重转变短一半以上就别采纳（内容凭空变少）
 *   · 说话人归因        —— §40；只有 1 条回执时必然失效并丢标签
 *   · 名单从**原始分段**兜底 —— §52 新发现：归因失效时 baseSegments 标签全丢，
 *                          若名单只从它取 ⇒ 同音纠正静默不跑（§45 的老坑）
 *   · 人名同音纠正      —— §44/§45/§48
 */
import {
  buildRefineBaseSegments,
  shouldAdoptFullTranscript,
  type FullSegmentReceipt,
} from './meeting-final-transcript.ts'
import { deriveRoster, repairRosterHomophones, repairSegmentBoundaryHomophones } from './meeting-roster-homophone.ts'
import { PINYIN_COMMON } from './pinyin-common.ts'
import { dedupeSegments } from './meeting-dedup.ts'
import type { MeetingSegment } from './meetings-store.ts'

/* ========================================================================
 * 决策层：纯函数，不碰 I/O（§52）
 * ========================================================================
 *
 * ★ 为什么把它从 `finalizeRecording` 里拆出来（这是 §52 的全部理由）：
 *   §45/§48/§49/§50 四轮的 bug **没有一个在 I/O 里**——
 *     §45 名单恒空（读错了源）、§48 consumers 没人调（顺序对但没接）、
 *     §49 分段恒空（读数在 stop 后失效）、§50 toWav 没传（该转没转）。
 *   它们全是**决策**错误。而 `finalizeRecording` 因为依赖 localDB + HTTP
 *   在 node 里跑不到，于是这些决策**一行行为门都没有**，只能靠人读源码。
 *
 *   与其给生产模块开五个注入口把它变成「注入袋」，不如把决策本身提成纯函数：
 *   I/O 留在薄壳里，决策可以被真输入直接验。
 */

export interface RefinePlanInput {
  meetingId: string
  /** 来自**持久层**的分段（§49：不能用录音态读数，stop 之后恒为 []）。 */
  segments: MeetingSegment[]
  /** 整段重转结果（`refetchFullTranscript` 的返回）。 */
  full: {
    text: string | null
    applied: boolean
    segments?: FullSegmentReceipt[] | null
  }
  /** `meeting.participants`（用户手填的名单；随手记路径为空）。 */
  participants?: string[] | null
  /** 录音总时长 ms（来自 `rt.stop()` 的返回值，不是 `recorder.elapsedMs`）。 */
  elapsedMs: number
}

export interface RefinePlan {
  /** 是否采纳整段重转文本 */
  adopted: boolean
  /** 采纳判定的原因（可观测，便于排障时区分「转写失败」与「质量不够」） */
  adoptReason: string
  /** 交给纠错/精校的分段 */
  baseSegments: MeetingSegment[]
  /** 推导出的权威名单 */
  roster: string[]
  /** 真正发给 refine 的分段 */
  refineSegments: MeetingSegment[]
  /** 人名同音纠正的条数（0 表示没改或没名单） */
  rosterFixes: number
}

/**
 * 收尾的**纯决策**：采纳什么、用哪些分段、名单是谁、要改哪几个同音字。
 *
 * 三步的顺序不能换（各自的理由见调用点注释）：
 *   1. 采纳判定 —— 整段重转不保证更好，变短一半以上就沿用分段文本
 *   2. 说话人归因 —— 把高精度文本按时间挂回原有标签
 *   3. 人名同音纠正 —— 必须在精校之前，LLM 才看得到正确的名字
 */
export function planRefine(input: RefinePlanInput): RefinePlan {
  const { segments, full, elapsedMs, meetingId } = input
  const adopt = full.applied && full.text
    ? shouldAdoptFullTranscript(full.text, segments)
    : { adopt: false, reason: full.applied ? 'full-empty' : 'not-applied' }
  // ★ 两条出口都要去重（2026-10-06）。
  //
  //   切片重叠会让相邻分段产生重复文本（「今天今天下午三点」）。它已经在
  //   ingestSpeechBlob / recordingRuntime / useLiveSummary / api 降级 /
  //   MeetingDetailView 五处出口被消解，但**这里拿到的 segments 是从持久层
  //   读回来的原始行**（finalizeRecording 的调用方注释明说「读持久层而不是
  //   recorder.runtime.segments」）——那是从 DB 重建的数组，不经过任何出口。
  //
  //   两条出口分别是：
  //     · buildRefineBaseSegments 的产物（整段重转按时间挂回标签）
  //     · adopt.adopt 为假时**直接回落**的原始 segments（归因失败/未重转）
  //   任一条都可能带重复进 refine 的 prompt ⇒ 精校会把「今天今天」当正常文本
  //   润色并固化。
  const baseSegments = dedupeSegments(
    adopt.adopt && full.text
      ? buildRefineBaseSegments({
          meetingId,
          original: segments,
          fullText: full.text,
          fullSegments: full.segments,
          elapsedMs,
        })
      : segments,
  )
  // 逐段修正而非整篇拼接后修正：拼接后要把字符位置映射回 segment，
  // 出错时会把修正落到**别的段**上，而错名字比漏修正更糟。
  //
  // ★★ 名单要同时看 **baseSegments 与原始 segments**（§52 行为门逼出来的真 bug）。
  //   归因失效时（只有 1 条回执，§50 对真 webm 的实测形态）`baseSegments`
  //   会塌成**一条 speakerLabel=null** 的分段；此时若名单只从它取 ⇒ 名单为空
  //   ⇒ 同音纠正**静默不跑**。而真名明明还在原始分段里躺着。
  //   ⇒ 名单是**权威信息**，不该跟着「这一轮用来做什么」的实现细节一起丢。
  const roster = deriveRoster({
    participants: input.participants,
    speakerLabels: [
      ...baseSegments.map((sg) => sg.speakerLabel),
      ...segments.map((sg) => sg.speakerLabel),
    ],
  })
  // ⚠ 名单为空时**不需要**在这里特判：`repairRosterHomophones` 对空名单
  //   原样返回（它的第一行就是 `if (trimmed.length === 0) return {text, fixes: []}`）。
  //   变异 P5 证明了这一点 —— 去掉这个守卫，所有判据全绿。
  //   ⇒ 单一来源放在被调函数里，不在调用方再写一遍「看起来承重」的三元。
  let rosterFixes = 0
  const perSegment = baseSegments.map((sg) => {
    const r = repairRosterHomophones(sg.text, roster, PINYIN_COMMON)
    if (r.fixes.length > 0) rosterFixes += r.fixes.length
    return r.fixes.length > 0 ? { ...sg, text: r.text } : sg
  })
  // ★ 跨段人名纠正（§80）：逐段纠正看不见**被切在段边界上**的名字。
  //
  //   实测（真网关、生产提示词、两模型各 3/3）：单段能纠正，切段纠正 0 条，
  //   而 LLM 精校**只把字改对（章→张）、跨行合并不了** ⇒ 用户看到
  //   「今天我们请到了张」/「伟来给大家讲一下产品路线」两个气泡。
  //
  //   放在逐段之后：先修好段内的，再处理跨段的 —— 顺序反了会让跨段扫描
  //   看到已被改过的文本，而那时的窗口判定已不是同一件事。
  const boundary = repairSegmentBoundaryHomophones(perSegment, roster, PINYIN_COMMON)
  rosterFixes += boundary.fixes.length
  const refineSegments = boundary.texts.map((text, i) =>
    text !== perSegment[i].text ? { ...perSegment[i], text } : perSegment[i],
  )
  return {
    adopted: adopt.adopt,
    adoptReason: adopt.reason,
    baseSegments,
    roster,
    refineSegments,
    rosterFixes,
  }
}

