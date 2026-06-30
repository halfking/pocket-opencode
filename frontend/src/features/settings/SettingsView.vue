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
            <div class="setting-value small">{{ window.location.origin }}</div>
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
    </div>

    <!-- 底部导航 -->
    <div class="bottom-nav">
      <button class="nav-item" @click="$router.push('/tasks')">
        <span class="nav-icon">📋</span>
        <span class="nav-label">任务</span>
      </button>
      <button class="nav-item" @click="$router.push('/instances')">
        <span class="nav-icon">💻</span>
        <span class="nav-label">实例</span>
      </button>
      <button class="nav-item active" @click="$router.push('/settings')">
        <span class="nav-icon">⚙️</span>
        <span class="nav-label">设置</span>
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { APP_VERSION, checkUpdate } from '../../utils/version'

const router = useRouter()

const user = ref<any>(null)
const selectedServer = ref<any>(null)
const selectedInstance = ref<any>(null)

onMounted(() => {
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
})

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
  background: #f5f7fa;
  display: flex;
  flex-direction: column;
  padding-bottom: 70px;
}

.top-bar {
  background: white;
  padding: 16px 20px;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.05);
}

.top-bar h1 {
  font-size: 20px;
  font-weight: 600;
  margin: 0;
}

.settings-container {
  flex: 1;
  overflow-y: auto;
  padding: 20px;
}

.settings-section {
  background: white;
  border-radius: 16px;
  padding: 20px;
  margin-bottom: 16px;
}

.settings-section h2 {
  font-size: 14px;
  font-weight: 600;
  color: #999;
  text-transform: uppercase;
  margin: 0 0 16px 0;
  letter-spacing: 0.5px;
}

.setting-item {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 16px 0;
  border-bottom: 1px solid #f0f0f0;
}

.setting-item:last-child {
  border-bottom: none;
}

.setting-icon {
  font-size: 24px;
  width: 40px;
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #f8f9fa;
  border-radius: 10px;
  flex-shrink: 0;
}

.setting-content {
  flex: 1;
}

.setting-label {
  font-size: 15px;
  font-weight: 600;
  color: #333;
  margin-bottom: 4px;
}

.setting-value {
  font-size: 14px;
  color: #666;
}

.setting-value.small {
  font-size: 12px;
  font-family: monospace;
}

.action-btn {
  width: 100%;
  padding: 16px;
  font-size: 16px;
  font-weight: 600;
  border: none;
  border-radius: 12px;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  margin-bottom: 12px;
  transition: all 0.3s;
}

.action-btn:last-child {
  margin-bottom: 0;
}

.action-btn.primary {
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  color: white;
}

.action-btn.danger {
  background: #fee;
  color: #c33;
  border: 1px solid #fcc;
}

.action-btn:active {
  transform: scale(0.98);
}

.bottom-nav {
  position: fixed;
  bottom: 0;
  left: 0;
  right: 0;
  background: white;
  display: flex;
  padding: 8px;
  box-shadow: 0 -2px 10px rgba(0, 0, 0, 0.1);
}

.nav-item {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  padding: 8px;
  background: transparent;
  border: none;
  cursor: pointer;
  gap: 4px;
}

.nav-icon {
  font-size: 22px;
}

.nav-label {
  font-size: 11px;
  color: #999;
}

.nav-item.active .nav-label {
  color: #667eea;
  font-weight: 600;
}
</style>

.action-btn.secondary {
  background: #e8f0fe;
  color: #667eea;
  border: 1px solid #d0e1fd;
}
