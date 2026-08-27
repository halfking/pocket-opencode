/**
 * useApprovalAlerts — needs-input 本地通知 + Deep Link（设计方案 v2 §4.2-5）。
 *
 * 触发规则：一条待审批（permission/question）在客户端首见后 ALERT_AFTER_MS
 * （默认 3 分钟，§4.2-5 阈值）仍未处理 → 触发一条本地通知；每条请求至多
 * 通知一次；请求消失（已处理/过期）时取消未触发的排程。
 *
 * 通知分级（mobile-shell §3.3）：审批属 critical —— 声音 + 振动（默认行为）。
 *
 * 点击 Deep Link：本 App 为 hash 路由单 Activity，通知点击在 JS 层导航：
 *   /sessions/:id?instance_id=xxx&approval=open → SessionConversationView
 *   读取 approval=open 自动弹出审批 Bottom Sheet。
 *
 * 仅原生平台（Capacitor native）生效；Web 下 no-op（浏览器开发用控制台日志）。
 * FCM/APNs 长链路为 P3（§5.3），本模块只覆盖前台/近期后台场景。
 */
import { watch, onUnmounted, type Ref } from 'vue'
import { useRouter } from 'vue-router'
import { Capacitor, type PluginListenerHandle } from '@capacitor/core'
import { LocalNotifications } from '@capacitor/local-notifications'
import type { PendingItem } from '../features/tasks/useInstanceApprovals'

/** 待审批等待多久仍未处理则通知（设计方案 §4.2-5 默认 3 分钟）。 */
export const ALERT_AFTER_MS = 3 * 60_000

/** 检查间隔。 */
const CHECK_INTERVAL_MS = 30_000

/** 本地通知 id 必须是 int32；用请求 id 的稳定哈希避免随机碰撞。 */
function notificationIdFor(requestId: string): number {
  let h = 0
  for (let i = 0; i < requestId.length; i++) {
    h = (h * 31 + requestId.charCodeAt(i)) | 0
  }
  return Math.abs(h) || 1
}

export interface UseApprovalAlertsReturn {
  /** 手动触发权限申请（Android 13+ POST_NOTIFICATIONS）；静默失败 */
  ensurePermission: () => Promise<void>
}

export function useApprovalAlerts(
  pending: Ref<PendingItem[]>,
  args: { instanceId: () => string; alertAfterMs?: number },
): UseApprovalAlertsReturn {
  const router = useRouter()
  const isNative = Capacitor.isNativePlatform()
  const alertAfterMs = args.alertAfterMs ?? ALERT_AFTER_MS
  /** requestId → 通知 id；已触发/已排程的记录，防重复。 */
  const scheduled = new Map<string, number>()
  let timer: ReturnType<typeof setInterval> | null = null
  let tapListener: Promise<PluginListenerHandle> | null = null
  let permissionAsked = false

  async function ensurePermission(): Promise<void> {
    if (!isNative) return
    try {
      const status = await LocalNotifications.checkPermissions()
      if (status.display !== 'granted' && !permissionAsked) {
        permissionAsked = true
        await LocalNotifications.requestPermissions()
      }
    } catch (err) {
      console.warn('[approval-alerts] permission check failed:', err)
    }
  }

  async function cancelNotification(id: number): Promise<void> {
    try {
      await LocalNotifications.cancel({ notifications: [{ id }] })
    } catch {
      // 取消失败不影响主流程（通知可能已展示/已点击）。
    }
  }

  async function checkAndSchedule(): Promise<void> {
    if (!isNative) return
    const items = pending.value
    const now = Date.now()
    const live = new Set(items.map((p) => p.requestId))

    // 已消失的请求：取消排程并清理记录。
    for (const [requestId, id] of scheduled) {
      if (!live.has(requestId)) {
        scheduled.delete(requestId)
        void cancelNotification(id)
      }
    }

    for (const item of items) {
      if (scheduled.has(item.requestId)) continue
      const waited = now - item.firstSeenAt
      if (waited < alertAfterMs) continue
      const id = notificationIdFor(item.requestId)
      scheduled.set(item.requestId, id)
      const waitLabel = Math.max(1, Math.round(waited / 60_000))
      const title =
        item.kind === 'permission' ? '需要审批' : 'AI 在等你回答'
      const body =
        item.kind === 'permission'
          ? `${item.action ?? '工具调用'} 已等待约 ${waitLabel} 分钟`
          : `${item.question?.question?.slice(0, 40) ?? '有问题'} 已等待约 ${waitLabel} 分钟`
      try {
        await LocalNotifications.schedule({
          notifications: [
            {
              id,
              title,
              body,
              schedule: { at: new Date(now + 200) },
              extra: {
                deepLink: {
                  sessionId: item.sessionId,
                  instanceId: args.instanceId(),
                },
              },
            },
          ],
        })
      } catch (err) {
        console.warn('[approval-alerts] schedule failed:', err)
        scheduled.delete(item.requestId)
      }
    }
  }

  if (isNative) {
    void ensurePermission()
    timer = setInterval(() => void checkAndSchedule(), CHECK_INTERVAL_MS)
    // pending 变化即刻检查一次（不等轮询），保证"已处理 → 取消排程"及时。
    const stop = watch(pending, () => void checkAndSchedule(), { deep: false })
    tapListener = LocalNotifications.addListener(
      'localNotificationActionPerformed',
      (event) => {
        const deepLink = (event.notification?.extra as { deepLink?: { sessionId?: string; instanceId?: string } })
          ?.deepLink
        if (!deepLink?.sessionId) return
        router.push({
          path: `/sessions/${deepLink.sessionId}`,
          query: {
            instance_id: deepLink.instanceId ?? args.instanceId(),
            approval: 'open',
          },
        })
      },
    )
    onUnmounted(() => {
      stop()
      if (timer !== null) clearInterval(timer)
      if (tapListener) void tapListener.then((h) => void h.remove()).catch(() => {})
    })
  }

  return { ensurePermission }
}
