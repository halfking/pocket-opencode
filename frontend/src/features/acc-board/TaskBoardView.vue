<template>
  <div class="page">
    <HeaderActionsPortal>
      <button type="button" class="header-action" aria-label="ACC 连接设置" @click="showSettings = !showSettings">
        <span class="material-symbols-outlined">settings</span>
      </button>
    </HeaderActionsPortal>

    <!-- Mock 提示横幅 -->
    <div v-if="mockActive" class="mock-banner">
      Mock 演示模式 — 数据为内置样例（URL 加 ?mock=1 或下方设置开关）
    </div>

    <!-- 连接设置面板：ACC 地址 + Bearer token（localStorage 持久化） -->
    <section v-if="showSettings" class="settings">
      <h2>ACC 连接设置</h2>
      <label class="field">
        <span>ACC 地址</span>
        <input v-model="draftBase" class="input" type="text" placeholder="http://127.0.0.1:4101" />
      </label>
      <label class="field">
        <span>Bearer Token（ACC JWT）</span>
        <textarea v-model="draftToken" class="input" rows="2" placeholder="粘贴 ACC 签发的 JWT" />
      </label>
      <label class="check">
        <input v-model="draftMock" type="checkbox" />
        Mock 演示模式（不请求 ACC）
      </label>
      <div class="settings-actions">
        <button type="button" class="btn" @click="showSettings = false">收起</button>
        <button type="button" class="btn primary" @click="saveSettings">保存并刷新</button>
      </div>
      <p class="settings-hint">Token 仅存本机 localStorage；ACC RequireAuth 校验失败会返回 401。</p>
    </section>

    <!-- 启动新 Run -->
    <section class="launcher">
      <button type="button" class="launcher-toggle" @click="showLauncher = !showLauncher">
        <span class="material-symbols-outlined">add</span>
        启动新 SOP Run
      </button>
      <div v-if="showLauncher" class="launcher-body">
        <label class="field">
          <span>模板</span>
          <select v-model="draftTemplate" class="input">
            <option value="">选择模板…</option>
            <option v-for="t in templates" :key="t.name" :value="t.name">{{ t.name }}（{{ t.nodes.length }} 节点）</option>
          </select>
        </label>
        <label class="field">
          <span>Project ID</span>
          <input v-model="draftProject" class="input" type="text" placeholder="test-proj-001" />
        </label>
        <label class="field">
          <span>Variables（JSON，可空）</span>
          <textarea v-model="draftVariables" class="input" rows="2" placeholder='{"repo": "example-api"}' />
        </label>
        <button type="button" class="btn primary" :disabled="creating || !draftProject.trim()" @click="createRun">
          {{ creating ? '启动中…' : '启动' }}
        </button>
        <p v-if="launcherError" class="inline-error" role="alert">{{ launcherError }}</p>
      </div>
    </section>

    <div class="toolbar">
      <span class="source-note">{{ listNote }}</span>
      <button type="button" class="tool" :disabled="loading" @click="load">刷新</button>
    </div>

    <div v-if="error" class="error" role="alert">
      <p>{{ error }}</p>
      <button type="button" @click="load">重试</button>
    </div>

    <div v-else-if="loading && runs.length === 0" class="state">加载中…</div>
    <div v-else-if="runs.length === 0" class="state">
      <p>还没有 SOP Run</p>
      <p class="state-sub">用上方「启动新 SOP Run」创建，或点右上角设置登记已有 run_id。</p>
    </div>

    <!-- 任务列表：状态徽章 + 进度 -->
    <main v-else class="list">
      <article v-for="(item, idx) in runs" :key="runKey(item, idx)" class="card" @click="openRun(item)">
        <div class="card-head">
          <span class="run-id">{{ runIdOf(item) }}</span>
          <span class="badge" :class="`tone-${visualOf(item).tone}`">
            <template v-if="visualOf(item).glyph">{{ visualOf(item).glyph }}</template>
            {{ visualOf(item).label }}
          </span>
        </div>
        <div v-if="runOf(item)" class="meta">
          <span>{{ progressText(item) }}</span>
          <span v-if="runOf(item)!.projectId">项目 {{ runOf(item)!.projectId }}</span>
          <span v-if="runOf(item)!.createdAt">{{ formatTime(runOf(item)!.createdAt) }}</span>
        </div>
        <!-- M2 任务13：卡片进度条（已完成节点数 / 总节点数） -->
        <div v-if="runOf(item) && runOf(item)!.nodes.length > 0" class="card-progress" :aria-label="`节点进度 ${cardPercent(item)}%`">
          <div class="card-progress-fill" :style="{ width: `${cardPercent(item)}%` }" />
        </div>
        <div v-else-if="errorOf(item)" class="meta unreachable">不可达: {{ errorOf(item) }}</div>
      </article>
    </main>

    <!-- 登记已有 run（ACC 无列表端点时的兜底入口） -->
    <section class="register">
      <input v-model="draftRunId" class="input grow" type="text" placeholder="登记已有 run_id（如 run-7c1…）" />
      <button type="button" class="tool" :disabled="!draftRunId.trim()" @click="registerRun">登记</button>
    </section>
  </div>
</template>

<script setup lang="ts">
/**
 * ACC 任务看板（M1 任务9 · 操作1）：SOP runs 列表 + 连接设置 + 启动入口。
 *
 * 列表数据源（按优先级）：
 *   1. mock 模式（?mock=1 / 设置开关）→ 内置样例；
 *   2. GET /api/v2/sop/runs（若 ACC 已实现列表端点）；
 *   3. 兜底：本地 run 登记表逐个单查（M0 契约只有单查）。
 */
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'
import {
  createAccSopRun,
  getAccTemplates,
  getAccBase,
  setAccBase,
  getAccToken,
  setAccToken,
  isAccUnreachable,
  listAccSopRuns,
  registerAccRun,
  type AccSopRun,
  type AccSopTemplate,
} from '../../api/accApi'
import { formatTime, progressPercent, runVisual, summarizeProgress } from './statusMeta'
import { adoptMockQuery, isMockActive, mockCreateRun, mockGetTemplates, mockListRuns, setMockFlag } from './mock'

defineOptions({ name: 'AccTaskBoardView' })

const router = useRouter()

const loading = ref(false)
const error = ref('')
const creating = ref(false)
const launcherError = ref('')
const runs = ref<{ run: AccSopRun | null; error?: string }[]>([])
const listSource = ref<'api' | 'registry' | 'mock' | 'snapshot'>('registry')
const templates = ref<AccSopTemplate[]>([])

const showSettings = ref(false)
const showLauncher = ref(false)
const draftBase = ref(getAccBase())
const draftToken = ref(getAccToken())
const draftMock = ref(isMockActive())
const draftTemplate = ref('')
const draftProject = ref('')
const draftVariables = ref('')
const draftRunId = ref('')

const mockActive = isMockActive()

// 深链/看板带 ?mock=1 时记到 sessionStorage —— 应用内跳详情后 mock 仍生效。
adoptMockQuery()

const listNote = computed(() => {
  if (mockActive) return 'mock 数据'
  if (listSource.value === 'snapshot') return '离线缓存（上次成功加载的数据）'
  if (listSource.value === 'api') return '来自 ACC 列表端点'
  return '来自本地登记表单查（ACC 暂无列表端点）'
})

// ---------------------------------------------------------------------------
// 离线缓存（M2 任务13 可选加分）：任务列表 localStorage 快照。
// ACC 暂不可达时回放上次成功加载的列表并明确标注「离线缓存」；配合
// public/acc-offline-sw.js（静态资源 SWR + 应用壳 network-first）使用。
// ---------------------------------------------------------------------------

const ACC_LAST_RUNS_KEY = 'acc_board_last_runs'

interface SnapshotPayload {
  savedAt: string
  items: { run: AccSopRun | null; error?: string }[]
}

function saveSnapshot(items: { run: AccSopRun | null; error?: string }[]): void {
  try {
    const payload: SnapshotPayload = { savedAt: new Date().toISOString(), items }
    localStorage.setItem(ACC_LAST_RUNS_KEY, JSON.stringify(payload))
  } catch {
    /* 私有模式等写入失败可忽略 */
  }
}

function readSnapshot(): SnapshotPayload | null {
  try {
    const raw = localStorage.getItem(ACC_LAST_RUNS_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as SnapshotPayload
    if (!parsed || !Array.isArray(parsed.items)) return null
    return parsed
  } catch {
    return null
  }
}

/** 注册离线缓存 SW（仅看板页触发；不支持 SW 的环境静默跳过）。 */
function registerOfflineSw(): void {
  if (typeof navigator === 'undefined' || !('serviceWorker' in navigator)) return
  // vite base 默认 '/'；生产与 dev 均从根取 sw 文件。
  navigator.serviceWorker.register('/acc-offline-sw.js').catch(() => {
    /* SW 注册失败不影响功能（离线缓存是加分项） */
  })
}

function saveSettings(): void {
  setAccBase(draftBase.value)
  setAccToken(draftToken.value)
  setMockFlag(draftMock.value)
  // mock 开关影响整页行为，直接刷新以保持 query/hash 与状态一致。
  window.location.reload()
}

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    if (mockActive) {
      const res = await mockListRuns()
      runs.value = res.items
      listSource.value = 'mock'
    } else {
      const res = await listAccSopRuns(20)
      runs.value = res.items
      listSource.value = res.source
      saveSnapshot(res.items)
    }
    if (templates.value.length === 0) {
      templates.value = mockActive ? await mockGetTemplates() : await getAccTemplates().catch(() => [])
    }
  } catch (e) {
    const rawError =
      (isAccUnreachable(e) ? '无法连接 ACC，请点右上角设置检查地址与 token。' : '') +
      (e instanceof Error ? e.message : String(e))
    // 离线兜底：上次成功加载的快照仍可看（明确标注，不冒充实时数据）。
    const snap = mockActive ? null : readSnapshot()
    if (snap) {
      runs.value = snap.items
      listSource.value = 'snapshot'
      error.value = `离线模式 — ${rawError}（以下为 ${formatTime(snap.savedAt)} 缓存的任务列表）`
    } else {
      error.value = rawError
    }
  } finally {
    loading.value = false
  }
}

async function createRun(): Promise<void> {
  launcherError.value = ''
  let variables: Record<string, unknown> | undefined
  const rawVars = draftVariables.value.trim()
  if (rawVars) {
    try {
      variables = JSON.parse(rawVars) as Record<string, unknown>
    } catch {
      launcherError.value = 'Variables 不是合法 JSON'
      return
    }
  }
  creating.value = true
  try {
    const res = mockActive
      ? await mockCreateRun({ template: draftTemplate.value, projectId: draftProject.value.trim() })
      : await createAccSopRun({
          template: draftTemplate.value || undefined,
          projectId: draftProject.value.trim(),
          variables,
        })
    showLauncher.value = false
    draftProject.value = ''
    draftVariables.value = ''
    await load()
    router.push(`/acc/runs/${encodeURIComponent(res.runId)}`)
  } catch (e) {
    launcherError.value = e instanceof Error ? e.message : String(e)
  } finally {
    creating.value = false
  }
}

function registerRun(): void {
  const runId = draftRunId.value.trim()
  if (!runId) return
  registerAccRun(runId)
  draftRunId.value = ''
  void load()
}

function openRun(item: { run: AccSopRun | null; error?: string }): void {
  const id = item.run?.runId
  if (id) router.push(`/acc/runs/${encodeURIComponent(id)}`)
}

// 列表项可能是「单查失败」的占位 —— 访问器统一兜底，避免模板里链式判空。
function runOf(item: { run: AccSopRun | null }): AccSopRun | null {
  return item.run
}
function runIdOf(item: { run: AccSopRun | null }): string {
  return item.run?.runId ?? '(未知 run)'
}
function runKey(item: { run: AccSopRun | null; error?: string }, idx: number): string {
  return item.run?.runId ?? `unreachable-${idx}`
}
function visualOf(item: { run: AccSopRun | null }) {
  return runVisual(item.run?.status ?? 'unknown')
}
function progressText(item: { run: AccSopRun | null }): string {
  const run = item.run
  if (!run) return ''
  const p = summarizeProgress(run.nodes)
  return (
    `${p.done}/${p.total} 节点完成` +
    (p.waiting > 0 ? ` · ${p.waiting} 个待审批` : '') +
    (p.handoff > 0 ? ` · 🔄${p.handoff} 移交中` : '')
  )
}
/** M2 任务13：卡片进度条百分比。 */
function cardPercent(item: { run: AccSopRun | null }): number {
  return progressPercent(item.run?.nodes ?? [])
}
function errorOf(item: { run: AccSopRun | null; error?: string }): string {
  return item.error ?? ''
}

onMounted(() => {
  void load()
  registerOfflineSw()
})
</script>

<style scoped>
.page { min-height: 100%; background: var(--bg-base); padding-bottom: 16px; }
.header-action { color: var(--brand-primary, #2563eb); background: transparent; border: none; cursor: pointer; padding: 4px; }

.mock-banner {
  margin: var(--space-3, 12px);
  padding: 8px 12px;
  border-radius: var(--radius-sm, 8px);
  background: #fef3c7;
  color: #92400e;
  font-size: var(--text-smd, 13px);
}

.settings, .launcher {
  margin: var(--space-3, 12px);
  padding: 12px;
  background: var(--bg-card, #fff);
  border: 1px solid var(--border, #e5e7eb);
  border-radius: var(--radius-md, 12px);
}
.settings h2 { margin: 0 0 10px; font-size: var(--text-md, 15px); color: var(--text-primary, #111); }
.field { display: flex; flex-direction: column; gap: 4px; margin-bottom: 10px; }
.field > span { font-size: var(--text-smd, 13px); color: var(--text-secondary, #555); }
.input {
  border: 1px solid var(--border, #ddd);
  border-radius: var(--radius-sm, 8px);
  background: var(--bg-base, #f7f7f8);
  color: var(--text-primary, #111);
  padding: 9px 10px;
  font-size: var(--text-smd, 13px);
  font-family: inherit;
}
textarea.input { resize: vertical; }
.check { display: flex; gap: 8px; align-items: center; font-size: var(--text-smd, 13px); color: var(--text-secondary, #555); margin-bottom: 10px; }
.settings-actions, .launcher-body > .btn { display: flex; gap: 8px; }
.settings-actions { justify-content: flex-end; }
.btn {
  border: 1px solid var(--border, #ddd);
  border-radius: var(--radius-sm, 8px);
  background: transparent;
  color: var(--text-primary, #111);
  padding: 8px 14px;
  font-size: var(--text-smd, 13px);
  cursor: pointer;
}
.btn:disabled { opacity: 0.5; cursor: not-allowed; }
.btn.primary { background: var(--brand-primary, #2563eb); border-color: var(--brand-primary, #2563eb); color: #fff; }
.settings-hint { margin: 10px 0 0; font-size: 12px; color: var(--text-tertiary, #888); }

.launcher-toggle {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  border: none;
  background: transparent;
  color: var(--brand-primary, #2563eb);
  font-size: var(--text-smd, 13px);
  font-weight: 600;
  cursor: pointer;
  padding: 0;
}
.launcher-body { margin-top: 10px; }

.toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 0 var(--space-3, 12px) var(--space-2, 8px);
}
.source-note { font-size: 12px; color: var(--text-tertiary, #888); }
.tool {
  border: 1px solid var(--border, #ddd);
  border-radius: var(--radius-sm, 8px);
  background: var(--bg-card, #fff);
  color: var(--text-primary, #111);
  padding: 7px 12px;
  font-size: var(--text-smd, 13px);
  cursor: pointer;
}
.tool:disabled { opacity: 0.5; }

.error {
  margin: var(--space-3, 12px);
  padding: 10px 12px;
  border: 1px solid #fca5a5;
  border-radius: var(--radius-sm, 8px);
  background: #fef2f2;
  color: #dc2626;
  font-size: var(--text-smd, 13px);
  display: flex;
  gap: 10px;
  align-items: center;
  justify-content: space-between;
}
.error p { margin: 0; word-break: break-all; }
.error button {
  border: 1px solid #fca5a5;
  border-radius: var(--radius-sm, 8px);
  background: #fff;
  color: #dc2626;
  padding: 6px 12px;
  cursor: pointer;
  white-space: nowrap;
}
.state { padding: 32px 16px; text-align: center; color: var(--text-tertiary, #888); font-size: var(--text-smd, 13px); }
.state-sub { margin: 6px 0 0; font-size: 12px; }

.list { display: flex; flex-direction: column; gap: var(--space-2, 8px); padding: 0 var(--space-3, 12px); }
.card {
  background: var(--bg-card, #fff);
  border: 1px solid var(--border, #e5e7eb);
  border-radius: var(--radius-md, 12px);
  padding: 12px;
  cursor: pointer;
}
.card-head { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.run-id { font-size: var(--text-smd, 13px); color: var(--text-primary, #111); font-weight: 600; word-break: break-all; }
.meta { display: flex; gap: 10px; flex-wrap: wrap; margin-top: 6px; font-size: 12px; color: var(--text-tertiary, #888); }
.meta.unreachable { color: #dc2626; word-break: break-all; }

/* M2 任务13：卡片进度条 */
.card-progress {
  margin-top: 8px;
  height: 4px;
  border-radius: 999px;
  background: var(--bg-base, #f1f5f9);
  overflow: hidden;
}
.card-progress-fill {
  height: 100%;
  border-radius: 999px;
  background: #16a34a;
  transition: width 0.3s ease;
}

.badge {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 3px 10px;
  border-radius: 999px;
  font-size: var(--text-smd, 13px);
  font-weight: 600;
  color: #fff;
  white-space: nowrap;
}
.badge.tone-done { background: #16a34a; }
.badge.tone-run { background: #eab308; }
.badge.tone-fail { background: #dc2626; }
.badge.tone-pending { background: #94a3b8; }
.badge.tone-gate { background: #f59e0b; }
.badge.tone-handoff { background: #8b5cf6; }
.badge.tone-cancel { background: #64748b; }
.badge.tone-skip { background: #64748b; }
.badge.tone-unknown { background: #94a3b8; }

.register { display: flex; gap: 8px; padding: var(--space-3, 12px); }
.grow { flex: 1; min-width: 0; }
</style>
