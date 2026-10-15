import { truncateCodeUnits } from '../../utils/text.ts'

// refined-ingest-writeback.ts — 精校收尾时**往会议记录里回写什么**。
//
// ── 这里修的是哪一个缺陷（§108）─────────────────────────────────────
//
// `ingestMeetingArtifacts` 原本无条件写：
//
//   updateMeeting(id, {
//     refinedTranscript: refine.refinedTranscript,
//     summary: refine.refinedTranscript.slice(0, 500) || meeting.summary,
//     status: refineStatus,
//   })
//
// 而**降级时** `refine.refinedTranscript` 装的是**原文**（`normalizeRefine`：
// `refinedTranscript: String(serverRefined ?? fallback)`，
// `fallback = renderTranscript(segments)`）。
//
// ⇒ `slice(0, 500)` 对一段非空原文必然非空 ⇒ `|| meeting.summary`
//   **永远不生效** ⇒ 滚动摘要链已经写好的摘要被**原文覆盖**。
//
//   后果：会议详情页的「总结」变成刚说过的 500 字原话，
//   并且 `syncMeeting` 还把它同步到云端。
//
//   ⚠ 与 §107 同族：**降级产物躺在「成功」字段里**。
//     §107 是「不展示」，这一条是「展示出来，还顶掉了更好的那份」。
//
// ★ 为什么成功路径的行为**不动**：
//   「成功时用精校全文的前 500 字当摘要」本身是个产品取舍
//   （它是预览，不是摘要）。改它属于替用户拍板。
//   本模块只修**降级**这一种形态 —— 那不是取舍，那是缺陷。

/** 精校回写的判定输入 */
export interface IngestWritebackInput {
  /** 会议当前已有的摘要（可能来自滚动摘要链，是好的那一版） */
  existingSummary: string | null
  /** 本次精校的结果（只取需要的字段） */
  refine: {
    refinedTranscript: string
  }
  /**
   * 状态由**调用方**用 `refineStatusFor` 判好后传进来。
   *
   * ⚠ 为什么不在本函数里调：`refine-rejected-notice.test.ts` 那道门
   *   守的是「三个落点都按判定取值，不得写死 refined」，
   *   而 `meeting-ingest.ts` 是其中一个落点。
   *   把判定放进 helper 会让那条门看不见「落点按判定取值」这件事 ——
   *   第一版正是这么写的，门当场报红（报得对）。
   *   ⇒ 判定在落点，纯函数只负责「给定状态，写哪几个字段」。
   */
  status: 'refined' | 'refine-failed'
}

export interface IngestWriteback {
  /** 精校全文：始终回写（§107 之后它是要展示的） */
  refinedTranscript: string
  /** 摘要：**null 表示这一轮不要动摘要** */
  summary: string | null
  status: 'refined' | 'refine-failed'
}

/**
 * resolveIngestWriteback 判定精校收尾时该往会议记录里写哪几个字段。
 *
 * @param input 已有摘要与本次精校结果
 * @returns refinedTranscript 始终有值；summary 为 null 时调用方必须**跳过该字段**
 */
export function resolveIngestWriteback(input: IngestWritebackInput): IngestWriteback {
  const { refine, status } = input
  const refinedTranscript = refine.refinedTranscript

  // ★ 状态**只能**取自调用方给的判定值，这里绝不自己再判一次 ——
  //   重写一遍就等于把「三个落点都按判定取值」这条禁令从后门绕过去，
  //   而且两处会各自漂移（第一版就是这么写的，当场被门抓出来）。
  if (status === 'refine-failed') {
    // ★ 降级：refinedTranscript 里装的是原文。
    //   拿它 slice(0,500) 当摘要 = 把刚说过的原话盖到滚动摘要上。
    //   ⇒ 这一轮**一个字都不改摘要**。
    return { refinedTranscript, summary: null, status }
  }

  return {
    refinedTranscript,
    // ⚠ §251.2 兄弟链：这一行原来也是裸 `slice(0, 500)`。
    //   它截的是**精校后的转写正文**（最容易含 emoji 的一侧），
    //   而产物经 `updateMeeting` **真的入库**（本地 DB + 云同步）——
    //   切在代理对中间会留下半个字符，过 Go 解码变 `U+FFFD` 且**不报错**。
    //   与 `meetings.ts` 的 `truncateCodeUnits` 同形，故复用它。
    summary: truncateCodeUnits(refinedTranscript, 500) || input.existingSummary,
    status,
  }
}