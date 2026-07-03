<template>
  <div
    :class="['compact-card', { 'compact-card--expanded': expanded }]"
    @click="handleClick"
  >
    <!-- 紧凑模式 -->
    <div v-if="!expanded" class="compact-view">
      <div class="card-header">
        <span class="card-icon">{{ icon }}</span>
        <span class="card-title">{{ title }}</span>
        <span class="card-time">{{ time }}</span>
        <button
          v-if="actionIcon"
          class="action-btn"
          @click.stop="handleAction"
        >
          {{ actionIcon }}
        </button>
      </div>
      <div class="card-preview">
        {{ preview }}
      </div>
      <div v-if="tags && tags.length > 0" class="card-tags">
        <span
          v-for="tag in tags.slice(0, 2)"
          :key="tag"
          class="tag"
        >
          {{ tag }}
        </span>
      </div>
    </div>

    <!-- 展开模式 -->
    <div v-else class="expanded-view">
      <div class="card-header">
        <span class="card-icon">{{ icon }}</span>
        <span class="card-title">{{ title }}</span>
        <button class="close-btn" @click.stop="handleCollapse">
          ✕
        </button>
      </div>
      
      <div class="card-content">
        <slot />
      </div>

      <div v-if="$slots.actions" class="card-actions">
        <slot name="actions" />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'

export interface CompactCardProps {
  icon: string
  title: string
  time: string
  preview: string
  tags?: string[]
  actionIcon?: string
  expandable?: boolean
}

const props = withDefaults(defineProps<CompactCardProps>(), {
  expandable: true,
})

const emit = defineEmits<{
  (e: 'click'): void
  (e: 'action'): void
  (e: 'expand'): void
  (e: 'collapse'): void
}>()

const expanded = ref(false)

const handleClick = () => {
  if (props.expandable) {
    const willExpand = !expanded.value
    expanded.value = willExpand
    if (willExpand) {
      emit('expand')
    } else {
      emit('collapse')
    }
  }
  emit('click')
}

const handleAction = () => {
  emit('action')
}

const handleCollapse = () => {
  expanded.value = false
  emit('collapse')
}

// 暴露方法供外部调用
defineExpose({
  expand: () => {
    expanded.value = true
  },
  collapse: () => {
    expanded.value = false
  },
  toggle: () => {
    expanded.value = !expanded.value
  },
})
</script>

<style scoped>
.compact-card {
  background: var(--color-bg-surface);
  border-radius: var(--radius-md);
  padding: 10px 12px;
  margin-bottom: 8px;
  cursor: pointer;
  transition: all var(--duration-base) var(--ease-out);
  border: 1px solid transparent;
}

.compact-card:hover {
  border-color: var(--color-border);
  box-shadow: var(--shadow-md);
}

.compact-card:active {
  transform: scale(0.98);
}

.compact-card--expanded {
  padding: 16px;
  box-shadow: var(--shadow-lg);
}

/* 紧凑视图 */
.compact-view {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.card-header {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 24px;
}

.card-icon {
  flex-shrink: 0;
  font-size: 18px;
  line-height: 1;
}

.card-title {
  flex: 1;
  font-size: 14px;
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.card-time {
  flex-shrink: 0;
  font-size: 11px;
  color: var(--color-text-tertiary);
  white-space: nowrap;
}

.action-btn {
  flex-shrink: 0;
  width: 28px;
  height: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: none;
  border: none;
  border-radius: var(--radius-sm);
  color: var(--color-text-secondary);
  font-size: 16px;
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.action-btn:hover {
  background: var(--color-bg-hover);
  color: var(--color-primary);
}

.card-preview {
  font-size: 13px;
  color: var(--color-text-secondary);
  line-height: 1.4;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  padding-left: 26px; /* 对齐 icon */
}

.card-tags {
  display: flex;
  gap: 6px;
  padding-left: 26px;
}

.tag {
  font-size: 11px;
  padding: 2px 8px;
  background: var(--color-bg-base);
  color: var(--color-text-tertiary);
  border-radius: var(--radius-full);
}

/* 展开视图 */
.expanded-view {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.expanded-view .card-header {
  border-bottom: 1px solid var(--color-border);
  padding-bottom: 12px;
}

.expanded-view .card-title {
  font-size: 16px;
}

.close-btn {
  flex-shrink: 0;
  width: 32px;
  height: 32px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: none;
  border: none;
  border-radius: var(--radius-md);
  color: var(--color-text-tertiary);
  font-size: 18px;
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.close-btn:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.card-content {
  font-size: 14px;
  line-height: 1.6;
  color: var(--color-text-primary);
}

.card-actions {
  display: flex;
  gap: 8px;
  padding-top: 8px;
  border-top: 1px solid var(--color-border);
}
</style>
