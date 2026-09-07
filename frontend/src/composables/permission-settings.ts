/**
 * 权限与隐私页：点击后该申请还是该打开系统设置，以及 Android 要打开哪一页。
 *
 * 已被禁止的项必须打开「对应功能」的系统设置，并用 package 定位到本应用。
 */
import {
  canRequestPermissionAgain,
  nextPermissionAction,
  type PermissionAction,
  type PermissionStatus,
} from './permission-action.ts'

export type SettingsPermissionName =
  | 'microphone'
  | 'notifications'
  | 'camera'
  | 'photos'
  | 'biometric'

export type PermissionRowStatus = PermissionStatus | 'unknown' | 'placeholder'

export function permissionStatusLabel(status: PermissionRowStatus, canRequestAgain: boolean): string {
  if (status === 'granted') return '已授权'
  if (status === 'unavailable') return '不支持'
  if (status === 'unknown' || status === 'placeholder') return status === 'placeholder' ? '即将支持' : '未检测'
  return canRequestAgain ? '未授权' : '已拒绝'
}

export function permissionStatusClass(status: PermissionRowStatus): string {
  if (status === 'granted') return 'ok'
  if (status === 'unavailable' || status === 'unknown') return 'muted'
  if (status === 'placeholder') return 'placeholder'
  return 'warn'
}

export interface PermissionRowClickInput {
  kind: SettingsPermissionName
  status: PermissionRowStatus
  canRequestAgain?: boolean
  afterRequest?: boolean
}

export interface AndroidSettingsIntent {
  action: string
  data?: string
  extras: Record<string, string>
}

const PACKAGE_EXTRA = 'android.intent.extra.PACKAGE_NAME'
const GROUP_EXTRA = 'android.intent.extra.PERMISSION_GROUP_NAME'
const APP_PACKAGE_EXTRA = 'android.provider.extra.APP_PACKAGE'

const PERMISSION_GROUPS: Partial<Record<SettingsPermissionName, string>> = {
  microphone: 'android.permission-group.MICROPHONE',
  camera: 'android.permission-group.CAMERA',
  photos: 'android.permission-group.READ_MEDIA_VISUAL',
  notifications: 'android.permission-group.NOTIFICATIONS',
}

export function permissionRowClickAction(input: PermissionRowClickInput): PermissionAction {
  if (input.kind === 'biometric') {
    if (input.status === 'unavailable') return 'open-settings'
    if (input.status === 'granted') return 'none'
    return 'request'
  }

  if (input.status === 'placeholder') return 'open-settings'
  if (input.status === 'unknown') return 'request'

  const canRequest = input.canRequestAgain ?? canRequestPermissionAgain(input.status)
  if (input.status === 'granted') return 'none'
  if (input.status === 'unavailable') return 'none'
  if (canRequest && !input.afterRequest) return 'request'
  if (canRequest && input.afterRequest) return 'none'
  return nextPermissionAction(input.status)
}

function appDetails(packageName: string): AndroidSettingsIntent {
  return {
    action: 'android.settings.APPLICATION_DETAILS_SETTINGS',
    data: `package:${packageName}`,
    extras: {},
  }
}

function manageAppPermission(packageName: string, group: string): AndroidSettingsIntent {
  return {
    action: 'android.intent.action.MANAGE_APP_PERMISSION',
    extras: {
      [PACKAGE_EXTRA]: packageName,
      [GROUP_EXTRA]: group,
    },
  }
}

export function androidSettingsPlan(
  name: SettingsPermissionName,
  packageName: string,
): AndroidSettingsIntent[] {
  const plan: AndroidSettingsIntent[] = []
  const group = PERMISSION_GROUPS[name]

  if (name === 'notifications') {
    plan.push({
      action: 'android.settings.APP_NOTIFICATION_SETTINGS',
      extras: { [APP_PACKAGE_EXTRA]: packageName },
    })
  } else if (name === 'biometric') {
    plan.push({ action: 'android.settings.FINGERPRINT_ENROLL', extras: {} })
    plan.push({ action: 'android.settings.SECURITY_SETTINGS', extras: {} })
  } else if (group) {
    plan.push(manageAppPermission(packageName, group))
  }

  plan.push({
    action: 'android.intent.action.MANAGE_APP_PERMISSIONS',
    extras: { [PACKAGE_EXTRA]: packageName },
  })
  plan.push(appDetails(packageName))
  return plan
}
