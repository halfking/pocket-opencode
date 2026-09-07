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
    <p v-if="error" class="studio-error" role="alert">{{ error }}</p>
  </div>
</template>

<script setup lang="ts">
defineProps<{
  modelValue: string
  error?: string
}>()
defineEmits<{
  'update:modelValue': [value: string]
}>()
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
