<!--
  NotebookTreeNode — 笔记本树形节点
  
  递归组件，支持任意层级嵌套
-->
<template>
  <div class="tree-node" :style="{ '--level': level }">
    <div
      class="node-content"
      :class="{ 
        selected: selectedId === notebook.id,
        'has-children': hasChildren
      }"
      role="treeitem"
      :aria-expanded="hasChildren ? isExpanded : undefined"
      :aria-level="level + 1"
      :aria-selected="selectedId === notebook.id"
      @click="handleClick"
      @contextmenu="handleContextMenu"
    >
      <button
        v-if="hasChildren"
        class="expand-btn"
        :aria-label="isExpanded ? t('knowledge.collapse') : t('knowledge.expand')"
        @click.stop="handleToggle"
      >
        <span class="material-symbols-outlined">
          {{ isExpanded ? 'expand_more' : 'chevron_right' }}
        </span>
      </button>
      <span v-else class="expand-placeholder"></span>

      <span class="node-icon" :style="{ color: notebook.color || 'var(--text-muted)' }">
        <span class="material-symbols-outlined">
          {{ notebook.icon || 'folder' }}
        </span>
      </span>

      <span class="node-label">{{ notebook.name }}</span>

      <span v-if="notebook.noteCount !== undefined" class="node-count">
        {{ notebook.noteCount }}
      </span>
    </div>

    <div
      v-if="hasChildren && isExpanded"
      class="node-children"
      role="group"
    >
      <NotebookTreeNode
        v-for="child in notebook.children"
        :key="child.id"
        :notebook="child"
        :selected-id="selectedId"
        :expanded-ids="expandedIds"
        :level="level + 1"
        @select="$emit('select', $event)"
        @toggle="$emit('toggle', $event)"
        @create="$emit('create', $event)"
        @update="(id, data) => $emit('update', id, data)"
        @delete="$emit('delete', $event)"
        @contextmenu="$emit('contextmenu', $event, child.id)"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Notebook } from '../../types/knowledge'

interface Props {
  notebook: Notebook
  selectedId?: string
  expandedIds: Set<string>
  level: number
}

interface Emits {
  (e: 'select', notebookId: string): void
  (e: 'toggle', notebookId: string): void
  (e: 'create', parentId: string): void
  (e: 'update', notebookId: string, data: Partial<Notebook>): void
  (e: 'delete', notebookId: string): void
  (e: 'contextmenu', event: MouseEvent, notebookId: string): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()
const { t } = useI18n()

const hasChildren = computed(() => {
  return props.notebook.children && props.notebook.children.length > 0
})

const isExpanded = computed(() => {
  return props.expandedIds.has(props.notebook.id)
})

function handleClick() {
  emit('select', props.notebook.id)
}

function handleToggle() {
  emit('toggle', props.notebook.id)
}

function handleContextMenu(event: MouseEvent) {
  emit('contextmenu', event, props.notebook.id)
}
</script>

<script lang="ts">
export default {
  name: 'NotebookTreeNode'
}
</script>

<style scoped>
.tree-node {
  --indent: calc(var(--level) * 20px);
}

.node-content {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  padding-left: calc(var(--space-3) + var(--indent));
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-out);
  user-select: none;
  min-height: 36px;
}

.node-content:hover {
  background: var(--bg-hover);
}

.node-content.selected {
  background: var(--brand-bg);
  color: var(--brand-primary);
}

.node-content.selected .node-icon {
  color: var(--brand-primary) !important;
}

.expand-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  padding: 0;
  border: none;
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  border-radius: var(--radius-sm);
  transition: all var(--duration-fast) var(--ease-out);
  flex-shrink: 0;
}

.expand-btn:hover {
  background: var(--bg-secondary);
  color: var(--text-primary);
}

.expand-btn .material-symbols-outlined {
  font-size: 18px;
}

.expand-placeholder {
  width: 20px;
  flex-shrink: 0;
}

.node-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.node-icon .material-symbols-outlined {
  font-size: 20px;
}

.node-label {
  flex: 1;
  font-size: var(--text-sm);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.node-count {
  font-size: var(--text-xs);
  color: var(--text-muted);
  padding: 2px 6px;
  border-radius: var(--radius-full);
  background: var(--bg-secondary);
  font-variant-numeric: tabular-nums;
  flex-shrink: 0;
}

.node-content.selected .node-count {
  background: var(--brand-primary);
  color: white;
}

.node-children {
  /* 子节点容器不需要额外样式，缩进由 --indent 控制 */
}
</style>
