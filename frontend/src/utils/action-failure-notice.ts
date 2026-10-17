/**
 * action-failure-notice.ts — 「对一条已有记录做一次动作」失败时的文案（纯函数，无运行时依赖）。
 *
 * ── 为什么它存在（§838）──
 *   会议列表的归档/恢复/删除、会议详情里的删除、笔记的元信息保存/删除，
 *   这五个处理器此前**都没有 try/catch**，而它们各自要写库 ⇒ 写失败时：
 *     · 列表/面板**保持原样**（那些 `meetings.value = …filter(…)` 都排在 await 之后，
 *       这点是对的：不能假装成功）；
 *     · 但**零反馈** —— 用户点了归档，看到列表没变，也**没有任何说明**。
 *
 *   同一节里还查到两处「注释与事实不符」：
 *     · 会议删除后的音频清理写成 `catch { /* ok *\/ }`（两处）
 *       ⇒ `deleteMeetingAudio` 写的是 IndexedDB（`openDB()` + 事务），
 *         失败时音频 blob **永久残留**且零痕迹 ⇒ 那是**静默的存储泄漏**，
 *         而代码注释声称没关系。
 *
 * ── 文案的统一规则 ──
 *   ① 必须说清**现在东西还在不在**（用户下一步据此决定要不要再点一次）；
 *   ② 必须给可执行动作；
 *   ③ 不得把技术串（SQLITE_BUSY / 未初始化 / openDB…）漏给用户。
 */

/** 一次「对已有记录的动作」。 */
export type RecordAction = 'archive' | 'restore' | 'delete' | 'meta-save' | 'meta-delete'

export interface RecordActionFailureInput {
  error?: unknown
  /**
   * 删除类动作专用：主体已删掉，但**附属文件/媒体没能清理**。
   * 这与「删除失败」是两件事，必须分开说 —— 前者用户不必重试，后者必须重试。
   */
  mediaCleaned?: boolean
}

export function describeRecordActionFailure(
  action: RecordAction,
  input: RecordActionFailureInput = {},
): string {
  switch (action) {
    case 'archive':
      return '归档失败，这场会议仍在列表里。请稍后重试。'
    case 'restore':
      return '恢复失败，这场会议仍在归档列表里（没有回到正常列表）。请稍后重试。'
    case 'delete':
      return input.mediaCleaned === false
        ? '会议已从列表删除，但它保存的音频没能清理，会继续占用本地空间。' +
          '删除本身不必重试；若反复出现，请检查本地存储权限。'
        : '删除失败，这条记录仍在。请稍后重试。'
    case 'meta-save':
      return '信息没能保存，面板保持打开以便你重试 —— 你刚填的内容还在输入框里。'
    case 'meta-delete':
      return '删除失败，这条记录仍在。请稍后重试。'
    default:
      return '操作失败，数据没有变动。请稍后重试。'
  }
}

/** 诊断前缀（会议侧）。 */
export const RECORD_ACTION_FAIL_TAG = '[record-action] 操作失败：'

/** 诊断前缀（附属媒体清理失败，与主体操作失败区分）。 */
export const RECORD_MEDIA_CLEANUP_FAIL_TAG = '[record-action] 主体已删除，但媒体/文件清理失败（将残留占用空间）：'
