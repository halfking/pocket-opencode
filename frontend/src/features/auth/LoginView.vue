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

      <MasterPasswordDialog
        :open="showMasterPasswordDialog"
        mode="create"
        @close="showMasterPasswordDialog = false"
        @success="onMasterPasswordCreated"
      />

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
import MasterPasswordDialog from './MasterPasswordDialog.vue'
import { useCryptoConfig } from '../../stores/crypto-config'

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
const showMasterPasswordDialog = ref(false)
const cryptoConfig = useCryptoConfig()

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
    unlockPassword.value = ''
    const redirect = typeof router.currentRoute.value.query.redirect === 'string'
      ? router.currentRoute.value.query.redirect
      : '/ai'
    router.replace(redirect)
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

function onMasterPasswordCreated() {
  showMasterPasswordDialog.value = false
  const redirect = typeof router.currentRoute.value.query.redirect === 'string'
    ? router.currentRoute.value.query.redirect
    : '/ai'
  router.replace(redirect)
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
    // S0-A 扩展：后端返回 { token, user, user_id, workspace_id }。
    const res = await http<{ token: string; user: string; user_id?: string; workspace_id?: string }>('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username: username.value, password: password.value }),
    })
    if (res.user_id && res.workspace_id) {
      auth.setAuthWithWorkspace(res.token, res.user, res.user_id, res.workspace_id)
    } else {
      auth.setAuth(res.token, res.user)
    }
    // 🦞 认证成功后才建立 WS（此前模块加载不会自动连）
    await connectWs()
    // 本地数据初始化由独立的主密码对话框负责，不把登录密码隐式当作数据库密钥。
    if (!cryptoConfig.cfg.hasMasterPassword) {
      showMasterPasswordDialog.value = true
      return
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
          if (!cryptoConfig.cfg.hasMasterPassword) {
            showMasterPasswordDialog.value = true
            return
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
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
}

.login-container {
  width: 100%;
  max-width: 400px;
  background: white;
  border-radius: var(--radius-lg);      /* 修改：使用变量 (10px，原 20px) */
  padding: 32px 24px;                   /* 修改：32px 24px（原 40px 30px） */
  box-shadow: 0 10px 40px rgba(0, 0, 0, 0.2);
}

.logo-section {
  text-align: center;
  margin-bottom: 40px;
}

.logo {
  font-size: 56px;                      /* 修改：56px（原 64px） */
  margin-bottom: 16px;                  /* 修改：16px（原 20px） */
}

.app-title {
  font-size: 24px;                      /* 修改：24px（原 28px） */
  font-weight: 700;
  color: #333;
  margin: 0 0 8px 0;                    /* 修改：8px（原 10px） */
}

.app-subtitle {
  font-size: 14px;
  color: #666;
  margin: 0;
}

.login-form {
  margin-bottom: 30px;
}

.form-group {
  margin-bottom: 16px;                  /* 修改：16px（原 20px） */
}

.form-group label {
  display: block;
  font-size: 14px;
  font-weight: 600;
  color: #333;
  margin-bottom: 8px;
}

.form-group input {
  width: 100%;
  padding: 12px 14px;                   /* 修改：12px 14px（原 14px 16px） */
  font-size: 16px;
  border: 2px solid #e0e0e0;
  border-radius: var(--radius-md);      /* 修改：使用变量 (8px，原 12px) */
  transition: all 0.3s;
  box-sizing: border-box;
}

.form-group input:focus {
  outline: none;
  border-color: #667eea;
  background: #f8f9ff;
}

.login-btn {
  width: 100%;
  padding: 14px;                        /* 修改：14px（原 16px） */
  font-size: 16px;
  font-weight: 600;
  color: white;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  border: none;
  border-radius: var(--radius-md);      /* 修改：使用变量 (8px，原 12px) */
  cursor: pointer;
  transition: all 0.3s;
}

.login-btn:hover:not(:disabled) {
  transform: translateY(-2px);
  box-shadow: 0 6px 20px rgba(102, 126, 234, 0.4);
}

.login-btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.error-message {
  margin-top: 15px;
  padding: 12px;
  background: #fee;
  border: 1px solid #fcc;
  border-radius: var(--radius-md);      /* 修改：使用变量 (8px) */
  color: #c33;
  font-size: 14px;
  text-align: center;
}

.version-info {
  text-align: center;
  color: #999;
  font-size: 12px;
}

.version-info p {
  margin: 5px 0;
}

.hint {
  color: #667eea;
  font-weight: 500;
}

.unlock-hint {
  color: #555;
  font-size: 13px;
  line-height: 1.6;
  text-align: center;
  margin-bottom: 16px;
  padding: 12px;
  background: #f8f9ff;
  border-radius: var(--radius-md);      /* 修改：使用变量 (8px) */
  border: 1px solid #e0e7ff;
}
</style>
