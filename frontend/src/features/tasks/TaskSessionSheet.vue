<script setup lang="ts">
/**
 * TaskSessionSheet — 会话抽屉（指挥中心 / 任务详情共用）。
 *
 * 业务逻辑已迁至 `useTaskSessionSheet`（2026-09-20 ViewModel 拆分）；
 * 本文件仅负责模板 + props/emit + 与 BottomSheet 的绑定。
 */
import { computed, toRef } from 'vue'
import type { TaskSessionBundleRow } from '../../api/client'
import BottomSheet from '../../components/base/BottomSheet.vue'
import SessionKindFilter from './SessionKindFilter.vue'
import TaskSessionTranscript from './TaskSessionTranscript.vue'
import { useTaskSessionSheet } from './useTaskSessionSheet'

const props = defineProps<{
  taskId: string
  row: TaskSessionBundleRow | null
}>()
const emit = defineEmits<{ close: [] }>()

const taskIdRef = toRef(props, 'taskId')
const rowRef = toRef(props, 'row')

const {
  kinds,
  messages,
  summary,
  title,
  loading,
  error,
  reload: _reload,
  extractTitle,
  summarize,
} = useTaskSessionSheet({ taskId: taskIdRef, row: rowRef })

// reload 由 useTaskSessionSheet 内部 watch 触发；这里保留引用避免被 vue-tsc 视作未用
void _reload

const sheetTitle = computed(() => title.value || props.row?.title || '会话')
</script>

<template>
  <BottomSheet
    :model-value="!!row"
    :title="sheetTitle"
    close-on-overlay
    @update:model-value="(v: boolean) => { if (!v) emit('close') }"
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
