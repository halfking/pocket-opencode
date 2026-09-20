/**
 * notificationDispatcher — 进程级「事件 → 用户感知」分发器(2026-09-20 通知体系 P1)。
 *
 * 职责边界:
 * - 后端 notifycenter 负责 inbox 持久化 + 前台 WS 定向推送(跨设备、离线可补);
 * - 本模块在前端把关键 WS 事件转成三层用户感知:
 *     1. inbox 入账(notification store,未读徽标即亮 —— 前台 UI 更新);
 *     2. 前台非宿主页 → toast(即时提示,不打断当前操作);
 *     3. App 在后台(appLifecycleHub.isHidden)→ 系统本地通知(带 deepLink,
 *        点击回跳对应页面)——「任务完成系统通知」能力的唯一入口。
 * - 「用户正停在事件宿主页」→ 不打扰:页面自身 UI 已在实时更新。
 *
 * 决策纯函数在 notificationDispatchPolicy.ts(node --test 直接覆盖);
 * start() 一次性接线(main.ts),组件层不再各自为战。
 */
import { Capacitor } from '@capacitor/core'
import { LocalNotifications } from '@capacitor/local-notifications'
import type { Pinia } from 'pinia'
import type { Router } from 'vue-router'
import { subscribe as wsBusSubscribe, type WsEnvelopeV1 } from './idempotentWsBus'
import { describeEvent, decideSurfacing } from './notificationDispatchPolicy'
import { appLifecycleHub } from '../native/appLifecycleHub'
import { useToast } from '../composables/useToast'
import { useNotificationStore } from '../stores/notification'
import type { Notification } from '../api/notifications'
import wsClient from '../api/websocket'

const DISPATCHED_EVENT_TYPES = [
  'notification',
  'scheduledtask.succeeded',
  'scheduledtask.failed',
  'scheduledtask.skipped',
  'round.completed',
  'approval.permission.pending',
  'approval.question.pending',
] as const

let started = false
let routerRef: Router | null = null
let permissionAsked = false
let unsubscribers: Array<() => void> = []

async function ensurePermission(): Promise<void> {
  if (!Capacitor.isNativePlatform() || permissionAsked) return
  permissionAsked = true
  try {
    const status = await LocalNotifications.checkPermissions()
    if (status.display !== 'granted') {
      await LocalNotifications.requestPermissions()
    }
  } catch (err) {
    console.warn('[notification-dispatcher] permission check failed:', err)
  }
}

async function postSystemNotification(descriptor: { localId: number; title: string; body: string; deepLink: string }): Promise<void> {
  if (!Capacitor.isNativePlatform()) return
  try {
    await LocalNotifications.schedule({
      notifications: [
        {
          id: descriptor.localId,
          title: descriptor.title,
          body: descriptor.body,
          schedule: { at: new Date(Date.now() + 200) },
          extra: { deepLink: descriptor.deepLink, dispatcher: true },
        },
      ],
    })
  } catch (err) {
    console.warn('[notification-dispatcher] schedule failed:', err)
  }
}

function handleEvent(eventType: string, env: WsEnvelopeV1<unknown>): void {
  const store = useNotificationStore()

  // 1. inbox 入账:后端 notification 推送直接进列表(去重由 store 保证);
  //    本地感知类事件(scheduledtask/round/approval)不写 inbox —— 它们的
  //    持久化记录(若需)由后端 notifycenter 决定,前端只负责即时感知。
  if (eventType === 'notification') {
    const n = (env.data ?? null) as Notification | null
    if (n && n.id) store.pushLocal(n)
  }

  const descriptor = describeEvent(eventType, env.data, env.id)
  if (!descriptor) return

  const routePath = routerRef?.currentRoute.value.path ?? ''
  const surface = decideSurfacing(eventType, routePath, appLifecycleHub.isHidden(), descriptor)
  if (surface === 'none') return

  if (surface === 'system') {
    void postSystemNotification(descriptor)
    return
  }
  const toast = useToast()
  if (descriptor.body) toast.info(`${descriptor.title} — ${descriptor.body}`)
  else toast.info(descriptor.title)
}

/**
 * 启动分发器。main.ts 在 pinia 安装后调用一次;重复调用安全。
 * - 订阅 idempotentWsBus 的关键事件;
 * - 注册系统通知点击回跳(按 dispatcher 自有 deepLink 字符串过滤,与审批
 *   告警的对象格式、闪卡的 /flashcards/review 互不干扰);
 * - WS 连接成功(首连/重连)→ 通知 inbox 增量补拉(hub 不回放事件)。
 */
export function startNotificationDispatcher(pinia: Pinia, router: Router): void {
  if (started) return
  started = true
  routerRef = router
  useNotificationStore(pinia)
  void ensurePermission()

  for (const t of DISPATCHED_EVENT_TYPES) {
    const handle = wsBusSubscribe(t, (env) => handleEvent(t, env))
    unsubscribers.push(handle.unsubscribe)
  }

  // 系统通知点击 → deepLink 回跳。extra.dispatcher 标记本模块自有载荷;
  // 其它监听器(useApprovalAlerts/闪卡)各按自家格式过滤,不会重复处理。
  if (Capacitor.isNativePlatform()) {
    void LocalNotifications.addListener('localNotificationActionPerformed', (event) => {
      const extra = event.notification?.extra as { dispatcher?: boolean; deepLink?: string } | undefined
      if (!extra?.dispatcher || typeof extra.deepLink !== 'string' || !extra.deepLink) return
      const [path, query] = extra.deepLink.split('?')
      const queryObj: Record<string, string> = {}
      if (query) {
        for (const pair of query.split('&')) {
          const [k, v] = pair.split('=')
          if (k) queryObj[k] = decodeURIComponent(v ?? '')
        }
      }
      routerRef?.push({ path, query: queryObj })
    }).catch(() => { /* 监听注册失败仅影响回跳,通知本体已送达 */ })
  }

  unsubscribers.push(wsClient.onConnected(() => {
    void useNotificationStore().loadInbox().catch(() => { /* 离线时静默,下次连接再补 */ })
  }))
}

/** 测试用:复位内部状态。 */
export function _resetNotificationDispatcherForTest(): void {
  for (const u of unsubscribers) u()
  unsubscribers = []
  started = false
  routerRef = null
  permissionAsked = false
}
