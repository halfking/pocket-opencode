/**
 * useMeetingAlerts — 会议即时提醒
 *
 * 检测摘要中的新行动项、截止日期，触发应用内 toast + 浏览器通知。
 */
import { ref } from 'vue'
import type { ActionItem, LiveSummary } from '../features/meetings/meetings-store'

export interface MeetingAlert {
  id: string
  type: 'action_item' | 'deadline' | 'info'
  message: string
  createdAt: number
}

const DATE_PATTERNS = [
  /周[一二三四五六日天]/,
  /(\d{1,2})月(\d{1,2})日/,
  /(\d{4})-(\d{2})-(\d{2})/,
  /下?[个]?周[一二三四五六日天]/,
  /明天|后天|今天/,
]

export function useMeetingAlerts() {
  const alerts = ref<MeetingAlert[]>([])
  const seenActionKeys = new Set<string>()
  let notificationPermission: NotificationPermission = 'default'

  async function requestNotificationPermission() {
    if (!('Notification' in window)) return
    if (Notification.permission === 'granted') {
      notificationPermission = 'granted'
      return
    }
    if (Notification.permission !== 'denied') {
      notificationPermission = await Notification.requestPermission()
    }
  }

  function pushAlert(type: MeetingAlert['type'], message: string) {
    const alert: MeetingAlert = {
      id: `alert-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`,
      type,
      message,
      createdAt: Date.now(),
    }
    alerts.value = [alert, ...alerts.value].slice(0, 5)

    if (notificationPermission === 'granted' && document.hidden) {
      new Notification('会议提醒', { body: message, tag: alert.id })
    }

    // 5 秒后自动清除 toast
    setTimeout(() => dismissAlert(alert.id), 8000)
  }

  function dismissAlert(id: string) {
    alerts.value = alerts.value.filter((a) => a.id !== id)
  }

  /** 对比新旧摘要，检测增量行动项 */
  function onSummaryUpdated(summary: LiveSummary | null) {
    if (!summary) return

    for (const item of summary.actionItems) {
      const key = `${item.text}|${item.due ?? ''}`
      if (seenActionKeys.has(key)) continue
      seenActionKeys.add(key)

      let msg = `新行动项：${item.text}`
      if (item.assignee) msg += `（${item.assignee}）`
      if (item.due) msg += `，截止 ${item.due}`
      pushAlert('action_item', msg)
    }

    // 从摘要文本检测日期表达
    for (const pattern of DATE_PATTERNS) {
      const match = summary.summary.match(pattern)
      if (match) {
        const key = `date:${match[0]}`
        if (!seenActionKeys.has(key)) {
          seenActionKeys.add(key)
          pushAlert('deadline', `检测到时间：${match[0]}`)
        }
        break
      }
    }
  }

  /** 从转写文本检测关键词（用户 watchlist 可扩展） */
  function onTranscriptChunk(text: string, watchKeywords: string[] = []) {
    for (const kw of watchKeywords) {
      if (text.includes(kw)) {
        const key = `kw:${kw}`
        if (!seenActionKeys.has(key)) {
          seenActionKeys.add(key)
          pushAlert('info', `关键词「${kw}」被提及`)
        }
      }
    }
  }

  function reset() {
    seenActionKeys.clear()
    alerts.value = []
  }

  return {
    alerts,
    pushAlert,
    dismissAlert,
    onSummaryUpdated,
    onTranscriptChunk,
    requestNotificationPermission,
    reset,
  }
}
