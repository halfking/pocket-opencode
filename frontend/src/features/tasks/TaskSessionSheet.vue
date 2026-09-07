<script setup lang="ts">
import { ref, watch } from 'vue'
import { api, type TaskSessionBundleRow, type TaskSessionMessage } from '../../api/client'
import BottomSheet from '../../components/base/BottomSheet.vue'
import SessionKindFilter, { type SessionMsgKind } from './SessionKindFilter.vue'
import TaskSessionTranscript from './TaskSessionTranscript.vue'

const props = defineProps<{
  taskId: string
  row: TaskSessionBundleRow | null
}>()
const emit = defineEmits<{ close: [] }>()

const kinds = ref<SessionMsgKind[]>(['user', 'assistant', 'tool', 'thinking'])
const messages = ref<TaskSessionMessage[]>([])
const summary = ref('')
const title = ref('')
const loading = ref(false)
const error = ref('')

watch(() => props.row, (row) => {
  title.value = row?.title || ''
  summary.value = ''
  messages.value = []
}, { immediate: true })

async function load() {
  if (!props.row || !props.taskId) return
  loading.value = true
  error.value = ''
  try {
    const sid = props.row.agentSessionId || props.row.id
    const kind = (props.row.agentKind || '').replace(/^disk-/, '')
    const data = await api.getTaskSessionTranscript(props.taskId, sid, kinds.value.join(','), kind)
    messages.value = data.messages || []
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载失败'
    messages.value = []
  } finally {
    loading.value = false
  }
}

watch(() => [props.row?.id, kinds.value.join(',')], () => { void load() })

async function extractTitle() {
  if (!props.row) return
  const sid = props.row.agentSessionId || props.row.id
  const { title: next } = await api.extractTaskSessionTitle(props.taskId, sid)
  title.value = next
}

async function summarize() {
  if (!props.row) return
  const sid = props.row.agentSessionId || props.row.id
  const { summary: text } = await api.summarizeTaskSession(props.taskId, sid)
  summary.value = text
}
</script>

<template>
  <BottomSheet
    :model-value="!!row"
    :title="title || row?.title || '会话'"
    close-on-overlay
    @update:model-value="(v) => { if (!v) emit('close') }"
  >
    <div v-if="row" class="sheet-body">
      <SessionKindFilter v-model="kinds" />
      <div class="actions">
        <button type="button" @click="extractTitle">提取标题</button>
        <button type="button" @click="summarize">刷新总结</button>
      </div>
      <p v-if="summary" class="summary">{{ summary }}</p>
      <p v-if="loading" class="empty">加载中…</p>
      <p v-else-if="error" class="empty">{{ error }}</p>
      <TaskSessionTranscript v-else :messages="messages" :kinds="kinds" />
    </div>
  </BottomSheet>
</template>

<style scoped>
.sheet-body { display: flex; flex-direction: column; gap: 10px; max-height: 70vh; overflow: auto; }
.actions { display: flex; gap: 8px; }
.actions button { font-size: 12px; }
.summary { font-size: 13px; color: var(--text-secondary, #666); margin: 0; }
.empty { font-size: 13px; color: var(--text-secondary, #888); }
</style>
