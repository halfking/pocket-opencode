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

const props = defineProps<{
  modelValue: string
  error?: string
}>()
defineEmits<{
  'update:modelValue': [value: string]
}>()

/**
 * 2026-10-01 审计修正：这里**不能再套 apiError**。
 *
 * runtime 在**写入** error 时就已经调过 `sttFailureText()`（见
 * native/recordingRuntime.ts），存进来的是面向用户的成品文案，
 * `stt_unavailable:` 错误码前缀已被剥掉。而 apiError 靠
 * `extractErrorCode()` 取第一个冒号前的码当错误码，前缀没了就取不到
 * → 落回通用兜底「语音转写服务尚未配置」，把"网关列了模型但没开通
 * provider，去设置里换外部服务"这条唯一可行动的信息整个盖掉
 * （会议页直接渲染 sttError，反而是完整的）。
 *
 * runtime 的 error 全部写入点都是字面文案或经 sttFailureText 归一的文案，
 * 所以渲染层直接渲染即可。详见 api/__tests__/stt-error-render-chain.test.mjs。
 */
const errorText = computed(() => props.error || '')
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
