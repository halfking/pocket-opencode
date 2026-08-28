<template>
  <div v-if="alerts.length" class="alert-stack">
    <div
      v-for="alert in alerts"
      :key="alert.id"
      class="alert-toast"
      :class="`alert-toast--${alert.type}`"
      @click="$emit('dismiss', alert.id)"
    >
      <span class="alert-icon">{{ icon(alert.type) }}</span>
      <span class="alert-msg">{{ alert.message }}</span>
      <button type="button" class="alert-close" aria-label="关闭">×</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import type { MeetingAlert } from '../../composables/useMeetingAlerts'

defineProps<{ alerts: MeetingAlert[] }>()
defineEmits<{ dismiss: [id: string] }>()

function icon(type: string): string {
  const map: Record<string, string> = {
    action_item: '✅',
    deadline: '📅',
    info: '💡',
  }
  return map[type] ?? '🔔'
}
</script>

<style scoped>
.alert-stack {
  position: fixed;
  top: calc(var(--topbar-height, 48px) + var(--space-2));
  left: var(--space-3);
  right: var(--space-3);
  z-index: var(--z-fab);
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  pointer-events: none;
}

.alert-toast {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  padding: 10px 12px;
  background: rgba(var(--bg-card-rgb, 255, 255, 255), 0.95);
  backdrop-filter: blur(8px);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-md);
  border-left: 3px solid var(--brand-primary);
  pointer-events: auto;
  animation: slide-in 0.3s ease;
  max-width: 360px;
}

.alert-toast--action_item { border-left-color: #22c55e; }
.alert-toast--deadline { border-left-color: #f59e0b; }
.alert-toast--info { border-left-color: #3b82f6; }

@keyframes slide-in {
  from { opacity: 0; transform: translateY(-8px); }
  to { opacity: 1; transform: translateY(0); }
}

.alert-icon { flex-shrink: 0; font-size: 16px; }

.alert-msg {
  flex: 1;
  font-size: 13px;
  line-height: 1.4;
  color: var(--text-primary);
}

.alert-close {
  border: none;
  background: none;
  font-size: 18px;
  color: var(--text-muted);
  cursor: pointer;
  padding: 0;
  line-height: 1;
}
</style>
