<!--
  BottomNav — the single navigation bar. Rendered by AppLayout.
  5 primary tabs + a "more" sheet for secondary modules (vault, settings,
  instances, sessions). Active state derives from the current route so we
  no longer hardcode "active" per view.

  Accessibility:
  - <nav aria-label="主导航"> wraps the whole bar.
  - Each <router-link> sets aria-current="page" when active so screen
    readers announce the current tab.
  - The "更多" button uses aria-haspopup + aria-expanded.
  - The bottom sheet uses role="dialog" + aria-modal + aria-label.
-->
<template>
  <nav class="bottom-nav" aria-label="主导航">
    <router-link
      v-for="item in items"
      :key="item.to"
      :to="item.to"
      class="nav-item"
      :class="{ active: isActive(item) }"
      :aria-current="isActive(item) ? 'page' : undefined"
    >
      <span class="icon" aria-hidden="true">{{ item.icon }}</span>
      <span class="label">{{ item.label }}</span>
    </router-link>

    <button
      class="nav-item"
      type="button"
      aria-haspopup="dialog"
      :aria-expanded="showMore"
      @click="showMore = !showMore"
    >
      <span class="icon" aria-hidden="true">⋮</span>
      <span class="label">更多</span>
    </button>

    <div
      v-if="showMore"
      class="more-sheet"
      role="dialog"
      aria-modal="true"
      aria-label="更多功能"
      @click.self="showMore = false"
    >
      <div class="more-panel">
        <router-link
          v-for="item in more"
          :key="item.to"
          :to="item.to"
          class="more-item"
          :aria-current="isActive(item) ? 'page' : undefined"
          @click="showMore = false"
        >
          <span class="icon" aria-hidden="true">{{ item.icon }}</span>
          <span>{{ item.label }}</span>
        </router-link>
      </div>
    </div>
  </nav>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRoute } from 'vue-router'

const route = useRoute()
const showMore = ref(false)

interface NavItem { to: string; icon: string; label: string; match?: string }

const items: NavItem[] = [
  { to: '/ai', icon: '🤖', label: 'AI', match: '/ai' },
  { to: '/notes', icon: '📝', label: '笔记', match: '/notes' },
  { to: '/meetings', icon: '🎙️', label: '会议', match: '/meetings' },
  { to: '/email', icon: '📨', label: '邮件', match: '/email' },
]

const more: NavItem[] = [
  { to: '/pkm/today', icon: '🗒️', label: 'PKM笔记' },
  { to: '/vault', icon: '🔐', label: '密码箱' },
  { to: '/tasks', icon: '📋', label: '任务' },
  { to: '/sessions', icon: '💬', label: '会话' },
  { to: '/instances', icon: '💻', label: '实例' },
  { to: '/cost', icon: '💰', label: '成本与配额' },
  { to: '/settings', icon: '⚙️', label: '设置' },
]

function isActive(item: NavItem) {
  return route.path.startsWith(item.match || item.to)
}
</script>

<style scoped>
.bottom-nav {
  position: fixed;
  bottom: 0;
  left: 0;
  right: 0;
  height: var(--bottomnav-height);
  /* Respect the home indicator on notched devices. */
  padding-bottom: env(safe-area-inset-bottom, 0);
  background: var(--bg-card);
  border-top: 1px solid var(--border);
  display: flex;
  align-items: stretch;
  justify-content: space-around;
  z-index: var(--z-bottom-nav);
}

.nav-item {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 2px;
  text-decoration: none;
  color: var(--text-secondary);
  font-size: var(--text-xs);
  padding: var(--space-1);
  transition: color var(--duration-fast) var(--ease-out);
  /* Touch-friendly tap target height. */
  min-height: 44px;
}

.nav-item.active {
  color: var(--brand-primary);
  font-weight: var(--font-weight-semibold);
}

.nav-item:active {
  opacity: 0.7;
}

.icon {
  font-size: 18px;
  line-height: 1;
}

.label {
  font-size: var(--text-xs);
  line-height: 1;
}

.more-sheet {
  position: fixed;
  inset: 0;
  background: var(--color-bg-overlay, rgba(0, 0, 0, 0.4));
  display: flex;
  align-items: flex-end;
  z-index: var(--z-sheet);
  /* Higher than TasksView FAB (z-30) so the sheet is never covered. */
}

.more-panel {
  width: 100%;
  background: var(--bg-card);
  border-radius: var(--radius-lg) var(--radius-lg) 0 0;
  padding: var(--space-4);
  /* Extra bottom padding so FAB and home indicator don't overlap tiles. */
  padding-bottom: calc(var(--space-4) + env(safe-area-inset-bottom, 0px) + 60px);
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: var(--space-3);
  max-height: 70vh;
  overflow-y: auto;
  overscroll-behavior: contain;
  animation: sheet-up var(--duration-base) var(--ease-spring);
}

@keyframes sheet-up {
  from { transform: translateY(100%); }
  to { transform: translateY(0); }
}

.more-item {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-1);
  text-decoration: none;
  color: var(--text-primary);
  font-size: var(--text-sm);
  padding: var(--space-3);
  border-radius: var(--radius-md);
  background: var(--bg-subtle);
  transition: background var(--duration-fast) var(--ease-out);
}

.more-item:active {
  background: var(--bg-elevated);
}

.more-item[aria-current='page'] {
  outline: 2px solid var(--brand-primary);
  outline-offset: -2px;
}

.more-item .icon {
  font-size: 22px;
}
</style>
