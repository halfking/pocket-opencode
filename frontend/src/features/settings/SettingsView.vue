<template>
  <div class="settings-view">
    <!-- 设置列表 -->
    <div class="settings-container">
      <!-- AI 网关（llmgo 网关：/ai-chat 对话流量的出口；未配置时 AI 聊天不可用） -->
      <div class="settings-section">
        <div class="section-head">
          <h2>{{ t('settings.aiGateway') }}</h2>
          <span :class="['gateway-status', gateway.apiKeySet ? 'ok' : 'off']">
            {{ gateway.apiKeySet ? t('settings.configured') : t('settings.notConfigured') }}
          </span>
        </div>
        <div class="setting-item">
          <div class="setting-icon"><span class="material-symbols-outlined">hub</span></div>
          <div class="setting-content">
            <div class="setting-label">{{ t('settings.gatewayAddress') }}</div>
            <div class="setting-value small">{{ gateway.baseURL || t('settings.notConfigured') }}</div>
          </div>
        </div>
        <div class="setting-item">
          <div class="setting-icon"><span class="material-symbols-outlined">key</span></div>
          <div class="setting-content">
            <div class="setting-label">{{ t('settings.apiKey') }}</div>
            <div class="setting-value">
              {{ gateway.apiKeySet ? t('settings.apiKeySet') : t('settings.apiKeyNotSet') }}
            </div>
          </div>
        </div>
        <div class="setting-item">
          <div class="setting-icon"><span class="material-symbols-outlined">psychology</span></div>
          <div class="setting-content">
            <div class="setting-label">{{ t('settings.availableModels') }}</div>
            <div class="setting-value">
              <span v-if="gateway.models.length === 0" class="muted">{{ t('settings.modelsNotFetched') }}</span>
              <span v-else class="model-row">
                <code v-for="m in gateway.models.slice(0, 3)" :key="m" class="model-chip">{{ m }}</code>
                <span v-if="gateway.models.length > 3" class="muted">
                  {{ t('settings.moreModels', { count: gateway.models.length - 3 }) }}
                </span>
              </span>
            </div>
          </div>
        </div>
        <div class="action-row">
          <button class="action-btn secondary" :disabled="testing" @click="testGateway">
            {{ testing ? t('settings.testing') : t('settings.testConnection') }}
          </button>
          <button class="action-btn primary" @click="openGatewayEditor">
            {{ t('settings.editConfig') }}
          </button>
        </div>
        <div v-if="testResult" :class="['test-result', testResult.ok ? 'ok' : 'fail']">
          {{ testResult.text }}
        </div>
      </div>

      <!-- 用户信息 -->
      <div class="settings-section">
        <h2>{{ t('settings.userInfo') }}</h2>
        <div class="setting-item">
          <div class="setting-icon"><span class="material-symbols-outlined">person</span></div>
          <div class="setting-content">
            <div class="setting-label">{{ t('settings.username') }}</div>
            <div class="setting-value">{{ user?.username }}</div>
          </div>
        </div>
        <div class="setting-item">
          <div class="setting-icon"><span class="material-symbols-outlined">calendar_month</span></div>
          <div class="setting-content">
            <div class="setting-label">{{ t('settings.loginTime') }}</div>
            <div class="setting-value">{{ formatLoginTime() }}</div>
          </div>
        </div>
      </div>

      <!-- 皮肤（亮色 / 暗色 / 跟随系统，选择即生效并持久化） -->
      <div class="settings-section">
        <h2>{{ t('settings.appearance') }}</h2>
        <div class="setting-item">
          <div class="setting-icon"><span class="material-symbols-outlined">palette</span></div>
          <div class="setting-content">
            <div class="setting-label">{{ t('settings.theme') }}</div>
            <div class="theme-options">
              <button
                v-for="opt in themeOptions"
                :key="opt.value"
                :class="['theme-option', { active: theme.preference === opt.value }]"
                @click="theme.setPreference(opt.value)"
              >
                <span class="material-symbols-outlined">{{ opt.icon }}</span>
                <span>{{ opt.label }}</span>
              </button>
            </div>
          </div>
        </div>
      </div>

      <!-- 当前连接 -->
      <div class="settings-section">
        <h2>{{ t('settings.currentConnection') }}</h2>
        <div class="setting-item">
          <div class="setting-icon"><span class="material-symbols-outlined">dns</span></div>
          <div class="setting-content">
            <div class="setting-label">{{ t('settings.server') }}</div>
            <div class="setting-value">{{ selectedServer?.name || t('settings.notSelected') }}</div>
          </div>
        </div>
        <div class="setting-item">
          <div class="setting-icon"><span class="material-symbols-outlined">computer</span></div>
          <div class="setting-content">
            <div class="setting-label">{{ t('settings.instance') }}</div>
            <div class="setting-value">{{ selectedInstance?.displayName || t('settings.notSelected') }}</div>
          </div>
        </div>
      </div>

      <!-- 应用信息 -->
      <div class="settings-section">
        <h2>{{ t('settings.appInfo') }}</h2>
        <div class="setting-item">
          <div class="setting-icon"><span class="material-symbols-outlined">smartphone</span></div>
          <div class="setting-content">
            <div class="setting-label">{{ t('settings.appName') }}</div>
            <div class="setting-value">{{ APP_VERSION.name }}</div>
          </div>
        </div>
        <div class="setting-item">
          <div class="setting-icon"><span class="material-symbols-outlined">info</span></div>
          <div class="setting-content">
            <div class="setting-label">{{ t('settings.version') }}</div>
            <div class="setting-value">{{ t('settings.versionFormat', { version: APP_VERSION.version, buildNumber: APP_VERSION.buildNumber }) }}</div>
          </div>
        </div>
        <div class="setting-item">
          <div class="setting-icon"><span class="material-symbols-outlined">event</span></div>
          <div class="setting-content">
            <div class="setting-label">{{ t('settings.buildDate') }}</div>
            <div class="setting-value">{{ APP_VERSION.buildDate }}</div>
          </div>
        </div>
        <div class="setting-item">
          <div class="setting-icon"><span class="material-symbols-outlined">hub</span></div>
          <div class="setting-content">
            <div class="setting-label">{{ t('settings.apiAddress') }}</div>
            <div class="setting-value small">{{ apiOrigin }}</div>
          </div>
        </div>
      </div>

      <!-- 权限与隐私 -->
      <div class="settings-section">
        <h2>{{ t('settings.privacy') }}</h2>
        <div class="setting-item entry" @click="goPermissions">
          <div class="setting-icon"><span class="material-symbols-outlined">admin_panel_settings</span></div>
          <div class="setting-content">
            <div class="setting-label">{{ t('settings.permissionsPrivacy') }}</div>
            <div class="setting-value">{{ t('settings.permissionsDesc') }}</div>
          </div>
          <span class="material-symbols-outlined chevron">chevron_right</span>
        </div>
      </div>

      <!-- 自动化管理 -->
      <div class="settings-section">
        <h2>{{ t('settings.automation') }}</h2>
        <div class="setting-item entry" @click="goScheduledTasks">
          <div class="setting-icon"><span class="material-symbols-outlined">schedule</span></div>
          <div class="setting-content">
            <div class="setting-label">{{ t('settings.scheduledTasks') }}</div>
            <div class="setting-value">{{ t('settings.scheduledTasksDesc') }}</div>
          </div>
          <span class="material-symbols-outlined chevron">chevron_right</span>
        </div>
      </div>

      <!-- 操作按钮 -->
      <div class="settings-section">
        <button class="action-btn secondary" @click="checkForUpdates">
          {{ t('settings.checkUpdates') }}
        </button>
        <button class="action-btn secondary" @click="changeServer">
          {{ t('settings.changeServer') }}
        </button>
        <button class="action-btn danger" @click="handleLogout">
          {{ t('settings.logout') }}
        </button>
      </div>
    </div>

    <!--
      ✅ 已移除硬编码底部导航（任务/实例/设置）。
      App.vue 现在用 AppLayout 包裹 router-view，共享的 BottomNav 会自动渲染
      5模块 Tab（AI/笔记/会议/邮件/更多）。
    -->
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { APP_VERSION, checkUpdate } from '../../utils/version'
import { api, type GatewayConfig, type GatewayTestResult } from '../../api/client'
import { useConfirm } from '../../composables/useConfirm'
import { useThemeStore, type ThemePreference } from '../../stores/theme'

const router = useRouter()
const { confirm } = useConfirm()
const { t } = useI18n()
const theme = useThemeStore()

// 皮肤三选项（图标 + 词条），选中即调用 setPreference 全局生效
const themeOptions: { value: ThemePreference; label: string; icon: string }[] = [
  { value: 'light', label: t('settings.themeLight'), icon: 'light_mode' },
  { value: 'dark', label: t('settings.themeDark'), icon: 'dark_mode' },
  { value: 'system', label: t('settings.themeSystem'), icon: 'brightness_auto' },
]

// 暴露给 template（Vue template 不能直接访问 window）
const apiOrigin = typeof window !== 'undefined' ? window.location.origin : ''

const user = ref<any>(null)
const selectedServer = ref<any>(null)
const selectedInstance = ref<any>(null)

// Phase 5: LLM Gateway 状态
const gateway = ref<GatewayConfig>({
  baseURL: '',
  apiKeySet: false,
  apiKey: '',
  models: [],
  source: 'pocketd',
})
const testing = ref(false)
const testResult = ref<{ ok: boolean; text: string } | null>(null)

onMounted(async () => {
  // 历史版本曾把裸用户名（非 JSON）写入 pocket_user，坏值不得中断挂载流程
  // （曾导致后续 AI 网关配置加载被跳过、区块恒显"未配置"）。
  const readJSON = <T,>(key: string): T | null => {
    try {
      const raw = localStorage.getItem(key)
      return raw ? (JSON.parse(raw) as T) : null
    } catch {
      return null
    }
  }

  // 加载用户信息
  user.value = readJSON('pocket_user')

  // 加载当前服务器 / 实例
  selectedServer.value = readJSON('selected_server')
  selectedInstance.value = readJSON('selected_instance')

  // 加载 LLM Gateway 配置
  await refreshGateway()
})

async function refreshGateway() {
  try {
    const cfg = await api.getGatewayConfig()
    // 后端 PG 往返可能给 null（模板直接读 models.length 会崩挂载），兜底 []
    gateway.value = { ...cfg, models: cfg.models ?? [] }
  } catch (err) {
    console.warn('Failed to load gateway config:', err)
  }
}

async function testGateway() {
  testing.value = true
  testResult.value = null
  try {
    const r: GatewayTestResult = await api.testGateway()
    if (r.ok) {
      testResult.value = {
        ok: true,
        text: t('settings.testSuccess', { count: r.models?.length || 0 }),
      }
      await refreshGateway()
    } else {
      testResult.value = {
        ok: false,
        text: t('settings.testFailed', { error: r.error || r.response || 'HTTP ' + r.status }),
      }
    }
  } catch (err: any) {
    testResult.value = { ok: false, text: '✗ ' + (err?.message || String(err)) }
  } finally {
    testing.value = false
  }
}

function openGatewayEditor() {
  router.push('/settings/llm-gateway')
}

function formatLoginTime(): string {
  if (!user.value?.loginTime) return '-'
  const date = new Date(user.value.loginTime)
  return date.toLocaleString('zh-CN')
}

async function checkForUpdates() {
  try {
    const response = await checkUpdate()
    if (response.hasUpdate) {
      alert(t('settings.newVersionAvailable', { 
        version: response.latest?.version,
        changelog: response.latest?.changelog.join('\n')
      }))
    } else {
      alert(t('settings.alreadyLatest'))
    }
  } catch (error) {
    console.error('检查更新失败:', error)
    alert(t('settings.checkUpdateFailed'))
  }
}

function changeServer() {
  router.push('/servers')
}

function goPermissions() {
  router.push('/settings/permissions')
}

function goScheduledTasks() {
  router.push('/settings/scheduled-tasks')
}

async function handleLogout() {
  if (await confirm({ title: t('settings.logout'), message: t('settings.logoutConfirm'), confirmText: t('settings.logout'), danger: true })) {
    localStorage.removeItem('pocket_user')
    localStorage.removeItem('selected_server')
    localStorage.removeItem('selected_instance')
    router.push('/login')
  }
}
</script>

<style scoped>
.settings-view {
  min-height: 100%;
}

.settings-container {
  flex: 1;
  padding: var(--space-3);
}

.settings-section {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  padding: var(--spacing-card-padding);
  margin-bottom: var(--spacing-list-gap);
}

.settings-section h2 {
  font-size: var(--text-xs);
  font-weight: var(--font-weight-semibold);
  color: var(--text-muted);
  text-transform: uppercase;
  margin: 0 0 var(--space-3) 0;
  letter-spacing: 0.5px;
}

/* 区块头 + 状态徽标（AI 网关配置态一眼可见） */
.section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin: 0 0 var(--space-3) 0;
}
.section-head h2 {
  margin: 0;
}
.gateway-status {
  padding: 2px var(--space-2);
  border-radius: var(--radius-full);
  font-size: var(--text-xs);
  font-weight: var(--font-weight-semibold);
}
.gateway-status.ok {
  background: var(--success-bg);
  color: var(--success);
}
.gateway-status.off {
  background: var(--warning-bg);
  color: var(--warning);
}

.setting-item {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3) 0;
  border-bottom: 1px solid var(--border);
}

.setting-item:last-child {
  border-bottom: none;
}

.setting-icon {
  width: 36px;
  height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--bg-subtle);
  border-radius: var(--radius-sm);
  flex-shrink: 0;
  color: var(--brand-primary);
}

.setting-icon .material-symbols-outlined {
  font-size: 20px;
}

.setting-content {
  flex: 1;
  min-width: 0;
}

.setting-label {
  font-size: var(--text-base);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  margin-bottom: 2px;
}

.setting-value {
  font-size: var(--text-sm);
  color: var(--text-secondary);
}

.setting-value.small {
  font-size: var(--text-xs);
  font-family: monospace;
  word-break: break-all;
}

/* 皮肤三选项分段控件 */
.theme-options {
  display: flex;
  gap: var(--space-2);
  margin-top: var(--space-2);
}

.theme-option {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-2) var(--space-1);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-subtle);
  color: var(--text-secondary);
  font-size: var(--text-sm);
  transition: border-color var(--duration-fast) var(--ease-out),
    background var(--duration-fast) var(--ease-out);
}

.theme-option .material-symbols-outlined {
  font-size: 18px;
}

.theme-option.active {
  background: var(--brand-bg);
  color: var(--brand-primary);
  border-color: var(--brand-primary);
  font-weight: var(--font-weight-semibold);
}

.action-btn {
  width: 100%;
  padding: var(--space-3);
  font-size: var(--text-base);
  font-weight: var(--font-weight-semibold);
  border: none;
  border-radius: var(--radius-md);
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  margin-bottom: var(--space-2);
  transition: opacity 120ms;
}

.action-btn:last-child {
  margin-bottom: 0;
}

.action-btn.primary {
  background: var(--brand-gradient);
  color: var(--text-inverse);
}

.action-btn.secondary {
  background: var(--brand-bg);
  color: var(--brand-primary);
  border: 1px solid var(--border);
}

.action-btn.danger {
  background: var(--danger-bg);
  color: var(--danger);
  border: 1px solid var(--border);
}

.action-btn:active {
  opacity: 0.85;
}

.action-row {
  display: flex;
  gap: var(--space-2);
  margin-top: var(--space-2);
}

.action-row .action-btn {
  flex: 1;
  margin-bottom: 0;
}

.test-result {
  margin-top: var(--space-2);
  padding: var(--space-2);
  border-radius: var(--radius-sm);
  font-size: var(--text-sm);
}

.test-result.ok {
  background: var(--success-bg);
  color: var(--success);
}

.test-result.fail {
  background: var(--danger-bg);
  color: var(--danger);
}

.model-row {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
  align-items: center;
}

.model-chip {
  font-size: var(--text-xs);
  padding: 1px var(--space-2);
  background: var(--bg-subtle);
  border-radius: var(--radius-sm);
  color: var(--text-secondary);
}

.muted {
  color: var(--text-muted);
  font-size: var(--text-sm);
}

/* 二级页跳转 entry（权限与隐私入口） */
.setting-item.entry {
  cursor: pointer;
}
.setting-item.entry:active {
  opacity: 0.7;
}
.setting-item.entry .chevron {
  color: var(--text-muted);
  font-size: 20px;
}
</style>
