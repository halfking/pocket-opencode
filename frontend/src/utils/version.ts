// 应用版本配置
export const APP_VERSION = {
  version: '1.2.0',
  buildNumber: 2,
  buildDate: '2026-06-29',
  name: 'OpenCode Pocket Mobile'
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
      platform: 'android',
      deviceModel: navigator.userAgent
    })
  })

  if (!response.ok) {
    throw new Error('Failed to check update')
  }

  return response.json()
}

// 下载 APK
export function downloadAPK(url: string) {
  window.open(url, '_blank')
}

// 格式化文件大小
export function formatFileSize(bytes: number): string {
  if (bytes < 1024) return bytes + ' B'
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB'
  return (bytes / (1024 * 1024)).toFixed(1) + ' MB'
}
