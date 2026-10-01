import { computed, onUnmounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { emailApi, type EmailInvoice, type EmailInvoiceStatus } from '../../api/email'
import { financeApi } from '../../api/finance'
import { useToast } from '../../composables/useToast'
import { useApiError } from '../../composables/useApiError'
import { downloadTextFile, downloadFile, DownloadUnsupportedError } from '../../utils/download'
import { isLocalOnlyId } from '../../native/list-sync/planner'
import * as invoiceStore from './invoices-store'
import { pullInvoiceServerPage, pushDirtyInvoices } from './invoice-list-pull'
import { useInvoiceThumbs } from './use-invoice-thumbs.ts'
import {
  INVOICE_PAGE_SIZE, invoiceFileKind, invoiceHasFile,
  mergeInvoicePages, sortInvoicesByReceived, type InvoiceFileKind,
} from './invoice-list'
import {
  formatMoney, normalizeCurrency, round2, sumByCurrency,
  summaryMoney as summaryMoneyText, type CurrencyAmount,
} from './invoice-money'

export function useInvoiceList() {
  const toast = useToast()
  const apiError = useApiError()
  const router = useRouter()
  const loading = ref(false)
  const syncing = ref(false)
  const exporting = ref(false)
  const pushing = ref(false)
  const error = ref('')
  const filter = ref<'' | EmailInvoiceStatus>('')
  const all = ref<EmailInvoice[]>([])
  // 合计按币种分组（groups）；singleAmount/singleCurrency 仅在恰好一种币种时有值。
  // 不用单个 `amount`：跨币种相加得到的数字不是金额，渲染成 ¥ 就是错账。
  const summary = ref({
    total: 0, filed: 0, groups: [] as Array<{ currency: string; amount: number }>,
    singleAmount: null as number | null, singleCurrency: null as string | null,
    downloaded: 0, pending: 0, failed: 0,
  })
  const bookingId = ref('')
  /** 飞书共享台账链接（推不出去时的兜底共享文档）。 */
  const shareDocUrl = ref('')
  const selectMode = ref(false)
  const selected = ref<string[]>([])
  // 缩略图：后端内嵌位图优先，文字型 PDF 回落到原生 PdfRenderer 渲染第 1 页。
  const { thumbs, loading: thumbLoading, loadThumbs: loadInvoiceThumbs, revokeAll: revokeAllThumbs, applyRemaps: remapThumbs } =
    useInvoiceThumbs()
  const preview = ref<{ inv: EmailInvoice; src: string; blob: Blob | null } | null>(null)
  const hasMore = ref(false)
  const loadingMore = ref(false)
  const nextOffset = ref(0)

  const invoices = computed(() => all.value)
  const previewSrc = computed(() => preview.value?.src || '')
  const previewBlob = computed(() => preview.value?.blob ?? null)
  const previewKey = computed(() => preview.value?.inv.id || '')
  const previewKind = computed<InvoiceFileKind>(() => invoiceFileKind(preview.value?.inv.fileName))
  const previewTitle = computed(() => preview.value?.inv.seller || '发票预览')

  function applySummary(list: EmailInvoice[], totals?: { total: number; filed: number; amount: number; currency?: string; amounts?: CurrencyAmount[] }) {
    // 合计按币种分组：跨币种直接相加不是金额，而把它渲染成 ¥ 就是错账
    // （需求 3「汇总金额」）。服务端多币种时 amount=0、amounts 非空，以它为准
    // （它统计全量，不受分页截断）；否则用当前页的发票自行分组。
    const groups: CurrencyAmount[] = totals?.amounts?.length
      ? totals.amounts.map((a) => ({ currency: normalizeCurrency(a.currency), amount: round2(a.amount) }))
      : totals?.amount
        ? [{ currency: normalizeCurrency(totals.currency), amount: round2(totals.amount) }]
        : sumByCurrency(list)
    summary.value = {
      total: totals?.total ?? list.length,
      filed: totals?.filed ?? list.filter((i) => i.status === 'filed').length,
      groups,
      // 单币种时保留一个标量，方便旧调用点；多币种为 null
      // （不是 0——0 是个看起来正常的错数）。
      singleAmount: groups.length === 1 ? groups[0]!.amount : null,
      singleCurrency: groups.length === 1 ? groups[0]!.currency : null,
      downloaded: list.filter(invoiceHasFile).length,
      pending: list.filter((i) => i.status === 'pending' || i.status === 'new').length,
      failed: list.filter((i) => i.status === 'failed').length,
    }
  }
  /** 合计区展示：单币种一个数，多币种逐币种拼。 */
  function summaryMoney(): string {
    return summaryMoneyText(summary.value.groups)
  }
  /** 单张发票的金额展示（用它自己的币种，不再一律 ¥）。 */
  function invoiceMoney(inv: EmailInvoice): string {
    return formatMoney(Number(inv.amount) || 0, inv.currency)
  }
  function formatAmount(n: number): string {
    return n.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
  }  function statusLabel(inv: EmailInvoice): string {
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
    revokeAllThumbs()
  }
  async function loadThumbs(list: EmailInvoice[]) {
    await loadInvoiceThumbs(list.filter(invoiceHasFile))
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
      // blob 一并留着：Android 上 PDF 由原生 PdfRenderer 渲染，src(blob:) 只给
      // 图片附件和 web/iOS 的 iframe 用（WebView 内核不渲染 PDF，iframe 会全白）。
      preview.value = { inv, src: URL.createObjectURL(blob), blob }
    } catch (e: any) {
      toast.error(apiError(e, 'errors.notFound'))
    }
  }
  function applyRemaps(remaps: { localId: string; serverId: string }[]) {
    // 缩略图缓存的键是发票 id，本地临时 id 重映射成服务端 id 后要跟着换键，
    // 否则重映射后每张卡都会重新去渲染一次 PDF。
    remapThumbs(remaps)
    for (const remap of remaps) {
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
      if (all.value.length === 0) error.value = apiError(e, 'errors.loadInvoicesFailed')
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
      toast.error(apiError(e, 'errors.loadEmailFailed'))
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
      toast.error(apiError(e, 'errors.operateFailed'))
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
      toast.error(apiError(e, 'errors.operateFailed'))
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
      const saved = await downloadFile(res.file, await emailApi.fetchInvoiceExport(res.file), 'application/pdf')
      toast.success(`已导出 ${res.count} 张发票 · ${saved}`)
      await load()
    } catch (e: any) {
      toast.error(e instanceof DownloadUnsupportedError ? e.message : apiError(e, 'errors.operateFailed'))
    } finally {
      exporting.value = false
    }
  }
  async function pushFeishu() {
    pushing.value = true
    try {
      const res = await emailApi.pushInvoicesToFeishu(selected.value.length ? selected.value : undefined)
      // 推不出去时服务端会兜底建共享台账（飞书电子表格）。把链接留住给用户点，
      // 只弹一句 toast 的话，链接一闪而过等于没做。
      if (res.shareDocUrl) shareDocUrl.value = res.shareDocUrl
      if (res.ledgerError) console.warn('[email] feishu ledger publish failed:', res.ledgerError)
      toast.success(res.pushed > 0 ? `已推送 ${res.pushed} 张` : (res.message || '未推送'))
      await load()
    } catch (e: any) {
      toast.error(apiError(e, 'errors.operateFailed'))
    } finally {
      pushing.value = false
    }
  }
  async function downloadInvoice(inv: EmailInvoice) {
    try {
      const name = inv.fileName || `invoice-${inv.id}.pdf`
      const saved = await downloadFile(name, await emailApi.fetchInvoiceFile(inv.id), 'application/pdf')
      toast.success(saved)
    } catch (e: any) {
      toast.error(e instanceof DownloadUnsupportedError ? e.message : apiError(e, 'errors.operateFailed'))
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
      toast.error(apiError(e, 'errors.operateFailed'))
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
      toast.error(apiError(e, 'errors.operateFailed'))
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
      toast.success(`${res.created ? '已入账' : '该发票已入账'} ${invoiceMoney(inv)}`)
    } catch (e: any) {
      toast.error(apiError(e, 'errors.operateFailed'))
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
    // 必须带币种列：不带的话导出的金额是裸数字，导出后再对账时无从判断是哪种货币。
    const lines = [['开票日期', '收到日期', '销售方', '金额', '币种', '发票号', '类目', '状态'].map(cell).join(',')]
    for (const inv of rows) {
      lines.push([inv.invoiceDate || '', inv.emailDate || '', inv.seller || '',
        (Number(inv.amount) || 0).toFixed(2), inv.currency || 'CNY',
        inv.invoiceNo || '', inv.category || '其他', inv.status].map(cell).join(','))
    }
    try {
      const saved = await downloadTextFile({ filename: 'openpocket-invoices.csv', content: '\uFEFF' + lines.join('\r\n'), mimeType: 'text/csv;charset=utf-8' })
      toast.success(`已导出 ${rows.length} 张发票 · ${saved}`)
    } catch (e) {
      toast.error(e instanceof DownloadUnsupportedError ? e.message : apiError(e, 'errors.operateFailed'))
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
      toast.error(apiError(e, 'errors.operateFailed'))
    }
  }
  watch(filter, () => { void load() })
  onUnmounted(() => {
    revokeThumbs()
    closePreview()
  })
  return {
    loading, loadingMore, hasMore, syncing, exporting, pushing, error, filter, summary, bookingId,
    shareDocUrl,
    selectMode, selected, thumbs, thumbLoading, preview, invoices, previewSrc, previewBlob, previewKey,
    previewKind, previewTitle,
    formatAmount, formatMoney, summaryMoney, invoiceMoney, statusLabel, bookable, toggleSelectMode, selectAllDownloaded, togglePick,
    downloadableSelection, openEmail, openPreview, closePreview, load, loadMore, runPipeline,
    syncAndReload, exportGrid, pushFeishu, downloadInvoice, markFiled, markNew, book,
    exportCsv, remove,
  }
}

