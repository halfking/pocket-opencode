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

        <!-- 相机（占位） -->
        <div class="setting-item clickable" @click="showComingSoon('相机')">
          <div class="setting-icon"><span class="material-symbols-outlined">photo_camera</span></div>
          <div class="setting-content">
            <div class="setting-label">相机</div>
            <div class="setting-value">即将支持：扫码、拍摄附件</div>
          </div>
          <div class="status-chip placeholder">即将支持</div>
        </div>

        <!-- 相册（占位） -->
        <div class="setting-item clickable" @click="showComingSoon('相册')">
          <div class="setting-icon"><span class="material-symbols-outlined">photo_library</span></div>
          <div class="setting-content">
            <div class="setting-label">相册</div>
            <div class="setting-value">即将支持：当前发图走系统文件选择器，无需授权</div>
          </div>
          <div class="status-chip placeholder">即将支持</div>
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
          Android 的指纹与人脸共用同一个系统能力，由 WebAuthn 在系统弹窗中自动选择；
          生物识别数据不会上传到服务器，仅用于本地解锁密码箱与登录。
        </p>
      </div>

      <!-- 帮助提示 -->
      <div class="settings-section note-section">
        <p class="hint">
          如果某项权限显示"未授权"，点击该项即可重新申请；已被系统拒绝的权限会引导你到系统设置开启。
        </p>
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
import { useBiometricStatus } from '../../composables/useBiometricStatus'
import {
  isBiometricAvailable,
  hasBiometricCredential,
  unbindBiometricCredential,
} from '../../native/biometricAuth'
import { useAppSettings } from '../../composables/useAppSettings'

const router = useRouter()
const mic = useMicPermission()
const notif = useNotificationPermission()
const bio = useBiometricStatus()
const appSettings = useAppSettings()

const refreshing = ref(false)

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
  if (s === 'denied') return '未授权'
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
  if (s === 'denied') return 'warn'
  return 'muted'
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
    return '设备未录入指纹/人脸（或系统不支持），请先在系统设置中录入'
  }
  if (nativeBiometricBound.value) return '指纹登录已开启：登录页可直接指纹登录；点击可解绑'
  return '密码登录成功后会自动绑定指纹；绑定后登录页可用指纹一键登录'
})

// --- 交互 ---
function goBack() {
  router.back()
}

async function refreshAll() {
  refreshing.value = true
  try {
    const jobs: Promise<unknown>[] = [mic.recheck(), notif.recheck(), refreshBiometricBinding()]
    // 服务端 WebAuthn 凭据数只在 Web 分支展示，原生平台不发这次（可能 401 的）请求
    if (!Capacitor.isNativePlatform()) jobs.push(bio.refresh())
    await Promise.all(jobs)
  } finally {
    refreshing.value = false
  }
}

async function handleMicClick() {
  const s = mic.state.value
  if (s === 'granted' || s === 'unavailable') return
  // 未授权：先尝试系统弹窗；若已彻底被拒（NotAllowedError 后状态保持 denied），跳系统设置
  const ok = await mic.ensure()
  if (!ok && mic.state.value === 'denied') {
    await appSettings.openAppDetails()
  }
}

async function handleNotificationClick() {
  const s = notif.state.value
  if (s === 'granted' || s === 'unavailable') return
  const result = await notif.ensure()
  if (result === 'denied') {
    await appSettings.openAppDetails()
  }
}

async function handleBiometricClick() {
  // Web 环境无原生绑定，仅提示
  if (!Capacitor.isNativePlatform()) {
    alert('当前为 Web 环境，指纹登录需在 Android App 中使用。')
    return
  }
  if (!nativeBiometricAvailable.value) {
    alert('设备未录入指纹/人脸。请先在系统设置中录入后重试。')
    return
  }
  if (nativeBiometricBound.value) {
    if (confirm('解绑后登录页将不再出现指纹登录，需重新用密码登录并绑定。确定解绑？')) {
      await unbindBiometricCredential()
      await refreshBiometricBinding()
    }
    return
  }
  alert('还未绑定：退出登录后在登录页用密码登录一次，将自动弹出指纹验证完成绑定。')
}

function showComingSoon(label: string) {
  alert(`${label}功能尚未上线。后续版本会在这里提供授权入口。`)
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
</style>
