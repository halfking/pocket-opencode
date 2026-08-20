<!--
  GatewayOverviewView — 单节点汇总首屏。

  路由：/gateway/:nodeId
  一次 /overview 请求并发聚合 board + routingHealth + credentials，
  任一块失败只标记该卡片，其余照常显示。
-->
<template>
  <div class="gw-view">
    <header class="top-bar">
      <button class="back-btn" @click="router.back()" aria-label="返回">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>
      <h1 class="title">{{ node?.name || '网关' }}</h1>
      <select v-model.number="days" class="range-select" @change="load">
        <option :value="1">今天</option>
        <option :value="7">7 天</option>
        <option :value="30">30 天</option>
      </select>
    </header>

    <nav class="tab-nav">
      <button class="tab" @click="go('providers')">供应商</button>
      <button class="tab" @click="go('credentials')">凭据</button>
      <button class="tab" @click="go('models')">模型</button>
      <button class="tab" @click="go('live')">实时</button>
    </nav>

    <div v-if="error" class="status-bar status-err">{{ error }}</div>

    <main class="body">
      <div v-if="loading" class="state">加载中…</div>

      <template v-else>
        <!-- 汇总卡 -->
        <section class="card">
          <h2 class="card-title">
            请求汇总
            <span v-if="blockErrors.board" class="chip chip-warn">不可用</span>
          </h2>
          <div v-if="blockErrors.board" class="block-error">{{ blockErrors.board }}</div>
          <div v-else class="metric-grid">
            <div class="metric">
              <div class="metric-value">{{ fmtNum(summary.requests) }}</div>
              <div class="metric-label">请求数</div>
            </div>
            <div class="metric">
              <div class="metric-value">{{ fmtNum(summary.tokens) }}</div>
              <div class="metric-label">Tokens</div>
            </div>
            <div class="metric">
              <div class="metric-value">${{ summary.cost.toFixed(4) }}</div>
              <div class="metric-label">成本</div>
            </div>
            <div class="metric">
              <div class="metric-value" :class="successClass">{{ successText }}</div>
              <div class="metric-label">成功率</div>
            </div>
          </div>
        </section>

        <!-- 凭据健康 -->
        <section class="card">
          <h2 class="card-title">
            凭据健康
            <span v-if="blockErrors.credentials" class="chip chip-warn">不可用</span>
          </h2>
          <div v-if="blockErrors.credentials" class="block-error">{{ blockErrors.credentials }}</div>
          <template v-else>
            <div class="metric-grid">
              <div class="metric">
                <div class="metric-value">{{ credStats.total }}</div>
                <div class="metric-label">凭据总数</div>
              </div>
              <div class="metric">
                <div class="metric-value ok">{{ credStats.ready }}</div>
                <div class="metric-label">就绪</div>
              </div>
              <div class="metric">
                <div class="metric-value warn">{{ credStats.degraded }}</div>
                <div class="metric-label">降级</div>
              </div>
              <div class="metric">
                <div class="metric-value err">{{ credStats.disabled }}</div>
                <div class="metric-label">已禁用</div>
              </div>
            </div>
            <div v-if="credStats.brokenModels > 0" class="inline-warn">
              {{ credStats.brokenModels }} 个凭据×模型绑定被探测判定为不可用
            </div>
            <button class="link-btn" @click="go('credentials')">查看明细 →</button>
          </template>
        </section>

        <!-- 路由健康 -->
        <section class="card">
          <h2 class="card-title">
            路由健康
            <span v-if="blockErrors.routingHealth" class="chip chip-warn">不可用</span>
          </h2>
          <div v-if="blockErrors.routingHealth" class="block-error">{{ blockErrors.routingHealth }}</div>
          <pre v-else class="raw-json">{{ prettyRoutingHealth }}</pre>
        </section>

        <p class="footnote">数据生成于 {{ generatedAt }}</p>
      </template>
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import * as gw from '../../api/gateway'
import type { GatewayNode } from '../../api/gateway'

const route = useRoute()
const router = useRouter()
const nodeId = Number(route.params.nodeId)

const node = ref<GatewayNode | null>(null)
const days = ref(1)
const loading = ref(true)
const error = ref('')
const blockErrors = ref<Record<string, string>>({})
const board = ref<any>(null)
const routingHealth = ref<any>(null)
const credentials = ref<gw.GatewayCredential[]>([])
const generatedAt = ref('')

function go(section: string) {
  router.push(`/gateway/${nodeId}/${section}`)
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const res = await gw.getOverview(nodeId, days.value)
    node.value = res.node
    board.value = res.board ?? null
    routingHealth.value = res.routingHealth ?? null
    credentials.value = res.credentials?.credentials ?? []
    blockErrors.value = res.errors ?? {}
    generatedAt.value = new Date(res.generatedAt).toLocaleString()
  } catch (e: any) {
    error.value = e?.message || '加载失败'
  } finally {
    loading.value = false
  }
}

// board 的 summary 字段在 Redis 与 PG 两条路径上命名一致，
// 但缺失时要退回 0 而不是渲染 NaN。
const summary = computed(() => {
  const s = board.value?.summary ?? {}
  return {
    requests: Number(s.requests ?? s.total_requests ?? 0),
    tokens: Number(s.tokens ?? s.total_tokens ?? 0),
    cost: Number(s.cost_usd ?? s.cost ?? 0),
    success: Number(s.success ?? s.success_requests ?? 0),
  }
})

const successText = computed(() => {
  if (summary.value.requests === 0) return '—'
  return `${((summary.value.success / summary.value.requests) * 100).toFixed(1)}%`
})

const successClass = computed(() => {
  if (summary.value.requests === 0) return ''
  const rate = summary.value.success / summary.value.requests
  if (rate >= 0.98) return 'ok'
  if (rate >= 0.9) return 'warn'
  return 'err'
})

const credStats = computed(() => {
  const list = credentials.value
  return {
    total: list.length,
    ready: list.filter((c) => !c.manual_disabled && c.availability_state === 'ready').length,
    degraded: list.filter((c) => !c.manual_disabled && c.availability_state !== 'ready').length,
    disabled: list.filter((c) => c.manual_disabled).length,
    brokenModels: list.reduce((sum, c) => sum + (c.broken_model_count ?? 0), 0),
  }
})

const prettyRoutingHealth = computed(() => {
  if (!routingHealth.value) return '无数据'
  return JSON.stringify(routingHealth.value, null, 2)
})

function fmtNum(n: number) {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return String(n)
}

onMounted(load)
</script>

<style scoped>
.gw-view {
  min-height: 100vh;
  background: var(--bg-base);
}
.top-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: var(--space-3);
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
}
.back-btn {
  background: none;
  border: none;
  color: var(--text-primary);
  display: flex;
}
.title {
  flex: 1;
  font-size: 17px;
  font-weight: 600;
  margin: 0;
}
.range-select {
  background: var(--bg-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm, 8px);
  color: var(--text-primary);
  font-size: 13px;
  padding: 4px 6px;
}
.tab-nav {
  display: flex;
  gap: 6px;
  padding: var(--space-2) var(--space-3);
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
  overflow-x: auto;
}
.tab {
  flex: none;
  padding: 6px 12px;
  font-size: 13px;
  background: var(--bg-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-full, 999px);
  color: var(--text-primary);
}
.status-bar {
  padding: var(--space-2) var(--space-3);
  font-size: 13px;
}
.status-err {
  background: color-mix(in srgb, var(--danger) 15%, transparent);
  color: var(--danger);
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
  margin: 0 0 var(--space-3);
  display: flex;
  align-items: center;
  gap: 8px;
}
.metric-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: var(--space-3);
}
.metric-value {
  font-size: 20px;
  font-weight: 600;
}
.metric-value.ok {
  color: var(--success);
}
.metric-value.warn {
  color: var(--warning);
}
.metric-value.err {
  color: var(--danger);
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
.chip-warn {
  color: var(--warning);
}
.block-error {
  font-size: 12px;
  color: var(--danger);
  word-break: break-word;
}
.inline-warn {
  margin-top: var(--space-3);
  font-size: 12px;
  color: var(--warning);
}
.link-btn {
  margin-top: var(--space-3);
  background: none;
  border: none;
  padding: 0;
  color: var(--primary, #4c8dff);
  font-size: 13px;
}
.raw-json {
  font-size: 11px;
  color: var(--text-secondary);
  background: var(--bg-subtle);
  padding: var(--space-2-5);
  border-radius: var(--radius-sm, 8px);
  overflow-x: auto;
  margin: 0;
  max-height: 240px;
}
.footnote {
  font-size: 11px;
  color: var(--text-secondary);
  text-align: center;
}
</style>
