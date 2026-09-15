/**
 * localagent/agent-loop.ts — 本地智能体核心循环。
 *
 * 语义移植自 pi packages/agent/src/agent-loop.ts 的双重 while(此处裁剪为
 * 单层:无 steering/followUp 队列,MVP 不需要),关键保留:
 *   - StreamFn 与循环解耦(LLM 传输可替换);
 *   - beforeToolCall 审批闸门(block 语义 → ApprovalGate 返回 false = 注入
 *     「用户拒绝」结果继续循环,模型可改道,对应 openhands UserRejectObservation);
 *   - maxSteps 兜底防失控;
 *   - abort signal 贯穿(回合间 + 工具执行中检查)。
 *
 * 协议层:回合文本由 tool-protocol.parseToolCall 解析工具调用;工具结果以
 * <tool_result> user 消息回灌。
 */

import {
  type AgentChatMessage,
  type AgentEvent,
  type AgentLoopOptions,
  type ToolOutcome,
} from './types.ts'
import { formatToolResult, parseToolCall } from './tool-protocol.ts'

const DEFAULT_MAX_STEPS = 12
const DEFAULT_TOOL_RESULT_LIMIT = 4000

export async function runAgentLoop(opts: AgentLoopOptions): Promise<void> {
  const maxSteps = opts.maxSteps ?? DEFAULT_MAX_STEPS
  const limit = opts.toolResultLimit ?? DEFAULT_TOOL_RESULT_LIMIT
  const toolMap = new Map(opts.tools.map((t) => [t.name, t]))
  const messages: AgentChatMessage[] = [
    { role: 'system', content: opts.systemPrompt },
    ...opts.history,
    { role: 'user', content: opts.prompt },
  ]

  let steps = 0
  let finish: 'completed' | 'max_steps' | 'aborted' = 'max_steps'
  // 模型给出直接回答(无工具调用)才算自然完成;否则循环退出即步数耗尽。
  let finishedNaturally = false

  while (steps < maxSteps) {
    if (opts.signal.aborted) {
      finish = 'aborted'
      break
    }
    steps++
    emit(opts, { type: 'status', status: 'thinking' })

    let turnText: string
    let usage: { promptTokens: number; completionTokens: number } | undefined
    try {
      const res = await opts.streamFn({
        messages,
        signal: opts.signal,
        onDelta: (text) => emit(opts, { type: 'text_delta', text }),
      })
      turnText = res.text
      usage = res.usage
    } catch (err) {
      if (opts.signal.aborted) {
        finish = 'aborted'
        break
      }
      emit(opts, { type: 'run_error', error: err instanceof Error ? err.message : String(err) })
      return
    }
    if (usage && (usage.promptTokens > 0 || usage.completionTokens > 0)) {
      emit(opts, { type: 'usage', promptTokens: usage.promptTokens, completionTokens: usage.completionTokens })
    }
    if (opts.signal.aborted) {
      // abort 与流正常 resolve 竞态:取消优先,不把残留文本当答案。
      finish = 'aborted'
      break
    }

    const call = parseToolCall(turnText)
    if (!call) {
      if (!turnText.trim()) {
        // 空回合(空气泡陷阱,对齐 aiStreamRuntime 的 empty 语义)。
        emit(opts, { type: 'run_error', error: '模型未返回内容(空流)' })
        return
      }
      // 直接回答:展示全文,循环结束。
      finishedNaturally = true
      finish = 'completed'
      emit(opts, { type: 'assistant', text: turnText.trim() })
      break
    }

    // 思考段(围栏块之前的文本):作为中间说明展示,并进入上下文。
    const lead = call.leadText
    if (lead) emit(opts, { type: 'assistant', text: lead, interim: true })

    const tool = toolMap.get(call.tool)
    const callId = `call-${steps}-${Math.random().toString(36).slice(2, 8)}`
    if (!tool) {
      // 幻觉工具:把可用清单回灌纠偏,继续循环。
      const known = [...toolMap.keys()].join(', ')
      emit(opts, {
        type: 'tool_call',
        id: callId,
        name: call.tool,
        args: call.args,
        state: 'error',
        error: `未知工具,可用:${known}`,
        risk: 'low',
      })
      pushTurn(messages, turnText, formatToolResult(call.tool, { ok: false, error: `未知工具 "${call.tool}"。可用工具:${known}` }, limit))
      continue
    }

    // 审批闸门(pi beforeToolCall block 语义)。注意:approval_required 事件由
    // runtime 的 gate 实现在「注册 pending 审批之后」发出,保证 UI 同步时能看到。
    if (tool.risk !== 'low' && opts.approvalGate) {
      emit(opts, { type: 'status', status: 'waiting_approval' })
      let allowed: boolean
      try {
        allowed = await opts.approvalGate({ toolCallId: callId, tool: tool.name, args: call.args, risk: tool.risk })
      } catch {
        allowed = false
      }
      if (opts.signal.aborted) {
        finish = 'aborted'
        break
      }
      if (!allowed) {
        emit(opts, {
          type: 'tool_call',
          id: callId,
          name: tool.name,
          args: call.args,
          state: 'denied',
          result: '用户拒绝执行该工具',
          risk: tool.risk,
        })
        pushTurn(messages, turnText, formatToolResult(tool.name, { ok: false, error: '用户拒绝了本次工具执行。请改用不需要该工具的方式回答,或先向用户说明。' }, limit))
        continue
      }
    }

    // 执行。
    const startedAt = Date.now()
    emit(opts, { type: 'status', status: 'tool_running' })
    emit(opts, { type: 'tool_call', id: callId, name: tool.name, args: call.args, state: 'running', risk: tool.risk })
    let outcome: ToolOutcome
    try {
      outcome = await tool.execute(call.args, { signal: opts.signal, emit: (evt) => emit(opts, evt) })
    } catch (err) {
      if (opts.signal.aborted) {
        finish = 'aborted'
        break
      }
      outcome = { ok: false, error: err instanceof Error ? err.message : String(err) }
    }
    const durationMs = Date.now() - startedAt
    emit(opts, {
      type: 'tool_call',
      id: callId,
      name: tool.name,
      args: call.args,
      state: outcome.ok ? 'completed' : 'error',
      result: outcome.ok ? outcome.result : undefined,
      error: outcome.ok ? undefined : outcome.error,
      data: outcome.data,
      durationMs,
      risk: tool.risk,
    })
    pushTurn(messages, turnText, formatToolResult(tool.name, outcome, limit))
  }

  if (!finishedNaturally && finish === 'max_steps') {
    emit(opts, { type: 'status', status: 'error' })
    emit(opts, { type: 'assistant', text: '(已达到单次任务的最大执行步数,已停止。你可以让我继续。)' })
  } else {
    emit(opts, { type: 'status', status: finish === 'aborted' ? 'aborted' : 'done' })
  }
  emit(opts, { type: 'run_done', steps, reason: finish })
}

function pushTurn(messages: AgentChatMessage[], assistantText: string, toolResult: string): void {
  messages.push({ role: 'assistant', content: assistantText })
  messages.push({ role: 'user', content: toolResult })
}

function emit(opts: AgentLoopOptions, evt: AgentEvent): void {
  try {
    opts.onEvent(evt)
  } catch {
    // UI 订阅者异常不拖垮循环。
  }
}
