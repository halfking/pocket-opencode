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
import { useNotificationPermission, type NotificationPermissionState } from './useNotificationPermission'
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
  /** 手动触发权限申请（Android 13+ POST_NOTIFICATIONS）。 */
  ensurePermission: () => Promise<void>
  /**
   * ⚠§865/D29：最近一次申请的结果（`'granted' | 'denied' | …`）。
   * 暴露出来是为了让宿主**有机会说实话** ——
   * 被拒之后审批提醒是静默失效的（用户提交了审批、等不到通知、界面零解释），
   * 而本 composable 此前连结果都不看。
   * ⚠ 宿主现在还没有用它说任何话（要不要提示、跳不跳转属产品口径，已登记）。
   */
  permissionState: Ref<NotificationPermissionState>
}

export function useApprovalAlerts(
  pending: Ref<PendingItem[]>,
  args: { instanceId: () => string; alertAfterMs?: number },
): UseApprovalAlertsReturn {
  const router = useRouter()
  const isNative = Capacitor.isNativePlatform()
  // §865/D29：申请路径必须唯一 —— 共享 composable 才有那套完整的跨平台回退
  // （原生 appSettings → 旧 LocalNotifications → Web Notification）与 `prompt-with-rationale` 态。
  const permission = useNotificationPermission()
  const alertAfterMs = args.alertAfterMs ?? ALERT_AFTER_MS
  /** requestId → 通知 id；已触发/已排程的记录，防重复。 */
  const scheduled = new Map<string, number>()
  let timer: ReturnType<typeof setInterval> | null = null
  let tapListener: Promise<PluginListenerHandle> | null = null
  let permissionAsked = false

  /**
   * ⚠⚠§865/D29：这里原本是**第二条独立的权限申请路径** ——
   *   直接调 `LocalNotifications.checkPermissions()` + `requestPermissions()`，
   *   ① 不走共享的 `useNotificationPermission`（⇒ 结果不汇入设置页已经渲染的那个状态）；
   *   ② **申请完不看结果**（⇒ 被拒之后审批提醒静默失效，界面上零解释）；
   *   ③ 而且触发时机是「用户一进任务页就弹系统框」，**没有任何前置说明**
   *      （Android 上无 rationale 的弹窗被拒率显著更高 ——
   *       共享 composable 里本来就有 `prompt-with-rationale` 这一态，只是这里用不上）。
   *
   * ⇒ 修法是**复用共享 composable**，让「申请路径」全仓只有一条；
   *   结果同时进入设置页已渲染的状态，并在这里留一条 warn 供诊断。
   */
  const permissionState = permission.state
  async function ensurePermission(): Promise<void> {
    if (!isNative) return
    if (permissionState.value === 'granted') return
    if (permissionAsked) return
    permissionAsked = true
    try {
      const result = await permission.ensure()
      if (result !== 'granted') {
        console.warn(
          `[approval-alerts] 通知权限未开启（${result}）：待审批 3 分钟后不会提醒，`
          + '设置页可查看并跳转系统设置',
        )
      }
    } catch (err) {
      console.warn('[approval-alerts] permission request failed:', err)
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

  return { ensurePermission, permissionState }
}
