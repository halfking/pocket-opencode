<template>
  <article class="inv-card" :class="{ filed: inv.status === 'filed' }">
    <div class="row">
      <button
        type="button"
        class="thumb"
        :disabled="!hasFile"
        :aria-label="hasFile ? '查看发票详情' : '暂无发票文件'"
        @click.stop="emit('preview')"
      >
        <img
          v-if="thumbUrl"
          :src="thumbUrl"
          alt=""
          class="thumb-img"
          loading="lazy"
          decoding="async"
          @load="emit('thumb-loaded', inv.id)"
        >
        <span v-else-if="thumbPending" class="material-symbols-outlined thumb-busy">hourglass_top</span>
        <span v-else class="material-symbols-outlined">{{ hasFile ? 'description' : 'hide_image' }}</span>
      </button>
      <button type="button" class="inv-main" @click="emit('open-email')">
        <div class="inv-top">
          <label v-if="selectMode" class="pick" @click.stop>
            <input
              type="checkbox"
              :checked="picked"
              :disabled="!hasFile"
              @change="emit('toggle-select')"
            >
          </label>
          <span class="inv-seller">{{ inv.seller || '未知销售方' }}</span>
          <span class="inv-amount">{{ amount }}</span>
        </div>
        <div class="inv-meta">
          <span class="cat-badge">{{ inv.category || '其他' }}</span>
          <span>{{ issueLabel }}</span>
          <span v-if="receivedLabel">{{ receivedLabel }}</span>
          <span v-if="inv.invoiceNo" class="mono">No.{{ inv.invoiceNo }}</span>
        </div>
        <div v-if="inv.fileName" class="inv-file mono">{{ inv.fileName }}</div>
        <div v-else-if="inv.status === 'failed'" class="inv-err">失败：{{ inv.lastError || '无法获取文件' }}</div>
        <div v-if="inv.subject" class="inv-subject">{{ inv.subject }}</div>
      </button>
    </div>
    <div class="inv-actions" @click.stop>
      <span :class="['status-pill', inv.status]">{{ statusText }}</span>
      <button v-if="hasFile" class="act-btn" type="button" @click="emit('download')">下载</button>
      <button
        v-if="showBook"
        class="act-btn primary"
        type="button"
        :disabled="booking || !canBook"
        :title="bookReason || undefined"
        @click="emit('book')"
      >{{ booking ? '入账中…' : '入账' }}</button>
      <button v-if="inv.status !== 'filed'" class="act-btn" type="button" @click="emit('file')">归档</button>
      <button v-else class="act-btn" type="button" @click="emit('unfile')">取消归档</button>
      <button class="act-btn danger" type="button" @click="emit('remove')">删除</button>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { EmailInvoice } from '../../api/email'
import { invoiceHasFile, invoiceIssueDateLabel, invoiceReceivedLabel } from './invoice-list'

const props = defineProps<{
  inv: EmailInvoice
  thumbUrl?: string
  /** 缩略图还在取（含原生 PDF 渲染）时为真，避免闪一下「无图」图标。 */
  thumbPending?: boolean
  selectMode: boolean
  picked: boolean
  booking: boolean
  statusText: string
  amount: string
  canBook: boolean
  /** 不能入账的原因（空串=可以入账）。外币发票要能看到原因，而不是按钮凭空消失。 */
  bookReason?: string
}>()

// 完全没有金额的发票（采集失败/待整理）本来就没有入账入口，不显示按钮。
// 但**有金额却因币种被挡**的必须显示成禁用态 + 原因，否则用户以为功能坏了。
const showBook = computed(() => props.canBook || !!props.amount)

const emit = defineEmits<{
  preview: []
  'open-email': []
  'toggle-select': []
  'thumb-loaded': [id: string]
  download: []
  book: []
  file: []
  unfile: []
  remove: []
}>()

const hasFile = computed(() => invoiceHasFile(props.inv))
const issueLabel = computed(() => invoiceIssueDateLabel(props.inv.invoiceDate))
const receivedLabel = computed(() => invoiceReceivedLabel(props.inv.emailDate, props.inv.createdAt))
</script>

<style scoped>
.inv-card {
  background: var(--bg-card); border: 1px solid var(--border); border-radius: 12px;
  padding: var(--space-3);
}
.inv-card.filed { opacity: 0.75; }
.row { display: flex; gap: 10px; align-items: flex-start; }
.thumb {
  flex: none; width: 64px; height: 80px; padding: 0; overflow: hidden;
  border: 1px solid var(--border); border-radius: 8px;
  background: var(--bg-subtle); color: var(--text-muted); cursor: pointer;
  display: flex; align-items: center; justify-content: center;
}
.thumb:disabled { cursor: default; opacity: 0.7; }
/*
 * 缩略图现在可能是「PDF 第 1 页的栅格化结果」（A4 竖版，比例 ≈ 1:1.41），
 * 而槽位是 64×80（1:1.25）。用 contain 而不是 cover——cover 会把发票的
 * 上下两截（抬头/金额）裁掉，用户看到的就是一张认不出内容的图。
 */
.thumb-img {
  width: 100%; height: 100%;
  object-fit: contain;
  object-position: top center;
  background: #fff;
}
.thumb-busy { animation: thumb-spin 1.2s linear infinite; opacity: 0.5; }
@keyframes thumb-spin { to { transform: rotate(360deg); } }
.inv-main {
  flex: 1; min-width: 0; text-align: left; background: none; border: none;
  color: inherit; cursor: pointer; padding: 0;
}
.inv-top { display: flex; align-items: baseline; justify-content: space-between; gap: 10px; }
.inv-seller { font-size: var(--text-base); font-weight: 600; color: var(--text-primary); word-break: break-all; }
.inv-amount { flex: none; font-size: var(--text-md); font-weight: 700; }
.inv-meta {
  display: flex; align-items: center; gap: 8px; margin-top: 6px; flex-wrap: wrap;
  font-size: var(--text-2xs); color: var(--text-secondary);
}
.mono { font-family: var(--font-mono); }
.cat-badge {
  padding: 2px 8px; border-radius: 999px; font-size: var(--text-xs);
  background: var(--bg-subtle); border: 1px solid var(--border);
}
.inv-subject, .inv-file {
  margin-top: 6px; font-size: var(--text-2xs); color: var(--text-muted);
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.inv-err { margin-top: 6px; font-size: var(--text-2xs); color: var(--danger); word-break: break-all; }
.inv-actions { display: flex; align-items: center; gap: 8px; margin-top: 10px; flex-wrap: wrap; }
.status-pill { font-size: var(--text-2xs); }
.status-pill.new, .status-pill.pending { color: var(--warning, #f59e0b); }
.status-pill.downloaded, .status-pill.filed { color: var(--success, #10b981); }
.status-pill.failed { color: var(--danger); }
.pick { display: flex; align-items: center; margin-right: 8px; }
.act-btn {
  padding: 5px 12px; font-size: var(--text-sm);
  background: var(--bg-subtle); border: 1px solid var(--border); border-radius: 8px;
  color: var(--text-primary); cursor: pointer;
}
.act-btn.primary { background: var(--brand-primary, #4c8dff); border-color: transparent; color: #fff; }
.act-btn.primary:disabled { opacity: 0.6; cursor: not-allowed; }
.act-btn.danger { color: var(--danger); }
</style>
