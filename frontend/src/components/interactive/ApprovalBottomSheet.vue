<template>
  <BottomSheet
    :model-value="visible"
    :title="titleText"
    :closable="closable"
    :close-on-overlay="!submitting"
    :swipeable="!submitting"
    height="half"
    role="dialog"
    aria-modal="true"
    :aria-label="titleText"
    @update:model-value="onVisibleChange"
  >
    <!-- Summary header: action + target resource -->
    <section class="approval-summary">
      <div class="approval-row">
        <span class="label">动作</span>
        <span class="value">{{ actionLabel }}</span>
      </div>
      <div class="approval-row">
        <span class="label">来源</span>
        <span class="value">{{ sourceLabel }}</span>
      </div>
      <div class="approval-row">
        <span class="label">影响范围</span>
        <span class="value">{{ scopeLabel }}</span>
      </div>
    </section>

    <!-- Long / collapsible details -->
    <details v-if="details" class="approval-details">
      <summary>查看完整详情</summary>
      <pre class="approval-details-body">{{ details }}</pre>
    </details>

    <!-- Server-confirmed status banner -->
    <div
      v-if="serverConfirmed === false"
      class="approval-banner approval-banner--warn"
      role="alert"
    >
      <span>服务端尚未确认。</span>
      <span class="approval-banner-hint">已发送的请求会保留，可重试。</span>
    </div>
    <div
      v-else-if="serverConfirmed === true"
      class="approval-banner approval-banner--ok"
      role="status"
    >
      <span>已确认。</span>
    </div>

    <!-- Decision footer -->
    <template #footer>
      <div class="approval-actions">
        <button
          type="button"
          class="btn btn-secondary"
          :disabled="submitting"
          @click="onReject"
        >
          拒绝
        </button>
        <button
          type="button"
          class="btn btn-outline"
          :disabled="submitting"
          @click="onAllow('once')"
        >
          仅此一次
        </button>
        <button
          type="button"
          class="btn btn-primary"
          :disabled="submitting"
          @click="onAllow('always')"
        >
          本会话允许
        </button>
      </div>
      <p class="approval-hint">
        “本会话允许”将持久化到当前 session；不会影响其他会话或工作区。
      </p>
    </template>
  </BottomSheet>
</template>

<script setup lang="ts">
/**
 * ApprovalBottomSheet — typed Bottom Sheet for the approval flow (PR8).
 *
 * Implements the UX rules from
 *   docs/优化v4/15-PR1-契约冻结与发布前置.md §3.5
 *   docs/优化v4/08-移动端UI与交互规范.md §3.3
 *
 * Goals (PR8 boundary, see 14 §2 row 8):
 *   - One visible primary decision ("本会话允许") plus two secondary
 *     actions ("仅此一次" / "拒绝") — no swipe-only confirmation.
 *   - Until the server confirms, status banner stays "server_confirmed
 *     === false"; the UI does not flip to "已批准" on a click alone.
 *   - Uses the existing shared BottomSheet primitive + tokens, not
 *     bespoke colors / z-index.
 *
 * PR8 does NOT:
 *   - Send network requests; parent component owns the API call and
 *     sets `serverConfirmed` after the response.
 *   - Modify any existing view; this is an opt-in component.
 */

import { computed } from 'vue'
import BottomSheet from '../base/BottomSheet.vue'

export type ApprovalDecision = 'once' | 'always' | 'reject'

export interface ApprovalSheetProps {
  /** Whether the sheet is currently visible. */
  visible: boolean
  /** Human-readable action title (e.g. "调用工具：bash"). */
  action?: string
  /** Source resource, e.g. "instance:acme · session:session-abc". */
  source?: string
  /** Scope / impact summary, e.g. "/home/user/project · 1 个文件". */
  scope?: string
  /** Optional long details (e.g. command, diff). */
  details?: string
  /** True while the request is in-flight; buttons become disabled. */
  submitting?: boolean
  /** True when the server has confirmed the decision. */
  serverConfirmed?: boolean | null
  /** Allow closing the sheet (disabled while submitting). */
  closable?: boolean
}

const props = withDefaults(defineProps<ApprovalSheetProps>(), {
  action: '',
  source: '',
  scope: '',
  details: '',
  submitting: false,
  serverConfirmed: null,
  closable: true,
})

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
  (e: 'decision', value: ApprovalDecision): void
}>()

const titleText = computed(() => '需要你的授权')
const actionLabel = computed(() => props.action || '未知动作')
const sourceLabel = computed(() => props.source || '未知来源')
const scopeLabel = computed(() => props.scope || '未指定')

function onVisibleChange(v: boolean): void {
  if (!v && props.submitting) return
  emit('update:visible', v)
}

function onAllow(scope: 'once' | 'always'): void {
  emit('decision', scope)
}

function onReject(): void {
  emit('decision', 'reject')
}
</script>

<style scoped>
.approval-summary {
  display: grid;
  gap: 8px;
  padding: 12px 16px;
  background: var(--surface-soft, #f6f7f9);
  border-radius: 8px;
}
.approval-row {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  font-size: 14px;
}
.approval-row .label {
  color: var(--text-secondary, #6b7280);
}
.approval-row .value {
  color: var(--text-primary, #1f2937);
  font-weight: 500;
  word-break: break-all;
  text-align: right;
}
.approval-details {
  margin-top: 12px;
  border-top: 1px solid var(--border, #e5e7eb);
  padding-top: 12px;
}
.approval-details summary {
  cursor: pointer;
  color: var(--text-secondary, #6b7280);
  font-size: 13px;
  padding: 4px 0;
}
.approval-details-body {
  background: var(--surface-soft, #f6f7f9);
  padding: 12px;
  border-radius: 6px;
  font-size: 12px;
  overflow-x: auto;
  white-space: pre-wrap;
  word-break: break-word;
}
.approval-banner {
  margin-top: 12px;
  padding: 12px;
  border-radius: 8px;
  font-size: 13px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.approval-banner--warn {
  background: var(--warning-bg, #fff7e6);
  color: var(--warning-fg, #92400e);
}
.approval-banner--ok {
  background: var(--success-bg, #ecfdf5);
  color: var(--success-fg, #065f46);
}
.approval-banner-hint {
  opacity: 0.85;
  font-size: 12px;
}
.approval-actions {
  display: flex;
  gap: 8px;
  margin-bottom: 8px;
}
.approval-hint {
  font-size: 12px;
  color: var(--text-secondary, #6b7280);
  margin: 0;
}
.btn {
  flex: 1;
  padding: 12px 8px;
  border-radius: 8px;
  font-size: 14px;
  cursor: pointer;
  border: 1px solid transparent;
  min-height: 44px;
}
.btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.btn-primary {
  background: var(--primary, #2563eb);
  color: var(--text-inverse);
}
.btn-outline {
  background: transparent;
  color: var(--primary, #2563eb);
  border-color: var(--primary, #2563eb);
}
.btn-secondary {
  background: var(--surface, #f3f4f6);
  color: var(--text-primary, #1f2937);
}
</style>
