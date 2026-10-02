<!--
  BottomNav — 全局底部导航（AppLayout 渲染）。

  2026-10-03 全局 IA 重组（本轮）：
  - 4+1 里的「会议」被撤掉，会议作为**笔记的一种来源**下沉到 NotesHubView。
    理由：会议跑完的产出物就是一份纪要，用户接着做的是「读 / 改 / 派生任务」，
    与手记同构。同一种对象的两个阶段各占一个 tab，等于逼用户先判断
    「这次记的东西算会议还是算笔记」才能决定点哪个。
  - 原「学习」tab 被「笔记」顶替，学习降级为「更多」宫格里的一个入口（/study）。
  - 新增「消息」tab：把原先散在三处的待处理输入（重要邮件 /rss /email、
    订阅新闻 /rss、任务与系统消息 /notifications）合并成一条统一时间线。
  - 最终 4 个一级目的地：首页 (/ai) · 笔记 (/notes) · 消息 (/messages) · 更多 (/more)。
    次要功能全部从 MoreHubView 进（1 跳直访）。

  历史：
  - 2026-09-23 TabBar 4+1 重组：首页 (/ai) · 学习 (/study) · 会议 (/meetings) · 更多 (/more)。
  - P2 设计轮（2026-08-28）：图标换 Material Symbols + M3 pill 激活态。
  - 2026-09-05 入口收敛：原「更多」Tab 移除，次要功能入 SettingsMenuDrawer。
  - 2026-09-23：抽屉收编为「MoreHubView」页面（1 跳直访 vs 原 2 跳抽屉）。

  Accessibility:
  - <nav aria-label="主导航"> 包裹整条。
  - 每个 <router-link> 激活时 aria-current="page"。
  - 「消息」的未读角标带 sr-only 文本，光标用户能听到「N 条未读」，
    不能只靠一个红点。
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
      @click="haptic('light')"
    >
      <span class="icon-pill" aria-hidden="true">
        <span class="material-symbols-outlined icon">{{ item.icon }}</span>
        <span v-if="item.to === '/messages' && messageUnread > 0" class="nav-badge">
          {{ messageUnread > 99 ? '99+' : messageUnread }}
        </span>
      </span>
      <span class="label">{{ item.label }}</span>
      <!-- 角标对光标用户不可见（icon-pill 是 aria-hidden），补一份口播。 -->
      <span v-if="item.to === '/messages' && messageUnread > 0" class="sr-only">
        {{ t('messagesHub.unreadBadge', { count: messageUnread }) }}
      </span>
    </router-link>
  </nav>
</template>

<script setup lang="ts">
import { computed, inject, onMounted, onUnmounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { SCROLL_CHROME_KEY } from '../composables/scroll-chrome'
import type { IconName } from '../constants/icons'
import { haptic } from '../composables/useHaptics'
import { useNotificationStore } from '../stores/notification'

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

interface NavItem { to: string; icon: IconName; label: string; match?: string }

/**
 * TabBar 一级目的地（2026-10-03 全局 IA 重组后）：
 * - /ai       首页：AI 任务指挥中心（TasksView）
 * - /notes    笔记：手记 + 会议纪要 + PKM 统一流（NotesHubView）
 * - /messages 消息：重要邮件 + 订阅新闻 + 任务消息统一时间线（MessagesHubView）
 * - /more     更多：次要功能聚合（MoreHubView），学习（/study）在其宫格内
 */
const items: NavItem[] = [
  { to: '/ai', icon: 'home', label: t('nav.home'), match: '/ai' },
  { to: '/notes', icon: 'edit_note', label: t('nav.notes'), match: '/notes' },
  { to: '/messages', icon: 'notifications', label: t('nav.messages'), match: '/messages' },
  { to: '/more', icon: 'apps', label: t('nav.more'), match: '/more' },
]

/**
 * 「消息」未读角标。
 *
 * 只读 notification store 的本地 inbox，**不额外发请求**——底部导航是常驻壳层，
 * 每秒级重新拉三个来源会把省电和流量打穿。代价是：用户停在别的 tab 时，
 * 角标要等 main.ts 的 WS 推送 / 增量同步到达才更新。这与顶栏铃铛
 * （AppLayout 用的是同一份 unreadCount）行为一致，不引入第二套真相。
 */
const notificationStore = useNotificationStore()
const messageUnread = computed(() => notificationStore.unreadCount)

function isActive(item: NavItem) {
  // /more 不与子路由都高亮「更多」——子路由在 own page 自己强调。
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
  /* 6 个 Tab 在 360dp 宽机型上平均只剩 60px，而 .icon-pill(56px)+padding(8px)
     构成 64px 的 flex 最小内容宽；不置 0 的话 flex 项无法收缩，最后一个
     「邮箱」Tab 会被挤出屏幕右缘（真机 Redmi 14R 5G / 360dp 实测 navScrollW 384 > 360）。 */
  min-width: 0;
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
  /* 窄屏下让 pill 跟着 Tab 宽度收缩，不反过来把 Tab 撑出屏幕 */
  max-width: 100%;
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

/* 未读角标：贴在 pill 右上角外侧。
   放在 pill **内部**会把它挤到「激活态」的对称关系之外（图标偏左、角标偏右），
   视觉上像是两个元素而不是一个图标带一个标记。 */
.nav-badge {
  position: absolute;
  top: -3px;
  right: 6px;
  min-width: 15px;
  padding: 0 4px;
  border-radius: var(--radius-full);
  background: var(--danger);
  color: #fff;
  font-size: 9px;
  line-height: 15px;
  text-align: center;
  font-variant-numeric: tabular-nums;
  box-shadow: 0 0 0 1.5px var(--bg-card);
}

.icon-pill {
  position: relative;
}

/* 只给读屏器的口播文本。仓库里另有一份同名规则，但它在
   PromptOptimizeField 的 <style scoped> 里 —— scoped 样式不跨组件生效，
   这里必须自己声明一份，不能指望继承。 */
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}

.nav-item:active .icon-pill {
  transform: scale(0.92);
}

.label {
  font-size: var(--text-2xs);
  line-height: 1;
  letter-spacing: 0.2px;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.nav-item.active .label {
  font-weight: var(--font-weight-semibold);
}
</style>
