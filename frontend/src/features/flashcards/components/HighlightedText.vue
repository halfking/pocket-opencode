<template>
  <span class="hlt">
    <template v-for="(seg, i) in segments" :key="i">
      <mark v-if="seg.match" class="mark">{{ seg.text }}</mark>
      <span v-else>{{ seg.text }}</span>
    </template>
  </span>
</template>

<script setup lang="ts">
/**
 * HighlightedText —— 把 query 高亮显示（不依赖 v-html，XSS 安全）。
 *
 * 空 query 时直接返回原文。
 * 不区分大小写（输入侧已 lower，匹配侧也 lower）。
 */
import { computed } from 'vue'

const props = defineProps<{ text: string; query: string }>()

const segments = computed(() => {
  const q = props.query.trim()
  if (!q) return [{ text: props.text, match: false }]
  const lower = props.text.toLowerCase()
  const ql = q.toLowerCase()
  const out: Array<{ text: string; match: boolean }> = []
  let cursor = 0
  let idx = lower.indexOf(ql, cursor)
  while (idx >= 0) {
    if (idx > cursor) out.push({ text: props.text.slice(cursor, idx), match: false })
    out.push({ text: props.text.slice(idx, idx + ql.length), match: true })
    cursor = idx + ql.length
    idx = lower.indexOf(ql, cursor)
  }
  if (cursor < props.text.length) out.push({ text: props.text.slice(cursor), match: false })
  return out
})
</script>

<style scoped>
.hlt { white-space: pre-wrap; word-break: break-word; }
.mark {
  background: rgba(245, 200, 60, 0.4);
  color: inherit;
  padding: 0 1px;
  border-radius: 2px;
}
</style>