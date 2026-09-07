<template>
  <section v-if="summary" class="brief" aria-live="polite">
    <h2 class="brief-title">{{ offline ? '匹配结果' : '智能摘要' }}</h2>
    <p class="brief-text">{{ summary }}</p>
  </section>
</template>

<script setup lang="ts">
defineProps<{
  summary: string
  offline?: boolean
}>()
</script>

<style scoped>
.brief {
  margin: var(--space-2) var(--space-3);
  padding: var(--space-3);
  border-radius: var(--radius-md);
  background: var(--brand-bg);
  border: 1px solid var(--brand-primary);
}
.brief-title { margin: 0 0 6px; font-size: 13px; color: var(--brand-primary); }
.brief-text { margin: 0; font-size: 14px; line-height: 1.5; color: var(--text-primary); }
</style>
