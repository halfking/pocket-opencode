<!--
  MoreHubView — 「更多」tab 聚合页面（2026-09-23 TabBar 4+1 重组 Phase 1）。

  设计动机：
  - iOS HIG / Material 3 推荐 3-5 tab；openpocket 原 6 tab 偏多。
  - 次要功能（Email / RSS / Vault / Settings ...）不再藏在左滑抽屉（2 跳），
    而是 1 跳直访的 9 宫格页面。
  - 「设置与运维」分组与「主功能」分组视觉分割；用户感知层次更清晰。

  路由：`/more`
  进入：BottomNav 4 tab 的「更多」入口。
-->
<template>
  <div class="more-hub">
    <!-- 用户卡片：账户入口 -->
    <button class="user-card" type="button" @click="go('/settings')">
      <span class="user-avatar" aria-hidden="true">
        <span class="material-symbols-outlined">account_circle</span>
      </span>
      <span class="user-info">
        <span class="user-name">{{ userName || t('settingsMenu.notLoggedIn') }}</span>
        <span class="user-action">{{ t('settingsMenu.viewAccount') }}</span>
      </span>
      <span class="material-symbols-outlined user-chevron" aria-hidden="true">chevron_right</span>
    </button>

    <!-- 主功能 9 宫格 -->
    <section class="grid-section">
      <h2 class="grid-title">{{ t('nav.moreFeatures') }}</h2>
      <ul class="grid">
        <li v-for="item in mainFeatures" :key="item.to">
          <button class="grid-cell" type="button" @click="go(item.to)">
            <span class="cell-icon" aria-hidden="true">
              <span class="material-symbols-outlined">{{ item.icon }}</span>
            </span>
            <span class="cell-label">{{ item.label }}</span>
          </button>
        </li>
      </ul>
    </section>

    <!-- 设置与运维分组 -->
    <section class="grid-section">
      <h2 class="grid-title">{{ t('settingsMenu.groupOps') }}</h2>
      <ul class="grid ops">
        <li v-for="item in opsFeatures" :key="item.to">
          <button class="grid-cell compact" type="button" @click="go(item.to)">
            <span class="cell-icon" aria-hidden="true">
              <span class="material-symbols-outlined">{{ item.icon }}</span>
            </span>
            <span class="cell-label">{{ item.label }}</span>
            <span class="material-symbols-outlined cell-chevron" aria-hidden="true">chevron_right</span>
          </button>
        </li>
      </ul>
    </section>

    <p class="version-foot">{{ t('settingsMenu.versionFootnote', { version }) }}</p>
  </div>
</template>

<script setup lang="ts">
/**
 * MoreHubView —— 「更多」tab 内容。
 *
 * 9 宫格主功能 + 设置与运维分组（列表形态）。
 * 点击直接 push 路由（与原抽屉 setTimeout 120ms 相比省 1 跳 + 0 延迟）。
 *
 * 历史：原本这些入口在 SettingsMenuDrawer 中；
 *       2026-09-23 TabBar 4+1 重组 → 迁到本页。
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAuthStore } from '../../stores/auth'
import type { IconName } from '../../constants/icons'
import { APP_VERSION } from '../../utils/version'

defineOptions({ name: 'MoreHubView' })

const { t } = useI18n()
const router = useRouter()
const auth = useAuthStore()
const userName = computed(() => auth.user)
const version = computed(() => APP_VERSION.version)

interface HubItem { to: string; icon: IconName; label: string }

/* 主功能（9 宫格）。顺序按用户高频到低频排。 */
const mainFeatures = computed<HubItem[]>(() => [
  { to: '/ai-chat', icon: 'forum', label: t('nav.aiChat') },
  { to: '/pkm/today', icon: 'sticky_note_2', label: t('nav.pkmNotes') },
  { to: '/email', icon: 'mail', label: t('nav.email') },
  { to: '/rss', icon: 'rss_feed', label: t('nav.rss') },
  { to: '/vault', icon: 'lock', label: t('nav.vault') },
  // BUG-Q（2026-09-30 可达性全量对账）：这里原来写的是 '/scheduled-tasks'，
  // 而路由表里根本没有这个路径 —— 只有 '/settings/scheduled-tasks'。
  // 也就是说「定时自动化」这个入口点进去是**未匹配路由**，用户看到空白页或 404。
  //
  // 这类和 BUG-P 是同一类问题（入口存在但去不到），但更糟：BUG-P 是少一个入口，
  // BUG-Q 是有一个入口指向虚空。两者都不会被「接口能通 / 路由表里有」的验收抓到。
  { to: '/settings/scheduled-tasks', icon: 'schedule', label: t('routes.scheduledTasks') },
  { to: '/marketplace/skills', icon: 'extension', label: t('nav.skillMarket') },
  { to: '/marketplace/agents', icon: 'smart_toy', label: t('nav.agentMarket') },
  { to: '/local-agent', icon: 'memory', label: t('nav.localAgent') },
  { to: '/marketplace/workbuddies', icon: 'handshake', label: t('nav.workbuddy') },
  // BUG-P（2026-09-30 真机/模拟器验收）：闪卡路由一直存在（/flashcards），
  // BUG-K/L/O 也都修好了，但**这个列表里没有它** —— 也就是说用户在正常 UI 导航下
  // 根本进不去闪卡模块。之前三轮验收全部用 CDP 直接改 location.hash 导航，
  // 绕过了真实入口，所以一直没暴露。
  //
  // 这类缺陷只有**从 UI 入口点进去**才会发现：路由能进 ≠ 用户能到。
  // 加完之后 .maestro/flashcards-write.yaml 才能从「更多」页点进去（该 flow 第一步
  // 就是 tapOn 更多 -> 闪卡）。
  { to: '/flashcards', icon: 'style', label: t('nav.flashcards') },
])

/* 设置与运维分组（横排列表项，每项带 chevron）。 */
const opsFeatures = computed<HubItem[]>(() => [
  { to: '/settings', icon: 'settings', label: t('routes.settings') },
  { to: '/notifications', icon: 'notifications', label: t('routes.notifications') || '通知中心' },
  { to: '/instances', icon: 'memory', label: t('routes.instances') },
  { to: '/sessions', icon: 'forum', label: t('routes.sessions') },
  { to: '/tasks', icon: 'checklist', label: t('routes.tasks') },
  { to: '/cost', icon: 'payments', label: t('routes.costQuota') },
  { to: '/gateway', icon: 'dns', label: t('routes.gatewayNodes') },
  { to: '/servers', icon: 'dns', label: t('routes.selectServer') },
  { to: '/flashcards/io', icon: 'import_export', label: t('flashcards.io.title') },
])

function go(to: string) {
  router.push(to)
}
</script>

<style scoped>
.more-hub {
  display: flex;
  flex-direction: column;
  gap: var(--space-5);
  padding-bottom: var(--space-3);
}

.user-card {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  width: 100%;
  padding: var(--space-3);
  background: var(--bg-subtle);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  text-align: left;
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-out);
}

.user-card:active {
  background: var(--color-bg-hover, rgba(0, 0, 0, 0.04));
}

.user-avatar {
  width: 44px;
  height: 44px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  background: var(--brand-bg, rgba(76, 141, 255, 0.12));
  color: var(--brand-primary, #4c8dff);
  flex-shrink: 0;
}

.user-avatar .material-symbols-outlined { font-size: 28px; }

.user-info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.user-name {
  font-size: var(--text-md);
  font-weight: var(--font-weight-semibold);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.user-action {
  font-size: var(--text-sm);
  color: var(--text-secondary);
}

.user-chevron {
  font-size: 20px;
  color: var(--text-tertiary, var(--text-muted));
  flex-shrink: 0;
}

.grid-section {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.grid-title {
  margin: 0 0 var(--space-1) var(--space-1);
  font-size: var(--text-2xs);
  font-weight: var(--font-weight-semibold);
  text-transform: uppercase;
  letter-spacing: 0.4px;
  color: var(--text-tertiary, var(--text-muted));
}

.grid {
  list-style: none;
  margin: 0;
  padding: var(--space-3);
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: var(--space-3);
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
}

.grid.ops {
  grid-template-columns: 1fr;
  padding: 0;
}

.grid-cell {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-2);
  background: transparent;
  border: none;
  border-radius: var(--radius-md);
  color: var(--text-primary);
  text-align: center;
  cursor: pointer;
  min-height: 80px;
  transition: background var(--duration-fast) var(--ease-out);
}

.grid-cell:active {
  background: var(--color-bg-hover, rgba(0, 0, 0, 0.04));
}

.grid-cell.compact {
  flex-direction: row;
  justify-content: flex-start;
  min-height: 44px;
  padding: var(--space-3);
  border-bottom: 1px solid var(--border);
}

.grid.ops li:last-child .grid-cell {
  border-bottom: none;
}

.cell-icon {
  width: 40px;
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-md);
  background: var(--brand-bg, rgba(76, 141, 255, 0.12));
  color: var(--brand-primary, #4c8dff);
  flex-shrink: 0;
}

.cell-icon .material-symbols-outlined {
  font-size: 22px;
}

.cell-label {
  font-size: var(--text-sm);
  font-weight: var(--font-weight-medium);
  line-height: 1.3;
}

.grid-cell.compact .cell-label {
  flex: 1;
  text-align: left;
  font-size: var(--text-base);
}

.cell-chevron {
  font-size: var(--text-xl);
  color: var(--text-tertiary, var(--text-muted));
}

.version-foot {
  margin: 0;
  padding: var(--space-2) var(--space-1);
  font-size: var(--text-2xs);
  color: var(--text-tertiary, var(--text-muted));
  text-align: center;
}
</style>