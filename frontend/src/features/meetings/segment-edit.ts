/**
 * segment-edit.ts —— §131 交互式校对：更正**已存在**的一段转写正文
 *
 * 为什么要有这一层（2026-10-07 实测）
 * ------------------------------------
 * §131 之前的形态是：转写段在 `TranscriptSegmentList.vue` 里是**只读**的
 * （`<p class="segment-text">{{ seg.text }}</p>`）。仓里唯一的「写入」入口是
 * 列表底部的 composer，而它 emit 的是 `append`（**追加一句新段**）——
 * 追加不是更正：原错句一个字没动，改正句还会被 §132 刚修好的相邻去重裁碎。
 * ⇒ 用户听错的人名/地名/数字在 App 内**没有任何更正办法**。
 *
 * 为什么必须是纯函数、而不是直接在组件里 splice
 * ----------------------------------------------
 * 这条链上有一处**两写者**陷阱：`displaySegments` 的数据源是
 * `segments.value.length ? segments.value : storedSegments.value`
 * —— 录音中读 runtime 的内存副本，没在录读 localDB 的行。
 * 两边都要改，而「改哪一边、要不要新建一段、空文本怎么办」全是判定，
 * 放进 `.vue` 就只能靠扫源码来测（扫不出真假）。抽到这里之后：
 *  - 「没命中 id 就不能凭空造一段」是可断言的行为，不是注释；
 *  - 「其它段的 origin 不许被顺手改成 manual」也是（否则关掉去重就能换绿灯，
 *    与 §132 的 M2 同一族反向）。
 *
 * ⚠ `origin` 必须翻成 `'manual'`，且只翻被改的那一段 —— 这是本文件存在的
 * 全部理由：用户的更正若还留在 `'asr'`，下一次 `dedupeSegments` 会把它当
 * 重转写裁碎（§132 实测 `张伟下周三交对比表` → `伟`）。
 */
import type { MeetingSegment } from './meetings-store'
// ⚠ 显式带 `.ts`：node 跑 .mjs 门时对 .ts 走 type-stripping，**不做扩展名解析**，
//   `import { normalizeUtterance } from './meeting-utterance'` 会在门里
//   ERR_MODULE_NOT_FOUND（type-only 导入会被整条剥掉，所以只有值导入才暴露）。
import { normalizeUtterance } from './meeting-utterance.ts'

/** 没改成时必须能说清「为什么没改」—— 页面要据此决定是否提示用户。 */
export type SegmentEditRejection = 'not-found' | 'empty-text' | 'unchanged'

export interface SegmentEditResult {
  /** 改动后的数组；未改动时**就是入参那一个引用**（供调用方跳过重渲染）。 */
  segments: MeetingSegment[]
  changed: boolean
  reason: 'ok' | SegmentEditRejection
  /** 只在 changed 时有值。 */
  segment?: MeetingSegment
}

export function applySegmentTextEdit(
  segments: readonly MeetingSegment[],
  segmentId: string,
  rawText: string,
): SegmentEditResult {
  const at = segments.findIndex((s) => s.id === segmentId)
  if (at < 0) return { segments: segments as MeetingSegment[], changed: false, reason: 'not-found' }

  // 与 `buildUtteranceSegment` 同一把尺子：空白折叠 + 去首尾空。
  // 空文本**必须拒**：写进去会得到一行 `[说话人] ` 的空段，而
  // `renderTranscript` 的「零净增段不产出一行」用例（meeting-dedup-asr.test.ts:74）
  // 挡不住它 —— 那条只管去重的净增，管不住手工段。
  const text = normalizeUtterance(rawText)
  if (!text) return { segments: segments as MeetingSegment[], changed: false, reason: 'empty-text' }
  if (text === segments[at].text) {
    return { segments: segments as MeetingSegment[], changed: false, reason: 'unchanged' }
  }

  const next = segments.slice()
  const edited: MeetingSegment = { ...segments[at], text, origin: 'manual' }
  next[at] = edited
  return { segments: next, changed: true, reason: 'ok', segment: edited }
}
