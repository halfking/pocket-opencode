<template>
  <div v-if="open" class="dialog-mask" @click.self="close">
    <section class="dialog" role="dialog" aria-modal="true" aria-labelledby="master-password-title">
      <h2 id="master-password-title">{{ mode === 'create' ? '创建主密码' : '修改主密码' }}</h2>
      <p class="hint">主密码独立于登录密码，用于解锁本地数据。请妥善保管，丢失后无法恢复。</p>
      <input v-model="password" type="password" autocomplete="new-password" placeholder="主密码（至少 8 位）" />
      <input v-model="confirm" type="password" autocomplete="new-password" placeholder="再次输入主密码" @keyup.enter="submit" />
      <input v-model="hint" type="text" placeholder="密码提示（可选）" />
      <p v-if="error" class="error">{{ error }}</p>
      <div class="actions">
        <button class="ghost" @click="close">取消</button>
        <button class="primary" :disabled="submitting" @click="submit">
          {{ submitting ? '初始化中…' : '确认' }}
        </button>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { initLobster } from '../../native/lobster-init'
import { useCryptoConfig } from '../../stores/crypto-config'

withDefaults(defineProps<{ open: boolean; mode: 'create' | 'change' }>(), { mode: 'create' })
const emit = defineEmits<{ (event: 'close'): void; (event: 'success', password: string): void }>()
const cryptoConfig = useCryptoConfig()
const password = ref('')
const confirm = ref('')
const hint = ref('')
const error = ref('')
const submitting = ref(false)

function close() {
  if (!submitting.value) emit('close')
}

async function submit() {
  error.value = ''
  if (password.value.length < 8) {
    error.value = '主密码至少需要 8 位'
    return
  }
  if (password.value !== confirm.value) {
    error.value = '两次输入的主密码不一致'
    return
  }

  submitting.value = true
  try {
    await initLobster(password.value)
    cryptoConfig.setMasterPassword(hint.value.trim() || null)
    emit('success', password.value)
    password.value = ''
    confirm.value = ''
    hint.value = ''
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '本地数据初始化失败'
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped>
.dialog-mask { position: fixed; inset: 0; z-index: 100; display: grid; place-items: center; padding: 20px; background: rgba(0, 0, 0, .45); }
.dialog { width: min(100%, 420px); display: grid; gap: 12px; padding: 24px; border-radius: var(--radius-lg); background: var(--bg-card); color: var(--text-primary); box-shadow: var(--shadow-lg); }
.hint { color: var(--text-secondary); font-size: 13px; line-height: 1.5; }
.dialog input { width: 100%; padding: 10px 12px; border: 1px solid var(--border); border-radius: var(--radius-md); background: var(--bg-base); color: inherit; }
.error { color: var(--danger); font-size: 13px; }
.actions { display: flex; justify-content: flex-end; gap: 8px; }
.actions button { padding: 9px 16px; border-radius: var(--radius-md); border: 1px solid var(--border); cursor: pointer; }
.ghost { background: transparent; color: inherit; }
.primary { background: var(--brand-primary); color: #fff; border-color: transparent !important; }
</style>
