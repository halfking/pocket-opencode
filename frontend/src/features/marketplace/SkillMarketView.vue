<template>
  <!--
    SkillMarketView — 技能市场。
    - 仅展示 kind=skill 的包；
    - 列表/空态/错误态 三态分离；
    - 安装前显示二次确认对话框；
    - 权限摘要从 manifest.permissions 派生（manifest 缺失时不显示）。
  -->
  <div class="page">
    <div class="toolbar">
      <input v-model="search" placeholder="搜索技能名 / 发布者" />
      <button type="button" :disabled="store.loading" @click="refresh">刷新</button>
    </div>

    <div v-if="store.error" class="error" role="alert">
      {{ apiError(store.error, 'errors.loadSettingsFailed') }}
      <button type="button" @click="refresh">重试</button>
    </div>

    <div v-else-if="store.loading" class="state">加载中…</div>

    <div v-else-if="filtered.length === 0" class="state">
      <p>暂无技能包</p>
      <p class="hint">技能由发布者提交、审核、发布后才会出现在这里。</p>
    </div>

    <main v-else class="list">
      <article v-for="pkg in filtered" :key="pkg.package_id" class="card">
        <header>
          <h2>{{ pkg.name }}</h2>
          <span class="kind">{{ pkg.kind }}</span>
        </header>
        <p class="meta">发布者 {{ pkg.publisher || '未知' }} · 可见性 {{ visibilityLabel(pkg.visibility) }}</p>
        <p v-if="permissionsOf(pkg).length" class="permissions">
          <strong>权限：</strong>
          <span v-for="p in permissionsOf(pkg)" :key="p" class="chip">{{ p }}</span>
        </p>
        <footer>
          <button type="button" class="primary" @click="confirmInstall(pkg)">安装</button>
          <button type="button" @click="openVersions(pkg)">查看版本</button>
        </footer>
      </article>
    </main>

    <div v-if="installTarget" class="confirm-overlay" @click.self="installTarget = null">
      <div class="confirm-dialog" role="dialog" aria-modal="true">
        <h3>安装「{{ installTarget.name }}」？</h3>
        <p>将记录一次安装行为，但包内容下载与执行由后续 sprint 接入。</p>
        <p v-if="permissionsOf(installTarget).length" class="permissions">
          将授予权限：
          <span v-for="p in permissionsOf(installTarget)" :key="p" class="chip">{{ p }}</span>
        </p>
        <div class="actions">
          <button type="button" @click="installTarget = null">取消</button>
          <button type="button" class="primary" :disabled="installing" @click="runInstall">确认安装</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useMarketplaceStore } from './store'
import type { MarketplacePackage, PackageVersion, Visibility } from './types'
import { useApiError } from '../../composables/useApiError'

const apiError = useApiError()
const store = useMarketplaceStore()
const search = ref('')
const installing = ref(false)
const installTarget = ref<MarketplacePackage | null>(null)
const expanded = ref<Record<string, PackageVersion[]>>({})
// 版本缓存，独立于 expanded（纯 UI 展开状态）。与 AgentMarketView / WorkbuddyView 同形。
const versionsByPackage = ref<Record<string, PackageVersion[]>>({})

onMounted(() => {
  store.loadPackages('skill').catch(() => {})
})

const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return store.packages
  return store.packages.filter(
    (p) =>
      p.name.toLowerCase().includes(q) ||
      (p.publisher || '').toLowerCase().includes(q),
  )
})

function visibilityLabel(v: Visibility): string {
  return { private: '私有', workspace: '工作区', org: '组织', public: '公开' }[v]
}

function permissionsOf(pkg: MarketplacePackage): string[] {
  // 当前 packages 列表不携带 manifest；权限摘要需要先加载 versions。
  // 简化处理：从已展开的 versions 中取最新一条 manifest 的 permissions。
  const versions = expanded.value[pkg.package_id]
  if (!versions || versions.length === 0) return []
  return versions[0].manifest.permissions || []
}

async function openVersions(pkg: MarketplacePackage) {
  if (expanded.value[pkg.package_id]) {
    delete expanded.value[pkg.package_id]
    expanded.value = { ...expanded.value }
    return
  }
  const list = await store.loadVersions(pkg.package_id)
  expanded.value = { ...expanded.value, [pkg.package_id]: list }
}

/**
 * 按需加载某包的版本列表。
 *
 * BUG-Y 的另一半：安装流程原先假设「用户已经点过『查看版本』」，
 * 于是从 `expanded` 里取——那是个纯 UI 展开状态，只有点过才会有值。
 * 改成与 AgentMarketView / WorkbuddyView 相同的写法：走 store 的
 * `versionsByPackage` 缓存，没缓存就加载。
 */
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
  const pkgId = installTarget.value.package_id
  // BUG-Y（2026-09-30 真机验收）：原来这里直接读
  //   const versions = expanded.value[pkgId]
  // 而 `expanded` **只有用户点过「查看版本」之后才会有值**。于是：
  //   点「安装」→ 弹确认框 → 点「确认安装」→ versions 为 undefined →
  //   报「该包尚无已发布版本，无法安装。」
  // 而那个包**确实有**已发布版本（`GET .../versions` 明确返回 status:"published"）。
  // 结果：安装对任何没先展开过的包都必然失败，且给出与事实相反的提示。
  //
  // 注意 AgentMarketView / WorkbuddyView **本来就有** ensureVersionsLoaded，
  // 只有这份是直接读 expanded —— 同一段逻辑写了三遍，只有这一遍写错了。
  // 这里改成同一写法，不维护第三套。
  const versions = await ensureVersionsLoaded(installTarget.value)
  const publishedVersion = versions.find((v) => v.status === 'published')
  if (!publishedVersion) {
    store.error = '该包尚无已发布版本，无法安装。'
    installing.value = false
    installTarget.value = null
    return
  }
  // releases 表需要先加载；这里取 store 中第一条匹配
  await store.loadReleases()
  const release = store.releases.find((r) => r.version_id === publishedVersion.version_id)
  if (!release) {
    store.error = '未找到对应的 release 记录。'
    installing.value = false
    installTarget.value = null
    return
  }
  // store.install 失败时返回 null 并把原因写进 store.error；原来忽略返回值，
  // 于是**后端拒绝安装也是静默的** —— 弹窗一关，用户什么都不知道。
  const inst = await store.install({ release_id: release.release_id, target_env: '' })
  if (!inst) {
    store.error = store.error || '安装失败，请重试。'
  }
  installing.value = false
  installTarget.value = null
}

function refresh() {
  return store.loadPackages('skill').catch(() => {})
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
.card h2 { flex: 1; margin: 0; font-size: var(--text-md); color: var(--text-primary); }
.kind { font-size: var(--text-2xs); padding: 3px 8px; border-radius: 999px; background: var(--brand-primary); color: var(--text-inverse); }
.meta { margin: 7px 0; font-size: var(--text-smd); color: var(--text-secondary); }
.permissions { display: flex; flex-wrap: wrap; gap: 6px; font-size: var(--text-sm); color: var(--text-secondary); align-items: center; }
.chip { padding: 2px 8px; border-radius: 999px; background: var(--bg-subtle); color: var(--text-primary); }
.card footer { display: flex; gap: 7px; margin-top: 11px; }
.card footer button { flex: 1; border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--bg-card); color: var(--text-primary); padding: 7px 12px; font-size: var(--text-sm); }
.card footer .primary { color: var(--text-inverse); background: var(--brand-gradient); border: 0; }
.state { padding: 48px 20px; text-align: center; color: var(--text-secondary); }
.state .hint { color: var(--text-muted); font-size: var(--text-sm); }
.error { margin: var(--space-3); padding: var(--space-3); color: var(--danger); background: var(--danger-bg); border-radius: var(--radius-sm); font-size: var(--text-smd); display: flex; justify-content: space-between; align-items: center; }
.error button { border: 1px solid var(--danger); border-radius: var(--radius-sm); background: var(--bg-card); color: var(--danger); padding: 5px 10px; }
.confirm-overlay { position: fixed; /* 底边随 --kb-inset 抬升：键盘在场时浮层收在键盘上沿之上，弹层内的输入框与确认按钮不会被盖住。同 BottomSheet.vue。 */ inset: 0 0 var(--kb-inset, 0px) 0; background: rgba(0,0,0,0.45); display: flex; align-items: center; justify-content: center; z-index: 50; }
.confirm-dialog { background: var(--bg-card); border-radius: var(--radius-md); padding: var(--space-4); width: min(90vw, 360px); }
.confirm-dialog h3 { margin: 0 0 var(--space-2); color: var(--text-primary); }
.confirm-dialog p { color: var(--text-secondary); font-size: var(--text-smd); }
.confirm-dialog .actions { display: flex; gap: var(--space-2); margin-top: var(--space-3); justify-content: flex-end; }
.confirm-dialog button { border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--bg-card); color: var(--text-primary); padding: 7px 14px; }
.confirm-dialog .primary { color: var(--text-inverse); background: var(--brand-gradient); border: 0; }
</style>