<template>
  <div v-if="open" class="dialog-mask" @click.self="onCancel">
    <section class="dialog" role="dialog" aria-modal="true" aria-labelledby="email-verify-title">
      <h2 id="email-verify-title">{{ title }}</h2>
      <p class="hint">{{ hintText }}</p>

      <div v-if="step === 1" class="form">
        <div class="form-group">
          <label>邮箱地址</label>
          <input
            v-model="email"
            type="email"
            placeholder="请输入邮箱"
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
            @keyup.enter="submit"
          />
          <p v-if="debugCode" class="hint">调试模式：验证码 = <code>{{ debugCode }}</code></p>
          <p class="resend">
            没收到？
            <button type="button" class="link-btn" :disabled="cooldown > 0" @click="requestCode">
              {{ cooldown > 0 ? `${cooldown}s 后重发` : '重新发送' }}
            </button>
          </p>
        </div>
        <button
          class="primary-btn"
          :disabled="!code || loading"
          @click="submit"
        >
          {{ loading ? '验证中...' : '确认' }}
        </button>
      </div>

      <p v-if="error" class="error">{{ error }}</p>

      <div class="actions">
        <button v-if="!required" class="ghost" :disabled="loading" @click="onCancel">
          {{ step === 1 ? '跳过' : '取消' }}
        </button>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { sendCode, updateEmail } from '../../api/auth'
import { judgeCodeDelivery } from './code-delivery'

const props = withDefaults(
  defineProps<{
    open: boolean
    required?: boolean
    initialEmail?: string
  }>(),
  {
    required: false,
    initialEmail: '',
  }
)

const emit = defineEmits<{
  (event: 'close'): void
  (event: 'success', email: string): void
  (event: 'skip'): void
}>()

const step = ref<1 | 2>(1)
const email = ref(props.initialEmail)
const code = ref('')
const loading = ref(false)
const error = ref('')
const cooldown = ref(0)
const debugCode = ref('')
let timer: ReturnType<typeof setInterval> | null = null

const title = computed(() => (step.value === 1 ? '设置邮箱' : '验证邮箱'))
const hintText = computed(() =>
  step.value === 1
    ? '请填写你的邮箱地址，用于密码找回和安全验证'
    : `验证码已发送到 ${email.value}`
)

watch(() => props.open, (newVal) => {
  if (newVal) {
    step.value = 1
    email.value = props.initialEmail
    code.value = ''
    error.value = ''
    debugCode.value = ''
    if (timer) {
      clearInterval(timer)
      timer = null
      cooldown.value = 0
    }
  }
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
    const verdict = judgeCodeDelivery(res)
    if (!verdict.advance) {
      error.value = verdict.error
      return
    }
    code.value = ''
    debugCode.value = res.debug_code || ''
    step.value = 2
    startCooldown()
  } catch (e: any) {
    console.warn('[auth] 发送验证码失败:', e?.body?.error || e?.message || e)
    error.value = '发送验证码失败，请稍后重试'
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
  loading.value = true
  try {
    await updateEmail(email.value, code.value)
    emit('success', email.value)
  } catch (e: any) {
    console.warn('[auth] 邮箱验证失败:', e?.body?.error || e?.message || e)
    error.value = e?.body?.error || '验证失败，请确认验证码是否正确'
  } finally {
    loading.value = false
  }
}

function onCancel() {
  if (loading.value) return
  if (step.value === 1 && !props.required) {
    emit('skip')
  } else {
    emit('close')
  }
}
</script>

<style scoped>
.dialog-mask {
  position: fixed;
  inset: 0 0 var(--kb-inset, 0px) 0;
  z-index: var(--z-alert);
  display: grid;
  place-items: center;
  padding: 20px;
  background: rgba(0, 0, 0, 0.45);
}
.dialog {
  width: min(100%, 420px);
  display: grid;
  gap: 12px;
  padding: 24px;
  border-radius: var(--radius-lg);
  background: var(--bg-card);
  color: var(--text-primary);
  box-shadow: var(--shadow-lg);
}
h2 {
  margin: 0;
  font-size: var(--text-xl);
  font-weight: var(--font-weight-bold);
}
.hint {
  color: var(--text-secondary);
  font-size: var(--text-sm);
  line-height: 1.5;
  margin: 0;
}
.form {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.form-group {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.form-group label {
  font-size: var(--text-sm);
  font-weight: var(--font-weight-semibold);
}
.form-group input {
  width: 100%;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-base);
  color: inherit;
  font-size: var(--text-base);
}
.form-group input:focus {
  outline: none;
  border-color: var(--brand-primary);
}
.resend {
  font-size: var(--text-xs);
  color: var(--text-secondary);
  margin: 0;
}
.link-btn {
  background: transparent;
  border: none;
  color: var(--brand-primary);
  font-size: inherit;
  padding: 0;
  cursor: pointer;
  text-decoration: underline;
}
.link-btn:disabled {
  color: var(--text-tertiary, #999);
  cursor: not-allowed;
  text-decoration: none;
}
code {
  font-family: var(--font-mono);
  background: var(--bg-subtle);
  padding: 2px 6px;
  border-radius: 4px;
  font-size: 0.95em;
}
.primary-btn {
  width: 100%;
  padding: 10px 16px;
  border-radius: var(--radius-md);
  border: 1px solid transparent;
  background: var(--brand-primary);
  color: #fff;
  font-weight: var(--font-weight-semibold);
  cursor: pointer;
}
.primary-btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}
.error {
  color: var(--danger);
  font-size: var(--text-sm);
  margin: 0;
}
.actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
.ghost {
  padding: 9px 16px;
  border-radius: var(--radius-md);
  border: 1px solid var(--border);
  background: transparent;
  color: inherit;
  cursor: pointer;
}
.ghost:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}
</style>
