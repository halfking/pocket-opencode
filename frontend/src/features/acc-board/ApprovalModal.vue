<template>
  <div class="am-overlay" @click.self="emit('close')">
    <div class="am-card" role="dialog" aria-modal="true" :aria-label="`审批节点 ${node.name}`">
      <header class="am-head">
        <span class="am-glyph">⏸</span>
        <div class="am-title">
          <h2>审批：{{ node.name }}</h2>
          <p class="am-sub">{{ runId }} · 节点 {{ node.nodeId }}</p>
        </div>
        <button type="button" class="am-close" aria-label="关闭" @click="emit('close')">
          <span class="material-symbols-outlined">close</span>
        </button>
      </header>

      <!-- 决定三选一：通过 / 驳回 / 退回 -->
      <div class="am-seg" role="radiogroup" aria-label="审批决定">
        <button
          v-for="opt in DECISIONS"
          :key="opt.value"
          type="button"
          role="radio"
          :aria-checked="decision === opt.value"
          :class="['am-seg-item', `is-${opt.value}`, { active: decision === opt.value }]"
          @click="decision = opt.value"
        >
          {{ opt.label }}
        </button>
      </div>

      <!-- 退回目标节点选择器（仅 rollback 时可选） -->
      <label class="am-field">
        <span class="am-field-label">回退目标节点{{ decision === 'rollback' ? '' : '（仅退回时生效）' }}</span>
        <select v-model="rollbackTo" class="am-input" :disabled="decision !== 'rollback'">
          <option value="">选择节点…</option>
          <option v-for="c in rollbackCandidates" :key="c.nodeId" :value="c.nodeId">
            {{ c.name }}（{{ c.nodeId }}）
          </option>
        </select>
      </label>

      <label class="am-field">
        <span class="am-field-label">评论</span>
        <textarea
          v-model="comment"
          class="am-input am-textarea"
          rows="3"
          placeholder="给执行方的说明（可空）"
        />
      </label>

      <label class="am-field">
        <span class="am-field-label">审批人</span>
        <input v-model="approver" class="am-input" type="text" placeholder="记录是谁批的" />
      </label>

      <p v-if="error" class="am-error" role="alert">{{ error }}</p>

      <footer class="am-actions">
        <button type="button" class="am-btn" :disabled="submitting" @click="emit('close')">取消</button>
        <button
          type="button"
          class="am-btn primary"
          :class="`confirm-${decision}`"
          :disabled="submitting || (decision === 'rollback' && !rollbackTo)"
          @click="submit"
        >
          {{ submitting ? '提交中…' : '提交' }}
        </button>
      </footer>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useApiError } from '../../composables/useApiError'
/**
 * HumanGate 审批弹窗（M1 任务9）。
 *
 * 契约：POST /api/v2/sop/runs/:run_id/gates/:node_id/approve
 *   {decision: approve|reject|rollback, rollback_to, comment, approver}
 *
 * mock 模式下走 mock.approveMockGate，状态机同样推进（见 mock.ts）。
 */
import { onBeforeUnmount, onMounted, ref } from 'vue'
import {
  approveAccGate,
  getAccApprover,
  setAccApprover,
  type AccGateDecisionValue,
  type AccSopNode,
} from '../../api/accApi'
import { mockApproveGate, isMockActive } from './mock'

const props = defineProps<{
  runId: string
  node: AccSopNode
  /** 可选的回退目标（一般是本节点之前的节点）。 */
  rollbackCandidates: AccSopNode[]
}>()

const emit = defineEmits<{
  (e: 'close'): void
  /** 审批提交成功（父层据此刷新详情）。 */
  (e: 'submitted', decision: AccGateDecisionValue): void
}>()

const DECISIONS: { value: AccGateDecisionValue; label: string }[] = [
  { value: 'approve', label: '通过' },
  { value: 'reject', label: '驳回' },
  { value: 'rollback', label: '退回' },
]

const decision = ref<AccGateDecisionValue>('approve')
const rollbackTo = ref('')
const comment = ref('')
const approver = ref(getAccApprover())
const submitting = ref(false)
const error = ref('')
const apiError = useApiError()

async function submit(): Promise<void> {
  error.value = ''
  if (decision.value === 'rollback' && !rollbackTo.value) {
    error.value = '退回必须选择目标节点'
    return
  }
  submitting.value = true
  try {
    setAccApprover(approver.value)
    const input = {
      decision: decision.value,
      rollbackTo: decision.value === 'rollback' ? rollbackTo.value : undefined,
      comment: comment.value,
      approver: approver.value,
    }
    if (isMockActive()) {
      await mockApproveGate(props.runId, props.node.nodeId, input)
    } else {
      await approveAccGate(props.runId, props.node.nodeId, input)
    }
    emit('submitted', decision.value)
    emit('close')
  } catch (e) {
    console.warn('[acc-board] gate approval failed', e)
    error.value = apiError(e, '审批提交失败，请稍后重试')
  } finally {
    submitting.value = false
  }
}

/** Esc 关闭（移动端 WebView 上键盘弹出时的逃生口）。 */
function onKey(e: KeyboardEvent): void {
  if (e.key === 'Escape') emit('close')
}
onMounted(() => document.addEventListener('keydown', onKey))
onBeforeUnmount(() => document.removeEventListener('keydown', onKey))
</script>

<style scoped>
.am-overlay {
  position: fixed;
  inset: 0 0 var(--kb-inset, 0px) 0;
  background: rgba(0, 0, 0, 0.45);
  display: flex;
  align-items: flex-end;
  justify-content: center;
  z-index: var(--z-dialog);
}
.am-card {
  max-height: 100%;
  overflow-y: auto;
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
.am-head { display: flex; align-items: center; gap: var(--space-2, 8px); }
.am-glyph { font-size: var(--text-2xl); }
.am-title { flex: 1; min-width: 0; }
.am-title h2 { margin: 0; font-size: var(--text-md, 16px); color: var(--text-primary, #111); }
.am-sub { margin: 2px 0 0; font-size: var(--text-smd, 13px); color: var(--text-tertiary, #888); word-break: break-all; }
.am-close { border: none; background: transparent; color: var(--text-secondary, #555); padding: 6px; cursor: pointer; }

.am-seg { display: flex; gap: 6px; }
.am-seg-item {
  flex: 1;
  padding: 9px 0;
  border: 1px solid var(--border, #ddd);
  border-radius: var(--radius-sm, 8px);
  background: transparent;
  color: var(--text-secondary, #555);
  font-size: var(--text-smd, 13px);
  cursor: pointer;
}
.am-seg-item.active { border-width: 2px; font-weight: 600; color: var(--text-primary, #111); }
.am-seg-item.is-approve.active { border-color: #16a34a; color: #16a34a; }
.am-seg-item.is-reject.active { border-color: #dc2626; color: #dc2626; }
.am-seg-item.is-rollback.active { border-color: #d97706; color: #d97706; }

.am-field { display: flex; flex-direction: column; gap: 4px; }
.am-field-label { font-size: var(--text-smd, 13px); color: var(--text-secondary, #555); }
.am-input {
  border: 1px solid var(--border, #ddd);
  border-radius: var(--radius-sm, 8px);
  background: var(--bg-base, #f7f7f8);
  color: var(--text-primary, #111);
  padding: 9px 10px;
  font-size: var(--text-smd, 13px);
  font-family: inherit;
}
.am-textarea { resize: vertical; }
.am-error { margin: 0; color: #dc2626; font-size: var(--text-smd, 13px); }

.am-actions { display: flex; gap: 8px; justify-content: flex-end; }
.am-btn {
  border: 1px solid var(--border, #ddd);
  border-radius: var(--radius-sm, 8px);
  background: transparent;
  color: var(--text-primary, #111);
  padding: 9px 16px;
  font-size: var(--text-smd, 13px);
  cursor: pointer;
}
.am-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.am-btn.primary { background: var(--brand-primary, #2563eb); border-color: var(--brand-primary, #2563eb); color: #fff; }
.am-btn.confirm-approve { background: #16a34a; border-color: #16a34a; }
.am-btn.confirm-reject { background: #dc2626; border-color: #dc2626; }
.am-btn.confirm-rollback { background: #d97706; border-color: #d97706; }
</style>
