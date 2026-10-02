// dead-api-classify.mjs — 「死能力」四分类的**纯函数**部分。
//
// 单独成文件是为了让判据能被测试直接 import 执行。check-dead-api.mjs 本身一
// import 就会跑整个扫描并 process.exit，拿它当判据等于测不了——
// 「判据必须可执行，否则没法证明它会红」。
//
// ## 四类（顺序即语义）
//
//   wired          被本模块以外的**非测试**文件引用 ⇒ 已接进 App
//   moduleInternal 只在本模块体内被引用（常量/内部 helper，外部拿不到）
//   testOnly       **连本模块都没用**，只被测试引用 ⇒ 能力被测过但没接进 App
//   dead           哪都没被引用 ⇒ 这才是「写好了没人接线」，棘轮管的就是它
//
// ## 为什么 moduleInternal 必须排在 testOnly 前面
//
// 原实现是 `app > 0 → wired`、`test > 0 → testOnly`、`ownRefs > 0 → moduleInternal`。
// 于是**同时**被本模块使用、又被测试引用的符号会被判成 testOnly，输出
// 「⚠️ 仅被 __tests__ 引用：能力被测过，但没接进 App」——而它在 App 里正被使用。
//
// 实测被误报的一批（2026-10-03）：MEETING_SUMMARY_TIMEOUT_MS、MEETING_REFINE_TIMEOUT_MS、
// NOTE_SUMMARIZE_TIMEOUT_MS、STT_TRANSCRIBE_TIMEOUT_MS、STT_PROBE_TIMEOUT_MS、
// CLASSIFY_TIMEOUT_MS、PIPELINE_TIMEOUT_MS、extractErrorCode、RECONNECT_BASE_MS。
// 例如 MEETING_SUMMARY_TIMEOUT_MS 就是 meetingsApi.summarize 的 http 选项里那个值，
// 而 meetingsApi.summarize 被 useLiveSummary 调用。标成「没接进 App」是**说谎**，
// 而且带 ⚠️，会让人去「接线」一个早就接好的东西。
//
// 把 ownRefs 提到 test 前面即可：testOnly 收紧成「连本模块都没用」，那才是真正
// 值得警告的形态（导出了、测了、但业务里根本没人调）。
//
// ## 为什么这不削弱棘轮
//
// dead 的定义是 app===0 && test===0 && ownRefs===0，调整顺序不改变这个合取式，
// 所以 `dead` 集合逐元素不变，基线不动。守卫测的正是这一条。

/**
 * @param {{app: number, test: number, ownRefs: number}} refs
 * @returns {'wired' | 'moduleInternal' | 'testOnly' | 'dead'}
 */
export function classifyRef({ app, test, ownRefs }) {
  if (app > 0) return 'wired'
  if (ownRefs > 0) return 'moduleInternal'
  if (test > 0) return 'testOnly'
  return 'dead'
}

/** 各类在报告里的标题与标记（check-dead-api.mjs 直接用，避免两处漂移）。 */
export const CLASS_BUCKETS = [
  { kind: 'dead', mark: '❌', title: '完全无人使用（棘轮管的就是这批）' },
  { kind: 'testOnly', mark: '⚠️ ', title: '仅被 __tests__ 引用：能力被测过，但没接进 App' },
  { kind: 'moduleInternal', mark: 'ℹ️ ', title: '仅本模块内部使用：外部拿不到，常量/内部 helper' },
]
