/**
 * notification.ts — S0-E Notification Center 状态管理。
 *
 * inbox 列表 + 未读计数 + 前台 WS 实时推送接入。
 *
 * WS 接入：在 main.ts 启动时调 notificationStore.subscribeWs()，把 websocket-hub
 * 的 'notification' 事件接到 store。后台/锁屏推送由 APNs/FCM（部署期接入）
 * 负责，本 store 只管前台 + inbox 历史。
 */
import { defineStore } from 'pinia'
import {
  notificationsApi,
  type Notification,
  type NotificationRule,
} from '../api/notifications'

export const useNotificationStore = defineStore('notification', {
  state: () => ({
    inbox: [] as Notification[],
    rules: [] as NotificationRule[],
    loading: false,
  }),
  getters: {
    unreadCount: (s) => s.inbox.filter((n) => n.read_at === 0).length,
    unreadUrgent: (s) =>
      s.inbox.filter((n) => n.read_at === 0 && n.priority === 'urgent'),
  },
  actions: {
    async loadInbox(opts: { limit?: number; unread?: boolean } = {}) {
      this.loading = true
      try {
        // 增量拉取：已有 inbox 时带 since 只取新通知，合并后按时间倒序，
        // 避免每次全量替换导致列表闪烁/滚动跳动。
        const since = this.inbox.length
          ? Math.max(...this.inbox.map((n) => n.created_at || 0))
          : 0
        const res = await notificationsApi.list({ limit: 50, ...opts, since })
        const fresh = res.notifications ?? []
        if (!this.inbox.length) {
          this.inbox = fresh
        } else {
          const seen = new Set(this.inbox.map((n) => n.id))
          const additions = fresh.filter((n) => !seen.has(n.id))
          this.inbox = [...additions, ...this.inbox].sort(
            (a, b) => (b.created_at || 0) - (a.created_at || 0),
          )
        }
      } finally {
        this.loading = false
      }
    },
    async loadRules() {
      this.rules = await notificationsApi.listRules()
    },
    async markRead(id?: string) {
      await notificationsApi.markRead(id)
      if (id) {
        const n = this.inbox.find((x) => x.id === id)
        if (n) n.read_at = Math.floor(Date.now() / 1000)
      } else {
        this.inbox.forEach((n) => {
          if (n.read_at === 0) n.read_at = Math.floor(Date.now() / 1000)
        })
      }
    },
    async upsertRule(rule: NotificationRule) {
      const saved = await notificationsApi.upsertRule(rule)
      const idx = this.rules.findIndex((r) => r.id === rule.id)
      if (idx >= 0) this.rules[idx] = saved
      else this.rules.push(saved)
      return saved
    },
    /**
     * 前台 WS 推送接入。后端通过 wsHub.Broadcast('notification', n) 推过来。
     * 在 main.ts 启动后调用一次：
     *   notificationStore.subscribeWs(wsClient)
     */
    subscribeWs(wsClient: { on?: (type: string, cb: (msg: any) => void) => void }) {
      wsClient.on?.('notification', (msg) => {
        const n: Notification = msg?.data ?? msg?.payload ?? msg
        if (n && n.id) {
          // 去重：避免 WS 推 + 列表拉重合
          if (!this.inbox.some((x) => x.id === n.id)) {
            this.inbox.unshift(n)
          }
        }
      })
    },
    /**
     * 收到一条本地产生的通知（业务模块主动 push 给前台）。
     */
    pushLocal(n: Notification) {
      if (!this.inbox.some((x) => x.id === n.id)) {
        this.inbox.unshift(n)
      }
    },
  },
})
