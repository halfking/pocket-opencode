<!--
  AppLayout — shared shell that replaces the per-view duplicated top bar
  and bottom nav. Wraps <router-view/> with TopBar + BottomNav. Individual
  feature views set their title via the route meta or the setHeader event.

  This is the single source of truth for navigation; new modules only add
  an entry to BottomNav.vue and a route, not a copy of the markup.
-->
<template>
  <div class="app-layout">
    <header class="top-bar">
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
  background: var(--bg-base);
  color: var(--text-primary);
  display: flex;
  flex-direction: column;
}
.top-bar {
  height: 48px;                    /* 修改：52px → 48px */
  display: flex;
  align-items: center;
  gap: var(--space-2-5);           /* 修改：space-3 → space-2-5 (10px) */
  padding: 0 var(--space-3);       /* 修改：space-4 → space-3 (12px) */
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
  font-size: 16px;                 /* 修改：17px → 16px */
  font-weight: 600;
  margin: 0;
}
.content {
  flex: 1;
  padding: var(--space-3);         /* 修改：space-4 → space-3 (12px) */
}
.content.has-bottom-nav {
  padding-bottom: calc(56px + var(--space-3)); /* 修改：60px → 56px, space-4 → space-3 */
}
</style>
