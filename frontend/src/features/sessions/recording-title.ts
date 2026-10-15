// recording-title.ts — 随手记会话录音建会时用的会议标题（§388）。
//
// ── 为什么单独一个文件（§52）──────────────────────────────────────
//
// 规则只有一条：**ID 片段不是标题**。
//
// `sessionTitle()` 的第三档兜底是 `sessionID.slice(0, 8)`
// （`SessionConversationView.vue:147`），那是**给界面显示**用的 ——
// 界面头部得有东西可显示。但它恒为真，于是：
//
//   ① 建会时 `title: sessionTitle() || '会话录音'` 里的 `||` **永远短路** ⇒
//      作者自己写的兜底从未生效，会议标题恒为 `ses_edac2`；
//   ② 会议列表里每条随手记录音都显示成同一个 ID 片段，彼此无法区分
//      （`MeetingListView.vue:44` 的 `'未命名会议'` 兜底同样永远走不到）；
//   ③ 精校的术语表里逐字收到「会议主题：ses_edac2。」
//      —— §378 的机制表没有这一档（依据非空但非自然语言），未测。
//
// ⚠ **这里不做什么**：
//   · 不改 `sessionTitle()` 的显示兜底（界面要显示东西，那是正常行为）；
//   · 不换命名策略（会议该叫什么是产品决策，见 §388.3②）。
//   只是让既有那行 `|| '会话录音'` 重新可达。
//
// ── 为什么必须是独立纯模块 ─────────────────────────────────────────
//
// 首版把这段逻辑内联在 `useSessionLiveRecord.ts` 里 ⇒ 测试 import 那个文件时
// 连带解析 `'../../composables/useMeetingRecorder'`（**无扩展名**）⇒
// `ERR_MODULE_NOT_FOUND`，用例一条都跑不起来。
// ⇒ 仓内同一纪律见 §52：「决策全部在纯函数里…而该函数在 node 里跑不到
// ⇒ 那些决策**一行行为门都没有**」。纯模块零 import，node 直接能跑。

/**
 * @param sessionId    当前会话 ID（来自 opencode，格式不在本仓定义）
 * @param sessionTitle 会话标题，可能是界面显示用的 ID 片段
 */
export function resolveRecordingTitle(sessionId: string, sessionTitle: string): string {
  const t = (sessionTitle ?? '').trim()
  if (!t) return '会话录音'
  const id = sessionId ?? ''
  // ⭐ 按「是不是当前 ID 的前缀」判定，而不是按 `ses_` 前缀的形状硬编码：
  //   ID 格式不由本仓定义（来自 opencode），形状变了这里自动跟着变。
  // ⚠ `id.length > 0` 不能省：否则 `''.startsWith(任何串)` 为真 ⇒ 所有标题都被吞掉。
  if (id.length > 0 && t.length <= id.length && id.startsWith(t)) return '会话录音'
  return t
}