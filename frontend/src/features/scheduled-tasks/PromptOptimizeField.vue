<template>
  <div class="field">
    <span class="label">{{ label }}</span>
    <UnifiedComposer
      :model-value="modelValue"
      :placeholder="placeholder"
      :single-line="singleLine"
      :allow-fullscreen="!singleLine"
      :submit-on-enter="false"
      :enable="{ voice: true, image: false, camera: false, file: false, agent: false, optimize: true }"
      @update:model-value="$emit('update:modelValue', $event)"
    >
      <template #submit><span class="sr-only">优化后仍需保存任务</span></template>
    </UnifiedComposer>
  </div>
</template>

<script setup lang="ts">
import UnifiedComposer from '../../components/business/UnifiedComposer.vue'

defineProps<{
  modelValue: string
  label: string
  placeholder?: string
  singleLine?: boolean
}>()
defineEmits<{ 'update:modelValue': [value: string] }>()
</script>

<style scoped>
.field { display: flex; flex-direction: column; gap: 6px; }
.label { font-size: 13px; font-weight: 600; color: var(--text-secondary); }
.sr-only { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); }
</style>
