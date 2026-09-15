<!--
  PlanCard — 任务计划卡(openhands TaskTracker task-list-section 移动版)。
  task_plan 工具 set/update 时更新;todo 圆圈 / in_progress 半圈 / done 勾。
-->
<template>
  <div class="plan-card">
    <p class="head"><span class="material-symbols-outlined" aria-hidden="true">checklist</span>任务计划</p>
    <ul class="items">
      <li v-for="(it, i) in item.items ?? []" :key="i" :class="`s-${it.status}`">
        <span class="mark" aria-hidden="true">
          <span v-if="it.status === 'done'" class="material-symbols-outlined">check_circle</span>
          <span v-else-if="it.status === 'in_progress'" class="material-symbols-outlined">pending</span>
          <span v-else class="material-symbols-outlined">radio_button_unchecked</span>
        </span>
        <span class="title">{{ it.title }}</span>
      </li>
    </ul>
  </div>
</template>

<script setup lang="ts">
import type { TimelineItem } from '../../localagent/runtime.ts'

defineProps<{ item: TimelineItem }>()
</script>

<style scoped>
.plan-card {
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-subtle);
  padding: var(--space-3);
}
.head {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 0 0 var(--space-2);
  font-size: 13px;
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
}
.head .material-symbols-outlined { font-size: 18px; color: var(--brand-primary, #4c8dff); }
.items { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 6px; }
.items li { display: flex; align-items: flex-start; gap: 8px; font-size: 13px; color: var(--text-primary); }
.items li.s-done .title { color: var(--text-muted); text-decoration: line-through; }
.mark .material-symbols-outlined { font-size: 18px; vertical-align: -4px; color: var(--text-muted); }
li.s-in_progress .mark .material-symbols-outlined { color: var(--brand-primary, #4c8dff); }
li.s-done .mark .material-symbols-outlined { color: var(--success, #2e7d32); }
</style>
