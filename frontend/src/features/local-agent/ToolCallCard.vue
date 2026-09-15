<!--
  ToolCallCard — 工具调用卡(openhands generic-event-message 精简版)。
  标题:工具中文名 + 状态指示;展开看参数与结果/错误。风险 medium/high
  常显标签,与审批条联动。
-->
<template>
  <div class="tool-card" :class="`st-${item.state}`">
    <button class="head" type="button" @click="expanded = !expanded" :aria-expanded="expanded">
      <span class="material-symbols-outlined icon" aria-hidden="true">{{ icon }}</span>
      <span class="name">{{ item.name }}</span>
      <span v-if="risky" class="risk-chip" aria-hidden="true">需确认</span>
      <span class="state-text">{{ stateText }}</span>
      <span class="material-symbols-outlined chev" aria-hidden="true">{{ expanded ? 'expand_less' : 'expand_more' }}</span>
    </button>
    <div v-if="expanded" class="body">
      <div v-if="item.args && Object.keys(item.args).length" class="section">
        <p class="label">参数</p>
        <pre class="code">{{ prettyArgs }}</pre>
      </div>
      <div v-if="item.result" class="section">
        <p class="label">结果</p>
        <pre class="code">{{ clippedResult }}</pre>
      </div>
      <div v-if="item.error" class="section">
        <p class="label err">错误</p>
        <pre class="code err">{{ item.error }}</pre>
      </div>
      <p v-if="item.durationMs != null" class="meta">耗时 {{ (item.durationMs / 1000).toFixed(1) }}s</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import type { TimelineItem } from '../../localagent/runtime.ts'

const props = defineProps<{ item: TimelineItem }>()
const expanded = ref(false)

const icon = computed(() => {
  const map: Record<string, string> = {
    current_time: 'schedule',
    calculate: 'calculate',
    device_info: 'smartphone',
    http_fetch: 'public',
    read_file: 'draft',
    write_file: 'edit_document',
    list_files: 'folder_open',
    task_plan: 'checklist',
    load_skill: 'bolt',
  }
  return map[props.item.name ?? ''] ?? 'handyman'
})

const risky = computed(() => props.item.risk === 'medium' || props.item.risk === 'high')

const stateText = computed(() => {
  switch (props.item.state) {
    case 'running':
      return '执行中…'
    case 'completed':
      return '完成'
    case 'error':
      return '失败'
    case 'denied':
      return '已拒绝'
    default:
      return ''
  }
})

const prettyArgs = computed(() => {
  try {
    return JSON.stringify(props.item.args ?? {}, null, 2)
  } catch {
    return String(props.item.args)
  }
})

const clippedResult = computed(() => {
  const r = props.item.result ?? ''
  return r.length > 1200 ? `${r.slice(0, 1200)}\n…(长结果已截断)` : r
})
</script>

<style scoped>
.tool-card {
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-subtle);
  overflow: hidden;
}
.tool-card.st-running { border-color: var(--brand-primary, #4c8dff); }
.tool-card.st-error { border-color: var(--danger, #e5484d); }
.tool-card.st-denied { opacity: 0.75; }

.head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  padding: var(--space-2) var(--space-3);
  background: transparent;
  border: none;
  color: var(--text-primary);
  font-size: 13px;
  cursor: pointer;
  text-align: left;
}
.icon { font-size: 18px; color: var(--text-secondary); }
.tool-card.st-running .icon { color: var(--brand-primary, #4c8dff); }
.name { font-weight: var(--font-weight-semibold); }
.risk-chip {
  font-size: 10px;
  padding: 1px 6px;
  border-radius: var(--radius-full);
  background: rgba(230, 159, 0, 0.14);
  color: #b07a00;
}
.state-text {
  margin-left: auto;
  color: var(--text-secondary);
  font-size: 12px;
}
.tool-card.st-completed .state-text { color: var(--success, #2e7d32); }
.tool-card.st-error .state-text { color: var(--danger, #e5484d); }
.chev { font-size: 18px; color: var(--text-muted); }

.body {
  padding: 0 var(--space-3) var(--space-3);
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}
.section .label {
  margin: 0 0 2px;
  font-size: 11px;
  color: var(--text-muted);
}
.section .label.err { color: var(--danger, #e5484d); }
.code {
  margin: 0;
  padding: var(--space-2);
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  font-size: 12px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-all;
  max-height: 200px;
  overflow-y: auto;
}
.code.err { color: var(--danger, #e5484d); }
.meta { margin: 0; font-size: 11px; color: var(--text-muted); }
</style>
