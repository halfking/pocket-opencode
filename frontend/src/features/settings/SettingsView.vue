<template>
  <div class="settings-view">
    <!-- 设置列表 -->
    <div class="settings-container">
      <!-- AI 网关（llmgo 网关：/ai-chat 对话流量的出口；未配置时 AI 聊天不可用） -->
      <div class="settings-section">
        <div class="section-head">
          <h2>AI 网关</h2>
          <span :class="['gateway-status', gateway.apiKeySet ? 'ok' : 'off']">
            {{ gateway.apiKeySet ? '已配置' : '未配置' }}
          </span>
        </div>
        <div class="setting-item">
          <div class="setting-icon"><span class="material-symbols-outlined">hub</span></div>
          <div class="setting-content">
            <div class="setting-label">网关地址</div>
            <div class="setting-value small">{{ gateway.baseURL || '未配置' }}</div>
          </div>
        </div>
        <div class="setting-item">
          <div class="setting-icon"><span class="material-symbols-outlined">key</span></div>
          <div class="setting-content">
            <div class="setting-label">API 密钥</div>
            <div class="setting-value">
              {{ gateway.apiKeySet ? '✓ 已设置' : '未设置 — AI 聊天需要密钥' }}
            </div>
          </div>
        </div>
        <div class="setting-item">
          <div class="setting-icon"><span class="material-symbols-outlined">psychology</span></div>
          <div class="setting-content">
            <div class="setting-label">可用模型</div>
            <div class="setting-value">
              <span v-if="gateway.models.length === 0" class="muted">未获取（配置后自动拉取）</span>
              <span v-else class="model-row">
                <code v-for="m in gateway.models.slice(0, 3)" :key="m" class="model-chip">{{ m }}</code>
                <span v-if="gateway.models.length > 3" class="muted">
                  +{{ gateway.models.length - 3 }}
                </span>
              </span>
            </div>
          </div>
        </div>
        <div class="action-row">
          <button class="action-btn secondary" :disabled="testing" @click="testGateway">
            {{ testing ? '测试中…' : '测试连接' }}
          </button>
          <button class="action-btn primary" @click="openGatewayEditor">
            编辑配置
          </button>
        </div>
        <div v-if="testResult" :class="['test-result', testResult.ok ? 'ok' : 'fail']">
          {{ testResult.text }}
        </div>
      </div>

      <!-- 用户信息 -->
      <div class="settings-section">
        <h2>用户信息</h2>
        <div class="setting-item">
          <div class="setting-icon"><span class="material-symbols-outlined">person</span></div>
          <div class="setting-content">
            <div class="setting-label">用户名</div>
            <div class="setting-value">{{ user?.username }}</div>
          </div>
        </div>
        <div class="setting-item">
          <div class="setting-icon"><span class="material-symbols-outlined">calendar_month</span></div>
          <div class="setting-content">
            <div class="setting-label">登录时间</div>
            <div class="setting-value">{{ formatLoginTime() }}</div>
          </div>
        </div>
      </div>

      <!-- 当前连接 -->
      <div class="settings-section">
        <h2>当前连接</h2>
        <div class="setting-item">
          <div class="setting-icon"><span class="material-symbols-outlined">dns</span></div>
          <div class="setting-content">
            <div class="setting-label">服务器</div>
            <div class="setting-value">{{ selectedServer?.name || '未选择' }}</div>
          </div>
        </div>
        <div class="setting-item">
          <div class="setting-icon"><span class="material-symbols-outlined">computer</span></div>
          <div class="setting-content">
            <div class="setting-label">实例</div>
            <div class="setting-value">{{ selectedInstance?.displayName || '未选择' }}</div>
          </div>
        </div>
      </div>

      <!-- 应用信息 -->
      <div class="settings-section">
        <h2>应用信息</h2>
        <div class="setting-item">
          <div class="setting-icon"><span class="material-symbols-outlined">smartphone</span></div>
          <div class="setting-content">
            <div class="setting-label">应用名称</div>
            <div class="setting-value">{{ APP_VERSION.name }}</div>
          </div>
        </div>
        <div class="setting-item">
          <div class="setting-icon"><span class="material-symbols-outlined">info</span></div>
          <div class="setting-content">
            <div class="setting-label">版本号</div>
            <div class="setting-value">v{{ APP_VERSION.version }} (Build {{ APP_VERSION.buildNumber }})</div>
          </div>
        </div>
        <div class="setting-item">
          <div class="setting-icon"><span class="material-symbols-outlined">event</span></div>
          <div class="setting-content">
            <div class="setting-label">构建日期</div>
            <div class="setting-value">{{ APP_VERSION.buildDate }}</div>
          </div>
        </div>
        <div class="setting-item">
          <div class="setting-icon"><span class="material-symbols-outlined">hub</span></div>
          <div class="setting-content">
            <div class="setting-label">API 地址</div>
            <div class="setting-value small">{{ apiOrigin }}</div>
          </div>
        </div>
      </div>

      <!-- 权限与隐私 -->
      <div class="settings-section">
        <h2>隐私</h2>
        <div class="setting-item entry" @click="goPermissions">
          <div class="setting-icon"><span class="material-symbols-outlined">admin_panel_settings</span></div>
          <div class="setting-content">
            <div class="setting-label">权限与隐私</div>
            <div class="setting-value">麦克风 / 通知 / 生物识别</div>
          </div>
          <span class="material-symbols-outlined chevron">chevron_right</span>
        </div>
      </div>

      <!-- 操作按钮 -->
      <div class="settings-section">
        <button class="action-btn secondary" @click="checkForUpdates">
          检查更新
        </button>
        <button class="action-btn secondary" @click="changeServer">
          切换服务器
        </button>
        <button class="action-btn danger" @click="handleLogout">
          退出登录
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
import { APP_VERSION, checkUpdate } from '../../utils/version'
import { api, type GatewayConfig, type GatewayTestResult } from '../../api/client'

const router = useRouter()

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
        text: `✓ 连通 · ${r.models?.length || 0} 个模型`,
      }
      await refreshGateway()
    } else {
      testResult.value = {
        ok: false,
        text: `✗ 失败：${r.error || r.response || 'HTTP ' + r.status}`,
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
      alert(`发现新版本 v${response.latest?.version}！\n\n更新内容:\n${response.latest?.changelog.join('\n')}`)
    } else {
      alert('当前已是最新版本！')
    }
  } catch (error) {
    console.error('检查更新失败:', error)
    alert('检查更新失败，请稍后重试')
  }
}

function changeServer() {
  router.push('/servers')
}

function goPermissions() {
  router.push('/settings/permissions')
}

function handleLogout() {
  if (confirm('确定要退出登录吗？')) {
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
