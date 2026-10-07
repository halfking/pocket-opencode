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
  /* `fixed` 相对**视口**，而顶栏的底边在 `--app-safe-top + --topbar-height`
     （安全区由 body 的 padding-top 顶下来，styles.css:88）。只写
     `--topbar-height + 8px` 会在有状态栏的设备上与顶栏重叠
     （模拟器实测安全区 24px ⇒ 重叠 16px，且 --z-fab 60 > --z-sticky 50，
     是浮层画在顶栏**上面**，不是被盖住）。
     门禁：src/styles/__tests__/topbar-chrome-gate.test.mjs。 */
  top: calc(var(--topbar-height, 48px) + var(--app-safe-top) + var(--space-2));
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

.alert-toast--action_item { border-left-color: var(--success); }
.alert-toast--deadline { border-left-color: var(--warning); }
.alert-toast--info { border-left-color: var(--info); }

@keyframes slide-in {
  from { opacity: 0; transform: translateY(-8px); }
  to { opacity: 1; transform: translateY(0); }
}

.alert-icon { flex-shrink: 0; font-size: var(--text-lg); }

.alert-msg {
  flex: 1;
  font-size: var(--text-smd);
  line-height: 1.4;
  color: var(--text-primary);
}

.alert-close {
  border: none;
  background: none;
  font-size: var(--text-xl);
  color: var(--text-muted);
  cursor: pointer;
  padding: 0;
  line-height: 1;
}
</style>
