<!--
  SettingsMenuDrawer — 统一的"设置/功能"侧边抽屉（AppLayout 顶栏左 ≡ 触发）。

  设计动机：业界惯例（iOS HIG / Material 3 / Slack / Notion Mobile）将"账户中心 +
  设置入口 + 次要功能"集中到一个菜单抽屉；这样每个页面顶栏是单层，左 ≡ 打开此抽屉，
  tab bar 不必再放设置 tab。

  依赖 BottomSheet（placement="left"）的现成动画/手势，列表按"个人中心 / 设置 / 高级"
  分组；点击即跳路由并关闭抽屉。
-->
<template>
  <BottomSheet
    :model-value="modelValue"
    placement="left"
    :title="t('settingsMenu.title')"
    :aria-label="t('settingsMenu.title')"
    @update:model-value="(v) => emit('update:modelValue', v)"
  >
    <div class="settings-menu">
      <!-- 用户信息卡片：账户中心入口 -->
      <button class="user-card" type="button" @click="goAccount">
        <span class="user-avatar" aria-hidden="true">
          <span class="material-symbols-outlined">account_circle</span>
        </span>
        <span class="user-info">
          <span class="user-name">{{ userName || t('settingsMenu.notLoggedIn') }}</span>
          <span class="user-action">{{ t('settingsMenu.viewAccount') }}</span>
        </span>
        <span class="material-symbols-outlined user-chevron" aria-hidden="true">chevron_right</span>
      </button>

      <section v-for="group in groups" :key="group.title" class="menu-group">
        <h4 class="group-title">{{ group.title }}</h4>
        <ul class="group-list" role="menu">
          <li v-for="item in group.items" :key="item.to" role="none">
            <button
              class="menu-item"
              type="button"
              role="menuitem"
              @click="go(item.to)"
            >
              <span class="menu-icon" aria-hidden="true">
                <span class="material-symbols-outlined">{{ item.icon }}</span>
              </span>
              <span class="menu-label">{{ item.label }}</span>
              <span class="material-symbols-outlined menu-chevron" aria-hidden="true">chevron_right</span>
            </button>
          </li>
        </ul>
      </section>

      <p class="menu-foot">{{ t('settingsMenu.versionFootnote', { version }) }}</p>
    </div>
  </BottomSheet>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import BottomSheet from './BottomSheet.vue'
import { useAuthStore } from '../../stores/auth'
import { APP_VERSION } from '../../utils/version'

defineProps<{ modelValue: boolean }>()
const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
}>()

const { t } = useI18n()
const router = useRouter()
const auth = useAuthStore()
const userName = computed(() => auth.user)
const version = computed(() => APP_VERSION.version)

interface MenuItem { to: string; icon: string; label: string }
interface MenuGroup { title: string; items: MenuItem[] }

const groups = computed<MenuGroup[]>(() => [
  {
    title: t('settingsMenu.groupSettings'),
    items: [
      { to: '/settings', icon: 'settings', label: t('routes.settings') },
      { to: '/settings/llm-gateway', icon: 'model_training', label: t('routes.aiModels') },
      { to: '/settings/scheduled-tasks', icon: 'schedule', label: t('routes.scheduledTasks') },
      { to: '/settings/permissions', icon: 'privacy_tip', label: t('routes.permissionsPrivacy') },
    ],
  },
  {
    title: t('settingsMenu.groupOps'),
    items: [
      { to: '/cost', icon: 'payments', label: t('routes.costQuota') },
      { to: '/gateway', icon: 'dns', label: t('routes.gatewayNodes') },
      { to: '/instances', icon: 'memory', label: t('routes.instances') },
      { to: '/tasks', icon: 'checklist', label: t('routes.tasks') },
      { to: '/sessions', icon: 'forum', label: t('routes.sessions') },
    ],
  },
])

function go(to: string) {
  emit('update:modelValue', false)
  // 等关闭动画一帧再 push，避免路由切换被 sheet 拦截
  setTimeout(() => router.push(to), 120)
}

function goAccount() {
  emit('update:modelValue', false)
  setTimeout(() => router.push('/settings'), 120)
}
</script>

<style scoped>
.settings-menu {
  display: flex;
  flex-direction: column;
  gap: var(--space-5);
  padding: 0;
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

.user-card:active { background: var(--color-bg-hover, rgba(0, 0, 0, 0.04)); }

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
  font-size: 15px;
  font-weight: var(--font-weight-semibold);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.user-action {
  font-size: 12px;
  color: var(--text-secondary);
}

.user-chevron {
  font-size: 20px;
  color: var(--text-tertiary, var(--text-muted));
  flex-shrink: 0;
}

.menu-group { display: flex; flex-direction: column; gap: var(--space-2); }

.group-title {
  margin: 0 0 var(--space-1) var(--space-1);
  font-size: 11px;
  font-weight: var(--font-weight-semibold);
  text-transform: uppercase;
  letter-spacing: 0.4px;
  color: var(--text-tertiary, var(--text-muted));
}

.group-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  overflow: hidden;
}

.menu-item {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  width: 100%;
  padding: var(--space-3);
  background: transparent;
  border: none;
  border-bottom: 1px solid var(--border);
  color: var(--text-primary);
  text-align: left;
  cursor: pointer;
  min-height: 44px;
  transition: background var(--duration-fast) var(--ease-out);
}

.group-list li:last-child .menu-item { border-bottom: none; }

.menu-item:active { background: var(--color-bg-hover, rgba(0, 0, 0, 0.04)); }

.menu-icon {
  width: 28px;
  height: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--brand-primary, #4c8dff);
  flex-shrink: 0;
}

.menu-icon .material-symbols-outlined { font-size: 20px; }

.menu-label {
  flex: 1;
  font-size: 14px;
  font-weight: var(--font-weight-medium);
}

.menu-chevron {
  font-size: 18px;
  color: var(--text-tertiary, var(--text-muted));
}

.menu-foot {
  margin: 0;
  padding: 0 var(--space-1) var(--space-3);
  font-size: 11px;
  color: var(--text-tertiary, var(--text-muted));
  text-align: center;
}
</style>
