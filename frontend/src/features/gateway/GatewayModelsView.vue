<!--
  GatewayModelsView — 按模型看路由。

  路由：/gateway/:nodeId/models
  上游 /api/routing/model-tree 返回 family → generation → variant → credentials[]
  的四层结构。移动端把前两层压成可折叠的一层（按 canonical 模型名分组），
  展开后看承载该模型的凭据与 tier/weight/routable。
-->
<template>
  <div class="gw-view">
    <header class="top-bar">
      <button class="back-btn" @click="router.back()" aria-label="返回">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>
      <h1 class="title">模型路由</h1>
      <button class="icon-btn" :disabled="loading" @click="load" aria-label="刷新">
        <span class="material-symbols-outlined">refresh</span>
      </button>
    </header>

    <div class="search-bar">
      <input v-model="query" placeholder="过滤模型名…" />
      <label class="featured-toggle">
        <input v-model="featuredOnly" type="checkbox" @change="load" />
        <span>只看精选</span>
      </label>
    </div>

    <div v-if="error" class="status-bar status-err">{{ error }}</div>
    <div v-if="notice" class="status-bar status-ok">{{ notice }}</div>

    <main class="body">
      <div v-if="loading" class="state">加载中…</div>
      <div v-else-if="visibleVariants.length === 0 && !error" class="state">没有匹配的模型</div>

      <div v-else class="model-list">
        <div v-for="v in visibleVariants" :key="v.key" class="model-group">
          <div class="group-head" @click="toggleExpand(v.key)">
            <span :class="['state-dot', v.routableCount > 0 ? 'state-ok' : 'state-err']" />
            <div class="group-name">{{ v.variant }}</div>
            <span class="group-count">{{ v.routableCount }}/{{ v.credentials.length }}</span>
            <span class="material-symbols-outlined chevron">
              {{ expanded.has(v.key) ? 'expand_less' : 'expand_more' }}
            </span>
          </div>

          <div v-if="v.family" class="group-sub">{{ v.family }}</div>

          <template v-if="expanded.has(v.key)">
            <div v-for="c in v.credentials" :key="c.credential_id" class="cred-row">
              <span :class="['state-dot', c.runtime_routable ? 'state-ok' : 'state-err']" />
              <div class="cred-info">
                <div class="cred-label">{{ c.credential_label || `#${c.credential_id}` }}</div>
                <div class="cred-meta">
                  {{ c.provider_name }} · T{{ c.tier }} · w{{ c.weight }}
                  <template v-if="c.p95_latency_ms"> · {{ c.p95_latency_ms }}ms</template>
                  <template v-if="c.success_rate != null"> · {{ (c.success_rate * 100).toFixed(0) }}%</template>
                </div>
                <div v-if="c.runtime_block_reason" class="cred-block">{{ c.runtime_block_reason }}</div>
              </div>
              <span v-if="c.unit_price_in_per_1m != null" class="cred-price">
                ${{ c.unit_price_in_per_1m }}/M
              </span>
            </div>

            <button class="probe-btn" :disabled="probing === v.variant" @click="probe(v.variant)">
              {{ probing === v.variant ? '探测中…' : '发一次真实请求验活' }}
            </button>
          </template>
        </div>
      </div>

      <p class="footnote">探测会产生真实上游调用与费用，结果会出现在实时流里。</p>
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError } from '../../api/http'
import * as gw from '../../api/gateway'
import type { ModelTreeCredential } from '../../api/gateway'
import { useConfirm } from '../../composables/useConfirm'

const route = useRoute()
const router = useRouter()
const { confirm } = useConfirm()
const nodeId = Number(route.params.nodeId)

interface FlatVariant {
  key: string
  variant: string
  family: string
  credentials: ModelTreeCredential[]
  routableCount: number
}

const variants = ref<FlatVariant[]>([])
const expanded = ref(new Set<string>())
const query = ref('')
const featuredOnly = ref(false)
const loading = ref(true)
const error = ref('')
const notice = ref('')
const probing = ref<string | null>(null)

async function load() {
  loading.value = true
  error.value = ''
  try {
    const res = await gw.getModelTree(nodeId, featuredOnly.value)
    variants.value = flatten(res)
  } catch (e: any) {
    if (e instanceof ApiError && e.status === 403) {
      error.value = '该网关账号权限不足，无法读取模型路由'
    } else {
      error.value = e?.message || '加载失败'
    }
  } finally {
    loading.value = false
  }
}

// 上游结构是 families[].generations[].variants[].credentials[]，但字段名在
// 不同版本间有出入（families / tree / data）。这里按几种已知形态兜一遍，
// 拿不到就返回空列表而不是抛错。
function flatten(payload: any): FlatVariant[] {
  const families = payload?.families ?? payload?.tree ?? payload?.data ?? []
  if (!Array.isArray(families)) return []

  const out: FlatVariant[] = []
  for (const fam of families) {
    const famName = fam?.family ?? ''
    for (const gen of fam?.generations ?? []) {
      for (const v of gen?.variants ?? []) {
        const creds: ModelTreeCredential[] = v?.credentials ?? []
        out.push({
          key: `${famName}/${gen?.generation ?? ''}/${v?.variant ?? ''}`,
          variant: v?.variant ?? v?.canonical_name ?? '(未命名)',
          family: famName,
          credentials: creds,
          routableCount: creds.filter((c) => c.runtime_routable).length,
        })
      }
    }
  }
  return out
}

const visibleVariants = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return variants.value
  return variants.value.filter((v) => v.variant.toLowerCase().includes(q))
})

function toggleExpand(key: string) {
  const next = new Set(expanded.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  expanded.value = next
}

async function probe(model: string) {
  if (!(await confirm({ title: '模型探测', message: `对 ${model} 发一次真实请求？会产生上游调用与费用。`, confirmText: '发送' }))) return

  probing.value = model
  error.value = ''
  notice.value = ''
  try {
    const res = await gw.probeModel(nodeId, model)
    notice.value = res?.ok === false ? `探测失败：${res?.error ?? '未知原因'}` : `${model} 探测通过`
  } catch (e: any) {
    if (e instanceof ApiError && e.status === 403) {
      error.value = '探测需要 pocket admin 角色 + 网关 super_admin'
    } else {
      error.value = e?.message || '探测失败'
    }
  } finally {
    probing.value = null
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
  display: flex;
  align-items: center;
  gap: 10px;
  padding: var(--space-2-5) var(--space-3);
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
}
.search-bar input[type='text'],
.search-bar input:not([type]) {
  flex: 1;
  padding: 8px 12px;
  border-radius: var(--radius-full, 999px);
  border: 1px solid var(--border);
  background: var(--bg-subtle);
  color: var(--text-primary);
  font-size: 14px;
}
.featured-toggle {
  display: flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  color: var(--text-secondary);
  flex: none;
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
.model-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}
.model-group {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md, 10px);
  padding: var(--space-2-5) var(--space-3);
}
.group-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.group-name {
  flex: 1;
  font-size: 13px;
  font-weight: 600;
  word-break: break-all;
}
.group-count {
  font-size: 11px;
  color: var(--text-secondary);
}
.chevron {
  font-size: 20px;
  color: var(--text-secondary);
}
.group-sub {
  font-size: 10px;
  color: var(--text-secondary);
  margin-top: 2px;
}
.state-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex: none;
}
.state-ok {
  background: var(--success);
}
.state-err {
  background: var(--danger);
}
.cred-row {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  padding: 8px 0;
  border-top: 1px solid var(--border);
  margin-top: 8px;
}
.cred-info {
  flex: 1;
}
.cred-label {
  font-size: 12px;
  font-weight: 500;
}
.cred-meta {
  font-size: 10px;
  color: var(--text-secondary);
  margin-top: 2px;
}
.cred-block {
  font-size: 10px;
  color: var(--danger);
  margin-top: 2px;
}
.cred-price {
  font-size: 10px;
  color: var(--text-secondary);
  flex: none;
}
.probe-btn {
  width: 100%;
  margin-top: 8px;
  padding: 7px;
  font-size: 12px;
  background: var(--bg-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm, 8px);
  color: var(--primary, #4c8dff);
}
.footnote {
  margin-top: var(--space-3);
  font-size: 11px;
  color: var(--text-secondary);
  text-align: center;
}
</style>
