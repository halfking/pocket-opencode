/**
 * localNotifications — 闪卡每日复习提醒的本地通知封装。
 *
 * 设计要点（与 useApprovalAlerts 对齐）：
 * - Web / Harmony / 无插件时所有方法降级为空或抛错，调用方继续走 noop。
 * - 仅原生平台（Capacitor Android / iOS）真正生效。
 * - Android 12+ SCHEDULE_EXACT_ALARM 被拒时自动回退 inexact（allowWhileIdle=false），
 *   并把 isExactNotification 设为 false；同时通过 emit('alarm-denied') 通知 UI 层
 *   （VivoBatteryWhitelistGuide）做 OriginOS 后台保活引导。
 * - 调用者收到 onReviewTap 时应跳转到 `/flashcards/review` 或当前 deck。
 *
 * 与 useApprovalAlerts 的差异：
 * - 这里面向"每日重复"，schedule.repeats=true + on.hour/minute；
 *   通知 id 用稳定 hash 但本模块自身持有一份常量 DAILY_REVIEW_ID 便于 cancelDailyReview。
 */
import { Capacitor, type PluginListenerHandle } from '@capacitor/core'
import { LocalNotifications } from '@capacitor/local-notifications'
import type { PermissionState } from '@capacitor/core'

/** 每日复习通知固定 id（int32）；与 useApprovalAlerts 的 hash 命名空间隔离。 */
export const DAILY_REVIEW_ID = 0x4f4c_4e01 // 'OLN' + 0x01 = 1330137089

export interface PermissionStatus {
  /** Whether notifications can be shown to the user. */
  display: PermissionState
  /** Android only — exact alarm privilege (SCHEDULE_EXACT_ALARM). */
  exactAlarm: PermissionState
}

export interface NotificationId {
  id: number
}

export interface DailyReviewPayload {
  id: number
  title: string
  body: string
  schedule: {
    repeats: boolean
    on: { hour: number; minute: number }
    allowWhileIdle?: boolean
  }
  isExactNotification?: boolean
  extra: { deepLink: string }
}

export interface BuildDailyReviewOptions {
  hour: number
  minute: number
  dueCount: number
  /** True if running on Android — controls isExactNotification availability. */
  onAndroid: boolean
  /** True if SCHEDULE_EXACT_ALARM permission was denied → inexact fallback. */
  exactDenied: boolean
}

export type Unsubscribe = () => void

export type AlarmDeniedReason = 'exact-denied' | 'display-denied'

type AlarmDeniedListener = (reason: AlarmDeniedReason) => void
const alarmDeniedListeners = new Set<AlarmDeniedListener>()

/**
 * Subscribe to a one-shot fire when the exact-alarm / display permission is denied
 * during a scheduleDailyReview call. VivoBatteryWhitelistGuide mounts this on its
 * root to decide whether to show the OriginOS bootstrap UX.
 *
 * Returns an unsubscribe handle. Each fire calls every listener once.
 */
export function onAlarmDenied(handler: AlarmDeniedListener): Unsubscribe {
  alarmDeniedListeners.add(handler)
  return () => { alarmDeniedListeners.delete(handler) }
}

function emitAlarmDenied(reason: AlarmDeniedReason): void {
  for (const listener of alarmDeniedListeners) {
    try { listener(reason) } catch { /* listener errors are non-fatal */ }
  }
}

function notificationIdFor(seed: string): number {
  let h = 0
  for (let i = 0; i < seed.length; i++) {
    h = (h * 31 + seed.charCodeAt(i)) | 0
  }
  return Math.abs(h) || 1
}

export { notificationIdFor }

async function probeExactAlarm(): Promise<PermissionState> {
  try {
    const r = await LocalNotifications.checkExactNotificationSetting()
    return r.exact_alarm
  } catch {
    // Plugin not available on iOS / web — treat as granted so we don't block.
    return 'granted'
  }
}

async function probeDisplay(): Promise<PermissionState> {
  try {
    const r = await LocalNotifications.checkPermissions()
    return r.display
  } catch {
    return 'denied'
  }
}

function isAndroid(): boolean {
  try { return Capacitor.getPlatform() === 'android' } catch { return false }
}

/**
 * Read the current permission state for both display and exact-alarm.
 * On non-Android or when the plugin is unavailable, exactAlarm is reported as
 * 'granted' so callers don't open the OriginOS guide on iOS / web by mistake.
 */
export async function checkPermissions(): Promise<PermissionStatus> {
  const display = await probeDisplay()
  const exactAlarm = isAndroid() ? await probeExactAlarm() : 'granted'
  return { display, exactAlarm }
}

/**
 * Request display permission (Android 13+ POST_NOTIFICATIONS + iOS UNUserNotificationCenter).
 * Returns the new status without throwing.
 */
export async function requestPermissions(): Promise<PermissionStatus> {
  try {
    await LocalNotifications.requestPermissions()
  } catch (err) {
    console.warn('[local-notifications] requestPermissions failed:', err)
  }
  return checkPermissions()
}

/**
 * Schedule a daily review reminder that fires every day at hour:minute until cancelled.
 * `dueCount` is interpolated into the body so users see their current backlog.
 *
 * Implementation notes:
 * - The notification id is a stable hash derived from hour/minute + a namespace tag
 *   so re-scheduling with the same time replaces the prior one instead of stacking.
 * - On Android, if SCHEDULE_EXACT_ALARM is denied we fall back to inexact alarm
 *   (`isExactNotification: false`, `allowWhileIdle: false`) so the reminder still
 *   arrives; we also emit `onAlarmDenied('exact-denied')` so the UI can guide the
 *   user through OriginOS / vendor-specific power-whitelisting.
 * - Non-native platforms throw — callers should feature-detect via isSupported().
 */
export async function scheduleDailyReview(
  hour: number,
  minute: number,
  dueCount: number,
): Promise<NotificationId> {
  if (!Capacitor.isNativePlatform()) {
    throw new Error('[local-notifications] scheduleDailyReview only runs on native platforms')
  }

  const status = await checkPermissions()
  if (status.display !== 'granted') {
    // Best-effort: ask once; if the user denies, surface so the UI can route to settings.
    const after = await requestPermissions()
    if (after.display !== 'granted') {
      emitAlarmDenied('display-denied')
      throw new Error('[local-notifications] notification permission not granted')
    }
  }

  const onAndroid = isAndroid()
  const exactDenied = onAndroid && status.exactAlarm !== 'granted'

  if (exactDenied) {
    console.warn(
      '[local-notifications] SCHEDULE_EXACT_ALARM denied — falling back to inexact alarm. '
      + 'The reminder may be delayed by Android Doze; consider guiding the user through '
      + 'the vendor battery whitelist (OriginOS: Settings → Battery → Background power '
      + 'consumption → Unrestricted).',
    )
    emitAlarmDenied('exact-denied')
  }

  const notification = buildDailyReviewPayload({
    hour,
    minute,
    dueCount,
    onAndroid,
    exactDenied,
  })

  await LocalNotifications.schedule({ notifications: [notification] })

  return { id: notification.id }
}

/**
 * Pure function that constructs the @capacitor/local-notifications payload for a
 * daily review reminder. Exposed for tests so they can assert the schedule shape
 * without loading the Capacitor plugin (which needs a real WebView / Android runtime).
 */
export function buildDailyReviewPayload(opts: BuildDailyReviewOptions): DailyReviewPayload {
  const { hour, minute, dueCount, onAndroid, exactDenied } = opts
  const base: DailyReviewPayload = {
    id: DAILY_REVIEW_ID,
    title: '复习时间到',
    body: `你有 ${dueCount} 张卡片待复习`,
    schedule: {
      repeats: true,
      on: { hour, minute },
    },
    extra: { deepLink: '/flashcards/review' },
  }

  if (onAndroid) {
    base.isExactNotification = !exactDenied
    if (exactDenied) {
      base.schedule.allowWhileIdle = false
    }
  }
  return base
}

/** Cancel the daily review reminder scheduled by scheduleDailyReview(). */
export async function cancelDailyReview(): Promise<void> {
  if (!Capacitor.isNativePlatform()) return
  try {
    await LocalNotifications.cancel({
      notifications: [{ id: DAILY_REVIEW_ID }],
    })
  } catch (err) {
    console.warn('[local-notifications] cancelDailyReview failed:', err)
  }
}

/**
 * Subscribe to taps on the daily review notification. The handler fires once per
 * tap; the listener is automatically removed when the returned Unsubscribe is
 * called.
 */
export async function onReviewTap(handler: () => void): Promise<Unsubscribe> {
  if (!Capacitor.isNativePlatform()) {
    return () => { /* noop on web */ }
  }

  const handle: PluginListenerHandle = await LocalNotifications.addListener(
    'localNotificationActionPerformed',
    (event) => {
      const deepLink = (event.notification?.extra as { deepLink?: string } | undefined)?.deepLink
      if (deepLink === '/flashcards/review') {
        try { handler() } catch (err) { console.warn('[local-notifications] tap handler threw:', err) }
      }
    },
  )

  return () => { void handle.remove().catch(() => { /* already removed */ }) }
}

/** Whether scheduleDailyReview / cancelDailyReview will actually do something. */
export function isSupported(): boolean {
  return Capacitor.isNativePlatform()
}