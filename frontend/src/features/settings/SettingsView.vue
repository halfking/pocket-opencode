<template>
  <div class="settings-view">
    <!-- 顶部栏 -->
    <div class="top-bar">
      <h1>设置</h1>
    </div>

    <!-- 设置列表 -->
    <div class="settings-container">
      <!-- 用户信息 -->
      <div class="settings-section">
        <h2>用户信息</h2>
        <div class="setting-item">
          <div class="setting-icon">👤</div>
          <div class="setting-content">
            <div class="setting-label">用户名</div>
            <div class="setting-value">{{ user?.username }}</div>
          </div>
        </div>
        <div class="setting-item">
          <div class="setting-icon">🕐</div>
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
          <div class="setting-icon">🌐</div>
          <div class="setting-content">
            <div class="setting-label">服务器</div>
            <div class="setting-value">{{ selectedServer?.name || '未选择' }}</div>
          </div>
        </div>
        <div class="setting-item">
          <div class="setting-icon">💻</div>
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
          <div class="setting-icon">📱</div>
          <div class="setting-content">
            <div class="setting-label">应用名称</div>
            <div class="setting-value">{{ APP_VERSION.name }}</div>
          </div>
        </div>
        <div class="setting-item">
          <div class="setting-icon">🔖</div>
          <div class="setting-content">
            <div class="setting-label">版本号</div>
            <div class="setting-value">v{{ APP_VERSION.version }} (Build {{ APP_VERSION.buildNumber }})</div>
          </div>
        </div>
        <div class="setting-item">
          <div class="setting-icon">📅</div>
          <div class="setting-content">
            <div class="setting-label">构建日期</div>
            <div class="setting-value">{{ APP_VERSION.buildDate }}</div>
          </div>
        </div>
        <div class="setting-item">
          <div class="setting-icon">🌐</div>
          <div class="setting-content">
            <div class="setting-label">API 地址</div>
            <div class="setting-value small">{{ apiOrigin }}</div>
          </div>
        </div>
      </div>

      <!-- 操作按钮 -->
      <div class="settings-section">
        <button class="action-btn secondary" @click="checkForUpdates">
          🔄 检查更新
        </button>
        <button class="action-btn primary" @click="changeServer">
          🔄 切换服务器
        </button>
        <button class="action-btn danger" @click="handleLogout">
          🚪 退出登录
        </button>
      </div>

      <!-- AI 模型（LLM Gateway） -->
      <div class="settings-section">
        <h2>AI 模型</h2>
        <div class="setting-item">
          <div class="setting-icon">🌐</div>
          <div class="setting-content">
            <div class="setting-label">Gateway URL</div>
            <div class="setting-value small">{{ gateway.baseURL || '未配置' }}</div>
          </div>
        </div>
        <div class="setting-item">
          <div class="setting-icon">🔑</div>
          <div class="setting-content">
            <div class="setting-label">API Key</div>
            <div class="setting-value">
              {{ gateway.apiKeySet ? '✓ 已设置' : '未设置' }}
            </div>
          </div>
        </div>
        <div class="setting-item">
          <div class="setting-icon">🧠</div>
          <div class="setting-content">
            <div class="setting-label">可用模型</div>
            <div class="setting-value">
              <span v-if="gateway.models.length === 0" class="muted">未配置</span>
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
            {{ testing ? '测试中…' : '🧪 测试连接' }}
          </button>
          <button class="action-btn primary" @click="openGatewayEditor">
            ⚙️ 编辑配置
          </button>
        </div>
        <div v-if="testResult" :class="['test-result', testResult.ok ? 'ok' : 'fail']">
          {{ testResult.text }}
        </div>
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
  // 加载用户信息
  const userStr = localStorage.getItem('pocket_user')
  if (userStr) {
    user.value = JSON.parse(userStr)
  }

  // 加载当前服务器
  const serverStr = localStorage.getItem('selected_server')
  if (serverStr) {
    selectedServer.value = JSON.parse(serverStr)
  }

  // 加载当前实例
  const instanceStr = localStorage.getItem('selected_instance')
  if (instanceStr) {
    selectedInstance.value = JSON.parse(instanceStr)
  }

  // 加载 LLM Gateway 配置
  await refreshGateway()
})

async function refreshGateway() {
  try {
    const cfg = await api.getGatewayConfig()
    gateway.value = cfg
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
  min-height: 100vh;
  background: var(--bg-base);
  display: flex;
  flex-direction: column;
  padding-bottom: 70px;
}

.top-bar {
  background: var(--bg-card);
  padding: var(--space-3) var(--space-4);
  border-bottom: 1px solid var(--border);
}

.top-bar h1 {
  font-size: var(--text-lg);
  font-weight: var(--font-weight-semibold);
  margin: 0;
  color: var(--text-primary);
}

.settings-container {
  flex: 1;
  overflow-y: auto;
  padding: var(--space-3);
}

.settings-section {
  background: var(--bg-card);
  border-radius: var(--radius-lg);
  padding: var(--space-4);
  margin-bottom: var(--space-3);
  border: 1px solid var(--border);
}

.settings-section h2 {
  font-size: var(--text-sm);
  font-weight: var(--font-weight-semibold);
  color: var(--text-muted);
  text-transform: uppercase;
  margin: 0 0 var(--space-3) 0;
  letter-spacing: 0.5px;
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
  font-size: var(--text-lg);
  width: 36px;
  height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--bg-subtle);
  border-radius: var(--radius-md);
  flex-shrink: 0;
}

.setting-content {
  flex: 1;
}

.setting-label {
  font-size: var(--text-base);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  margin-bottom: var(--space-1);
}

.setting-value {
  font-size: var(--text-sm);
  color: var(--text-secondary);
}

.setting-value.small {
  font-size: var(--text-xs);
  font-family: monospace;
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
  margin-bottom: var(--space-2-5);
  transition: all 120ms;
}

.action-btn:last-child {
  margin-bottom: 0;
}

.action-btn.primary {
  background: var(--brand-primary);
  color: var(--text-inverse);
}

.action-btn.secondary {
  background: rgba(102, 126, 234, 0.1);
  color: var(--brand-primary);
  border: 1px solid rgba(102, 126, 234, 0.2);
}

.action-btn.danger {
  background: rgba(239, 68, 68, 0.1);
  color: var(--danger);
  border: 1px solid rgba(239, 68, 68, 0.2);
}

.action-btn:active {
  transform: scale(0.98);
}

.muted {
  color: var(--text-muted);
}

.model-row {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.model-chip {
  font-size: var(--text-xs);
  padding: var(--space-1) var(--space-2);
  background: var(--bg-subtle);
  border-radius: var(--radius-sm);
  font-family: monospace;
  color: var(--text-primary);
}

.action-row {
  display: flex;
  gap: var(--space-2);
  margin-top: var(--space-3);
}

.action-row .action-btn {
  flex: 1;
  margin-bottom: 0;
}

.test-result {
  margin-top: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  font-size: var(--text-sm);
  text-align: center;
}

.test-result.ok {
  background: rgba(16, 185, 129, 0.1);
  color: var(--success);
}

.test-result.fail {
  background: rgba(239, 68, 68, 0.1);
  color: var(--danger);
}

/*
  ✅ 已删除硬编码底部导航的 CSS 样式（.bottom-nav / .nav-item / .nav-icon /
  .nav-label），由 AppLayout 提供的共享 BottomNav 接管。
*/
</style>
