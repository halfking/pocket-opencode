<!--
  FinanceView — 记账（自动 + 手动）。

  路由：/finance
  数据源：/api/finance（PG）。支持：
    - 月度收支统计头部（本月收入/支出/结余）
    - 自然语言快速记账（「打车花了 32 元」→ 预览确认后入账）
    - 交易列表（来源标记：手动/语音/自动），删除
    - 笔记语音总结触发的自动记账（source=auto）在此统一可见
-->
<template>
  <div class="page">
    <HeaderActionsPortal>
      <button type="button" class="icon-btn" :disabled="loading" aria-label="刷新" @click="load">
        <span class="material-symbols-outlined">refresh</span>
      </button>
    </HeaderActionsPortal>

    <!-- 月度统计 -->
    <div class="stats-card">
      <div class="stat">
        <span class="stat-label">本月收入</span>
        <span class="stat-val income">+¥{{ fmt(stats.total_income) }}</span>
      </div>
      <div class="stat">
        <span class="stat-label">本月支出</span>
        <span class="stat-val expense">-¥{{ fmt(stats.total_expense) }}</span>
      </div>
      <div class="stat">
        <span class="stat-label">结余</span>
        <span class="stat-val" :class="stats.balance >= 0 ? 'income' : 'expense'">¥{{ fmt(stats.balance) }}</span>
      </div>
    </div>

    <!-- 自然语言快速记账 -->
    <div class="quick-add">
      <input
        v-model="quickText"
        class="quick-input"
        placeholder="记一笔：打车花了 32 元"
        @keyup.enter="quickParse"
      />
      <button class="quick-btn" :disabled="!quickText.trim() || quickBusy" @click="quickParse">
        {{ quickBusy ? '识别中…' : '记账' }}
      </button>
    </div>
    <div v-if="quickPreview" class="quick-preview">
      <span>{{ quickPreview.type === 'income' ? '收入' : '支出' }} · {{ quickPreview.category }} · ¥{{ fmt(quickPreview.amount) }}</span>
      <button class="pv-btn confirm" @click="quickConfirm">确认入账</button>
      <button class="pv-btn" @click="quickPreview = null">取消</button>
    </div>
    <div v-else-if="quickError" class="quick-preview err">{{ quickError }}</div>

    <div v-if="error" class="status-err">{{ error }}</div>
    <div v-if="statsError" class="stats-warn">{{ statsError }}</div>

    <!-- 分类占比（本月支出） -->
    <div v-if="categoryRows.length > 0" class="cat-row">
      <span
        v-for="c in categoryRows"
        :key="c.name"
        class="cat-chip"
      >{{ c.name }} ¥{{ fmt(c.value) }}</span>
    </div>

    <main class="body">
      <div v-if="loading" class="state">加载中…</div>
      <div v-else-if="txs.length === 0" class="state">
        <p>暂无账单</p>
        <p class="hint">手动记一笔，或在笔记里写「打车花了 32 元」生成总结时自动入账</p>
      </div>

      <article v-for="tx in txs" :key="tx.id" class="tx-card">
        <div class="tx-icon" :class="tx.type">{{ tx.type === 'income' ? '↓' : '↑' }}</div>
        <div class="tx-main">
          <div class="tx-top">
            <span class="tx-category">{{ tx.category }}</span>
            <span class="tx-amount" :class="tx.type">
              {{ tx.type === 'income' ? '+' : '-' }}¥{{ fmt(tx.amount) }}
            </span>
          </div>
          <div class="tx-meta">
            <span>{{ fmtDate(tx.created_at) }}</span>
            <span v-if="sourceLabel(tx.source)" class="src-badge">{{ sourceLabel(tx.source) }}</span>
          </div>
          <div v-if="tx.note" class="tx-note">{{ tx.note }}</div>
        </div>
        <button class="tx-del" aria-label="删除" @click="remove(tx)">
          <span class="material-symbols-outlined">delete</span>
        </button>
      </article>
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { financeApi, type FinanceStats, type FinanceTransaction, type FinanceParseResult } from '../../api/finance'
import { useToast } from '../../composables/useToast'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'

const toast = useToast()
const loading = ref(false)
const error = ref('')
const txs = ref<FinanceTransaction[]>([])
const stats = ref<FinanceStats>({ month: '', total_income: 0, total_expense: 0, balance: 0, by_category: {}, count: 0 })

const quickText = ref('')
const quickBusy = ref(false)
const quickPreview = ref<FinanceParseResult | null>(null)
const quickError = ref('')

const categoryRows = computed(() =>
  Object.entries(stats.value.by_category ?? {})
    .map(([name, value]) => ({ name, value }))
    .sort((a, b) => b.value - a.value)
    .slice(0, 6),
)

function fmt(n: number): string {
  return (n ?? 0).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

function fmtDate(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return `${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}

function sourceLabel(s?: string): string {
  if (s === 'auto') return '笔记自动'
  if (s === 'voice') return '语音'
  return ''
}

const statsError = ref('')

async function load() {
  loading.value = true
  error.value = ''
  statsError.value = ''
  try {
    // 本地时区拼「本月」，不能用 toISOString（UTC 会在月初凌晨错位到上月）
    const now = new Date()
    const month = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`
    const [listRes, statRes] = await Promise.all([
      financeApi.list(),
      financeApi.stats(month).catch(() => null),
    ])
    txs.value = listRes.transactions ?? []
    if (statRes) {
      stats.value = statRes
    } else {
      statsError.value = '统计加载失败，以下金额可能不准确'
    }
  } catch (e: any) {
    error.value = e?.message || '加载失败'
  } finally {
    loading.value = false
  }
}

// 输入变化后旧解析结果作废，防止「确认入账」入的是旧文本的账
watch(quickText, () => {
  quickPreview.value = null
  quickError.value = ''
})

async function quickParse() {
  const text = quickText.value.trim()
  if (!text) return
  quickBusy.value = true
  quickError.value = ''
  quickPreview.value = null
  try {
    quickPreview.value = await financeApi.parse(text)
  } catch {
    quickError.value = '未能识别金额或收支类型，试试「打车花了 32 元」'
  } finally {
    quickBusy.value = false
  }
}

async function quickConfirm() {
  const p = quickPreview.value
  if (!p) return
  try {
    await financeApi.create({
      type: p.type,
      amount: p.amount,
      category: p.category,
      note: p.note,
      source: 'manual',
    })
    toast.success('已入账')
    quickPreview.value = null
    quickText.value = ''
    await load()
  } catch (e: any) {
    toast.error(e?.message || '入账失败')
  }
}

async function remove(tx: FinanceTransaction) {
  try {
    await financeApi.remove(tx.id)
    txs.value = txs.value.filter((t) => t.id !== tx.id)
    await load()
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
.stats-card {
  display: grid; grid-template-columns: 1fr 1fr 1fr; gap: 8px;
  margin: var(--space-3); padding: var(--space-3);
  background: var(--bg-card); border: 1px solid var(--border); border-radius: 12px;
}
.stat { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
.stat-label { font-size: 11px; color: var(--text-secondary); }
.stat-val { font-size: 15px; font-weight: 700; color: var(--text-primary); word-break: break-all; }
.stat-val.income { color: var(--success, #10b981); }
.stat-val.expense { color: var(--danger); }
.quick-add {
  display: flex; gap: 8px; margin: 0 var(--space-3) var(--space-2);
}
.quick-input {
  flex: 1; padding: 10px 12px; font-size: 14px;
  background: var(--bg-card); border: 1px solid var(--border); border-radius: 10px;
  color: var(--text-primary); outline: none;
}
.quick-input:focus { border-color: var(--brand-primary); }
.quick-btn {
  flex: none; padding: 0 16px; font-size: 13px; border-radius: 10px; border: none;
  background: var(--brand-primary, #4c8dff); color: #fff; cursor: pointer;
}
.quick-btn:disabled { opacity: 0.5; }
.quick-preview {
  display: flex; align-items: center; gap: 8px; flex-wrap: wrap;
  margin: 0 var(--space-3) var(--space-2); padding: 8px 12px; font-size: 12px;
  background: var(--bg-card); border: 1px dashed var(--border); border-radius: 10px;
  color: var(--text-secondary);
}
.quick-preview.err { color: var(--warning, #f59e0b); border-color: var(--warning, #f59e0b); }
.pv-btn {
  margin-left: auto; padding: 5px 12px; font-size: 12px;
  background: var(--bg-subtle); border: 1px solid var(--border); border-radius: 8px;
  color: var(--text-primary); cursor: pointer;
}
.pv-btn.confirm { background: var(--brand-primary, #4c8dff); border-color: transparent; color: #fff; }
.pv-btn + .pv-btn { margin-left: 0; }
.status-err {
  margin: 0 var(--space-3) var(--space-2); padding: var(--space-2) var(--space-3); font-size: 12px;
  background: color-mix(in srgb, var(--danger) 12%, transparent); color: var(--danger); border-radius: 8px;
}
.stats-warn {
  margin: 0 var(--space-3) var(--space-2); padding: var(--space-2) var(--space-3); font-size: 12px;
  background: color-mix(in srgb, var(--warning, #f59e0b) 12%, transparent); color: var(--warning, #f59e0b); border-radius: 8px;
}
.cat-row { display: flex; gap: 6px; flex-wrap: wrap; padding: 0 var(--space-3) var(--space-2); }
.cat-chip {
  font-size: 11px; padding: 3px 9px; border-radius: 999px;
  background: var(--bg-subtle); border: 1px solid var(--border); color: var(--text-secondary);
}
.body { padding: 0 var(--space-3) 100px; display: flex; flex-direction: column; gap: var(--space-2); }
.state { padding: 40px 20px; text-align: center; color: var(--text-secondary); font-size: 14px; }
.hint { font-size: 12px; margin-top: 8px; }
.tx-card {
  display: flex; align-items: flex-start; gap: 10px;
  background: var(--bg-card); border: 1px solid var(--border); border-radius: 12px;
  padding: var(--space-3);
}
.tx-icon {
  flex: none; width: 32px; height: 32px; border-radius: 50%;
  display: flex; align-items: center; justify-content: center;
  font-size: 15px; font-weight: 700;
}
.tx-icon.expense { background: color-mix(in srgb, var(--danger) 12%, transparent); color: var(--danger); }
.tx-icon.income { background: color-mix(in srgb, var(--success, #10b981) 12%, transparent); color: var(--success, #10b981); }
.tx-main { flex: 1; min-width: 0; }
.tx-top { display: flex; align-items: baseline; justify-content: space-between; gap: 10px; }
.tx-category { font-size: 14px; font-weight: 600; color: var(--text-primary); }
.tx-amount { flex: none; font-size: 15px; font-weight: 700; }
.tx-amount.expense { color: var(--danger); }
.tx-amount.income { color: var(--success, #10b981); }
.tx-meta { display: flex; align-items: center; gap: 8px; margin-top: 4px; font-size: 11px; color: var(--text-secondary); }
.src-badge {
  padding: 1px 7px; border-radius: 999px; font-size: 10px;
  background: var(--bg-subtle); border: 1px solid var(--border);
}
.tx-note {
  margin-top: 4px; font-size: 11px; color: var(--text-muted);
  white-space: nowrap; overflow: hidden; text-overflow: ellipsis;
}
.tx-del {
  flex: none; background: none; border: none; color: var(--text-muted);
  cursor: pointer; padding: 2px;
}
.tx-del .material-symbols-outlined { font-size: 18px; }
</style>
