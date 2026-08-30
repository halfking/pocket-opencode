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
          v-if="bioReady"
          class="login-btn bio-btn"
          :disabled="loading"
          @click="biometricLogin"
        >
          <span class="material-symbols-outlined" aria-hidden="true">fingerprint</span>
          {{ loading ? '登录中...' : '指纹登录' }}
        </button>

        <div v-if="bioReady" class="bio-divider"><span>或使用密码登录</span></div>

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
import {
  isBiometricAvailable,
  hasBiometricCredential,
  bindBiometricCredential,
  getBiometricCredential,
  unbindBiometricCredential,
} from '../../native/biometricAuth'
import MasterPasswordDialog from './MasterPasswordDialog.vue'
import { useCryptoConfig } from '../../stores/crypto-config'

const router = useRouter()
const auth = useAuthStore()

const username = ref('admin')
const password = ref('')
const loading = ref(false)
const error = ref('')

// 指纹登录（仅 Android 原生壳 + 已绑定凭据时出现）
const bioReady = ref(false)

// 场景：刷新页面后 token 持久（localStorage），但龙虾（crypto + SQLCipher）未初始化
// 此时需要用户重新输入主密码解锁本地数据，而非直接跳走。
const needUnlock = ref(false)
const unlockPassword = ref('')
const showMasterPasswordDialog = ref(false)
const cryptoConfig = useCryptoConfig()

onMounted(async () => {
  // 指纹登录入口：原生壳 + 设备已录入生物特征 + 本机已绑定凭据，三者齐备才显示
  try {
    if (await isBiometricAvailable() && await hasBiometricCredential()) {
      bioReady.value = true
    }
  } catch { /* 生物识别不可用时静默降级为密码登录 */ }

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
  await doLogin(username.value, password.value, { fromBiometric: false })
}

/** 指纹登录：验证通过后取回本机绑定凭据走同一登录流程。 */
async function biometricLogin() {
  loading.value = true
  error.value = ''
  let cred: { username: string; password: string }
  try {
    cred = await getBiometricCredential('使用指纹登录 OpenCode Pocket')
  } catch (e: any) {
    loading.value = false
    // 用户取消（系统弹窗 error code 13 = USER_CANCELED）不打扰；其它失败提示并降级为密码登录
    if (!(e?.message || '').includes('biometric error 13')) {
      error.value = `指纹验证失败：${e?.message || e}`
    }
    return
  }
  await doLogin(cred.username, cred.password, { fromBiometric: true })
}

/**
 * 统一登录流程。密码登录成功后顺手做指纹绑定（设备支持且未绑定时，
 * 尽力而为不阻塞）；指纹登录 401 说明绑定凭据已过期（服务端密码已改），
 * 自动解绑并提示改用密码。
 */
async function doLogin(u: string, p: string, opts: { fromBiometric: boolean }) {
  loading.value = true
  error.value = ''

  try {
    // Phase C: 服务端无状态认证（只为签发调用 /embed /llm 的 JWT）
    // S0-A 扩展：后端返回 { token, user, user_id, workspace_id }。
    const res = await http<{ token: string; user: string; user_id?: string; workspace_id?: string }>('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ username: u, password: p }),
    })
    if (res.user_id && res.workspace_id) {
      auth.setAuthWithWorkspace(res.token, res.user, res.user_id, res.workspace_id)
    } else {
      auth.setAuth(res.token, res.user)
    }
    // 🦞 认证成功后才建立 WS（此前模块加载不会自动连）
    await connectWs()

    // 密码登录成功后绑定指纹：后台尽力而为，不阻塞进入应用
    // （凭据明文只在闭包内短暂持有；用户取消或失败则下次登录再试）
    if (!opts.fromBiometric && !bioReady.value) {
      void (async () => {
        if (await isBiometricAvailable() && !(await hasBiometricCredential())) {
          const ok = await bindBiometricCredential(u, p)
          bioReady.value = ok
        }
      })()
    }

    // 本地数据初始化由独立的主密码对话框负责，不把登录密码隐式当作数据库密钥。
    if (!cryptoConfig.cfg.hasMasterPassword) {
      showMasterPasswordDialog.value = true
      return
    }
    router.push('/ai')
  } catch (e: any) {
    if (e instanceof ApiError) {
      if (e.status === 401) {
        if (opts.fromBiometric) {
          // 绑定的凭据已被服务端拒绝（密码修改过）→ 解绑并引导密码登录
          await unbindBiometricCredential()
          bioReady.value = false
          error.value = '指纹凭据已失效（密码可能已修改），已自动解绑，请用密码重新登录'
        } else {
          error.value = '登录失败：用户名或密码错误'
        }
      } else if (e.status === 404) {
        // 后端尚未部署 auth 路由时，回退到 legacy localStorage 兼容模式。
        if (u === 'admin' && p === 'admin') {
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
  min-height: 100%;
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

/* 指纹登录按钮复用 .login-btn，这里只补图标排版 */
.bio-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
}

.bio-btn .material-symbols-outlined {
  font-size: 22px;
}

.bio-divider {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: 4px 0;
  color: var(--text-tertiary, #999);
  font-size: 12px;
}

.bio-divider::before,
.bio-divider::after {
  content: '';
  flex: 1;
  height: 1px;
  background: var(--border, #ddd);
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
