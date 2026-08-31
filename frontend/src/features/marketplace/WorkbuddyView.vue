<template>
  <!--
    WorkbuddyView — "工作搭子" 工作流市场 / 模板。
    - kind=workflow 的市场；
    - 强调组合式工作流（含多个步骤的 skill / agent 调用）；
    - 复用 store 加载与安装流程（与 SkillMarketView 同形）。
  -->
  <div class="page">
    <div class="toolbar">
      <input v-model="search" placeholder="搜索工作流名" />
      <button type="button" :disabled="store.loading" @click="refresh">刷新</button>
    </div>

    <div v-if="store.error" class="error" role="alert">{{ store.error }}</div>
    <div v-else-if="store.loading" class="state">加载中…</div>
    <div v-else-if="filtered.length === 0" class="state">
      <p>暂无工作流模板</p>
      <p class="hint">工作流是 marketplace 中 kind=workflow 的复合技能包，后续 sprint 接入完整流程编排。</p>
    </div>

    <main v-else class="list">
      <article v-for="pkg in filtered" :key="pkg.package_id" class="card">
        <header>
          <h2>{{ pkg.name }}</h2>
          <span class="kind">workflow</span>
        </header>
        <p class="meta">发布者 {{ pkg.publisher || '未知' }} · {{ pkgSteps(pkg) }} 步</p>
        <p v-if="depsOf(pkg).length" class="deps">
          <strong>依赖：</strong>
          <span v-for="d in depsOf(pkg)" :key="d.package_id" class="chip">
            {{ d.package_id }}@{{ d.version }}
          </span>
        </p>
        <footer>
          <button type="button" class="primary" @click="confirmInstall(pkg)">安装为工作搭子</button>
        </footer>
      </article>
    </main>

    <div v-if="installTarget" class="confirm-overlay" @click.self="installTarget = null">
      <div class="confirm-dialog" role="dialog" aria-modal="true">
        <h3>安装工作流「{{ installTarget.name }}」？</h3>
        <p>将记录一次安装行为；依赖项的解析与下载留待后续 sprint。</p>
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

const store = useMarketplaceStore()
const search = ref('')
const installing = ref(false)
const installTarget = ref<MarketplacePackage | null>(null)
const versionsByPackage = ref<Record<string, PackageVersion[]>>({})

onMounted(() => {
  store.loadPackages('workflow').catch(() => {})
})

const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return store.packages
  return store.packages.filter((p) => p.name.toLowerCase().includes(q))
})

function depsOf(pkg: MarketplacePackage): Array<{ package_id: string; version: string }> {
  const versions = versionsByPackage.value[pkg.package_id]
  return versions?.[0]?.manifest?.dependencies || []
}

function pkgSteps(pkg: MarketplacePackage): number {
  const versions = versionsByPackage.value[pkg.package_id]
  // 用 dependencies 数量粗略估计"步骤数"（每一步可能引用一个依赖包）
  return depsOf(pkg).length || 1
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
    store.error = '该工作流尚无已发布版本。'
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
  await store.install({ release_id: release.release_id, target_env: '' })
  installing.value = false
  installTarget.value = null
}

function refresh() {
  return store.loadPackages('workflow').catch(() => {})
}
</script>

<style scoped>
.page { min-height: 100%; background: var(--bg-base); }
.toolbar { display: flex; gap: var(--space-2); padding: var(--space-3); border-bottom: 1px solid var(--border); }
.toolbar input { flex: 1; border: 1px solid var(--border); border-radius: var(--radius-sm); padding: 7px 10px; background: var(--bg-card); color: var(--text-primary); }
.toolbar button { border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--bg-card); color: var(--text-primary); padding: 7px 12px; }
.list { display: flex; flex-direction: column; gap: var(--space-2); padding: var(--space-3); }
.card { padding: var(--space-3); border: 1px solid var(--border); border-radius: var(--radius-md); background: var(--bg-card); }
.card header { display: flex; align-items: center; gap: 8px; }
.card h2 { flex: 1; margin: 0; font-size: 15px; color: var(--text-primary); }
.kind { font-size: 11px; padding: 3px 8px; border-radius: 999px; background: var(--success); color: var(--text-inverse); }
.meta { margin: 7px 0; font-size: 13px; color: var(--text-secondary); }
.deps { display: flex; flex-wrap: wrap; gap: 6px; font-size: 12px; color: var(--text-secondary); align-items: center; }
.chip { padding: 2px 8px; border-radius: 999px; background: var(--bg-subtle); color: var(--text-primary); }
.card footer { display: flex; gap: 7px; margin-top: 11px; }
.card footer button { flex: 1; border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--bg-card); color: var(--text-primary); padding: 7px 12px; font-size: 12px; }
.card footer .primary { color: var(--text-inverse); background: var(--brand-gradient); border: 0; }
.state { padding: 48px 20px; text-align: center; color: var(--text-secondary); }
.state .hint { color: var(--text-muted); font-size: 12px; }
.error { margin: var(--space-3); padding: var(--space-3); color: var(--danger); background: var(--danger-bg); border-radius: var(--radius-sm); font-size: 13px; }
.confirm-overlay { position: fixed; inset: 0; background: rgba(0,0,0,0.45); display: flex; align-items: center; justify-content: center; z-index: 50; }
.confirm-dialog { background: var(--bg-card); border-radius: var(--radius-md); padding: var(--space-4); width: min(90vw, 360px); }
.confirm-dialog h3 { margin: 0 0 var(--space-2); color: var(--text-primary); }
.confirm-dialog p { color: var(--text-secondary); font-size: 13px; }
.confirm-dialog .actions { display: flex; gap: var(--space-2); margin-top: var(--space-3); justify-content: flex-end; }
.confirm-dialog button { border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--bg-card); color: var(--text-primary); padding: 7px 14px; }
.confirm-dialog .primary { color: var(--text-inverse); background: var(--brand-gradient); border: 0; }
</style>