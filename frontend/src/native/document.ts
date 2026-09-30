/**
 * Document 插件的 TS 绑定（Android 原生：PdfRenderer 预览 + MediaStore 落盘）。
 *
 * 为什么不走 <iframe>：Android WebView 内核不渲染 PDF，实测（Redmi 2411DRN47C /
 * WebView 126.0.6478.71）<iframe src="blob:...pdf"> 是全白，见
 * docs/handoff 的真机取证。所以预览由原生 PdfRenderer 逐页栅格化成 PNG，
 * 前端用 <img> 展示。
 *
 * 为什么不用 Share 导出：@capacitor/share 会拉起系统「打开方式」选择框
 * （真机上为 MiuiChooserActivity，标题即旧的「保存或分享文件」），任何导出入口
 * 都被系统弹窗打断。改为 saveToDownloads 静默写入系统「下载」目录。
 *
 * BUG-G（2026-09-30）注意：Capacitor 的 registerPlugin() 返回的是**带 .then 的
 * thenable 代理**，不能从 async 函数直接 return，也不能作为 .then() 回调的返回值，
 * 否则会抛 "Document.then() is not implemented on android" 并让降级路径失效。
 * 一律用非 thenable 盒子 { value } 装载，async 只返回盒子。
 */
// 显式带 .ts 后缀：tsconfig 开了 allowImportingTsExtensions，且 node --test 的 ESM
// 解析器不认无后缀的相对导入，带后缀才能让 __tests__/document.test.mjs 直接测本模块。
import { runtimePlatform } from './runtime-platform.ts'

export interface PdfInfo {
  pageCount: number
}

export interface RenderedPdfPage {
  /** 0 基页码 */
  page: number
  pageCount: number
  /** 栅格化后的像素宽高 */
  width: number
  height: number
  /** data:image/png;base64,... */
  image: string
}

export interface SavedDownload {
  name: string
  uri: string
  bytes: number
  location: string
}

export interface CapDocumentPlugin {
  pdfInfo(opts: { path: string }): Promise<PdfInfo>
  renderPdfPage(opts: { path: string; page: number; width: number }): Promise<RenderedPdfPage>
  saveToDownloads(opts: { path: string; filename: string; mimeType: string }): Promise<SavedDownload>
}

type DocumentBox = { value: CapDocumentPlugin }

class StubDocument implements CapDocumentPlugin {
  private unsupported = () => Promise.reject(new Error('Document plugin is not available on this platform'))
  pdfInfo = () => this.unsupported()
  renderPdfPage = () => this.unsupported()
  saveToDownloads = () => this.unsupported()
}

let _impl: DocumentBox | null = null
async function load(): Promise<DocumentBox> {
  if (_impl) return _impl
  let impl: CapDocumentPlugin
  try {
    const cap = await import('@capacitor/core')
    impl = (cap.registerPlugin as <T>(name: string) => T)('Document') as CapDocumentPlugin
  } catch {
    impl = new StubDocument()
  }
  _impl = { value: impl }
  return _impl
}

export const documentNative: CapDocumentPlugin = new Proxy(
  {} as CapDocumentPlugin,
  {
    get(_t, prop: keyof CapDocumentPlugin) {
      // box.value 而非 box：代理本身是 thenable，直接透传会再次触发 .then()
      return (...args: unknown[]) =>
        load().then((box) => (box.value[prop] as Function)(...args))
    },
  },
)

/**
 * 内置文档能力只在 Android 原生壳可用。
 * web / iOS / harmony 都没有 PdfRenderer 与 MediaStore 落盘实现，调用方需自行回退。
 */
export function hasNativeDocumentSupport(): boolean {
  return runtimePlatform() === 'android'
}

/** 栅格化宽度：按设备像素给足清晰度，再由插件侧夹到安全区间。 */
export function renderWidthForViewport(cssWidth: number): number {
  const dpr = typeof window !== 'undefined' ? Math.min(window.devicePixelRatio || 1, 3) : 2
  return Math.round(Math.max(320, Math.min(2400, cssWidth * dpr)))
}
