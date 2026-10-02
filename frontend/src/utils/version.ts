import { runtimePlatform } from '../native/runtime-platform'
import { assertNotHTML } from '../api/jsonGuard'
import { resolveApiBase } from '../config/api-base'

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

/** 服务端把「版本配置文件找不到」翻成 503 并带一个可识别的 error 码。
 * 它不是「稍后重试会好」的那类故障——重试一万次也是同样结果，
 * 所以必须和普通失败区分开，否则用户会一直等一个不会来的重试。 */
export const VERSION_CONFIG_UNAVAILABLE = 'version_config_not_found'

export class VersionConfigUnavailableError extends Error {
  readonly detail?: string
  constructor(detail?: string) {
    super('server version config unavailable')
    this.name = 'VersionConfigUnavailableError'
    this.detail = detail
  }
}

// 检查更新
export async function checkUpdate(): Promise<CheckUpdateResponse> {
  const response = await fetch(`${resolveApiBase()}/api/app/check-update`, {
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
    // 先探 error 码再抛：503 带诊断体，而普通 500 是纯文本，
    // 直接 throw 会把这两种性质完全不同的故障压成同一个 'Failed to check update'。
    const code = await readErrorCode(response)
    if (code === VERSION_CONFIG_UNAVAILABLE) {
      throw new VersionConfigUnavailableError(await readErrorDetail(response))
    }
    throw new Error('Failed to check update')
  }

  // 裸 fetch 也可能拿到 HTML（移动端漏注入 API base 时 Capacitor 返回 index.html），
  // 统一经 assertNotHTML 换成可定位的错误。
  return assertNotHTML(response).json()
}

async function readErrorCode(response: Response): Promise<string | undefined> {
  try {
    const body = await response.clone().json()
    return typeof body?.error === 'string' ? body.error : undefined
  } catch {
    // 非 JSON 错误体（http.Error 的纯文本）不是异常路径，走通用失败分支。
    return undefined
  }
}

async function readErrorDetail(response: Response): Promise<string | undefined> {
  try {
    const body = await response.json()
    return typeof body?.detail === 'string' ? body.detail : undefined
  } catch {
    return undefined
  }
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
