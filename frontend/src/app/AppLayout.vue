<!--
  AppLayout — shared shell that replaces the per-view duplicated top bar
  and bottom nav. Wraps <router-view/> with TopBar + BottomNav. Individual
  feature views set their title via the route meta or the setHeader event.

  This is the single source of truth for navigation; new modules only add
  an entry to BottomNav.vue and a route, not a copy of the markup.
-->
<template>
  <div class="app-layout">
    <header v-if="showTopBar" class="top-bar">
      <button v-if="canGoBack" class="back-btn" @click="goBack">←</button>
      <h1 class="title">{{ title }}</h1>
      <slot name="actions" />
    </header>

    <main class="content" :class="{ 'has-bottom-nav': showBottomNav }">
      <slot />
    </main>

    <BottomNav v-if="showBottomNav" />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import BottomNav from '../components/BottomNav.vue'

const route = useRoute()
const router = useRouter()

const title = computed(() => (route.meta.title as string) || 'OpenCode Pocket')
const showTopBar = computed(() => route.meta.showTopBar !== false)
const showBottomNav = computed(() => route.meta.bottomNav !== false)
const canGoBack = computed(() => Boolean(route.meta.canGoBack))

function goBack() {
  if (window.history.length > 1) router.back()
  else router.push('/ai')
}
</script>

<style scoped>
.app-layout {
  min-height: 100vh;
  width: 100%;
  background: var(--bg-base);
  color: var(--text-primary);
  display: flex;
  flex-direction: column;
  /* 安全区域：折叠屏/刘海屏铺满且不被遮挡 */
  padding-left: env(safe-area-inset-left, 0);
  padding-right: env(safe-area-inset-right, 0);
}
.top-bar {
  height: 48px;
  display: flex;
  align-items: center;
  gap: var(--space-2-5);
  padding: 0 var(--space-3);
  padding-top: env(safe-area-inset-top, 0);
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
  position: sticky;
  top: 0;
  z-index: 10;
}
.back-btn {
  background: none;
  border: none;
  font-size: 20px;
  color: var(--text-primary);
  cursor: pointer;
  padding: var(--space-1) var(--space-2);
}
.title {
  flex: 1;
  font-size: 16px;
  font-weight: 600;
  margin: 0;
}
.content {
  flex: 1;
  width: 100%;
  /* 大屏（折叠展开/平板）居中限宽，避免内容被拉得过宽难读 */
  max-width: var(--content-max, 100%);
  margin: 0 auto;
  padding: var(--space-3);
}
.content.has-bottom-nav {
  padding-bottom: calc(56px + var(--space-3));
}

/* 折叠屏展开态 / 平板（≥840px）：内容区放宽，列表类页面切双列 */
@media (min-width: 840px) {
  .app-layout { --content-max: 1100px; }
  .content { padding: var(--space-4) var(--space-5); }
  /* 通用列表双列：笔记/邮件/会议/联系人列表在大屏两列铺满 */
  .content :is(.note-list, .meeting-list, .meetings-page .meeting-list, .contact-list, .email-list) {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: var(--space-3);
  }
}
@media (min-width: 1280px) {
  .app-layout { --content-max: 1320px; }
  .content :is(.note-list, .meeting-list, .contact-list, .email-list) {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}
</style>
