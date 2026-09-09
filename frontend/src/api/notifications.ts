/**
 * notifications.ts — S0-E Notification Center API client.
 *
 * 对接后端 /api/notifications 子树：
 *   GET  /api/notifications              inbox 列表（?limit=&unread=）
 *   POST /api/notifications/mark-read    标记已读（id 或全部）
 *   GET  /api/notifications/rules        列出规则
 *   POST /api/notifications/rules        创建/更新规则
 *
 * 前台实时推送通过 WS（websocket-hub.ts 已识别 'notification' 类型）。
 */
import { http } from './http'

export interface Notification {
  id: string
  workspace_id: string
  user_id: string
  source: string
  kind: string
  title: string
  body: string
  payload?: unknown
  priority: 'low' | 'normal' | 'high' | 'urgent'
  read_at: number
  created_at: number
}

export interface NotificationRule {
  id: string
  workspace_id: string
  source: string
  kind: string
  channels: string[] // 'inbox' | 'websocket' | 'apns' | 'fcm'
  priority: 'low' | 'normal' | 'high' | 'urgent'
  quiet_start_min: number
  quiet_end_min: number
  enabled: boolean
}

export const notificationsApi = {
  // 增量同步信封（docs/2026-09-09-list-sync-rules.md §3.2）：since（秒）
  // 只回传 created_at 更晚的通知；serverTimeMs 供时钟校正。
  list: (opts: { limit?: number; unread?: boolean; since?: number } = {}) => {
    const p = new URLSearchParams()
    if (opts.limit) p.set('limit', String(opts.limit))
    if (opts.unread) p.set('unread', '1')
    if (opts.since && opts.since > 0) p.set('since', String(opts.since))
    const qs = p.toString()
    return http<{ notifications: Notification[]; serverTimeMs?: number }>(
      `/api/notifications${qs ? '?' + qs : ''}`,
    )
  },

  markRead: (id?: string) =>
    http<{ ok: string }>('/api/notifications/mark-read', {
      method: 'POST',
      body: JSON.stringify({ id: id ?? '' }),
    }),

  listRules: () =>
    http<{ rules: NotificationRule[] }>('/api/notifications/rules').then((r) => r.rules),

  upsertRule: (rule: NotificationRule) =>
    http<NotificationRule>('/api/notifications/rules', {
      method: 'POST',
      body: JSON.stringify(rule),
    }),
}
