<!--
  GatewayLiveStreamView — 实时请求流（移动端竖向泳道）。

  路由：/gateway/:nodeId/live
  经 pocketd SSE 代理消费上游 /api/admin/live-stream。桌面端是横向泳道，
  手机宽度放不下，这里改成"每个泳道一行、tile 横向滚动"的竖向布局。
-->
<template>
  <div class="gw-view">
    <header class="top-bar">
      <button class="back-btn" @click="router.back()" aria-label="返回">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>
      <h1 class="title">实时请求</h1>
      <span :class="['conn-dot', connected ? 'conn-ok' : 'conn-off']" :title="connected ? '已连接' : '未连接'" />
    </header>

    <!-- 汇总 -->
    <div class="summary-bar">
      <span class="s-item">总 <b>{{ summary.total }}</b></span>
      <span class="s-item ok">成 <b>{{ summary.success }}</b></span>
      <span class="s-item err">败 <b>{{ summary.failure }}</b></span>
      <span class="s-item warn">限 <b>{{ summary.rate_limited }}</b></span>
      <span class="s-item">进行 <b>{{ summary.in_progress }}</b></span>
    </div>

    <!-- 维度切换 -->
    <div v-if="dimensionKeys.length > 0" class="dim-row">
      <button
        v-for="d in dimensionKeys"
        :key="d"
        :class="['dim', { active: activeDim === d }]"
        @click="activeDim = d"
      >
        {{ dimLabel(d) }}
      </button>
    </div>

    <div v-if="error" class="status-bar status-err">{{ error }}</div>

    <main class="body">
      <div v-if="!connected && lanes.length === 0" class="state">连接中…</div>
      <div v-else-if="lanes.length === 0" class="state">
        <p>暂无请求</p>
        <p class="hint">有新请求时会自动出现</p>
      </div>

      <div v-else class="lane-list">
        <div v-for="lane in lanes" :key="lane.id" class="lane">
          <div class="lane-head">
            <div class="lane-name">{{ lane.name }}</div>
            <div class="lane-stats">
              <span class="ok">{{ lane.stats.success }}</span>
              <span class="err">{{ lane.stats.failure }}</span>
              <span class="muted">/{{ lane.stats.total }}</span>
            </div>
          </div>
          <div class="tile-track">
            <span
              v-for="t in lane.requests.slice(0, 60)"
              :key="t.request_id"
              :class="['tile', `tile-${t.status}`, { probe: t.is_probe }]"
              :title="tileTitle(t)"
              @click="selected = t"
            />
          </div>
        </div>
      </div>
    </main>

    <!-- tile 详情 -->
    <div v-if="selected" class="sheet-mask" @click.self="selected = null">
      <div class="sheet">
        <h2 class="sheet-title">请求详情</h2>
        <dl class="detail">
          <dt>模型</dt><dd>{{ selected.model || '—' }}</dd>
          <dt>供应商</dt><dd>{{ selected.provider || selected.vendor || '—' }}</dd>
          <dt>状态</dt><dd :class="statusClass(selected.status)">{{ selected.status }}</dd>
          <dt v-if="selected.error_kind">错误</dt>
          <dd v-if="selected.error_kind" class="err">{{ selected.error_kind }}</dd>
          <dt>延迟</dt><dd>{{ selected.latency_ms != null ? selected.latency_ms + 'ms' : '—' }}</dd>
          <dt>Tokens</dt>
          <dd>{{ (selected.prompt_tokens ?? 0) + (selected.completion_tokens ?? 0) || '—' }}</dd>
          <dt>成本</dt><dd>{{ selected.cost_usd != null ? '$' + selected.cost_usd.toFixed(6) : '—' }}</dd>
          <dt>时间</dt><dd>{{ new Date(selected.timestamp).toLocaleString() }}</dd>
          <dt v-if="selected.is_probe">探测</dt>
          <dd v-if="selected.is_probe">{{ selected.probe_origin }} #{{ selected.probe_attempt }}</dd>
        </dl>
        <button class="btn-primary" @click="selected = null">关闭</button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '../../stores/auth'
import { GatewayLiveClient } from '../../api/gateway-live'
import type { LiveStreamLane, LiveStreamStats, LiveStreamTile } from '../../api/gateway-live'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const nodeId = Number(route.params.nodeId)

const connected = ref(false)
const error = ref('')
const summary = ref<LiveStreamStats>({ total: 0, success: 0, failure: 0, rate_limited: 0, in_progress: 0 })
const dimensions = ref<Record<string, LiveStreamLane[]>>({})
const activeDim = ref('')
const selected = ref<LiveStreamTile | null>(null)

let client: GatewayLiveClient | null = null

const dimensionKeys = computed(() => Object.keys(dimensions.value))

const lanes = computed(() => {
  const dim = activeDim.value || dimensionKeys.value[0]
  return dim ? (dimensions.value[dim] ?? []) : []
})

const DIM_LABELS: Record<string, string> = {
  model: '模型',
  provider: '供应商',
  vendor: '厂商',
  status: '状态',
  tenant: '租户',
  client: '客户端',
}

function dimLabel(d: string) {
  return DIM_LABELS[d] ?? d
}

function tileTitle(t: LiveStreamTile) {
  const parts = [t.model || t.provider || t.status]
  if (t.latency_ms != null) parts.push(`${t.latency_ms}ms`)
  if (t.error_kind) parts.push(t.error_kind)
  return parts.join(' · ')
}

function statusClass(s: string) {
  if (s === 'success') return 'ok'
  if (s === 'failure') return 'err'
  return ''
}

// changed_lanes 是增量：只包含发生变化的泳道，需要按 lane.id 合并进现有状态，
// 整段替换会让没变化的泳道消失。
function mergeLanes(changed: Record<string, LiveStreamLane[]>) {
  const next = { ...dimensions.value }
  for (const [dim, changedLanes] of Object.entries(changed)) {
    const existing = next[dim] ? [...next[dim]] : []
    for (const lane of changedLanes) {
      const idx = existing.findIndex((l) => l.id === lane.id)
      if (idx >= 0) existing[idx] = lane
      else existing.push(lane)
    }
    next[dim] = existing
  }
  dimensions.value = next
}

onMounted(() => {
  client = new GatewayLiveClient(
    nodeId,
    () => auth.token,
    {
      onOpen: () => {
        connected.value = true
        error.value = ''
      },
      onEnvelope: (env) => {
        if (env.snapshot) {
          // 优先用 detail_dimensions（含每条 tile 的完整字段）。
          const dims = env.snapshot.detail_dimensions ?? env.snapshot.dimensions ?? {}
          dimensions.value = dims
          summary.value = env.snapshot.summary ?? summary.value
          if (!activeDim.value) activeDim.value = Object.keys(dims)[0] ?? ''
        }
        if (env.delta) {
          if (env.delta.changed_lanes) mergeLanes(env.delta.changed_lanes)
          if (env.delta.summary) summary.value = env.delta.summary
        }
      },
      onError: () => {
        connected.value = false
        error.value = '连接中断，正在重试…'
      },
    },
  )
  client.open()
})

onUnmounted(() => {
  client?.close()
  client = null
})
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
.conn-dot {
  width: 9px;
  height: 9px;
  border-radius: 50%;
}
.conn-ok {
  background: var(--success);
}
.conn-off {
  background: var(--text-secondary);
}
.summary-bar {
  display: flex;
  gap: 14px;
  padding: var(--space-2-5) var(--space-3);
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
  overflow-x: auto;
}
.s-item {
  font-size: 10px;
  color: var(--text-secondary);
  flex: none;
}
.s-item b {
  font-size: 14px;
  color: var(--text-primary);
  margin-left: 3px;
}
.s-item.ok b {
  color: var(--success);
}
.s-item.err b {
  color: var(--danger);
}
.s-item.warn b {
  color: var(--warning);
}
.dim-row {
  display: flex;
  gap: 6px;
  padding: var(--space-2) var(--space-3);
  overflow-x: auto;
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
}
.dim {
  flex: none;
  padding: 5px 11px;
  font-size: 12px;
  background: var(--bg-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-full, 999px);
  color: var(--text-secondary);
}
.dim.active {
  background: var(--primary, #4c8dff);
  color: #fff;
  border-color: transparent;
}
.status-bar {
  padding: var(--space-2) var(--space-3);
  font-size: 12px;
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
.hint {
  font-size: 12px;
  margin-top: 8px;
}
.lane-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2-5);
}
.lane {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md, 10px);
  padding: var(--space-2-5);
}
.lane-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 7px;
}
.lane-name {
  font-size: 12px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.lane-stats {
  font-size: 10px;
  display: flex;
  gap: 4px;
  flex: none;
}
.lane-stats .ok {
  color: var(--success);
}
.lane-stats .err {
  color: var(--danger);
}
.lane-stats .muted {
  color: var(--text-secondary);
}
.tile-track {
  display: flex;
  gap: 2px;
  overflow-x: auto;
  padding-bottom: 2px;
}
.tile {
  width: 9px;
  height: 20px;
  border-radius: 2px;
  flex: none;
  background: var(--text-secondary);
}
.tile-success {
  background: var(--success);
}
.tile-failure {
  background: var(--danger);
}
.tile-in_progress {
  background: var(--warning);
}
.tile-idle {
  background: var(--bg-subtle);
}
.tile.probe {
  outline: 1px dashed var(--text-secondary);
  outline-offset: -1px;
}
.sheet-mask {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.45);
  display: flex;
  align-items: flex-end;
  z-index: var(--z-base);
}
.sheet {
  width: 100%;
  background: var(--bg-card);
  border-radius: 14px 14px 0 0;
  padding: var(--space-4) var(--space-3) var(--space-5);
}
.sheet-title {
  font-size: 16px;
  font-weight: 600;
  margin: 0 0 var(--space-3);
}
.detail {
  display: grid;
  grid-template-columns: 72px 1fr;
  gap: 7px 12px;
  margin: 0 0 var(--space-4);
  font-size: 12px;
}
.detail dt {
  color: var(--text-secondary);
}
.detail dd {
  margin: 0;
  word-break: break-all;
}
.detail dd.ok {
  color: var(--success);
}
.detail dd.err {
  color: var(--danger);
}
.btn-primary {
  width: 100%;
  padding: 11px;
  font-size: 14px;
  background: var(--primary, #4c8dff);
  border: none;
  border-radius: var(--radius-sm, 8px);
  color: #fff;
}
</style>
