<template>
  <aside class="insight" :aria-label="isRecording ? '即时总结' : '会议纪要'">
    <header class="insight-head">
      <span>{{ isRecording ? '即时总结' : '会议纪要' }}</span>
      <span v-if="isUpdating" class="muted">更新中…</span>
    </header>
    <div class="insight-body">
      <p v-if="summaryText" class="summary">{{ summaryText }}</p>
      <p v-else class="muted">{{ emptyHint }}</p>
      <ul v-if="summary?.keyPoints?.length">
        <li v-for="(p, i) in summary.keyPoints" :key="i">{{ p }}</li>
      </ul>
      <section v-if="todos.length" class="block">
        <div class="label">待办</div>
        <div v-for="(a, i) in todos" :key="i" class="todo">
          <span>{{ a.text }}</span>
          <span class="todo-actions">
            <button type="button" @click="$emit('share-todo', a)">转交</button>
            <button type="button" @click="$emit('acc-todo', a)">ACC</button>
          </span>
        </div>
      </section>
      <section v-if="recommendations.length" class="block">
        <div class="label">相关 · 笔记 / 知识库 / 网络</div>
        <button
          v-for="rec in recommendations"
          :key="`${rec.type}:${rec.id}`"
          type="button"
          class="rec"
          @click="$emit('open-related', rec)"
        >
          <span>{{ typeIcon(rec.type) }} {{ rec.title }}</span>
          <span class="muted">{{ typeLabel(rec.type) }}</span>
        </button>
      </section>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { ActionItem, LiveSummary, RecommendItem } from './meetings-store'
import { draftsFromActionItems, type MeetingTodoDraft } from './meeting-todos'

const props = withDefaults(defineProps<{
  summary: LiveSummary | null
  finalSummary?: string | null
  recommendations?: RecommendItem[]
  isUpdating?: boolean
  isRecording?: boolean
}>(), {
  recommendations: () => [],
  isUpdating: false,
  isRecording: false,
  finalSummary: '',
})

defineEmits<{
  'share-todo': [draft: MeetingTodoDraft]
  'acc-todo': [draft: MeetingTodoDraft]
  'open-related': [item: RecommendItem]
}>()

const summaryText = computed(() => props.finalSummary || props.summary?.summary || '')
const todos = computed(() => draftsFromActionItems(props.summary?.actionItems as ActionItem[]))
const emptyHint = computed(() => (
  props.isRecording ? '转写开始后，右侧会滚动更新摘要，并从笔记、知识库和网络检索相关信息' : '点击顶栏「总结」生成当前纪要'
))

function typeIcon(type: string): string {
  const icons: Record<string, string> = {
    note: '📝', email: '📨', meeting: '🎙', contact: '👤', web: '🌐', knowledge: '📚',
  }
  return icons[type] ?? '💡'
}

function typeLabel(type: string): string {
  const labels: Record<string, string> = {
    note: '笔记', email: '邮件', meeting: '会议', contact: '联系人', web: '网络', knowledge: '知识库',
  }
  return labels[type] ?? type
}
</script>

<style scoped>
.insight {
  min-width: 0; min-height: 0; display: flex; flex-direction: column;
  background: var(--bg-card); border-left: 1px solid var(--border-subtle);
}
.insight-head {
  display: flex; justify-content: space-between; align-items: center;
  padding: 10px 12px; font-size: 13px; font-weight: 600; flex-shrink: 0;
}
.insight-body { flex: 1; overflow-y: auto; padding: 0 12px 12px; }
.summary { margin: 0 0 10px; font-size: 13px; line-height: 1.6; color: var(--text-primary); }
.muted { color: var(--text-muted); font-size: 12px; }
ul { margin: 0 0 10px; padding-left: 16px; font-size: 12px; color: var(--text-secondary); line-height: 1.7; }
.block { margin-top: 12px; }
.label { font-size: 11px; color: var(--text-muted); margin-bottom: 6px; }
.todo, .rec {
  display: flex; justify-content: space-between; gap: 8px; width: 100%;
  padding: 8px 0; border: none; border-bottom: 1px solid var(--border-subtle);
  background: transparent; color: var(--text-primary); font-size: 12px; text-align: left;
}
.todo-actions { display: flex; gap: 6px; flex-shrink: 0; }
.todo-actions button {
  border: 1px solid var(--border); border-radius: 999px; background: var(--bg-subtle);
  font-size: 11px; padding: 2px 8px; color: var(--brand-primary);
}
</style>
