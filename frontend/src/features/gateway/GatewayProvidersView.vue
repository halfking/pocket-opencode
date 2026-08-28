<!--
  GatewayProvidersView — 供应商列表。

  路由：/gateway/:nodeId/providers
  上游 /api/providers 要求网关 super_admin；账号权限不足时会 403，
  这里把它显示成可诊断的提示而不是"加载失败"。
-->
<template>
  <div class="gw-view">
    <header class="top-bar">
      <button class="back-btn" @click="router.back()" aria-label="返回">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>
      <h1 class="title">供应商</h1>
      <button class="icon-btn" :disabled="loading" @click="load" aria-label="刷新">
        <span class="material-symbols-outlined">refresh</span>
      </button>
    </header>

    <div class="search-bar">
      <input v-model="query" placeholder="搜索供应商…" @keyup.enter="load" />
    </div>

    <div v-if="error" class="status-bar status-err">{{ error }}</div>
    <div v-if="permissionHint" class="status-bar status-warn">{{ permissionHint }}</div>
    <div v-if="notice" class="status-bar status-ok">{{ notice }}</div>

    <main class="body">
      <div v-if="loading" class="state">加载中…</div>
      <div v-else-if="providers.length === 0 && !error" class="state">没有供应商</div>

      <div v-else class="list">
        <div v-for="p in providers" :key="p.id" class="card">
          <div class="card-head">
            <span :class="['health-dot', healthClass(p)]" />
            <div class="name">
              {{ p.display_name || p.code }}
              <span v-if="!p.enabled" class="chip chip-err">已禁用</span>
              <span v-else-if="p.manual_disabled" class="chip chip-warn">人工停用</span>
            </div>
          </div>

          <div class="sub">{{ p.vendor_name || p.category }} · {{ p.protocol }}</div>

          <div class="counts">
            <span class="count"><b class="ok">{{ p.healthy_credential_count }}</b> 健康</span>
            <span class="count"><b class="warn">{{ p.warning_credential_count }}</b> 警告</span>
            <span class="count"><b class="err">{{ p.unreachable_credential_count }}</b> 不可达</span>
            <span class="count"><b>{{ p.active_credential_count }}</b> 启用</span>
          </div>

          <div class="binding-row">
            <div class="binding-bar">
              <div class="binding-fill" :style="{ width: routablePct(p) + '%' }" />
            </div>
            <span class="binding-text">
              {{ p.routable_binding_count }}/{{ p.total_binding_count }} 可路由
            </span>
          </div>

          <button class="link-btn" @click="viewCredentials(p)">查看凭据 →</button>
          <!-- 启用/禁用（需 admin 角色，影响面大：整个 provider 下所有凭据）-->
          <div class="actions">
            <button
              :class="['btn-ghost', p.enabled ? 'danger' : '']"
              :disabled="busy === p.id"
              @click="toggle(p)"
            >
              {{ busy === p.id ? '操作中…' : p.enabled ? '禁用供应商' : '启用供应商' }}
            </button>
          </div>
        </div>
      </div>
    </main>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError } from '../../api/http'
import * as gw from '../../api/gateway'
import type { GatewayProvider } from '../../api/gateway'
import { useConfirm } from '../../composables/useConfirm'

const route = useRoute()
const router = useRouter()
const { confirm } = useConfirm()
const nodeId = Number(route.params.nodeId)

const providers = ref<GatewayProvider[]>([])
const query = ref('')
const loading = ref(true)
const error = ref('')
const permissionHint = ref('')
const busy = ref<number | null>(null)
const notice = ref('')

async function load() {
  loading.value = true
  error.value = ''
  permissionHint.value = ''
  try {
    const res = await gw.listProviders(nodeId, query.value || undefined)
    providers.value = res.providers ?? []
  } catch (e: any) {
    // 403 在这里是最常见的失败：网关要求 super_admin 才能读供应商表。
    if (e instanceof ApiError && e.status === 403) {
      permissionHint.value = '该网关账号权限不足：供应商列表需要网关 super_admin 角色。到节点页重新探测可确认当前角色。'
    } else {
      error.value = e?.message || '加载失败'
    }
  } finally {
    loading.value = false
  }
}

function healthClass(p: GatewayProvider) {
  if (!p.enabled || p.manual_disabled) return 'health-off'
  if (p.unreachable_credential_count > 0) return 'health-error'
  if (p.warning_credential_count > 0) return 'health-warn'
  return 'health-ok'
}

function routablePct(p: GatewayProvider) {
  if (!p.total_binding_count) return 0
  return Math.round((p.routable_binding_count / p.total_binding_count) * 100)
}

function viewCredentials(p: GatewayProvider) {
  router.push(`/gateway/${nodeId}/credentials?provider_id=${p.id}`)
}

async function toggle(p: GatewayProvider) {
  const verb = p.enabled ? '禁用' : '启用'
  const warn = p.enabled
    ? `禁用「${p.display_name}」会让其下所有凭据无法路由，确认继续？`
    : `启用「${p.display_name}」？`
  if (!(await confirm({ title: `${verb}供应商`, message: warn, confirmText: verb, danger: p.enabled }))) return

  busy.value = p.id
  error.value = ''
  notice.value = ''
  try {
    await gw.toggleProvider(nodeId, p.id)
    notice.value = `已${verb}供应商「${p.display_name}」`
    await load()
  } catch (e: any) {
    if (e instanceof ApiError && e.status === 403) {
      error.value = '需要 pocket admin 角色 + 网关 super_admin 才能变更供应商状态'
    } else {
      error.value = e?.message || `${verb}失败`
    }
  } finally {
    busy.value = null
  }
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
.search-bar {
  padding: var(--space-2-5) var(--space-3);
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
}
.search-bar input {
  width: 100%;
  padding: 8px 12px;
  border-radius: var(--radius-full, 999px);
  border: 1px solid var(--border);
  background: var(--bg-subtle);
  color: var(--text-primary);
  font-size: 14px;
  box-sizing: border-box;
}
.status-bar {
  padding: var(--space-2) var(--space-3);
  font-size: 12px;
}
.status-err {
  background: color-mix(in srgb, var(--danger) 15%, transparent);
  color: var(--danger);
}
.status-warn {
  background: color-mix(in srgb, var(--warning) 18%, transparent);
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
  font-size: 15px;
  font-weight: 600;
  display: flex;
  align-items: center;
  gap: 6px;
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
.chip-warn {
  color: var(--warning);
}
.chip-err {
  color: var(--danger);
}
.counts {
  display: flex;
  gap: 12px;
  margin-top: 10px;
  flex-wrap: wrap;
}
.count {
  font-size: 11px;
  color: var(--text-secondary);
}
.count b {
  font-size: 13px;
  color: var(--text-primary);
}
.count b.ok {
  color: var(--success);
}
.count b.warn {
  color: var(--warning);
}
.count b.err {
  color: var(--danger);
}
.binding-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 10px;
}
.binding-bar {
  flex: 1;
  height: 4px;
  background: var(--bg-subtle);
  border-radius: 2px;
  overflow: hidden;
}
.binding-fill {
  height: 100%;
  background: var(--success);
}
.binding-text {
  font-size: 11px;
  color: var(--text-secondary);
  flex: none;
}
.status-ok {
  background: color-mix(in srgb, var(--success) 15%, transparent);
  color: var(--success);
}
.actions {
  display: flex;
  gap: 8px;
  margin-top: 10px;
}
.btn-ghost {
  flex: 1;
  padding: 7px;
  font-size: 12px;
  background: var(--bg-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm, 8px);
  color: var(--text-primary);
}
.btn-ghost.danger {
  color: var(--danger);
}
.link-btn {
  margin-top: 10px;
  background: none;
  border: none;
  padding: 0;
  color: var(--primary, #4c8dff);
  font-size: 13px;
}
</style>
