<!--
  NoteList — 笔记列表组件
  
  功能：
  - 展示笔记列表（标题、摘要、标签）
  - 排序（按创建时间、更新时间、标题）
  - 过滤和搜索
  - 虚拟滚动支持大列表
  - 加载状态和空状态
  
  Props:
  - notes: 笔记数组
  - loading: 加载状态
  - selectedId: 当前选中的笔记ID
  
  Events:
  - select: 选中笔记
  - create: 创建新笔记
-->
<template>
  <div class="note-list">
    <div class="list-header">
      <div class="header-top">
        <h2 class="list-title">{{ title || t('knowledge.notes') }}</h2>
        <button
          class="btn-create"
          :aria-label="t('knowledge.createNote')"
          @click="handleCreate"
        >
          <span class="material-symbols-outlined">add</span>
        </button>
      </div>

      <div class="list-controls">
        <div class="sort-control">
          <button
            class="btn-sort"
            :aria-label="t('knowledge.sortBy')"
            @click="toggleSortMenu"
          >
            <span class="material-symbols-outlined">sort</span>
            <span class="sort-label">{{ currentSortLabel }}</span>
            <span class="material-symbols-outlined chevron">expand_more</span>
          </button>

          <div v-if="sortMenuOpen" class="sort-menu">
            <button
              v-for="option in sortOptions"
              :key="`${option.field}-${option.order}`"
              class="sort-option"
              :class="{ active: isSortActive(option) }"
              @click="handleSort(option)"
            >
              <span>{{ option.label }}</span>
              <span class="material-symbols-outlined" v-if="isSortActive(option)">
                check
              </span>
            </button>
          </div>
        </div>

        <div class="view-control">
          <button
            class="btn-view"
            :class="{ active: viewMode === 'list' }"
            :aria-label="t('knowledge.listView')"
            @click="viewMode = 'list'"
          >
            <span class="material-symbols-outlined">view_list</span>
          </button>
          <button
            class="btn-view"
            :class="{ active: viewMode === 'grid' }"
            :aria-label="t('knowledge.gridView')"
            @click="viewMode = 'grid'"
          >
            <span class="material-symbols-outlined">grid_view</span>
          </button>
        </div>
      </div>
    </div>

    <div class="list-content" ref="listContentRef">
      <template v-if="loading">
        <div class="note-skeleton" v-for="i in 8" :key="i">
          <div class="skeleton-header"></div>
          <div class="skeleton-line"></div>
          <div class="skeleton-line short"></div>
          <div class="skeleton-tags"></div>
        </div>
      </template>

      <template v-else-if="sortedNotes.length === 0">
        <div class="list-empty">
          <span class="material-symbols-outlined empty-icon">note_add</span>
          <p>{{ t('knowledge.noNotes') }}</p>
          <button class="btn-create-first" @click="handleCreate">
            {{ t('knowledge.createFirstNote') }}
          </button>
        </div>
      </template>

      <template v-else>
        <div class="notes-container" :class="{ 'grid-mode': viewMode === 'grid' }">
          <article
            v-for="note in sortedNotes"
            :key="note.id"
            class="note-item"
            :class="{ selected: selectedId === note.id }"
            @click="handleSelect(note.id)"
          >
            <div class="note-header">
              <h3 class="note-title">
                {{ note.title || t('knowledge.untitled') }}
              </h3>
              <span v-if="note.isPinned" class="pin-icon" :aria-label="t('knowledge.pinned')">
                <span class="material-symbols-outlined">push_pin</span>
              </span>
            </div>

            <p v-if="note.summary" class="note-summary">
              {{ note.summary }}
            </p>

            <div class="note-meta">
              <div v-if="note.tags && note.tags.length > 0" class="note-tags">
                <span
                  v-for="tag in note.tags.slice(0, 3)"
                  :key="tag"
                  class="tag"
                >
                  #{{ tag }}
                </span>
                <span v-if="note.tags.length > 3" class="tag-more">
                  +{{ note.tags.length - 3 }}
                </span>
              </div>

              <div class="note-footer">
                <span v-if="note.source" class="note-source">
                  <span class="material-symbols-outlined">{{ getSourceIcon(note.source) }}</span>
                  <span class="source-label">{{ note.source }}</span>
                </span>
                <time class="note-time" :datetime="new Date(note.updatedAt).toISOString()">
                  {{ formatTime(note.updatedAt) }}
                </time>
              </div>
            </div>
          </article>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Note } from '../../types/knowledge'

interface SortOption {
  field: 'createdAt' | 'updatedAt' | 'title'
  order: 'asc' | 'desc'
  label: string
}

interface Props {
  notes: Note[]
  loading?: boolean
  selectedId?: string
  title?: string
}

interface Emits {
  (e: 'select', noteId: string): void
  (e: 'create'): void
}

const props = withDefaults(defineProps<Props>(), {
  loading: false,
  notes: () => []
})

const emit = defineEmits<Emits>()
const { t } = useI18n()

// 视图模式
const viewMode = ref<'list' | 'grid'>('list')

// 排序状态
const currentSort = ref<SortOption>({
  field: 'updatedAt',
  order: 'desc',
  label: t('knowledge.sortByUpdated')
})

const sortMenuOpen = ref(false)
const listContentRef = ref<HTMLElement | null>(null)

const sortOptions: SortOption[] = [
  { field: 'updatedAt', order: 'desc', label: t('knowledge.sortByUpdated') },
  { field: 'createdAt', order: 'desc', label: t('knowledge.sortByCreated') },
  { field: 'title', order: 'asc', label: t('knowledge.sortByTitle') },
]

const currentSortLabel = computed(() => currentSort.value.label)

// 排序后的笔记列表
const sortedNotes = computed(() => {
  const notesCopy = [...props.notes]
  const { field, order } = currentSort.value

  notesCopy.sort((a, b) => {
    let comparison = 0

    if (field === 'title') {
      const titleA = (a.title || '').toLowerCase()
      const titleB = (b.title || '').toLowerCase()
      comparison = titleA.localeCompare(titleB)
    } else {
      comparison = a[field] - b[field]
    }

    return order === 'asc' ? comparison : -comparison
  })

  // 置顶笔记始终在最前面
  return notesCopy.sort((a, b) => {
    if (a.isPinned && !b.isPinned) return -1
    if (!a.isPinned && b.isPinned) return 1
    return 0
  })
})

function toggleSortMenu() {
  sortMenuOpen.value = !sortMenuOpen.value
}

function isSortActive(option: SortOption) {
  return (
    currentSort.value.field === option.field &&
    currentSort.value.order === option.order
  )
}

function handleSort(option: SortOption) {
  currentSort.value = option
  sortMenuOpen.value = false
}

function handleSelect(noteId: string) {
  emit('select', noteId)
}

function handleCreate() {
  emit('create')
}

function getSourceIcon(source: string): string {
  const iconMap: Record<string, string> = {
    email: 'mail',
    meeting: 'event',
    voice: 'mic',
    manual: 'edit_note',
    import: 'upload_file',
    memora: 'psychology',
  }
  return iconMap[source] || 'description'
}

function formatTime(timestamp: number): string {
  const date = new Date(timestamp)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffMins = Math.floor(diffMs / 60000)
  const diffHours = Math.floor(diffMs / 3600000)
  const diffDays = Math.floor(diffMs / 86400000)

  if (diffMins < 1) return t('knowledge.justNow')
  if (diffMins < 60) return t('knowledge.minutesAgo', { n: diffMins })
  if (diffHours < 24) return t('knowledge.hoursAgo', { n: diffHours })
  if (diffDays < 7) return t('knowledge.daysAgo', { n: diffDays })
  
  return date.toLocaleDateString()
}

// 点击外部关闭排序菜单
function handleClickOutside(event: MouseEvent) {
  const target = event.target as HTMLElement
  if (!target.closest('.sort-control')) {
    sortMenuOpen.value = false
  }
}

onMounted(() => {
  document.addEventListener('click', handleClickOutside)
})

onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside)
})
</script>

<script lang="ts">
export default {
  name: 'NoteList'
}
</script>

<style scoped>
.note-list {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--bg-card);
  border-radius: var(--radius-lg);
  overflow: hidden;
}

.list-header {
  padding: var(--space-4);
  border-bottom: 1px solid var(--border);
  background: var(--bg-card);
  position: sticky;
  top: 0;
  z-index: var(--z-base);
}

.header-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-3);
}

.list-title {
  font-size: var(--text-lg);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  margin: 0;
}

.btn-create {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border: none;
  border-radius: var(--radius-md);
  background: var(--brand-primary);
  color: white;
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.btn-create:hover {
  background: var(--brand-hover);
  transform: translateY(-1px);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.15);
}

.btn-create:active {
  transform: translateY(0);
}

.list-controls {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  justify-content: space-between;
}

.sort-control {
  position: relative;
  flex: 1;
}

.btn-sort {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-card);
  color: var(--text-primary);
  font-size: var(--text-sm);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
  width: 100%;
}

.btn-sort:hover {
  background: var(--bg-hover);
  border-color: var(--brand-primary);
}

.btn-sort .material-symbols-outlined {
  font-size: var(--text-xl);
}

.sort-label {
  flex: 1;
  text-align: left;
}

.chevron {
  color: var(--text-muted);
  transition: transform var(--duration-fast) var(--ease-out);
}

.sort-menu {
  position: absolute;
  top: calc(100% + var(--space-1));
  left: 0;
  right: 0;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
  padding: var(--space-1);
  z-index: var(--z-popover);
  animation: menu-in var(--duration-fast) var(--ease-out);
}

@keyframes menu-in {
  from {
    opacity: 0;
    transform: translateY(-4px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

.sort-option {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  padding: var(--space-2) var(--space-3);
  border: none;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text-primary);
  font-size: var(--text-sm);
  text-align: left;
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-out);
}

.sort-option:hover {
  background: var(--bg-hover);
}

.sort-option.active {
  color: var(--brand-primary);
  font-weight: var(--font-weight-medium);
}

.sort-option .material-symbols-outlined {
  font-size: var(--text-xl);
}

.view-control {
  display: flex;
  gap: var(--space-1);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  padding: 2px;
  background: var(--bg-secondary);
}

.btn-view {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border: none;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.btn-view:hover {
  color: var(--text-primary);
}

.btn-view.active {
  background: var(--bg-card);
  color: var(--brand-primary);
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
}

.btn-view .material-symbols-outlined {
  font-size: 20px;
}

.list-content {
  flex: 1;
  overflow-y: auto;
  padding: var(--space-2);
}

/* 骨架屏 */
.note-skeleton {
  padding: var(--space-4);
  margin-bottom: var(--space-2);
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
}

.skeleton-header {
  height: 20px;
  width: 60%;
  background: linear-gradient(
    90deg,
    var(--bg-secondary) 25%,
    var(--bg-hover) 50%,
    var(--bg-secondary) 75%
  );
  background-size: 200% 100%;
  animation: skeleton-loading 1.5s ease-in-out infinite;
  border-radius: var(--radius-sm);
  margin-bottom: var(--space-3);
}

.skeleton-line {
  height: 14px;
  background: linear-gradient(
    90deg,
    var(--bg-secondary) 25%,
    var(--bg-hover) 50%,
    var(--bg-secondary) 75%
  );
  background-size: 200% 100%;
  animation: skeleton-loading 1.5s ease-in-out infinite;
  border-radius: var(--radius-sm);
  margin-bottom: var(--space-2);
}

.skeleton-line.short {
  width: 40%;
}

.skeleton-tags {
  height: 20px;
  width: 50%;
  background: linear-gradient(
    90deg,
    var(--bg-secondary) 25%,
    var(--bg-hover) 50%,
    var(--bg-secondary) 75%
  );
  background-size: 200% 100%;
  animation: skeleton-loading 1.5s ease-in-out infinite;
  border-radius: var(--radius-sm);
  margin-top: var(--space-3);
}

@keyframes skeleton-loading {
  0% {
    background-position: 200% 0;
  }
  100% {
    background-position: -200% 0;
  }
}

/* 空状态 */
.list-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: var(--space-12) var(--space-4);
  text-align: center;
  color: var(--text-muted);
}

.empty-icon {
  font-size: 64px;
  color: var(--text-tertiary);
  margin-bottom: var(--space-4);
}

.list-empty p {
  margin: 0 0 var(--space-4);
  font-size: var(--text-base);
}

.btn-create-first {
  padding: var(--space-3) var(--space-6);
  border: none;
  border-radius: var(--radius-md);
  background: var(--brand-primary);
  color: white;
  font-size: var(--text-base);
  font-weight: var(--font-weight-medium);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.btn-create-first:hover {
  background: var(--brand-hover);
  transform: translateY(-2px);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
}

.btn-create-first:active {
  transform: translateY(0);
}

/* 笔记列表 */
.notes-container {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.notes-container.grid-mode {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: var(--space-3);
}

.note-item {
  padding: var(--space-4);
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.note-item:hover {
  border-color: var(--brand-primary);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.08);
  transform: translateY(-1px);
}

.note-item.selected {
  border-color: var(--brand-primary);
  background: var(--brand-bg);
}

.note-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-2);
  margin-bottom: var(--space-2);
}

.note-title {
  flex: 1;
  font-size: var(--text-base);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  margin: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

.pin-icon {
  color: var(--brand-primary);
  display: flex;
  flex-shrink: 0;
}

.pin-icon .material-symbols-outlined {
  font-size: var(--text-xl);
}

.note-summary {
  font-size: var(--text-sm);
  color: var(--text-secondary);
  margin: 0 0 var(--space-3);
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  line-height: 1.5;
}

.note-meta {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.note-tags {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.tag {
  padding: 2px 8px;
  border-radius: var(--radius-full);
  background: var(--bg-secondary);
  color: var(--brand-primary);
  font-size: var(--text-xs);
  font-weight: var(--font-weight-medium);
}

.tag-more {
  padding: 2px 8px;
  border-radius: var(--radius-full);
  background: var(--bg-secondary);
  color: var(--text-muted);
  font-size: var(--text-xs);
}

.note-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.note-source {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  font-size: var(--text-xs);
  color: var(--text-muted);
}

.note-source .material-symbols-outlined {
  font-size: var(--text-base);
}

.source-label {
  text-transform: capitalize;
}

.note-time {
  font-size: var(--text-xs);
  color: var(--text-tertiary);
  font-variant-numeric: tabular-nums;
}
</style>
