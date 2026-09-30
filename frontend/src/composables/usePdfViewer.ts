/**
 * 应用内置 PDF 预览（Android 原生 PdfRenderer 逐页栅格化）。
 *
 * 为什么需要它：Android WebView 内核不渲染 PDF。真机实测（Redmi 2411DRN47C /
 * WebView 126.0.6478.71）把 PDF 塞进 <iframe src="blob:..."> 得到的是全白区域，
 * 用户看到的就是「点了没反应」。这里改为：把字节落到 Cache 目录 → 原生 PdfRenderer
 * 渲染成 PNG → 前端用 <img> 展示，并支持翻页。
 *
 * web / iOS 上 PdfRenderer 不存在，supported=false，由调用方回退到 <iframe>。
 */
import { ref, type Ref } from 'vue'
import { Filesystem, Directory } from '@capacitor/filesystem'
import { documentNative, hasNativeDocumentSupport, renderWidthForViewport } from '../native/document'
import { useApiError } from './useApiError'

/** 单个预览最多渲染多少页，防止异常大 PDF 把内存/桥接打满。 */
const MAX_PAGES = 50

export interface PdfViewer {
  supported: boolean
  /** 挂在可滚动容器上，用于按实际宽度决定栅格化分辨率 */
  container: Ref<HTMLElement | null>
  pageCount: Ref<number>
  page: Ref<number>
  image: Ref<string>
  loading: Ref<boolean>
  error: Ref<string>
  open(blob: Blob | null, key: string): Promise<void>
  goTo(index: number): Promise<void>
  next(): Promise<void>
  prev(): Promise<void>
  reset(): void
}

async function blobToBase64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onloadend = () => {
      const dataUrl = reader.result as string
      const base64 = dataUrl.split(',')[1] ?? ''
      if (!base64) { reject(new Error('文件编码失败')); return }
      resolve(base64)
    }
    reader.onerror = () => reject(reader.error ?? new Error('文件读取失败'))
    reader.readAsDataURL(blob)
  })
}

export function usePdfViewer(): PdfViewer {
  const supported = hasNativeDocumentSupport()
  const container = ref<HTMLElement | null>(null)
  const pageCount = ref(0)
  const page = ref(0)
  const image = ref('')
  const loading = ref(false)
  const error = ref('')
  // 原生 PdfRenderer / Filesystem 抛回来的 message 是给开发看的（路径、权限、桥接细节），
  // 直接赋给 error 就等于把它上屏。归一成人话，原始信息留档到 console。
  const apiError = useApiError()

  /** 当前写入 Cache 的文件名；同一份文档重复打开时覆盖，避免 Cache 无限增长。 */
  let cacheName = ''
  /** 打开过程中的自增令牌：快速翻页时丢弃过期的渲染结果。 */
  let token = 0

  function measureWidth(): number {
    const el = container.value
    const cssWidth = el && el.clientWidth > 0 ? el.clientWidth : (typeof window !== 'undefined' ? window.innerWidth : 360)
    return renderWidthForViewport(cssWidth)
  }

  async function render(index: number, myToken: number): Promise<void> {
    if (!cacheName || myToken !== token) return
    loading.value = true
    try {
      const res = await documentNative.renderPdfPage({
        path: cacheName,
        page: index,
        width: measureWidth(),
      })
      if (myToken !== token) return
      pageCount.value = res.pageCount
      page.value = res.page
      image.value = res.image
      error.value = ''
    } catch (e) {
      if (myToken !== token) return
      console.warn('[pdf-viewer] render failed:', e)
      error.value = apiError(e, 'PDF 渲染失败')
      image.value = ''
    } finally {
      if (myToken === token) loading.value = false
    }
  }

  async function open(blob: Blob | null, key: string): Promise<void> {
    token += 1
    const myToken = token
    reset()
    if (!blob || !supported) return

    loading.value = true
    try {
      cacheName = `preview-${key.replace(/[^a-zA-Z0-9._-]/g, '_')}.pdf`
      await Filesystem.writeFile({
        path: cacheName,
        data: await blobToBase64(blob),
        directory: Directory.Cache,
      })
      if (myToken !== token) return
      const info = await documentNative.pdfInfo({ path: cacheName })
      if (myToken !== token) return
      pageCount.value = Math.min(info.pageCount, MAX_PAGES)
      await render(0, myToken)
    } catch (e) {
      if (myToken !== token) return
      console.warn('[pdf-viewer] open failed:', e)
      error.value = apiError(e, 'PDF 打开失败')
      pageCount.value = 0
    } finally {
      if (myToken === token) loading.value = false
    }
  }

  async function goTo(index: number): Promise<void> {
    if (!supported || !cacheName) return
    const clamped = Math.max(0, Math.min(pageCount.value - 1, index))
    if (clamped === page.value && image.value) return
    token += 1
    await render(clamped, token)
  }

  function reset(): void {
    pageCount.value = 0
    page.value = 0
    image.value = ''
    error.value = ''
    loading.value = false
    cacheName = ''
  }

  return {
    supported,
    container,
    pageCount,
    page,
    image,
    loading,
    error,
    open,
    goTo,
    next: () => goTo(page.value + 1),
    prev: () => goTo(page.value - 1),
    reset,
  }
}
