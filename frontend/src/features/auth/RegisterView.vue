<!--
  RegisterView — 独立注册页（/register，匿名可访问）。

  分步流程与 ForgotPasswordView 同视觉语言：
    1. 验证邮箱（send-code purpose=register，60s 冷却）
    2. 验证码 + 用户名 + 密码 → registerUser → 注册即登录

  登录收尾与 LoginView.completeAuth 完全一致：
  setAuthWithWorkspace → connectWs → 无主密码时弹 MasterPasswordDialog → /ai。

  核心流程（验证码签发 / 邮件 / 建号）由 RedClaw 后端实现，前端只调用
  /api/auth/send-code + /api/auth/register（api/auth.ts 已封装）。
-->
<template>
  <div class="register-view">
    <div class="register-container">
      <div class="header">
        <div class="logo">🦞</div>
        <h1 class="title">注册账号</h1>
        <p class="subtitle">使用邮箱验证码创建账号（核心流程由 RedClaw 提供）</p>
      </div>

      <ol class="steps" aria-label="注册步骤">
        <li :class="{ active: step >= 1, done: step > 1 }">1. 验证邮箱</li>
        <li :class="{ active: step >= 2 }">2. 设置账号</li>
      </ol>

      <div v-if="step === 1" class="form">
        <div class="form-group">
          <label>邮箱</label>
          <input
            v-model="email"
            type="email"
            name="email"
            autocomplete="email"
            placeholder="用作登录账号"
            @keyup.enter="requestCode"
          />
        </div>
        <button
          class="primary-btn"
          :disabled="!email || cooldown > 0 || loading"
          @click="requestCode"
        >
          {{ cooldown > 0 ? `${cooldown}s 后可重发` : '发送验证码' }}
        </button>
      </div>

      <div v-else-if="step === 2" class="form">
        <p class="sent-hint">验证码已发送至 <strong>{{ email }}</strong></p>
        <div class="form-group">
          <label>验证码</label>
          <input
            v-model="code"
            type="text"
            inputmode="numeric"
            maxlength="6"
            placeholder="6 位数字验证码"
          />
          <p v-if="debugCode" class="hint">调试模式：验证码 = <code>{{ debugCode }}</code></p>
          <p class="resend">
            没收到？
            <button type="button" class="link-btn" :disabled="cooldown > 0" @click="requestCode">
              {{ cooldown > 0 ? `${cooldown}s 后重发` : '重新发送' }}
            </button>
          </p>
        </div>
        <div class="form-group">
          <label>用户名</label>
          <input
            v-model="username"
            type="text"
            name="username"
            autocomplete="username"
            placeholder="3-32 字符，字母/数字/_.-"
          />
        </div>
        <div class="form-group">
          <label>密码</label>
          <input
            v-model="password"
            type="password"
            name="new-password"
            autocomplete="new-password"
            placeholder="≥8 位，含字母与数字"
          />
        </div>
        <div class="form-group">
          <label>确认密码</label>
          <input
            v-model="confirmPassword"
            type="password"
            autocomplete="new-password"
            placeholder="再次输入密码"
            @keyup.enter="submit"
          />
        </div>
        <button
          class="primary-btn"
          :disabled="!code || !username || !password || !confirmPassword || loading"
          @click="submit"
        >
          {{ loading ? '注册中...' : '注册并登录' }}
        </button>
      </div>

      <div v-if="error" class="error-message">{{ error }}</div>

      <p class="back-link">
        <router-link to="/login">已有账号？返回登录</router-link>
      </p>
    </div>

    <MasterPasswordDialog
      :open="showMasterPasswordDialog"
      mode="create"
      @close="showMasterPasswordDialog = false"
      @success="onMasterPasswordCreated"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '../../stores/auth'
import { connectWs } from '../../api/websocket'
import { sendCode, registerUser } from '../../api/auth'
import MasterPasswordDialog from './MasterPasswordDialog.vue'
import { useCryptoConfig } from '../../stores/crypto-config'

const router = useRouter()
const auth = useAuthStore()
const cryptoConfig = useCryptoConfig()

const step = ref<1 | 2>(1)
const email = ref('')
const code = ref('')
const username = ref('')
const password = ref('')
const confirmPassword = ref('')
const loading = ref(false)
const error = ref('')
const cooldown = ref(0)
const debugCode = ref('')
const showMasterPasswordDialog = ref(false)
let timer: ReturnType<typeof setInterval> | null = null

onUnmounted(() => {
  if (timer) clearInterval(timer)
})

function startCooldown() {
  cooldown.value = 60
  if (timer) clearInterval(timer)
  timer = setInterval(() => {
    cooldown.value--
    if (cooldown.value <= 0 && timer) {
      clearInterval(timer)
      timer = null
    }
  }, 1000)
}

async function requestCode() {
  error.value = ''
  if (!email.value) {
    error.value = '请输入邮箱'
    return
  }
  loading.value = true
  try {
    const res = await sendCode(email.value, 'register')
    code.value = ''
    debugCode.value = res.debug_code || ''
    step.value = 2
    startCooldown()
  } catch (e: any) {
    error.value = e?.body?.error || e?.message || '发送验证码失败'
  } finally {
    loading.value = false
  }
}

/** 客户端校验与后端 validateUsername/validatePassword 规则一致，错误前置。 */
function validate(): string {
  if (!code.value) return '请输入验证码'
  const name = username.value.trim()
  if (name.length < 3 || name.length > 32) return '用户名长度需在 3-32 之间'
  if (!/^[A-Za-z0-9._-]+$/.test(name)) return '用户名仅支持字母、数字、下划线、点、中划线'
  if (password.value.length < 8) return '密码至少 8 位'
  if (!/[0-9]/.test(password.value) || !/[A-Za-z]/.test(password.value)) return '密码需包含字母和数字'
  if (password.value !== confirmPassword.value) return '两次输入的密码不一致'
  return ''
}

async function submit() {
  error.value = validate()
  if (error.value) return
  loading.value = true
  try {
    const res = await registerUser({
      email: email.value,
      code: code.value,
      username: username.value.trim(),
      password: password.value,
    })
    await completeAuth(res.token, res.user, res.user_id, res.workspace_id)
  } catch (e: any) {
    if (e?.body?.error) {
      error.value = e.body.error
    } else if (e?.status === 409) {
      error.value = '邮箱或用户名已被注册'
    } else if (e?.status === 400) {
      error.value = e?.message || '验证码错误或已过期'
    } else {
      error.value = e?.message || '注册失败'
    }
  } finally {
    loading.value = false
  }
}

/** 与 LoginView.completeAuth 一致的注册成功收尾链。 */
async function completeAuth(token: string, user: string, userId?: string, workspaceId?: string) {
  if (userId && workspaceId) {
    auth.setAuthWithWorkspace(token, user, userId, workspaceId)
  } else {
    auth.setAuth(token, user)
  }
  await connectWs()
  if (!cryptoConfig.cfg.hasMasterPassword) {
    showMasterPasswordDialog.value = true
    return
  }
  router.push('/ai')
}

function onMasterPasswordCreated() {
  showMasterPasswordDialog.value = false
  router.replace('/ai')
}
</script>

<style scoped>
.register-view {
  min-height: 100%;
  background: var(--brand-gradient);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4);
}
.register-container {
  width: 100%;
  max-width: 400px;
  background: var(--bg-card);
  border-radius: var(--radius-lg);
  padding: var(--space-6) var(--space-4);
  border: 1px solid var(--border);
  box-shadow: var(--shadow-lg);
}
.header { text-align: center; margin-bottom: var(--space-5); }
.logo { font-size: 48px; margin-bottom: var(--space-2); }
.title { font-size: var(--text-xl); font-weight: var(--font-weight-bold); color: var(--text-primary); margin: 0 0 var(--space-1) 0; }
.subtitle { font-size: var(--text-sm); color: var(--text-secondary); margin: 0; }
.steps { display: flex; gap: 8px; list-style: none; padding: 0; margin: 0 0 var(--space-5) 0; font-size: var(--text-xs); color: var(--text-tertiary, #999); }
.steps li { flex: 1; text-align: center; padding: 6px 0; border-bottom: 2px solid var(--border); transition: border-color 150ms, color 150ms; }
.steps li.active { color: var(--brand-primary); border-bottom-color: var(--brand-primary); }
.steps li.done { color: var(--success, #2f9e44); border-bottom-color: var(--success, #2f9e44); }
.form { display: flex; flex-direction: column; gap: var(--space-3); }
.form-group { display: flex; flex-direction: column; gap: var(--space-1); }
.form-group label { font-size: var(--text-sm); font-weight: var(--font-weight-semibold); color: var(--text-primary); }
.form-group input { padding: var(--space-3); font-size: var(--text-lg); border: 1px solid var(--border); border-radius: var(--radius-md); background: var(--bg-subtle); color: var(--text-primary); }
.form-group input:focus { outline: none; border-color: var(--brand-primary); background: var(--bg-card); }
.sent-hint { font-size: var(--text-sm); color: var(--text-secondary); margin: 0; }
.sent-hint strong { color: var(--text-primary); word-break: break-all; }
.primary-btn { width: 100%; padding: var(--space-3); font-size: var(--text-lg); font-weight: var(--font-weight-semibold); color: var(--text-inverse); background: var(--brand-gradient); border: none; border-radius: var(--radius-md); cursor: pointer; }
.primary-btn:disabled { opacity: 0.6; cursor: not-allowed; }
.hint { font-size: var(--text-xs); color: var(--text-secondary); margin: var(--space-1) 0 0 0; }
.resend { font-size: var(--text-xs); color: var(--text-secondary); margin: var(--space-1) 0 0 0; }
.link-btn { background: transparent; border: none; color: var(--brand-primary); font-size: inherit; padding: 0; cursor: pointer; text-decoration: underline; }
.link-btn:disabled { color: var(--text-tertiary, #999); cursor: not-allowed; text-decoration: none; }
code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; background: var(--bg-subtle); padding: 2px 6px; border-radius: 4px; font-size: 0.95em; }
.error-message { margin-top: var(--space-3); padding: var(--space-3); background: var(--danger-bg); border: 1px solid var(--border); border-radius: var(--radius-md); color: var(--danger); font-size: var(--text-sm); text-align: center; }
.back-link { text-align: center; margin-top: var(--space-4); font-size: var(--text-sm); }
.back-link a { color: var(--brand-primary); text-decoration: none; }
</style>
