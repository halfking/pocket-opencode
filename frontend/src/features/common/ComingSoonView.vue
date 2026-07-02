<!--
  ComingSoonView — placeholder for modules whose UI is not yet implemented
  (meetings, chat-aggregation). Prevents dead routes / blank screens when
  the bottom nav links to a module that's still in design or Phase 6A/6B.
-->
<template>
  <AppLayout>
    <div class="coming-soon">
      <div class="icon">{{ icon }}</div>
      <h2>{{ title }}</h2>
      <p class="desc">{{ desc }}</p>
      <p class="phase">{{ phase }}</p>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import AppLayout from '../../app/AppLayout.vue'

defineProps<{
  icon?: string
  title: string
  desc?: string
  phase?: string
}>()
</script>

<style scoped>
.coming-soon {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-height: 60vh;
  text-align: center;
  gap: var(--space-2);
}
.icon { font-size: 56px; }
h2 { margin: 0; font-size: 18px; color: var(--text-primary); }
.desc { color: var(--text-secondary); font-size: 14px; max-width: 280px; }
.phase {
  color: var(--text-muted);
  font-size: 12px;
  background: var(--bg-subtle);
  padding: var(--space-1) var(--space-3);
  border-radius: var(--radius-full);
}
</style>
