<!--
  GatewayRoutingConfigView — 网关路由配置（移动端三 Tab）。

  路由：/gateway/:nodeId/routing-config
  Tab 1 任务类型：work_types（8 个 L1 任务类型）及其模型路由，可整体替换；
  Tab 2 默认路由：task_default_routing（task_type × profile × tier → 模型）CRUD；
  Tab 3 策略：routing policy / featured models（可编辑）/ 评分权重 / 默认限流。

  全部走 pocketd 白名单代理；写操作需要 pocket admin 角色 + 网关 super_admin。
-->
<template>
  <div class="gw-view">
    <header class="top-bar">
      <button class="back-btn" @click="router.back()" aria-label="返回">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>
      <h1 class="title">路由配置</h1>
    </header>

    <div class="tab-row">
      <button v-for="t in TABS" :key="t" :class="['tab', { active: tab === t }]" @click="switchTab(t)">
        {{ t }}
      </button>
    </div>

    <div v-if="error" class="status-err">{{ error }} <button class="link-btn" @click="reload">重试</button></div>

    <main class="body">
      <!-- ── 任务类型 ── -->
      <template v-if="tab === '任务类型'">
        <div v-if="loading" class="state">加载中…</div>
        <div v-else-if="workTypes.length === 0" class="state">暂无任务类型数据</div>
        <section v-for="wt in workTypes" :key="wt.key" class="card">
          <div class="wt-head">
            <div>
              <div class="wt-key">{{ wt.name || wt.key }}</div>
              <div class="wt-sub">{{ wt.key }}<span v-if="wt.enabled === false" class="off">· 已停用</span></div>
            </div>
            <button class="mini-btn" @click="startEditRoutes(wt)">编辑路由</button>
          </div>
          <p v-if="wt.description" class="wt-desc">{{ wt.description }}</p>

          <!-- 路由编辑态 -->
          <div v-if="editing === wt.key" class="route-editor">
            <textarea
              v-model="routesDraft[wt.key]"
              class="routes-input"
              rows="3"
              placeholder="模型 id，逗号或换行分隔"
            ></textarea>
            <div class="editor-actions">
              <button class="mini-btn primary" :disabled="saving" @click="saveRoutes(wt)">
                {{ saving ? '保存中…' : '保存' }}
              </button>
              <button class="mini-btn" @click="editing = ''">取消</button>
            </div>
          </div>
          <!-- 路由展示态 -->
          <div v-else class="route-chips">
            <span v-for="r in routeModels(wt)" :key="r" class="route-chip">{{ r }}</span>
            <span v-if="routeModels(wt).length === 0" class="none">（无路由）</span>
          </div>
        </section>
      </template>

      <!-- ── 默认路由 ── -->
      <template v-else-if="tab === '默认路由'">
        <section class="card">
          <h2 class="card-h">新增默认路由</h2>
          <div class="form-grid">
            <label>任务类型
              <select v-model="form.task_type" class="inp">
                <option v-for="t in L1_TASKS" :key="t" :value="t">{{ t }}</option>
              </select>
            </label>
            <label>偏好
              <select v-model="form.profile" class="inp">
                <option value="">（默认）</option>
                <option value="smart">smart</option>
                <option value="speed_first">speed_first</option>
                <option value="cost_first">cost_first</option>
              </select>
            </label>
            <label>层级
              <select v-model="form.tier" class="inp">
                <option value="primary">primary</option>
                <option value="secondary">secondary</option>
                <option value="fallback">fallback</option>
              </select>
            </label>
            <label>模型（canonical）
              <input v-model="form.canonical_model" class="inp" placeholder="如 deepseek-v3.2" />
            </label>
            <label>优先级
              <input v-model.number="form.priority" type="number" class="inp" />
            </label>
            <label>备注
              <input v-model="form.reason" class="inp" placeholder="可选" />
            </label>
          </div>
          <button class="mini-btn primary" :disabled="saving || !form.canonical_model" @click="createDefault">
            {{ saving ? '提交中…' : '新增' }}
          </button>
        </section>

        <div v-if="loading" class="state">加载中…</div>
        <div v-else-if="defaults.length === 0" class="state">暂无默认路由</div>
        <section v-for="d in defaults" :key="d.id" class="card row-card">
          <div class="d-main">
            <div class="d-model">{{ d.canonical_model }}</div>
            <div class="d-meta">
              {{ d.task_type }}<template v-if="d.profile"> · {{ d.profile }}</template> · {{ d.tier }}
              <template v-if="d.priority"> · P{{ d.priority }}</template>
            </div>
            <div v-if="d.reason" class="d-reason">{{ d.reason }}</div>
          </div>
          <button class="mini-btn danger" :disabled="saving" @click="removeDefault(d)">删除</button>
        </section>
      </template>

      <!-- ── 策略 ── -->
      <template v-else>
        <section class="card">
          <h2 class="card-h">精选模型 <button class="mini-btn" @click="editingFeatured = !editingFeatured">{{ editingFeatured ? '取消' : '编辑' }}</button></h2>
          <div class="route-chips">
            <span v-for="m in featured" :key="m" class="route-chip">
              {{ m }}
              <button v-if="editingFeatured" class="chip-x" @click="removeFeatured(m)">×</button>
            </span>
            <span v-if="featured.length === 0" class="none">（无）</span>
          </div>
          <div v-if="editingFeatured" class="route-editor">
            <input v-model="featuredDraft" class="inp" placeholder="输入模型 id 后回车添加" @keydown.enter.prevent="addFeatured" />
            <div class="editor-actions">
              <button class="mini-btn primary" :disabled="saving" @click="saveFeatured">
                {{ saving ? '保存中…' : '保存精选' }}
              </button>
            </div>
          </div>
        </section>

        <section class="card">
          <h2 class="card-h">路由策略</h2>
          <pre class="raw-json">{{ pretty(policy) }}</pre>
        </section>
        <section class="card">
          <h2 class="card-h">评分权重</h2>
          <pre class="raw-json">{{ pretty(weights) }}</pre>
        </section>
        <section class="card">
          <h2 class="card-h">默认限流</h2>
          <pre class="raw-json">{{ pretty(limits) }}</pre>
        </section>
      </template>
    </main>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import * as gw from '../../api/gateway'
import type { WorkType, TaskDefaultRouting } from '../../api/gateway'
import { useToast } from '../../composables/useToast'
import { useConfirm } from '../../composables/useConfirm'

const route = useRoute()
const router = useRouter()
const toast = useToast()
const { confirm } = useConfirm()
const nodeId = Number(route.params.nodeId)

const TABS = ['任务类型', '默认路由', '策略'] as const
const tab = ref<(typeof TABS)[number]>('任务类型')
const L1_TASKS = ['chat', 'reasoning', 'code', 'agent', 'creative', 'long_context', 'vision', 'function_call']

const loading = ref(false)
const saving = ref(false)
const error = ref('')

// 任务类型
const workTypes = ref<WorkType[]>([])
const editing = ref('')
const routesDraft = ref<Record<string, string>>({})

// 默认路由
const defaults = ref<TaskDefaultRouting[]>([])
const form = ref({ task_type: 'chat', profile: '', tier: 'primary', canonical_model: '', priority: 0, reason: '' })

// 策略
const policy = ref<any>(null)
const weights = ref<any>(null)
const limits = ref<any>(null)
const featured = ref<string[]>([])
const editingFeatured = ref(false)
const featuredDraft = ref('')

function pretty(v: any): string {
  if (v == null) return '（不可用）'
  try {
    return JSON.stringify(v, null, 2)
  } catch {
    return String(v)
  }
}

function routeModels(wt: WorkType): string[] {
  return (wt.routes ?? [])
    .map((r) => String(r.canonical_model ?? r.model ?? ''))
    .filter(Boolean)
}

function switchTab(t: (typeof TABS)[number]) {
  tab.value = t
  error.value = ''
  void reload()
}

async function reload() {
  loading.value = true
  error.value = ''
  try {
    if (tab.value === '任务类型') {
      const res = await gw.getWorkTypes(nodeId)
      workTypes.value = (res.work_types ?? (Array.isArray(res) ? (res as any) : [])) as WorkType[]
    } else if (tab.value === '默认路由') {
      const res = await gw.listTaskDefaults(nodeId)
      defaults.value = (res.defaults ?? (res as any).rows ?? (Array.isArray(res) ? (res as any) : [])) as TaskDefaultRouting[]
    } else {
      const [p, w, l, f] = await Promise.allSettled([
        gw.getRoutingPolicy(nodeId),
        gw.getScoringWeights(nodeId),
        gw.getDefaultLimits(nodeId),
        gw.getFeaturedModels(nodeId),
      ])
      policy.value = p.status === 'fulfilled' ? p.value : null
      weights.value = w.status === 'fulfilled' ? w.value : null
      limits.value = l.status === 'fulfilled' ? l.value : null
      if (f.status === 'fulfilled') {
        const raw = f.value?.featured_models ?? f.value?.models ?? []
        featured.value = (Array.isArray(raw) ? raw : []).map(String)
      }
    }
  } catch (e: any) {
    error.value = e?.message || String(e)
  } finally {
    loading.value = false
  }
}

// ---- 任务类型路由编辑 ----
function startEditRoutes(wt: WorkType) {
  editing.value = wt.key
  routesDraft.value[wt.key] = routeModels(wt).join(', ')
}

async function saveRoutes(wt: WorkType) {
  const models = (routesDraft.value[wt.key] ?? '')
    .split(/[,，\n]/)
    .map((s) => s.trim())
    .filter(Boolean)
  saving.value = true
  try {
    await gw.replaceWorkTypeRoutes(
      nodeId,
      wt.key,
      models.map((m) => ({ model: m })),
    )
    wt.routes = models.map((m) => ({ model: m }))
    editing.value = ''
    toast.success(`已更新「${wt.key}」的路由`)
  } catch (e: any) {
    toast.error('保存失败：' + (e?.message || String(e)))
  } finally {
    saving.value = false
  }
}

// ---- 默认路由 CRUD ----
async function createDefault() {
  saving.value = true
  try {
    await gw.createTaskDefault(nodeId, {
      task_type: form.value.task_type,
      canonical_model: form.value.canonical_model.trim(),
      profile: form.value.profile || undefined,
      tier: form.value.tier,
      priority: form.value.priority || undefined,
      reason: form.value.reason || undefined,
    })
    form.value.canonical_model = ''
    form.value.reason = ''
    toast.success('已新增默认路由')
    await reload()
  } catch (e: any) {
    toast.error('新增失败：' + (e?.message || String(e)))
  } finally {
    saving.value = false
  }
}

async function removeDefault(d: TaskDefaultRouting) {
  if (!(await confirm({
    title: '删除默认路由',
    message: `删除默认路由：${d.task_type} → ${d.canonical_model}？`,
    confirmText: '删除',
    danger: true,
  }))) return
  saving.value = true
  try {
    await gw.deleteTaskDefault(nodeId, d.id)
    toast.success('已删除')
    await reload()
  } catch (e: any) {
    toast.error('删除失败：' + (e?.message || String(e)))
  } finally {
    saving.value = false
  }
}

// ---- 精选模型 ----
function addFeatured() {
  const m = featuredDraft.value.trim()
  if (!m) return
  if (!featured.value.includes(m)) featured.value.push(m)
  featuredDraft.value = ''
}

function removeFeatured(m: string) {
  featured.value = featured.value.filter((x) => x !== m)
}

async function saveFeatured() {
  saving.value = true
  try {
    await gw.setFeaturedModels(nodeId, [...featured.value])
    toast.success('精选模型已保存')
    editingFeatured.value = false
  } catch (e: any) {
    toast.error('保存失败：' + (e?.message || String(e)))
  } finally {
    saving.value = false
  }
}

onMounted(reload)
</script>

<style scoped>
.gw-view { min-height: 100vh; background: var(--bg-base); }
.top-bar {
  display: flex; align-items: center; gap: 8px;
  padding: var(--space-3);
  padding-top: calc(var(--space-3) + env(safe-area-inset-top));
  background: var(--bg-card); border-bottom: 1px solid var(--border);
}
.back-btn { background: none; border: none; color: var(--text-primary); display: flex; }
.title { flex: 1; font-size: 17px; font-weight: 600; margin: 0; }
.tab-row {
  display: flex; background: var(--bg-card); border-bottom: 1px solid var(--border);
  position: sticky; top: 0; z-index: var(--z-base);
}
.tab {
  flex: 1; padding: 12px 0; font-size: 14px; background: none;
  border: none; border-bottom: 2px solid transparent; color: var(--text-secondary);
  cursor: pointer;
}
.tab.active { color: var(--primary, #4c8dff); border-bottom-color: var(--primary, #4c8dff); font-weight: 600; }
.status-err {
  padding: 10px 16px; font-size: 12px;
  background: color-mix(in srgb, var(--danger) 12%, transparent); color: var(--danger);
  display: flex; align-items: center; gap: 8px;
}
.link-btn { background: none; border: none; color: var(--primary, #4c8dff); cursor: pointer; font-size: 12px; }
.body { padding: var(--space-3); display: flex; flex-direction: column; gap: var(--space-3); }
.state { padding: 40px 20px; text-align: center; color: var(--text-secondary); font-size: 14px; }
.card {
  background: var(--bg-card); border: 1px solid var(--border);
  border-radius: 12px; padding: var(--space-3);
}
.card-h { margin: 0 0 10px; font-size: 14px; font-weight: 600; display: flex; align-items: center; gap: 10px; }
.wt-head { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
.wt-key { font-size: 15px; font-weight: 600; }
.wt-sub { font-size: 11px; color: var(--text-secondary); margin-top: 2px; }
.off { color: var(--danger); }
.wt-desc { font-size: 12px; color: var(--text-secondary); margin: 8px 0; line-height: 1.6; }
.route-chips { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 8px; }
.route-chip {
  font-size: 11px; padding: 4px 9px; border-radius: 999px;
  background: var(--bg-subtle); border: 1px solid var(--border); color: var(--text-primary);
  display: inline-flex; align-items: center; gap: 4px;
}
.chip-x { background: none; border: none; color: var(--danger); cursor: pointer; padding: 0; font-size: 13px; }
.none { font-size: 12px; color: var(--text-muted); }
.route-editor { margin-top: 10px; }
.routes-input, .inp {
  width: 100%; box-sizing: border-box; padding: 10px 12px; font-size: 13px;
  background: var(--bg-base); color: var(--text-primary);
  border: 1px solid var(--border); border-radius: 10px; outline: none; font-family: inherit;
}
.routes-input { font-family: 'SF Mono', Menlo, monospace; resize: vertical; }
.routes-input:focus, .inp:focus { border-color: var(--primary, #4c8dff); }
.editor-actions { display: flex; gap: 8px; margin-top: 8px; }
.mini-btn {
  padding: 7px 14px; font-size: 12px; border-radius: 999px; cursor: pointer;
  background: var(--bg-subtle); border: 1px solid var(--border); color: var(--text-primary);
}
.mini-btn.primary { background: var(--primary, #4c8dff); border-color: transparent; color: #fff; }
.mini-btn.danger { color: var(--danger); border-color: color-mix(in srgb, var(--danger) 40%, transparent); }
.mini-btn:disabled { opacity: 0.5; }
.form-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; margin-bottom: 12px; }
.form-grid label { display: flex; flex-direction: column; gap: 4px; font-size: 11px; color: var(--text-secondary); }
.form-grid label:nth-child(5), .form-grid label:nth-child(6) { grid-column: span 1; }
.row-card { display: flex; align-items: center; gap: 10px; }
.d-main { flex: 1; min-width: 0; }
.d-model { font-size: 14px; font-weight: 600; word-break: break-all; }
.d-meta { font-size: 11px; color: var(--text-secondary); margin-top: 2px; }
.d-reason { font-size: 11px; color: var(--text-muted); margin-top: 2px; }
.raw-json {
  margin: 0; padding: 10px; font-size: 11px; line-height: 1.6;
  background: var(--bg-subtle); border-radius: 8px; overflow-x: auto;
  font-family: 'SF Mono', Menlo, monospace; color: var(--text-primary);
  white-space: pre-wrap; word-break: break-all;
}
</style>
