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
    <a href="#main" class="skip-link" @click.prevent="focusMain">{{ t('layout.skipToMain') }}</a>

    <header v-if="showTopBar" class="top-bar" role="banner">
      <button
        v-if="showMenuButton && !canGoBack"
        class="menu-btn"
        type="button"
        :aria-label="t('layout.openMenu')"
        aria-haspopup="dialog"
        :aria-expanded="menuOpen"
        @click="menuOpen = true"
      >
        <span class="material-symbols-outlined" aria-hidden="true">menu</span>
      </button>
      <button
        v-if="canGoBack"
        class="back-btn"
        type="button"
        :aria-label="t('layout.backButton')"
        @click="goBack"
      >
        <span class="material-symbols-outlined" aria-hidden="true">arrow_back</span>
      </button>
      <h1 class="title">{{ title }}</h1>
      <!-- 页面经 HeaderActionsPortal 注入的标题栏右侧操作区（编辑/保存/筛选等）。
           与 ScrollChromePortal 同构，消灭 AgentDetail/Edit、CostQuota、MeetingRecord
           里的双层标题栏。 -->
      <div id="app-header-actions" class="header-actions"></div>
      <slot name="actions" />
    </header>

    <!-- 全局状态条：连接 / 同步 / 离线队列（08 §2.2，不用只显示 Toast）。 -->
    <GlobalStatusBar v-if="showTopBar" />

    <!-- ScrollChromePortal 的 Teleport 目标（页级搜索工具栏等 chrome 注入点）。
         8ddbc43 引入后某次 AppLayout 重构将其丢失，sessions/email/instances/
         meetings/vault 五个视图的工具栏随之静默不渲染——P1.5+ 轮修复恢复。 -->
    <div v-if="showTopBar" id="app-chrome-sub" class="chrome-sub"></div>

    <main
      id="main"
      ref="mainEl"
      class="content"
      role="main"
      :class="[
        `scroll-${scrollMode}`,
        { 'has-bottom-nav': showBottomNav, fullscreen: isFullscreen },
      ]"
      :aria-label="title"
      tabindex="-1"
    >
      <slot />
    </main>

    <!-- 统一菜单抽屉：所有页面左 ≡ 都打开这个（业界惯例：账户 + 设置入口集中）。
         meta.menu=false 的页面（设置详情、登录等）隐藏触发按钮。 -->
    <SettingsMenuDrawer v-if="showMenuButton" v-model="menuOpen" />

    <BottomNav v-if="showBottomNav" />
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch, nextTick, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { App as CapApp } from '@capacitor/app'
import { Capacitor } from '@capacitor/core'
import BottomNav from '../components/BottomNav.vue'
import GlobalStatusBar from '../components/GlobalStatusBar.vue'
import SettingsMenuDrawer from '../components/base/SettingsMenuDrawer.vue'
import { useBreakpoint } from '../composables/useBreakpoint'
import { useDevicePosture } from '../composables/useDevicePosture'

const { t } = useI18n()

const route = useRoute()
const router = useRouter()
const { isFoldableExpanded } = useBreakpoint()
const { hingeRect, hingeOrientation } = useDevicePosture()

const mainEl = ref<HTMLElement | null>(null)
const menuOpen = ref(false)

/* 测试钩子（仅 dev）：允许通过 JS 触发菜单打开或 URL 参数 `?openMenu=1` 自动打开。
   用途：模拟器无 UI 自动化时，可用 simctl openurl 打开菜单抽屉验证视觉。 */
if (import.meta.env.DEV && typeof window !== 'undefined') {
  ;(window as unknown as { __openMenu?: () => void }).__openMenu = () => {
    menuOpen.value = true
  }
}

/** URL `#/ai?openMenu=1` 自动打开（仅 dev，便于无 UI 自动化后端时验证）。
    清参必须走 history.replaceState：走 router.replace 会触发下面的
    fullPath watch 把刚打开的抽屉立即关掉。 */
if (import.meta.env.DEV && route.query.openMenu) {
  nextTick(() => {
    const [hashPath, hashQuery = ''] = window.location.hash.slice(1).split('?')
    const params = new URLSearchParams(hashQuery)
    params.delete('openMenu')
    const clean = params.toString()
    window.history.replaceState(null, '', `#${hashPath}${clean ? `?${clean}` : ''}`)
    menuOpen.value = true
  })
}

const title = computed(() => (route.meta.title as string) || 'OpenCode Pocket')

/* Android 系统返回：抽屉开着时先关抽屉，而不是把返回事件交给 WebView
   （默认行为会导航后退甚至退出应用，抽屉仍留在屏幕上）。仅原生壳生效。 */
if (Capacitor.isNativePlatform()) {
  const backSub = CapApp.addListener('backButton', () => {
    if (menuOpen.value) {
      menuOpen.value = false
      return
    }
    // 有历史则后退；栈空（根页面）时退出应用——接管了 backButton 就必须
    // 兜底 Capacitor 被覆盖的默认退出行为，否则用户无法退出。
    if (window.history.state?.back == null) {
      void CapApp.exitApp()
    } else {
      window.history.back()
    }
  })
  onUnmounted(() => {
    void backSub.then(h => h.remove()).catch(() => {})
  })
}
// hideAppHeader：视图自带全屏头部（会话工作台等）时隐藏壳层顶栏与全局状态条，
// 避免与视图头部双层堆叠（P1.5 界面减负；meta 契约此前只被 ScrollChromePortal 消费）。
const showTopBar = computed(
  () => route.meta.showTopBar !== false && route.meta.hideAppHeader !== true,
)
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

type ScrollMode = 'shell' | 'self' | 'split'
const scrollMode = computed<ScrollMode>(() => {
  const mode = route.meta.scrollMode as ScrollMode | undefined
  if (mode === 'self') return 'self'
  if (mode === 'split') return isFoldableExpanded.value ? 'split' : 'shell'
  return 'shell'
})

/**
 * 顶栏左 ≡ 菜单触发按钮：业界惯例（iOS HIG / Material 3）每个主页面都应有菜单入口，
 * 把"账户 / 设置 / 次要功能"集中到 SettingsMenuDrawer。
 * meta.menu=false 的页面（设置详情、登录、服务器选择）隐藏此按钮，避免冗余。
 * 路由切换时关闭抽屉（防止深链打开后旧抽屉卡住）。
 */
const showMenuButton = computed(() => route.meta.menu !== false)
watch(() => route.fullPath, () => { menuOpen.value = false })

/**
 * 全屏自管页（hideAppHeader：会话工作台等自带完整头部/滚动的视图）：
 * 去掉 .content 常规 padding，让视图头部贴住状态栏下方（body padding 之下），
 * 否则 12px 内边距叠加在视图自己的头部上方浪费空间（P1.5+ 真机排查）。
 */
const isFullscreen = computed(() => route.meta.hideAppHeader === true)

/* 折叠屏铰链中线（CSS 变量）：供 SplitLayout 等分屏视图对齐铰链边缘，
   避免内容跨铰链产生可读性损失。 */
const foldHingePx = computed(() => {
  if (hingeOrientation.value !== 'vertical') return null
  return `${hingeRect.value?.x ?? 0}px`
})
watch(foldHingePx, (v) => {
  if (typeof document === 'undefined') return
  if (v) document.documentElement.style.setProperty('--fold-hinge-x', v)
  else document.documentElement.style.removeProperty('--fold-hinge-x')
}, { immediate: true })

/**
 * 返回策略：
 * 1. sessionStorage 标记是否"从首页栈出发"——根路由 `/ai`、`/tasks` 等。
 * 2. 如果当前就是从首页 push 来的（"isHomeRoot = false"），直接 router.back()
 *    会回到首页，结果可预测。
 * 3. 如果当前就在首页根（isHomeRoot = true），router.back() 会落到 entry 空白页，
 *    应保持 push('/ai') 兜底。
 *
 * 这种方式不依赖 window.history.length，避免 Capacitor WebView 启动期
 * length 起点异常与深链入口干扰。
 */
function goBack() {
  const FALLBACK_HOME = '/ai'
  if (typeof sessionStorage === 'undefined') {
    router.push(FALLBACK_HOME)
    return
  }
  const cameFromHome = sessionStorage.getItem('pocket:navigatedFromHome') === '1'
  if (cameFromHome) {
    sessionStorage.removeItem('pocket:navigatedFromHome')
    router.back()
  } else {
    router.push(FALLBACK_HOME)
  }
}

function focusMain() {
  // Move focus to <main> so the skip link lands keyboard users at content.
  mainEl.value?.focus()
  mainEl.value?.scrollTo({ top: 0 })
}
</script>

<style scoped>
.app-layout {
  height: 100%;
  min-height: 0;
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
  /* 安全区唯一来源是 body 的 padding-top（styles.css）；此处再加会双重下移
     （真机实测标题被压低 39px，P1.5+ 排查）。 */
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
  position: sticky;
  top: 0;
  z-index: var(--z-sticky);
}

.back-btn {
  width: 44px;
  height: 44px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-primary);
  border-radius: var(--radius-full);
  line-height: 1;
  transition: background var(--duration-fast) var(--ease-out);
}

.back-btn .material-symbols-outlined {
  font-size: 22px;
}

.back-btn:active {
  background: var(--bg-subtle);
}

/* 顶栏左 ≡：与 back-btn 同尺寸，业界惯例菜单入口。 */
.menu-btn {
  width: 44px;
  height: 44px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--text-primary);
  background: transparent;
  border: none;
  border-radius: var(--radius-full);
  cursor: pointer;
  flex-shrink: 0;
  transition: background var(--duration-fast) var(--ease-out);
}

.menu-btn:active {
  background: var(--bg-subtle);
}

.menu-btn .material-symbols-outlined {
  font-size: 22px;
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

/* 页面注入的右侧操作容器：横向排布，与 back-btn 同侧对齐 */
.header-actions {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  flex-shrink: 0;
}

.header-actions:empty {
  display: none;
}

/* 页面经 HeaderActionsPortal 注入的按钮统一外观（scoped 穿透 teleport 内容） */
:deep(.header-actions > *) {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 44px;
  height: 44px;
  padding: 0 var(--space-2);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  background: transparent;
  border: none;
  font-size: var(--text-md);
  font-weight: var(--font-weight-medium);
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-out);
  white-space: nowrap;
}

:deep(.header-actions > *:active) {
  background: var(--bg-subtle);
}

:deep(.header-actions > button:disabled) {
  opacity: 0.5;
}

:deep(.header-actions .material-symbols-outlined) {
  font-size: 22px;
  line-height: 1;
}

.content {
  flex: 1 1 auto;
  width: 100%;
  min-height: 0;
  overflow-x: hidden;
  overflow-y: auto;
  -webkit-overflow-scrolling: touch;
  /* Large screens (foldable expanded / tablet): center and cap width. */
  max-width: var(--content-max, 100%);
  margin: 0 auto;
  padding: var(--space-3);
  /* main is the scroll container for focus management. */
  outline: none;
}

.content.has-bottom-nav {
  padding-bottom: calc(var(--bottom-chrome-height) + var(--space-3));
}

.content.scroll-self,
.content.scroll-split,
.content.fullscreen {
  overflow-y: hidden;
}

/* chrome 注入点为空时不占位（无 chrome 的页面零成本）。 */
.chrome-sub:empty {
  display: none;
}

/* 全屏自管页：视图自带头部与滚动，取消常规内容内边距（见 isFullscreen 注释）。 */
.content.fullscreen {
  padding: 0;
  display: flex;
  flex-direction: column;
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
