/**
 * 统一文件导出工具（审计 P1-3 收敛 + 原生导出支持）。
 *
 * - web：blob + a[download]（延迟回收，规避旧 WebView 取 blob 前引用被回收的竞态）。
 * - android：经 pocket-native 抽象写 Cache 目录 → Document 插件用 MediaStore
 *   **静默写入系统「下载」目录**。这里刻意不再用 @capacitor/share：真机实测
 *   （Redmi 2411DRN47C / WebView 126）Share.share 会拉起系统「打开方式」选择框
 *   （MiuiChooserActivity，标题就是旧代码里的「保存或分享文件」），候选只有 QQ 等，
 *   任何导出入口都被系统弹窗打断。API 29+ 无需任何运行时权限。
 * - ios：经 pocket-native 抽象写 Cache 目录 → PocketShare 调起系统分享面板
 *   （无 MediaStore 等价物，保留系统分享面板）。
 * - harmony：arkts-webview 桥暂无该能力，抛 DownloadUnsupportedError 显式失败
 *   （不产生文件是静默的，必须让用户感知）。
 *
 * 所有导出函数返回给用户看的一句话（落盘位置），调用方可直接 toast。
 *
 * Phase 9.4 增量：移除 @capacitor/filesystem + @capacitor/share 顶层直依赖，
 * 业务代码只走 getPocketNative() 抽象入口（计划文档 §4.1 候选）。Document
 * 插件（MediaStore）是 Capacitor registerPlugin 注入的独立模块，不在 Phase 9
 * 收编范围 —— 它没有跨端抽象需求（仅 Android 真后端），保留原 Capacitor 接入。
 *
 * 调用方只需捕获 DownloadUnsupportedError 与通用异常并 toast，无需感知平台差异。
 */
import { getPocketNative } from '../native/pocket-native'
import { runtimePlatform } from '../native/runtime-platform'
import { documentNative, hasNativeDocumentSupport } from '../native/document'
import {
  utf8ToBase64,
  blobToBase64,
  arrayBufferToBase64,
} from './download-encoding'

export { utf8ToBase64, blobToBase64, arrayBufferToBase64 }

export class DownloadUnsupportedError extends Error {
  constructor(message = '当前环境不支持导出文件，请在网页版使用') {
    super(message)
    this.name = 'DownloadUnsupportedError'
  }
}

export interface TextDownloadOptions {
  filename: string
  content: string
  /** 完整 MIME（含 charset，如 'text/csv;charset=utf-8'） */
  mimeType: string
}

/** 文本文件导出。CSV 的 BOM 前缀等编码细节由调用方拼入 content。 */
export async function downloadTextFile(opts: TextDownloadOptions): Promise<string> {
  const platform = runtimePlatform()

  if (platform === 'web') {
    webDownloadText(opts.content, opts.filename, opts.mimeType)
    return `已下载 ${opts.filename}`
  }

  if (platform === 'harmony') throw new DownloadUnsupportedError()

  // android：写 Cache（UTF-8 → base64）→ MediaStore 静默落盘
  // ios：写 Cache（UTF-8 → base64）→ 系统分享面板
  const base64 = utf8ToBase64(opts.content)
  await persistToCache(opts.filename, base64)
  return persistFile(opts.filename, opts.mimeType)
}

/** 二进制文件导出（Blob / ArrayBuffer，如 PDF、ZIP）。 */
export async function downloadFile(filename: string, data: Blob | ArrayBuffer, mimeType: string): Promise<string> {
  const platform = runtimePlatform()

  if (platform === 'web') {
    const blob = data instanceof Blob ? data : new Blob([data], { type: mimeType })
    webDownloadBlob(blob, filename)
    return `已下载 ${filename}`
  }

  if (platform === 'harmony') throw new DownloadUnsupportedError()

  const base64 = data instanceof Blob ? await blobToBase64(data) : arrayBufferToBase64(data)
  await persistToCache(filename, base64)
  return persistFile(filename, mimeType)
}

/**
 * 把 Cache 目录里的文件交付出去。返回给用户的一句话。
 *
 * Android 走 MediaStore 静默落盘（无系统弹窗）；其余平台保留系统分享面板。
 * 文件已经写在 Cache 里，所以用户取消分享不算失败。
 */
async function persistFile(filename: string, mimeType: string): Promise<string> {
  if (hasNativeDocumentSupport()) {
    const saved = await documentNative.saveToDownloads({ path: filename, filename, mimeType })
    return `已保存到「下载/${saved.name}」`
  }
  await shareFile(filename)
  return `已导出 ${filename}`
}

/**
 * 经 pocket-native 抽象把 base64 数据写入 Cache 目录。
 *
 * Phase 9.4 迁移要点：
 *   - 移除顶层 @capacitor/filesystem 直接 import；
 *   - 走 getPocketNative().filesystem.writeFile（path, base64, 'cache'）；
 *   - iOS stub 阶段 PocketFilesystem 抛 notImpl —— catch 后退 webDownload 兜底，
 *     保证 dev 体验（不阻塞本地 build/test）。
 *
 * 注意：Document 插件（documentNative）仍是独立 Capacitor registerPlugin 入口，
 * 不走 pocket-native —— 它只在 Android 真后端，跨端抽象无收益。
 */
async function persistToCache(filename: string, base64: string): Promise<void> {
  try {
    const native = getPocketNative()
    await native.filesystem.writeFile(filename, base64, 'cache')
  } catch (err) {
    // iOS stub 抛 notImpl / share 不可用 → webDownload 兜底
    if (err instanceof DownloadUnsupportedError) throw err
    const platform = runtimePlatform()
    if (platform === 'web') return
    webDownloadFallback(filename, base64)
  }
}

/** 调起系统分享面板（iOS 路径）。用户取消分享不视为失败（文件已生成在缓存目录）。 */
async function shareFile(filename: string): Promise<void> {
  const native = getPocketNative()
  const { uri } = await native.filesystem.getUri(filename, 'cache')
  const canShare = await native.share.canShare()
  if (!canShare) throw new Error('当前设备没有可用的分享渠道')
  await native.share.share({
    title: filename,
    text: filename,
    url: uri,
    dialogTitle: '保存或分享文件',
  })
}

/* ===== Web 路径实现（origin 同款）===== */

function webDownloadText(content: string, filename: string, mimeType: string): void {
  if (typeof document === 'undefined') {
    throw new DownloadUnsupportedError('[utils/download] document unavailable')
  }
  const blob = new Blob([content], { type: mimeType })
  webDownloadBlob(blob, filename)
}

function webDownloadBlob(blob: Blob, filename: string): void {
  if (typeof document === 'undefined') {
    throw new DownloadUnsupportedError('[utils/download] document unavailable')
  }
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 10_000)
}

/**
 * iOS stub / share 不可用时的兜底：把 base64 解码成 Blob 后走 webDownloadBlob。
 * 仅 Android/iOS 端 PocketFilesystem 抛错时被调用；web 永不进此路径。
 */
function webDownloadFallback(filename: string, base64: string): void {
  if (typeof document === 'undefined') {
    throw new DownloadUnsupportedError('[utils/download] document unavailable')
  }
  const bytes = Uint8Array.from(atob(base64), (c) => c.charCodeAt(0))
  const blob = new Blob([bytes])
  webDownloadBlob(blob, filename)
}