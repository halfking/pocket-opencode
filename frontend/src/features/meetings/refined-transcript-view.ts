// refined-transcript-view.ts — 决定「会议详情页该展示原文还是精校版」。
//
// ── 为什么需要这个模块（§107）──────────────────────────────────────
//
// 精校链在 §105 修好之前是「生产必然 404」，所以**从来没有人需要回答**
// 「精校结果给用户看吗」这个问题。而现在它真跑起来了，
// 事实链是：
//
//   finalizeRecording → meetingsApi.refine() → result.refinedTranscript
//     → updateMeeting(id, { refinedTranscript, status: refineStatusFor(...) })
//     → localDB（native/schema.ts 有 refined_transcript 列）
//
// 数据**一直都在** `LocalMeeting.refinedTranscript` 里（meetings-store.ts:257），
// 但 `MeetingDetailView.vue` **从头到尾没有读过这个字段** ——
// 全 `.vue` 侧唯一读它的是 `NotesHubView.vue:316` 的一行摘要预览。
//
// ⇒ 用户在会议里录完、精校真跑了、结果真存了，**打开这场会议看不到它**。
// 这是「做了但不可见」，不是设计取舍：功能等于不存在。
//
// ── 为什么是纯函数而不是直接写 computed ────────────────────────────
//
// ① 它决定**「说谎风险」**：status=`refine-failed` 时 refinedTranscript 里装的是
//    **原文**（`api/meetings.ts:371/387` 的回落链就是 `refinedTranscript: transcript`）。
//    此时若照常标成「已精校」，就是**把降级说成成功** —— 与 §16 / §36 同型。
// ② 纯函数能被单测断言返回值，不必挂载组件；
//    组件里那段 computed 与本文件分叉后，测到的就不是真在跑的那段。

/** 详情页展示哪一版转写 */
export type RefinedViewMode = 'refined' | 'original'

export interface RefinedViewInput {
  /** localDB 里的精校全文；降级时它装的是原文 */
  refinedTranscript: string | null
  /** `refineStatusFor` 的返回值之一：`'refined' | 'refine-failed'` */
  status: string | null
}

export interface RefinedView {
  /** 有没有「精校结果」可展示 —— 降级的那份**不算** */
  hasRefined: boolean
  /** 默认展示哪一版 */
  mode: RefinedViewMode
  /** 给用户看的那句话；空串表示不显示徽章 */
  badge: '' | '已精校' | '精校未生效 · 下面显示原文'
}

/**
 * resolveRefinedView 判定会议详情页的转写展示形态。
 *
 * ⚠ 关键规则：`status === 'refine-failed'` 时**即便 refinedTranscript 非空也不展示它**。
 *   因为回落链（`fallbackRefine`）写进去的就是原文，
 *   把它当「精校版」展示 = 把一次没成功的精校说成成功了。
 *
 * @param input 会议记录上的两个字段
 * @returns 展示形态、默认模式与徽章文案
 */
export function resolveRefinedView(input: RefinedViewInput): RefinedView {
  const text = (input.refinedTranscript ?? '').trim()
  const degraded = input.status === 'refine-failed'

  if (text === '') {
    return { hasRefined: false, mode: 'original', badge: '' }
  }
  if (degraded) {
    // 徽章要说清「你看到的还是原话」，而不是沉默 —— 否则用户会以为
    // 精校跑过了、只是内容没变。
    return { hasRefined: false, mode: 'original', badge: '精校未生效 · 下面显示原文' }
  }
  return { hasRefined: true, mode: 'refined', badge: '已精校' }
}