/**
 * meeting-transcript-cache.ts — §925：`local_meetings.transcript` 这一列的**缓存策略**（纯函数）。
 *
 * ── 事实源与缓存的分工（这是本节修的那个缺陷的根）──
 *
 * `local_meeting_segments` 是**事实源**（每段一行，逐段落库）；
 * `local_meetings.transcript` 是它的**派生缓存**（整场转写拼成一段文本）。
 *
 * ⚠⚠ 而它此前是**当事实源在维护**的：
 *   · 写入侧：每新增一段，就把「到目前为止的整场转写」**整段重写**进这一行
 *     （`updateTranscript` → `UPDATE local_meetings SET transcript = ?`）
 *     ⇒ 一场会的写入量是 **O(时长²)**。
 *   · 读取侧：`listMeetings` 是 `SELECT *` ⇒ 每次列表都把**每场会的整场转写**读出来，
 *     而这一列在本地**只有一个消费者**：`NotesHubView` 的**预览**（`preview()` 只取 120 字）。
 *
 * ⇒ 实测（真 SQLite `node:sqlite`，真实 ASR 语料，1952 段 = 8 小时；
 *   ⚠ 耗时是**环境读数** ⇒ 交错重复 5 轮取中位数；落库字节数是**确定量**）：
 *   · 写入侧：现状落库 **376.3 MB**、中位 **17.3 s**（Mac 直连 SSD；手机上更贵）。
 *     节流后落库 **2.4 MB**、中位 **1.44 s** ⇒ 落库量 **0.65%**、耗时 **12×**。
 *   · 读取侧：30 场会议各 25 KB 转写 + 25 KB 精校稿 ⇒ 一次列表读出 **1.45 MB**，
 *     而界面只要 **≈10.5 KB**（30×120 字）；有界片段后读出 **85.7 KB**（**17×**）。
 *
 * ⇒ 修法（本模块就是那两个决策）：
 *   ① **写入按增长量节流**（**显式 opt-in**）：逐段追加那条路径声明「我可以晚点落库」，
 *      于是只有它被节流；其余写入点**默认照旧立刻落库**。
 *      ⇒ 8 小时会议 376.3 MB → **2.4 MB**、17.3 s → **1.44 s**。
 *   ② **列表只取有界的片段**：`substr(transcript, 1, 512) AS transcript`
 *      ⇒ 预览要的那 120 字**逐字不变**，而列表读出的字节从「每场整场」变成「每场 512 字符」。
 *      （`refined_transcript` 是**同一类**派生缓存 ⇒ 同样只取片段。）
 *
 * ⚠⚠ 为什么节流是**显式 opt-in** 而不是默认：
 *   默认节流 ⇒ 将来新增一个「用户改了一句话」的写入点时，若忘了声明 ⇒
 *   **用户的改动可能不落库** ⇒ ⛔ 那是丢内容。
 *   默认不节流 ⇒ 将来新增逐段写入点时忘了声明 ⇒ 只是多写几次（性能），
 *   **不会丢内容** ⇒ ⇒ ⇒ **把风险落在「慢」那一侧，不落在「丢」那一侧**。
 *
 * ⚠ 口径（必须说清，别让人误读成「转写被截断存了」）：
 *   这一列是**有界的派生缓存** ——
 *   · **头部**始终是最新的（预览只读头部 ⇒ 录音期间也看不出来）；
 *   · **尾部**最多落后 **32 KB ≈ 41 分钟**语音，**但只在录音期间**：
 *     收尾时 `recordingRuntime.stop()` 会**整篇补齐一次**（不声明 `throttled`）
 *     ⇒ 会议结束后这一列是**完整**的；
 *   · **事实源永远是 `local_meeting_segments`**。
 *   ⇒ 详情页、精校链、同音纠正全部走分段，不走这一列 ⇒ 不受影响。
 */

/**
 * 两次落库之间，转写至少要增长这么多字节才值得再写一次。
 *
 * ⚠ 取 32 KB 的理由（**两条都要成立，且都被判据按「派生性质」钉住**）：
 *   ① **够大**（省写入量）：8 小时会议转写 394.4 KB ⇒ 只有 **13 次**落库
 *     （而不是 1952 次）⇒ 落库量降到 **0.65%**。
 *   ② **够小**（滞后可控）：真实语料 ≈ **792.5 B/分** ⇒ 32 KB ≈ **41 分钟**语音。
 *     ⚠⚠ **这里必须说准**：32 KB ≈ 158 段 ≈ **41 分钟**，**不是**「几十秒」。
 *     （首版注释写的「≈ 40 秒」是**算错了 60 倍** —— 32 KB ÷ 206.9 B/段 = 158 段，
 *       158 段 × 14.8 秒/段 = 2336 秒 = **39 分钟**。）
 *     ⇒ 这段滞后**只发生在录音期间**（收尾会整篇补齐，见 `recordingRuntime.stop()`），
 *     而**录音期间唯一读这一列的消费者是 120 字预览**（读**头部** ⇒ 头部始终最新 ⇒ 看不出来）。
 */
export const TRANSCRIPT_PERSIST_MIN_GROWTH_BYTES = 32 * 1024

/**
 * 列表查询取转写的**字符数**上限。
 *
 * ⚠ 为什么是 512 而不是 120：`preview()` 先做 stripHtml / 剥 markdown 标记再截到 120 字
 * ⇒ 若只取 120 个**原始**字符，剥完可能不足 120 ⇒ 预览会短一截。
 * ⇒ 取 512 个原始字符 ⇒ 剥完仍然 ≥ 120（除非正文几乎全是标记）。
 */
export const TRANSCRIPT_LIST_PREVIEW_CHARS = 512

/**
 * 这一轮要不要把转写落库。
 *
 * ⭐ **默认（`throttled` 不给）= 立刻落库** —— 见文件头「为什么节流是显式 opt-in」。
 *
 * @param lastPersistedBytes 上一次落库时转写的**字节数**（0 = 从没落过）
 * @param nextBytes         这一轮转写的字节数
 * @param throttled         这一条路径声明「我可以晚点落库」（逐段追加那条）
 */
export function shouldPersistTranscript(input: {
  lastPersistedBytes: number
  nextBytes: number
  throttled?: boolean
}): boolean {
  // 默认路径：立刻落库（安全的一侧）。
  if (!input.throttled) return true
  // 第一次一定要落库：否则一场短会议（转写不到 32 KB）这一列会**永远是空的**
  // ⇒ 而本地唯一的消费者是预览 ⇒ 用户会看到一行空预览。
  if (!Number.isFinite(input.lastPersistedBytes) || input.lastPersistedBytes <= 0) return true
  // 用绝对值：转写被**整份替换**（整场重转的结果比原来短）时也要落库，
  // 否则这一列会一直留着旧版本（新的那份反而更短 ⇒ 差值是负数）。
  return Math.abs(input.nextBytes - input.lastPersistedBytes) >= TRANSCRIPT_PERSIST_MIN_GROWTH_BYTES
}

/**
 * 列表查询要读的列（`rowToMeeting` 用到的全都在这里，一个不多一个不少）。
 *
 * ⚠ 每一项都必须是 `rowToMeeting` 真的读的那一列 —— 少一列 ⇒ 列表里那个字段恒为 undefined。
 *   （判据在 meeting-dedup.test.ts 的 L 组：两份**互相核对**，治「抄两份必漂移」。）
 */
const MEETING_LIST_COLUMNS = [
  'id', 'title', 'location', 'participants', 'audio_path', 'duration_ms',
  // ⚠ 转写只取**有界片段**：这一列的唯一消费者是 120 字的预览（NotesHubView）。
  `substr(transcript, 1, ${TRANSCRIPT_LIST_PREVIEW_CHARS}) AS transcript`,
  'summary', 'live_summary',
  // ⚠ 精校稿**同理**：它也是分段转写的派生（同一类「派生缓存当事实源」）。
  //   列表里它的消费者**也只有**那一个 120 字预览（NotesHubView）⇒ 同样只取片段。
  //   （详情页走 `getMeeting` = `SELECT *` ⇒ 那里拿到的仍是**全文**。）
  `substr(refined_transcript, 1, ${TRANSCRIPT_LIST_PREVIEW_CHARS}) AS refined_transcript`,
  'recommendations',
  'note_id', 'session_id', 'status', 'started_at', 'created_at',
  'deleted_at', 'archived_at', 'tags', 'topic', 'summary_skill',
].join(', ')

/**
 * 会议列表的 SQL（**纯函数**：SQL 字符串本身可被行为门直接断言）。
 *
 * ⚠ 此前是 `SELECT *` ⇒ 每次列表都把每场会的**整场转写**读出来。
 * ⚠ 为什么不留 `SELECT *`：`*` 会把 `transcript` 这一列**整列**读进内存，
 *   而本地 SQLite 的一行本来就可能是几百 KB ⇒ 30 场会议 = 11.8 MB（界面只要 3.6 KB）。
 */
export function meetingListSql(archived: boolean, scopeClause?: string): string {
  // ⚠ `scopeClause` 省略时输出与改动前**逐字节相同** —— 这个函数的形状
  //   本身是行为门的判据（见 meeting-dedup.test.ts「列表 SQL 的列与
   //   rowToMeeting 读的字段对不上」），改它会让既有判据的锚点失配。
  //   归属过滤是**增量**的（2026-10-29）：不传就是老行为，传了才按归属收窄。
  //
  // ⚠ 归属未知的行**永远不入选**（scopeClause 为 `1 = 0`）：
  //   未认领 ≠ 属于当前账号 ⇒ 跨账号的历史行不该出现在列表里。
  const scope = scopeClause ? ` AND ${scopeClause}` : ''
  return `SELECT ${MEETING_LIST_COLUMNS} FROM local_meetings
     WHERE deleted_at IS NULL AND IFNULL(archived_at, 0) ${archived ? '>' : '='} 0${scope}
     ORDER BY started_at DESC LIMIT ? OFFSET ?`
}
