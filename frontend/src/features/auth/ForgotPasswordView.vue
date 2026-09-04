<template>
  <div class="forgot-view">
    <div class="forgot-container">
      <div class="header">
        <div class="logo">🔴</div>
        <h1 class="title">重置密码</h1>
        <p class="subtitle">通过邮箱验证码重置 Redclaw 密码（密码统一由 RedClaw 管理）</p>
      </div>

      <ol class="steps" aria-label="重置步骤">
        <li :class="{ active: step >= 1, done: step > 1 }">1. 输入邮箱</li>
        <li :class="{ active: step >= 2, done: step > 2 }">2. 验证码 + 新密码</li>
        <li :class="{ active: step >= 3 }">3. 完成</li>
      </ol>

      <div v-if="step === 1" class="form">
        <div class="form-group">
          <label>邮箱</label>
          <input
            v-model="email"
            type="email"
            placeholder="注册时使用的邮箱"
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
          <label>新密码</label>
          <input
            v-model="newPassword"
            type="password"
            placeholder="≥8 位，含字母与数字"
          />
        </div>
        <div class="form-group">
          <label>确认密码</label>
          <input
            v-model="confirmPassword"
            type="password"
            placeholder="再次输入新密码"
            @keyup.enter="submit"
          />
        </div>
        <button
          class="primary-btn"
          :disabled="!code || !newPassword || !confirmPassword || loading"
          @click="submit"
        >
          {{ loading ? '提交中...' : '重置密码' }}
        </button>
      </div>

      <div v-else-if="step === 3" class="form success">
        <div class="success-icon">✓</div>
        <p class="success-text">密码重置成功</p>
        <p class="hint">请使用新密码重新登录</p>
        <button class="primary-btn" @click="goLogin">返回登录</button>
      </div>

      <div v-if="error" class="error-message">{{ error }}</div>

      <p v-if="step < 3" class="back-link">
        <router-link to="/login">← 返回登录</router-link>
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { sendCode, forgotPassword } from '../../api/auth'

const router = useRouter()
const step = ref<1 | 2 | 3>(1)
const email = ref('')
const code = ref('')
const newPassword = ref('')
const confirmPassword = ref('')
const loading = ref(false)
const error = ref('')
const cooldown = ref(0)
const debugCode = ref('')
let timer: ReturnType<typeof setInterval> | null = null

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
    const res = await sendCode(email.value, 'reset')
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

async function submit() {
  error.value = ''
  if (!code.value) {
    error.value = '请输入验证码'
    return
  }
  if (newPassword.value !== confirmPassword.value) {
    error.value = '两次输入的密码不一致'
    return
  }
  if (newPassword.value.length < 8) {
    error.value = '密码至少 8 位'
    return
  }
  loading.value = true
  try {
    await forgotPassword(email.value, code.value, newPassword.value)
    step.value = 3
  } catch (e: any) {
    error.value = e?.body?.error || e?.message || '重置失败'
  } finally {
    loading.value = false
  }
}

function goLogin() {
  router.replace('/login')
}
</script>

<style scoped>
.forgot-view {
  min-height: 100%;
  background: var(--brand-gradient);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4);
}
.forgot-container {
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
.primary-btn { width: 100%; padding: var(--space-3); font-size: var(--text-lg); font-weight: var(--font-weight-semibold); color: var(--text-inverse); background: var(--brand-gradient); border: none; border-radius: var(--radius-md); cursor: pointer; }
.primary-btn:disabled { opacity: 0.6; cursor: not-allowed; }
.hint { font-size: var(--text-xs); color: var(--text-secondary); margin: var(--space-1) 0 0 0; }
.resend { font-size: var(--text-xs); color: var(--text-secondary); margin: var(--space-1) 0 0 0; }
.link-btn { background: transparent; border: none; color: var(--brand-primary); font-size: inherit; padding: 0; cursor: pointer; text-decoration: underline; }
.link-btn:disabled { color: var(--text-tertiary, #999); cursor: not-allowed; text-decoration: none; }
code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; background: var(--bg-subtle); padding: 2px 6px; border-radius: 4px; font-size: 0.95em; }
.success { align-items: center; text-align: center; padding: var(--space-4) 0; }
.success-icon { width: 56px; height: 56px; border-radius: 50%; background: var(--success, #2f9e44); color: #fff; font-size: 32px; line-height: 56px; text-align: center; }
.success-text { font-size: var(--text-lg); font-weight: var(--font-weight-semibold); color: var(--text-primary); margin: var(--space-2) 0 var(--space-1) 0; }
.error-message { margin-top: var(--space-3); padding: var(--space-3); background: var(--danger-bg); border: 1px solid var(--border); border-radius: var(--radius-md); color: var(--danger); font-size: var(--text-sm); text-align: center; }
.back-link { text-align: center; margin-top: var(--space-4); font-size: var(--text-sm); }
.back-link a { color: var(--brand-primary); text-decoration: none; }
</style>
