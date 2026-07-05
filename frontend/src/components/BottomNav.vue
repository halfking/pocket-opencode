<!--
  BottomNav — the single navigation bar. Rendered by AppLayout.
  5 primary tabs + a "more" sheet for secondary modules (vault, settings,
  instances, sessions). Active state derives from the current route so we
  no longer hardcode "active" per view.
-->
<template>
  <nav class="bottom-nav">
    <router-link
      v-for="item in items"
      :key="item.to"
      :to="item.to"
      class="nav-item"
      :class="{ active: isActive(item) }"
    >
      <span class="icon">{{ item.icon }}</span>
      <span class="label">{{ item.label }}</span>
    </router-link>

    <button class="nav-item" @click="showMore = !showMore">
      <span class="icon">⋮</span>
      <span class="label">更多</span>
    </button>

    <div v-if="showMore" class="more-sheet" @click.self="showMore = false">
      <div class="more-panel">
        <router-link
          v-for="item in more"
          :key="item.to"
          :to="item.to"
          class="more-item"
          @click="showMore = false"
        >
          <span class="icon">{{ item.icon }}</span>
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
  { to: '/vault', icon: '🔐', label: '密码箱' },
  { to: '/tasks', icon: '📋', label: '任务' },
  { to: '/sessions', icon: '💬', label: '会话' },
  { to: '/instances', icon: '💻', label: '实例' },
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
  background: var(--bg-card);
  border-top: 1px solid var(--border);
  display: flex;
  align-items: center;
  justify-content: space-around;
  z-index: 20;
}
.nav-item {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
  background: none;
  border: none;
  text-decoration: none;
  color: var(--text-secondary);
  font-size: 11px;
  padding: var(--space-1);
  cursor: pointer;
}
.nav-item.active {
  color: var(--brand-primary);
}
.icon { font-size: 18px; line-height: 1; }
.label { font-size: 11px; }
.more-sheet {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.4);
  display: flex;
  align-items: flex-end;
  z-index: 60; /* Phase 6: 高于 TasksView FAB (z-index:50)，避免遮挡 sheet tile */
}
.more-panel {
  width: 100%;
  background: var(--bg-card);
  border-radius: var(--radius-lg) var(--radius-lg) 0 0;
  padding: var(--space-4);
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: var(--space-3);
}
.more-item {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-1);
  text-decoration: none;
  color: var(--text-primary);
  font-size: 12px;
  padding: var(--space-3);
  border-radius: var(--radius-md);
  background: var(--bg-subtle);
}
.more-item .icon { font-size: 22px; }
</style>
