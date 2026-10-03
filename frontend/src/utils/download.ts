/**
 * 统一文件导出工具（审计 P1-3 收敛 + 原生导出支持）。
 *
 * - web：blob + a[download]（延迟回收，规避旧 WebView 取 blob 前引用被回收的竞态）。
 * - android/ios：经 pocket-native 抽象写 Cache 目录 → 调起系统分享面板（保存到文件/发
 *   送到应用由用户选择）。Share 插件内部走 FileProvider，Cache 目录无需额外存储权限。
 * - harmony：arkts-webview 桥暂无该能力，抛 DownloadUnsupportedError 显式失败
 *   （不产生文件是静默的，必须让用户感知）。
 *
 * Phase 9.4：移除 @capacitor/filesystem / @capacitor/share 直依赖，业务代码只走
 * getPocketNative() 抽象入口（计划文档 §4.1 候选）。iOS 端 PocketFilesystem 仍是 stub，
 * 写文件/分享走 try/catch 自动退到 webDownload() 兜底，避免 dev 体验阻塞。
 *
 * 调用方只需捕获 DownloadUnsupportedError 与通用异常并 toast，无需感知平台差异。
 */
import { getPocketNative } from '../native/pocket-native.ts'
import { runtimePlatform } from '../native/runtime-platform.ts'
import {
  utf8ToBase64,
  blobToBase64,
  arrayBufferToBase64,
} from './download-encoding.ts'

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
export async function downloadTextFile(opts: TextDownloadOptions): Promise<void> {
  const platform = runtimePlatform()

  if (platform === 'web') {
    webDownload(opts.content, opts.filename, opts.mimeType)
    return
  }

  if (platform === 'harmony') throw new DownloadUnsupportedError()

  // android / ios：写缓存目录（UTF-8 文本 → base64）+ 系统分享
  const base64 = utf8ToBase64(opts.content)
  try {
    const native = getPocketNative()
    await native.filesystem.writeFile(opts.filename, base64, 'cache')
    const { uri } = await native.filesystem.getUri(opts.filename, 'cache')
    const canShare = await native.share.canShare()
    if (!canShare) throw new Error('当前设备没有可用的分享渠道')
    await native.share.share({
      title: opts.filename,
      text: opts.content.slice(0, 200),
      url: uri,
      dialogTitle: '保存或分享文件',
    })
  } catch (err) {
    // iOS stub 阶段 PocketFilesystem 抛 notImpl —— 自动退 webDownload 兜底，保证 dev 体验。
    if (err instanceof DownloadUnsupportedError) throw err
    webDownload(opts.content, opts.filename, opts.mimeType)
  }
}

/** 二进制文件导出（Blob / ArrayBuffer，如 PDF、ZIP）。 */
export async function downloadFile(filename: string, data: Blob | ArrayBuffer, mimeType: string): Promise<void> {
  const platform = runtimePlatform()

  if (platform === 'web') {
    const blob = data instanceof Blob ? data : new Blob([data], { type: mimeType })
    webDownloadBlob(blob, filename)
    return
  }

  if (platform === 'harmony') throw new DownloadUnsupportedError()

  const base64 = data instanceof Blob ? await blobToBase64(data) : arrayBufferToBase64(data)
  try {
    const native = getPocketNative()
    await native.filesystem.writeFile(filename, base64, 'cache')
    const { uri } = await native.filesystem.getUri(filename, 'cache')
    const canShare = await native.share.canShare()
    if (!canShare) throw new Error('当前设备没有可用的分享渠道')
    await native.share.share({
      title: filename,
      text: filename,
      url: uri,
      dialogTitle: '保存或分享文件',
    })
  } catch (err) {
    // iOS stub / share 不可用 → 退 webDownload 兜底（PDF / ZIP 走 data URL 触发保存）。
    if (err instanceof DownloadUnsupportedError) throw err
    const blob = data instanceof Blob ? data : new Blob([data], { type: mimeType })
    webDownloadBlob(blob, filename)
  }
}

/* ===== Web 路径实现 ===== */

function webDownload(content: string, filename: string, mimeType: string): void {
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

/* ===== 编码 helpers 已抽到 ./download-encoding.ts（Phase 9.4 单测覆盖） ===== */