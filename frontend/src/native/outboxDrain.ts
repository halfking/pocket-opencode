/**
 * outboxDrain.ts — 移动离线队列的重放循环（SEC-06）。
 *
 * 宿主在网络恢复 / App 唤醒时调用 drainOutbox：
 *   1. listReady 取出到期记录（按 createdAt 顺序）
 *   2. workspace 不匹配的记录直接丢弃（SEC-06：切换 workspace 不发送旧队列）
 *   3. claim → 发送 → 成功删除 / 可重试失败指数退避 / 超限进死信
 *   4. 审批类 409（不再 pending）视为终态，避免永久重试已过期的审批
 *
 * 发送器（OutboxSender）按 action 注入；与后端 /api/mobile/* 路由对应的
 * 生产发送器见本文件底部 createMobileOutboxSenders。
 */

import type { OutboxRecord, OutboxStorage } from '../utils/outbox.ts'
import {
  claim,
  deadLetter,
  failForRetry,
  matchesWorkspace,
  shouldDeadLetter,
  succeed,
} from '../utils/outbox.ts'
import type { MobileSyncStore } from './mobileSync.ts'
import type { SqliteApprovalStore } from './approvalStore.ts'

export interface SendOutcome {
  ok: boolean
  /** 终态失败（如审批已过期）：停止重试，转入死信。 */
  terminal?: boolean
  /** 服务端返回的续传游标。 */
  cursor?: string
  errorCode?: string
}

export type OutboxSender = (record: OutboxRecord) => Promise<SendOutcome>

export interface DrainOptions {
  workspaceId: string
  /** 每个 action 对应的发送器。 */
  senders: Record<string, OutboxSender>
  now?: () => number
  batchSize?: number
  maxAttempts?: number
}

export interface DrainResult {
  succeeded: number
  retried: number
  deadLettered: number
  droppedWorkspaceMismatch: number
  deadLetteredNoSender: number
}

export async function drainOutbox(
  storage: OutboxStorage,
  opts: DrainOptions,
): Promise<DrainResult> {
  const now = opts.now ?? Date.now
  const batchSize = opts.batchSize ?? 16
  const maxAttempts = opts.maxAttempts ?? 8
  const result: DrainResult = {
    succeeded: 0,
    retried: 0,
    deadLettered: 0,
    droppedWorkspaceMismatch: 0,
    deadLetteredNoSender: 0,
  }

  const ready = await storage.listReady(now(), batchSize)
  for (const record of ready) {
    if (!matchesWorkspace(record, opts.workspaceId)) {
      await storage.delete(record.id)
      result.droppedWorkspaceMismatch++
      continue
    }

    const sender = opts.senders[record.action]
    if (sender === undefined) {
      await storage.put(deadLetter(record, 'no_sender'))
      result.deadLetteredNoSender++
      continue
    }

    const claimed = claim(record, now())
    await storage.put(claimed)

    let outcome: SendOutcome
    try {
      outcome = await sender(claimed)
    } catch (err) {
      outcome = {
        ok: false,
        errorCode: err instanceof Error ? err.message : String(err),
      }
    }

    if (outcome.ok) {
      await storage.put(succeed(claimed, outcome.cursor))
      await storage.delete(claimed.id)
      result.succeeded++
      continue
    }
    if (shouldDeadLetter(claimed, now(), maxAttempts) || outcome.terminal) {
      await storage.put(deadLetter(claimed, outcome.errorCode ?? 'exhausted'))
      result.deadLettered++
      continue
    }
    await storage.put(failForRetry(claimed, outcome.errorCode ?? 'send_failed', now()))
    result.retried++
  }
  return result
}

// ---------------------------------------------------------------------------
// 生产发送器：映射到后端 /api/mobile/* 路由
// ---------------------------------------------------------------------------

export type AuthorizedFetch = (
  input: string,
  init?: { method?: string; headers?: Record<string, string>; body?: string },
) => Promise<{ status: number; json: () => Promise<unknown> }>

export interface SessionCreatePayload {
  clientId: string
  instanceId: string
  title?: string
  agent?: string
}

export interface SessionPromptPayload {
  /** local_mobile_sessions.id（客户端行 id）。 */
  sessionClientId: string
  instanceId: string
  text: string
  agent?: string
}

export interface ApprovalReplyPayload {
  kind: 'permission' | 'question'
  requestId: string
  instanceId: string
  sessionId: string
  decision?: string
  message?: string
  answers?: unknown
}

async function sendJson(
  doFetch: AuthorizedFetch,
  url: string,
  body: unknown,
  idempotencyKey: string,
): Promise<SendOutcome> {
  try {
    const res = await doFetch(url, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Idempotency-Key': idempotencyKey,
      },
      body: JSON.stringify(body ?? {}),
    })
    if (res.status >= 200 && res.status < 300) {
      let cursor: string | undefined
      try {
        const parsed = (await res.json()) as { id?: string; messageID?: string }
        cursor = parsed?.id ?? parsed?.messageID
      } catch {
        // 204/空响应无游标
      }
      return { ok: true, cursor }
    }
    if (res.status === 409) {
      // 审批/会话状态已变化（如审批不再 pending）：终态，停止重试。
      return { ok: false, terminal: true, errorCode: 'conflict_not_pending' }
    }
    if (res.status === 401 || res.status === 403 || res.status === 404) {
      // 鉴权/资源错误重试无益（404 可能是实例下线），交给 TTL 死信。
      return { ok: false, terminal: true, errorCode: `http_${res.status}` }
    }
    return { ok: false, errorCode: `http_${res.status}` }
  } catch (err) {
    return { ok: false, errorCode: err instanceof Error ? err.message : String(err) }
  }
}

/** 回复成功后的决定标签（用于 approvalStore.markReplied）。 */
function approvalDecisionLabel(payload: ApprovalReplyPayload): string {
  if (payload.kind === 'permission') return payload.decision ?? 'once'
  return payload.decision === 'reject' ? 'reject' : 'answer'
}

export function createMobileOutboxSenders(args: {
  doFetch: AuthorizedFetch
  syncStore: MobileSyncStore
  /** 可选：审批本地表，drain 终态回写（sent / expired）。 */
  approvalStore?: Pick<SqliteApprovalStore, 'markReplied' | 'markExpired'>
}): Record<string, OutboxSender> {
  const { doFetch, syncStore, approvalStore } = args

  return {
    'session.create': async (record) => {
      const payload = record.payload as SessionCreatePayload
      const outcome = await sendJson(
        doFetch,
        `/api/mobile/sessions?instance_id=${encodeURIComponent(payload.instanceId)}`,
        payload.agent !== undefined ? { agent: payload.agent } : {},
        record.idempotencyKey,
      )
      // 上游创建成功即绑定 serverId，让本地会话立即可收 prompt 重放。
      if (outcome.ok && outcome.cursor !== undefined) {
        const row = await syncStore.findSessionById(payload.clientId)
        if (row !== null && row.serverId === null) {
          await syncStore.updateSession({ ...row, serverId: outcome.cursor, dirty: false, updatedAt: Date.now() })
        }
      }
      return outcome
    },

    'session.prompt': async (record) => {
      const payload = record.payload as SessionPromptPayload
      const session = await syncStore.findSessionById(payload.sessionClientId)
      if (session === null || session.serverId === null) {
        // 离线创建的会话还没同步到上游：可重试，等 session.create 先完成。
        return { ok: false, errorCode: 'session_not_synced' }
      }
      const body: Record<string, unknown> = { text: payload.text }
      if (payload.agent !== undefined) body.agent = payload.agent
      const outcome = await sendJson(
        doFetch,
        `/api/mobile/sessions/${encodeURIComponent(session.serverId)}/prompt?instance_id=${encodeURIComponent(payload.instanceId)}`,
        body,
        record.idempotencyKey,
      )
      if (outcome.ok) {
        await syncStore.markMessageSentByIdempotencyKey(record.idempotencyKey, outcome.cursor ?? null)
      }
      return outcome
    },

    'approval.reply': async (record) => {
      const payload = record.payload as ApprovalReplyPayload
      const base = `/api/mobile/approvals/${payload.kind}/${encodeURIComponent(payload.requestId)}`
      const suffix = payload.kind === 'question' && payload.decision === 'reject' ? 'reject' : 'reply'
      const body: Record<string, unknown> = {
        instance_id: payload.instanceId,
        session_id: payload.sessionId,
      }
      if (payload.kind === 'permission') {
        body.decision = payload.decision ?? 'once'
        if (payload.message !== undefined) body.message = payload.message
      } else if (suffix === 'reply') {
        body.answers = payload.answers ?? []
      }
      const outcome = await sendJson(doFetch, `${base}/${suffix}`, body, record.idempotencyKey)
      // 服务端确认后才置 sent；409（不再 pending）置 expired（08 §4.5）。
      if (approvalStore !== undefined) {
        if (outcome.ok) {
          await approvalStore.markReplied(payload.requestId, approvalDecisionLabel(payload))
        } else if (outcome.terminal && outcome.errorCode === 'conflict_not_pending') {
          await approvalStore.markExpired(payload.requestId)
        }
      }
      return outcome
    },
  }
}
