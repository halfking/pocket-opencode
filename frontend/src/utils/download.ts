/**
 * 统一文件导出工具（审计 P1-3 收敛 + 原生导出支持）。
 *
 * - web：blob + a[download]（延迟回收，规避旧 WebView 取 blob 前引用被回收的竞态）。
 * - android/ios：@capacitor/filesystem 写 Cache 目录 → @capacitor/share 调起系统
 *   分享面板（保存到文件/发送到应用由用户选择）。Share 插件内部走 FileProvider，
 *   Cache 目录无需额外存储权限。
 * - harmony：arkts-webview 桥暂无该能力，抛 DownloadUnsupportedError 显式失败
 *   （不产生文件是静默的，必须让用户感知）。
 *
 * 调用方只需捕获 DownloadUnsupportedError 与通用异常并 toast，无需感知平台差异。
 */
import { Filesystem, Directory, Encoding } from '@capacitor/filesystem'
import { Share } from '@capacitor/share'
import { runtimePlatform } from '../native/runtime-platform'

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
    const blob = new Blob([opts.content], { type: opts.mimeType })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = opts.filename
    document.body.appendChild(a)
    a.click()
    a.remove()
    setTimeout(() => URL.revokeObjectURL(url), 10_000)
    return
  }

  if (platform === 'harmony') throw new DownloadUnsupportedError()

  // android / ios：写缓存目录（UTF-8 文本，无需 base64）+ 系统分享
  const result = await Filesystem.writeFile({
    path: opts.filename,
    data: opts.content,
    directory: Directory.Cache,
    encoding: Encoding.UTF8,
    recursive: true,
  })
  await shareFile(opts.filename, result.uri)
}

/** 二进制文件导出（Blob / ArrayBuffer，如 PDF、ZIP）。 */
export async function downloadFile(filename: string, data: Blob | ArrayBuffer, mimeType: string): Promise<void> {
  const platform = runtimePlatform()

  if (platform === 'web') {
    const blob = data instanceof Blob ? data : new Blob([data], { type: mimeType })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = filename
    document.body.appendChild(a)
    a.click()
    a.remove()
    setTimeout(() => URL.revokeObjectURL(url), 10_000)
    return
  }

  if (platform === 'harmony') throw new DownloadUnsupportedError()

  const base64 = data instanceof Blob ? await blobToBase64(data) : arrayBufferToBase64(data)
  const result = await Filesystem.writeFile({
    path: filename,
    data: base64,
    directory: Directory.Cache,
    recursive: true,
  })
  await shareFile(filename, result.uri)
}

/** 调起系统分享面板。用户取消分享不视为失败（文件已生成在缓存目录）。 */
async function shareFile(filename: string, uri: string): Promise<void> {
  const canShare = await Share.canShare()
  if (!canShare.value) throw new Error('当前设备没有可用的分享渠道')
  await Share.share({
    title: filename,
    url: uri,
    dialogTitle: '保存或分享文件',
  })
}

function blobToBase64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onloadend = () => {
      const dataUrl = reader.result as string
      // 去除 data:...;base64, 前缀，@capacitor/filesystem 只接受纯 base64
      const base64 = dataUrl.split(',')[1] ?? ''
      if (!base64) {
        reject(new Error('文件编码失败'))
        return
      }
      resolve(base64)
    }
    reader.onerror = () => reject(reader.error ?? new Error('文件读取失败'))
    reader.readAsDataURL(blob)
  })
}

function arrayBufferToBase64(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer)
  let binary = ''
  const chunk = 0x8000 // 分块拼接，避免 String.fromCharCode 超参
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk))
  }
  return btoa(binary)
}
