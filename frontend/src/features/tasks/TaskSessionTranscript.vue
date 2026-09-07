<script setup lang="ts">
import { computed } from 'vue'
import type { TaskSessionMessage } from '../../api/client'

const props = defineProps<{
  messages: TaskSessionMessage[]
  kinds: string[]
}>()

const visible = computed(() => {
  const allow = new Set(props.kinds)
  return props.messages.filter((m) => allow.has(m.type) || (m.type === 'other' && allow.has('assistant')))
})
</script>

<template>
  <ul class="transcript">
    <li v-if="!visible.length" class="empty">没有匹配的消息</li>
    <li v-for="m in visible" :key="m.id" :class="['msg', m.type]">
      <div class="meta">
        <span class="type">{{ m.type }}</span>
        <span v-if="m.name" class="name">{{ m.name }}</span>
      </div>
      <pre class="text">{{ m.text }}</pre>
    </li>
  </ul>
</template>

<style scoped>
.transcript { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 10px; }
.empty { color: var(--text-secondary, #888); font-size: 13px; }
.msg { padding: 8px 10px; border-radius: 8px; background: var(--bg-elevated, #f5f5f5); }
.msg.user { background: var(--accent-bg, #e8f1ff); }
.msg.thinking { opacity: 0.85; font-style: italic; }
.meta { font-size: 11px; color: var(--text-secondary, #888); display: flex; gap: 8px; margin-bottom: 4px; }
.text { margin: 0; white-space: pre-wrap; word-break: break-word; font: inherit; font-size: 13px; }
</style>
