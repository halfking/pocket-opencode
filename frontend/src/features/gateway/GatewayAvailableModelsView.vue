<!--
  GatewayAvailableModelsView — 网关可用模型目录（按家族/版本，含模态）。

  路由：/gateway/:nodeId/catalog
  数据源：pocketd 白名单代理 → GET /api/routing/available-models。
  用途：浏览 canonical 模型（modality / 上下文窗口 / 供应商数 / 精选），
  也是 App 内「按模态默认模型」的目录来源。
-->
<template>
  <div class="gw-view">
    <header class="top-bar">
      <button class="back-btn" @click="router.back()" aria-label="返回">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>
      <h1 class="title">模型目录</h1>
      <button class="refresh" :disabled="loading" aria-label="刷新" @click="load">
        <span class="material-symbols-outlined">refresh</span>
      </button>
    </header>

    <!-- 模态筛选 -->
    <div class="filter-row">
      <button :class="['chip', { active: activeMod === '' }]" @click="activeMod = ''">全部</button>
      <button
        v-for="m in MODALITIES"
        :key="m"
        :class="['chip', { active: activeMod === m }]"
        @click="activeMod = activeMod === m ? '' : m"
      >
        {{ MOD_LABELS[m] }}
      </button>
    </div>

    <div v-if="error" class="status-err">{{ error }}</div>

    <main class="body">
      <div v-if="loading" class="state">加载中…</div>
      <div v-else-if="filtered.length === 0" class="state">
        <p>{{ error ? '' : '暂无模型' }}</p>
        <p class="hint">需要在「网关节点」里配置带 admin 凭据的节点</p>
      </div>

      <section v-for="fam in filtered" :key="fam.family" class="family">
        <h2 class="family-name">{{ fam.family }}</h2>
        <article v-for="v in fam.versions ?? []" :key="v.canonical_name" class="model-row" @click="toggleDetail(v.canonical_name)">
          <div class="model-main">
            <div class="model-name">
              <span v-if="v.featured" class="star" title="精选">★</span>
              {{ v.display_name || v.canonical_name }}
            </div>
            <div class="model-meta">
              <span v-if="v.canonical_name !== v.display_name" class="mono">{{ v.canonical_name }}</span>
              <span v-if="v.context_window">· {{ formatCtx(v.context_window) }} ctx</span>
              <span v-if="v.provider_count">· {{ v.provider_count }} 供应商</span>
            </div>
          </div>
          <span class="modality-badge" :data-mod="v.modality">{{ modLabel(v.modality) }}</span>

          <div v-if="expanded === v.canonical_name" class="model-detail">
            <div v-if="v.aliases?.length" class="detail-line">
              <b>别名</b> {{ v.aliases.slice(0, 8).join(', ') }}
            </div>
            <div v-if="v.raw_names?.length" class="detail-line">
              <b>原始名</b> {{ v.raw_names.slice(0, 8).join(', ') }}
            </div>
            <div v-if="v.tags?.length" class="detail-line">
              <b>标签</b> {{ v.tags.join(', ') }}
            </div>
          </div>
        </article>
      </section>
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { getAvailableModels, type AvailableModelFamily } from '../../api/gateway'

const route = useRoute()
const router = useRouter()
const nodeId = Number(route.params.nodeId)

const loading = ref(false)
const error = ref('')
const families = ref<AvailableModelFamily[]>([])
const activeMod = ref<string>('')
const expanded = ref('')

const MODALITIES = ['text', 'vision', 'audio', 'video', 'multimodal', 'embedding'] as const
const MOD_LABELS: Record<string, string> = {
  text: '文本',
  vision: '图像',
  audio: '音频',
  video: '视频',
  multimodal: '多模态',
  embedding: '嵌入',
}

function modLabel(m?: string): string {
  return MOD_LABELS[m ?? ''] ?? m ?? '—'
}

const filtered = computed(() => {
  if (!activeMod.value) return families.value
  return families.value
    .map((f) => ({
      family: f.family,
      versions: (f.versions ?? []).filter((v) => String(v.modality ?? '') === activeMod.value),
    }))
    .filter((f) => (f.versions ?? []).length > 0)
})

function formatCtx(n: number): string {
  if (n >= 1000) return `${Math.round(n / 1000)}K`
  return String(n)
}

function toggleDetail(name: string) {
  expanded.value = expanded.value === name ? '' : name
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const res = await getAvailableModels(nodeId)
    families.value = res.families ?? []
  } catch (e: any) {
    error.value = e?.message || String(e)
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.gw-view { min-height: 100vh; background: var(--bg-base); }
.top-bar {
  display: flex; align-items: center; gap: 8px;
  padding: var(--space-3);
  padding-top: calc(var(--space-3) + env(safe-area-inset-top));
  background: var(--bg-card); border-bottom: 1px solid var(--border);
}
.back-btn, .refresh {
  background: none; border: none; color: var(--text-primary);
  display: flex; cursor: pointer; padding: 4px;
}
.title { flex: 1; font-size: 17px; font-weight: 600; margin: 0; }
.filter-row {
  display: flex; gap: 6px; padding: var(--space-2) var(--space-3);
  overflow-x: auto; background: var(--bg-card); border-bottom: 1px solid var(--border);
}
.chip {
  flex: none; padding: 5px 12px; font-size: 12px;
  background: var(--bg-subtle); border: 1px solid var(--border);
  border-radius: 999px; color: var(--text-secondary);
}
.chip.active { background: var(--primary, #4c8dff); color: #fff; border-color: transparent; }
.status-err {
  padding: var(--space-2) var(--space-3); font-size: 12px;
  background: color-mix(in srgb, var(--danger) 12%, transparent); color: var(--danger);
}
.body { padding: var(--space-3); display: flex; flex-direction: column; gap: var(--space-3); }
.state { padding: 40px 20px; text-align: center; color: var(--text-secondary); font-size: 14px; }
.hint { font-size: 12px; margin-top: 8px; }
.family {
  background: var(--bg-card); border: 1px solid var(--border);
  border-radius: 12px; overflow: hidden;
}
.family-name {
  margin: 0; padding: 10px 14px; font-size: 13px; font-weight: 600;
  color: var(--text-secondary); background: var(--bg-subtle);
}
.model-row {
  display: flex; align-items: center; gap: 10px;
  padding: 11px 14px; border-top: 1px solid var(--border); cursor: pointer;
  flex-wrap: wrap;
}
.model-main { flex: 1; min-width: 0; }
.model-name { font-size: 14px; color: var(--text-primary); font-weight: 500; word-break: break-all; }
.star { color: var(--warning); }
.model-meta { font-size: 11px; color: var(--text-secondary); margin-top: 3px; word-break: break-all; }
.mono { font-family: 'SF Mono', Menlo, monospace; }
.modality-badge {
  flex: none; font-size: 10px; padding: 3px 8px; border-radius: 999px;
  background: var(--bg-subtle); color: var(--text-secondary); border: 1px solid var(--border);
}
.modality-badge[data-mod='vision'] { color: var(--primary, #4c8dff); }
.model-detail { width: 100%; padding-top: 6px; }
.detail-line { font-size: 11px; color: var(--text-secondary); margin-top: 4px; word-break: break-all; }
.detail-line b { color: var(--text-primary); margin-right: 6px; }
</style>
