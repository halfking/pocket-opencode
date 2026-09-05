<!--
  BottomNav — 全局底部导航（AppLayout 渲染）。
  P2 设计轮专业化重做（2026-08-28）：
  - 图标体系从 emoji 切换到 Material Symbols 自托管子集（与全 App 图标
    语言统一；emoji 各机型字形不一致是"粗制滥造"观感的主因之一）；
  - 激活态采用 Material 3 NavigationBar 惯例：品牌色图标 + pill 指示背景
    （56×32 圆角），标签加重；非激活弱化；
  - 2026-09-05 入口收敛：移除「更多」Tab 与更多面板，次要功能入口统一
    收敛到顶栏左 ≡ 菜单抽屉（SettingsMenuDrawer「更多功能」组），
    TabBar 只保留 5 个一级目的地。

  Accessibility:
  - <nav aria-label="主导航"> 包裹整条。
  - 每个 <router-link> 激活时 aria-current="page"。
-->
<template>
  <nav
    ref="navEl"
    class="bottom-nav"
    :class="{ snapping: chromeSnapping }"
    :inert="fullyHidden"
    :aria-label="t('nav.mainNavigation')"
  >
    <router-link
      v-for="item in items"
      :key="item.to"
      :to="item.to"
      class="nav-item"
      :class="{ active: isActive(item) }"
      :aria-current="isActive(item) ? 'page' : undefined"
    >
      <span class="icon-pill" aria-hidden="true">
        <span class="material-symbols-outlined icon">{{ item.icon }}</span>
      </span>
      <span class="label">{{ item.label }}</span>
    </router-link>
  </nav>
</template>

<script setup lang="ts">
import { computed, inject, onMounted, onUnmounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { SCROLL_CHROME_KEY } from '../composables/scroll-chrome'

const route = useRoute()
const { t } = useI18n()

/* 滚动联动：向壳层引擎上报自身高度（参与 maxHide），读取吸附态开过渡。
   全隐时 inert——滑出屏幕的导航不应再吃键盘 Tab 焦点（绑定落定态避免
   跟手过程中 hiddenOffset 短暂峰值导致的 inert 闪烁）。 */
const chromeCtx = inject(SCROLL_CHROME_KEY, null)
const navEl = ref<HTMLElement | null>(null)
const chromeSnapping = chromeCtx?.snapping ?? ref(false)
const fullyHidden = computed(() => chromeCtx?.hidden.value ?? false)
let navRO: ResizeObserver | null = null
onMounted(() => {
  const el = navEl.value
  if (!el || !chromeCtx) return
  const measure = () => {
    chromeCtx.bottomNavHeight.value = el.offsetHeight
  }
  measure()
  navRO = new ResizeObserver(measure)
  navRO.observe(el)
})
onUnmounted(() => navRO?.disconnect())

interface NavItem { to: string; icon: string; label: string; match?: string }

const items: NavItem[] = [
  { to: '/ai', icon: 'smart_toy', label: t('nav.ai'), match: '/ai' },
  { to: '/ai-chat', icon: 'forum', label: t('nav.aiChat'), match: '/ai-chat' },
  { to: '/notes', icon: 'edit_note', label: t('nav.notes'), match: '/notes' },
  { to: '/meetings', icon: 'mic', label: t('nav.meetings'), match: '/meetings' },
  { to: '/email', icon: 'mail', label: t('nav.email'), match: '/email' },
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
  height: var(--bottom-chrome-height);
  /* Keep application tabs above Android/iOS system navigation gestures. */
  padding-bottom: var(--app-safe-bottom);
  background: var(--bg-card);
  /* hairline 顶边 + 轻阴影替代生硬边框（M3 elevation 惯例） */
  border-top: 1px solid var(--border);
  box-shadow: 0 -1px 8px rgba(0, 0, 0, 0.04);
  display: flex;
  align-items: stretch;
  justify-content: space-around;
  z-index: var(--z-bottom-nav, 20);
  will-change: transform;
  transform: translate3d(0, var(--bottom-chrome-hide, 0px), 0);
}

/* 滚动吸附阶段的位移过渡（跟手阶段 1:1 无过渡）；曲线与时长见 tokens.css */
.bottom-nav.snapping {
  transition: transform var(--duration-chrome) var(--ease-chrome);
}

.nav-item {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 2px;
  text-decoration: none;
  color: var(--text-muted);
  font-size: var(--text-xs);
  padding: var(--space-1);
  min-height: 44px;
  transition: color var(--duration-fast) var(--ease-out);
}

.nav-item.active {
  color: var(--brand-primary);
}

/* M3 NavigationBar 激活指示：品牌色图标 + pill 背景 */
.icon-pill {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 56px;
  height: 30px;
  border-radius: var(--radius-full);
  transition: background var(--duration-fast) var(--ease-out);
}

.nav-item.active .icon-pill {
  background: var(--brand-bg);
}

.icon {
  font-size: 24px;
  line-height: 1;
}

.nav-item:active .icon-pill {
  transform: scale(0.92);
}

.label {
  font-size: 11px;
  line-height: 1;
  letter-spacing: 0.2px;
}

.nav-item.active .label {
  font-weight: var(--font-weight-semibold);
}
</style>
