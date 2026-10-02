/**
 * notification.ts — S0-E Notification Center 状态管理。
 *
 * inbox 列表 + 未读计数。
 *
 * 接线(2026-09-20 通知体系 P1):main.ts 启动 notificationDispatcher 后,
 * 'notification' WS 事件经幂等总线入账(pushLocal);WS 连接成功(首连/
 * 重连)时增量 loadInbox。后台/锁屏推送由 APNs/FCM(部署期接入)负责,
 * 本 store 只管前台 + inbox 历史。
 */
import { defineStore } from 'pinia'
import {
  notificationsApi,
  type Notification,
  type NotificationRule,
} from '../api/notifications'

/**
 * 首次加载通知历史时要的条数。
 *
 * 取 200 是因为这是后端的硬上限：`notifycenter.Store.ListNotifications`
 * 里 `if limit <= 0 || limit > 200 { limit = 50 }` —— 再大也会被静默压回 50，
 * 那就等于没改。所以这个常量**必须与后端上限同步**，调大它不会有任何效果，
 * 只会在后端放宽上限时让人误以为已经放宽了。
 */
const NOTIFICATION_FIRST_LOAD_LIMIT = 200

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
        // 首次加载与增量必须分开走，理由是**首次拉不到更旧的那一页**。
        //
        // 后端 GET /api/notifications 只有 limit / unread / since 三个参数，
        // 没有 offset 或游标（server_notifycenter.go:38-52），而 since 只能
        // 取**更新**的（filterNotificationsSince 过滤 created_at > since）。
        // 也就是说：客户端没有任何办法把分页往回翻。首次拿到的就是
        // `ORDER BY created_at DESC LIMIT n` 的那 n 条，n 之外的更老通知
        // 在此后的每一次增量里都不会被再请求一次。
        //
        // 2026-10-02 实测的临界点：通知中心当时 24 条（全部 email.important），
        // limit=50 够用。需求 4 的 90 天窗口首次上线会一次性推 32 条 →
        // 总数 56 > 50，于是最旧的 6 条对「首次加载发生在那之后」的客户端
        // （换设备、清了本地存储、此前没打开过通知中心）永久不可见。
        //
        // 所以首次加载直接要满后端上限；再往上就得后端给游标了，那是另一次
        // 改动，不在这里假装解决。
        const firstLoad = this.inbox.length === 0
        const since = firstLoad
          ? 0
          : Math.max(...this.inbox.map((n) => n.created_at || 0))
        const limit = opts.limit ?? (firstLoad ? NOTIFICATION_FIRST_LOAD_LIMIT : 50)
        const res = await notificationsApi.list({ ...opts, limit, since })
        const fresh = res.notifications ?? []
        if (firstLoad) {
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
