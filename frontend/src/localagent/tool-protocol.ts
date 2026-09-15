/**
 * localagent/tool-protocol.ts — 提示词驱动的工具调用协议。
 *
 * 现有 /api/llm/stream 是纯文本补全通道(无 function-calling),且网关 auto
 * 回退会跨模型路由,因此工具调用用「围栏 JSON 块」协议在提示词层实现:
 *
 *   模型需要用工具时,回合末尾输出:
 *     ```json
 *     {"tool": "tool_name", "args": { ... }}
 *     ```
 *   直接回答时不输出该块。
 *
 * 解析规则(容错优先):
 *   1. 扫描全部 ``` 围栏块(语言标记可缺,取 json/json 之外的标记也不报错);
 *   2. JSON.parse 成功且对象含字符串 `tool` 字段的块视为工具调用,取最后一个;
 *   3. parse 失败时做轻量修复(去掉尾逗号);仍失败不算工具调用——按纯文本
 *      回答展示(严格性由 system prompt + 结果回灌纠偏,不做静默重试)。
 */

export interface ParsedToolCall {
  tool: string
  args: Record<string, unknown>
  /** 围栏块之前的文本(模型的「思考段」),可为空。 */
  leadText: string
  /** 命中的围栏块全文(从回合文本中剔除用)。 */
  raw: string
}

const FENCE_RE = /```([^\n]*)\n([\s\S]*?)```/g

/** 判断模型回合文本是否携带工具调用;携带则解析出。 */
export function parseToolCall(text: string): ParsedToolCall | null {
  FENCE_RE.lastIndex = 0
  const blocks: Array<{ lang: string; code: string; raw: string; index: number }> = []
  let m: RegExpExecArray | null
  while ((m = FENCE_RE.exec(text)) !== null) {
    blocks.push({ lang: (m[1] || '').trim().toLowerCase(), code: m[2], raw: m[0], index: m.index })
  }
  for (let i = blocks.length - 1; i >= 0; i--) {
    const b = blocks[i]
    if (b.lang && b.lang !== 'json') continue
    const parsed = tryParseLoose(b.code)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) continue
    const tool = (parsed as Record<string, unknown>)['tool']
    if (typeof tool !== 'string' || tool.trim() === '') continue
    const rawArgs = (parsed as Record<string, unknown>)['args']
    const args = normalizeArgs(rawArgs)
    const leadText = text.slice(0, b.index).trim()
    return { tool: tool.trim(), args, leadText, raw: b.raw }
  }
  return null
}

function normalizeArgs(raw: unknown): Record<string, unknown> {
  if (raw === null || raw === undefined) return {}
  if (typeof raw === 'object' && !Array.isArray(raw)) return raw as Record<string, unknown>
  // args 非对象(字符串/数字等)时包一层,工具侧按 args.value 兜底可取。
  return { value: raw }
}

/** JSON.parse + 尾逗号修剪的一次重试。 */
function tryParseLoose(code: string): unknown | null {
  const trimmed = code.trim()
  try {
    return JSON.parse(trimmed)
  } catch {
    // fallthrough
  }
  try {
    return JSON.parse(trimmed.replace(/,(\s*[}\]])/g, '$1'))
  } catch {
    return null
  }
}

// ---------------------------------------------------------------------------
// system prompt 片段
// ---------------------------------------------------------------------------

/** 生成工具协议 + 工具清单的 system prompt 片段(对齐 pi buildSystemPrompt 的分层拼接)。 */
export function buildToolProtocolPrompt(tools: Array<Pick<import('./types').AgentTool, 'name' | 'description' | 'parameters' | 'promptSnippet'>>): string {
  const lines: string[] = [
    '## 工具调用协议(严格遵守)',
    '',
    '你可以使用下列工具。需要用工具时,必须在回复的**末尾**输出一个 json 围栏块并立即停止,格式:',
    '',
    '```json',
    '{"tool": "工具名", "args": { ... 参数 ... }}',
    '```',
    '',
    '规则:',
    '- 一次只调用一个工具;系统会执行它并把结果以 <tool_result> 包裹发回,你再继续。',
    '- 不需要工具时,直接用自然语言回答,**不要**输出上述围栏块。',
    '- 工具参数必须符合每个工具的参数说明;不要发明不存在的工具或参数。',
    '- 围栏块之外的文字会被当作给用户看的说明,可以简短说明你打算做什么。',
    '',
    '## 可用工具',
    '',
  ]
  for (const t of tools) {
    const params = Object.entries(t.parameters.properties || {})
      .map(([k, v]) => {
        const req = t.parameters.required?.includes(k) ? '必填' : '可选'
        return `    - ${k}(${v.type},${req}):${v.description ?? ''}`
      })
      .join('\n')
    lines.push(`### ${t.name} — ${t.description}`)
    if (t.promptSnippet) lines.push(`  提示:${t.promptSnippet}`)
    lines.push(params ? '  参数:' : '  参数:无')
    if (params) lines.push(params)
    lines.push('')
  }
  return lines.join('\n')
}

/** 包裹工具结果(user 消息内容)。 */
export function formatToolResult(toolName: string, outcome: { ok: boolean; result?: string; error?: string }, limit: number): string {
  const body = outcome.ok ? (outcome.result ?? '(空结果)') : `(执行失败)${outcome.error ?? '未知错误'}`
  const clipped = body.length > limit ? `${body.slice(0, limit)}\n…(结果过长已截断,可缩小请求范围重试)` : body
  return `<tool_result tool="${toolName}" ok="${outcome.ok}">\n${clipped}\n</tool_result>`
}
