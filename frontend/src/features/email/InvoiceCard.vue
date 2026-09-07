<template>
  <article class="inv-card" :class="{ filed: inv.status === 'filed' }">
    <div class="row">
      <button
        type="button"
        class="thumb"
        :disabled="!hasFile"
        :aria-label="hasFile ? '查看发票文件' : '暂无发票文件'"
        @click.stop="emit('preview')"
      >
        <img v-if="thumbUrl" :src="thumbUrl" alt="" class="thumb-img">
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
          <span class="inv-amount">¥{{ amount }}</span>
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
        v-if="canBook"
        class="act-btn primary"
        type="button"
        :disabled="booking"
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
  selectMode: boolean
  picked: boolean
  booking: boolean
  statusText: string
  amount: string
  canBook: boolean
}>()

const emit = defineEmits<{
  preview: []
  'open-email': []
  'toggle-select': []
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
.thumb-img { width: 100%; height: 100%; object-fit: cover; }
.inv-main {
  flex: 1; min-width: 0; text-align: left; background: none; border: none;
  color: inherit; cursor: pointer; padding: 0;
}
.inv-top { display: flex; align-items: baseline; justify-content: space-between; gap: 10px; }
.inv-seller { font-size: 14px; font-weight: 600; color: var(--text-primary); word-break: break-all; }
.inv-amount { flex: none; font-size: 15px; font-weight: 700; }
.inv-meta {
  display: flex; align-items: center; gap: 8px; margin-top: 6px; flex-wrap: wrap;
  font-size: 11px; color: var(--text-secondary);
}
.mono { font-family: 'SF Mono', Menlo, monospace; }
.cat-badge {
  padding: 2px 8px; border-radius: 999px; font-size: 10px;
  background: var(--bg-subtle); border: 1px solid var(--border);
}
.inv-subject, .inv-file {
  margin-top: 6px; font-size: 11px; color: var(--text-muted);
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.inv-err { margin-top: 6px; font-size: 11px; color: var(--danger); word-break: break-all; }
.inv-actions { display: flex; align-items: center; gap: 8px; margin-top: 10px; flex-wrap: wrap; }
.status-pill { font-size: 11px; }
.status-pill.new, .status-pill.pending { color: var(--warning, #f59e0b); }
.status-pill.downloaded, .status-pill.filed { color: var(--success, #10b981); }
.status-pill.failed { color: var(--danger); }
.pick { display: flex; align-items: center; margin-right: 8px; }
.act-btn {
  padding: 5px 12px; font-size: 12px;
  background: var(--bg-subtle); border: 1px solid var(--border); border-radius: 8px;
  color: var(--text-primary); cursor: pointer;
}
.act-btn.primary { background: var(--brand-primary, #4c8dff); border-color: transparent; color: #fff; }
.act-btn.primary:disabled { opacity: 0.6; cursor: not-allowed; }
.act-btn.danger { color: var(--danger); }
</style>
