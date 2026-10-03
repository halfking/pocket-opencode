<!--
  InvoiceListView — 邮件发票自动整理列表。
  路由：/email/invoices；点卡片看原邮件，点缩略图看全图/文件。
-->
<template>
  <div class="page">
    <HeaderActionsPortal>
      <!--
        整理作业进行中：同一个位置换成「停止」。早先这里只有 disabled，
        于是用户看得见按钮在转圈、却没有任何办法终止；而这轮实测要跑 1m30s
        以上（后端 15 分钟预算），切页回来更是连转圈都看不见。
      -->
      <button
        v-if="pipelineRunning"
        type="button"
        class="icon-btn is-running"
        aria-label="停止整理"
        aria-busy="true"
        @click="cancelPipeline"
      >
        <span class="material-symbols-outlined">stop_circle</span>
      </button>
      <button
        v-else
        type="button"
        class="icon-btn"
        :disabled="syncing"
        aria-label="收信整理"
        @click="runPipeline"
      >
        <span class="material-symbols-outlined">auto_awesome</span>
      </button>
      <button type="button" class="icon-btn" :disabled="syncing || pipelineRunning" aria-label="同步" @click="syncAndReload">
        <span class="material-symbols-outlined">sync</span>
      </button>
      <button type="button" class="icon-btn" aria-label="导出 CSV" @click="exportCsv">
        <span class="material-symbols-outlined">download</span>
      </button>
    </HeaderActionsPortal>

    <div class="summary-card">
      <div class="summary-main">
        <span class="summary-amount">{{ summaryMoney() }}</span>
        <span class="summary-label">
          共 {{ summary.total }} 张 · 已归档 {{ summary.filed }}
          <template v-if="summary.downloaded > 0">· 文件 {{ summary.downloaded }}</template>
        </span>
      </div>
      <!--
        状态行：整理在后台跑时（可能已经跑了很久，用户中途切走过）这里必须有
        明确说明 + 停止入口。role="status" 让读屏也能听到。
      -->
      <p v-if="pipelineRunning" class="job-hint" role="status">
        <span>正在整理邮件…收信 → 清理 → 发票采集，通常需要 1~2 分钟</span>
        <button type="button" class="job-hint-stop" @click="cancelPipeline">停止</button>
      </p>
    </div>

    <div class="file-ops">
      <button class="chip" :class="{ active: selectMode }" @click="toggleSelectMode">
        {{ selectMode ? '取消选择' : '选择' }}
      </button>
      <button v-if="selectMode" class="chip" @click="selectAllDownloaded">选已下载</button>
      <a
        v-if="shareDocUrl"
        class="chip share-doc"
        :href="shareDocUrl"
        target="_blank"
        rel="noopener noreferrer"
      >共享台账</a>
      <div class="spacer" />
      <button
        class="chip export"
        :disabled="exporting || downloadableSelection().length === 0"
        @click="exportGrid(gridChoice)"
      >
        {{ exporting ? '导出中…' : `导出 A4 ${gridChoice}×${gridChoice}` }}
      </button>
      <!-- 2×2 / 3×3 都合法：3×3 每页 9 张，量大时省纸 -->
      <button
        class="chip"
        :class="{ active: gridChoice === 2 }"
        :disabled="exporting"
        @click="gridChoice = 2"
      >2×2</button>
      <button
        class="chip"
        :class="{ active: gridChoice === 3 }"
        :disabled="exporting"
        @click="gridChoice = 3"
      >3×3</button>
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
        :thumb-pending="!thumbs[inv.id] && thumbLoading.has(inv.id)"
        :select-mode="selectMode"
        :picked="selected.includes(inv.id)"
        :booking="bookingId === inv.id"
        :status-text="statusLabel(inv)"
        :amount="invoiceMoney(inv)"
        :can-book="bookable(inv)"
        :book-reason="bookBlockReason(inv)"
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
      :blob="previewBlob"
      :doc-key="previewKey"
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
import { useListScene } from '../../composables/use-list-scene'

defineOptions({ name: 'InvoiceListView' })

const {
  loading, loadingMore, hasMore, syncing, exporting, pushing, error, filter, summary, bookingId, shareDocUrl,
  pipelineRunning,
  selectMode, selected, thumbs, thumbLoading, preview, invoices, previewSrc, previewBlob, previewKey,
  previewKind, previewTitle,
  formatAmount, statusLabel, bookable, toggleSelectMode, selectAllDownloaded, togglePick,
  downloadableSelection, openEmail, openPreview, closePreview, load, loadMore, runPipeline,
  cancelPipeline, summaryMoney, invoiceMoney, bookBlockReason,
  syncAndReload, exportGrid, pushFeishu, downloadInvoice, markFiled, markNew, book,
  exportCsv, remove,
} = useInvoiceList()

/** A4 网格密度：2 = 每页 4 张，3 = 每页 9 张（服务端只接受这两个值）。 */
const gridChoice = ref<2 | 3>(2)

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
/* KeepAlive 现场保持：filter 筛选/选择集保留；返回时按需刷新 + 恢复滚动 */
useListScene('email-invoices', load)
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
.summary-label { font-size: var(--text-2xs); color: var(--text-secondary); }
.filter-row, .file-ops { display: flex; gap: 6px; padding: 0 var(--space-3) var(--space-2); flex-wrap: wrap; align-items: center; }
.file-ops .spacer { flex: 1; }
.chip { padding: 5px 12px; font-size: var(--text-sm); background: var(--bg-subtle); border: 1px solid var(--border); border-radius: 999px; color: var(--text-secondary); cursor: pointer; }
.chip.active { background: var(--brand-primary, #4c8dff); color: #fff; border-color: transparent; }
.chip.export { color: var(--brand-primary, #4c8dff); }
.chip.feishu { color: var(--success, #10b981); }
.chip:disabled { opacity: 0.5; cursor: not-allowed; }
.status-err { margin: 0 var(--space-3) var(--space-2); padding: var(--space-2) var(--space-3); font-size: var(--text-sm); color: var(--danger); }
.body { padding: 0 var(--space-3) 100px; display: flex; flex-direction: column; gap: var(--space-2); }
.state { padding: 40px 20px; text-align: center; color: var(--text-secondary); }
.hint { font-size: var(--text-sm); margin-top: 8px; }
.more { padding: 16px 0 24px; text-align: center; font-size: var(--text-sm); color: var(--text-muted); }
/* 整理作业进行中的状态行。放在 summary-card 里而不是 toast：toast 一闪而过，
   而这一轮实测要 1m30s 以上，切页回来还可能仍在跑，需要一个常驻位置。 */
.job-hint {
  display: flex; align-items: center; gap: var(--space-2);
  margin: var(--space-2) 0 0; padding-top: var(--space-2);
  border-top: 1px solid var(--border-color, var(--border));
  font-size: var(--text-sm); color: var(--text-secondary);
}
.job-hint .job-hint-stop {
  flex: none; border: none; background: none; padding: 0;
  color: var(--brand-primary, #4c8dff); font-size: var(--text-sm); font-weight: 600;
}
.icon-btn.is-running { color: var(--danger, #ef4444); }
</style>

