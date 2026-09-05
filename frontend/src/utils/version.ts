import { runtimePlatform } from '../native/runtime-platform'
import { assertNotHTML } from '../api/jsonGuard'

// 应用版本配置
export const APP_VERSION = {
  version: '1.2.0',
  buildNumber: 2,
  buildDate: '2026-06-29',
  name: 'Redclaw Mobile'
}

// 版本信息接口
export interface VersionInfo {
  version: string
  buildNumber: number
  downloadUrl: string
  fileSize: number
  changelog: string[]
  forceUpdate: boolean
  releaseDate: string
}

// 检查更新响应
export interface CheckUpdateResponse {
  hasUpdate: boolean
  latest?: VersionInfo
  forceUpdate: boolean
  message: string
}

// 检查更新
export async function checkUpdate(): Promise<CheckUpdateResponse> {
  const API_BASE = import.meta.env.VITE_API_BASE || ''

  const response = await fetch(`${API_BASE}/api/app/check-update`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json'
    },
    body: JSON.stringify({
      currentVersion: APP_VERSION.version,
      currentBuild: APP_VERSION.buildNumber,
      platform: runtimePlatform(),
      deviceModel: navigator.userAgent
    })
  })

  if (!response.ok) {
    throw new Error('Failed to check update')
  }

  // 裸 fetch 也可能拿到 HTML（移动端漏注入 API base 时 Capacitor 返回 index.html），
  // 统一经 assertNotHTML 换成可定位的错误。
  return assertNotHTML(response).json()
}

/** Only Android currently has an APK delivery channel. iOS uses App Store
 * distribution and HarmonyOS Phase A has no HAP delivery channel yet. */
export function canDownloadApk(): boolean {
  return runtimePlatform() === 'android'
}

export function downloadAPK(url: string): boolean {
  if (!canDownloadApk()) return false
  window.open(url, '_blank')
  return true
}

// 格式化文件大小
export function formatFileSize(bytes: number): string {
  if (bytes < 1024) return bytes + ' B'
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB'
  return (bytes / (1024 * 1024)).toFixed(1) + ' MB'
}
