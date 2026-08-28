<!--
  GatewayCredentialsView — 凭据列表（列表模式，不含 per-model 明细）。

  路由：/gateway/:nodeId/credentials?provider_id=N
  上游 monitor-summary 在不带 credential_id 时只返回聚合计数，
  这样列表页保持轻量；点进单条才拉 models[]。
-->
<template>
  <div class="gw-view">
    <header class="top-bar">
      <button class="back-btn" @click="router.back()" aria-label="返回">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>
      <h1 class="title">凭据</h1>
      <button class="icon-btn" :disabled="loading" @click="load" aria-label="刷新">
        <span class="material-symbols-outlined">refresh</span>
      </button>
    </header>

    <div v-if="error" class="status-bar status-err">{{ error }}</div>
    <div v-if="notice" class="status-bar status-ok">{{ notice }}</div>

    <main class="body">
      <div v-if="loading" class="state">加载中…</div>
      <div v-else-if="credentials.length === 0 && !error" class="state">没有凭据</div>

      <div v-else class="list">
        <div v-for="c in credentials" :key="c.id" class="card">
          <div class="card-head" @click="openDetail(c)">
            <span :class="['health-dot', healthClass(c)]" />
            <div class="name">
              {{ c.label || `#${c.id}` }}
              <span v-if="c.manual_disabled" class="chip chip-err">人工停用</span>
            </div>
            <span class="material-symbols-outlined chevron">chevron_right</span>
          </div>

          <div class="sub">{{ c.provider_name }} · {{ c.availability_state }} · {{ c.quota_state }}</div>

          <div class="stats">
            <span class="stat">
              模型 <b>{{ c.model_available ?? 0 }}/{{ c.model_total ?? 0 }}</b>
            </span>
            <span v-if="c.broken_model_count" class="stat">
              探测坏 <b class="err">{{ c.broken_model_count }}</b>
            </span>
            <span v-if="c.aggregated_success_rate != null" class="stat">
              成功率 <b :class="rateClass(c.aggregated_success_rate)">{{ pct(c.aggregated_success_rate) }}</b>
            </span>
            <span class="stat">并发 <b>{{ c.effective_concurrency }}</b></span>
          </div>

          <div v-if="c.state_reason_detail" class="reason">{{ c.state_reason_detail }}</div>

          <!-- 写操作：非 admin 时后端返回 403，这里按钮照常显示但会提示 -->
          <div class="actions">
            <button
              v-if="c.manual_disabled"
              class="btn-ghost"
              :disabled="busy === c.id"
              @click="act(c, 'clear')"
            >
              {{ busy === c.id ? '处理中…' : '恢复' }}
            </button>
            <button v-else class="btn-ghost danger" :disabled="busy === c.id" @click="act(c, 'disable')">
              {{ busy === c.id ? '处理中…' : '停用' }}
            </button>
            <button class="btn-ghost" :disabled="busy === c.id" @click="act(c, 'promote')">上线</button>
            <button class="btn-ghost" :disabled="busy === c.id" @click="act(c, 'demote')">下线</button>
          </div>
        </div>
      </div>

      <p v-if="meta" class="footnote">
        {{ meta.cache_hit ? '命中上游 30s 缓存' : '实时查询' }} · {{ new Date(meta.generated_at).toLocaleTimeString() }}
      </p>
    </main>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError } from '../../api/http'
import * as gw from '../../api/gateway'
import type { GatewayCredential } from '../../api/gateway'
import { useConfirm } from '../../composables/useConfirm'

const route = useRoute()
const router = useRouter()
const { confirm } = useConfirm()
const nodeId = Number(route.params.nodeId)
const providerId = route.query.provider_id ? Number(route.query.provider_id) : undefined

const credentials = ref<GatewayCredential[]>([])
const meta = ref<any>(null)
const loading = ref(true)
const error = ref('')
const notice = ref('')
const busy = ref<number | null>(null)

async function load() {
  loading.value = true
  error.value = ''
  try {
    const res = await gw.listCredentials(nodeId, providerId)
    credentials.value = res.credentials ?? []
    meta.value = res.meta ?? null
  } catch (e: any) {
    error.value = e?.message || '加载失败'
  } finally {
    loading.value = false
  }
}

function openDetail(c: GatewayCredential) {
  router.push(`/gateway/${nodeId}/credentials/${c.id}`)
}

async function act(c: GatewayCredential, kind: 'promote' | 'demote' | 'disable' | 'clear') {
  const labels: Record<typeof kind, string> = {
    promote: '上线',
    demote: '下线',
    disable: '停用',
    clear: '恢复',
  }
  if (!(await confirm({
    title: `${labels[kind]}凭据`,
    message: `确认对「${c.label || c.id}」执行${labels[kind]}？该操作会立即改变网关路由。`,
    confirmText: labels[kind],
    danger: kind === 'disable' || kind === 'demote',
  }))) {
    return
  }

  busy.value = c.id
  error.value = ''
  notice.value = ''
  try {
    if (kind === 'promote') await gw.promoteCredential(nodeId, c.id, 'mobile ops')
    else if (kind === 'demote') await gw.demoteCredential(nodeId, c.id, 'mobile ops')
    else if (kind === 'disable') await gw.setManualDisabled(nodeId, c.id, 'mobile ops')
    else await gw.clearManualDisabled(nodeId, c.id)

    notice.value = `${labels[kind]}成功`
    await load()
  } catch (e: any) {
    if (e instanceof ApiError && e.status === 403) {
      error.value = '需要 pocket admin 角色才能变更网关状态'
    } else {
      error.value = e?.message || `${labels[kind]}失败`
    }
  } finally {
    busy.value = null
  }
}

function healthClass(c: GatewayCredential) {
  if (c.manual_disabled) return 'health-off'
  if (c.availability_state !== 'ready') return 'health-error'
  if (c.health_status === 'warning' || (c.broken_model_count ?? 0) > 0) return 'health-warn'
  return 'health-ok'
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
.back-btn,
.icon-btn {
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
  padding: 40px 20px;
  text-align: center;
  color: var(--text-secondary);
  font-size: 14px;
}
.list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2-5);
}
.card {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md, 10px);
  padding: var(--space-3);
}
.card-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.name {
  flex: 1;
  font-size: 15px;
  font-weight: 600;
  display: flex;
  align-items: center;
  gap: 6px;
}
.chevron {
  color: var(--text-secondary);
  font-size: 20px;
}
.health-dot {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  flex: none;
}
.health-ok {
  background: var(--success);
}
.health-warn {
  background: var(--warning);
}
.health-error {
  background: var(--danger);
}
.health-off {
  background: var(--text-secondary);
}
.sub {
  font-size: 12px;
  color: var(--text-secondary);
  margin-top: 4px;
}
.chip {
  font-size: 11px;
  padding: 2px 7px;
  border-radius: var(--radius-full, 999px);
  background: var(--bg-subtle);
}
.chip-err {
  color: var(--danger);
}
.stats {
  display: flex;
  gap: 12px;
  margin-top: 10px;
  flex-wrap: wrap;
}
.stat {
  font-size: 11px;
  color: var(--text-secondary);
}
.stat b {
  font-size: 13px;
  color: var(--text-primary);
}
.stat b.ok {
  color: var(--success);
}
.stat b.warn {
  color: var(--warning);
}
.stat b.err {
  color: var(--danger);
}
.reason {
  margin-top: 8px;
  font-size: 11px;
  color: var(--warning);
  word-break: break-word;
}
.actions {
  display: flex;
  gap: 8px;
  margin-top: 10px;
}
.btn-ghost {
  flex: 1;
  padding: 7px;
  font-size: 13px;
  background: var(--bg-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm, 8px);
  color: var(--text-primary);
}
.btn-ghost.danger {
  color: var(--danger);
}
.footnote {
  margin-top: var(--space-3);
  font-size: 11px;
  color: var(--text-secondary);
  text-align: center;
}
</style>
