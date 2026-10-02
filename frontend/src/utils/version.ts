import { App } from '@capacitor/app'
// 这三个 import 显式带 .ts 扩展名：tsconfig 已开 allowImportingTsExtensions，
// Vite 也能解析。带扩展名是为了让 node --test 能直接 import 本文件跑行为用例
// （Node 原生 ESM 不做无扩展名解析）。仓库里 invoice-totals-wire-keys.test.mjs
// 已经是这个路子。
import { runtimePlatform } from '../native/runtime-platform.ts'
import { assertNotHTML } from '../api/jsonGuard.ts'
import { resolveApiBase } from '../config/api-base.ts'

// 应用版本配置
// 这是**兜底常量**，不是设备身份的唯一真相来源。真实身份请用 resolveAppVersion()。
// 原因：APP_VERSION 写死在 TS 里，只有改代码 + 重新构建才会变；而 gradle 里的
// versionName / versionCode 才是真正打进 APK 的那个数。两者会漂移——
// 曾经漂到界面显示「Build 2 / 2026-06-29」而设备上跑的其实是 gradle 的
// versionCode 3，于是「设备上装的是旧包」这件事**从界面上完全看不出来**，
// 整整一轮验收在测过时产物（docs/handoff §4.74.2）。原生才是真相。
export const APP_VERSION = {
  version: '1.2.0',
  buildNumber: 2,
  buildDate: '2026-06-29',
  name: 'Redclaw Mobile'
}

export interface ResolvedAppVersion {
  version: string
  buildNumber: number
  buildDate: string
  name: string
  /** true = 来自原生 BuildConfig（可信）；false = 回退到 APP_VERSION 常量。 */
  fromNative: boolean
}

let resolvedVersionCache: ResolvedAppVersion | null = null

// vite 在编译期用真实构建时刻替换它（见 vite.config.ts 的 define）。
// node --test 下它不存在，所以必须用 typeof 守卫，而不是直接引用 ——
// 否则判据会在 import 阶段就 ReferenceError，那不是「测试失败」而是「跑不起来」。
declare const __BUILD_TIME__: string

/** 本次构建的时刻，形如 2026-10-03 02:07:41 UTC+08:00。
 *  未经过 vite 构建（node 测试、vitest）时为 null。 */
export function buildTimestamp(): string | null {
  try {
    if (typeof __BUILD_TIME__ === 'string' && __BUILD_TIME__.trim() !== '') {
      return __BUILD_TIME__.trim()
    }
  } catch {
    // 未声明的标识符在严格 ESM 下会抛 ReferenceError，兜住即可。
  }
  return null
}

/** 显示用的构建时刻：有编译期时间戳就用它，否则退回常量里的旧日期。 */
export function displayBuildDate(): string {
  return buildTimestamp() ?? APP_VERSION.buildDate
}

/**
 * 解析设备上**真正安装的**版本身份。
 *
 * 原生平台（Android/iOS）走 Capacitor App.getInfo()，拿到的是打进 APK 的
 * versionName / versionCode；任何一步失败（非原生、插件缺失、返回值异常）
 * 都回退到 APP_VERSION 常量，绝不抛错——这是展示/上报路径，不该因为读不到
 * 版本号就让页面崩。结果只算一次并缓存：版本号在一进程内不会变。
 */
export async function resolveAppVersion(): Promise<ResolvedAppVersion> {
  if (resolvedVersionCache) return resolvedVersionCache
  resolvedVersionCache = await loadAppVersion()
  return resolvedVersionCache
}

async function loadAppVersion(): Promise<ResolvedAppVersion> {
  const fallback: ResolvedAppVersion = { ...APP_VERSION, buildDate: displayBuildDate(), fromNative: false }
  if (nativeInfoProviderOverride) {
    try {
      return normalise(await nativeInfoProviderOverride(), fallback)
    } catch {
      return fallback
    }
  }
  if (runtimePlatform() === 'web') return fallback
  try {
    return normalise(await App.getInfo(), fallback)
  } catch {
    return fallback
  }
}

function normalise(
  info: { version?: unknown; build?: unknown },
  fallback: ResolvedAppVersion
): ResolvedAppVersion {
  const version = typeof info?.version === 'string' && info.version.trim() !== ''
    ? info.version.trim()
    : fallback.version
  const build = Number.parseInt(String(info?.build), 10)
  const buildNumber = Number.isFinite(build) && build > 0 ? build : fallback.buildNumber
  return { version, buildNumber, buildDate: fallback.buildDate, name: fallback.name, fromNative: true }
}

/** 仅供测试：把「读原生」这一步换成可控的假实现。
 *
 * 为什么需要它：resolveAppVersion 在测试环境里 runtimePlatform() 恒为 'web'，
 * 于是**永远走回退分支**。没有这个把手，判据只能验证「回退后等于常量」，
 * 而「拿到原生值时用的是不是它」这一半就没人守——把 loadAppVersion 整个
 * 改成恒返回常量，判据照样全绿。装饰性护栏比没有更糟。
 *
 * 传 null 恢复正常行为。生产代码不调用它。
 */
export function __setNativeInfoProviderForTest(
  provider: (() => Promise<{ version?: unknown; build?: unknown }>) | null
): void {
  nativeInfoProviderOverride = provider
  resolvedVersionCache = null
}

let nativeInfoProviderOverride: (() => Promise<{ version?: unknown; build?: unknown }>) | null = null

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
