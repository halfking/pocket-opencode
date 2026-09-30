/**
 * 发票卡片缩略图（2026-10-01 真机审计 P0：标题图必须是 PDF 内容的缩略图）。
 *
 * 需求要求「标题图是发票内容 pdf 的缩略图」。原先只有一条取图路径：
 * `GET /api/emails/invoices/{id}/thumb` → 后端 `ExtractInvoiceThumb`，而它对 PDF
 * 只能抽出**内嵌位图**。国内增值税电子发票大多是「矢量文字 + 版式」PDF，根本不含
 * 位图，于是后端返回 404，卡片左侧长期是一个灰色文档图标——正是「点标题图看不到
 * 发票长什么样」的根因。实测：
 *   - 云服务开票中心-1280.00.pdf（1537B，文字型）→ thumbOK=false
 *   - 杭州创客家-3500.00.pdf（157KB，扫描件）    → thumbOK=true
 *
 * 这里补一条真正的**页面栅格化**路径：拿 PDF 字节 → 原生 PdfRenderer 渲染第 1 页
 * → data URI。原生渲染对文字型和扫描件都成立，因此缩略图与点开后的第一页
 * **完全一致**（这正是需求要的「PDF 的缩略图」）。
 *
 * 策略：先并发请求后端 thumb（命中就是零延迟，且服务端可缓存）；
 * 没命中再走原生渲染。这样扫描件发票不受影响，文字型发票被补齐。
 */
import { ref, type Ref } from 'vue'
import { Directory, Filesystem } from '@capacitor/filesystem'
import { emailApi } from '../../api/email'
import { documentNative, hasNativeDocumentSupport, renderWidthForViewport } from '../../native/document.ts'
import { planThumbStrategy, thumbCacheFileName } from './invoice-thumb-plan.ts'

/** 卡片缩略图槽位约 64px CSS 宽；给足 DPR 让放大看也清晰。 */
const THUMB_CSS_WIDTH = 64
/** 同时渲染的发票数上限，避免一屏 20 张卡时把 PdfRenderer 压垮。 */
const MAX_CONCURRENT = 2

export interface InvoiceThumbSource {
  /** invoice.id */
  id: string
  /** 发票文件名，用来判断是不是 PDF。 */
  fileName?: string
}

async function blobToBase64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onloadend = () => {
      const base64 = String(reader.result || '').split(',')[1] ?? ''
      if (!base64) reject(new Error('文件编码失败'))
      else resolve(base64)
    }
    reader.onerror = () => reject(reader.error ?? new Error('文件读取失败'))
    reader.readAsDataURL(blob)
  })
}

/**
 * 取单张发票的缩略图。
 *
 * 顺序：后端 thumb（便宜、可能命中）→ 原生渲染第 1 页（权威、与预览一致）。
 * 两条都失败返回 ''，调用方显示文档图标。
 */
export async function loadInvoiceThumb(
  inv: InvoiceThumbSource,
  options: { width?: number } = {},
): Promise<string> {
  // 1) 先试后端 thumb：图片类发票直接命中，扫描件 PDF 也命中。
  try {
    const blob = await emailApi.fetchInvoiceThumb(inv.id)
    return URL.createObjectURL(blob)
  } catch {
    // 404 = 后端抽不出位图，继续走原生渲染。
  }

  // 2) 原生 PdfRenderer 渲染第 1 页（仅文字型 PDF 走这里，决策见 invoice-thumb-plan）。
  if (planThumbStrategy(inv.fileName, hasNativeDocumentSupport()) !== 'native-render') return ''

  const cacheName = thumbCacheFileName(inv.id)
  try {
    const file = await emailApi.fetchInvoiceFile(inv.id)
    await Filesystem.writeFile({
      path: cacheName,
      data: await blobToBase64(file),
      directory: Directory.Cache,
    })
    const rendered = await documentNative.renderPdfPage({
      path: cacheName,
      page: 0,
      width: renderWidthForViewport(options.width ?? THUMB_CSS_WIDTH),
    })
    return rendered.image || ''
  } catch {
    return ''
  }
}

/**
 * 批量加载并按 id 缓存。
 *
 * 只对「还没有缩略图」的条目发请求，翻页/筛选复用已有结果；
 * 失败不阻塞列表（单张拿不到就显示图标）。
 */
export function useInvoiceThumbs() {
  const thumbs = ref<Record<string, string>>({})
  /**
   * 正在取图的发票 id。
   *
   * 单独用 Set 而不是靠「thumbs[id] 为空」判断：空值既可能是「还在取」，
   * 也可能是「取完了确实没有图」（例如无内嵌位图又不支持原生渲染）。
   * 不区分的话卡片会永远转圈。
   */
  const loading = ref<Set<string>>(new Set())

  function markLoading(ids: string[]): void {
    const next = new Set(loading.value)
    for (const id of ids) next.add(id)
    loading.value = next
  }

  function clearLoading(ids: string[]): void {
    const next = new Set(loading.value)
    for (const id of ids) next.delete(id)
    loading.value = next
  }

  function revokeAll(): void {
    for (const url of Object.values(thumbs.value)) {
      // data: URI 没有 revokeObjectURL 的必要，但 blob: 有；统一调用是安全的。
      if (url.startsWith('blob:')) URL.revokeObjectURL(url)
    }
    thumbs.value = {}
  }

  function applyRemaps(remaps: Array<{ localId: string; serverId: string }>): void {
    if (!remaps.length) return
    const next: Record<string, string> = {}
    for (const [id, url] of Object.entries(thumbs.value)) {
      const remap = remaps.find((r) => r.localId === id)
      next[remap ? remap.serverId : id] = url
    }
    thumbs.value = next
  }

  async function loadThumbs(list: InvoiceThumbSource[]): Promise<void> {
    // 空字符串是「已尝试且没有图」的终态，不该每翻一页就重试一轮。
    const pending = list.filter((inv) => inv.id && !(inv.id in thumbs.value))
    if (!pending.length) return

    markLoading(pending.map((i) => i.id))
    let cursor = 0
    const worker = async () => {
      for (;;) {
        const i = cursor++
        if (i >= pending.length) return
        const inv = pending[i]
        const url = await loadInvoiceThumb(inv)
        const next = { ...thumbs.value }
        // 失败也记空串，标记为终态，避免对必然失败的条目反复重试。
        next[inv.id] = url
        thumbs.value = next
        const still = new Set(loading.value)
        still.delete(inv.id)
        loading.value = still
      }
    }
    await Promise.all(Array.from({ length: Math.min(MAX_CONCURRENT, pending.length) }, worker))
  }

  function thumbFor(id: string): string {
    return thumbs.value[id] || ''
  }

  function isLoading(id: string): boolean {
    return loading.value.has(id)
  }

  return { thumbs, loading, thumbFor, isLoading, loadThumbs, revokeAll, applyRemaps }
}
