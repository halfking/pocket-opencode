/**
 * 发票缩略图取图策略（纯逻辑，无 Vue / Capacitor / 网络依赖）。
 *
 * 单独成文件是为了能直接 `node --test` 跑——真正的取图逻辑在
 * use-invoice-thumbs.ts 里，它 import 了 vue 与 @capacitor/filesystem，
 * 在 node 下加载不了。策略决策本身不需要这些依赖，拆出来即可测。
 *
 * 背景（2026-10-01 真机审计 P0）：需求要求「标题图是发票内容 pdf 的缩略图」。
 * 原先缩略图只有一条来源：后端 /thumb → ExtractInvoiceThumb，而它对 PDF 只能
 * 抽**内嵌位图**。增值税电子发票多为「矢量文字 + 版式」PDF，根本不含位图，
 * 后端直接 404，卡片左侧长期是灰色文档图标——这就是「点标题图看不到发票
 * 长什么样」的根因。实测文字型发票（1537B）抽不出图，扫描件（157KB）可以。
 */

/** 取图策略：走哪条路径拿缩略图。 */
export type ThumbStrategy = 'server-thumb' | 'native-render'

export function isPdfName(fileName?: string): boolean {
  return /\.pdf$/i.test(fileName || '')
}

/**
 * 决定一张发票的缩略图怎么取。
 *
 * - 图片类附件：后端 /thumb 原样返回即可，零延迟。
 * - 文字型 PDF：后端抽不出位图，**必须**用原生 PdfRenderer 渲染第 1 页。
 *   这是需求「标题图是 PDF 内容缩略图」唯一能成立的地方，也是缩略图与
 *   点开后第一页能保持一致的前提。
 * - 非 PDF / 无文件名：只走后端；拿不到就由卡片显示文档图标。
 */
export function planThumbStrategy(
  fileName: string | undefined,
  nativeSupported: boolean,
): ThumbStrategy {
  if (!isPdfName(fileName)) return 'server-thumb'
  return nativeSupported ? 'native-render' : 'server-thumb'
}

/** Cache 目录里的稳定文件名；同一张发票重复渲染时覆盖，不增长。 */
export function thumbCacheFileName(id: string): string {
  return `thumb-${id.replace(/[^a-zA-Z0-9._-]/g, '_')}.pdf`
}
