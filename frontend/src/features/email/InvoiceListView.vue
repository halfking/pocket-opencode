<!--
  InvoiceListView — 邮件发票自动整理列表。
  路由：/email/invoices；点卡片看原邮件，点缩略图看全图/文件。
-->
<template>
  <div class="page">
    <HeaderActionsPortal>
      <button type="button" class="icon-btn" :disabled="syncing" aria-label="收信整理" @click="runPipeline">
        <span class="material-symbols-outlined">auto_awesome</span>
      </button>
      <button type="button" class="icon-btn" :disabled="syncing" aria-label="同步" @click="syncAndReload">
        <span class="material-symbols-outlined">sync</span>
      </button>
      <button type="button" class="icon-btn" aria-label="导出 CSV" @click="exportCsv">
        <span class="material-symbols-outlined">download</span>
      </button>
    </HeaderActionsPortal>

    <div class="summary-card">
      <div class="summary-main">
        <span class="summary-amount">¥{{ formatAmount(summary.amount) }}</span>
        <span class="summary-label">
          共 {{ summary.total }} 张 · 已归档 {{ summary.filed }}
          <template v-if="summary.downloaded > 0">· 文件 {{ summary.downloaded }}</template>
        </span>
      </div>
    </div>

    <div class="file-ops">
      <button class="chip" :class="{ active: selectMode }" @click="toggleSelectMode">
        {{ selectMode ? '取消选择' : '选择' }}
      </button>
      <button v-if="selectMode" class="chip" @click="selectAllDownloaded">选已下载</button>
      <div class="spacer" />
      <button class="chip export" :disabled="exporting || downloadableSelection().length === 0" @click="exportGrid(2)">
        {{ exporting ? '导出中…' : '导出 A4 2×2' }}
      </button>
      <button class="chip feishu" :disabled="pushing" @click="pushFeishu()">
        {{ pushing ? '推送中…' : '推送飞书' }}
      </button>
    </div>

    <ScrollChromePortal>
      <div class="filter-row">
        <button :class="['chip', { active: filter === '' }]" @click="filter = ''">全部</button>
        <button :class="['chip', { active: filter === 'new' }]" @click="filter = 'new'">待整理</button>
        <button :class="['chip', { active: filter === 'downloaded' }]" @click="filter = 'downloaded'">已下载</button>
        <button :class="['chip', { active: filter === 'filed' }]" @click="filter = 'filed'">已归档</button>
      </div>
    </ScrollChromePortal>

    <div v-if="error" class="status-err">{{ error }}</div>
    <main class="body">
      <div v-if="loading" class="state">加载中…</div>
      <div v-else-if="invoices.length === 0" class="state">
        <p>暂无发票记录</p>
        <p class="hint">邮箱同步后，账单/发票类邮件会自动提取到这里</p>
      </div>
      <InvoiceCard
        v-for="inv in invoices"
        :key="inv.id"
        :inv="inv"
        :thumb-url="thumbs[inv.id]"
        :select-mode="selectMode"
        :picked="selected.includes(inv.id)"
        :booking="bookingId === inv.id"
        :status-text="statusLabel(inv)"
        :amount="formatAmount(inv.amount)"
        :can-book="bookable(inv)"
        @preview="openPreview(inv)"
        @open-email="openEmail(inv)"
        @toggle-select="togglePick(inv.id)"
        @download="downloadInvoice(inv)"
        @book="book(inv)"
        @file="markFiled(inv)"
        @unfile="markNew(inv)"
        @remove="remove(inv)"
      />
      <div v-if="invoices.length > 0" ref="moreEl" class="more">
        <span v-if="loadingMore">加载中…</span>
        <span v-else-if="hasMore">上拉加载更多</span>
        <span v-else>没有更多了</span>
      </div>
    </main>
    <InvoicePreviewSheet
      :open="!!preview"
      :title="previewTitle"
      :src="previewSrc"
      :kind="previewKind"
      @close="closePreview"
      @open-email="preview && openEmail(preview.inv)"
      @download="preview && downloadInvoice(preview.inv)"
    />
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { wsClient } from '../../api/websocket'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'
import ScrollChromePortal from '../../components/layout/ScrollChromePortal.vue'
import InvoiceCard from './InvoiceCard.vue'
import InvoicePreviewSheet from './InvoicePreviewSheet.vue'
import { useInvoiceList } from './use-invoice-list'

const {
  loading, loadingMore, hasMore, syncing, exporting, pushing, error, filter, summary, bookingId,
  selectMode, selected, thumbs, preview, invoices, previewSrc, previewKind, previewTitle,
  formatAmount, statusLabel, bookable, toggleSelectMode, selectAllDownloaded, togglePick,
  downloadableSelection, openEmail, openPreview, closePreview, load, loadMore, runPipeline,
  syncAndReload, exportGrid, pushFeishu, downloadInvoice, markFiled, markNew, book,
  exportCsv, remove,
} = useInvoiceList()

const moreEl = ref<HTMLElement | null>(null)
let moreObs: IntersectionObserver | null = null

onMounted(() => {
  wsClient.on('email.invoice.extracted', load)
  wsClient.on('email.invoices.exported', load)
  void load()
  moreObs = new IntersectionObserver((ents) => {
    if (ents.some((e) => e.isIntersecting)) void loadMore()
  }, { rootMargin: '120px' })
})
watch(moreEl, (el, prev) => {
  if (!moreObs) return
  if (prev) moreObs.unobserve(prev)
  if (el) moreObs.observe(el)
})
onUnmounted(() => {
  wsClient.off('email.invoice.extracted', load)
  wsClient.off('email.invoices.exported', load)
  moreObs?.disconnect()
})
</script>

<style scoped>
.page { min-height: 100%; background: var(--bg-base); }
.icon-btn { background: none; border: none; color: var(--text-primary); display: flex; cursor: pointer; padding: 4px; }
.summary-card { margin: var(--space-3); padding: var(--space-3); background: var(--bg-card); border: 1px solid var(--border); border-radius: 12px; }
.summary-main { display: flex; flex-direction: column; gap: 2px; }
.summary-amount { font-size: 20px; font-weight: 700; }
.summary-label { font-size: 11px; color: var(--text-secondary); }
.filter-row, .file-ops { display: flex; gap: 6px; padding: 0 var(--space-3) var(--space-2); flex-wrap: wrap; align-items: center; }
.file-ops .spacer { flex: 1; }
.chip { padding: 5px 12px; font-size: 12px; background: var(--bg-subtle); border: 1px solid var(--border); border-radius: 999px; color: var(--text-secondary); cursor: pointer; }
.chip.active { background: var(--brand-primary, #4c8dff); color: #fff; border-color: transparent; }
.chip.export { color: var(--brand-primary, #4c8dff); }
.chip.feishu { color: var(--success, #10b981); }
.chip:disabled { opacity: 0.5; cursor: not-allowed; }
.status-err { margin: 0 var(--space-3) var(--space-2); padding: var(--space-2) var(--space-3); font-size: 12px; color: var(--danger); }
.body { padding: 0 var(--space-3) 100px; display: flex; flex-direction: column; gap: var(--space-2); }
.state { padding: 40px 20px; text-align: center; color: var(--text-secondary); }
.hint { font-size: 12px; margin-top: 8px; }
.more { padding: 16px 0 24px; text-align: center; font-size: 12px; color: var(--text-muted); }
</style>
