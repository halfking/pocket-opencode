<!--
  NotebookTree — 笔记本树形导航组件
  
  功能：
  - 展示嵌套的笔记本层级结构
  - 支持展开/折叠子节点
  - 右键菜单操作（创建、重命名、删除）
  - 图标和颜色自定义
  - 拖拽排序（未来扩展）
  
  Props:
  - notebooks: 笔记本树形数据
  - selectedId: 当前选中的笔记本ID
  
  Events:
  - select: 选中笔记本
  - create: 创建新笔记本
  - update: 更新笔记本
  - delete: 删除笔记本
  
  Accessibility:
  - 树形结构使用 role="tree" 和 role="treeitem"
  - 支持键盘导航（上下键、左右键展开/折叠、Enter 选择）
  - aria-expanded 标记展开状态
-->
<template>
  <nav class="notebook-tree" role="tree" :aria-label="t('knowledge.notebookTree')">
    <div class="tree-header">
      <h2 class="tree-title">{{ t('knowledge.notebooks') }}</h2>
      <button
        class="btn-add"
        :aria-label="t('knowledge.createNotebook')"
        @click="handleCreate(null)"
      >
        <span class="material-symbols-outlined">add</span>
      </button>
    </div>

    <div class="tree-content">
      <template v-if="loading">
        <div class="tree-skeleton" v-for="i in 5" :key="i">
          <div class="skeleton-line"></div>
        </div>
      </template>

      <template v-else-if="notebooks.length === 0">
        <div class="tree-empty">
          <span class="material-symbols-outlined empty-icon">folder_off</span>
          <p>{{ t('knowledge.noNotebooks') }}</p>
          <button class="btn-create-first" @click="handleCreate(null)">
            {{ t('knowledge.createFirstNotebook') }}
          </button>
        </div>
      </template>

      <template v-else>
        <TreeNode
          v-for="notebook in notebooks"
          :key="notebook.id"
          :notebook="notebook"
          :selected-id="selectedId"
          :expanded-ids="expandedIds"
          :level="0"
          @select="handleSelect"
          @toggle="handleToggle"
          @create="handleCreate"
          @update="handleUpdate"
          @delete="handleDelete"
          @contextmenu="handleContextMenu"
        />
      </template>
    </div>

    <!-- 右键菜单 -->
    <div
      v-if="contextMenu.visible"
      class="context-menu"
      :style="{ top: `${contextMenu.y}px`, left: `${contextMenu.x}px` }"
      @click.stop
    >
      <button class="menu-item" @click="handleCreate(contextMenu.notebookId)">
        <span class="material-symbols-outlined">add</span>
        {{ t('knowledge.createSubNotebook') }}
      </button>
      <button class="menu-item" @click="handleRename(contextMenu.notebookId)">
        <span class="material-symbols-outlined">edit</span>
        {{ t('knowledge.rename') }}
      </button>
      <div class="menu-divider"></div>
      <button class="menu-item danger" @click="handleDelete(contextMenu.notebookId)">
        <span class="material-symbols-outlined">delete</span>
        {{ t('knowledge.delete') }}
      </button>
    </div>
  </nav>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Notebook } from '../../types/knowledge'
import TreeNode from './NotebookTreeNode.vue'

interface Props {
  notebooks: Notebook[]
  selectedId?: string
  loading?: boolean
}

interface Emits {
  (e: 'select', notebookId: string): void
  (e: 'create', parentId: string | null): void
  (e: 'update', notebookId: string, data: Partial<Notebook>): void
  (e: 'delete', notebookId: string): void
}

const props = withDefaults(defineProps<Props>(), {
  loading: false
})

const emit = defineEmits<Emits>()
const { t } = useI18n()

// 展开状态管理
const expandedIds = ref<Set<string>>(new Set())

// 右键菜单状态
const contextMenu = ref({
  visible: false,
  x: 0,
  y: 0,
  notebookId: null as string | null
})

// 处理选择
function handleSelect(notebookId: string) {
  emit('select', notebookId)
}

// 处理展开/折叠
function handleToggle(notebookId: string) {
  if (expandedIds.value.has(notebookId)) {
    expandedIds.value.delete(notebookId)
  } else {
    expandedIds.value.add(notebookId)
  }
}

// 处理创建
function handleCreate(parentId: string | null) {
  contextMenu.value.visible = false
  emit('create', parentId)
}

// 处理更新
function handleUpdate(notebookId: string, data: Partial<Notebook>) {
  emit('update', notebookId, data)
}

// 处理重命名
function handleRename(notebookId: string | null) {
  if (!notebookId) return
  contextMenu.value.visible = false
  // 这里应该打开重命名对话框，简化处理直接触发 update
  const name = prompt(t('knowledge.enterNewName'))
  if (name && name.trim()) {
    emit('update', notebookId, { name: name.trim() })
  }
}

// 处理删除
function handleDelete(notebookId: string | null) {
  if (!notebookId) return
  contextMenu.value.visible = false
  
  if (confirm(t('knowledge.confirmDeleteNotebook'))) {
    emit('delete', notebookId)
  }
}

// 处理右键菜单
function handleContextMenu(event: MouseEvent, notebookId: string) {
  event.preventDefault()
  contextMenu.value = {
    visible: true,
    x: event.clientX,
    y: event.clientY,
    notebookId
  }
}

// 点击外部关闭右键菜单
function handleClickOutside() {
  contextMenu.value.visible = false
}

onMounted(() => {
  document.addEventListener('click', handleClickOutside)
  document.addEventListener('contextmenu', handleClickOutside)
})

onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside)
  document.removeEventListener('contextmenu', handleClickOutside)
})

// 初始展开所有有子节点的笔记本
onMounted(() => {
  const expandAll = (notebooks: Notebook[]) => {
    notebooks.forEach(nb => {
      if (nb.children && nb.children.length > 0) {
        expandedIds.value.add(nb.id)
        expandAll(nb.children)
      }
    })
  }
  expandAll(props.notebooks)
})
</script>

<script lang="ts">
export default {
  name: 'NotebookTree'
}
</script>

<style scoped>
.notebook-tree {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--bg-card);
  border-radius: var(--radius-lg);
  overflow: hidden;
}

.tree-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-3) var(--space-4);
  border-bottom: 1px solid var(--border);
  background: var(--bg-card);
  position: sticky;
  top: 0;
  z-index: 10;
}

.tree-title {
  font-size: var(--text-base);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  margin: 0;
}

.btn-add {
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
}

.btn-add:hover {
  background: var(--bg-hover);
  color: var(--brand-primary);
}

.btn-add:active {
  transform: scale(0.95);
}

.tree-content {
  flex: 1;
  overflow-y: auto;
  padding: var(--space-2);
}

/* 骨架屏 */
.tree-skeleton {
  padding: var(--space-2);
  margin-bottom: var(--space-1);
}

.skeleton-line {
  height: 36px;
  background: linear-gradient(
    90deg,
    var(--bg-secondary) 25%,
    var(--bg-hover) 50%,
    var(--bg-secondary) 75%
  );
  background-size: 200% 100%;
  animation: skeleton-loading 1.5s ease-in-out infinite;
  border-radius: var(--radius-md);
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
.tree-empty {
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

.tree-empty p {
  margin: 0 0 var(--space-4);
  font-size: var(--text-sm);
}

.btn-create-first {
  padding: var(--space-2) var(--space-4);
  border: none;
  border-radius: var(--radius-md);
  background: var(--brand-primary);
  color: white;
  font-size: var(--text-sm);
  font-weight: var(--font-weight-medium);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.btn-create-first:hover {
  background: var(--brand-hover);
  transform: translateY(-1px);
}

.btn-create-first:active {
  transform: translateY(0);
}

/* 右键菜单 */
.context-menu {
  position: fixed;
  min-width: 200px;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
  padding: var(--space-1);
  z-index: 1000;
  animation: context-menu-in var(--duration-fast) var(--ease-out);
}

@keyframes context-menu-in {
  from {
    opacity: 0;
    transform: scale(0.95);
  }
  to {
    opacity: 1;
    transform: scale(1);
  }
}

.menu-item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
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

.menu-item:hover {
  background: var(--bg-hover);
}

.menu-item.danger {
  color: var(--danger);
}

.menu-item .material-symbols-outlined {
  font-size: 18px;
}

.menu-divider {
  height: 1px;
  background: var(--border);
  margin: var(--space-1) 0;
}
</style>
