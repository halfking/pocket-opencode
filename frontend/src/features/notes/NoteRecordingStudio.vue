<template>
  <div class="studio">
    <label class="studio-label" for="note-live-transcript">即时转写</label>
    <textarea
      id="note-live-transcript"
      class="studio-input"
      :value="modelValue"
      placeholder="开始说话，文字会出现在这里…"
      @input="$emit('update:modelValue', ($event.target as HTMLTextAreaElement).value)"
    />
    <p v-if="error" class="studio-error" role="alert">{{ errorText }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useApiError } from '../../composables/useApiError'

const props = defineProps<{
  modelValue: string
  error?: string
}>()
defineEmits<{
  'update:modelValue': [value: string]
}>()

const apiError = useApiError()
/**
 * recordingRuntime.error 存的是原始异常文本（转写接口失败时可能是
 * "Failed to fetch" / 英文错误码），直接上屏用户无法据此行动。
 * 这里统一归一：已知类别走 i18n，识别不出就给领域兜底「语音转文字失败」。
 */
const errorText = computed(() =>
  props.error ? apiError(props.error, 'errors.sttNotConfigured') : '',
)
</script>

<style scoped>
.studio {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3);
  min-height: 40vh;
}
.studio-label {
  font-size: 12px;
  color: var(--text-muted);
}
.studio-input {
  flex: 1;
  min-height: 36vh;
  width: 100%;
  box-sizing: border-box;
  padding: var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-card);
  color: var(--text-primary);
  font-size: 15px;
  line-height: 1.6;
  resize: vertical;
  font-family: inherit;
}
.studio-error {
  margin: 0;
  color: var(--danger);
  font-size: 13px;
}
</style>
