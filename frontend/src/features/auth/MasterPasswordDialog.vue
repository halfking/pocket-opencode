<template>
  <div v-if="open" class="dialog-mask" @click.self="close">
    <section class="dialog" role="dialog" aria-modal="true" aria-labelledby="master-password-title">
      <!-- ⚠§859/D26：mode='unlock' 是**本机加密配置读取失败**后的专用形态。
           它必须存在，否则用户明明早就设过主密码，却会看到一个标题写着
           「创建主密码」、提示写着「丢失后无法恢复」的对话框；
           而 initLobster 第一步就是 verifyMasterPassword
           ⇒ 输错就只得到「主密码不正确」⇒ 被卡在「创建→不正确→再创建」的循环里。 -->
      <h2 id="master-password-title">{{ title }}</h2>
      <p class="hint">{{ hintText }}</p>
      <p v-if="mode === 'unlock'" class="recovered" role="status">{{ recoveredNote }}</p>
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
import { computed, ref } from 'vue'
import { initLobster } from '../../native/lobster-init'
import { persistMasterSecretIfBound } from '../../native/biometricAuth'
import { useCryptoConfig } from '../../stores/crypto-config'

const props = withDefaults(defineProps<{ open: boolean; mode: 'create' | 'change' | 'unlock' }>(), { mode: 'create' })

const title = computed(() =>
  props.mode === 'create' ? '创建主密码'
    : props.mode === 'change' ? '修改主密码'
      : '输入主密码解锁',
)
// ⚠ 不能叫 `hint` —— 弹窗里已有一个 `hint` ref（密码提示输入框），
//   同名会直接撞车（第一版就撞了，typecheck 报重复标识符）。
const hintText = computed(() =>
  props.mode === 'unlock'
    ? '本机加密配置无法读取，请输入你原来设置的主密码解锁本地数据。'
    : '主密码独立于登录密码，用于解锁本地数据。请妥善保管，丢失后无法恢复。',
)
const recoveredNote = '检测到本机加密配置损坏（内容已另存备查）。如果想不起主密码，本地数据将无法恢复。'
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
    // ⚠§866/D31：写失败不再冒泡（冒泡会被本函数外层的 catch 说成「初始化失败」，
    //   而初始化其实已经成功了）。这里如实区分「解锁成功但没存住」。
    if (cryptoConfig.persistFailed) {
      error.value = '主密码已通过验证并解锁成功，但设置没能写入本地存储（存储满或不可用）——下次启动可能需要重新验证。'
    }
    await persistMasterSecretIfBound(password.value)
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
.dialog-mask { position: fixed; /* 底边随 --kb-inset 抬升：键盘在场时浮层收在键盘上沿之上，弹层内的输入框与确认按钮不会被盖住。同 BottomSheet.vue。 */ inset: 0 0 var(--kb-inset, 0px) 0; z-index: var(--z-alert); display: grid; place-items: center; padding: 20px; background: rgba(0, 0, 0, .45); }
.dialog { width: min(100%, 420px); display: grid; gap: 12px; padding: 24px; border-radius: var(--radius-lg); background: var(--bg-card); color: var(--text-primary); box-shadow: var(--shadow-lg); }
.hint { color: var(--text-secondary); font-size: var(--text-smd); line-height: 1.5; }
.dialog input { width: 100%; padding: 10px 12px; border: 1px solid var(--border); border-radius: var(--radius-md); background: var(--bg-base); color: inherit; }
.error { color: var(--danger); font-size: var(--text-smd); }
.actions { display: flex; justify-content: flex-end; gap: 8px; }
.actions button { padding: 9px 16px; border-radius: var(--radius-md); border: 1px solid var(--border); cursor: pointer; }
.ghost { background: transparent; color: inherit; }
.primary { background: var(--brand-primary); color: #fff; border-color: transparent !important; }
</style>
