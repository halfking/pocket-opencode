/**
 * 统一文件导出工具（审计 P1-3 收敛）。
 *
 * 原生 WebView（android/ios/harmony）不支持 blob + a[download]：点击后不产生任何
 * 文件，却不会有下载失败事件——此前各视图各写一份导致「假成功」提示。所有文件
 * 导出必须走这里，原生端明确抛 DownloadUnsupportedError，由调用方提示用户。
 *
 * 完整原生导出（@capacitor/filesystem 写文档目录 + Share 面板）属新依赖，
 * 待决策后在 downloadTextFile 的原生分支扩展，调用方无需改动。
 */
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

/** web 端走 a[download]；原生端抛 DownloadUnsupportedError（不产生文件是静默的，
 *  必须显式失败让用户感知）。CSV 的 BOM 前缀等编码细节由调用方拼入 content。 */
export async function downloadTextFile(opts: TextDownloadOptions): Promise<void> {
  if (runtimePlatform() !== 'web') throw new DownloadUnsupportedError()

  const blob = new Blob([opts.content], { type: opts.mimeType })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = opts.filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  // 延迟回收，规避旧 WebView 取 blob 前引用被回收的竞态
  setTimeout(() => URL.revokeObjectURL(url), 10_000)
}
