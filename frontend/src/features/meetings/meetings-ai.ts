/**
 * meetings-ai.ts — S2.2 会议纪要 AI 编排。
 *
 * 会议正文只通过 S0-B LLM BFF 处理，不落服务端；纪要结果写回本地
 * local_meetings.summary。v1 采用非流式 UI 收集流式 delta。
 */
import { llmBffApi, type ChatStreamDelta } from '../../api/llm-bff'
import { truncateUtf8BytesHead, truncateUtf8BytesTail, utf8ByteLength } from '../../utils/text'

/**
 * `/api/llm/stream` 的**单条消息**上限（**UTF-8 字节**）。
 *
 * ⚠ 出处：`backend/internal/server/server_llmbff.go:127`
 *   `if len(m.Content) > 32000 { 400 "message too long (max 32000 bytes)" }`
 *   —— Go 的 `len(string)` 是**字节数**（实测 `len("中") == 3`）。
 * ⚠ 与 `/api/llm/chat` 那道闸**同值**（`server_assistant.go:2955`），
 *   但那是**另一个 handler** ⇒ 改了那边不会自动改这边 ⇒ 两道门一起钉（Go 侧 4 路对齐）。
 * ⚠ 服务端是**逐条**判的（`for _, m := range body.Messages { ... }`）
 *   ⇒ 每条 message **各自**都要 ≤ 闸值，**不是**几条加起来。
 */
const LLM_STREAM_MAX_CONTENT_BYTES = 32000

/**
 * 转写被裁时追加的说明。
 *
 * ⚠ 为什么必须有它：裁掉的是**开头**（见 `fitToStreamGate`）⇒ 不说明的话，
 *   模型会把「尾部片段」当成整场会议 ⇒ 会写出「会议讨论了 X」而 X 根本没在片段里
 *   ⇒ 这正是「不要编造转写中不存在的信息」要防的事。
 */
const TRANSCRIPT_TRUNCATED_NOTICE =
  '\n\n⚠ 转写过长，以下仅为会议转写的**尾部**片段（前面的内容因长度上限被省略）。' +
  '请据此生成纪要，不要臆造片段中不存在的信息。'

/**
 * §919：把「系统提示 + 转写」装进 `/api/llm/stream` 的**逐条**字节闸。
 *
 * ── 这个缺陷是怎么来的（与 §916/§917/§918 同一族）──
 *   原实现是 `transcript.slice(0, 60000)` —— 那是 **60000 个码元**，
 *   而服务端的闸是 **32000 个字节** ⇒ 中文转写里 60000 码元 = 180000 字节
 *   ⇒ **超出闸 5.6 倍** ⇒ 只要转写超过 10666 个中文字（**按真实提示词斜率
 *   844 字节/分**（真实 ASR 语料实测，含 `[说话人] ` 前缀）⇒ 越闸点
 *   ≈ **37.9 分钟**的会议；⚠ §920 更正了原写的「39.4 分钟」——那个数是按
 *   「纯转写 3 字节/字」算的，**漏了说话人前缀**与真实密度 2.93），
 *   这条请求就**必然 400** ⇒
 *   「生成纪要」按钮对长会议**完全不可用**（只弹一个 toast）。
 *   ⇒ 而 §916 修好的 `/api/llm/chat` 那条路对长会议是好的
 *   ⇒ **两条会议纪要路径的能力不一致**（一条能用、一条必然失败）。
 *
 * ── 为什么裁**尾部**（不是头部）──
 *   会议结论与行动项（§66.4：`action_items[].due` 是摘要链里**时间点的唯一来源**）
 *   通常出现在讨论到该事项时，且越靠后越具体 ⇒ 取尾部更能拿到「决定了什么、谁做什么」。
 *   ⚠ 这是**产品口径**，不是技术必然：另一种合理选择是「头尾都取、中间省略」
 *     （保住开头的议题 + 结尾的行动项）⇒ 已登记待属主拍板（简报 143）。
 *   ⇒ 在拍板前取尾部是**保守默认**：它与 §916 滚动摘要的取尾一致。
 *
 * ── 裁剪顺序（有讲究）──
 *   ① 先裁**转写**（取尾部），预算 = 闸 − system 的字节 − 说明的字节
 *      ⇒ 因为服务端**逐条**判，两条各自都要过闸。
 *   ② 仍超 ⇒ 只可能是 **system 自己**太大（用户自定义的 summary skill）
 *      ⇒ 裁它的**头部**（「要输出什么格式」在前面，裁尾会毁掉指令）。
 *      ⚠ skillPrompt 是**用户可编辑**的输入（他自己能改短），
 *        这里裁只是不让它把整条请求带崩，不是在替用户做决定。
 */
export function fitToStreamGate(
  system: string,
  transcript: string,
): { system: string; transcript: string } {
  const gate = LLM_STREAM_MAX_CONTENT_BYTES
  let sys = system
  let t = transcript

  // ① 裁转写（取尾部）。system 原样保留 ⇒ 先量它的字节。
  const sysBytes = utf8ByteLength(sys)
  if (utf8ByteLength(t) + sysBytes > gate) {
    t = truncateUtf8BytesTail(t, Math.max(0, gate - sysBytes - utf8ByteLength(TRANSCRIPT_TRUNCATED_NOTICE)))
    sys = sys + TRANSCRIPT_TRUNCATED_NOTICE
  }

  // ② 仍超 ⇒ system 自己就超了 ⇒ 裁它的**头部**（保格式指令），并保住说明。
  const tBytes = utf8ByteLength(t)
  if (tBytes + utf8ByteLength(sys) > gate) {
    const sysNoNotice = sys.endsWith(TRANSCRIPT_TRUNCATED_NOTICE)
      ? sys.slice(0, sys.length - TRANSCRIPT_TRUNCATED_NOTICE.length)
      : sys
    const budget = Math.max(0, gate - tBytes - utf8ByteLength(TRANSCRIPT_TRUNCATED_NOTICE))
    sys = truncateUtf8BytesHead(sysNoNotice, budget) + TRANSCRIPT_TRUNCATED_NOTICE
  }

  return { system: sys, transcript: t }
}

export async function summarizeMeeting(
  transcript: string,
  handlers: { onRetry?: (model: string) => void; skillPrompt?: string } = {},
): Promise<string> {
  if (!transcript.trim()) throw new Error('会议转写为空，无法生成纪要')

  const chunks: string[] = []
  // 非流式 UI 没有气泡灰字渲染位：onRetry 只记录回退轨迹，正文到达即静默，
  // 与 ai-chat 的 retry 提示语义对齐（runbook §16.6-2：全调用方接入 retry 帧）。
  const retriedModels = new Set<string>()
  const rawSystem = handlers.skillPrompt?.trim() ||
    '你是会议记录助手。请用中文输出结构化会议纪要，包含：会议摘要、关键决策、行动项（负责人/截止时间若能识别）、待确认问题。不要编造转写中不存在的信息。'
  // ⚠ §919：两条 message **各自**都要过 32000 **字节**的闸（服务端逐条判）。
  const fitted = fitToStreamGate(rawSystem, transcript)
  await new Promise<void>((resolve, reject) => {
    llmBffApi.streamChat(
      {
        kind: 'meeting_summary',
        syncModule: 'meetings',
        messages: [
          { role: 'system', content: fitted.system },
          { role: 'user', content: fitted.transcript },
        ],
      },
      {
        onDelta(delta: ChatStreamDelta) {
          if (delta.content) chunks.push(delta.content)
        },
        onRetry(model: string) {
          retriedModels.add(model)
          handlers.onRetry?.(model)
        },
        onError: reject,
        onDone: () => resolve(),
      },
    )
  })
  if (chunks.length === 0 && retriedModels.size > 0) {
    throw new Error(`上游模型不可用（已重试 ${[...retriedModels].join('、')}），未返回纪要内容`)
  }
  return chunks.join('')
}
