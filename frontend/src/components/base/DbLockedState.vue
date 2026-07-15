<template>
  <div class="db-locked">
    <span class="db-icon" aria-hidden="true">🔒</span>
    <p class="db-title">本地数据未解锁</p>
    <p class="db-hint">
      {{ hint }}<br />请退出重新登录以初始化本地存储
    </p>
    <button type="button" class="db-action" @click="$emit('relogin')">重新登录</button>
  </div>
</template>

<script setup lang="ts">
withDefaults(
  defineProps<{ hint?: string }>(),
  { hint: '此功能需要本地加密数据库' },
)
defineEmits<{ relogin: [] }>()
</script>

<style scoped>
.db-locked {
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  padding: var(--space-6) var(--space-4);
}
.db-icon {
  font-size: 48px;
  margin-bottom: var(--space-3);
}
.db-title {
  margin: 0 0 var(--space-2);
  font-size: var(--text-lg);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
}
.db-hint {
  margin: 0 0 var(--space-4);
  font-size: var(--text-sm);
  color: var(--text-secondary);
  line-height: 1.5;
}
.db-action {
  padding: var(--space-2) var(--space-5);
  font-size: var(--text-sm);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  cursor: pointer;
}
.db-action:active {
  background: var(--bg-subtle);
}
</style>
