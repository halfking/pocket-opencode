<template>
  <!--
    AgentMarketView — 智能体市场。
    - 与 SkillMarketView 同壳，但 kind=agent；
    - 额外展示 tags 标签，方便用户筛选；
    - 提供本地 / 云端分发策略选择器（影响后续安装流程）。
  -->
  <div class="page">
    <div class="toolbar">
      <input v-model="search" placeholder="搜索智能体名 / 标签" />
      <select v-model="strategy" aria-label="执行策略">
        <option value="local_first">本地优先</option>
        <option value="cloud_first">云端优先</option>
        <option value="hybrid">按复杂度混合</option>
        <option value="local_only">仅本地</option>
        <option value="cloud_only">仅云端</option>
      </select>
      <button type="button" :disabled="store.loading" @click="refresh">刷新</button>
    </div>

    <p class="strategy-hint">当前策略：{{ strategyLabel }}。该选择会影响任务执行时的本地/云端路由。</p>

    <div v-if="store.error" class="error" role="alert">{{ apiError(store.error, 'errors.loadSettingsFailed') }}</div>
    <div v-else-if="store.loading" class="state">加载中…</div>
    <div v-else-if="filtered.length === 0" class="state">
      <p>暂无智能体</p>
      <p class="hint">从市场安装智能体会自动在你的角色库派生一份本地副本。</p>
    </div>

    <main v-else class="list">
      <article v-for="pkg in filtered" :key="pkg.package_id" class="card">
        <header>
          <h2>{{ pkg.name }}</h2>
          <span class="kind">agent</span>
        </header>
        <p class="meta">发布者 {{ pkg.publisher || '未知' }} · 版本 {{ versionOf(pkg) || '—' }}</p>
        <p v-if="tagsOf(pkg).length" class="tags">
          <span v-for="t in tagsOf(pkg)" :key="t" class="chip">{{ t }}</span>
        </p>
        <footer>
          <button type="button" class="primary" @click="confirmInstall(pkg)">安装并启用</button>
        </footer>
      </article>
    </main>

    <div v-if="installTarget" class="confirm-overlay" @click.self="installTarget = null">
      <div class="confirm-dialog" role="dialog" aria-modal="true">
        <h3>安装智能体「{{ installTarget.name }}」？</h3>
        <p>将以 <strong>{{ strategyLabel }}</strong> 的策略纳入你的角色库。</p>
        <div class="actions">
          <button type="button" @click="installTarget = null">取消</button>
          <button type="button" class="primary" :disabled="installing" @click="runInstall">确认</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useMarketplaceStore } from './store'
import type { MarketplacePackage, PackageVersion } from './types'
import { useApiError } from '../../composables/useApiError'

const apiError = useApiError()
const store = useMarketplaceStore()
const search = ref('')
const strategy = ref('local_first')
const installing = ref(false)
const installTarget = ref<MarketplacePackage | null>(null)
const versionsByPackage = ref<Record<string, PackageVersion[]>>({})

onMounted(() => {
  store.loadPackages('agent').catch(() => {})
})

const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return store.packages
  return store.packages.filter((p) => {
    if (p.name.toLowerCase().includes(q)) return true
    const tags = tagsOf(p)
    return tags.some((t) => t.toLowerCase().includes(q))
  })
})

const strategyLabel = computed(() =>
  ({
    local_first: '本地优先（失败兜底云端）',
    cloud_first: '云端优先（失败兜底本地）',
    hybrid: '按任务复杂度混合',
    local_only: '仅本地（离线模式）',
    cloud_only: '仅云端（省电模式）',
  })[strategy.value] || strategy.value,
)

function versionOf(pkg: MarketplacePackage): string {
  const list = versionsByPackage.value[pkg.package_id]
  return list?.[0]?.version || ''
}

function tagsOf(pkg: MarketplacePackage): string[] {
  return versionsByPackage.value[pkg.package_id]?.[0]?.manifest?.licenses || []
}

async function ensureVersionsLoaded(pkg: MarketplacePackage): Promise<PackageVersion[]> {
  if (versionsByPackage.value[pkg.package_id]) return versionsByPackage.value[pkg.package_id]
  const list = await store.loadVersions(pkg.package_id)
  versionsByPackage.value = { ...versionsByPackage.value, [pkg.package_id]: list }
  return list
}

function confirmInstall(pkg: MarketplacePackage) {
  installTarget.value = pkg
}

async function runInstall() {
  if (!installTarget.value) return
  installing.value = true
  const versions = await ensureVersionsLoaded(installTarget.value)
  const published = versions.find((v) => v.status === 'published')
  if (!published) {
    store.error = '该智能体尚无已发布版本。'
    installing.value = false
    installTarget.value = null
    return
  }
  await store.loadReleases()
  const release = store.releases.find((r) => r.version_id === published.version_id)
  if (!release) {
    store.error = '未找到对应的 release。'
    installing.value = false
    installTarget.value = null
    return
  }
  await store.install({ release_id: release.release_id, target_env: strategy.value })
  installing.value = false
  installTarget.value = null
}

function refresh() {
  return store.loadPackages('agent').catch(() => {})
}
</script>

<style scoped>
.page { min-height: 100%; background: var(--bg-base); }
/* 真机（360dp）实测：工具栏内容宽 389px 超出视口 29px，「刷新」按钮右边缘落在 389，
   整个按钮点不到。根因是 flex 子项默认 `min-width: auto`：
   input 的 min-content 约等于默认 size=20 字符宽，select 的 min-content 等于最长选项
   「按复杂度混合」宽，两者都不肯收缩，把行撑破。
   显式给 min-width 后由 input 先让位，select 保留可用宽度。 */
.toolbar { display: flex; gap: var(--space-2); padding: var(--space-3); border-bottom: 1px solid var(--border); }
.toolbar input { flex: 1 1 0; min-width: 56px; border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 7px 10px; background: var(--bg-card); color: var(--text-primary); }
.toolbar select { flex: 0 1 auto; min-width: 0; max-width: 45%; border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 7px 10px; background: var(--bg-card); color: var(--text-primary); }
.toolbar button { flex-shrink: 0; border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--bg-card); color: var(--text-primary); padding: 7px 12px; }
.strategy-hint { padding: var(--space-2) var(--space-3); color: var(--text-secondary); font-size: var(--text-sm); background: var(--bg-subtle); border-bottom: 1px solid var(--border); margin: 0; }
.list { display: flex; flex-direction: column; gap: var(--space-2); padding: var(--space-3); }
.card { padding: var(--space-3); border: 1px solid var(--border); border-radius: var(--radius-md); background: var(--bg-card); }
.card header { display: flex; align-items: center; gap: 8px; }
.card h2 { flex: 1; margin: 0; font-size: var(--text-md); color: var(--text-primary); }
.kind { font-size: var(--text-2xs); padding: 3px 8px; border-radius: 999px; background: var(--brand-primary); color: var(--text-inverse); }
.meta { margin: 7px 0; font-size: var(--text-smd); color: var(--text-secondary); }
.tags { display: flex; flex-wrap: wrap; gap: 6px; }
.chip { padding: 2px 8px; border-radius: 999px; background: var(--bg-subtle); color: var(--text-primary); font-size: var(--text-sm); }
.card footer { display: flex; gap: 7px; margin-top: 11px; }
.card footer button { flex: 1; border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--bg-card); color: var(--text-primary); padding: 7px 12px; font-size: var(--text-sm); }
.card footer .primary { color: var(--text-inverse); background: var(--brand-gradient); border: 0; }
.state { padding: 48px 20px; text-align: center; color: var(--text-secondary); }
.state .hint { color: var(--text-muted); font-size: var(--text-sm); }
.error { margin: var(--space-3); padding: var(--space-3); color: var(--danger); background: var(--danger-bg); border-radius: var(--radius-sm); font-size: var(--text-smd); }
.confirm-overlay { position: fixed; /* 底边随 --kb-inset 抬升：键盘在场时浮层收在键盘上沿之上，弹层内的输入框与确认按钮不会被盖住。同 BottomSheet.vue。 */ inset: 0 0 var(--kb-inset, 0px) 0; background: rgba(0,0,0,0.45); display: flex; align-items: center; justify-content: center; z-index: var(--z-dialog); }
.confirm-dialog { background: var(--bg-card); border-radius: var(--radius-md); padding: var(--space-4); width: min(90vw, 360px); }
.confirm-dialog h3 { margin: 0 0 var(--space-2); color: var(--text-primary); }
.confirm-dialog p { color: var(--text-secondary); font-size: var(--text-smd); }
.confirm-dialog .actions { display: flex; gap: var(--space-2); margin-top: var(--space-3); justify-content: flex-end; }
.confirm-dialog button { border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--bg-card); color: var(--text-primary); padding: 7px 14px; }
.confirm-dialog .primary { color: var(--text-inverse); background: var(--brand-gradient); border: 0; }
</style>