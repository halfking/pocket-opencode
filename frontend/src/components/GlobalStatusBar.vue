<!--
  GlobalStatusBar — 连接 / 同步 / 离线队列的全局状态条（优化 v4 08 §2.2、§4.2）。

  规则：
  - 置于内容区上方（AppLayout 顶栏下方），不用只显示 Toast。
  - 状态同时有图标 + 文字 + 颜色，不单靠颜色传达。
  - role=status + aria-live=polite：状态变化可被读屏感知，不打断操作。
  - 在线且无待发队列时完全不渲染（不占空间）。
-->
<template>
  <div
    v-if="visible"
    class="global-status-bar"
    :class="`gsb-${tone}`"
    role="status"
    aria-live="polite"
  >
    <span class="gsb-icon" aria-hidden="true">{{ icon }}</span>
    <span class="gsb-text">{{ text }}</span>
    <button
      v-if="showSyncAction"
      class="gsb-action"
      type="button"
      @click="onSyncNow"
    >
      {{ conn.syncing ? '同步中…' : '立即同步' }}
    </button>
    <button
      v-if="conn.lastError !== ''"
      class="gsb-dismiss"
      type="button"
      aria-label="关闭错误提示"
      @click="conn.clearError()"
    >
      <span aria-hidden="true">✕</span>
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useConnectivityStore } from '../stores/connectivity'

const conn = useConnectivityStore()

const visible = computed(() => {
  if (!conn.online) return true
  if (conn.pendingCount > 0) return true
  if (conn.deadLetterCount > 0) return true
  if (conn.lastError !== '') return true
  return false
})

const tone = computed(() => {
  if (!conn.online) return 'offline'
  if (conn.deadLetterCount > 0 || conn.lastError !== '') return 'error'
  if (conn.syncing) return 'syncing'
  return 'pending'
})

const icon = computed(() => {
  if (!conn.online) return '📴'
  if (conn.deadLetterCount > 0 || conn.lastError !== '') return '⚠️'
  if (conn.syncing) return '🔄'
  return '📤'
})

const text = computed(() => {
  if (!conn.online) {
    return conn.pendingCount > 0
      ? `离线中 · ${conn.pendingCount} 条操作待联网发送`
      : '离线中 · 操作将保存到本地'
  }
  if (conn.deadLetterCount > 0) {
    return `${conn.deadLetterCount} 条操作发送失败，可点击重试`
  }
  if (conn.lastError !== '') return `同步出现问题：${conn.lastError}`
  if (conn.syncing) return '正在同步…'
  return `${conn.pendingCount} 条操作待发送`
})

const showSyncAction = computed(() => conn.online && !conn.syncing)

async function onSyncNow() {
  await conn.syncNow()
}
</script>

<style scoped>
.global-status-bar {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  /* 与内容区水平边距对齐（页面水平边距 16px，08 §2.1）。 */
  margin: var(--space-2) var(--space-3) 0;
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  background: var(--bg-card);
  border: 1px solid var(--border);
  color: var(--text-secondary);
  font-size: var(--text-sm);
  text-align: left;
}

.gsb-icon {
  line-height: 1;
}

.gsb-text {
  flex: 1;
  min-width: 0;
}

.gsb-offline {
  border-color: var(--warning, #f59e0b);
}
.gsb-error {
  border-color: var(--danger);
  color: var(--danger);
}
.gsb-syncing .gsb-icon {
  animation: gsb-spin 1s linear infinite;
}

.gsb-action,
.gsb-dismiss {
  border: none;
  background: transparent;
  color: var(--brand-primary);
  font-size: var(--text-sm);
  cursor: pointer;
  /* 触摸目标至少 48px 高（08 §5）。 */
  min-height: 48px;
  padding: 0 var(--space-2);
  border-radius: var(--radius-sm);
}

.gsb-dismiss {
  color: var(--text-muted);
}

@keyframes gsb-spin {
  to {
    transform: rotate(360deg);
  }
}

@media (prefers-reduced-motion: reduce) {
  .gsb-syncing .gsb-icon {
    animation: none;
  }
}
</style>
