<!--
  InvoiceListView — 邮件发票自动整理列表。

  路由：/email/invoices
  数据源：/api/emails/invoices（后端规则提取：subject/snippet/缓存正文）。
  邮件同步 + AI 分类为 bill 后自动提取落库；本页提供浏览、归档、删除，
  「入账」一键转入记账（/finance），CSV 导出，以及「手动整理」
  （提取结果为空时引导去邮件详情触发）。
-->
<template>
  <div class="page">
    <HeaderActionsPortal>
      <button type="button" class="icon-btn" aria-label="导出 CSV" @click="exportCsv">
        <span class="material-symbols-outlined">download</span>
      </button>
      <button type="button" class="icon-btn" :disabled="loading" aria-label="刷新" @click="load">
        <span class="material-symbols-outlined">refresh</span>
      </button>
    </HeaderActionsPortal>

    <!-- 汇总 -->
    <div class="summary-card">
      <div class="summary-main">
        <span class="summary-amount">¥{{ formatAmount(summary.amount) }}</span>
        <span class="summary-label">
          共 {{ summary.total }} 张 · 已归档 {{ summary.filed }}
          <template v-if="summary.downloaded > 0">· 文件 {{ summary.downloaded }}</template>
          <template v-if="summary.pending > 0">· 待下载 {{ summary.pending }}</template>
          <template v-if="summary.failed > 0">· 失败 {{ summary.failed }}</template>
        </span>
        <span v-if="shareDocName" class="summary-doc" :title="shareDocName">共享清单：{{ shareDocName }}</span>
      </div>
      <div class="summary-actions">
        <button class="sync-btn" :disabled="syncing" @click="runPipeline">
          {{ syncing ? '处理中…' : '收信整理' }}
        </button>
        <button class="sync-btn" :disabled="syncing" @click="syncAndReload">同步</button>
      </div>
    </div>

    <!-- 文件操作行：多选导出 A4 / 推送飞书 -->
    <div class="file-ops">
      <button class="chip" :class="{ active: selectMode }" @click="toggleSelectMode">
        {{ selectMode ? '取消选择' : '选择' }}
      </button>
      <button v-if="selectMode" class="chip" @click="selectAllDownloaded">选已下载</button>
      <div class="spacer" />
      <button
        class="chip export"
        :disabled="exporting || downloadableSelection().length === 0"
        @click="exportGrid(2)"
      >{{ exporting ? '导出中…' : '导出 A4 2×2' }}</button>
      <button
        class="chip export"
        :disabled="exporting || downloadableSelection().length === 0"
        @click="exportGrid(3)"
      >3×3</button>
      <button
        class="chip feishu"
        :disabled="pushing"
        @click="pushFeishu()"
      >{{ pushing ? '推送中…' : '推送飞书' }}</button>
    </div>

    <!-- 状态筛选 -->
    <div class="filter-row">
      <button :class="['chip', { active: filter === '' }]" @click="setFilter('')">全部</button>
      <button :class="['chip', { active: filter === 'new' }]" @click="setFilter('new')">待整理</button>
      <button :class="['chip', { active: filter === 'pending' }]" @click="setFilter('pending')">待下载</button>
      <button :class="['chip', { active: filter === 'downloaded' }]" @click="setFilter('downloaded')">已下载</button>
      <button :class="['chip', { active: filter === 'failed' }]" @click="setFilter('failed')">失败</button>
      <button :class="['chip', { active: filter === 'filed' }]" @click="setFilter('filed')">已归档</button>
    </div>

    <div v-if="error" class="status-err">{{ error }}</div>

    <main class="body">
      <div v-if="loading" class="state">加载中…</div>
      <div v-else-if="invoices.length === 0" class="state">
        <p>暂无发票记录</p>
        <p class="hint">邮箱同步后，账单/发票类邮件会自动提取到这里</p>
      </div>

      <article v-for="inv in invoices" :key="inv.id" class="inv-card" :class="{ filed: inv.status === 'filed' }">
        <div class="inv-main">
          <div class="inv-top">
            <label v-if="selectMode" class="pick">
              <input
                v-model="selected"
                type="checkbox"
                :value="inv.id"
                :disabled="!inv.fileName"
              >
            </label>
            <span class="inv-seller">{{ inv.seller || '未知销售方' }}</span>
            <span class="inv-amount">¥{{ formatAmount(inv.amount) }}</span>
          </div>
          <div class="inv-meta">
            <span class="cat-badge">{{ inv.category || '其他' }}</span>
            <span v-if="inv.invoiceDate">{{ inv.invoiceDate }}</span>
            <span v-if="inv.invoiceNo" class="mono">No.{{ inv.invoiceNo }}</span>
            <span v-if="inv.kind && inv.kind !== 'bill'" class="kind-badge">{{ kindLabel(inv.kind) }}</span>
            <span v-if="inv.feishuSentAt" class="fs-badge">飞书✓</span>
          </div>
          <div v-if="inv.fileName" class="inv-file mono" :title="inv.lastError || inv.fileName">
            {{ inv.fileName }}
            <span v-if="inv.fileSource" class="src-tag">{{ fileSourceLabel(inv.fileSource) }}</span>
          </div>
          <div v-else-if="inv.status === 'pending' && inv.lastError" class="inv-err">
            待重试（第 {{ inv.attempts || 0 }} 次）：{{ inv.lastError }}
          </div>
          <div v-else-if="inv.status === 'failed'" class="inv-err">
            失败：{{ inv.lastError || '无法获取文件' }}
          </div>
          <div v-if="inv.subject" class="inv-subject" :title="inv.subject">{{ inv.subject }}</div>
        </div>
        <div class="inv-actions">
          <span :class="['status-pill', inv.status]">{{ statusLabel(inv) }}</span>
          <button
            v-if="inv.fileName"
            class="act-btn"
            type="button"
            @click="downloadInvoice(inv)"
          >下载 PDF</button>
          <button
            v-if="bookable(inv)"
            class="act-btn primary"
            type="button"
            :disabled="bookingId !== '' && bookingId !== inv.id"
            @click="book(inv)"
          >{{ bookingId === inv.id ? '入账中…' : '入账' }}</button>
          <button
            v-if="inv.status !== 'filed'"
            class="act-btn"
            type="button"
            @click="markFiled(inv)"
          >归档</button>
          <button
            v-else
            class="act-btn"
            type="button"
            @click="markNew(inv)"
          >取消归档</button>
          <button class="act-btn danger" type="button" @click="remove(inv)">删除</button>
        </div>
      </article>
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { emailApi, type EmailInvoice, type EmailInvoiceStatus } from '../../api/email'
import { financeApi } from '../../api/finance'
import * as invoiceStore from './invoices-store'
import { useToast } from '../../composables/useToast'
import { downloadTextFile, downloadFile, DownloadUnsupportedError } from '../../utils/download'
import { wsClient } from '../../api/websocket'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'

const toast = useToast()
const loading = ref(false)
const syncing = ref(false)
const exporting = ref(false)
const pushing = ref(false)
const error = ref('')
const filter = ref<'' | EmailInvoiceStatus>('')
const all = ref<EmailInvoice[]>([])
const summary = ref({ total: 0, filed: 0, amount: 0, downloaded: 0, pending: 0, failed: 0 })
const bookingId = ref('')
const shareDocName = ref('')
// 多选导出
const selectMode = ref(false)
const selected = ref<string[]>([])

/** 展示列表 = 当前筛选下的子集（汇总永远基于全量）。 */
const invoices = computed(() =>
  filter.value ? all.value.filter((i) => i.status === filter.value) : all.value,
)

function applySummary(list: EmailInvoice[]) {
  summary.value = {
    total: list.length,
    filed: list.filter((i) => i.status === 'filed').length,
    amount: list.reduce((s, i) => s + (Number(i.amount) || 0), 0),
    downloaded: list.filter((i) => !!i.fileName).length,
    pending: list.filter((i) => i.status === 'pending' || i.status === 'new').length,
    failed: list.filter((i) => i.status === 'failed').length,
  }
}

function formatAmount(n: number): string {
  return n.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

function kindLabel(kind: string): string {
  const map: Record<string, string> = {
    'e-invoice': '电子发票',
    'vat-special': '专票',
    paper: '纸质',
    receipt: '收据',
    bill: '账单',
  }
  return map[kind] ?? kind
}

function setFilter(f: '' | EmailInvoiceStatus) {
  filter.value = f
}

// ── 状态/来源标签 ─────────────────────────────────────────────────────
function statusLabel(inv: EmailInvoice): string {
  const map: Record<EmailInvoiceStatus, string> = {
    new: '待整理',
    pending: '待下载',
    downloaded: '已下载',
    failed: '失败',
    filed: '已归档',
  }
  return map[inv.status] ?? inv.status
}

function fileSourceLabel(src: string): string {
  const map: Record<string, string> = {
    attachment: '附件',
    'pdf-url': '链接',
    'xml-render': 'XML 重渲染',
  }
  return map[src] ?? src
}

// ── 收信整理：完整流水线（收信→清垃圾→提醒→发票采集→飞书/汇总） ────────────
async function runPipeline() {
  syncing.value = true
  try {
    const rep = await emailApi.runPipeline()
    const bits: string[] = [`新邮件 ${rep.newEmails ?? 0}`]
    if (rep.spamMoved) bits.push(`垃圾清理 ${rep.spamMoved}`)
    if (rep.remindersSent) bits.push(`提醒 ${rep.remindersSent}`)
    if (rep.invoices?.downloaded) bits.push(`发票落盘 ${rep.invoices.downloaded}`)
    if (rep.invoices?.pending) bits.push(`待重试 ${rep.invoices.pending}`)
    if (rep.feishuPushed) bits.push(`已推飞书 ${rep.feishuPushed}`)
    toast.success(`整理完成：${bits.join(' · ')}`)
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

// ── 文件操作 ──────────────────────────────────────────────────────────
function toggleSelectMode() {
  selectMode.value = !selectMode.value
  selected.value = []
}

function selectAllDownloaded() {
  selected.value = all.value.filter((i) => !!i.fileName).map((i) => i.id)
}

/** 导出候选：多选优先，未选时取全部已下载文件。 */
function downloadableSelection(): string[] {
  if (selected.value.length > 0) {
    return selected.value.filter((id) => all.value.some((i) => i.id === id && !!i.fileName))
  }
  return all.value.filter((i) => !!i.fileName).map((i) => i.id)
}

async function exportGrid(grid: 2 | 3) {
  const ids = downloadableSelection()
  if (ids.length === 0) {
    toast.error('没有已下载发票文件的记录可导出')
    return
  }
  exporting.value = true
  try {
    const res = await emailApi.exportInvoicesGrid(ids, grid)
    const blob = await emailApi.fetchInvoiceExport(res.file)
    await downloadFile(res.file, blob, 'application/pdf')
    toast.success(`已导出 ${res.count} 张发票 → A4 ${grid}×${grid}`)
    await load()
  } catch (e: any) {
    if (e instanceof DownloadUnsupportedError) toast.error(e.message)
    else toast.error(e?.body?.error || e?.message || '导出失败')
  } finally {
    exporting.value = false
  }
}

async function pushFeishu() {
  pushing.value = true
  try {
    const res = await emailApi.pushInvoicesToFeishu(selected.value.length ? selected.value : undefined)
    if (res.pushed > 0) {
      toast.success(`已推送 ${res.pushed} 张发票到飞书`)
    } else {
      // 兜底路径：服务端已生成共享汇总文档
      toast.info(res.message || '未推送任何发票（飞书未配置或无可推文件），已生成共享汇总清单')
    }
    if (res.failed > 0) toast.error(`${res.failed} 张推送失败：${(res.errors || []).join('；')}`)
    await load()
  } catch (e: any) {
    toast.error(e?.body?.error || e?.message || '推送失败')
  } finally {
    pushing.value = false
  }
}

async function downloadInvoice(inv: EmailInvoice) {
  try {
    const blob = await emailApi.fetchInvoiceFile(inv.id)
    const name = inv.fileName || `invoice-${inv.id}.pdf`
    await downloadFile(name, blob, 'application/pdf')
    toast.success(`已下载 ${name}`)
  } catch (e: any) {
    if (e instanceof DownloadUnsupportedError) toast.error(e.message)
    else toast.error(e?.message || '下载失败')
  }
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    // limit=500（后端上限）：汇总与镜像基于全量，避免默认 200 截断失真
    const res = await emailApi.listInvoices(undefined, 500)
    all.value = res.invoices ?? []
    applySummary(all.value)
    try {
      const sum = await emailApi.invoiceSummary()
      shareDocName.value = sum.shareDocMd || sum.shareDocCsv || ''
    } catch { /* 汇总文档是增强信息，失败不打扰 */ }
    try {
      await invoiceStore.syncFromServer(all.value)
    } catch {
      // 镜像是增强能力（web 无本地库时跳过）
    }
  } catch (e: any) {
    // 服务端不可达：退化为本地镜像离线浏览
    try {
      all.value = await invoiceStore.listLocal()
      applySummary(all.value)
      if (all.value.length === 0) error.value = e?.message || '加载失败'
    } catch {
      error.value = e?.message || '加载失败'
    }
  } finally {
    loading.value = false
  }
}

// 服务端广播：发票提取完成 / A4 导出完成 → 刷新列表
function onInvoiceExtracted() {
  void load()
}
function onInvoicesExported() {
  void load()
}
onMounted(() => {
  wsClient.on('email.invoice.extracted', onInvoiceExtracted)
  wsClient.on('email.invoices.exported', onInvoicesExported)
})
onUnmounted(() => {
  wsClient.off('email.invoice.extracted', onInvoiceExtracted)
  wsClient.off('email.invoices.exported', onInvoicesExported)
})

async function markFiled(inv: EmailInvoice) {
  try {
    await emailApi.setInvoiceStatus(inv.id, 'filed')
    inv.status = 'filed'
    applySummary(all.value)
    try { await invoiceStore.setLocalStatus(inv.id, 'filed') } catch { /* 本地镜像可选 */ }
    toast.success('已归档')
  } catch (e: any) {
    toast.error(e?.message || '操作失败')
  }
}

async function markNew(inv: EmailInvoice) {
  try {
    await emailApi.setInvoiceStatus(inv.id, 'new')
    inv.status = 'new'
    applySummary(all.value)
    try { await invoiceStore.setLocalStatus(inv.id, 'new') } catch { /* 本地镜像可选 */ }
  } catch (e: any) {
    toast.error(e?.message || '操作失败')
  }
}

/** 可入账：有金额且是人民币（记账金额为 CNY 语义，外币发票不做本币入账）。 */
function bookable(inv: EmailInvoice): boolean {
  return (Number(inv.amount) || 0) > 0 && (!inv.currency || inv.currency === 'CNY')
}

/** 一键入账：发票视为支出，类目沿用发票推断；入账后自动归档。
 *  带 note_ref 幂等键（invoice:<id>）：归档失败重试/取消归档后再入账都不会重复记账。 */
async function book(inv: EmailInvoice) {
  if (bookingId.value) return
  bookingId.value = inv.id
  try {
    const res = await financeApi.create({
      type: 'expense',
      amount: Number(inv.amount) || 0,
      category: inv.category || '其他',
      note: `[发票] ${inv.seller || inv.subject || '未知销售方'}${inv.invoiceNo ? `｜No.${inv.invoiceNo}` : ''}`,
      source: 'invoice',
      note_ref: `invoice:${inv.id}`,
    })
    if (inv.status !== 'filed') {
      try {
        await emailApi.setInvoiceStatus(inv.id, 'filed')
        inv.status = 'filed'
        applySummary(all.value)
        try { await invoiceStore.setLocalStatus(inv.id, 'filed') } catch { /* 本地镜像可选 */ }
      } catch { /* 归档失败不影响入账结果 */ }
    }
    const verb = res.created ? '已入账' : '该发票已入账'
    toast.success(`${verb} ¥${formatAmount(inv.amount)}，可在记账中查看`)
  } catch (e: any) {
    toast.error(e?.body?.error || e?.message || '入账失败')
  } finally {
    bookingId.value = ''
  }
}

// ── CSV 导出（当前筛选列表；带 BOM，Excel 直接打开不乱码） ────────────────
function csvCell(v: string | number): string {
  let s = String(v ?? '')
  // 公式注入防护：Excel/WPS 会把 =+-@ 开头的单元格当公式执行（值来自入站邮件，不可信）
  if (/^[=+\-@\t\r]/.test(s)) s = `'${s}`
  return `"${s.replace(/"/g, '""')}"`
}

/** 原生端不支持导出由 downloadTextFile 统一抛 DownloadUnsupportedError，调用方提示。 */
async function exportCsv() {
  const rows = invoices.value
  if (rows.length === 0) {
    toast.error('当前没有可导出的发票')
    return
  }
  const header = ['日期', '销售方', '金额', '发票号', '类目', '状态']
  const lines = [header.map(csvCell).join(',')]
  for (const inv of rows) {
    lines.push([
      inv.invoiceDate || '',
      inv.seller || '',
      (Number(inv.amount) || 0).toFixed(2),
      inv.invoiceNo || '',
      inv.category || '其他',
      inv.status === 'filed' ? '已归档' : '待整理',
    ].map(csvCell).join(','))
  }
  const now = new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  try {
    await downloadTextFile({
      filename: `openpocket-invoices-${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}.csv`,
      content: '\uFEFF' + lines.join('\r\n'),
      mimeType: 'text/csv;charset=utf-8',
    })
    toast.success(`已导出 ${rows.length} 张发票`)
  } catch (e) {
    if (e instanceof DownloadUnsupportedError) {
      toast.error(e.message)
      return
    }
    toast.error('导出失败')
  }
}

async function remove(inv: EmailInvoice) {
  try {
    await emailApi.deleteInvoice(inv.id)
    all.value = all.value.filter((i) => i.id !== inv.id)
    applySummary(all.value)
    try { await invoiceStore.removeLocal(inv.id) } catch { /* 本地镜像可选 */ }
    toast.success('已删除')
  } catch (e: any) {
    toast.error(e?.message || '删除失败')
  }
}

onMounted(load)
</script>

<style scoped>
.page { min-height: 100%; background: var(--bg-base); }
.icon-btn {
  background: none; border: none; color: var(--text-primary);
  display: flex; cursor: pointer; padding: 4px;
}
.summary-card {
  display: flex; align-items: center; justify-content: space-between; gap: 10px;
  margin: var(--space-3); padding: var(--space-3);
  background: var(--bg-card); border: 1px solid var(--border); border-radius: 12px;
}
.summary-main { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.summary-amount { font-size: 20px; font-weight: 700; color: var(--text-primary); }
.summary-label { font-size: 11px; color: var(--text-secondary); }
.sync-btn {
  flex: none; padding: 8px 14px; font-size: 12px; border-radius: 999px;
  border: none; background: var(--brand-primary, #4c8dff); color: #fff; cursor: pointer;
}
.sync-btn:disabled { opacity: 0.6; }
.filter-row {
  display: flex; gap: 6px; padding: 0 var(--space-3) var(--space-2);
}
.chip {
  flex: none; padding: 5px 12px; font-size: 12px;
  background: var(--bg-subtle); border: 1px solid var(--border);
  border-radius: 999px; color: var(--text-secondary); cursor: pointer;
}
.chip.active { background: var(--brand-primary, #4c8dff); color: #fff; border-color: transparent; }
.status-err {
  margin: 0 var(--space-3) var(--space-2); padding: var(--space-2) var(--space-3); font-size: 12px;
  background: color-mix(in srgb, var(--danger) 12%, transparent); color: var(--danger); border-radius: 8px;
}
.body { padding: 0 var(--space-3) 100px; display: flex; flex-direction: column; gap: var(--space-2); }
.state { padding: 40px 20px; text-align: center; color: var(--text-secondary); font-size: 14px; }
.hint { font-size: 12px; margin-top: 8px; }
.inv-card {
  background: var(--bg-card); border: 1px solid var(--border); border-radius: 12px;
  padding: var(--space-3);
}
.inv-card.filed { opacity: 0.75; }
.inv-top { display: flex; align-items: baseline; justify-content: space-between; gap: 10px; }
.inv-seller { font-size: 14px; font-weight: 600; color: var(--text-primary); word-break: break-all; }
.inv-amount { flex: none; font-size: 15px; font-weight: 700; color: var(--text-primary); }
.inv-meta {
  display: flex; align-items: center; gap: 8px; margin-top: 6px; flex-wrap: wrap;
  font-size: 11px; color: var(--text-secondary);
}
.mono { font-family: 'SF Mono', Menlo, monospace; }
.cat-badge {
  padding: 2px 8px; border-radius: 999px; font-size: 10px;
  background: var(--bg-subtle); border: 1px solid var(--border); color: var(--text-secondary);
}
.kind-badge {
  padding: 2px 8px; border-radius: 999px; font-size: 10px;
  background: color-mix(in srgb, var(--brand-primary, #4c8dff) 10%, transparent);
  color: var(--brand-primary, #4c8dff);
}
.inv-subject {
  margin-top: 6px; font-size: 11px; color: var(--text-muted);
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.inv-actions {
  display: flex; align-items: center; gap: 8px; margin-top: 10px;
}
.status-pill { font-size: 11px; }
.status-pill.new { color: var(--warning, #f59e0b); }
.status-pill.pending { color: var(--warning, #f59e0b); }
.status-pill.downloaded { color: var(--success, #10b981); }
.status-pill.failed { color: var(--danger); }
.status-pill.filed { color: var(--success, #10b981); }
.file-ops {
  display: flex; align-items: center; gap: 6px; flex-wrap: wrap;
  padding: 0 var(--space-3) var(--space-2);
}
.file-ops .spacer { flex: 1; }
.chip.export {
  border-color: color-mix(in srgb, var(--brand-primary, #4c8dff) 40%, transparent);
  color: var(--brand-primary, #4c8dff);
}
.chip.export:disabled { opacity: 0.5; cursor: not-allowed; }
.chip.feishu {
  border-color: color-mix(in srgb, var(--success, #10b981) 40%, transparent);
  color: var(--success, #10b981);
}
.chip.feishu:disabled { opacity: 0.5; cursor: not-allowed; }
.pick { display: flex; align-items: center; margin-right: 8px; }
.summary-actions { display: flex; flex-direction: column; gap: 6px; flex: none; }
.summary-doc {
  font-size: 10px; color: var(--text-muted);
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 220px;
}
.inv-file {
  margin-top: 6px; font-size: 11px; color: var(--text-secondary); word-break: break-all;
}
.src-tag {
  margin-left: 6px; padding: 1px 6px; border-radius: 999px; font-size: 9px;
  background: var(--bg-subtle); border: 1px solid var(--border); color: var(--text-secondary);
}
.fs-badge {
  padding: 1px 6px; border-radius: 999px; font-size: 9px;
  background: color-mix(in srgb, var(--success, #10b981) 12%, transparent);
  color: var(--success, #10b981);
}
.inv-err {
  margin-top: 6px; font-size: 11px; color: var(--danger);
  word-break: break-all;
}
.act-btn {
  margin-left: auto; padding: 5px 12px; font-size: 12px;
  background: var(--bg-subtle); border: 1px solid var(--border); border-radius: 8px;
  color: var(--text-primary); cursor: pointer;
}
.act-btn + .act-btn { margin-left: 0; }
.act-btn.primary {
  background: var(--brand-primary, #4c8dff); border-color: transparent; color: #fff;
}
.act-btn.primary:disabled { opacity: 0.6; cursor: not-allowed; }
.act-btn.danger { color: var(--danger); }
</style>
