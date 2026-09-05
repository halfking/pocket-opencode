<!--
  InvoiceListView — 邮件发票自动整理列表。

  路由：/email/invoices
  数据源：/api/emails/invoices（后端规则提取：subject/snippet/缓存正文）。
  邮件同步 + AI 分类为 bill 后自动提取落库；本页提供浏览、归档、删除，
  以及「手动整理」（提取结果为空时引导去邮件详情触发）。
-->
<template>
  <div class="page">
    <HeaderActionsPortal>
      <button type="button" class="icon-btn" :disabled="loading" aria-label="刷新" @click="load">
        <span class="material-symbols-outlined">refresh</span>
      </button>
    </HeaderActionsPortal>

    <!-- 汇总 -->
    <div class="summary-card">
      <div class="summary-main">
        <span class="summary-amount">¥{{ formatAmount(summary.amount) }}</span>
        <span class="summary-label">共 {{ summary.total }} 张 · 已归档 {{ summary.filed }}</span>
      </div>
      <button class="sync-btn" :disabled="syncing" @click="syncAndReload">
        {{ syncing ? '同步中…' : '同步邮箱' }}
      </button>
    </div>

    <!-- 状态筛选 -->
    <div class="filter-row">
      <button :class="['chip', { active: filter === '' }]" @click="setFilter('')">全部</button>
      <button :class="['chip', { active: filter === 'new' }]" @click="setFilter('new')">待整理</button>
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
            <span class="inv-seller">{{ inv.seller || '未知销售方' }}</span>
            <span class="inv-amount">¥{{ formatAmount(inv.amount) }}</span>
          </div>
          <div class="inv-meta">
            <span class="cat-badge">{{ inv.category || '其他' }}</span>
            <span v-if="inv.invoiceDate">{{ inv.invoiceDate }}</span>
            <span v-if="inv.invoiceNo" class="mono">No.{{ inv.invoiceNo }}</span>
            <span v-if="inv.kind && inv.kind !== 'bill'" class="kind-badge">{{ kindLabel(inv.kind) }}</span>
          </div>
          <div v-if="inv.subject" class="inv-subject" :title="inv.subject">{{ inv.subject }}</div>
        </div>
        <div class="inv-actions">
          <span :class="['status-pill', inv.status]">
            {{ inv.status === 'filed' ? '已归档' : '待整理' }}
          </span>
          <button
            v-if="inv.status === 'new'"
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
import { computed, onMounted, ref } from 'vue'
import { emailApi, type EmailInvoice } from '../../api/email'
import * as invoiceStore from './invoices-store'
import { useToast } from '../../composables/useToast'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'

const toast = useToast()
const loading = ref(false)
const syncing = ref(false)
const error = ref('')
const filter = ref<'' | 'new' | 'filed'>('')
const all = ref<EmailInvoice[]>([])
const summary = ref({ total: 0, filed: 0, amount: 0 })

/** 展示列表 = 当前筛选下的子集（汇总永远基于全量）。 */
const invoices = computed(() =>
  filter.value ? all.value.filter((i) => i.status === filter.value) : all.value,
)

function applySummary(list: EmailInvoice[]) {
  summary.value = {
    total: list.length,
    filed: list.filter((i) => i.status === 'filed').length,
    amount: list.reduce((s, i) => s + (Number(i.amount) || 0), 0),
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

function setFilter(f: '' | 'new' | 'filed') {
  filter.value = f
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    // 服务端 SSOT：拉全量并重建本地镜像（离线兜底 + 下次首屏加速）
    const res = await emailApi.listInvoices()
    all.value = res.invoices ?? []
    applySummary(all.value)
    try {
      await invoiceStore.syncFromServer()
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
.status-pill.filed { color: var(--success, #10b981); }
.act-btn {
  margin-left: auto; padding: 5px 12px; font-size: 12px;
  background: var(--bg-subtle); border: 1px solid var(--border); border-radius: 8px;
  color: var(--text-primary); cursor: pointer;
}
.act-btn + .act-btn { margin-left: 0; }
.act-btn.danger { color: var(--danger); }
</style>
