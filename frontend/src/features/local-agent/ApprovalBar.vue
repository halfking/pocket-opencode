<!--
  ApprovalBar — 工具执行审批条(openhands conversation-confirmation-buttons 移动版)。
  medium/high 风险工具执行前暂停循环,在此放行或拒绝;拒绝会作为工具结果
  回灌模型,由它改道而不是终止任务。
-->
<template>
  <div class="approval-bar" role="alertdialog" aria-label="工具执行确认">
    <div class="row">
      <span class="material-symbols-outlined warn-icon" aria-hidden="true">shield</span>
      <div class="text">
        <p class="title">智能体请求执行 <strong>{{ approval.label }}</strong></p>
        <p class="desc">{{ desc }}</p>
      </div>
    </div>
    <div class="actions">
      <button class="btn ghost" type="button" @click="$emit('respond', false)">拒绝</button>
      <button class="btn primary" type="button" @click="$emit('respond', true)">允许</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { PendingApproval } from '../../localagent/runtime.ts'

const props = defineProps<{ approval: PendingApproval }>()
defineEmits<{ (e: 'respond', allow: boolean): void }>()

const desc = computed(() => {
  const a = props.approval
  if (a.tool === 'write_file') {
    return `写入文件 ${String(a.args?.['path'] ?? '?')}(${String(a.args?.['content'] ?? '').length} 字符)`
  }
  if (a.tool === 'http_fetch') {
    return `GET ${String(a.args?.['url'] ?? '?')}`
  }
  return JSON.stringify(a.args ?? {}).slice(0, 120)
})
</script>

<style scoped>
.approval-bar {
  border: 1px solid rgba(230, 159, 0, 0.5);
  background: rgba(255, 213, 128, 0.12);
  border-radius: var(--radius-md);
  padding: var(--space-3);
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}
.row { display: flex; gap: var(--space-2); align-items: flex-start; }
.warn-icon { color: #b07a00; font-size: 20px; }
.title { margin: 0; font-size: 13px; font-weight: var(--font-weight-semibold); }
.desc {
  margin: 2px 0 0;
  font-size: 12px;
  color: var(--text-secondary);
  word-break: break-all;
}
.actions { display: flex; gap: var(--space-2); justify-content: flex-end; }
.btn {
  border-radius: var(--radius-full);
  padding: 6px 18px;
  font-size: 13px;
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text-primary);
  cursor: pointer;
}
.btn.primary {
  background: var(--brand-primary, #4c8dff);
  border-color: var(--brand-primary, #4c8dff);
  color: #fff;
}
.btn:active { transform: scale(0.97); }
</style>
