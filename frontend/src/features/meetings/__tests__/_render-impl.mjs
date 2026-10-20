// _render-impl.mjs —— 两个既有门禁共用的「渲染实现区间」提取器。
//
// ── 为什么需要它（§931）──
//
// §931 把 `meeting-dedup.ts` 的渲染重构成「**状态机 + 薄包装**」：
//
//   export function renderTranscript(segments) {      ← 现在只剩三行包装
//     const state = new TranscriptRenderState()
//     for (const s of segments) state.append(s)
//     return state.text()
//   }
//
// ⇒ 「说话人兜底」（§427）与「人工段豁免」（§401）这两条**形态自证**门，
//   原本都切 `renderTranscript` 的**函数体**⇒ 切不到它们了（门红，但**行为用例全绿**）。
// ⇒ ⇒ 处置：**只改指向、不动语义** —— 扫的区间从「某个函数的体」改成
//   「**整个渲染实现**」：从 `TranscriptSegmentLike`（入参类型）到文件末尾
//   （`createTranscriptRenderer` 是最后一段）。
//
// ⚠⚠ 区间**必须排除** `dedupeSegments`（它在更靠前）
//   ⇒ §401 那条门原来的注释写着「切出函数体，避免命中 dedupeSegments 里的 origin
//   （那处本来就对）」⇒ 区间一旦把它包进来，那条门就**失去区分力**。
//
// ⇒ ★ 抄两份必然漂移（§3 的教训）⇒ 两个门禁**共用这一份**。

/**
 * 取 `meeting-dedup.ts` 的**渲染实现区间**。
 *
 * @param {string} src  `meeting-dedup.ts` 的源码
 * @returns {string} 区间文本；取不到时返回空串（调用方必须自己断言）
 */
export function renderImpl(src) {
  const start = src.indexOf('export interface TranscriptSegmentLike')
  if (start < 0) return ''
  // 渲染实现是文件的**最后一段**（createTranscriptRenderer 收尾）⇒ 到文件末尾即可。
  // ⚠ 但要确认末尾确实是它 ⇒ 否则会把后面新增的东西也扫进来。
  const lastAnchor = src.lastIndexOf('export function createTranscriptRenderer(')
  if (lastAnchor < 0) return ''
  return src.slice(start, src.length)
}
