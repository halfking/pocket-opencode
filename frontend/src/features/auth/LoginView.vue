<template>
  <div class="login-view">
    <div class="login-container">
      <!-- Logo 和标题 -->
      <div class="logo-section">
        <div class="logo">🦞</div>
        <h1 class="app-title">OpenCode Pocket</h1>
        <p class="app-subtitle">{{ needUnlock ? '解锁本地数据' : '移动端多实例管理平台' }}</p>
      </div>

      <!-- 解锁界面（已登录但刷新后 crypto 未初始化）-->
      <div v-if="needUnlock" class="login-form">
        <p class="unlock-hint">检测到已有登录态，但本地加密库未解锁。<br />请重新输入主密码以访问本地数据。</p>
        <div class="form-group">
          <label>主密码</label>
          <input
            v-model="unlockPassword"
            type="password"
            placeholder="输入主密码解锁"
            @keyup.enter="unlock"
          />
        </div>
        <button class="login-btn" :disabled="!unlockPassword || loading" @click="unlock">
          {{ loading ? '解锁中...' : '🔓 解锁' }}
        </button>
        <div v-if="error" class="error-message">{{ error }}</div>
        <p class="hint" style="margin-top: 20px; cursor: pointer;" @click="logoutAndRelogin">退出重新登录 →</p>
      </div>

      <!-- 登录表单 -->
      <div v-else class="login-form">
        <div class="form-group">
          <label>用户名</label>
          <input
            v-model="username"
            type="text"
            placeholder="输入用户名"
            @keyup.enter="handleLogin"
          />
        </div>

        <div class="form-group">
          <label>密码</label>
          <input
            v-model="password"
            type="password"
            placeholder="输入密码"
            @keyup.enter="handleLogin"
          />
        </div>

        <button 
          class="login-btn"
          :disabled="!username || !password || loading"
          @click="handleLogin"
        >
          {{ loading ? '登录中...' : '登录' }}
        </button>

        <div v-if="error" class="error-message">
          {{ error }}
        </div>
      </div>

      <!-- 版本信息 -->
      <div class="version-info">
        <p>v1.1.0-mobile</p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '../../stores/auth'
import { http, ApiError } from '../../api/http'
import { connectWs } from '../../api/websocket'
import { initLobster, isLobsterReady } from '../../native/lobster-init'

const router = useRouter()
const auth = useAuthStore()

const username = ref('admin')
const password = ref('admin')
const loading = ref(false)
const error = ref('')

// 场景：刷新页面后 token 持久（localStorage），但龙虾（crypto + SQLCipher）未初始化
// 此时需要用户重新输入主密码解锁本地数据，而非直接跳走。
const needUnlock = ref(false)
const unlockPassword = ref('')

onMounted(() => {
  if (auth.isAuthenticated && !isLobsterReady()) {
    needUnlock.value = true
  } else if (auth.isAuthenticated && isLobsterReady()) {
    // 已登录且已初始化，直接进首页
    router.push('/ai')
  }
})

async function unlock() {
  if (!unlockPassword.value) {
    error.value = '请输入主密码以解锁本地数据'
    return
  }
  loading.value = true
  error.value = ''
  try {
    await initLobster(unlockPassword.value)
    needUnlock.value = false
    router.push('/ai')
  } catch (e: any) {
    error.value = `解锁失败（主密码错误？）：${e.message || e}`
  } finally {
    loading.value = false
  }
}

function logoutAndRelogin() {
  auth.logout()
  needUnlock.value = false
  error.value = ''
}

async function handleLogin() {
  if (!username.value || !password.value) {
    error.value = '请输入用户名和密码'
    return
  }

  loading.value = true
  error.value = ''

  try {
    // Phase C: 服务端无状态认证（只为签发调用 /embed /llm 的 JWT）
    const res = await http<{ token: string; user: string }>('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username: username.value, password: password.value }),
    })
    auth.setAuth(res.token, res.user)
    // 🦞 认证成功后才建立 WS（此前模块加载不会自动连）
    await connectWs()
    // 🦞 尝试初始化本地加密库（如失败不阻塞登录，仅影响本地加密功能）
    try {
      await initLobster(password.value)
    } catch (lobsterErr: any) {
      console.warn('[login] 龙虾初始化失败，将以非加密模式继续:', lobsterErr?.message || lobsterErr)
      // 不阻塞登录，本地加密功能不可用但服务端功能正常
    }
    router.push('/ai')
  } catch (e: any) {
    if (e instanceof ApiError) {
      if (e.status === 401) {
        // 认证失败：用户名密码错误，或后端未开启 POCKET_DEV_AUTH=true（admin/admin 需此 gate）
        error.value = '登录失败：凭据错误或后端未开启开发登录（需设置 POCKET_DEV_AUTH=true）'
      } else if (e.status === 404) {
        // 后端尚未部署 auth 路由时，回退到 legacy localStorage 兼容模式。
        if (username.value === 'admin' && password.value === 'admin') {
          const legacyUser = JSON.stringify({ username: 'admin', loginTime: new Date().toISOString() })
          const legacyToken = 'legacy-token-' + Date.now() // 临时 token 用于兼容性
          auth.setAuth(legacyToken, legacyUser)
          await connectWs()
          // ✅ 修复：legacy 分支也给 initLobster 包 try/catch，
          // 否则 native 插件（如 SQLCipher）异常时整个登录流程崩。
          try {
            await initLobster(password.value)
          } catch (lobsterErr: any) {
            console.warn('[login-legacy] 龙虾初始化失败:', lobsterErr?.message || lobsterErr)
          }
          router.push('/ai')
          return
        }
        error.value = '后端未部署认证接口'
      } else {
        error.value = e.message || '登录失败'
      }
    } else {
      error.value = e.message || '登录失败'
    }
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.login-view {
  min-height: 100vh;
  background: var(--brand-gradient);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4);
}

.login-container {
  width: 100%;
  max-width: 400px;
  background: var(--bg-card);
  border-radius: var(--radius-lg);
  padding: var(--space-6) var(--space-4);
  border: 1px solid var(--border);
  box-shadow: var(--shadow-lg);
}

.logo-section {
  text-align: center;
  margin-bottom: var(--space-6);
}

.logo {
  font-size: 56px;
  margin-bottom: var(--space-3);
}

.app-title {
  font-size: var(--text-xl);
  font-weight: var(--font-weight-bold);
  color: var(--text-primary);
  margin: 0 0 var(--space-2) 0;
}

.app-subtitle {
  font-size: var(--text-sm);
  color: var(--text-secondary);
  margin: 0;
}

.login-form {
  margin-bottom: var(--space-5);
}

.form-group {
  margin-bottom: var(--space-3);
}

.form-group label {
  display: block;
  font-size: var(--text-sm);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  margin-bottom: var(--space-2);
}

.form-group input {
  width: 100%;
  padding: var(--space-3);
  font-size: var(--text-lg);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-subtle);
  color: var(--text-primary);
  transition: border-color 150ms;
  box-sizing: border-box;
}

.form-group input:focus {
  outline: none;
  border-color: var(--brand-primary);
  background: var(--bg-card);
}

.login-btn {
  width: 100%;
  padding: var(--space-3);
  font-size: var(--text-lg);
  font-weight: var(--font-weight-semibold);
  color: var(--text-inverse);
  background: var(--brand-gradient);
  border: none;
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: opacity 150ms;
}

.login-btn:active:not(:disabled) {
  opacity: 0.9;
}

.login-btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.error-message {
  margin-top: var(--space-3);
  padding: var(--space-3);
  background: var(--danger-bg);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  color: var(--danger);
  font-size: var(--text-sm);
  text-align: center;
}

.version-info {
  text-align: center;
  color: var(--text-muted);
  font-size: var(--text-xs);
}

.version-info p {
  margin: var(--space-1) 0;
}

.hint {
  color: var(--brand-primary);
  font-weight: var(--font-weight-medium);
}

.unlock-hint {
  color: var(--text-secondary);
  font-size: var(--text-sm);
  line-height: 1.6;
  text-align: center;
  margin-bottom: var(--space-3);
  padding: var(--space-3);
  background: var(--brand-bg);
  border-radius: var(--radius-md);
  border: 1px solid var(--border);
}
</style>
