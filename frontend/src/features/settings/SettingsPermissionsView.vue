<template>
  <div class="permissions-view">
    <!-- 顶部返回栏 -->
    <div class="page-header">
      <button class="back-btn" @click="goBack" aria-label="返回">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>
      <h1 class="page-title">权限与隐私</h1>
    </div>

    <div class="permissions-container">
      <!-- 运行时权限 -->
      <div class="settings-section">
        <div class="section-head">
          <h2>运行时权限</h2>
          <button class="icon-btn" :disabled="refreshing" @click="refreshAll" aria-label="刷新状态">
            <span class="material-symbols-outlined" :class="{ spinning: refreshing }">refresh</span>
          </button>
        </div>

        <!-- 麦克风 -->
        <div class="setting-item clickable" @click="handleMicClick">
          <div class="setting-icon"><span class="material-symbols-outlined">mic</span></div>
          <div class="setting-content">
            <div class="setting-label">麦克风</div>
            <div class="setting-value">会议记录、语音输入、语音笔记需要麦克风</div>
          </div>
          <div :class="['status-chip', micStateClass]">{{ micLabel }}</div>
        </div>

        <!-- 通知 -->
        <div class="setting-item clickable" @click="handleNotificationClick">
          <div class="setting-icon"><span class="material-symbols-outlined">notifications</span></div>
          <div class="setting-content">
            <div class="setting-label">通知</div>
            <div class="setting-value">会话审批、会议提醒需要通知权限（Android 13+）</div>
          </div>
          <div :class="['status-chip', notifStateClass]">{{ notifLabel }}</div>
        </div>

        <!-- 相机 -->
        <div class="setting-item clickable" @click="handleCameraClick">
          <div class="setting-icon"><span class="material-symbols-outlined">photo_camera</span></div>
          <div class="setting-content">
            <div class="setting-label">相机</div>
            <div class="setting-value">扫码、拍摄附件需要相机</div>
          </div>
          <div :class="['status-chip', camera.stateClass]">{{ camera.label }}</div>
        </div>

        <!-- 相册 -->
        <div class="setting-item clickable" @click="handlePhotosClick">
          <div class="setting-icon"><span class="material-symbols-outlined">photo_library</span></div>
          <div class="setting-content">
            <div class="setting-label">相册</div>
            <div class="setting-value">发送图片、选择附件需要访问相册</div>
          </div>
          <div :class="['status-chip', photos.stateClass]">{{ photos.label }}</div>
        </div>
      </div>

      <!-- 生物识别登录 -->
      <div class="settings-section">
        <h2>生物识别登录</h2>
        <div class="setting-item clickable" @click="handleBiometricClick">
          <div class="setting-icon"><span class="material-symbols-outlined">fingerprint</span></div>
          <div class="setting-content">
            <div class="setting-label">指纹 / 人脸</div>
            <div class="setting-value">{{ biometricSubtitle }}</div>
          </div>
          <div :class="['status-chip', biometricStateClass]">{{ biometricLabel }}</div>
        </div>
        <p class="hint">
          Android 的指纹与人脸共用同一个系统能力，点击未绑定项会弹出系统验证并完成本机绑定；
          生物识别数据不会上传到服务器，仅用于登录、主密码解锁与本地密码箱。
        </p>
      </div>

      <!-- 帮助提示 -->
      <div class="settings-section note-section">
        <p class="hint">
          未授权时点击可再次弹出系统申请。已被禁止的项会打开对应的系统设置并定位到本应用，便于手动开启。
        </p>
      </div>
    </div>

    <div v-if="bindOpen" class="bind-overlay" @click.self="closeBind">
      <div class="bind-card" role="dialog" aria-modal="true" aria-labelledby="bind-title">
        <h2 id="bind-title">绑定指纹 / 人脸</h2>
        <p class="bind-copy">输入当前账号的登录密码，随后在系统弹窗中验证指纹或人脸。</p>
        <input
          v-model="bindPassword"
          class="bind-input"
          type="password"
          autocomplete="current-password"
          placeholder="登录密码"
          :disabled="bindBusy"
          @keyup.enter="confirmBind"
        />
        <p v-if="bindError" class="bind-error">{{ bindError }}</p>
        <div class="bind-actions">
          <button class="bind-btn ghost" type="button" :disabled="bindBusy" @click="closeBind">取消</button>
          <button class="bind-btn primary" type="button" :disabled="bindBusy || !bindPassword" @click="confirmBind">
            {{ bindBusy ? '绑定中…' : '绑定' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { App } from '@capacitor/app'
import { Capacitor } from '@capacitor/core'
import { useMicPermission } from '../../composables/useMicPermission'
import { useNotificationPermission } from '../../composables/useNotificationPermission'
import { useNativePermission } from '../../composables/useNativePermission'
import { useBiometricStatus } from '../../composables/useBiometricStatus'
import {
  isBiometricAvailable,
  hasBiometricCredential,
  unbindBiometricCredential,
  bindBiometricLogin,
} from '../../native/biometricAuth'
import {
  isBiometricUserCancel,
  loginDisplayName,
  needsBiometricEnrollment,
} from '../../native/biometric-errors'
import { useAuthStore } from '../../stores/auth'
import { useAppSettings, type RuntimePermissionName } from '../../composables/useAppSettings'
import {
  permissionRowClickAction,
  type SettingsPermissionName,
} from '../../composables/permission-settings'
import type { PermissionStatus } from '../../composables/permission-action'

const router = useRouter()
const mic = useMicPermission()
const notif = useNotificationPermission()
const camera = useNativePermission('camera')
const photos = useNativePermission('photos')
const bio = useBiometricStatus()
const appSettings = useAppSettings()
const auth = useAuthStore()

const refreshing = ref(false)
const bindOpen = ref(false)
const bindPassword = ref('')
const bindError = ref('')
const bindBusy = ref(false)

// --- 指纹登录绑定（原生壳：本机绑定态；Web：服务端 WebAuthn 凭据数） ---
const nativeBiometricAvailable = ref(false)
const nativeBiometricBound = ref(false)

async function refreshBiometricBinding() {
  if (!Capacitor.isNativePlatform()) {
    nativeBiometricAvailable.value = false
    nativeBiometricBound.value = false
    return
  }
  nativeBiometricAvailable.value = await isBiometricAvailable()
  nativeBiometricBound.value = nativeBiometricAvailable.value
    ? await hasBiometricCredential()
    : false
}

// --- 麦克风 ---
const micLabel = computed(() => {
  const s = mic.state.value
  if (s === 'granted') return '已授权'
  if (s === 'denied') return mic.canRequestAgain.value ? '未授权' : '已拒绝'
  if (s === 'unavailable') return '不支持'
  return '未检测'
})
const micStateClass = computed(() => {
  const s = mic.state.value
  if (s === 'granted') return 'ok'
  if (s === 'denied') return 'warn'
  return 'muted'
})

// --- 通知 ---
const notifLabel = computed(() => notif.label.value)
const notifStateClass = computed(() => {
  const s = notif.state.value
  if (s === 'granted') return 'ok'
  if (s === 'unavailable' || s === 'unknown') return 'muted'
  return 'warn'
})

// --- 生物识别 ---
const biometricLabel = computed(() => {
  // 原生壳：以本机指纹登录绑定态为准
  if (Capacitor.isNativePlatform()) {
    if (!nativeBiometricAvailable.value) return '不支持'
    return nativeBiometricBound.value ? '已绑定' : '未绑定'
  }
  // Web：服务端 WebAuthn 凭据数
  const a = bio.availability.value
  if (a === 'loading') return '查询中…'
  if (a === 'ready') return bio.credentialCount.value > 0 ? `已注册 ${bio.credentialCount.value} 个` : '未启用'
  if (a === 'unauthenticated') return '未登录'
  if (a === 'unavailable') return '不可用'
  return '检测中'
})
const biometricStateClass = computed(() => {
  if (Capacitor.isNativePlatform()) {
    if (nativeBiometricBound.value) return 'ok'
    return nativeBiometricAvailable.value ? 'warn' : 'muted'
  }
  const a = bio.availability.value
  if (a === 'ready' && bio.credentialCount.value > 0) return 'ok'
  if (a === 'ready' || a === 'unauthenticated') return 'warn'
  return 'muted'
})
const biometricSubtitle = computed(() => {
  if (!Capacitor.isNativePlatform()) {
    return '当前为 Web 环境；生物识别需 Android 原生壳或受支持的浏览器（WebAuthn）。'
  }
  if (!nativeBiometricAvailable.value) {
    return '设备未录入指纹/人脸（或系统不支持），点击可打开系统设置录入'
  }
  if (nativeBiometricBound.value) return '已开启：登录可用指纹，解锁主密码也可点认证；点击可解绑'
  return '点击后输入登录密码，验证指纹/人脸即可绑定'
})

// --- 交互 ---
function goBack() {
  router.back()
}

async function refreshAll() {
  refreshing.value = true
  try {
    const jobs: Promise<unknown>[] = [
      mic.recheck(),
      notif.recheck(),
      camera.recheck(),
      photos.recheck(),
      refreshBiometricBinding(),
    ]
    // 服务端 WebAuthn 凭据数只在 Web 分支展示，原生平台不发这次（可能 401 的）请求
    if (!Capacitor.isNativePlatform()) jobs.push(bio.refresh())
    await Promise.all(jobs)
  } finally {
    refreshing.value = false
  }
}

async function openSystemSettings(name: SettingsPermissionName) {
  const opened = await appSettings.openPermissionSettings(name)
  if (!opened) {
    alert('无法打开系统设置。请到系统设置中找到本应用后手动开启对应权限。')
  }
}

function runtimeFollowUp(name: RuntimePermissionName): {
  status: PermissionStatus | 'unknown'
  canRequestAgain: boolean
} {
  if (name === 'microphone') return { status: mic.state.value, canRequestAgain: mic.canRequestAgain.value }
  if (name === 'notifications') return { status: notif.state.value, canRequestAgain: notif.canRequestAgain.value }
  if (name === 'camera') return { status: camera.state.value, canRequestAgain: camera.canRequestAgain.value }
  return { status: photos.state.value, canRequestAgain: photos.canRequestAgain.value }
}

async function handleRuntimeClick(
  name: RuntimePermissionName,
  status: PermissionStatus | 'unknown',
  canRequestAgain: boolean,
  ensure: () => Promise<unknown>,
) {
  const action = permissionRowClickAction({ kind: name, status, canRequestAgain })
  if (action === 'none') return
  if (action === 'open-settings') {
    await openSystemSettings(name)
    return
  }
  await ensure()
  const followUp = runtimeFollowUp(name)
  if (
    permissionRowClickAction({
      kind: name,
      status: followUp.status,
      canRequestAgain: followUp.canRequestAgain,
      afterRequest: true,
    }) === 'open-settings'
  ) {
    await openSystemSettings(name)
  }
}

async function handleMicClick() {
  await handleRuntimeClick('microphone', mic.state.value, mic.canRequestAgain.value, () => mic.ensure())
}

async function handleNotificationClick() {
  await handleRuntimeClick(
    'notifications',
    notif.state.value,
    notif.canRequestAgain.value,
    () => notif.ensure(),
  )
}

async function handleCameraClick() {
  await handleRuntimeClick('camera', camera.state.value, camera.canRequestAgain.value, () => camera.ensure())
}

async function handlePhotosClick() {
  await handleRuntimeClick('photos', photos.state.value, photos.canRequestAgain.value, () => photos.ensure())
}

async function handleBiometricClick() {
  if (!Capacitor.isNativePlatform()) {
    alert('当前为 Web 环境，指纹登录需在 Android App 中使用。')
    return
  }
  if (nativeBiometricBound.value) {
    if (confirm('解绑后登录页将不再出现指纹登录，需重新用密码登录并绑定。确定解绑？')) {
      await unbindBiometricCredential()
      await refreshBiometricBinding()
    }
    return
  }
  bindPassword.value = ''
  bindError.value = ''
  bindOpen.value = true
}

function closeBind() {
  if (bindBusy.value) return
  bindOpen.value = false
  bindPassword.value = ''
  bindError.value = ''
}

async function confirmBind() {
  const username = loginDisplayName(auth.user || '')
  const password = bindPassword.value.trim()
  if (!username) {
    bindError.value = '未找到当前登录账号，请重新登录后再绑定'
    return
  }
  if (!password) {
    bindError.value = '请输入登录密码'
    return
  }
  bindBusy.value = true
  bindError.value = ''
  try {
    await bindBiometricLogin(username, password, '绑定指纹 / 人脸登录')
    bindOpen.value = false
    bindPassword.value = ''
    await refreshBiometricBinding()
  } catch (e: unknown) {
    const message = e instanceof Error ? e.message : String(e)
    if (isBiometricUserCancel(message)) return
    if (needsBiometricEnrollment(message) || !(await isBiometricAvailable())) {
      bindOpen.value = false
      await openSystemSettings('biometric')
      return
    }
    bindError.value = message.includes('invalid username or password')
      ? '密码不能为空，请重新输入'
      : '绑定失败，请确认密码正确后重试'
  } finally {
    bindBusy.value = false
  }
}

onMounted(async () => {
  await refreshAll()

  // 从系统设置返回时自动重新检查（Android 上开放授权页会走 app pause/resume）
  if (Capacitor.isNativePlatform()) {
    App.addListener('resume', () => {
      void refreshAll()
    })
  }
})
</script>

<style scoped>
.permissions-view {
  min-height: 100%;
  display: flex;
  flex-direction: column;
}

.page-header {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-3) var(--space-2);
  border-bottom: 1px solid var(--border);
  background: var(--bg-card);
}

.back-btn {
  width: 36px;
  height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  background: transparent;
  color: var(--text-primary);
  border-radius: var(--radius-sm);
  cursor: pointer;
}

.back-btn:active {
  background: var(--bg-subtle);
}

.page-title {
  font-size: var(--text-lg);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  margin: 0;
}

.permissions-container {
  flex: 1;
  padding: var(--space-3);
}

.settings-section {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  padding: var(--spacing-card-padding);
  margin-bottom: var(--spacing-list-gap);
}

.settings-section h2 {
  font-size: var(--text-xs);
  font-weight: var(--font-weight-semibold);
  color: var(--text-muted);
  text-transform: uppercase;
  margin: 0 0 var(--space-3) 0;
  letter-spacing: 0.5px;
}

.section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin: 0 0 var(--space-3) 0;
}
.section-head h2 {
  margin: 0;
}

.icon-btn {
  width: 28px;
  height: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  border-radius: var(--radius-sm);
}
.icon-btn:disabled {
  opacity: 0.5;
}
.icon-btn .material-symbols-outlined.spinning {
  animation: spin 1s linear infinite;
}
@keyframes spin {
  to { transform: rotate(360deg); }
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

.setting-item.clickable {
  cursor: pointer;
}
.setting-item.clickable:active {
  opacity: 0.7;
}

.setting-icon {
  width: 36px;
  height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--bg-subtle);
  border-radius: var(--radius-sm);
  flex-shrink: 0;
  color: var(--brand-primary);
}

.setting-icon .material-symbols-outlined {
  font-size: 20px;
}

.setting-content {
  flex: 1;
  min-width: 0;
}

.setting-label {
  font-size: var(--text-base);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  margin-bottom: 2px;
}

.setting-value {
  font-size: var(--text-sm);
  color: var(--text-secondary);
}

.status-chip {
  padding: 2px var(--space-2);
  border-radius: var(--radius-full);
  font-size: var(--text-xs);
  font-weight: var(--font-weight-semibold);
  flex-shrink: 0;
}
.status-chip.ok {
  background: var(--success-bg);
  color: var(--success);
}
.status-chip.warn {
  background: var(--warning-bg);
  color: var(--warning);
}
.status-chip.muted {
  background: var(--bg-subtle);
  color: var(--text-muted);
}
.status-chip.placeholder {
  background: var(--bg-subtle);
  color: var(--brand-primary);
}

.hint {
  font-size: var(--text-xs);
  color: var(--text-muted);
  line-height: 1.6;
  margin: var(--space-2) 0 0;
}

.note-section {
  background: transparent;
  border: none;
  padding: var(--space-2);
}

.bind-overlay {
  position: fixed;
  inset: 0;
  z-index: 40;
  background: var(--color-bg-overlay, rgba(0, 0, 0, 0.45));
  display: flex;
  align-items: flex-end;
  justify-content: center;
  padding: var(--space-3);
}

.bind-card {
  width: 100%;
  max-width: 420px;
  background: var(--bg-card);
  border-radius: var(--radius-md);
  padding: var(--spacing-card-padding);
  margin-bottom: var(--space-4);
}

.bind-card h2 {
  margin: 0 0 var(--space-2);
  font-size: var(--text-lg);
}

.bind-copy,
.bind-error {
  font-size: var(--text-sm);
  line-height: 1.5;
  margin: 0 0 var(--space-3);
  color: var(--text-secondary);
}

.bind-error {
  color: var(--danger);
}

.bind-input {
  width: 100%;
  box-sizing: border-box;
  padding: var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--bg-subtle);
  color: var(--text-primary);
  margin-bottom: var(--space-3);
}

.bind-actions {
  display: flex;
  gap: var(--space-2);
}

.bind-btn {
  flex: 1;
  padding: var(--space-3);
  border: none;
  border-radius: var(--radius-md);
  font-weight: var(--font-weight-semibold);
}

.bind-btn.ghost {
  background: var(--bg-subtle);
  color: var(--text-secondary);
}

.bind-btn.primary {
  background: var(--brand-gradient);
  color: var(--text-inverse);
}

.bind-btn:disabled {
  opacity: 0.5;
}
</style>
