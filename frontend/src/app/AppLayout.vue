<!--
  AppLayout — shared shell that replaces the per-view duplicated top bar
  and bottom nav. Wraps <router-view/> with TopBar + BottomNav. Individual
  feature views set their title via the route meta or the setHeader event.

  This is the single source of truth for navigation; new modules only add
  an entry to BottomNav.vue and a route, not a copy of the markup.

  Accessibility:
  - Skip link is the first focusable element so keyboard users can jump
    directly to <main> instead of tabbing through the top bar every page.
  - <header role="banner"> marks the app-level top bar.
  - <main id="main" role="main"> is the single primary landmark per page.
  - <nav> in BottomNav carries aria-label="主导航".
-->
<template>
  <div class="app-layout">
    <a href="#main" class="skip-link" @click.prevent="focusMain">跳到主要内容</a>

    <header v-if="showTopBar" class="top-bar" role="banner">
      <button
        v-if="canGoBack"
        class="back-btn"
        type="button"
        aria-label="返回"
        @click="goBack"
      >
        <span aria-hidden="true">←</span>
      </button>
      <h1 class="title">{{ title }}</h1>
      <slot name="actions" />
    </header>

    <!-- 全局状态条：连接 / 同步 / 离线队列（08 §2.2，不用只显示 Toast）。 -->
    <GlobalStatusBar v-if="showTopBar" />

    <main
      id="main"
      ref="mainEl"
      class="content"
      role="main"
      :class="{ 'has-bottom-nav': showBottomNav }"
      :aria-label="title"
      tabindex="-1"
    >
      <slot />
    </main>

    <BottomNav v-if="showBottomNav" />
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import BottomNav from '../components/BottomNav.vue'
import GlobalStatusBar from '../components/GlobalStatusBar.vue'
import { useBreakpoint } from '../composables/useBreakpoint'

const route = useRoute()
const router = useRouter()
const { isFoldableExpanded } = useBreakpoint()

const mainEl = ref<HTMLElement | null>(null)

const title = computed(() => (route.meta.title as string) || 'OpenCode Pocket')
const showTopBar = computed(() => route.meta.showTopBar !== false)
const showBottomNav = computed(() => {
  if (route.meta.bottomNav === false) return false
  // 平板会话工作台选中 detail 后进入详情态，按 08 §2.2 隐藏底部导航。
  if (
    isFoldableExpanded.value &&
    route.name === 'sessions' &&
    typeof route.query.selected === 'string' &&
    route.query.selected !== ''
  ) {
    return false
  }
  return true
})
const canGoBack = computed(() => Boolean(route.meta.canGoBack))

function goBack() {
  if (window.history.length > 1) router.back()
  else router.push('/ai')
}

function focusMain() {
  // Move focus to <main> so the skip link lands keyboard users at content.
  mainEl.value?.focus()
  mainEl.value?.scrollTo({ top: 0 })
}
</script>

<style scoped>
.app-layout {
  min-height: 100vh;
  min-height: 100dvh;
  width: 100%;
  background: var(--bg-base);
  color: var(--text-primary);
  display: flex;
  flex-direction: column;
  /* Safe area: keep fold/notch screens full-bleed without overlap. */
  padding-left: env(safe-area-inset-left, 0);
  padding-right: env(safe-area-inset-right, 0);
}

/* Skip link — visually hidden until focused via keyboard. */
.skip-link {
  position: absolute;
  left: var(--space-2);
  top: -40px;
  z-index: var(--z-toast);
  padding: var(--space-2) var(--space-3);
  background: var(--brand-primary);
  color: var(--text-inverse);
  border-radius: var(--radius-md);
  font-size: var(--text-sm);
  font-weight: var(--font-weight-semibold);
  text-decoration: none;
  transition: top var(--duration-fast) var(--ease-out);
}

.skip-link:focus,
.skip-link:focus-visible {
  top: var(--space-2);
  outline: 2px solid var(--text-inverse);
  outline-offset: 2px;
}

.top-bar {
  height: var(--topbar-height);
  display: flex;
  align-items: center;
  gap: var(--space-2-5);
  padding: 0 var(--space-3);
  padding-top: env(safe-area-inset-top, 0);
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
  position: sticky;
  top: 0;
  z-index: var(--z-sticky);
}

.back-btn {
  font-size: 20px;
  color: var(--text-primary);
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-sm);
  line-height: 1;
  transition: background var(--duration-fast) var(--ease-out);
}

.back-btn:active {
  background: var(--bg-subtle);
}

.title {
  flex: 1;
  font-size: var(--text-lg);
  font-weight: var(--font-weight-semibold);
  margin: 0;
  color: var(--text-primary);
  /* Allow long titles to ellipsize instead of wrapping. */
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.content {
  flex: 1;
  width: 100%;
  /* Large screens (foldable expanded / tablet): center and cap width. */
  max-width: var(--content-max, 100%);
  margin: 0 auto;
  padding: var(--space-3);
  /* main is the scroll container for focus management. */
  outline: none;
}

.content.has-bottom-nav {
  padding-bottom: calc(var(--bottomnav-height) + var(--space-3));
}

/* Foldable expanded / tablet (≥840px): widen content and switch lists to 2-col. */
@media (min-width: 840px) {
  .app-layout {
    --content-max: 1100px;
  }
  .content {
    padding: var(--space-4) var(--space-5);
  }
  .content :is(.note-list, .meeting-list, .meetings-page .meeting-list, .contact-list, .email-list) {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: var(--space-3);
  }
}

@media (min-width: 1280px) {
  .app-layout {
    --content-max: 1320px;
  }
  .content :is(.note-list, .meeting-list, .contact-list, .email-list) {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}
</style>
