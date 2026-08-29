<!--
  GatewayCredentialDetailView — 单凭据的 per-model 节点状态。

  路由：/gateway/:nodeId/credentials/:credentialId
  上游 monitor-summary 带 credential_id 时进 detail 模式，返回 models[]，
  每项含 effective_state / probe_state / P95 / 成功率 / 数据来源。
  effective_state 的优先级（上游 deriveModelEffectiveState）：
    manual_disabled > probe_broken > offer_missing > binding_missing > available
-->
<template>
  <div class="gw-view">
    <HeaderActionsPortal>
      <span class="cred-label">{{ cred?.label || `凭据 #${credentialId}` }}</span>
      <button type="button" class="icon-btn" :disabled="loading" aria-label="刷新" @click="load">
        <span class="material-symbols-outlined">refresh</span>
      </button>
    </HeaderActionsPortal>

    <div v-if="error" class="status-bar status-err">{{ error }}</div>
    <div v-if="notice" class="status-bar status-ok">{{ notice }}</div>

    <main class="body">
      <div v-if="loading" class="state">加载中…</div>

      <template v-else-if="cred">
        <section class="card">
          <div class="sum-row">
            <span :class="['chip', stateChip(cred.availability_state)]">{{ cred.availability_state }}</span>
            <span class="chip">{{ cred.quota_state }}</span>
            <span v-if="cred.manual_disabled" class="chip chip-err">人工停用</span>
          </div>
          <div class="sum-meta">
            {{ cred.provider_name }} · 并发 {{ cred.effective_concurrency }} ·
            连续失败 {{ cred.consecutive_failures }} · 总请求 {{ cred.total_requests }}
          </div>
          <div v-if="cred.state_reason_detail" class="reason">{{ cred.state_reason_detail }}</div>
        </section>

        <!-- 状态筛选 -->
        <div class="filter-row">
          <button
            v-for="f in filters"
            :key="f.key"
            :class="['filter', { active: activeFilter === f.key }]"
            @click="activeFilter = f.key"
          >
            {{ f.label }}<span v-if="counts[f.key]" class="filter-count">{{ counts[f.key] }}</span>
          </button>
        </div>

        <div v-if="visibleModels.length === 0" class="state">该筛选下没有模型</div>

        <div v-else class="model-list">
          <div v-for="m in visibleModels" :key="m.raw_model_name" class="model-card">
            <div class="model-head">
              <span :class="['state-dot', `state-${m.effective_state}`]" />
              <div class="model-name">{{ m.raw_model_name }}</div>
              <span :class="['chip', 'chip-sm', stateChipForModel(m.effective_state)]">
                {{ stateText(m.effective_state) }}
              </span>
            </div>

            <div class="model-metrics">
              <span class="m">
                成功率
                <b v-if="m.recent_success_rate != null" :class="rateClass(m.recent_success_rate)">
                  {{ pct(m.recent_success_rate) }}
                </b>
                <b v-else class="muted">—</b>
              </span>
              <span class="m">
                P95
                <b v-if="m.p95_latency_ms">{{ m.p95_latency_ms }}ms</b>
                <b v-else class="muted">—</b>
              </span>
              <span class="m">
                调用 <b>{{ m.total_calls }}</b>
              </span>
            </div>

            <div class="model-tags">
              <span :class="['tag', m.data_source === 'live' ? 'tag-live' : 'tag-declared']">
                {{ m.data_source === 'live' ? '24h 内有调用' : '仅声明' }}
              </span>
              <span v-if="m.p95_source && m.p95_source !== 'no_data'" class="tag">
                P95 来源 {{ m.p95_source }}
              </span>
              <span v-if="m.probe_state && m.probe_state !== 'unknown'" :class="['tag', probeTagClass(m.probe_state)]">
                探测 {{ m.probe_state }}
              </span>
            </div>

            <div v-if="m.model_disabled_reason" class="model-reason">{{ m.model_disabled_reason }}</div>

            <div class="model-actions">
              <button
                v-if="m.effective_state === 'manual_disabled'"
                class="btn-ghost"
                :disabled="busyModel === m.raw_model_name"
                @click="toggle(m, 'online')"
              >
                {{ busyModel === m.raw_model_name ? '处理中…' : '上线' }}
              </button>
              <button
                v-else
                class="btn-ghost danger"
                :disabled="busyModel === m.raw_model_name"
                @click="toggle(m, 'offline')"
              >
                {{ busyModel === m.raw_model_name ? '处理中…' : '下线' }}
              </button>
            </div>
          </div>
        </div>
      </template>
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError } from '../../api/http'
import * as gw from '../../api/gateway'
import type { CredentialModelStatus, GatewayCredential, ModelEffectiveState } from '../../api/gateway'
import { useConfirm } from '../../composables/useConfirm'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'

const route = useRoute()
const router = useRouter()
const { confirm } = useConfirm()
const nodeId = Number(route.params.nodeId)
const credentialId = Number(route.params.credentialId)

const cred = ref<GatewayCredential | null>(null)
const loading = ref(true)
const error = ref('')
const notice = ref('')
const busyModel = ref<string | null>(null)
const activeFilter = ref<'all' | 'problem' | 'available' | 'live'>('all')

const filters = [
  { key: 'all' as const, label: '全部' },
  { key: 'problem' as const, label: '有问题' },
  { key: 'available' as const, label: '可用' },
  { key: 'live' as const, label: '活跃' },
]

async function load() {
  loading.value = true
  error.value = ''
  try {
    const res = await gw.getCredential(nodeId, credentialId)
    cred.value = res.credentials?.[0] ?? null
    if (!cred.value) error.value = '未找到该凭据'
  } catch (e: any) {
    error.value = e?.message || '加载失败'
  } finally {
    loading.value = false
  }
}

const models = computed<CredentialModelStatus[]>(() => cred.value?.models ?? [])

const counts = computed<Record<string, number>>(() => ({
  all: models.value.length,
  problem: models.value.filter((m) => m.effective_state !== 'available').length,
  available: models.value.filter((m) => m.effective_state === 'available').length,
  live: models.value.filter((m) => m.data_source === 'live').length,
}))

const visibleModels = computed(() => {
  switch (activeFilter.value) {
    case 'problem':
      return models.value.filter((m) => m.effective_state !== 'available')
    case 'available':
      return models.value.filter((m) => m.effective_state === 'available')
    case 'live':
      return models.value.filter((m) => m.data_source === 'live')
    default:
      return models.value
  }
})

async function toggle(m: CredentialModelStatus, action: 'online' | 'offline') {
  const verb = action === 'online' ? '上线' : '下线'
  if (!(await confirm({
    title: `${verb}模型`,
    message: `确认将 ${m.raw_model_name} ${verb}？会立即改变网关路由。`,
    confirmText: verb,
    danger: action === 'offline',
  }))) return

  busyModel.value = m.raw_model_name
  error.value = ''
  notice.value = ''
  try {
    await gw.toggleCredentialModel(nodeId, credentialId, m.raw_model_name, action, 'mobile ops')
    notice.value = `${m.raw_model_name} 已${verb}`
    await load()
  } catch (e: any) {
    if (e instanceof ApiError && e.status === 403) {
      error.value = '需要 pocket admin 角色才能变更模型状态'
    } else {
      error.value = e?.message || `${verb}失败`
    }
  } finally {
    busyModel.value = null
  }
}

const STATE_TEXT: Record<ModelEffectiveState, string> = {
  available: '可用',
  manual_disabled: '人工停用',
  probe_broken: '探测不通',
  offer_missing: '无 offer',
  binding_missing: '无绑定',
}

function stateText(s: ModelEffectiveState) {
  return STATE_TEXT[s] ?? s
}

function stateChipForModel(s: ModelEffectiveState) {
  return s === 'available' ? 'chip-ok' : s === 'manual_disabled' ? 'chip-muted' : 'chip-err'
}

function stateChip(s: string) {
  return s === 'ready' ? 'chip-ok' : 'chip-err'
}

function probeTagClass(s: string) {
  if (s === 'broken_confirmed') return 'tag-err'
  if (s === 'healthy_confirmed') return 'tag-ok'
  return 'tag-warn'
}

function pct(v: number) {
  return `${(v * 100).toFixed(1)}%`
}

function rateClass(v: number) {
  if (v >= 0.98) return 'ok'
  if (v >= 0.9) return 'warn'
  return 'err'
}

onMounted(load)
</script>

<style scoped>
.gw-view {
  min-height: 100%;
  background: var(--bg-base);
}
.icon-btn {
  background: none;
  border: none;
  color: var(--text-primary);
  display: flex;
}
.cred-label {
  font-size: var(--text-sm);
  color: var(--text-secondary);
  max-width: 140px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.status-bar {
  padding: var(--space-2) var(--space-3);
  font-size: 12px;
}
.status-err {
  background: color-mix(in srgb, var(--danger) 15%, transparent);
  color: var(--danger);
}
.status-ok {
  background: color-mix(in srgb, var(--success) 15%, transparent);
  color: var(--success);
}
.body {
  padding: var(--space-3);
}
.state {
  padding: 32px 20px;
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
.sum-row {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}
.sum-meta {
  font-size: 11px;
  color: var(--text-secondary);
  margin-top: 8px;
}
.reason {
  margin-top: 8px;
  font-size: 11px;
  color: var(--warning);
  word-break: break-word;
}
.chip {
  font-size: 11px;
  padding: 2px 7px;
  border-radius: var(--radius-full, 999px);
  background: var(--bg-subtle);
  color: var(--text-secondary);
}
.chip-sm {
  font-size: 10px;
}
.chip-ok {
  color: var(--success);
}
.chip-err {
  color: var(--danger);
}
.chip-muted {
  color: var(--text-secondary);
}
.filter-row {
  display: flex;
  gap: 6px;
  overflow-x: auto;
  margin-bottom: var(--space-2-5);
}
.filter {
  flex: none;
  padding: 6px 12px;
  font-size: 12px;
  background: var(--bg-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-full, 999px);
  color: var(--text-secondary);
}
.filter.active {
  background: var(--primary, #4c8dff);
  color: #fff;
  border-color: transparent;
}
.filter-count {
  margin-left: 4px;
  opacity: 0.75;
}
.model-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}
.model-card {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md, 10px);
  padding: var(--space-2-5) var(--space-3);
}
.model-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.model-name {
  flex: 1;
  font-size: 13px;
  font-weight: 600;
  word-break: break-all;
}
.state-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex: none;
}
.state-available {
  background: var(--success);
}
.state-probe_broken {
  background: var(--danger);
}
.state-offer_missing,
.state-binding_missing {
  background: var(--warning);
}
.state-manual_disabled {
  background: var(--text-secondary);
}
.model-metrics {
  display: flex;
  gap: 14px;
  margin-top: 8px;
}
.m {
  font-size: 10px;
  color: var(--text-secondary);
}
.m b {
  font-size: 12px;
  color: var(--text-primary);
  margin-left: 3px;
}
.m b.ok {
  color: var(--success);
}
.m b.warn {
  color: var(--warning);
}
.m b.err {
  color: var(--danger);
}
.m b.muted {
  color: var(--text-secondary);
}
.model-tags {
  display: flex;
  gap: 5px;
  margin-top: 8px;
  flex-wrap: wrap;
}
.tag {
  font-size: 10px;
  padding: 1px 6px;
  border-radius: var(--radius-full, 999px);
  background: var(--bg-subtle);
  color: var(--text-secondary);
}
.tag-live {
  color: var(--success);
}
.tag-declared {
  color: var(--text-secondary);
}
.tag-ok {
  color: var(--success);
}
.tag-warn {
  color: var(--warning);
}
.tag-err {
  color: var(--danger);
}
.model-reason {
  margin-top: 6px;
  font-size: 10px;
  color: var(--warning);
  word-break: break-word;
}
.model-actions {
  display: flex;
  gap: 8px;
  margin-top: 8px;
}
.btn-ghost {
  flex: 1;
  padding: 6px;
  font-size: 12px;
  background: var(--bg-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm, 8px);
  color: var(--text-primary);
}
.btn-ghost.danger {
  color: var(--danger);
}
</style>
