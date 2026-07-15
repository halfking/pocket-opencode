<!--
  AppLayout — shared shell: scroll-linked chrome hide (title + sub-toolbar),
  BottomNav, and main scroll area.
-->
<template>
  <div
    class="app-layout"
    :class="{ 'no-chrome': hideAppHeader, 'chrome-snapping': chrome.snapping }"
    :style="layoutStyle"
    @click.capture="onGlobalTapToggle"
  >
    <div
      v-if="!hideAppHeader"
      ref="chromeShellRef"
      class="chrome-shell"
    >
      <header ref="headerRef" class="top-bar">
        <button v-if="canGoBack" class="back-btn" @click="goBack">←</button>
        <h1 class="title">{{ title }}</h1>
        <slot name="actions" />
      </header>
      <div id="app-chrome-sub" ref="chromeSubRef" class="chrome-sub"></div>
    </div>

    <main
      ref="scrollRef"
      class="content"
      :class="{ 'has-bottom-nav': showBottomNav, 'full-bleed': hideAppHeader }"
      :style="contentStyle"
      @scroll="onMainScrollFixed"
    >
      <slot />
    </main>

    <BottomNav v-if="showBottomNav" ref="bottomNavRef" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, provide, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import BottomNav from '../components/BottomNav.vue'
import { SCROLL_CHROME_KEY, isChromeToggleTap } from '../composables/scroll-chrome'
import { createScrollHideChrome } from '../composables/useScrollHideChrome'

const route = useRoute()
const router = useRouter()

const title = computed(() => (route.meta.title as string) || 'OpenCode Pocket')
const showBottomNav = computed(() => route.meta.bottomNav !== false)
const canGoBack = computed(() => Boolean(route.meta.canGoBack))
const hideAppHeader = computed(() => route.meta.hideAppHeader === true)
const scrollChromeEnabled = computed(
  () => !hideAppHeader.value && route.meta.hideScrollChrome !== true,
)

const chromeShellRef = ref<HTMLElement | null>(null)
const headerRef = ref<HTMLElement | null>(null)
const chromeSubRef = ref<HTMLElement | null>(null)
const scrollRef = ref<HTMLElement | null>(null)
const bottomNavRef = ref<InstanceType<typeof BottomNav> | null>(null)

const headerHeight = ref(48)
const subChromeHeight = ref(0)
const bottomNavHeight = ref(56)
const chromeTotalHeight = computed(() => headerHeight.value + subChromeHeight.value)

const maxHideOffset = computed(() => {
  if (!scrollChromeEnabled.value) return 0
  let max = chromeTotalHeight.value
  if (showBottomNav.value) max = Math.max(max, bottomNavHeight.value)
  return max
})

const chrome = createScrollHideChrome(() => maxHideOffset.value)

const topHiddenPx = computed(() =>
  Math.min(chrome.hiddenOffset.value, chromeTotalHeight.value),
)
const bottomHiddenPx = computed(() =>
  showBottomNav.value
    ? Math.min(chrome.hiddenOffset.value, bottomNavHeight.value)
    : 0,
)

const contentStyle = computed(() => {
  if (hideAppHeader.value) return undefined
  return { paddingTop: `${chromeTotalHeight.value}px` }
})

const layoutStyle = computed(() => ({
  '--bottom-chrome-hide': `${bottomHiddenPx.value}px`,
  '--top-chrome-hide': `${topHiddenPx.value}px`,
}))

let mainLastTop = 0
function onMainScrollFixed() {
  if (!scrollChromeEnabled.value || !scrollRef.value) return
  const el = scrollRef.value
  const delta = el.scrollTop - mainLastTop
  mainLastTop = el.scrollTop
  chrome.reportScroll({ scrollTop: el.scrollTop, delta })
}

function onGlobalTapToggle(e: MouseEvent) {
  if (!showBottomNav.value || !scrollChromeEnabled.value) return
  if (!isChromeToggleTap(e.target as HTMLElement)) return
  chrome.toggle()
}

provide(SCROLL_CHROME_KEY, {
  ...chrome,
  chromeTotalHeight,
  bottomNavHeight,
  enabled: scrollChromeEnabled,
  topHiddenPx,
  bottomHiddenPx,
})

function goBack() {
  if (window.history.length > 1) router.back()
  else router.push('/ai')
}

let subObserver: ResizeObserver | null = null
let headerObserver: ResizeObserver | null = null
let navObserver: ResizeObserver | null = null

function measureHeights() {
  headerHeight.value = headerRef.value?.offsetHeight ?? 48
  subChromeHeight.value = chromeSubRef.value?.offsetHeight ?? 0
  const navEl = bottomNavRef.value?.$el as HTMLElement | undefined
  bottomNavHeight.value = navEl?.offsetHeight ?? 56
}

watch(
  () => route.fullPath,
  () => {
    chrome.reset()
    mainLastTop = 0
    if (scrollRef.value) scrollRef.value.scrollTop = 0
  },
)

onMounted(() => {
  measureHeights()

  if (headerRef.value) {
    headerObserver = new ResizeObserver(measureHeights)
    headerObserver.observe(headerRef.value)
  }
  if (chromeSubRef.value) {
    subObserver = new ResizeObserver(measureHeights)
    subObserver.observe(chromeSubRef.value)
  }
  const navEl = bottomNavRef.value?.$el as HTMLElement | undefined
  if (navEl) {
    navObserver = new ResizeObserver(measureHeights)
    navObserver.observe(navEl)
  }
  measureHeights()
})

onUnmounted(() => {
  headerObserver?.disconnect()
  subObserver?.disconnect()
  navObserver?.disconnect()
})
</script>

<style scoped>
.app-layout {
  position: fixed;
  inset: 0;
  background: var(--bg-base);
  color: var(--text-primary);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.app-layout.no-chrome .content {
  padding-top: 0 !important;
}

.chrome-shell {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  z-index: 20;
  will-change: transform;
  background: var(--bg-card);
  transform: translate3d(0, calc(-1 * var(--top-chrome-hide, 0px)), 0);
}

.app-layout.chrome-snapping .chrome-shell,
.app-layout.chrome-snapping :deep(.bottom-nav) {
  transition: transform 280ms cubic-bezier(0.32, 0.72, 0, 1);
}

.top-bar {
  height: var(--topbar-height);
  display: flex;
  align-items: center;
  gap: var(--space-2-5);
  padding: 0 var(--space-3);
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
}

.chrome-sub:empty {
  display: none;
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
  min-height: 0;
  overflow-y: auto;
  -webkit-overflow-scrolling: touch;
  padding: var(--space-3);
}

.content.has-bottom-nav {
  padding-bottom: calc(var(--bottomnav-height) + var(--space-3));
}

.content.full-bleed {
  padding: 0;
  overflow: hidden;
}
</style>
