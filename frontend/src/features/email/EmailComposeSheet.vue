<!--
  回复 / 转发 / 转 Todo：默认不占主区，由导航栏按钮打开。
-->
<template>
  <BottomSheet
    :model-value="kind !== 'hidden'"
    :title="sheetTitle"
    height="auto"
    aria-label="邮件操作输入"
    @update:model-value="onVisible"
  >
    <div v-if="kind === 'forward'" class="to-row">
      <label class="to-label" for="email-fwd-to">收件人</label>
      <input
        id="email-fwd-to"
        v-model="toRaw"
        class="to-input"
        type="email"
        inputmode="email"
        autocomplete="email"
        placeholder="多个地址用逗号分隔"
      />
    </div>
    <UnifiedComposer
      v-model="draft"
      :placeholder="placeholder"
      :enable="{ voice: true, image: false, camera: false, file: false, agent: true, optimize: true }"
      :submit-on-enter="false"
      :submit-label="submitLabel"
      :submitting="submitting"
      @submit="onSubmit"
    />
    <p v-if="error" class="err" role="alert">{{ error }}</p>
  </BottomSheet>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { BottomSheet, UnifiedComposer } from '../../components'
import { composeSheetTitle, type ComposeKind } from './compose-mode'
import { parseForwardRecipients } from './email-body-format'

const props = defineProps<{
  kind: ComposeKind
  fromName: string
  seed: string
  submitting: boolean
  error: string
}>()

const emit = defineEmits<{
  close: []
  submit: [payload: { text: string; to: string[] }]
}>()

const draft = ref('')
const toRaw = ref('')

const sheetTitle = computed(() => composeSheetTitle(props.kind, props.fromName))
const placeholder = computed(() => {
  if (props.kind === 'todo') return '确认任务标题和说明…'
  if (props.kind === 'forward') return '可补一句说明，原文已附在下方…'
  return '写回复…'
})
const submitLabel = computed(() => {
  if (props.kind === 'todo') return '创建任务'
  if (props.kind === 'forward') return '转发'
  return '发送'
})

watch(
  () => [props.kind, props.seed] as const,
  ([kind, seed]) => {
    draft.value = kind === 'hidden' ? '' : seed
    if (kind !== 'forward') toRaw.value = ''
  },
  { immediate: true },
)

function onVisible(open: boolean) {
  if (!open) emit('close')
}

function onSubmit(payload: { text: string }) {
  emit('submit', {
    text: payload.text,
    to: parseForwardRecipients(toRaw.value),
  })
}
</script>

<style scoped>
.to-row { display: flex; flex-direction: column; gap: 6px; margin-bottom: var(--space-3); }
.to-label { font-size: 12px; color: var(--text-secondary); }
.to-input {
  width: 100%;
  box-sizing: border-box;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-card);
  color: var(--text-primary);
  font-size: 14px;
}
.err { margin: var(--space-2) 0 0; color: var(--danger); font-size: 12px; }
</style>
