<script setup lang="ts">
/**
 * JsonTreeNode — 树状 JSON 节点的递归渲染器
 *
 * 设计:
 * - 受控展开:父节点默认展开(defaultDepth 层),其余折叠
 * - 点击行展开/折叠
 * - 节点高亮:key / string / number / boolean / null 颜色区分
 */
import { computed, ref } from 'vue'

interface Node {
  type: 'object' | 'array' | 'string' | 'number' | 'boolean' | 'null' | string
  key: string
  pathKey: string
  depth: number
  value?: unknown
  length?: number
  children?: Node[]
}

const props = withDefaults(
  defineProps<{
    node: Node
    defaultDepth?: number
  }>(),
  { defaultDepth: 1 },
)

const open = ref(props.node.depth < props.defaultDepth)

function toggle() {
  open.value = !open.value
}

const isContainer = computed(() => props.node.type === 'object' || props.node.type === 'array')
const preview = computed(() => {
  if (props.node.type === 'object') return `{${props.node.length ?? 0}}`
  if (props.node.type === 'array') return `[${props.node.length ?? 0}]`
  return ''
})

function displayValue(v: unknown, t: string): string {
  if (t === 'string') return JSON.stringify(v ?? '')
  if (v === undefined) return ''
  return String(v)
}
</script>

<template>
  <div class="json-node">
    <div
      class="json-row"
      :class="{ 'is-container': isContainer }"
      :style="{ paddingLeft: `${node.depth * 14 + 4}px` }"
      @click="isContainer && toggle()"
    >
      <span v-if="isContainer" class="json-toggle" :class="{ open }">
        <span class="material-symbols-outlined">{{ open ? 'expand_more' : 'chevron_right' }}</span>
      </span>
      <span v-else class="json-toggle-placeholder" />

      <span class="json-key">{{ node.key }}</span>
      <span class="json-colon">:</span>

      <template v-if="isContainer">
        <span v-if="!open" class="json-preview">{{ preview }}</span>
        <span v-else class="json-bracket">
          {{ node.type === 'object' ? '{' : '[' }}
        </span>
      </template>
      <template v-else>
        <span :class="['json-value', `json-${node.type}`]">{{ displayValue(node.value, node.type) }}</span>
      </template>
    </div>

    <template v-if="isContainer && open && node.children">
      <JsonTreeNode
        v-for="child in node.children"
        :key="child.pathKey"
        :node="child"
        :default-depth="defaultDepth"
      />
      <div
        class="json-row json-close"
        :style="{ paddingLeft: `${node.depth * 14 + 4}px` }"
      >
        <span class="json-toggle-placeholder" />
        <span class="json-bracket">{{ node.type === 'object' ? '}' : ']' }}</span>
      </div>
    </template>
  </div>
</template>

<style scoped>
.json-node {
  font-family: ui-monospace, 'SF Mono', Menlo, monospace;
  font-size: inherit;
}

.json-row {
  display: flex;
  align-items: baseline;
  gap: 4px;
  padding: 1px 4px;
  border-radius: 3px;
  cursor: default;
  min-height: 20px;
}

.json-row.is-container {
  cursor: pointer;
}

.json-row.is-container:hover {
  background: var(--bg-hover, rgba(0, 0, 0, 0.04));
}

.json-toggle,
.json-toggle-placeholder {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  flex-shrink: 0;
  color: var(--text-tertiary, #999);
}

.json-toggle .material-symbols-outlined {
  font-size: 14px;
}

.json-key {
  color: var(--brand-primary, #5b8def);
  font-weight: 500;
}

.json-colon {
  color: var(--text-tertiary, #999);
}

.json-value {
  word-break: break-word;
}

.json-string {
  color: var(--success, #2e7d32);
}

.json-number {
  color: var(--warning, #ed6c02);
}

.json-boolean {
  color: var(--info, #0288d1);
}

.json-null {
  color: var(--text-tertiary, #999);
  font-style: italic;
}

.json-preview,
.json-bracket {
  color: var(--text-tertiary, #999);
  margin-left: 2px;
}
</style>
