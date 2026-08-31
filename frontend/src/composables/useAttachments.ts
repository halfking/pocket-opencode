/**
 * useAttachments — 统一输入组件共享的图片附件管理。
 *
 * 校验规则与后端 /api/llm/stream 保持一致（自 AIChatView 抽取）：
 *   - 单次最多 4 张
 *   - 单张 ≤ 4MB（data URL 字符 ≤ 6MB）
 *   - 附件总量 ≤ 32MB
 */
import { ref } from 'vue'
import { useToast } from './useToast'

export const MAX_IMAGES = 4
export const MAX_IMAGE_BYTES = 4 << 20
export const MAX_DATA_URL_CHARS = 6 * 1024 * 1024
export const MAX_TOTAL_PAYLOAD_CHARS = 32 * 1024 * 1024

export interface Attachment {
  /** data:image/... 或 https:// 外链 */
  dataUrl: string
  name: string
}

export function useAttachments() {
  const attachments = ref<Attachment[]>([])
  const toast = useToast()

  function canAddMore(): boolean {
    if (attachments.value.length >= MAX_IMAGES) {
      toast.error(`最多 ${MAX_IMAGES} 张图片`)
      return false
    }
    return true
  }

  function addDataUrl(dataUrl: string, name: string): boolean {
    if (!canAddMore()) return false
    if (dataUrl.length > MAX_DATA_URL_CHARS) {
      toast.error(`「${name}」图片编码后超过 6MB，已跳过`)
      return false
    }
    const total = attachments.value.reduce((sum, cur) => sum + cur.dataUrl.length, 0) + dataUrl.length
    if (total > MAX_TOTAL_PAYLOAD_CHARS) {
      toast.error('附件过大，请减少图片张数后再试')
      return false
    }
    attachments.value.push({ dataUrl, name })
    return true
  }

  function addFiles(files: File[]) {
    for (const f of files) {
      if (!canAddMore()) break
      if (!f.type.startsWith('image/')) continue
      if (f.size > MAX_IMAGE_BYTES) {
        toast.error(`「${f.name}」超过 4MB，已跳过`)
        continue
      }
      const reader = new FileReader()
      reader.onload = () => {
        if (typeof reader.result === 'string') addDataUrl(reader.result, f.name)
      }
      reader.readAsDataURL(f)
    }
  }

  /** 粘贴板图片（微信/截图 Ctrl+V）。返回 true 表示已拦截该粘贴。 */
  function addFromClipboard(e: ClipboardEvent): boolean {
    const items = e.clipboardData?.items
    if (!items) return false
    let handled = false
    for (const item of items) {
      if (item.kind !== 'file' || !item.type.startsWith('image/')) continue
      const file = item.getAsFile()
      if (!file) continue
      if (file.size > MAX_IMAGE_BYTES) {
        toast.error('粘贴图片超过 4MB，已跳过')
        continue
      }
      const reader = new FileReader()
      reader.onload = () => {
        if (typeof reader.result === 'string') addDataUrl(reader.result, '粘贴')
      }
      reader.readAsDataURL(file)
      handled = true
    }
    return handled
  }

  function remove(index: number) {
    attachments.value.splice(index, 1)
  }

  function clear() {
    attachments.value = []
  }

  /** dataUrl 数组（发送用）。 */
  function imageUrls(): string[] {
    return attachments.value.map((a) => a.dataUrl)
  }

  return {
    attachments,
    addDataUrl,
    addFiles,
    addFromClipboard,
    remove,
    clear,
    imageUrls,
  }
}
