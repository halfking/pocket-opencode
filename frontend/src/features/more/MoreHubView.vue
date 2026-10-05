<!--
  MoreHubView — 「更多」tab 聚合页面。

  设计动机：
  - iOS HIG / Material 3 推荐 3-5 tab；一级目的地必须留给**每天都去**的地方。
  - 次要功能（Email / Vault / Settings ...）不再藏在左滑抽屉（2 跳），
    而是 1 跳直访的宫格页面。
  - 「设置与运维」分组与「主功能」分组视觉分割；用户感知层次更清晰。

  2026-10-03 全局 IA 重组：本页在一级导航里的角色从「次要功能的收纳箱」变成
  「学习 + 长尾工具的收纳箱」。一级 tab 收敛为 首页 / 笔记 / 消息 / 更多 四个，
  学习（/study）从一级 tab 降级进本页宫格。

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
        <li v-for="item in reachableMainFeatures" :key="item.to">
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
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAuthStore } from '../../stores/auth'
import { isKeystoreAvailable } from '../../native/keystore'
import { applyCapabilityGates, type HubItem } from './hubItems.ts'
import { APP_VERSION, resolveAppVersion } from '../../utils/version'
import { ICON } from '../../constants/icons'

defineOptions({ name: 'MoreHubView' })

const { t } = useI18n()
const router = useRouter()
const auth = useAuthStore()
const userName = computed(() => auth.user)
const version = ref(APP_VERSION.version)

// HubItem 的定义已收敛到 ./hubItems.ts —— 9 宫格与密码箱门控共用同一份，
// 免得两处各自声明一份、字段一漂移就静默失配。

/* 主功能宫格。顺序按用户高频到低频排。
 *
 * 2026-10-03 全局 IA 重组后的三处调整：
 *  1. **学习（/study）进宫格**——它此前独占一个一级 tab，现在降级到这里。
 *     闪卡用户从「更多 → 学习」进入，路径变长一跳，但换来的是一级 tab 从
 *     4 个里腾出一个位置给「笔记」和「消息」这两个更高频的目的地。
 *  2. **RSS（/rss）从宫格移除**——订阅新闻已经并入「消息」tab 的统一时间线，
 *     在这里再摆一个平行入口会让用户面对两个都能看订阅的地方。
 *     管理订阅源的完整页面从 MessagesHubView 底部直达，1 跳可达。
 *  3. **通知中心（/notifications）从运维分组移除**——它是「消息」时间线里
 *     的一个来源，不再是独立目的地；同一条通知在两处都能进会让"未读"变成
 *     两套互不同步的账。 */
const mainFeatures = computed<HubItem[]>(() => [
  { to: '/study', icon: 'style', label: t('nav.study') },
  { to: '/ai-chat', icon: 'forum', label: t('nav.aiChat') },
  // 「笔记」「消息」**刻意不在宫格里**：它们已经是底部两个一级 tab。
  // 在「更多」里再摆一份，等于同一个目的地有两个入口而两者可能不同步
  // （tab 上有未读角标，宫格那份没有），用户会以为是两个功能。
  // 会议列表同理下沉到这里：它是会议筛选/归档页，不是笔记流的一部分。
  { to: '/meetings', icon: 'mic', label: t('nav.meetings') },
  { to: '/email', icon: 'mail', label: t('nav.email') },
  // 密码箱：受能力门控（见下方 reachableMainFeatures）。留着这行是为了
  // 插件落地后入口自己回来，不是说它在所有平台都可用——别当成漏删的入口。
  { to: '/vault', icon: 'lock', label: t('nav.vault') },
  // BUG-Q（2026-09-30 可达性全量对账）：这里原来写的是 '/scheduled-tasks'，
  // 而路由表里根本没有这个路径 —— 只有 '/settings/scheduled-tasks'。
  // 也就是说「定时自动化」这个入口点进去是**未匹配路由**，用户看到空白页或 404。
  //
  // 这类和 BUG-P 是同一类问题（入口存在但去不到），但更糟：BUG-P 是少一个入口，
  // BUG-Q 是有一个入口指向虚空。两者都不会被「接口能通 / 路由表里有」的验收抓到。
  //
  // 2026-10-06：日历入口从 More 宫格移除，搬进「消息」tab 的「时间线｜日历」分段。
  // 不是「漏删」——是有意收口：同一个功能挂在两个一级入口下，用户会开始怀疑
  // 两边看到的是不是同一份数据（它们确实是，但用户无从判断）。
  // /calendar 路由本身保留，仍可深链直接进月视图。
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

/* 能力门控（2026-10-03）：密码箱在 Android 上永远打不开——原生侧没有
   KeystorePlugin.java，StubKeystore 的 12 个方法全是 reject。把它放在和
   「对话 / 邮箱」平级的位置上，等于给用户一个必然失败的入口。
   这里问的是**真实能力**而不是 security.keystore_v1 那个恒 false 的静态开关：
   插件真落地那天，入口会自己回来，不依赖谁记得改开关。
   null = 探针还没回来（首帧），按不可用处理：先藏后现，好过先闪一个死入口。 */
const vaultUsable = ref<boolean | null>(null)

/** 真正渲染的宫格 = 目录里这个平台确实走得通的那些。 */
const reachableMainFeatures = computed<HubItem[]>(() =>
  applyCapabilityGates(mainFeatures.value, { '/vault': vaultUsable.value })
)

onMounted(async () => {
  version.value = (await resolveAppVersion()).version
  vaultUsable.value = await isKeystoreAvailable()
})

/* 设置与运维分组（横排列表项，每项带 chevron）。 */
const opsFeatures = computed<HubItem[]>(() => [
  { to: '/settings', icon: 'settings', label: t('routes.settings') },
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
  /* ⚠️ 2026-10-03 真机实测修的缺陷：这一页**根本滚不动**，
   * 「运维与高级」分组有 8 项，真机上只有前 2 项露得出来。
   *
   * 证据（Xiaomi 2411DRN47C / 720x1640）：
   *   连续 3 次上滑（三种不同起点与时长），前后截图**逐字节相同**；
   *   截图显示内容明显溢出视口（运维组被底部导航切断，只剩「设置」半行），
   *   而页面纹丝不动。⇒ 会话 / 任务 / 成本与配额 / 网关节点 / 选择服务器 /
   *   导入导出 这 6 个入口对真实用户**永久不可达**（不是测试工装问题，是产品缺陷）。
   *
   * 机制与 NotesHubView 是同一个：/more 的路由 meta 声明了 `scrollMode:'self'`，
   * 而 AppLayout 对 scroll-self 路由把外层滚动关掉
   * ——`.content.scroll-self { overflow-y: hidden }`，契约是「视图自己滚」；
   * 可本容器既没有 `height:100%` 也没有 `overflow-y:auto`，于是内容超出后
   * 既滚不动、也点不到。
   *
   * 形状照抄同族已验证可用的写法（NotesHubView 的 .notes-hub、
   * TasksView 的 .ai-view）：有界 flex 列 + 自己就是滚动容器。
   * `min-height:0` 不能省：flex 子项默认 min-height:auto，会拒绝缩到
   * 容器高度以下，那样 height:100% 形同虚设。 */
  height: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow-y: auto;
  -webkit-overflow-scrolling: touch;
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