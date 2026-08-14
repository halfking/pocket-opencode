/**
 * mobileOffline.ts — 移动端离线写操作的统一入口。
 *
 * UI 层离线时的三类写操作全部走这里：
 *   - createSessionLocally：本地建会话 + outbox 入队 session.create
 *   - enqueuePromptLocally：本地存 pending 消息 + outbox 入队 session.prompt
 *   - enqueueApprovalReplyLocally：本地存决定 + outbox 入队 approval.reply
 *
 * 所有动作都带幂等键（离线生成、存 SQLite），网络恢复后由 drainOutbox
 * 重放（见 outboxDrain.ts），由 syncSessions 收敛上游镜像（见 mobileSync.ts）。
 */

import { enqueue } from '../utils/outbox.ts'
import type { OutboxStorage } from '../utils/outbox.ts'
import type { MobileMessageRow, MobileSessionRow, MobileSyncStore } from './mobileSync.ts'
import { newLocalSessionId } from './mobileSync.ts'
import type { ApprovalReplyPayload } from './outboxDrain.ts'

function newId(): string {
  return crypto.randomUUID()
}

export async function createSessionLocally(args: {
  store: MobileSyncStore
  outbox: OutboxStorage
  workspaceId: string
  instanceId: string
  title?: string
  agent?: string
  now?: number
}): Promise<MobileSessionRow> {
  const now = args.now ?? Date.now()
  const id = newLocalSessionId()
  const idempotencyKey = `sess_${id}`
  const row: MobileSessionRow = {
    id,
    serverId: null,
    workspaceId: args.workspaceId,
    instanceId: args.instanceId,
    title: args.title ?? '',
    status: 'idle',
    idempotencyKey,
    clientRev: 1,
    serverRev: 0,
    dirty: true,
    createdAt: now,
    updatedAt: now,
    deletedAt: null,
  }
  await args.store.insertSession(row)
  await args.outbox.put(
    enqueue(
      {
        workspaceId: args.workspaceId,
        action: 'session.create',
        payload: { clientId: id, instanceId: args.instanceId, title: row.title, agent: args.agent },
        idempotencyKey,
      },
      now,
    ),
  )
  return row
}

export async function enqueuePromptLocally(args: {
  store: MobileSyncStore
  outbox: OutboxStorage
  workspaceId: string
  instanceId: string
  sessionClientId: string
  text: string
  agent?: string
  now?: number
}): Promise<MobileMessageRow> {
  const now = args.now ?? Date.now()
  const messageId = `loc_msg_${newId()}`
  const idempotencyKey = `prompt_${messageId}`
  const row: MobileMessageRow = {
    id: messageId,
    sessionId: args.sessionClientId,
    workspaceId: args.workspaceId,
    instanceId: args.instanceId,
    type: 'user',
    text: args.text,
    state: 'pending',
    serverMessageId: null,
    idempotencyKey,
    createdAt: now,
    updatedAt: now,
  }
  await args.store.insertMessage(row)
  await args.outbox.put(
    enqueue(
      {
        workspaceId: args.workspaceId,
        action: 'session.prompt',
        payload: {
          sessionClientId: args.sessionClientId,
          instanceId: args.instanceId,
          text: args.text,
          agent: args.agent,
        },
        idempotencyKey,
      },
      now,
    ),
  )
  return row
}

export async function enqueueApprovalReplyLocally(args: {
  outbox: OutboxStorage
  workspaceId: string
  reply: ApprovalReplyPayload
  now?: number
}): Promise<void> {
  const now = args.now ?? Date.now()
  const idempotencyKey = `appr_${args.reply.kind}_${args.reply.requestId}`
  await args.outbox.put(
    enqueue(
      {
        workspaceId: args.workspaceId,
        action: 'approval.reply',
        payload: args.reply,
        idempotencyKey,
      },
      now,
    ),
  )
}
