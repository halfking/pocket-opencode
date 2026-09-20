/**
 * notificationDispatchPolicy — 事件 → 用户感知 的纯决策(2026-09-20 通知体系 P1)。
 *
 * 拆成无运行时依赖的模块(仅依赖 localNotifications 的 id 哈希,node 可加载)
 * 以便 node --test 直接覆盖;services/notificationDispatcher.ts 负责接线。
 */
import { notificationIdFor } from '../native/localNotifications.ts'
import type { Notification } from '../api/notifications.ts'

export interface EventDescriptor {
  title: string
  body: string
  /** 点击系统通知/toast 语义的回跳路径(含 query);空 = 不回跳。 */
  deepLink: string
  /** 事件宿主页前缀:用户正在这些页面时该事件不打扰。 */
  hostPrefixes: string[]
  /** 本地通知 id(稳定哈希,重复事件不堆叠)。 */
  localId: number
}

export type Surface = 'none' | 'toast' | 'system'

/** 事件类型 → 感知文案 + 回跳 + 宿主页。结构不符的事件返回 null(仅入账不感知)。 */
export function describeEvent(eventType: string, data: unknown, envId?: string): EventDescriptor | null {
  const d = (data && typeof data === 'object' ? data : {}) as Record<string, unknown>
  const str = (k: string): string => (typeof d[k] === 'string' ? (d[k] as string) : '')
  const seed = envId || `${eventType}:${str('runId') || str('session_id') || str('id')}`
  const localId = notificationIdFor(seed)

  if (eventType === 'notification') {
    const n = data as Partial<Notification> | null
    const title = (n && typeof n.title === 'string' && n.title) || '新通知'
    const body = (n && typeof n.body === 'string' && n.body) || ''
    return { title, body, deepLink: '/notifications', hostPrefixes: ['/notifications'], localId }
  }

  if (eventType === 'scheduledtask.failed') {
    const body = str('error') ? `失败原因:${str('error').slice(0, 80)}` : '运行失败,点击查看'
    return {
      title: '定时任务失败',
      body,
      deepLink: '/settings/scheduled-tasks',
      hostPrefixes: ['/settings/scheduled-tasks'],
      localId,
    }
  }
  if (eventType === 'scheduledtask.succeeded') {
    return {
      title: '定时任务完成',
      body: '一次定时运行已成功结束',
      deepLink: '/settings/scheduled-tasks',
      hostPrefixes: ['/settings/scheduled-tasks'],
      localId,
    }
  }
  // skipped / started 等中间态:不感知。
  if (eventType.startsWith('scheduledtask.')) return null

  if (eventType === 'round.completed') {
    const sessionId = str('session_id')
    const instanceId = str('instance_id')
    const failed = str('status') === 'error'
    const summary = str('summary')
    return {
      title: failed ? 'AI 任务出错' : 'AI 任务完成',
      body: summary ? summary.slice(0, 80) : (failed ? '本轮运行出错,点击查看' : '本轮运行结束,点击查看'),
      deepLink: sessionId
        ? `/sessions/${sessionId}${instanceId ? `?instance_id=${encodeURIComponent(instanceId)}` : ''}`
        : '/sessions',
      hostPrefixes: sessionId ? [`/sessions/${sessionId}`] : ['/sessions'],
      localId,
    }
  }

  if (eventType === 'approval.permission.pending' || eventType === 'approval.question.pending') {
    const inner = unwrapInnerEnvelope(data)
    const sessionId = inner.sessionId
    const instanceId = inner.instanceId
    const question = eventType === 'approval.question.pending'
    return {
      title: question ? 'AI 在等你回答' : '需要审批',
      body: question ? 'AI 提出了一个问题,等待你的回答' : '一个工具调用等待你的批准',
      deepLink: sessionId
        ? `/sessions/${sessionId}?instance_id=${encodeURIComponent(instanceId)}&approval=open`
        : '/tasks',
      hostPrefixes: sessionId ? [`/sessions/${sessionId}`, '/tasks'] : ['/tasks'],
      localId,
    }
  }

  return null
}

/** approval 事件的内层信封解析(instance/session id 提取;容错,失败返回空)。 */
export function unwrapInnerEnvelope(data: unknown): { instanceId: string; sessionId: string } {
  try {
    const e = data as { data?: Record<string, unknown> } | null
    const d = e?.data
    if (!d || typeof d !== 'object') return { instanceId: '', sessionId: '' }
    return {
      instanceId: typeof d.instance_id === 'string' ? d.instance_id : '',
      sessionId: typeof d.session_id === 'string' ? d.session_id : '',
    }
  } catch {
    return { instanceId: '', sessionId: '' }
  }
}

/**
 * 分发决策(纯函数):
 * - 用户正停在事件宿主页 → none(页面 UI 已在更新);
 * - 前台其它页面 → toast;
 * - App 后台 → system(系统通知 + deepLink)。
 * scheduledtask.succeeded 特例:前台也不 toast(高频周期任务的完成是预期
 * 行为,toast 会变成噪音),仅后台给一条系统通知。
 */
export function decideSurfacing(
  eventType: string,
  routePath: string,
  isHidden: boolean,
  descriptor: EventDescriptor,
): Surface {
  if (!isHidden) {
    const onHost = descriptor.hostPrefixes.some(
      (p) => routePath === p || routePath.startsWith(p + '/') || routePath.startsWith(p),
    )
    if (onHost) return 'none'
    if (eventType === 'scheduledtask.succeeded') return 'none'
    return 'toast'
  }
  return 'system'
}
