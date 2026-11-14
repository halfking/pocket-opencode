<!--
  SearchBar — 全局知识库搜索栏
  
  功能：
  - 实时搜索（防抖 300ms）
  - 高级过滤器（来源、笔记本、标签、日期范围）
  - 快捷键支持（Cmd/Ctrl+K 聚焦）
  - 搜索建议和历史
  - 清空搜索
  
  Props:
  - modelValue: 搜索关键词
  - filters: 搜索过滤器
  - notebooks: 笔记本列表（用于过滤）
  - tags: 标签列表（用于过滤）
  - loading: 搜索中状态
  
  Events:
  - update:modelValue: 更新搜索关键词
  - update:filters: 更新过滤器
  - search: 触发搜索
-->
<template>
  <div class="search-bar" :class="{ focused: isFocused, expanded: filtersExpanded }">
    <div class="search-input-wrapper">
      <span class="search-icon material-symbols-outlined">search</span>
      
      <input
        ref="searchInput"
        type="text"
        class="search-input"
        :placeholder="t('knowledge.searchPlaceholder')"
        :value="modelValue"
        :aria-label="t('knowledge.search')"
        @input="handleInput"
        @focus="isFocused = true"
        @blur="handleBlur"
        @keydown.esc="handleEscape"
        @keydown.enter="handleSearch"
      />

      <div class="search-actions">
        <span v-if="loading" class="loading-spinner">
          <span class="spinner"></span>
        </span>

        <button
          v-if="modelValue"
          class="btn-clear"
          :aria-label="t('knowledge.clearSearch')"
          @click="handleClear"
        >
          <span class="material-symbols-outlined">close</span>
        </button>

        <button
          class="btn-filter"
          :class="{ active: hasActiveFilters }"
          :aria-label="t('knowledge.filters')"
          @click="toggleFilters"
        >
          <span class="material-symbols-outlined">tune</span>
          <span v-if="activeFilterCount > 0" class="filter-badge">
            {{ activeFilterCount }}
          </span>
        </button>

        <div class="keyboard-hint">
          <kbd>{{ isMac ? '⌘' : 'Ctrl' }}</kbd>
          <kbd>K</kbd>
        </div>
      </div>
    </div>

    <!-- 高级过滤器面板 -->
    <transition name="filters-slide">
      <div v-if="filtersExpanded" class="filters-panel">
        <div class="filter-row">
          <label class="filter-label">{{ t('knowledge.source') }}</label>
          <div class="filter-chips">
            <button
              v-for="source in sourceOptions"
              :key="source.value"
              class="filter-chip"
              :class="{ active: localFilters.source === source.value }"
              @click="updateSource(source.value)"
            >
              <span class="material-symbols-outlined">{{ source.icon }}</span>
              <span>{{ source.label }}</span>
            </button>
          </div>
        </div>

        <div class="filter-row">
          <label class="filter-label">{{ t('knowledge.notebook') }}</label>
          <select
            class="filter-select"
            :value="localFilters.notebook"
            @change="updateNotebook($event)"
          >
            <option value="">{{ t('knowledge.allNotebooks') }}</option>
            <option
              v-for="notebook in notebooks"
              :key="notebook.id"
              :value="notebook.id"
            >
              {{ notebook.name }}
            </option>
          </select>
        </div>

        <div class="filter-row">
          <label class="filter-label">{{ t('knowledge.tags') }}</label>
          <div class="tags-input-wrapper">
            <div class="selected-tags">
              <span
                v-for="tag in localFilters.tags"
                :key="tag"
                class="selected-tag"
              >
                #{{ tag }}
                <button
                  class="remove-tag"
                  :aria-label="t('knowledge.removeTag', { tag })"
                  @click="removeTag(tag)"
                >
                  <span class="material-symbols-outlined">close</span>
                </button>
              </span>
            </div>
            <input
              type="text"
              class="tag-input"
              :placeholder="t('knowledge.addTag')"
              @keydown.enter="handleAddTag"
            />
          </div>
        </div>

        <div class="filter-row">
          <label class="filter-label">{{ t('knowledge.domain') }}</label>
          <div class="filter-chips">
            <button
              v-for="domain in domainOptions"
              :key="domain.value"
              class="filter-chip"
              :class="{ active: localFilters.domain === domain.value }"
              @click="updateDomain(domain.value)"
            >
              {{ domain.label }}
            </button>
          </div>
        </div>

        <div class="filter-row">
          <label class="filter-label">{{ t('knowledge.dateRange') }}</label>
          <div class="date-range">
            <input
              type="date"
              class="date-input"
              :value="localFilters.dateRange?.from?.toISOString().split('T')[0]"
              @change="updateDateFrom($event)"
            />
            <span class="date-separator">—</span>
            <input
              type="date"
              class="date-input"
              :value="localFilters.dateRange?.to?.toISOString().split('T')[0]"
              @change="updateDateTo($event)"
            />
          </div>
        </div>

        <div class="filter-actions">
          <button class="btn-reset" @click="handleResetFilters">
            {{ t('knowledge.resetFilters') }}
          </button>
          <button class="btn-apply" @click="handleApplyFilters">
            {{ t('knowledge.applyFilters') }}
          </button>
        </div>
      </div>
    </transition>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Notebook, SearchFilters } from '../../types/knowledge'

interface Props {
  modelValue: string
  filters: SearchFilters
  notebooks?: Notebook[]
  tags?: string[]
  loading?: boolean
}

interface Emits {
  (e: 'update:modelValue', value: string): void
  (e: 'update:filters', filters: SearchFilters): void
  (e: 'search'): void
}

const props = withDefaults(defineProps<Props>(), {
  notebooks: () => [],
  tags: () => [],
  loading: false
})

const emit = defineEmits<Emits>()
const { t } = useI18n()

const searchInput = ref<HTMLInputElement | null>(null)
const isFocused = ref(false)
const filtersExpanded = ref(false)
const localFilters = ref<SearchFilters>({ ...props.filters })

// 检测操作系统
const isMac = computed(() => {
  return typeof navigator !== 'undefined' && /Mac/.test(navigator.platform)
})

// 来源选项
const sourceOptions: Array<{ value: SearchFilters['source']; label: string; icon: string }> = [
  { value: 'all', label: t('knowledge.allSources'), icon: 'inventory_2' },
  { value: 'notes', label: t('knowledge.notes'), icon: 'edit_note' },
  { value: 'memora', label: t('knowledge.memora'), icon: 'psychology' },
  { value: 'emails', label: t('knowledge.emails'), icon: 'mail' },
  { value: 'meetings', label: t('knowledge.meetings'), icon: 'event' },
]

// 领域选项
const domainOptions: Array<{ value: SearchFilters['domain']; label: string }> = [
  { value: undefined, label: t('knowledge.allDomains') },
  { value: 'work', label: t('knowledge.work') },
  { value: 'study', label: t('knowledge.study') },
  { value: 'life', label: t('knowledge.life') },
  { value: 'idea', label: t('knowledge.idea') },
]

// 活跃的过滤器数量
const activeFilterCount = computed(() => {
  let count = 0
  if (localFilters.value.source !== 'all') count++
  if (localFilters.value.notebook) count++
  if (localFilters.value.tags.length > 0) count++
  if (localFilters.value.domain) count++
  if (localFilters.value.dateRange?.from || localFilters.value.dateRange?.to) count++
  return count
})

const hasActiveFilters = computed(() => activeFilterCount.value > 0)

// 防抖处理
let debounceTimer: number | null = null

function handleInput(event: Event) {
  const target = event.target as HTMLInputElement
  const value = target.value
  
  if (debounceTimer) {
    clearTimeout(debounceTimer)
  }
  
  debounceTimer = window.setTimeout(() => {
    emit('update:modelValue', value)
    emit('search')
  }, 300)
}

function handleBlur() {
  // 延迟失焦，避免点击过滤器按钮时立即收起
  setTimeout(() => {
    isFocused.value = false
  }, 200)
}

function handleEscape() {
  searchInput.value?.blur()
  if (filtersExpanded.value) {
    filtersExpanded.value = false
  }
}

function handleClear() {
  emit('update:modelValue', '')
  emit('search')
  searchInput.value?.focus()
}

function handleSearch() {
  emit('search')
}

function toggleFilters() {
  filtersExpanded.value = !filtersExpanded.value
}

// 更新过滤器
function updateSource(source: SearchFilters['source']) {
  localFilters.value.source = source
}

function updateNotebook(event: Event) {
  const target = event.target as HTMLSelectElement
  localFilters.value.notebook = target.value || undefined
}

function updateDomain(domain: SearchFilters['domain']) {
  localFilters.value.domain = domain
}

function updateDateFrom(event: Event) {
  const target = event.target as HTMLInputElement
  if (!localFilters.value.dateRange) {
    localFilters.value.dateRange = {}
  }
  localFilters.value.dateRange.from = target.value ? new Date(target.value) : undefined
}

function updateDateTo(event: Event) {
  const target = event.target as HTMLInputElement
  if (!localFilters.value.dateRange) {
    localFilters.value.dateRange = {}
  }
  localFilters.value.dateRange.to = target.value ? new Date(target.value) : undefined
}

function handleAddTag(event: KeyboardEvent) {
  const target = event.target as HTMLInputElement
  const tag = target.value.trim().replace(/^#/, '')
  
  if (tag && !localFilters.value.tags.includes(tag)) {
    localFilters.value.tags.push(tag)
    target.value = ''
  }
}

function removeTag(tag: string) {
  localFilters.value.tags = localFilters.value.tags.filter(t => t !== tag)
}

function handleResetFilters() {
  localFilters.value = {
    source: 'all',
    tags: [],
  }
  emit('update:filters', localFilters.value)
  emit('search')
}

function handleApplyFilters() {
  emit('update:filters', localFilters.value)
  emit('search')
  filtersExpanded.value = false
}

// 键盘快捷键
function handleKeyboardShortcut(event: KeyboardEvent) {
  if ((event.metaKey || event.ctrlKey) && event.key === 'k') {
    event.preventDefault()
    searchInput.value?.focus()
  }
}

// 同步外部过滤器变化
watch(() => props.filters, (newFilters) => {
  localFilters.value = { ...newFilters }
}, { deep: true })

onMounted(() => {
  document.addEventListener('keydown', handleKeyboardShortcut)
})

onUnmounted(() => {
  document.removeEventListener('keydown', handleKeyboardShortcut)
  if (debounceTimer) {
    clearTimeout(debounceTimer)
  }
})
</script>

<script lang="ts">
export default {
  name: 'SearchBar'
}
</script>

<style scoped>
.search-bar {
  display: flex;
  flex-direction: column;
  background: var(--bg-card);
  border-radius: var(--radius-lg);
  border: 1px solid var(--border);
  transition: all var(--duration-fast) var(--ease-out);
}

.search-bar.focused {
  border-color: var(--brand-primary);
  box-shadow: 0 0 0 3px var(--brand-bg);
}

.search-input-wrapper {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-4);
}

.search-icon {
  color: var(--text-muted);
  font-size: 24px;
  flex-shrink: 0;
}

.search-input {
  flex: 1;
  border: none;
  background: transparent;
  color: var(--text-primary);
  font-size: var(--text-base);
  outline: none;
  min-width: 0;
}

.search-input::placeholder {
  color: var(--text-tertiary);
}

.search-actions {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.loading-spinner {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
}

.spinner {
  width: 16px;
  height: 16px;
  border: 2px solid var(--border);
  border-top-color: var(--brand-primary);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

.btn-clear,
.btn-filter {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border: none;
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
  position: relative;
}

.btn-clear:hover,
.btn-filter:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.btn-filter.active {
  color: var(--brand-primary);
  background: var(--brand-bg);
}

.btn-clear .material-symbols-outlined,
.btn-filter .material-symbols-outlined {
  font-size: 20px;
}

.filter-badge {
  position: absolute;
  top: -2px;
  right: -2px;
  min-width: 16px;
  height: 16px;
  padding: 0 4px;
  border-radius: var(--radius-full);
  background: var(--brand-primary);
  color: white;
  font-size: 10px;
  line-height: 16px;
  text-align: center;
  font-variant-numeric: tabular-nums;
  font-weight: var(--font-weight-semibold);
}

.keyboard-hint {
  display: none;
  align-items: center;
  gap: 2px;
  padding: 2px 6px;
  border-radius: var(--radius-sm);
  background: var(--bg-secondary);
}

@media (min-width: 768px) {
  .keyboard-hint {
    display: flex;
  }
}

.keyboard-hint kbd {
  padding: 2px 4px;
  border-radius: var(--radius-xs);
  background: var(--bg-card);
  border: 1px solid var(--border);
  font-size: 11px;
  font-family: var(--font-mono);
  color: var(--text-muted);
  line-height: 1;
}

/* 过滤器面板 */
.filters-panel {
  padding: var(--space-4);
  border-top: 1px solid var(--border);
  background: var(--bg-secondary);
}

.filters-slide-enter-active,
.filters-slide-leave-active {
  transition: all var(--duration-normal) var(--ease-out);
  overflow: hidden;
}

.filters-slide-enter-from,
.filters-slide-leave-to {
  opacity: 0;
  max-height: 0;
}

.filters-slide-enter-to,
.filters-slide-leave-from {
  opacity: 1;
  max-height: 500px;
}

.filter-row {
  margin-bottom: var(--space-4);
}

.filter-row:last-child {
  margin-bottom: 0;
}

.filter-label {
  display: block;
  font-size: var(--text-sm);
  font-weight: var(--font-weight-medium);
  color: var(--text-primary);
  margin-bottom: var(--space-2);
}

.filter-chips {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.filter-chip {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-full);
  background: var(--bg-card);
  color: var(--text-primary);
  font-size: var(--text-sm);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.filter-chip:hover {
  border-color: var(--brand-primary);
}

.filter-chip.active {
  border-color: var(--brand-primary);
  background: var(--brand-primary);
  color: white;
}

.filter-chip .material-symbols-outlined {
  font-size: 16px;
}

.filter-select {
  width: 100%;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-card);
  color: var(--text-primary);
  font-size: var(--text-sm);
  cursor: pointer;
  outline: none;
  transition: border-color var(--duration-fast) var(--ease-out);
}

.filter-select:focus {
  border-color: var(--brand-primary);
}

.tags-input-wrapper {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  padding: var(--space-2);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-card);
}

.selected-tags {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.selected-tag {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  padding: 2px 8px;
  border-radius: var(--radius-full);
  background: var(--brand-bg);
  color: var(--brand-primary);
  font-size: var(--text-sm);
}

.remove-tag {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  padding: 0;
  border: none;
  border-radius: 50%;
  background: transparent;
  color: var(--brand-primary);
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-out);
}

.remove-tag:hover {
  background: var(--brand-primary);
  color: white;
}

.remove-tag .material-symbols-outlined {
  font-size: 12px;
}

.tag-input {
  flex: 1;
  min-width: 120px;
  border: none;
  background: transparent;
  color: var(--text-primary);
  font-size: var(--text-sm);
  outline: none;
}

.date-range {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.date-input {
  flex: 1;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-card);
  color: var(--text-primary);
  font-size: var(--text-sm);
  outline: none;
  transition: border-color var(--duration-fast) var(--ease-out);
}

.date-input:focus {
  border-color: var(--brand-primary);
}

.date-separator {
  color: var(--text-muted);
}

.filter-actions {
  display: flex;
  gap: var(--space-2);
  margin-top: var(--space-4);
  justify-content: flex-end;
}

.btn-reset,
.btn-apply {
  padding: var(--space-2) var(--space-4);
  border: none;
  border-radius: var(--radius-md);
  font-size: var(--text-sm);
  font-weight: var(--font-weight-medium);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.btn-reset {
  background: var(--bg-card);
  color: var(--text-primary);
  border: 1px solid var(--border);
}

.btn-reset:hover {
  background: var(--bg-hover);
}

.btn-apply {
  background: var(--brand-primary);
  color: white;
}

.btn-apply:hover {
  background: var(--brand-hover);
}
</style>
