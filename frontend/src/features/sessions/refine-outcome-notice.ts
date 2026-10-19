/**
 * refine-outcome-notice.ts — 精校结果的**降级/拦下**文案（纯函数，无副作用）。
 *
 * ⚠ 抽成无 value import 的纯模块，是因为这个文案本身就是判据：
 *   它承诺「告诉用户到底发生了什么」，一旦有人把分支合并回调用点，
 *   就没人能单独验它（§23 的提醒成败从不上报是同一族）。
 *
 * ★ 为什么必须区分「无法解析」与「被拦下」（2026-10-07 实测）：
 *
 *   服务端有两条**不同**的回落路径，都置 refine_fallback=true：
 *
 *     1. parseRefineJSON 失败      —— 模型没遵守 JSON 契约
 *     2. guardRefineResult 拦下    —— 精校结果里混进了原始转写中不存在的
 *                                     参会人姓名（模型把人名换成了名单里的人）
 *
 *   两件事对用户毫无共同点。若都报「云端返回无法解析」，
 *   那就是**用一句错的话解释一次正确的行为** ——
 *   用户会去查网络/云端，而真正的原因（名单污染）无人知晓。
 *   这与本会话反复记的「桶名是对外承诺」是同一条。
 */

export type RefineOutcome = 'ok' | 'parse-fallback' | 'rejected' | 'adopted-with-loss'

export interface RefineNoticeInput {
  /** normalizeRefine 判定「结果不是真正精校」 */
  fromFallback?: boolean
  /** 服务端 guardRefineResult 拦下了精校结果 */
  rejected?: boolean
  /**
   * ⚠⚠ §908/D77：`rejected` 的**成因**。它现在有两个，说法必须不同：
   *   - `participant_leak`：精校稿混进了原文没有的参会人姓名（§35）
   *   - `shrinkage`：模型**缩写**了（回显不完），不是纠错（§907/D76）
   * 不给就按 `participant_leak` 说（保持既有行为，向后兼容）。
   */
  rejectedReason?: RefineRejectReason
  /**
   * ⚠⚠ §910/D79：`assessRefineRetention` 判定**不通过**的原因码
   * （`refine-empty` / `refine-too-short(...)`）。
   * ⚠ 它命中的正是「响应完整、但模型把精校做成了摘要」那一档 ——
   *   而文案此前一律说「云端返回无法解析」，那是**错的**：
   *   JSON 解析得好好的，模型解析失败那句话会让用户去查错的方向。
   */
  retentionReason?: string
  /** 本地整段重转已采用 */
  adopted?: boolean
  /** 重转里有分片失败 */
  failedSegments?: number
}

/** 精校结果被拦下的两种成因（§908/D77）。 */
export type RefineRejectReason = 'participant_leak' | 'shrinkage'

/**
 * 返回给用户的精校结果文案。
 * - kind 为 info 时用 toast.info，为 success 时用 toast.success
 * - text 已经把「发生了什么」说清楚，不需要调用点再加工
 */
export function buildRefineNotice(input: RefineNoticeInput): {
  kind: 'info' | 'success'
  text: string
} {
  const { fromFallback, rejected, rejectedReason, retentionReason, adopted, failedSegments } = input
  const adoptSuffix = adopted ? '已整段重转；' : ''

  // ⚠ 顺序不能反：rejected 同时也会带 fromFallback=true，
  //   先判 parse-fallback 会把「被拦下」误报成「无法解析」。
  if (rejected) {
    // ⚠⚠ §908/D77：`shrinkage` 与 `participant_leak` 的**原因对用户完全不同**。
    //   §907 给长会议加了缩写护栏之后，这条分支会在**每一场长会议**上触发；
    //   若仍说「检测到与参会人名单不符的人名」，用户会去查参会人名单 ——
    //   而名单从来没问题，真实原因是「这场会太长，模型回显不完」。
    //   ⇒ 本文件头写着「桶名是对外承诺」，这条就是它的第二个兑现点。
    if (rejectedReason === 'shrinkage') {
      return {
        kind: 'info',
        text: `${adoptSuffix}精校未生效：这场会议转写太长，模型没能完整回显，` +
          `已丢弃不完整的精校稿并显示原始转写（内容完整，只少了错字纠正）`,
      }
    }
    return {
      kind: 'info',
      text: `${adoptSuffix}精校结果被拦下（检测到与参会人名单不符的人名，已丢弃并显示原始转写）`,
    }
  }
  if (fromFallback) {
    // ⚠⚠ §910/D79：留存判定不通过**不是**「无法解析」。
    //   `refine-too-short` 的形态是「响应完整、但模型把精校做成了摘要」，
    //   报成解析失败会让用户去查网络/云端，而真正的原因无人知晓。
    if (retentionReason?.startsWith('refine-too-short')) {
      return {
        kind: 'info',
        text: `${adoptSuffix}精翻未生效（这份精校比原文短太多，可能是模型写成了摘要），`
          + `显示的是原始转写（内容完整，只少了错字纠正）`,
      }
    }
    return {
      kind: 'info',
      text: `${adoptSuffix}精翻未生效（云端返回无法解析），显示的是原始转写`,
    }
  }
  if (adopted && failedSegments && failedSegments > 0) {
    return {
      kind: 'success',
      text: `录音已结束，已整段重转并精翻（有 ${failedSegments} 段未转出）`,
    }
  }
  if (adopted) {
    return { kind: 'success', text: '录音已结束，已整段重转并精翻' }
  }
  return { kind: 'success', text: '录音已结束，精翻完成' }
}

/**
 * 会议该落成哪个 status。
 *
 * ⚠ 为什么必须单列一个「精翻未生效」状态（2026-10-07）：
 *
 *   录音流的 toast 已经改诚实了，但**会议列表的徽章**还写着
 *   `refined → '已精翻'` —— 而 `meeting-ingest.ts` /
 *   `useSessionLiveRecord.ts` / 后端 `finalizeMeetingRefine`
 *   三处都**无条件**把 status 设成 `'refined'`。
 *
 *   ⇒ 用户打开列表，看到一条「已精翻」，点进去却是刚说过的原话。
 *   降级发生在最后一跳，而最后一跳的**文案**已经修好了、
 *   **状态**没跟上 —— 这是本会话第四次遇到「桶名与承诺不符」，
 *   这次它藏在状态机里而不是 toast 里。
 *
 *   为什么不复用 `'completed'`：那会让「已完成」同时表示
 *   「正常完成」和「精翻失败」，把两者混进同一个桶 —— 正是本会话
 *   反复记的那件事。
 */
export function refineStatusFor(input: RefineNoticeInput): 'refined' | 'refine-failed' {
  return input.fromFallback || input.rejected ? 'refine-failed' : 'refined'
}

