<script setup lang="ts">
/**
 * TaskSessionSheet — 会话抽屉（指挥中心 / 任务详情共用）。
 *
 * 业务逻辑已迁至 `useTaskSessionSheet`（2026-09-20 ViewModel 拆分）；
 * 本文件仅负责模板 + props/emit + 与 BottomSheet 的绑定。
 */
import { computed, toRef, watch } from 'vue'
import type { TaskSessionBundleRow } from '../../api/client'
import BottomSheet from '../../components/base/BottomSheet.vue'
import SessionKindFilter from './SessionKindFilter.vue'
import TaskSessionTranscript from './TaskSessionTranscript.vue'
import { useTaskSessionSheet } from './useTaskSessionSheet'

const props = defineProps<{
  taskId: string
  row: TaskSessionBundleRow | null
  /**
   * 初始草稿（「继续跟进」预填「继续」，用户可改写后再发）。
   *
   * ★ 2026-10-27：这是「提示词的修改」在 UI 上的落点 —— 预填的提示词
   *   必须**可编辑**，否则"修改提示词"就只是一句声明。
   */
  initialPrompt?: string
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
  draft,
  sending,
  sendError,
  sentHint,
  reload: _reload,
  extractTitle,
  summarize,
  send,
} = useTaskSessionSheet({ taskId: taskIdRef, row: rowRef })

// reload 由 useTaskSessionSheet 内部 watch 触发；这里保留引用避免被 vue-tsc 视作未用
void _reload

// 外部给的初始提示词：只在会话切换的那一刻灌进草稿，
// 之后由用户自己改写（watch draft 会在用户打字时覆盖回来，所以只看 row）。
watch(
  () => [rowRef.value?.id, props.initialPrompt] as const,
  ([, initial]) => {
    if (initial !== undefined) draft.value = initial
  },
  { immediate: true },
)

const sheetTitle = computed(() => title.value || props.row?.title || '会话')

/** 原生会话才有续写能力（实例会话走的是全局会话视图那条路）。 */
const canContinue = computed(() => props.row?.native === true)

async function onSend(): Promise<void> {
  await send()
}
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

      <!-- 「继续跟进 / 提示词的修改」的落点。
           只有**原生会话**才有这一块：实例会话的消息走全局会话视图
           （那条路要求 instance_id），在这里发会发不出去。 -->
      <div v-if="canContinue" class="composer">
        <textarea
          v-model="draft"
          class="composer-input"
          rows="3"
          placeholder="继续，或改写提示词后发送"
        />
        <div class="composer-actions">
          <span v-if="sendError" class="composer-error">{{ sendError }}</span>
          <span v-else-if="sentHint" class="composer-hint">{{ sentHint }}</span>
          <button
            type="button"
            class="composer-send"
            :disabled="sending || !draft.trim()"
            @click="onSend"
          >{{ sending ? '发送中…' : '发送' }}</button>
        </div>
      </div>
    </div>
  </BottomSheet>
</template>

<style scoped>
.sheet-body { display: flex; flex-direction: column; gap: 10px; max-height: 70vh; overflow: auto; }
.actions { display: flex; gap: 8px; }
.actions button { font-size: var(--text-sm); }
.summary { font-size: var(--text-smd); color: var(--text-secondary, #666); margin: 0; }
.empty { font-size: var(--text-smd); color: var(--text-secondary, #888); }
.composer { display: flex; flex-direction: column; gap: 8px; padding-top: 8px; border-top: 1px solid var(--border, #e5e7eb); }
.composer-input { width: 100%; font-size: var(--text-smd); font-family: inherit; padding: 8px; border-radius: 8px; border: 1px solid var(--border, #d1d5db); background: var(--bg-card, #fff); color: var(--text-primary, #111); resize: vertical; }
.composer-actions { display: flex; align-items: center; gap: 8px; justify-content: flex-end; }
.composer-error { font-size: var(--text-sm); color: var(--danger, #dc2626); flex: 1; }
.composer-hint { font-size: var(--text-sm); color: var(--text-secondary, #666); flex: 1; }
.composer-send { font-size: var(--text-sm); padding: 6px 14px; }
</style>
