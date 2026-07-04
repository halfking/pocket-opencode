<!--
  ThemeTabs — M3 SegmentedButton group for switching task themes.

  Phase 4: 把"全局任务列表"按主题（workstream）拆开。
  - tabs 是固定 5 项（与 BottomNav 5 模块对齐）：全部 / AI / 笔记 / 会议 / 邮件
  - 每项带未完成任务数 badge（仅 active + blocked 计入）
  - 选中态用 M3 `secondary-container` 背景 + `on-secondary-container` 文字
  - 视觉态切换通过 state-layer（hover/press 时半透明 overlay）
-->
<template>
  <div class="theme-tabs" role="tablist">
    <button
      v-for="tab in tabs"
      :key="tab.id"
      :class="['theme-tab', { active: tab.id === modelValue, 'has-badge': tab.count > 0 }]"
      :aria-selected="tab.id === modelValue"
      role="tab"
      @click="$emit('update:modelValue', tab.id)"
    >
      <span class="theme-tab-icon" v-if="tab.icon">{{ tab.icon }}</span>
      <span class="theme-tab-label">{{ tab.label }}</span>
      <span v-if="tab.count > 0" class="theme-tab-badge">{{ tab.count }}</span>
    </button>
  </div>
</template>

<script setup lang="ts">
export interface ThemeTab {
  id: string
  label: string
  icon?: string
  count: number
}

defineProps<{
  modelValue: string
  tabs: ThemeTab[]
}>()

defineEmits<{
  (e: 'update:modelValue', id: string): void
}>()
</script>

<style scoped>
.theme-tabs {
  display: flex;
  gap: 6px;
  padding: 8px 12px;
  overflow-x: auto;
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
  scrollbar-width: none;
}
.theme-tabs::-webkit-scrollbar {
  display: none;
}

.theme-tab {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  height: 32px;
  padding: 0 14px;
  border-radius: 999px;
  border: 1px solid var(--border);
  background: transparent;
  color: var(--text-secondary);
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  white-space: nowrap;
  position: relative;
  transition:
    background 180ms ease,
    color 180ms ease,
    border-color 180ms ease;
}

.theme-tab:active::before {
  /* M3 state-layer: 半透明遮罩模拟按下 */
  content: '';
  position: absolute;
  inset: 0;
  border-radius: inherit;
  background: currentColor;
  opacity: 0.08;
  pointer-events: none;
}

.theme-tab.active {
  background: var(--brand-primary);
  color: #fff;
  border-color: var(--brand-primary);
  font-weight: 600;
}

.theme-tab.has-badge .theme-tab-badge {
  background: var(--warning);
  color: #fff;
  border-radius: 999px;
  padding: 1px 7px;
  font-size: 11px;
  line-height: 14px;
  min-width: 18px;
  text-align: center;
  font-weight: 700;
}

.theme-tab.active .theme-tab-badge {
  background: rgba(255, 255, 255, 0.25);
  color: #fff;
}

.theme-tab-icon {
  font-size: 14px;
  line-height: 1;
}
</style>