/**
 * meetings API — 会议摘要/推荐/精翻，代理 pocketd → kxmemory / LLM
 */
import { renderTranscript } from '../features/meetings/meeting-dedup.ts'
import { buildFallbackRefinePrompt } from '../features/meetings/refine-prompt.ts'
import { buildFallbackSummaryPrompt } from '../features/meetings/summary-prompt.ts'
import { http, isAbortError, LLM_CHAT_TIMEOUT_MS } from './http'
import type { ActionItem, LiveSummary, MeetingSegment, RecommendItem } from '../features/meetings/meetings-store'
import { truncateCodeUnits, truncateUtf8BytesTail, utf8ByteLength } from '../utils/text.ts'
import { assessRefineRetention } from './refine-retention-guard.ts'
import { assertCloudModule } from './data-policy.ts'
import { isModulePolicyLocalError } from './module-policy.ts'
import { PocketResourceUnavailableError, PocketResourceAuthorityError } from './pocket-resource'

// ⚠ helper 的真身在中立层 `src/utils/text.ts`（§251.2 兄弟链：
//   它也被 features/meetings/refined-ingest-writeback.ts 使用，
//   而那一层不能反向 import api 层，否则成环）。
//   这里**再导出**是为了让既有引用 `from '../api/meetings'` 不用改。
export { truncateCodeUnits }

/**
 * 会议链路长请求的客户端超时。
 *
 * 2026-10-03 普查出来的：这两个调用点原先**都没传 timeoutMs**，一律吃
 * http.ts 的默认 30 秒，而后端各自的预算是
 *
 *   handleMeetingSummary  context.WithTimeout(r.Context(), 45*time.Second)  ← 45s
 *   handleMeetingRefine   context.WithTimeout(r.Context(), 90*time.Second)  ← 90s
 *
 * （server_meeting.go:224 / 313）
 *
 * 两条全中：客户端 30s < 服务端 45s/90s。表现是「服务端写完了、前端报失败」
 * ——会议摘要偶尔转不出来、事后精翻一按就报错，而且只要推理真的用满预算就
 * **每次都失败**。
 *
 * 取值留出余量而不是与服务端相等：客户端计时含网络与鉴权开销，取相等值时
 * 客户端实际总是先到点。
 *
 * 同文件还有第三条 handleTranscribeMeeting（120s）目前**前端没有调用点**
 * （录音走 /api/stt/*），所以这里不给它配常量——留一个没人引用的常量只会
 * 让下一个人以为它生效了。
 */
export const MEETING_SUMMARY_TIMEOUT_MS = 90_000
export const MEETING_REFINE_TIMEOUT_MS = 150_000

export interface SummaryResult {
  summary: string
  keyPoints: string[]
  actionItems: ActionItem[]
  decisions: string[]
  openQuestions: string[]
  /**
   * 智能体检索到的参考资料（2026-10-06）。
   *
   * 只在 agent 路径（会议摘要走有界 tool-calling 循环、能调 search_notes
   * 查用户自己的笔记）返回；一次性 chat 路径没有这个字段 ⇒ 可空。
   *
   * noteId 指向用户自己的笔记，**不是模型编的** —— 提示词明确禁止编造引用，
   * 工具没返回的笔记不许进这个列表。
   */
  references?: SummaryReference[]
  /**
   * true = 服务端**智能体路径**产出了这一轮摘要（server_meeting.go 的
   * `result["agent"] = true`，只在 agent 分支里写）。
   *
   * ⚠ §109：这是**降级信号**，不是装饰字段。
   *   agent 路径失败时服务端会**回落**到一次性 chat，那条路径**不带这个键** ——
   *   而它也**不会**带 references ⇒ 用户要的「参考资料与建议」整块消失，
   *   界面此前对此**只字不提**。
   *   ⇒ 有这个键才有 references；没有它就不能说「没找到你的笔记」。
   *
   * ⚠ 缺省是 undefined 而不是 false：老数据没有这个键，
   *   语义上应读作「不知道」，由展示层决定要不要说这句话。
   */
  agent?: boolean
  /**
   * true = 这一轮的 `summary` **不是摘要，是原始转写的前 500 字节**。
   *
   * ⚠ §220：它与 `agent` 是**两个不同的降级维度，不能互相推断**。
   *
   *   · `agent === undefined` 且本键未置位
   *     ⇒ 一次性 chat 真的出了摘要，只是没有 references。
   *     ⇒ 说「已回落到一次性摘要」是**准确**的。
   *   · 本键为 true
   *     ⇒ 智能体路径**和**一次性路径都没拿到可用结果。
   *     ⇒ 此时说「已回落到一次性摘要」是**撒谎**：那个一次性的正是失败者。
   *
   *   两种状态此前**长得一模一样**（结构完整、HTTP 200、四数组全空），
   *   界面只能按 agent 猜 ⇒ 猜错时正好说出最不可信的那句话。
   *
   * 缺省是 undefined：老数据与「真摘要」共用 undefined，
   * 展示层只在显式为 true 时才多说话（§30 的不对称原则：宁可少说一句）。
   */
  summaryFallback?: boolean
  /**
   * 服务端本次送给 LLM 的转写规模（**runes**，不是字节）——
   * 只在 `summaryFallback === true` 时有意义。
   *
   * ⚠ §426：一次性回落链**不带转写截断**（另两条带：agent 尾 4000 /
   *   `/recommend` 尾 2000），所以它是四条链里唯一可能失控的输入。
   *   「多长算长」属待拍板 #17；这个字段只把**已经发生的规模**带出来，
   *   不参与任何分支判断。
   */
  inputRunes?: number
}

/** 一条参考资料：用户自己写过的一条笔记。 */
export interface SummaryReference {
  title: string
  noteId: string
  why: string
}

export interface RefineResult {
  refinedTranscript: string
  translations: Record<string, string>
  structuredMinutes: {
    agenda: string[]
    decisions: string[]
    actionItems: ActionItem[]
    nextMeeting: string | null
  }
  todos: ActionItem[]
  noteId?: string
  tasksCreated?: number
  /** true = 云端不可用，仅本地拼装转写，非真正精翻 */
  fromFallback?: boolean
  /**
   * true = 云端精校**成功**，但结果不可信，已由服务端 guardRefineResult
   * 整体丢弃并回落原文。
   *
   * ⚠ 与 fromFallback 同时为 true，但**原因完全不同**：前者是「云端解析不了」，
   *   后者是「云端解析好了，但结果不可信」。文案必须分开（桶名是对外承诺）。
   */
  rejected?: boolean
  /**
   * ⚠⚠ §908/D77：`rejected` 的**成因**。它现在有两个，而它们的用户侧动作不同：
   *   - `participant_leak`：混进了原文没有的参会人姓名（§35）
   *   - `shrinkage`：模型**缩写**了，回显不完（§907/D76，长会议恒触发）
   * 缺省按 `participant_leak` 处理（向后兼容）。
   */
  rejectedReason?: 'participant_leak' | 'shrinkage'
  /**
   * ⚠⚠ §910/D79：`assessRefineRetention` 判定**不通过**时的原因码。
   *
   * ⚠ 它此前只进了 `console.warn`（`refine-too-short(1234<2500，原文 5000)` 这种），
   *   而对用户的文案一律压成「云端返回无法解析」——
   *   **可 JSON 解析得好好的**，真实原因是「模型把精校做成了摘要」。
   *   ⇒ 与 §907/§908 同族：**桶名是对外承诺**，把已有的判定原因放出来不需要新机制。
   */
  retentionReason?: string
  /**
   * 服务端本次送给 LLM 的转写规模（**runes**，不是字节）。
   *
   * ⚠ §426：精校链**也不带转写截断**，而 `ErrOutputTruncated` 只认**输出**侧
   *   （`finish_reason=length`）⇒ 输入装不下时上游返 4xx、不匹配它 ⇒
   *   后端 502 ⇒ 前端把同一份超大请求体原样再发一次给 `/api/llm/chat` ⇒
   *   同样失败 ⇒ 用户拿回原始转写。
   *   ⇒ 此前「转写太长」与「模型没听话」在响应里**完全同形**（都只有
   *     `fromFallback`）。这个字段是第一种情况的唯一线索。
   * ⚠ 目前仍**不**参与分支判断（阈值属待拍板 #17）。
   */
  inputRunes?: number
}

export interface MeetingSyncPayload {
  id: string
  title?: string
  location?: string
  participants?: string[]
  startedAt: number
  durationMs?: number
  summary?: string
  refinedTranscript?: string
  noteId?: string
  status?: string
}

export const meetingsApi = {
  /** 增量滚动摘要 */
  async summarize(
    meetingId: string,
    segments: MeetingSegment[],
    prevSummary?: string,
    // ⚠ 2026-10-07（§159）**改口**：此前这里刻意只有 3 个字段，理由是
    //   「`buildSummaryPrompt` 不接 meta，给 summarize 加 topic 就是只声明不读」。
    //   那条理由**在「两条链用不同类型」的前提下成立**，而实际不是：
    //   Go 的 `meetingSummaryBody.Meta` 与 `meetingRefineBody.Meta`
    //   **共用同一个 `meetingMetaIn`**（server_meeting.go），它已经声明了 4 个字段。
    //   ⇒ 单一类型下「摘要链不声明 topic」根本表达不出来，
    //     前端 3 字段 / Go 4 字段的不一致由 `TestMeetingMetaFieldsMatchTheFrontend` 抓到。
    //
    // 所以这里补上 `topic?`，让**声明**与 Go 的**解码**一致（那道门守的正是这一层）。
    //
    // ★ 诚实记账：**摘要链目前仍不消费 topic**。
    //   `llmMeetingSummary(ctx, r, segs, prev)` 压根不收 meta，
    //   `buildSummaryPrompt(transcript, prev)` 也没有 meta 形参
    //   ⇒ topic 到达服务端后被丢掉，不影响摘要内容。
    //   这是**已知缺口**，不是「已支持」：真要让摘要用上主题，
    //   得把 meta 接进 `buildSummaryPrompt`（改提示词，需 A/B 复核），属另一件事。
    //   删掉这一个 `topic?:` 即可回退。
    meta?: { title?: string; participants?: string[]; location?: string; topic?: string },
    signal?: AbortSignal,
  ): Promise<SummaryResult> {
    try {
      const raw = await http<Record<string, unknown>>(`/api/meetings/${meetingId}/summary`, {
        method: 'POST',
        body: JSON.stringify({
          segments: toApiSegments(segments),
          prev_summary: prevSummary,
          meta,
        }),
        timeoutMs: MEETING_SUMMARY_TIMEOUT_MS,
        signal,
      })
      return normalizeSummary(raw)
    } catch (e) {
      // 中止不是「失败」：走到 fallback 会把用户主动取消，悄悄变成一份
      // 降级摘要写进会议里——那比不做还糟（用户以为取消了，库里却有内容）。
      if (isAbortError(e) || isModulePolicyLocalError(e) || e instanceof PocketResourceUnavailableError || e instanceof PocketResourceAuthorityError) throw e
      return fallbackSummarize(segments, prevSummary, signal)
    }
  },

  /** 智能推荐 */
  /**
   * 推荐结果 + **「这次没拿到」标志**。
   *
   * ⚠ §223：此前只回 `RecommendItem[]`，而「空数组」是一个**歧义输出**——
   *   它同时表示「模型认为没有值得建议的建议」（正当结果）与
   *   「这条链路压根没拿到」（LLM 报错 / 截断 / 解析失败）。
   *   服务端此前更糟：`err != nil` 时也是 200 + `{items: []}`，
   *   **一次失败在响应里与一次正当的空完全同形**。
   * ⇒ 调用方拿 fallback 才能在空的时候说一句实话；
   *   只有「空」没有这个标志时，说什么都不对（说了是噪音，不说是不诚实）。
   */
  async recommend(
    meetingId: string,
    segments: MeetingSegment[],
    summary?: string,
  ): Promise<{ items: RecommendItem[]; fallback: boolean }> {
    try {
      const res = await http<{ items?: RecommendItem[]; fallback?: boolean }>(
        `/api/meetings/${meetingId}/recommend`,
        {
          method: 'POST',
          body: JSON.stringify({ segments: toApiSegments(segments), summary }),
        },
      )
      return { items: res.items ?? [], fallback: res.fallback === true }
    } catch (error) {
      if (isModulePolicyLocalError(error) || error instanceof PocketResourceUnavailableError || error instanceof PocketResourceAuthorityError) throw error
      // ⚠ 网络/超时失败同样是「没拿到」，不是「没有建议」。
      //   归成空数组会让用户的「参考资料与建议」无声消失。
      return { items: [], fallback: true }
    }
  },

  /** 事后精翻 */
  async refine(
    meetingId: string,
    segments: MeetingSegment[],
    targetLangs: string[] = ['en'],
    // ⚠ §138.3：`topic` 此前不在这里，服务端 `meetingMetaIn` 也没有该字段
    //   ⇒ 主题里的专有名词永远到不了精校。两侧必须同时有，只改一侧等于白改。
    meta?: { title?: string; participants?: string[]; location?: string; topic?: string },
    signal?: AbortSignal,
    assertCurrent?: () => void,
  ): Promise<RefineResult> {
    try {
      const raw = await http<Record<string, unknown>>(`/api/meetings/${meetingId}/refine`, {
        method: 'POST',
        body: JSON.stringify({
          segments: toApiSegments(segments),
          target_langs: targetLangs,
          meta,
        }),
        timeoutMs: MEETING_REFINE_TIMEOUT_MS,
        signal,
        assertCurrent,
      })
      return normalizeRefine(raw, segments)
    } catch (e) {
      if (isAbortError(e) || isModulePolicyLocalError(e) || e instanceof PocketResourceUnavailableError || e instanceof PocketResourceAuthorityError) throw e
      // ★ meta 一起传下去（§54）：回落路径此前拿不到术语表，
      //   「林兰→林岚」「页百零一→CI」这类错误在这条高频路径上修不了。
      return fallbackRefine(segments, signal, meta, assertCurrent)
    }
  },

  /**
   * 云同步会议元数据。
   *
   * ⚠ §936：旧注释写「不含音频/转写全文」是**错的** —— 它**确实**把
   * `refinedTranscript` 放进了请求体（下一行）⇒ ⇒ 服务端每场会议存一份
   * 精校全文副本。两个调用点里只有 `syncMeetingMetadata` 不带全文。
   *
   * ⚠ 本轮**只把注释改成实话，不改行为**：「多端之间要不要同步精校全文」
   * 是**产品口径**（服务端存储形态 + 仓外移动端兼容）⇒ 登记待拍板
   * （简报第 160 条 ②），不在技术层自决。
   */
  async syncMeeting(payload: MeetingSyncPayload, assertCurrent?: () => void): Promise<void> {
    await http('/api/meetings', {
      method: 'POST',
      assertCurrent,
      body: JSON.stringify({
        id: payload.id,
        title: payload.title,
        location: payload.location,
        participants: payload.participants ?? [],
        startedAt: payload.startedAt,
        durationMs: payload.durationMs ?? 0,
        summary: payload.summary,
        refinedTranscript: payload.refinedTranscript,
        noteId: payload.noteId,
        status: payload.status ?? 'completed',
      }),
    })
  },
}

function toApiSegments(segments: MeetingSegment[]) {
  return segments.map((s) => ({
    speaker: s.speakerLabel ?? '说话人',
    text: s.text,
    lang: s.lang,
    start_ms: s.startMs,
    end_ms: s.endMs,
  }))
}

/**
 * 导出供测试直接断言（§109）：与 `normalizeRefine` 同一理由 ——
 * 「agent 降级信号有没有活过每一跳」是**纯数据变换**，
 * 源码扫描式判据量不到它（把 `raw.agent === true` 改成别的写法照样绿）。
 */
export function normalizeSummary(raw: Record<string, unknown>): SummaryResult {
  // ⚠ §426：`summary_input_runes` 是服务端记下的**本次 LLM 输入规模**
  //   （一次性回落链不带转写截断，是四条链里唯一可能失控的）。
  //   它目前**不参与任何分支** —— 「多长算长」属待拍板 #17。
  //   这里只是把服务端已经知道的量带到调用方，让阈值以后能从实测来定，
  //   而不是继续拿一份猜的数去定架构。
  const summaryInputRunes = raw.summary_input_runes ?? raw.summaryInputRunes
  return {
    // ⚠ §109：服务端只在 agent 分支里写 `agent`，回落路径**没有这个键**。
    //   必须原样透出，否则「参考资料为什么没了」这件事在最后一跳丢掉。
    ...(raw.agent === true ? { agent: true } : {}),
    // ⚠ §220：服务端的回落信号必须在这里透出，理由与 agent 那一行相同。
    //   服务端 emptySummary 的 summary 字段是**原始转写前 500 字节**，
    //   四数组全空却结构完整 ⇒ 丢了它，界面只能靠 agent 猜，
    //   而猜错时正好说出「已回落到一次性摘要」这句不可信的话。
    ...(raw.summary_fallback === true || raw.summaryFallback === true
      ? { summaryFallback: true }
      : {}),
    references: normalizeReferences(raw.references),
    ...(summaryInputRunes === undefined ? {} : { inputRunes: Number(summaryInputRunes) }),
    summary: String(raw.summary ?? ''),
    keyPoints: (raw.key_points ?? raw.keyPoints ?? []) as string[],
    actionItems: normalizeActionItems(raw.action_items ?? raw.actionItems),
    decisions: (raw.decisions ?? []) as string[],
    openQuestions: (raw.open_questions ?? raw.openQuestions ?? []) as string[],
  }
}

/**
 * 导出供测试直接断言（2026-10-06）。
 *
 * 之前只有源码扫描式门禁，把 `raw.refine_fallback === true` 改成
 * `false` 仍然绿 —— 因为同一函数里还留着 `raw.refineFallback`，
 * 文本照样匹配。**判据量的是「代码写在那」，不是「会执行」**。
 * 降级信号这种纯数据变换必须行为断言。
 */
export function normalizeRefine(raw: Record<string, unknown>, segments: MeetingSegment[]): RefineResult {
  const sm = (raw.structured_minutes ?? raw.structuredMinutes ?? {}) as Record<string, unknown>
  // 去重（2026-10-06）：切片重叠产生的重复会顺着降级路径进 prompt。
  const fallback = renderTranscript(segments)
  const serverRefined = raw.refined_transcript ?? raw.refinedTranscript
  // ⚠ 2026-10-06：服务端降级必须在这里透出来。
  //
  // 两种「看起来精校了、其实没有」的形态，之前都不可见：
  //   1. 服务端 parseRefineJSON 失败 ⇒ 回落成原始转写（带 refine_fallback）；
  //   2. 服务端压根没回 refined_transcript ⇒ 这里用本地 fallback 顶替。
  // 两者都返回 200 且结构完整，界面照常渲染 ⇒ 用户以为精校跑过了。
  // 降级本身是对的（宁可给原文也别给空），缺的是让调用方知道。
  // ⚠⚠ §831：提示词第 3 条写着「**不得删除原文有的内容**」，而此前**零强制** ——
  //   服务端只防「响应被截断」，客户端只防「空值」⇒ 中间那一档
  //   「响应完整、但模型把精校做成了摘要」无人看守 ⇒ 用户看到少了很多的正文，
  //   徽章却写「已精校」。判定放在纯模块里（§52：本文件在 node 里 import 不了）。
  const retention = assessRefineRetention({ original: fallback, refined: String(serverRefined ?? '') })
  // ⚠⚠ §910：把**原因**带出��去，不再只进 console.warn。
  //   「精校稿比原文短太多」与「云端返回无法解析」对用户是**两件事**，
  //   后者会让用户去查网络/云端，而真正的原因无人知晓。
  const retentionReason = retention.ok ? undefined : retention.reason
  if (!retention.ok) {
    console.warn(
      `[meetings] 精校结果未通过内容留存判定（${retention.reason}）：`
      + '退回原始转写并标记降级 —— 否则用户会看到一份少了大段内容、却标着「已精校」的正文。',
    )
  }
  const fromFallback =
    raw.refine_fallback === true ||
    raw.refineFallback === true ||
    serverRefined === undefined ||
    serverRefined === null ||
    // §831：留存判定不通过**也是降级**，且必须让下游知道
    // （`refineStatusFor` 据此置 refine-failed，`resolveRefinedView` 据此展示原文 + 徽章）。
    !retention.ok
  // ⚠ 2026-10-07：与 fromFallback 分开透出，别合并。
  //
  // 服务端「拒绝」的结果同样带 refine_fallback=true（它确实回落到原文了），
  // 若只透出 fromFallback，调用方只能说「云端返回无法解析」——
  // 而实际上 JSON 解析得好好的，是名单污染被拦下了。
  // 用户会照着错误原因去查网络，而真正的原因无人知晓。
  const rejected = raw.refine_rejected === true || raw.refineRejected === true
  // ⚠⚠ §908/D77：`refine_rejected` 现在有**两个**成因，必须把原因也带出来。
  //   §907 给长会议加了「缩写」护栏之后，前端若只知道「被拦下」，
  //   就会对每一场长会议说「检测到与参会人名单不符的人名」——
  //   而名单从来没问题，真实原因是「转写太长，模型回显不完」。
  //   ⇒ 桶名是对外承诺（`refine-outcome-notice.ts` 头注释写着这条纪律）。
  const rejectedReason = (raw.refine_rejected_reason ?? raw.refineRejectedReason) as
    | 'participant_leak'
    | 'shrinkage'
    | undefined
  // ⚠ §426：与 summaryInputRunes 同理——服务端记下的**本次 LLM 输入规模**。
  //   ⚠ 它的用处正是把两种现在**完全同形**的回落区分开：
  //     「转写太长、请求被上游拒了」与「模型没按契约返回」——
  //     两者此前在响应里只有同一个 refine_fallback=true。
  //   ⚠ 目前仍**不**参与分支判断（阈值属待拍板 #17）；只把事实带出来。
  const refineInputRunes = raw.refine_input_runes ?? raw.refineInputRunes
  return {
    // §831：留存判定不通过 ⇒ 展示原文，而不是那份被砍掉一半的「精校」
    refinedTranscript: retention.ok ? String(serverRefined ?? fallback) : fallback,
    translations: (raw.translations ?? {}) as Record<string, string>,
    structuredMinutes: {
      agenda: (sm.agenda ?? []) as string[],
      decisions: (sm.decisions ?? []) as string[],
      actionItems: normalizeActionItems(sm.action_items ?? sm.actionItems),
      nextMeeting: (sm.next_meeting ?? sm.nextMeeting ?? null) as string | null,
    },
    todos: normalizeActionItems(raw.todos),
    noteId: (raw.note_id ?? raw.noteId) as string | undefined,
    tasksCreated: Number(raw.tasks_created ?? raw.tasksCreated ?? 0) || undefined,
    ...(fromFallback ? { fromFallback: true } : {}),
    ...(rejected ? { rejected: true, ...(rejectedReason ? { rejectedReason } : {}) } : {}),
    ...(retentionReason ? { retentionReason } : {}),
    ...(refineInputRunes === undefined ? {} : { inputRunes: Number(refineInputRunes) }),
  }
}

/**
 * 解析 agent 返回的 references。
 *
 * 逐条过滤而不是整段丢弃：模型偶尔会给一条缺 noteId 的引用
 * （那一条点不开），但其余引用是有效的 —— 让它们一起消失不合理。
 *
 * ★ 没有 URL 兜底是有意的：noteId 为空的引用如果照渲染成一个按钮，
 * 点击后会走到 /notes/undefined。所以宁可整条不显示。
 */
function normalizeReferences(raw: unknown): SummaryReference[] {
  if (!Array.isArray(raw)) return []
  const out: SummaryReference[] = []
  for (const r of raw) {
    if (!r || typeof r !== 'object') continue
    const o = r as Record<string, unknown>
    const noteId = String(o.note_id ?? o.noteId ?? '').trim()
    if (!noteId) continue
    out.push({
      title: String(o.title ?? '（无标题笔记）'),
      noteId,
      why: String(o.why ?? ''),
    })
  }
  return out
}

function normalizeActionItems(raw: unknown): ActionItem[] {
  if (!Array.isArray(raw)) return []
  return raw.map((a) =>
    typeof a === 'string' ? { text: a } : a as ActionItem,
  )
}

/**
 * §916：`/api/llm/chat` 的**单条消息**长度硬上限。
 *
 * ⚠⚠ **单位是 UTF-8 字节，不是字符** —— 这是本节最要紧的一条。
 *
 *   出处（服务端两处同值）：
 *   `backend/internal/server/server_assistant.go` 的 `handleLLMChat`
 *     —— `if len(m.Content) > 32000 { 400 "message too long (max 32000 bytes per message)" }`
 *   `backend/internal/server/server_llmbff.go` 的 `/api/llm/stream` 入口（同值）
 *   ⚠ **不写行号**：行号会随代码增长漂（本处就曾漂过 7 行）⇒ 按**符号**指。
 *
 *   Go 的 `len(string)` 返回的是**字节数**
 *   ⇒ 32000 字节 ≈ **10666 个中文字**（这是**闸的语义**：按 3 字节/字）。
 *   ⚠⚠ §920 **更正**了「分钟」那一半：换算成会议时长**不能**用「10666 字 ÷ 271 字/分」，
 *     因为真实提示词里还有**说话人前缀**（`[说话人] ` 每行约 16 字节）且真实转写密度是
 *     **2.93 字节/字**（不是 3）⇒ 真实斜率 = **844 字节/分**（真实 ASR 语料实测）
 *     ⇒ 摘要兜底（模板开销 522 字节）的越闸点 ≈ **37.1 分钟**
 *     ⇒ 精校兜底（模板开销 3172 字节）≈ **33.9 分钟**
 *     ⇒ 流式纪要（§919 修好后）**永不超闸**（被裁进闸内）。
 *   ⚠⚠ 这比先前按「字符」估的「≈2 小时」**短 3 倍** ——
 *     同样地，`/api/embed` 的 16000 闸（`server_assistant.go` 的 `handleEmbed`：
 *     `len(body.Text) > 16000`，也是字节）
 *     只装得下 **5333 个中文字** ≈ **19.7 分钟**，
 *     而 §896/§912 当时按「16000 字」算出来的是「63 分钟」⇒ **那两个数字要更正**。
 *   ⚠ 错误信息原先写的是「chars」⇒ **信息与判定不同形**（这正是本节那个坑的成因）
 *     ⇒ 已按实测把两处文案改成 bytes。
 *
 * ⚠ 为什么前端必须知道这个上限（§916 的缺陷本体）：
 *   兜底路径是把**整场转写**拼进提示词再打 `/api/llm/chat` ⇒
 *   转写一旦超过闸（≳40 分钟的会议）⇒ 请求**必然 400** ⇒
 *   兜底**结构性不可达** ⇒ 摘要退化成「转写前 200 字 + summaryFallback 标志」，
 *   而那**不是摘要**（§220 已把它的形状与信号钉住）。
 *
 * ⚠ 与 `EMBED_TEXT_CHAR_LIMIT`（§896）同形：这里也是**前端镜像**服务端的一个数。
 *   两份常量要收敛成一处契约的问题**未拍板**（简报 119 的延伸）。
 *   ⚠ 名字里带 `BYTES` 是刻意的：这个名字就是防「按字符算」的那道护栏。
 */
const LLM_CHAT_MAX_CONTENT_BYTES = 32000

/**
 * §916：把摘要兜底的提示词装进 `/api/llm/chat` 的长度闸。
 *
 * ── 裁剪顺序（有讲究，不是随手挑的）──
 *   ① 先裁**转写**，且取**尾部**：
 *      滚动摘要的提示词结构是「已有摘要 + 新增转写」⇒ **新的内容在尾部**
 *      （会议还没讲完），更早的内容由 `prevSummary` 携带
 *      ⇒ 若取头部，兜底会每次都在总结会议开头，**永远看不到新说的话**。
 *   ② 转写裁光还超 ⇒ 只可能是 `prevSummary` 本身太大
 *      （§101 明确**不设** `max_tokens` ⇒ 模型的 summary 字段长度不受控）
 *      ⇒ 再裁它的**头部**（保留最近一次改写的结果）。
 *
 * ── 为什么这个扣减是**精确**的 ──
 *   读 `buildFallbackSummaryPrompt` 的模板可知：
 *     · **转写是纯后缀**（`…新增转写：\n${transcript}`）
 *     · **`prevSummary` 原样内嵌**（`已有摘要：\n${prevSummary}`）
 *   ⇒ `build(t, p)` 的字节数 == 模板开销 + t 的字节数 + p 的字节数
 *   ⇒ 「扣掉开销再裁」**不会**出现「裁完还超」或「裁过头」。
 *
 * ⚠ 不改**单位口径**：这里量的是**服务端的字节闸**，与 §245.7 那个
 *   （尚未拍板的）「字节/码元/字符」产品口径**无关** —— 本函数不碰
 *   `truncateCodeUnits`（200/500 那两处回落截断的单位仍未拍板，§244.4）。
 */
function fitSummaryPromptToGate(
  transcript: string,
  prevSummary?: string,
): { transcript: string; prevSummary?: string } {
  const build = buildFallbackSummaryPrompt
  const gate = LLM_CHAT_MAX_CONTENT_BYTES

  let t = transcript
  let p = prevSummary

  // ① 裁转写（取尾部）。开销 = 模板 + prevSummary（转写按纯后缀扣）。
  if (utf8ByteLength(build(t, p)) > gate) {
    const overhead = utf8ByteLength(build('', p))
    t = truncateUtf8BytesTail(t, Math.max(0, gate - overhead))
  }

  // ② 仍超 ⇒ 只能是「已有摘要」自己就超了（模板开销 ≈ 600 字节，不可能是它）。
  if (utf8ByteLength(build(t, p)) > gate) {
    // prevSummary 在模板里**原样内嵌** ⇒ 用一个 1 字节的探针量出模板开销。
    const overhead = utf8ByteLength(build('', 'X')) - 1
    p = truncateUtf8BytesTail(p ?? '', Math.max(0, gate - overhead))
  }

  return { transcript: t, prevSummary: p }
}

/** LLM 兜底：直接调 /api/llm/chat */
async function fallbackSummarize(
  segments: MeetingSegment[],
  prevSummary?: string,
  signal?: AbortSignal,
): Promise<SummaryResult> {
  assertCloudModule('meetings')
  const transcript = renderTranscript(segments)

  // ⚠ 2026-10-07（§66.4）：提示词本体移到 features/meetings/summary-prompt.ts。
  //   原来这里是**一段内联模板字符串**，只有裸 schema、零条要求 ——
  //   与 §54 在精校链上发现并已修复的缺陷同型，而 action_items[].due
  //   是摘要链里**时间点的唯一来源**（schema 里没有 next_meeting）。
  //   与服务端 `buildSummaryPrompt` 的防漂由 summary-prompt-parity.test.mjs 承担。
  // ⚠⚠ §916：转写可能超过 `/api/llm/chat` 的 32000 **字节**硬闸
  //   （实测 32000 字节 ≈ 10666 个中文字；按真实提示词斜率 844 字节/分
  //   ⇒ 越闸点 ≈ **37.1 分钟**，§920 实测 ⇒ 原写的「39 分钟」偏高）
  //   ⇒ 不裁就是**必然 400**，兜底**结构性不可达** ⇒ 摘要退化成「转写前 200 字」。
  //   ⇒ 这里按闸裁（取**尾部**：新的内容在尾部，更早的由 prevSummary 携带）。
  //   ⚠ 低于闸时**逐字不变**（`fitSummaryPromptToGate` 原样返回）
  //     ⇒ 短会议行为零变化。
  //   ⚠ `transcript`（全量）仍留给下面的 `parseSummaryJson` ——
  //     那一步要的是「转写原文」来兜底，不是「送进提示词的那一段」。
  const fitted = fitSummaryPromptToGate(transcript, prevSummary)
  const prompt = buildFallbackSummaryPrompt(fitted.transcript, fitted.prevSummary)

  try {
    const res = await http<{ content: string }>('/api/llm/chat', {
      syncModule: 'meetings',
      method: 'POST',
      body: JSON.stringify({
        kind: 'meeting_summary',
        messages: [{ role: 'user', content: prompt }],
      }),
      signal,
      // ⚠ §929：这条兜底此前吃 `http()` 的默认 **30 s**，而服务端那条路的
      //   有效预算是 **85 s**（网关客户端的 ResponseHeaderTimeout）
      //   ⇒ 客户端比服务端先放弃 55 s ⇒ 兜底路径在生产上基本必然超时。
      timeoutMs: LLM_CHAT_TIMEOUT_MS,
    })
    return parseSummaryJson(res.content, transcript)
  } catch (e) {
    // 降级链自己也可能撞上中止（用户在兜底重试期间点了取消）。这里如果照旧
    // 吞掉，就会把一次取消变成「转写前 200 字的摘要」——用户在界面上点了取消，
    // 会议里却多了一段内容。宁可让中止继续往外抛。
    if (isAbortError(e) || isModulePolicyLocalError(e) || e instanceof PocketResourceUnavailableError || e instanceof PocketResourceAuthorityError) throw e
    return {
      summary: truncateCodeUnits(transcript, 200) || '暂无摘要',
      keyPoints: [],
      actionItems: [],
      decisions: [],
      openQuestions: [],
      // ⚠ §220：这条比 emptySummary 更靠后 —— 连降级链自己都失败了。
      //   此时若界面按「没有 agent」解释，会说出「已回落到一次性摘要」，
      //   而那个一次性的正是失败者 ⇒ 必须显式声明「这段根本不是摘要」。
      summaryFallback: true,
    }
  }
}

/**
 * 后端精校失败时的回落：直接打 `/api/llm/chat`。
 *
 * ⚠ 触发面不是边角情况（§54 发现 C）：本仓 llmgateway 的
 *   `ResponseHeaderTimeout` 实测 60s，而网关 `auto` 路由到
 *   deepseek-v4-flash 时首字节 24~142s（§44）⇒ 后端先超时返 502
 *   ⇒ 前端必走这里。这条路径的产出质量直接决定用户看到什么。
 *
 * ★ `meta` 必须一起传下去（§54）：回落路径此前既没有术语表（主题/参会人），
 *   也没要求模型产出 action_items/todos ⇒ 「时间点自动进日程」在这条
 *   **高频**路径上没有任何输入。提示词本体见 features/meetings/refine-prompt.ts，
 *   契约与服务端那份共用一份清单（REFINE_PROMPT_CONTRACT），防再漂。
 */
async function fallbackRefine(
  segments: MeetingSegment[],
  signal?: AbortSignal,
  meta?: { title?: string; participants?: string[]; location?: string },
  assertCurrent?: () => void,
): Promise<RefineResult> {
  assertCloudModule('meetings')
  const transcript = renderTranscript(segments)
  const prompt = buildFallbackRefinePrompt(transcript, meta)

  // ⚠⚠ §920：这条兜底的提示词**没有上限** ⇒ 超过 `LLM_CHAT_MAX_CONTENT_BYTES`
  //   **字节**时，服务端那道闸（`len(m.Content) > 32000`）会**必然 400**
  //   ⇒ 走不到下面的 catch ⇒ 用户拿到的是**未精校的原文**。
  //
  //   ── 实测读数（真实 ASR 语料，斜率 844 字节/分，含说话人前缀）──
  //   · 越闸点 ≈ **33.9 分钟**（精校模板开销 3172 字节，比摘要那条的 522 大得多
  //     ⇒ **比摘要兜底早 3.2 分钟**撞闸）
  //   · ⚠ 这条路是**高频**兜底（§54：后端 60s 超时 ⇒ 前端必走这里）
  //   · ⚠⚠ 而转写是**用户不能编辑的派生输入** ⇒ 用户**绕不开**
  //     （§918 定的判别标准：不可编辑的派生输入撞闸 ⇒ 必须修）
  //   · ⚠⚠ **不对称**：同一份转写走后端那条路（`/api/meetings/{id}/refine`）
  //     **没有**这道 32000 字节闸（实测 62.5 分钟的会议送 70301 字节照样发得出去）
  //     ⇒ 「后端精校失败时，长会议的用户一定拿不到精校」
  //
  //   ── 这里**只出声、不改降级行为**（与 §818/§819/§820 同款纪律）──
  //   修法（裁尾 / 分段精校 / 别的）是**产品口径**，已挂在简报 140 第 ② 问
  //   ⇒ 本节不代拍 ⇒ 只把「它必然 400」从静默变成可见。
  const promptBytes = utf8ByteLength(prompt)
  if (promptBytes > LLM_CHAT_MAX_CONTENT_BYTES) {
    console.warn(
      `[refine-fallback] 兜底提示词 ${promptBytes} 字节 > 闸 ${LLM_CHAT_MAX_CONTENT_BYTES} 字节` +
      ` ⇒ 这条请求**必然 400**（转写约 ${(promptBytes / 844).toFixed(1)} 分钟；` +
      `实测越闸点 33.9 分钟）⇒ 精校会回落成原文。修法待拍板（简报 140 第 ② 问）。`,
    )
  }

  try {
    const res = await http<{ content: string }>('/api/llm/chat', {
      syncModule: 'meetings',
      assertCurrent,
      method: 'POST',
      body: JSON.stringify({
        kind: 'meeting_refine',
        messages: [{
          role: 'user',
          content: prompt,
        }],
      }),
      signal,
      // ⚠ §929：同上 —— 这条兜底此前吃 30 s 默认，而服务端有效预算 85 s。
      //   它是**高频**兜底（§54：后端先超时 ⇒ 前端必走这里）⇒ 30 s 意味着
      //   「后端 502 之后，兜底也必然超时」⇒ 用户拿回原文 + 「未生效」。
      timeoutMs: LLM_CHAT_TIMEOUT_MS,
    })
    return refineFromLlmContent(res.content, transcript)
  } catch (e) {
    // 同 fallbackSummarize：中止必须继续往外抛，不能落到下面那份
    // fromFallback 的拼装结果上。
    if (isAbortError(e) || isModulePolicyLocalError(e) || e instanceof PocketResourceUnavailableError || e instanceof PocketResourceAuthorityError) throw e
    return refineFromLlmContent('', transcript)
  }
}

/**
 * 兜底链的「LLM 正文 → RefineResult」（纯函数，两条分支共用）。
 *
 * ⚠ 为什么抽出来：**空值闸必须对两条分支同时成立**，
 * 而原来的写法只在成功分支用 `parsed.refined_transcript ?? transcript`。
 *
 * `??` 只挡 `null` / `undefined`，**挡不住 `""`** ——
 * 而 `{"refined_transcript":"", …}` 是真网关实测过的形态
 * （服务端 `parseRefineJSON` 的注释里就记着「真网关实测滚动摘要时就见过」，
 *   并且为它专门加了一道空值闸）。
 * ⇒ 那时 `refinedTranscript` 会是**空串**：用户看到「精校结果一片空白」，
 * 而这条路径存在的理由恰恰是 `normalizeRefine` 注释里那句「宁可给原文也别给空」。
 *
 * ⚠ 服务端口径是 `strings.TrimSpace(s) == ""` 才算空 ⇒ 这里照抄（含全空白）。
 * ⚠ 抽成纯函数还有第二个好处：它**可被行为断言**。
 * 原来两条分支各自拼装，判据只能源码扫描（`summary-fallback-guard.test.ts` 那种），
 * 那证明的是「代码写在那」，不是「它真的会这么判」。
 */
export function refineFromLlmContent(content: string, transcript: string): RefineResult {
  let parsed: Record<string, any> = {}
  try {
    parsed = JSON.parse(extractJson(content)) ?? {}
  } catch {
    parsed = {} // 解析失败与「空响应」在这里是同一件事：拿不到精校结果
  }
  const sm = (parsed.structured_minutes ?? parsed.structuredMinutes ?? {}) as Record<string, unknown>
  const raw = parsed.refined_transcript
  const hasRefined = typeof raw === 'string' && raw.trim() !== ''
  // ⚠⚠ §831：**两个**调用点都必须过这道判定。
  //   这里（浏览器内 LLM 兜底路径）与上面 `normalizeRefine`（服务端路径）是两条独立的
  //   产出 refined_transcript 的路 ⇒ 只改一处，另一处照样能把摘要当精校展示。
  //   本函数**恒** `fromFallback: true`，所以文案与状态已经对；此处只管正文用哪一份。
  const retention = assessRefineRetention({ original: transcript, refined: hasRefined ? (raw as string) : '' })
  return {
    // ⚠ §910：这条是**浏览器内 LLM 兜底路径**，原因同样带出去（两条路都得给同一个真相）
    ...(retention.ok ? {} : { retentionReason: retention.reason }),
    refinedTranscript: hasRefined && retention.ok ? (raw as string) : transcript,
    translations: (parsed.translations ?? {}) as Record<string, string>,
    structuredMinutes: {
      agenda: (sm.agenda ?? []) as string[],
      decisions: (sm.decisions ?? []) as string[],
      actionItems: normalizeActionItems(sm.action_items ?? sm.actionItems),
      nextMeeting: (sm.next_meeting ?? sm.nextMeeting ?? null) as string | null,
    },
    todos: normalizeActionItems(parsed.todos),
    fromFallback: true,
  }
}

/**
 * 解析云端返回的摘要 JSON。
 *
 * fallbackText 是这条链上**已经算好的**转写文本（不是重新拼的），
 * 用来在「模型没给出可用摘要」时兜底。
 *
 * ⚠ 2026-10-06 补的空摘要兜底，与服务端 parseSummaryJSON 同一类缺陷：
 * 模型完全可能返回**合法却空**的 JSON
 * （{"summary":"","key_points":[], …}）。此时 JSON.parse 不抛错、
 * catch 分支进不去，`?? ''` 直接给用户一个空摘要面板 —— 而这是
 * 「云端不可用时才会走」的兜底路径，出错时用户连报错都看不到。
 *
 * 「合法但空」与「非法」对用户是同一件事：没拿到摘要。
 * 所以两者归到同一处回落：宁可把转写本身显示出来（用户认得出
 * 那是自己说过的话），也不要假装成功。
 */
function parseSummaryJson(content: string, fallbackText = ''): SummaryResult {
  try {
    const parsed = JSON.parse(extractJson(content))
    const raw = parsed.summary ?? parsed.tldr ?? ''
    if (!String(raw).trim()) {
      return emptySummary(fallbackText || truncateCodeUnits(String(content), 500))
    }
    return {
      summary: String(raw),
      keyPoints: parsed.key_points ?? parsed.keyPoints ?? parsed.topics ?? [],
      actionItems: (parsed.action_items ?? parsed.actionItems ?? []).map((a: ActionItem | string) =>
        typeof a === 'string' ? { text: a } : a,
      ),
      decisions: parsed.decisions ?? [],
      openQuestions: parsed.open_questions ?? parsed.openQuestions ?? [],
    }
  } catch {
    return emptySummary(fallbackText || truncateCodeUnits(String(content), 500))
  }
}

/** 摘要不可用时的统一回落形状。各字段必须是数组，否则调用方会拿到 undefined。 */
export function emptySummary(text: string): SummaryResult {
  // ⚠ summaryFallback 必须**由这里**置位，不能只靠调用点记得加 ——
  // 本函数的全部调用点都是「没拿到摘要」，而它的产物结构完整、
  // 会被当成功渲染（§220）。
  return {
    summary: truncateCodeUnits(text, 500),
    keyPoints: [],
    actionItems: [],
    decisions: [],
    openQuestions: [],
    summaryFallback: true,
  }
}

function extractJson(text: string): string {
  const match = text.match(/\{[\s\S]*\}/)
  return match ? match[0] : text
}

export function toLiveSummary(result: SummaryResult): LiveSummary {
  return {
    summary: result.summary,
    // ⚠ 必须带进 live_summary —— 它是 JSON blob 列，不需要迁移，
    //   但**不带**的话重新加载后信号就丢了，界面会开始说假话。
    agent: result.agent,
    // ⚠ 与 agent 同理：不带进 blob，重新加载后信号就丢了，界面会开始说假话。
    summaryFallback: result.summaryFallback,
    references: result.references,
    keyPoints: result.keyPoints,
    actionItems: result.actionItems,
    decisions: result.decisions,
    openQuestions: result.openQuestions,
    updatedAt: Date.now(),
  }
}
