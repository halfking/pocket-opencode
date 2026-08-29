<!--
  CostQuotaView — 移动端只读成本 / 配额面板。

  数据源：GET /api/llm/usage?days=N  +  GET /api/llm/quota
  不编辑预算（Store.Set 不暴露给前端）。
-->
<template>
  <div class="cost-view">
    <!-- 标题与返回由 AppLayout 统一渲染；时间范围 select 通过 Portal 注入壳层顶栏右侧 -->
    <HeaderActionsPortal>
      <select
        v-model.number="days"
        class="range-select"
        aria-label="时间范围"
        @change="load"
      >
        <option :value="1">今天</option>
        <option :value="7">7 天</option>
        <option :value="30">30 天</option>
      </select>
    </HeaderActionsPortal>

    <div v-if="!online" class="status-bar status-warn" role="status" aria-live="polite">
      当前离线，仅展示上次缓存数据。
    </div>
    <div v-if="topError" class="status-bar status-err" role="status" aria-live="polite">
      {{ topError }}
    </div>

    <main class="body">
      <div v-if="loading && !usage && !quota" class="state">加载中…</div>

      <template v-else>
        <!-- 用量汇总 -->
        <section class="card" aria-labelledby="usage-title">
          <h2 id="usage-title" class="card-title">用量汇总</h2>
          <div v-if="usageError" class="block-error">{{ usageError }}</div>
          <div v-else-if="usage && usage.call_count === 0" class="state state-inline">
            暂无调用记录 — 数据来源：LLM BFF 适配器路径（老 /api/llm/chat、/api/embed 仅写审计不记用量）。
          </div>
          <div v-else-if="usage" class="metric-grid">
            <div class="metric">
              <div class="metric-value" :aria-label="`调用次数 ${usage.call_count}`">
                {{ fmtNum(usage.call_count) }}
              </div>
              <div class="metric-label">调用次数</div>
            </div>
            <div class="metric">
              <div class="metric-value" :aria-label="`总 Tokens ${usage.total_tokens}`">
                {{ fmtNum(usage.total_tokens) }}
              </div>
              <div class="metric-label">Tokens</div>
            </div>
            <div class="metric">
              <div class="metric-value" :aria-label="`输入 Tokens ${usage.prompt_tokens}`">
                {{ fmtNum(usage.prompt_tokens) }}
              </div>
              <div class="metric-label">输入 Tokens</div>
            </div>
            <div class="metric">
              <div class="metric-value" :aria-label="`输出 Tokens ${usage.completion_tokens}`">
                {{ fmtNum(usage.completion_tokens) }}
              </div>
              <div class="metric-label">输出 Tokens</div>
            </div>
            <div class="metric metric-wide">
              <div
                class="metric-value cost"
                :aria-label="`估算成本 ${usage.total_cost_usd.toFixed(4)} 美元`"
              >
                ${{ usage.total_cost_usd.toFixed(4) }}
              </div>
              <div class="metric-label">估算成本（USD）</div>
            </div>
          </div>
          <p v-if="usage" class="footnote">
            范围：{{ usage.period_start }} → {{ usage.period_end }}
          </p>
        </section>

        <!-- 配额 -->
        <section class="card" aria-labelledby="quota-title">
          <h2 id="quota-title" class="card-title">
            配额
            <span :class="['chip', enforceChipClass]" :aria-label="enforceChipLabel">
              {{ enforceChipLabel }}
            </span>
          </h2>
          <div v-if="quotaError" class="block-error">{{ quotaError }}</div>
          <template v-else-if="quota">
            <div class="quota-meta">
              <div class="quota-meta-row">
                <span class="quota-meta-label">策略</span>
                <code class="quota-meta-value">{{ quota.strategy || '—' }}</code>
              </div>
              <div class="quota-meta-row">
                <span class="quota-meta-label">Workspace</span>
                <code class="quota-meta-value small">{{ quota.workspace_id || '—' }}</code>
              </div>
            </div>

            <div v-if="quota.budgets.length === 0" class="state state-inline">
              未配置预算 — 当前策略对所有调用放行。
            </div>
            <ul v-else class="budget-list">
              <li v-for="b in quota.budgets" :key="budgetKey(b)" class="budget-item">
                <div class="budget-head">
                  <span class="budget-kind">{{ kindLabel(b.kind) }}</span>
                  <span class="budget-limit">
                    上限：<strong>{{ formatLimit(b) }}</strong>
                  </span>
                </div>
                <div class="budget-period">
                  <template v-if="b.period_start && b.period_end">
                    {{ b.period_start }} → {{ b.period_end }}
                  </template>
                  <template v-else>无时段限制</template>
                </div>
              </li>
            </ul>
            <p class="footnote">
              预算由服务端配置；本视图只读。
            </p>
          </template>
        </section>

        <button
          class="refresh-btn"
          type="button"
          :disabled="loading || !online"
          @click="load"
        >
          <span v-if="!loading">↻ 刷新</span>
          <span v-else>刷新中…</span>
        </button>
      </template>
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import {
  llmBffApi,
  type QuotaBudget,
  type QuotaResponse,
  type UsageSummary,
} from '../../api/llm-bff'
import { useConnectivityStore } from '../../stores/connectivity'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'

const connectivity = useConnectivityStore()

const days = ref(7)
const loading = ref(true)
const topError = ref('')
const usage = ref<UsageSummary | null>(null)
const usageError = ref('')
const quota = ref<QuotaResponse | null>(null)
const quotaError = ref('')

const online = computed(() => connectivity.online)

const enforceChipClass = computed(() =>
  quota.value?.enforce_mode ? 'chip-err' : 'chip-ok',
)
const enforceChipLabel = computed(() =>
  quota.value?.enforce_mode ? '拒绝模式' : '审计模式',
)

let inflight: AbortController | null = null

async function load() {
  inflight?.abort()
  const ctrl = new AbortController()
  inflight = ctrl

  loading.value = true
  topError.value = ''
  usageError.value = ''
  quotaError.value = ''
  try {
    const [u, q] = await Promise.allSettled([
      llmBffApi.getUsage(days.value),
      llmBffApi.getQuota(),
    ])
    if (ctrl.signal.aborted) return
    if (u.status === 'fulfilled') {
      usage.value = u.value
    } else {
      usageError.value = `用量：${reasonMessage(u.reason)}`
    }
    if (q.status === 'fulfilled') {
      quota.value = q.value
    } else {
      quotaError.value = `配额：${reasonMessage(q.reason)}`
    }
    if (u.status === 'rejected' && q.status === 'rejected') {
      topError.value = '加载失败，请稍后重试'
    }
  } finally {
    if (inflight === ctrl) {
      loading.value = false
      inflight = null
    }
  }
}

function reasonMessage(reason: unknown): string {
  if (reason instanceof Error) return reason.message
  return String(reason)
}

function fmtNum(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return String(n)
}

function kindLabel(kind: QuotaBudget['kind']): string {
  switch (kind) {
    case 'tokens':
      return 'Tokens'
    case 'cost_usd':
      return '成本（USD）'
    case 'calls':
      return '调用次数'
  }
}

function formatLimit(b: QuotaBudget): string {
  if (b.kind === 'cost_usd') return `$${b.limit.toFixed(2)}`
  return fmtNum(b.limit)
}

// period_start/end 都为零值时（无时段限制），单 kind 不能保证唯一性；
// 加上 limit 防止同一 workspace 下多条同 kind 预算的 :key 冲突。
function budgetKey(b: QuotaBudget): string {
  return `${b.kind}|${b.limit}|${b.period_start ?? ''}|${b.period_end ?? ''}`
}

onMounted(load)
onUnmounted(() => {
  inflight?.abort()
  inflight = null
})
</script>

<style scoped>
.cost-view {
  min-height: 100%;
  background: var(--bg-base);
}
.range-select {
  background: var(--bg-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm, 8px);
  color: var(--text-primary);
  font-size: 13px;
  padding: var(--space-1) var(--space-2);
  min-height: 44px;
}
.range-select:focus-visible {
  outline: 2px solid var(--brand-primary);
  outline-offset: 2px;
}

.status-bar {
  padding: var(--space-2) var(--space-3);
  font-size: 13px;
}
.status-err {
  background: color-mix(in srgb, var(--danger) 15%, transparent);
  color: var(--danger);
}
.status-warn {
  background: color-mix(in srgb, var(--warning) 15%, transparent);
  color: var(--warning);
}

.body {
  padding: var(--space-3);
}

.state {
  padding: 40px 20px;
  text-align: center;
  color: var(--text-secondary);
  font-size: 14px;
}
.state-inline {
  padding: var(--space-3);
}

.card {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md, 10px);
  padding: var(--space-3);
  margin-bottom: var(--space-2-5);
}

.card-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--text-primary);
  margin: 0 0 var(--space-3);
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.metric-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: var(--space-3);
}

.metric {
  background: var(--bg-subtle);
  border-radius: var(--radius-sm, 8px);
  padding: var(--space-3);
}
.metric-wide {
  grid-column: span 2;
}

.metric-value {
  font-size: 20px;
  font-weight: 600;
  color: var(--text-primary);
  font-variant-numeric: tabular-nums;
}
.metric-value.cost {
  color: var(--brand-primary);
  font-size: 22px;
}

.metric-label {
  font-size: 11px;
  color: var(--text-secondary);
  margin-top: 2px;
}

.chip {
  font-size: 11px;
  padding: 2px 7px;
  border-radius: var(--radius-full, 999px);
  background: var(--bg-subtle);
}
.chip-ok {
  color: var(--success);
}
.chip-err {
  color: var(--danger);
}

.block-error {
  font-size: 12px;
  color: var(--danger);
  word-break: break-word;
}

.footnote {
  font-size: 11px;
  color: var(--text-secondary);
  text-align: center;
  margin: var(--space-3) 0 0;
}

.quota-meta {
  display: grid;
  gap: var(--space-1);
  margin-bottom: var(--space-3);
  padding-bottom: var(--space-3);
  border-bottom: 1px solid var(--border);
}
.quota-meta-row {
  display: flex;
  justify-content: space-between;
  font-size: 12px;
}
.quota-meta-label {
  color: var(--text-secondary);
}
.quota-meta-value {
  color: var(--text-primary);
  font-family: monospace;
}
.quota-meta-value.small {
  font-size: 11px;
  word-break: break-all;
  text-align: right;
  max-width: 60%;
}

.budget-list {
  list-style: none;
  padding: 0;
  margin: 0;
  display: grid;
  gap: var(--space-2);
}

.budget-item {
  background: var(--bg-subtle);
  border-radius: var(--radius-sm, 8px);
  padding: var(--space-2-5) var(--space-3);
}
.budget-head {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  font-size: 13px;
  color: var(--text-primary);
}
.budget-kind {
  font-weight: 600;
}
.budget-limit {
  font-size: 12px;
  color: var(--text-secondary);
}
.budget-limit strong {
  color: var(--text-primary);
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.budget-period {
  margin-top: 2px;
  font-size: 11px;
  color: var(--text-muted);
  font-family: monospace;
}

.refresh-btn {
  width: 100%;
  margin-top: var(--space-3);
  padding: var(--space-3);
  font-size: 14px;
  font-weight: 600;
  background: var(--bg-card);
  color: var(--brand-primary);
  border: 1px solid var(--border);
  border-radius: var(--radius-md, 10px);
  cursor: pointer;
  min-height: 44px;
  transition: background var(--duration-fast, 150ms) ease-out;
}
.refresh-btn:active {
  background: var(--bg-subtle);
}
.refresh-btn:focus-visible {
  outline: 2px solid var(--brand-primary);
  outline-offset: 2px;
}
.refresh-btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

@media (prefers-reduced-motion: reduce) {
  .range-select,
  .refresh-btn {
    transition: none;
  }
}
</style>