import { computed, onUnmounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { emailApi, type EmailInvoice, type EmailInvoiceStatus } from '../../api/email'
import { financeApi } from '../../api/finance'
import { useToast } from '../../composables/useToast'
import { downloadTextFile, downloadFile, DownloadUnsupportedError } from '../../utils/download'
import { remapRecordMap } from '../../native/list-sync/id-align'
import { isLocalOnlyId } from '../../native/list-sync/planner'
import * as invoiceStore from './invoices-store'
import { pullInvoiceServerPage, pushDirtyInvoices } from './invoice-list-pull'
import {
  INVOICE_PAGE_SIZE, invoiceFileKind, invoiceHasFile,
  mergeInvoicePages, sortInvoicesByReceived, type InvoiceFileKind,
} from './invoice-list'

export function useInvoiceList() {
  const toast = useToast()
  const router = useRouter()
  const loading = ref(false)
  const syncing = ref(false)
  const exporting = ref(false)
  const pushing = ref(false)
  const error = ref('')
  const filter = ref<'' | EmailInvoiceStatus>('')
  const all = ref<EmailInvoice[]>([])
  const summary = ref({ total: 0, filed: 0, amount: 0, downloaded: 0, pending: 0, failed: 0 })
  const bookingId = ref('')
  const selectMode = ref(false)
  const selected = ref<string[]>([])
  const thumbs = ref<Record<string, string>>({})
  const preview = ref<{ inv: EmailInvoice; src: string } | null>(null)
  const hasMore = ref(false)
  const loadingMore = ref(false)
  const nextOffset = ref(0)

  const invoices = computed(() => all.value)
  const previewSrc = computed(() => preview.value?.src || '')
  const previewKind = computed<InvoiceFileKind>(() => invoiceFileKind(preview.value?.inv.fileName))
  const previewTitle = computed(() => preview.value?.inv.seller || '发票预览')

  function applySummary(list: EmailInvoice[], totals?: { total: number; filed: number; amount: number }) {
    summary.value = {
      total: totals?.total ?? list.length,
      filed: totals?.filed ?? list.filter((i) => i.status === 'filed').length,
      amount: totals?.amount ?? list.reduce((s, i) => s + (Number(i.amount) || 0), 0),
      downloaded: list.filter(invoiceHasFile).length,
      pending: list.filter((i) => i.status === 'pending' || i.status === 'new').length,
      failed: list.filter((i) => i.status === 'failed').length,
    }
  }
  function formatAmount(n: number): string {
    return n.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
  }
  function statusLabel(inv: EmailInvoice): string {
    return ({ new: '待整理', pending: '待下载', downloaded: '已下载', failed: '失败', filed: '已归档' } as const)[inv.status] ?? inv.status
  }
  function bookable(inv: EmailInvoice): boolean {
    return (Number(inv.amount) || 0) > 0 && (!inv.currency || inv.currency === 'CNY')
  }
  function toggleSelectMode() {
    selectMode.value = !selectMode.value
    selected.value = []
  }
  function selectAllDownloaded() {
    selected.value = all.value.filter(invoiceHasFile).map((i) => i.id)
  }
  function togglePick(id: string) {
    selected.value = selected.value.includes(id) ? selected.value.filter((x) => x !== id) : [...selected.value, id]
  }
  function downloadableSelection(): string[] {
    if (selected.value.length > 0) {
      return selected.value.filter((id) => all.value.some((i) => i.id === id && invoiceHasFile(i)))
    }
    return all.value.filter(invoiceHasFile).map((i) => i.id)
  }
  function revokeThumbs() {
    Object.values(thumbs.value).forEach((u) => URL.revokeObjectURL(u))
    thumbs.value = {}
  }
  async function loadThumbs(list: EmailInvoice[]) {
    const next = { ...thumbs.value }
    for (const inv of list.filter(invoiceHasFile)) {
      if (next[inv.id]) continue
      try {
        next[inv.id] = URL.createObjectURL(await emailApi.fetchInvoiceThumb(inv.id))
      } catch { /* 无嵌入图时卡片走文档图标 */ }
    }
    thumbs.value = next
  }
  function openEmail(inv: EmailInvoice) {
    if (!inv.emailId) {
      toast.error('找不到来源邮件')
      return
    }
    router.push({ name: 'email-detail', params: { id: inv.emailId } })
  }
  function closePreview() {
    if (preview.value?.src) URL.revokeObjectURL(preview.value.src)
    preview.value = null
  }
  async function openPreview(inv: EmailInvoice) {
    if (!invoiceHasFile(inv)) return
    try {
      const blob = await emailApi.fetchInvoiceFile(inv.id)
      closePreview()
      preview.value = { inv, src: URL.createObjectURL(blob) }
    } catch (e: any) {
      toast.error(e?.message || '无法打开发票文件')
    }
  }
  function applyRemaps(remaps: { localId: string; serverId: string }[]) {
    for (const remap of remaps) {
      thumbs.value = remapRecordMap(thumbs.value, remap)
      selected.value = selected.value.map((id) => (id === remap.localId ? remap.serverId : id))
    }
  }
  async function pullServerPage(offset: number) {
    const pulled = await pullInvoiceServerPage({
      status: filter.value || undefined,
      offset,
      current: all.value,
    })
    applyRemaps(pulled.remaps)
    all.value = pulled.rows
    nextOffset.value = pulled.rows.length
    hasMore.value = pulled.hasMore
    applySummary(pulled.rows, pulled.totals)
    void loadThumbs(pulled.rows)
  }
  async function load() {
    loading.value = true
    error.value = ''
    nextOffset.value = 0
    hasMore.value = false
    try {
      const page = await invoiceStore.listLocalPage({
        status: filter.value || undefined,
        limit: INVOICE_PAGE_SIZE,
        offset: 0,
      })
      all.value = sortInvoicesByReceived(page.rows)
      nextOffset.value = page.rows.length
      hasMore.value = page.hasMore
      applySummary(all.value)
      revokeThumbs()
      void loadThumbs(page.rows)
      if (page.rows.length) loading.value = false
    } catch { /* 本地库未就绪时仍拉服务端 */ }
    try {
      await pullServerPage(0)
    } catch (e: any) {
      if (all.value.length === 0) error.value = e?.message || '加载失败'
    } finally {
      loading.value = false
    }
    void pushDirtyInvoices().catch(() => {})
  }
  async function loadMore() {
    if (loading.value || loadingMore.value || !hasMore.value) return
    loadingMore.value = true
    const offset = nextOffset.value
    try {
      const local = await invoiceStore.listLocalPage({
        status: filter.value || undefined,
        limit: INVOICE_PAGE_SIZE,
        offset,
      })
      if (local.rows.length) {
        all.value = mergeInvoicePages(all.value, local.rows)
        nextOffset.value = all.value.length
        hasMore.value = local.hasMore
        void loadThumbs(local.rows)
      }
      await pullServerPage(offset)
    } catch (e: any) {
      toast.error(e?.message || '加载更多失败')
    } finally {
      loadingMore.value = false
    }
  }
  async function runPipeline() {
    syncing.value = true
    try {
      const rep = await emailApi.runPipeline()
      toast.success(`整理完成：新邮件 ${rep.newEmails ?? 0}`)
      await load()
    } catch (e: any) {
      toast.error(e?.message || '整理失败')
    } finally {
      syncing.value = false
    }
  }
  async function syncAndReload() {
    syncing.value = true
    try {
      await emailApi.syncNow()
      toast.success('邮箱同步完成，正在提取发票…')
      await load()
    } catch (e: any) {
      toast.error(e?.message || '同步失败')
    } finally {
      syncing.value = false
    }
  }
  async function exportGrid(grid: 2 | 3) {
    const ids = downloadableSelection()
    if (ids.length === 0) return
    exporting.value = true
    try {
      const res = await emailApi.exportInvoicesGrid(ids, grid)
      await downloadFile(res.file, await emailApi.fetchInvoiceExport(res.file), 'application/pdf')
      toast.success(`已导出 ${res.count} 张发票`)
      await load()
    } catch (e: any) {
      toast.error(e instanceof DownloadUnsupportedError ? e.message : (e?.message || '导出失败'))
    } finally {
      exporting.value = false
    }
  }
  async function pushFeishu() {
    pushing.value = true
    try {
      const res = await emailApi.pushInvoicesToFeishu(selected.value.length ? selected.value : undefined)
      toast.success(res.pushed > 0 ? `已推送 ${res.pushed} 张` : (res.message || '未推送'))
      await load()
    } catch (e: any) {
      toast.error(e?.message || '推送失败')
    } finally {
      pushing.value = false
    }
  }
  async function downloadInvoice(inv: EmailInvoice) {
    try {
      const name = inv.fileName || `invoice-${inv.id}.pdf`
      await downloadFile(name, await emailApi.fetchInvoiceFile(inv.id), 'application/pdf')
      toast.success(`已下载 ${name}`)
    } catch (e: any) {
      toast.error(e instanceof DownloadUnsupportedError ? e.message : (e?.message || '下载失败'))
    }
  }
  async function markFiled(inv: EmailInvoice) {
    inv.status = 'filed'
    applySummary(all.value)
    try {
      await invoiceStore.setLocalStatus(inv.id, 'filed')
      if (!isLocalOnlyId(inv.id)) {
        await emailApi.setInvoiceStatus(inv.id, 'filed')
        await invoiceStore.clearDirty(inv.id)
      }
      toast.success('已归档')
    } catch (e: any) {
      toast.error(e?.message || '操作失败')
    }
  }
  async function markNew(inv: EmailInvoice) {
    inv.status = 'new'
    applySummary(all.value)
    try {
      await invoiceStore.setLocalStatus(inv.id, 'new')
      if (!isLocalOnlyId(inv.id)) {
        await emailApi.setInvoiceStatus(inv.id, 'new')
        await invoiceStore.clearDirty(inv.id)
      }
    } catch (e: any) {
      toast.error(e?.message || '操作失败')
    }
  }
  async function book(inv: EmailInvoice) {
    if (bookingId.value) return
    bookingId.value = inv.id
    try {
      const res = await financeApi.create({
        type: 'expense', amount: Number(inv.amount) || 0, category: inv.category || '其他',
        note: `[发票] ${inv.seller || inv.subject || '未知销售方'}`, source: 'invoice', note_ref: `invoice:${inv.id}`,
      })
      if (inv.status !== 'filed') {
        try { await emailApi.setInvoiceStatus(inv.id, 'filed'); inv.status = 'filed'; applySummary(all.value) } catch { /* ignore */ }
      }
      toast.success(`${res.created ? '已入账' : '该发票已入账'} ¥${formatAmount(inv.amount)}`)
    } catch (e: any) {
      toast.error(e?.message || '入账失败')
    } finally {
      bookingId.value = ''
    }
  }
  async function exportCsv() {
    const rows = invoices.value
    if (rows.length === 0) { toast.error('当前没有可导出的发票'); return }
    const cell = (v: string | number) => {
      let s = String(v ?? '')
      if (/^[=+\-@\t\r]/.test(s)) s = `'${s}`
      return `"${s.replace(/"/g, '""')}"`
    }
    const lines = [['开票日期', '收到日期', '销售方', '金额', '发票号', '类目', '状态'].map(cell).join(',')]
    for (const inv of rows) {
      lines.push([inv.invoiceDate || '', inv.emailDate || '', inv.seller || '', (Number(inv.amount) || 0).toFixed(2), inv.invoiceNo || '', inv.category || '其他', inv.status].map(cell).join(','))
    }
    try {
      await downloadTextFile({ filename: 'openpocket-invoices.csv', content: '\uFEFF' + lines.join('\r\n'), mimeType: 'text/csv;charset=utf-8' })
      toast.success(`已导出 ${rows.length} 张发票`)
    } catch (e) {
      toast.error(e instanceof DownloadUnsupportedError ? e.message : '导出失败')
    }
  }
  async function remove(inv: EmailInvoice) {
    try {
      await invoiceStore.removeLocal(inv.id)
      all.value = all.value.filter((i) => i.id !== inv.id)
      applySummary(all.value)
      if (!isLocalOnlyId(inv.id)) await emailApi.deleteInvoice(inv.id)
      toast.success('已删除')
    } catch (e: any) {
      toast.error(e?.message || '删除失败')
    }
  }
  watch(filter, () => { void load() })
  onUnmounted(() => {
    revokeThumbs()
    closePreview()
  })
  return {
    loading, loadingMore, hasMore, syncing, exporting, pushing, error, filter, summary, bookingId,
    selectMode, selected, thumbs, preview, invoices, previewSrc, previewKind, previewTitle,
    formatAmount, statusLabel, bookable, toggleSelectMode, selectAllDownloaded, togglePick,
    downloadableSelection, openEmail, openPreview, closePreview, load, loadMore, runPipeline,
    syncAndReload, exportGrid, pushFeishu, downloadInvoice, markFiled, markNew, book,
    exportCsv, remove,
  }
}
