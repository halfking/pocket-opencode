<!--
  BottomNav — 全局底部导航（AppLayout 渲染）。
  P2 设计轮专业化重做（2026-08-28）：
  - 图标体系从 emoji 切换到 Material Symbols 自托管子集（与全 App 图标
    语言统一；emoji 各机型字形不一致是"粗制滥造"观感的主因之一）；
  - 激活态采用 Material 3 NavigationBar 惯例：品牌色图标 + pill 指示背景
    （56×32 圆角），标签加重；非激活弱化；
  - 「更多」面板：顶部圆角 16px + drag handle + 标题行；支持
    ① 点击 backdrop 空白区关闭 ② 下滑关闭（标准 sheet 手势，阈值 72px
    或速度判定）；左滑非 sheet 惯例，不做（登记设计文档 DD-9）。

  Accessibility:
  - <nav aria-label="主导航"> 包裹整条。
  - 每个 <router-link> 激活时 aria-current="page"。
  - 更多按钮 aria-haspopup + aria-expanded；面板 role="dialog" + aria-modal。
-->
<template>
  <nav class="bottom-nav" :aria-label="t('nav.mainNavigation')">
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

    <button
      class="nav-item"
      :class="{ active: showMore }"
      type="button"
      aria-haspopup="dialog"
      :aria-expanded="showMore"
      @click="showMore = !showMore"
    >
      <span class="icon-pill" aria-hidden="true">
        <span class="material-symbols-outlined icon">more_horiz</span>
      </span>
      <span class="label">{{ t('nav.more') }}</span>
    </button>

    <div
      v-if="showMore"
      class="more-sheet"
      role="dialog"
      aria-modal="true"
      :aria-label="t('nav.moreFeatures')"
      @click.self="closeMore"
    >
      <div
        ref="morePanelEl"
        class="more-panel"
        :style="{ transform: `translateY(${moreDragY}px)` }"
        @touchstart.passive="onDragStart"
        @touchmove.passive="onDragMove"
        @touchend="onDragEnd"
      >
        <div class="drag-handle" aria-hidden="true"></div>
        <div class="more-head">
          <span class="more-title">{{ t('nav.moreFeatures') }}</span>
          <button type="button" class="more-close" :aria-label="t('common.close')" @click="closeMore">
            <span class="material-symbols-outlined">close</span>
          </button>
        </div>
        <div class="more-grid">
          <router-link
            v-for="item in more"
            :key="item.to"
            :to="item.to"
            class="more-item"
            :aria-current="isActive(item) ? 'page' : undefined"
            @click="closeMore"
          >
            <span class="more-icon" aria-hidden="true">
              <span class="material-symbols-outlined">{{ item.icon }}</span>
            </span>
            <span class="more-label">{{ item.label }}</span>
          </router-link>
        </div>
      </div>
    </div>
  </nav>
</template>

<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'

const route = useRoute()
const { t } = useI18n()
const showMore = ref(false)
const morePanelEl = ref<HTMLElement | null>(null)

interface NavItem { to: string; icon: string; label: string; match?: string }

const items: NavItem[] = [
  { to: '/ai', icon: 'smart_toy', label: t('nav.ai'), match: '/ai' },
  { to: '/ai-chat', icon: 'forum', label: t('nav.aiChat'), match: '/ai-chat' },
  { to: '/notes', icon: 'edit_note', label: t('nav.notes'), match: '/notes' },
  { to: '/meetings', icon: 'mic', label: t('nav.meetings'), match: '/meetings' },
  { to: '/email', icon: 'mail', label: t('nav.email'), match: '/email' },
]

const more: NavItem[] = [
  { to: '/pkm/today', icon: 'sticky_note_2', label: t('nav.pkmNotes') },
  { to: '/vault', icon: 'lock', label: t('nav.vault') },
  // 入口收敛：tasks/sessions/instances/cost 已统一到顶栏 ≡ 菜单抽屉「运维与高级」，
  // 不再在「更多」面板重复暴露；设置入口同样收敛到 ≡（tab bar 不放设置）。
  // 各路由本身仍存在（深链可访问）。
]

function isActive(item: NavItem) {
  return route.path.startsWith(item.match || item.to)
}

function closeMore() {
  showMore.value = false
}

// ── 下滑关闭（标准 bottom-sheet 手势）：面板顶部 drag-handle 区或面板任意
//    位置未滚动到顶时下滑超过阈值即关闭。面板内容可滚动，滚动中不打断。
const DRAG_CLOSE_PX = 72
const moreDragY = ref(0)
let dragStartY: number | null = null
let dragScrollTopAtStart: number | null = null

function onDragStart(e: TouchEvent) {
  const t = e.touches[0]
  dragStartY = t.clientY
  dragScrollTopAtStart = morePanelEl.value ? morePanelEl.value.scrollTop : 0
}

function onDragMove(e: TouchEvent) {
  if (dragStartY === null) return
  const t = e.touches[0]
  const dy = t.clientY - dragStartY
  // 仅向下拖（dy>0）跟随且只在面板内容已滚到顶时
  if (dy > 0 && (dragScrollTopAtStart ?? 0) <= 0) {
    moreDragY.value = dy
  }
}

function onDragEnd(e: TouchEvent) {
  if (dragStartY === null) return
  const t = e.changedTouches[0]
  const dy = t.clientY - dragStartY
  dragStartY = null
  dragScrollTopAtStart = null
  if (dy > DRAG_CLOSE_PX) {
    closeMore()
  }
  moreDragY.value = 0
}

// 打开时重置拖拽偏移（面板重新入场动画由 class 切换触发）
watch(showMore, async (v) => {
  if (v) {
    moreDragY.value = 0
    await nextTick()
  }
})
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

/* ── 更多面板：标准 bottom-sheet（圆角 + drag handle + 下滑/点击关闭） ── */
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
  border-radius: 16px 16px 0 0;
  padding: var(--space-2) var(--space-4) var(--space-4);
  /* FAB 与 home indicator 不压 tiles */
  padding-bottom: calc(var(--space-4) + var(--app-safe-bottom) + 60px);
  max-height: 70vh;
  overflow-y: auto;
  overscroll-behavior: contain;
  animation: sheet-up var(--duration-base) var(--ease-spring);
  touch-action: pan-y;
}

@keyframes sheet-up {
  from { transform: translateY(100%); }
  to { transform: translateY(0); }
}

.drag-handle {
  width: 36px;
  height: 4px;
  border-radius: var(--radius-full);
  background: var(--border-strong, var(--border));
  margin: 0 auto var(--space-2);
}

.more-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-3);
}

.more-title {
  font-size: var(--text-md);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
}

.more-close {
  width: 44px;
  height: 44px;
  margin: calc(var(--space-2) * -1);
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-full);
  color: var(--text-secondary);
}

.more-close:active {
  background: var(--bg-subtle);
}

.more-grid {
  display: grid;
  /* 3 列：真机反馈 4 列过挤（图标与标签拥挤），3 列保证 44px 图标底衬
     与标签完整展示（P3 反馈轮） */
  grid-template-columns: repeat(3, 1fr);
  gap: var(--space-2);
}

.more-item {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-1);
  text-decoration: none;
  color: var(--text-primary);
  font-size: var(--text-xs);
  padding: var(--space-3) var(--space-1);
  border-radius: var(--radius-md);
  transition: background var(--duration-fast) var(--ease-out);
}

.more-item:active {
  background: var(--bg-subtle);
}

.more-item[aria-current='page'] {
  outline: 2px solid var(--brand-primary);
  outline-offset: -2px;
}

.more-icon {
  width: 44px;
  height: 44px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-md);
  background: var(--bg-subtle);
  color: var(--brand-primary);
}

.more-icon .material-symbols-outlined {
  font-size: 22px;
}

.more-label {
  color: var(--text-secondary);
}
</style>
