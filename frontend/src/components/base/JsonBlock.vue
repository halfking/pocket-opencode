<script setup lang="ts">
/**
 * JsonBlock — 通用 JSON 渲染组件
 *
 * 特性:
 * - 双视图模式:text(原始字符串) / tree(高亮 + 折叠)
 * - 全屏查看:在小屏 / 长 JSON 下点开全屏,查看体验更佳
 * - 容错:解析失败时降级为 text 模式(显示原始字符串)
 *
 * 设计要点:
 * - 不依赖任何外部库(本地解析 + 高亮)
 * - 树状视图惰性展开:默认只展开第 1 层
 * - 切换视图 / 全屏 状态保存在父组件 props,避免组件内"闪烁"
 */
import { computed, ref } from 'vue'

interface Props {
  /** 任意可序列化值。接收字符串时尝试按 JSON 解析,失败则按文本显示。 */
  data: unknown
  /** 初始显示模式 */
  initialMode?: 'text' | 'tree'
  /** 最大可折叠深度(超过该深度全部展开)。仅影响首屏渲染性能。 */
  collapseDepth?: number
  /** 受控:当前模式(由父组件在 fullscreen 下切换使用) */
  mode?: 'text' | 'tree'
}

const props = withDefaults(defineProps<Props>(), {
  initialMode: 'tree',
  collapseDepth: 1,
  mode: undefined,
})

const emit = defineEmits<{
  (e: 'update:mode', v: 'text' | 'tree'): void
}>()

const isFullscreen = ref(false)

// 把任意输入规整成可序列化的字符串(用于 text 模式)
const rawText = computed(() => {
  if (typeof props.data === 'string') return props.data
  try {
    return JSON.stringify(props.data, null, 2)
  } catch {
    return String(props.data)
  }
})

// 解析:字符串 → 尝试 JSON.parse;其余原样返回
const parsed = computed<unknown>(() => {
  if (typeof props.data === 'string') {
    try {
      return JSON.parse(props.data)
    } catch {
      return null
    }
  }
  return props.data
})

const internalMode = ref<'text' | 'tree'>(props.initialMode)
const currentMode = computed<'text' | 'tree'>({
  get: () => (props.mode ?? internalMode.value),
  set: (v) => {
    internalMode.value = v
    emit('update:mode', v)
  },
})

const canShowTree = computed(() => parsed.value !== null && parsed.value !== undefined)
const isLarge = computed(() => rawText.value.length > 600 || rawText.value.split('\n').length > 20)

function setMode(m: 'text' | 'tree') {
  currentMode.value = m
}

function toggleFullscreen() {
  isFullscreen.value = !isFullscreen.value
}

// 树节点(字符串里按 raw 拼,Vue 负责渲染)
// v-for 的 key 必须稳定:用 path 数组转字符串
function buildTree(value: unknown, key?: string, depth = 0, path: string[] = []): unknown {
  const nodeKey = key ?? '$'
  const nodePath = key ? [...path, key] : path
  const pathKey = nodePath.join('.') || '$'

  if (value === null) return { type: 'null', key: nodeKey, pathKey, depth }
  if (Array.isArray(value)) {
    return {
      type: 'array',
      key: nodeKey,
      pathKey,
      depth,
      length: value.length,
      children: value.map((v, i) => buildTree(v, String(i), depth + 1, nodePath)),
    }
  }
  if (typeof value === 'object') {
    const entries = Object.entries(value as Record<string, unknown>)
    return {
      type: 'object',
      key: nodeKey,
      pathKey,
      depth,
      length: entries.length,
      children: entries.map(([k, v]) => buildTree(v, k, depth + 1, nodePath)),
    }
  }
  return { type: typeof value, key: nodeKey, pathKey, depth, value }
}
</script>

<template>
  <div class="json-block" :class="{ 'is-fullscreen': isFullscreen, 'is-large': isLarge }">
    <div class="json-toolbar">
      <div class="json-toolbar-left">
        <button
          type="button"
          class="json-tab"
          :class="{ active: currentMode === 'tree' }"
          :disabled="!canShowTree"
          @click="setMode('tree')"
        >
          树状
        </button>
        <button
          type="button"
          class="json-tab"
          :class="{ active: currentMode === 'text' }"
          @click="setMode('text')"
        >
          文本
        </button>
      </div>
      <button
        type="button"
        class="json-fs-btn"
        :aria-label="isFullscreen ? '退出全屏' : '全屏查看'"
        @click="toggleFullscreen"
      >
        <span class="material-symbols-outlined">{{ isFullscreen ? 'close_fullscreen' : 'fullscreen' }}</span>
      </button>
    </div>

    <div class="json-body">
      <template v-if="currentMode === 'text'">
        <pre class="json-text">{{ rawText }}</pre>
      </template>
      <template v-else-if="currentMode === 'tree' && canShowTree">
        <JsonTreeNode :node="buildTree(parsed)" :default-depth="collapseDepth" />
      </template>
      <template v-else>
        <pre class="json-text">{{ rawText }}</pre>
        <div class="json-hint">无法解析为 JSON,已按文本展示</div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.json-block {
  display: flex;
  flex-direction: column;
  background: var(--bg-tertiary, #f8f8f8);
  border-radius: 6px;
  overflow: hidden;
  font-family: ui-monospace, 'SF Mono', Menlo, monospace;
  font-size: 12px;
  line-height: 1.5;
}

.json-block.is-large {
  max-height: 280px;
}

.json-block.is-fullscreen {
  position: fixed;
  inset: 0;
  z-index: 999;
  background: var(--bg-primary, #fff);
  border-radius: 0;
  max-height: none;
}

.json-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 4px 8px;
  background: var(--bg-secondary, #f0f0f0);
  border-bottom: 1px solid var(--border-color, #e5e5e5);
}

.json-block.is-fullscreen .json-toolbar {
  padding: 8px 16px;
}

.json-toolbar-left {
  display: flex;
  gap: 4px;
}

.json-tab {
  background: transparent;
  border: 1px solid transparent;
  border-radius: 4px;
  padding: 2px 8px;
  font-size: 11px;
  color: var(--text-secondary, #666);
  cursor: pointer;
}

.json-tab:hover:not(:disabled) {
  background: var(--bg-hover, rgba(0, 0, 0, 0.04));
}

.json-tab.active {
  background: var(--bg-primary, #fff);
  border-color: var(--border-color, #e5e5e5);
  color: var(--text-primary, #000);
  font-weight: 600;
}

.json-tab:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.json-fs-btn {
  background: transparent;
  border: none;
  padding: 4px;
  border-radius: 4px;
  cursor: pointer;
  color: var(--text-secondary, #666);
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.json-fs-btn:hover {
  background: var(--bg-hover, rgba(0, 0, 0, 0.06));
  color: var(--text-primary, #000);
}

.json-fs-btn .material-symbols-outlined {
  font-size: 18px;
}

.json-body {
  flex: 1;
  overflow: auto;
  padding: 8px 12px;
  min-height: 0;
}

.json-block.is-fullscreen .json-body {
  padding: 16px 24px;
  font-size: 14px;
}

.json-text {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  color: var(--text-primary, #000);
}

.json-hint {
  margin-top: 4px;
  font-size: 10px;
  color: var(--text-tertiary, #999);
}
</style>
