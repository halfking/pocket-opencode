<!--
  TagCloud — 标签云组件
  
  功能：
  - 展示所有标签
  - 按使用频率显示不同大小
  - 点击标签过滤笔记
  - 支持多选
  - 颜色编码（可选）
  
  Props:
  - tags: 标签数据（包含名称和使用次数）
  - selectedTags: 当前选中的标签
  - maxSize: 最大字体大小
  - minSize: 最小字体大小
  
  Events:
  - select: 选中/取消选中标签
-->
<template>
  <div class="tag-cloud">
    <div class="cloud-header">
      <h3 class="cloud-title">{{ t('knowledge.tags') }}</h3>
      <button
        v-if="selectedTags.length > 0"
        class="btn-clear-selection"
        :aria-label="t('knowledge.clearSelection')"
        @click="handleClearAll"
      >
        <span class="material-symbols-outlined">close</span>
        {{ t('knowledge.clearAll') }}
      </button>
    </div>

    <div class="cloud-content" ref="cloudContent">
      <template v-if="loading">
        <div class="cloud-skeleton">
          <div class="skeleton-tag" v-for="i in 20" :key="i"></div>
        </div>
      </template>

      <template v-else-if="sortedTags.length === 0">
        <div class="cloud-empty">
          <span class="material-symbols-outlined empty-icon">label_off</span>
          <p>{{ t('knowledge.noTags') }}</p>
        </div>
      </template>

      <template v-else>
        <div class="cloud-tags">
          <button
            v-for="tag in sortedTags"
            :key="tag.name"
            class="tag-item"
            :class="{
              selected: isSelected(tag.name),
              'size-xs': getTagSize(tag) === 'xs',
              'size-sm': getTagSize(tag) === 'sm',
              'size-md': getTagSize(tag) === 'md',
              'size-lg': getTagSize(tag) === 'lg',
              'size-xl': getTagSize(tag) === 'xl',
            }"
            :style="{ color: tag.color || getTagColor(tag.name) }"
            :aria-label="t('knowledge.tagWithCount', { tag: tag.name, count: tag.count })"
            :aria-pressed="isSelected(tag.name)"
            @click="handleSelect(tag.name)"
          >
            <span class="tag-name">#{{ tag.name }}</span>
            <span class="tag-count">{{ tag.count }}</span>
          </button>
        </div>
      </template>
    </div>

    <div v-if="sortedTags.length > displayLimit" class="cloud-footer">
      <button class="btn-show-more" @click="toggleShowAll">
        {{ showAll ? t('knowledge.showLess') : t('knowledge.showMore') }}
        <span class="material-symbols-outlined">
          {{ showAll ? 'expand_less' : 'expand_more' }}
        </span>
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Tag } from '../../types/knowledge'

interface Props {
  tags: Tag[]
  selectedTags?: string[]
  loading?: boolean
  maxSize?: number
  minSize?: number
  displayLimit?: number
}

interface Emits {
  (e: 'select', tags: string[]): void
}

const props = withDefaults(defineProps<Props>(), {
  selectedTags: () => [],
  loading: false,
  maxSize: 28,
  minSize: 12,
  displayLimit: 30
})

const emit = defineEmits<Emits>()
const { t } = useI18n()

const showAll = ref(false)
const cloudContent = ref<HTMLElement | null>(null)

// 按使用频率排序标签
const sortedTags = computed(() => {
  const sorted = [...props.tags].sort((a, b) => b.count - a.count)
  return showAll.value ? sorted : sorted.slice(0, props.displayLimit)
})

// 获取标签大小
const maxCount = computed(() => {
  return Math.max(...props.tags.map(t => t.count), 1)
})

const minCount = computed(() => {
  return Math.min(...props.tags.map(t => t.count), 1)
})

type TagSize = 'xs' | 'sm' | 'md' | 'lg' | 'xl'

function getTagSize(tag: Tag): TagSize {
  if (props.tags.length === 1) return 'md'
  
  const range = maxCount.value - minCount.value
  if (range === 0) return 'md'
  
  const normalized = (tag.count - minCount.value) / range
  
  if (normalized < 0.2) return 'xs'
  if (normalized < 0.4) return 'sm'
  if (normalized < 0.6) return 'md'
  if (normalized < 0.8) return 'lg'
  return 'xl'
}

// 为标签生成一致的颜色
function getTagColor(tagName: string): string {
  const colors = [
    '#3b82f6', // blue
    '#10b981', // green
    '#f59e0b', // amber
    '#ef4444', // red
    '#8b5cf6', // violet
    '#ec4899', // pink
    '#06b6d4', // cyan
    '#f97316', // orange
  ]
  
  // 使用标签名生成一致的哈希值
  let hash = 0
  for (let i = 0; i < tagName.length; i++) {
    hash = tagName.charCodeAt(i) + ((hash << 5) - hash)
  }
  
  return colors[Math.abs(hash) % colors.length]
}

function isSelected(tagName: string): boolean {
  return props.selectedTags.includes(tagName)
}

function handleSelect(tagName: string) {
  const currentSelection = [...props.selectedTags]
  
  if (isSelected(tagName)) {
    // 取消选中
    const index = currentSelection.indexOf(tagName)
    currentSelection.splice(index, 1)
  } else {
    // 选中
    currentSelection.push(tagName)
  }
  
  emit('select', currentSelection)
}

function handleClearAll() {
  emit('select', [])
}

function toggleShowAll() {
  showAll.value = !showAll.value
}
</script>

<script lang="ts">
export default {
  name: 'TagCloud'
}
</script>

<style scoped>
.tag-cloud {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--bg-card);
  border-radius: var(--radius-lg);
  overflow: hidden;
}

.cloud-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-4);
  border-bottom: 1px solid var(--border);
  background: var(--bg-card);
  position: sticky;
  top: 0;
  z-index: var(--z-base);
}

.cloud-title {
  font-size: var(--text-base);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  margin: 0;
}

.btn-clear-selection {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-1) var(--space-2);
  border: none;
  border-radius: var(--radius-md);
  background: var(--bg-hover);
  color: var(--text-primary);
  font-size: var(--text-xs);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.btn-clear-selection:hover {
  background: var(--bg-secondary);
}

.btn-clear-selection .material-symbols-outlined {
  font-size: var(--text-base);
}

.cloud-content {
  flex: 1;
  overflow-y: auto;
  padding: var(--space-4);
}

/* 骨架屏 */
.cloud-skeleton {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.skeleton-tag {
  height: 28px;
  width: calc(20% + (80% * var(--random)));
  background: linear-gradient(
    90deg,
    var(--bg-secondary) 25%,
    var(--bg-hover) 50%,
    var(--bg-secondary) 75%
  );
  background-size: 200% 100%;
  animation: skeleton-loading 1.5s ease-in-out infinite;
  border-radius: var(--radius-full);
}

.skeleton-tag:nth-child(1) { --random: 0.8; }
.skeleton-tag:nth-child(2) { --random: 0.3; }
.skeleton-tag:nth-child(3) { --random: 0.6; }
.skeleton-tag:nth-child(4) { --random: 0.9; }
.skeleton-tag:nth-child(5) { --random: 0.4; }
.skeleton-tag:nth-child(6) { --random: 0.7; }
.skeleton-tag:nth-child(7) { --random: 0.5; }
.skeleton-tag:nth-child(8) { --random: 0.2; }

@keyframes skeleton-loading {
  0% {
    background-position: 200% 0;
  }
  100% {
    background-position: -200% 0;
  }
}

/* 空状态 */
.cloud-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: var(--space-8) var(--space-4);
  text-align: center;
  color: var(--text-muted);
}

.empty-icon {
  font-size: 48px;
  color: var(--text-tertiary);
  margin-bottom: var(--space-3);
}

.cloud-empty p {
  margin: 0;
  font-size: var(--text-sm);
}

/* 标签云 */
.cloud-tags {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  align-items: center;
  justify-content: center;
}

.tag-item {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-1) var(--space-3);
  border: 1px solid currentColor;
  border-radius: var(--radius-full);
  background: transparent;
  font-weight: var(--font-weight-medium);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
  opacity: 0.7;
  white-space: nowrap;
}

.tag-item:hover {
  opacity: 1;
  transform: translateY(-2px);
  box-shadow: 0 2px 8px currentColor;
}

.tag-item.selected {
  opacity: 1;
  background: currentColor;
}

.tag-item.selected .tag-name,
.tag-item.selected .tag-count {
  color: white;
  filter: none;
}

/* 标签大小变化 */
.tag-item.size-xs {
  font-size: var(--text-2xs);
  padding: 2px 8px;
}

.tag-item.size-sm {
  font-size: var(--text-sm);
  padding: 3px 10px;
}

.tag-item.size-md {
  font-size: var(--text-base);
  padding: 4px 12px;
}

.tag-item.size-lg {
  font-size: var(--text-lg);
  padding: 5px 14px;
}

.tag-item.size-xl {
  font-size: var(--text-xl);
  padding: 6px 16px;
}

.tag-name {
  color: currentColor;
}

.tag-count {
  color: currentColor;
  opacity: 0.6;
  font-size: 0.85em;
  font-variant-numeric: tabular-nums;
}

.cloud-footer {
  padding: var(--space-3) var(--space-4);
  border-top: 1px solid var(--border);
  background: var(--bg-card);
  text-align: center;
}

.btn-show-more {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-2) var(--space-4);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-card);
  color: var(--text-primary);
  font-size: var(--text-sm);
  font-weight: var(--font-weight-medium);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.btn-show-more:hover {
  background: var(--bg-hover);
  border-color: var(--brand-primary);
  color: var(--brand-primary);
}

.btn-show-more .material-symbols-outlined {
  font-size: var(--text-xl);
}

/* 响应式 */
@media (max-width: 768px) {
  .cloud-tags {
    justify-content: flex-start;
  }
  
  .tag-item.size-xl {
    font-size: var(--text-lg);
  }
  
  .tag-item.size-lg {
    font-size: var(--text-base);
  }
}
</style>
