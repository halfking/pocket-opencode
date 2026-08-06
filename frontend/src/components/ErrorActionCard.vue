<template>
  <div class="error-action-card" role="alert">
    <span>{{ message }}</span>
    <div class="actions">
      <button v-if="retry" @click="$emit('retry')">重试</button>
      <button v-if="settings" @click="$emit('settings')">打开设置</button>
    </div>
  </div>
</template>

<script setup lang="ts">
defineProps<{ message: string; retry?: boolean; settings?: boolean }>()
defineEmits<{ (event: 'retry'): void; (event: 'settings'): void }>()
</script>

<style scoped>
.error-action-card { display: grid; gap: 10px; margin-top: 24px; padding: 14px; border-radius: 10px; text-align: left; background: var(--bg-card); color: var(--danger); }
.actions { display: flex; gap: 8px; }.actions button { border: 1px solid var(--border); border-radius: 7px; padding: 6px 10px; background: transparent; color: inherit; cursor: pointer; }
</style>
