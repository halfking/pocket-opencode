<script setup lang="ts">
export type SessionMsgKind = 'user' | 'assistant' | 'tool' | 'thinking'

const props = defineProps<{ modelValue: SessionMsgKind[] }>()
const emit = defineEmits<{ 'update:modelValue': [SessionMsgKind[]] }>()

const options: { id: SessionMsgKind; label: string }[] = [
  { id: 'user', label: '用户' },
  { id: 'assistant', label: '回复' },
  { id: 'tool', label: '工具' },
  { id: 'thinking', label: '思考' },
]

function toggle(id: SessionMsgKind) {
  const cur = props.modelValue
  const next = cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id]
  emit('update:modelValue', next.length ? next : [...options.map((o) => o.id)])
}
</script>

<template>
  <div class="kind-filter">
    <button
      v-for="opt in options"
      :key="opt.id"
      type="button"
      :class="{ on: modelValue.includes(opt.id) }"
      @click="toggle(opt.id)"
    >{{ opt.label }}</button>
  </div>
</template>

<style scoped>
.kind-filter { display: flex; gap: 6px; flex-wrap: wrap; }
button {
  border: 1px solid var(--border, #ddd);
  background: transparent;
  color: inherit;
  border-radius: 999px;
  padding: 4px 10px;
  font-size: 12px;
}
button.on { background: var(--accent, #3b82f6); color: #fff; border-color: transparent; }
</style>
