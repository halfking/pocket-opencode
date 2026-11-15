<template>
  <div class="hd-overlay" @click.self="emit('close')">
    <div class="hd-card" role="dialog" aria-modal="true" :aria-label="dialogTitle">
      <header class="hd-head">
        <span class="hd-glyph" aria-hidden="true">🔄</span>
        <div class="hd-title">
          <h2>{{ dialogTitle }}</h2>
          <p class="hd-sub">{{ runId }}</p>
        </div>
        <button type="button" class="hd-close" aria-label="关闭" @click="emit('close')">
          <span class="material-symbols-outlined">close</span>
        </button>
      </header>

      <!-- 主机选择器：数据源 GET /api/v2/runtime/agents（mock: 内置 host-A/B/C） -->
      <label class="hd-field">
        <span class="hd-field-label">
          目标主机{{ mode === 'delegate' ? '（留空 = Auto balance，由 ACC 调度器挑选）' : '（必选）' }}
        </span>
        <select v-model="targetHost" class="hd-input" :disabled="loadingHosts || submitting">
          <option v-if="mode === 'delegate'" value="">Auto balance（自动均衡）</option>
          <option v-if="mode === 'reassign'" value="" disabled>选择主机…</option>
          <option v-for="h in hosts" :key="h.hostId" :value="h.hostId">
            {{ h.name }}{{ h.online ? '' : '（离线）' }}
          </option>
          <option v-if="loadingHosts" value="" disabled>主机列表加载中…</option>
        </select>
      </label>

      <p v-if="hostError" class="hd-error" role="alert">
        {{ hostError }}
        <button type="button" class="hd-retry" @click="loadHosts">重试</button>
      </p>

      <p v-if="hosts.length === 0 && !loadingHosts && !hostError" class="hd-hint">
        机器池为空 — 没有已注册的执行主机。请确认 ac 节点已通过 POST /api/v2/runtime/register 上线。
      </p>

      <!-- 移交原因（仅 delegate；审计字段） -->
      <label v-if="mode === 'delegate'" class="hd-field">
        <span class="hd-field-label">移交原因（可空）</span>
        <textarea
          v-model="reason"
          class="hd-input hd-textarea"
          rows="3"
          placeholder="如：host-A 无空闲 claude，需要切到 host-B"
        />
      </label>

      <p v-if="error" class="hd-error" role="alert">{{ error }}</p>

      <footer class="hd-actions">
        <button type="button" class="hd-btn" :disabled="submitting" @click="emit('close')">取消</button>
        <button
          type="button"
          class="hd-btn primary"
          :disabled="submitting || loadingHosts || (mode === 'reassign' && !targetHost)"
          @click="submit"
        >
          {{ submitting ? '提交中…' : mode === 'reassign' ? '确认重派' : '确认移交' }}
        </button>
      </footer>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * 重派 / 移交对话框（M2 任务13 · 功能2/3）。
 *
 * 两种模式共用一个组件：
 *   - mode='reassign'：POST /api/v2/tasks/:task_id/reassign {target_host}
 *     （语义 = 固定 target_host 的 delegate；主机必选，没有 Auto balance）；
 *   - mode='delegate'：POST /api/v2/tasks/:task_id/delegate
 *     {target_host?, reason?}（主机留空 = Auto balance）。
 *
 * 主机数据源：GET /api/v2/runtime/agents（mock 模式取 mock.ts 内置机器池）。
 * 409（已有 pending envelope / 租约易主）有显式中文提示，不做静默失败。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  AccApiError,
  delegateAccTask,
  listAccAgents,
  reassignAccTask,
  type AccHandoffResult,
  type AccRuntimeHost,
} from '../../api/accApi'
import { isMockActive, mockDelegateTask, mockListHosts, mockReassignTask } from './mock'

defineOptions({ name: 'AccHandoffDialog' })

const props = defineProps<{
  runId: string
  /** 'reassign' = 手动重派（主机必选）；'delegate' = 移交（可 Auto balance + 原因）。 */
  mode: 'reassign' | 'delegate'
}>()

const emit = defineEmits<{
  (e: 'close'): void
  /** 提交成功，父层据此展示 envelope 并刷新 run。 */
  (e: 'submitted', result: AccHandoffResult & { mode: 'reassign' | 'delegate' }): void
}>()

const hosts = ref<AccRuntimeHost[]>([])
const loadingHosts = ref(false)
const hostError = ref('')
const targetHost = ref('')
const reason = ref('')
const submitting = ref(false)
const error = ref('')

const dialogTitle = computed(() => (props.mode === 'reassign' ? '重派到指定主机' : '移交到其他主机'))

async function loadHosts(): Promise<void> {
  loadingHosts.value = true
  hostError.value = ''
  try {
    hosts.value = isMockActive() ? await mockListHosts() : await listAccAgents()
  } catch (e) {
    hostError.value = `主机列表加载失败：${e instanceof Error ? e.message : String(e)}`
  } finally {
    loadingHosts.value = false
  }
}

async function submit(): Promise<void> {
  error.value = ''
  if (props.mode === 'reassign' && !targetHost.value) {
    error.value = '重派必须选择目标主机（Auto balance 请用「移交」）'
    return
  }
  submitting.value = true
  try {
    let result: AccHandoffResult
    if (isMockActive()) {
      if (props.mode === 'reassign') {
        const r = await mockReassignTask(props.runId, targetHost.value)
        result = { envelopeId: r.envelopeId, taskId: r.taskId, status: r.status, targetHost: r.targetHost, raw: r }
      } else {
        const r = await mockDelegateTask(props.runId, { targetHost: targetHost.value || undefined, reason: reason.value })
        result = { envelopeId: r.envelopeId, taskId: r.taskId, status: r.status, targetHost: r.targetHost, raw: r }
      }
    } else if (props.mode === 'reassign') {
      result = await reassignAccTask(props.runId, targetHost.value)
    } else {
      result = await delegateAccTask(props.runId, {
        targetHost: targetHost.value || undefined,
        reason: reason.value,
      })
    }
    emit('submitted', { ...result, mode: props.mode })
    emit('close')
  } catch (e) {
    if (e instanceof AccApiError && e.status === 409) {
      error.value = `移交冲突（409）：${e.message}。请等待当前移交包被接受（或失败）后再试。`
    } else {
      error.value = e instanceof Error ? e.message : String(e)
    }
  } finally {
    submitting.value = false
  }
}

function onKey(e: KeyboardEvent): void {
  if (e.key === 'Escape') emit('close')
}
onMounted(() => {
  document.addEventListener('keydown', onKey)
  void loadHosts()
})
onBeforeUnmount(() => document.removeEventListener('keydown', onKey))
</script>

<style scoped>
.hd-overlay {
  position: fixed;
  /* 键盘在场时浮层收在键盘上沿之下，弹层内的输入框与确认按钮不会被盖住。
     同 BottomSheet.vue / marketplace 的 confirm-overlay。 */
  inset: 0 0 var(--kb-inset, 0px) 0;
  background: rgba(0, 0, 0, 0.45);
  display: flex;
  align-items: flex-end;
  justify-content: center;
  /* z-index 门禁：裸数字必须走 token。
     审批/移交是**模态**（遮罩 + 底部弹出卡片），与项目里其它模态
     （ConfirmDialog / AgentMarketView.confirm-overlay）同层级 ⇒ 用 --z-dialog。 */
  z-index: var(--z-dialog);
}
.hd-card {
  width: 100%;
  max-width: 560px;
  background: var(--bg-card, #fff);
  border-radius: var(--radius-lg, 16px) var(--radius-lg, 16px) 0 0;
  padding: var(--space-4, 16px);
  padding-bottom: calc(var(--space-4, 16px) + env(safe-area-inset-bottom, 0px));
  display: flex;
  flex-direction: column;
  gap: var(--space-3, 12px);
}
.hd-head { display: flex; align-items: center; gap: var(--space-2, 8px); }
.hd-glyph { font-size: 22px; }
.hd-title { flex: 1; min-width: 0; }
.hd-title h2 { margin: 0; font-size: var(--text-md, 16px); color: var(--text-primary, #111); }
.hd-sub { margin: 2px 0 0; font-size: var(--text-smd, 13px); color: var(--text-tertiary, #888); word-break: break-all; }
.hd-close { border: none; background: transparent; color: var(--text-secondary, #555); padding: 6px; cursor: pointer; }

.hd-field { display: flex; flex-direction: column; gap: 4px; }
.hd-field-label { font-size: var(--text-smd, 13px); color: var(--text-secondary, #555); }
.hd-input {
  border: 1px solid var(--border, #ddd);
  border-radius: var(--radius-sm, 8px);
  background: var(--bg-base, #f7f7f8);
  color: var(--text-primary, #111);
  padding: 9px 10px;
  font-size: var(--text-smd, 13px);
  font-family: inherit;
}
.hd-textarea { resize: vertical; }
.hd-hint { margin: 0; color: var(--text-tertiary, #888); font-size: 12px; }
.hd-error { margin: 0; color: #dc2626; font-size: var(--text-smd, 13px); word-break: break-all; }
.hd-retry {
  border: 1px solid #fca5a5;
  border-radius: var(--radius-sm, 8px);
  background: #fff;
  color: #dc2626;
  padding: 4px 10px;
  cursor: pointer;
  font-size: 12px;
  margin-left: 6px;
}

.hd-actions { display: flex; gap: 8px; justify-content: flex-end; }
.hd-btn {
  border: 1px solid var(--border, #ddd);
  border-radius: var(--radius-sm, 8px);
  background: transparent;
  color: var(--text-primary, #111);
  padding: 9px 16px;
  font-size: var(--text-smd, 13px);
  cursor: pointer;
}
.hd-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.hd-btn.primary { background: #8b5cf6; border-color: #8b5cf6; color: #fff; }
</style>
