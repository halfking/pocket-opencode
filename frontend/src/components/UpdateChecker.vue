<template>
  <!--
    非阻塞提示条：静默检查发现新版时出现。

    2026-10-06 之前没有这条，只有 onMounted 里直接弹的模态框。于是「有新版」
    这个事实**每次冷启动都重新弹一次**，把用户拦在首页——这与用户要的
    「无感更新」正好相反。发现新版不该打断当前操作，用户点一下再进详情。
  -->
  <div v-if="showBanner" class="update-banner" role="status">
    <span class="material-symbols-outlined" aria-hidden="true">system_update_alt</span>
    <span class="banner-text">{{ bannerText }}</span>
    <button type="button" class="banner-open" @click="openUpdateDialog">
      {{ prefetchReady ? '立即安装' : '查看' }}
    </button>
    <button type="button" class="banner-close" :aria-label="t('common.close')" @click="dismissBanner">
      <span class="material-symbols-outlined" aria-hidden="true">close</span>
    </button>
  </div>

  <div v-if="showUpdateDialog" class="update-overlay" @click="handleCancel">
    <div class="update-dialog" @click.stop>
      <!-- 更新图标 -->
      <div class="update-icon">🎉</div>

      <!-- 标题 -->
      <h2 class="update-title">发现新版本</h2>

      <!-- 版本信息 -->
      <div class="version-info">
        <div class="version-row">
          <span class="label">当前版本:</span>
          <span class="value">v{{ currentVersion }} (Build {{ currentBuild }})</span>
        </div>
        <div class="version-row">
          <span class="label">最新版本:</span>
          <span class="value highlight">v{{ updateInfo?.version }} (Build {{ updateInfo?.buildNumber }})</span>
        </div>
        <div class="version-row">
          <span class="label">更新大小:</span>
          <span class="value">{{ formatSize(updateInfo?.fileSize || 0) }}</span>
        </div>
        <div class="version-row">
          <span class="label">发布日期:</span>
          <span class="value">{{ updateInfo?.releaseDate }}</span>
        </div>
        <!-- 预下载状态：装的时候不用再等一次下载 -->
        <div v-if="prefetchReady" class="version-row">
          <span class="label">下载状态:</span>
          <span class="value highlight">已下载完成，可直接安装</span>
        </div>
      </div>

      <!-- 更新日志 -->
      <div class="changelog-section">
        <h3>更新内容</h3>
        <ul class="changelog-list">
          <li v-for="(item, index) in updateInfo?.changelog" :key="index">
            {{ item }}
          </li>
        </ul>
      </div>

      <!-- 强制更新提示 -->
      <div v-if="forceUpdate" class="force-update-notice">
        ⚠️ 此更新为强制更新，必须升级才能继续使用
      </div>

      <!-- 操作按钮 -->
      <div class="dialog-actions">
        <button 
          v-if="!forceUpdate" 
          class="cancel-btn" 
          @click="handleCancel"
        >
          稍后提醒
        </button>
        <button 
          class="update-btn" 
          @click="handleUpdate"
          :disabled="downloading"
        >
          {{ downloading ? '下载中...' : (prefetchReady ? '立即安装' : '立即更新') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  canDownloadApk,
  checkUpdate,
  downloadAPK,
  formatFileSize,
  prefetchApk,
  resolveAppVersion,
  type ResolvedAppVersion,
  type VersionInfo,
} from '../utils/version'
import {
  applyCheckFailure,
  applyCheckResult,
  applyPrefetched,
  canInstallPrefetched,
  decideCheck,
  emptyFingerprint,
  hasKnownUpdate,
  readFingerprint,
  safeLocalStorage,
  writeFingerprint,
  type UpdateFingerprint,
} from '../utils/update-state'

const { t } = useI18n()

const showUpdateDialog = ref(false)
const showBanner = ref(false)
const updateInfo = ref<VersionInfo | null>(null)
const forceUpdate = ref(false)
const downloading = ref(false)
const bannerDismissedFor = ref<string>('')
// 弹窗里的「当前版本」同样必须显示设备上真装的那个构建。
// 它过去读的是 TS 常量，于是弹窗会写「当前 v1.2.0 (Build 2)」而设备上装的是
// Build 3 —— 用户拿这一行去判断「我是不是最新版」时，被告知的信息本身就是错的。
const currentVersion = ref('')
const currentBuild = ref(0)

const fingerprint = ref<UpdateFingerprint>(emptyFingerprint())
const storage = safeLocalStorage()

/** 已预下载且仍然对得上目标版本 ⇒ 可以直接装。 */
const prefetchReady = computed(() => canInstallPrefetched(fingerprint.value))
const bannerKey = computed(() => `${fingerprint.value.latestVersion ?? ''}-${fingerprint.value.latestBuild ?? 0}`)
const bannerText = computed(() => {
  const v = fingerprint.value.latestVersion
  return v ? `发现新版本 v${v}（Build ${fingerprint.value.latestBuild ?? 0}）` : ''
})

/**
 * 启动时的**静默**检查。
 *
 * 与 2026-10-06 之前的三点差别：
 *   1. 先过 `decideCheck` 限频（默认 6h），不再是每次冷启动都打服务端；
 *   2. 发现新版**不弹模态框**，只出提示条 —— 不打断当前操作；
 *   3. 拿到新版后顺手把 APK 预下载到本地，用户点「立即安装」时不用再等。
 *
 * 任一步失败都静默：更新检查是锦上添花，绝不能让它把启动搞挂。
 */
onMounted(async () => {
  const me: ResolvedAppVersion = await resolveAppVersion()
  currentVersion.value = me.version
  currentBuild.value = me.buildNumber
  fingerprint.value = readFingerprint(storage) ?? emptyFingerprint()

  // 上次已知有新版 ⇒ 先把提示条挂出来，不用等这次网络往返。
  if (hasKnownUpdate(fingerprint.value) && !fingerprint.value.forceUpdate) {
    showBanner.value = bannerDismissedFor.value !== bannerKey.value
  }

  const decision = decideCheck(fingerprint.value, {
    now: Date.now(),
    identity: { version: me.version, buildNumber: me.buildNumber },
  })
  if (!decision.check) return

  await performUpdateCheck(me)
})

async function performUpdateCheck(identity?: ResolvedAppVersion) {
  const me = identity ?? (await resolveAppVersion())
  const now = Date.now()
  try {
    const response = await checkUpdate({ identity: me })
    fingerprint.value = applyCheckResult(fingerprint.value, {
      now,
      identity: { version: me.version, buildNumber: me.buildNumber },
      result: {
        hasUpdate: response.hasUpdate,
        latest: response.latest
          ? {
              version: response.latest.version,
              buildNumber: response.latest.buildNumber,
              fileSize: response.latest.fileSize,
            }
          : null,
        forceUpdate: response.forceUpdate,
      },
    })
    writeFingerprint(storage, fingerprint.value)

    if (response.hasUpdate && response.latest && canDownloadApk()) {
      updateInfo.value = response.latest
      forceUpdate.value = response.forceUpdate
      // 强制更新仍然直接拦（服务端明确要求），普通更新走提示条。
      if (response.forceUpdate) {
        showUpdateDialog.value = true
      } else {
        showBanner.value = bannerDismissedFor.value !== bannerKey.value
      }
      void maybePrefetch()
    }
  } catch (error) {
    // 失败只推进时间戳并保留既有信息，**不**抹掉「有新版」这个事实。
    fingerprint.value = applyCheckFailure(fingerprint.value, {
      now,
      identity: { version: me.version, buildNumber: me.buildNumber },
    })
    writeFingerprint(storage, fingerprint.value)
    console.warn('检查更新失败（已保留上次结果）:', error)
  }
}

/** 已经预下载好就别重复下。 */
async function maybePrefetch() {
  if (!updateInfo.value?.downloadUrl) return
  if (canInstallPrefetched(fingerprint.value)) return
  const result = await prefetchApk(updateInfo.value.downloadUrl, updateInfo.value.fileSize)
  if (!result.ok || !result.path || result.bytes === undefined) {
    // 预下载失败不提示：点「立即更新」时仍能走浏览器下载。
    console.warn('APK 预下载未完成:', result.error)
    return
  }
  fingerprint.value = applyPrefetched(fingerprint.value, {
    now: Date.now(),
    path: result.path,
    bytes: result.bytes,
  })
  writeFingerprint(storage, fingerprint.value)
}

function openUpdateDialog() {
  showBanner.value = false
  showUpdateDialog.value = true
}

function dismissBanner() {
  showBanner.value = false
  bannerDismissedFor.value = bannerKey.value
}

function handleUpdate() {
  if (!updateInfo.value) return

  downloading.value = true
  
  // HarmonyOS Phase A has no HAP distribution channel. It can report a
  // release but must never initiate an APK download.
  if (!canDownloadApk()) {
    downloading.value = false
    return
  }

  // 预下载已完成 ⇒ 直接装本地那份，不再走一次网络下载。
  if (canInstallPrefetched(fingerprint.value)) {
    const p = fingerprint.value.prefetchedApk
    void openLocalApk(p?.path ?? '')
  } else if (!downloadAPK(updateInfo.value.downloadUrl)) {
    downloading.value = false
    return
  }
  
  // 延迟重置状态
  setTimeout(() => {
    downloading.value = false
    if (!forceUpdate.value) {
      showUpdateDialog.value = false
    }
  }, 2000)
}

/** 用可分享 URI 打开本地已下载的安装包。失败时静默退回远端下载。 */
async function openLocalApk(path: string) {
  if (!path) return
  try {
    const { getPocketNative } = await import('../native/pocket-native')
    const fs = getPocketNative().filesystem
    const uri = await fs?.getUri(path, 'data')
    if (uri?.uri) window.open(uri.uri, '_blank')
  } catch (e) {
    console.warn('打开本地安装包失败，退回远端下载:', (e as Error).message)
    if (updateInfo.value) downloadAPK(updateInfo.value.downloadUrl)
  }
}

function handleCancel() {
  if (forceUpdate.value) return
  showUpdateDialog.value = false
  // 「稍后提醒」= 本次会话不再提示条；下次冷启动若仍需提示会重新出现。
  dismissBanner()
}

function formatSize(bytes: number): string {
  return formatFileSize(bytes)
}

// 暴露方法供外部调用（设置页「检查更新」显式触发 ⇒ force + 弹窗）
defineExpose({
  checkUpdate: async () => {
    showBanner.value = false
    await performUpdateCheck()
    if (updateInfo.value) showUpdateDialog.value = true
    return fingerprint.value
  },
  /** 仅供测试注入指纹，避免组件测试依赖真实 localStorage。 */
  __setFingerprintForTest(fp: UpdateFingerprint) { fingerprint.value = fp },
})
</script>

<style scoped>
/* 非阻塞提示条：贴在状态栏下方，不遮挡任何交互。 */
.update-banner {
  position: fixed;
  top: calc(var(--app-safe-top, 0px) + var(--space-2));
  left: var(--space-3);
  right: var(--space-3);
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-3);
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  box-shadow: var(--shadow-md);
  z-index: var(--z-toast);
  animation: bannerIn 0.2s ease-out;
}

@keyframes bannerIn {
  from { transform: translateY(-12px); opacity: 0; }
  to { transform: translateY(0); opacity: 1; }
}

.banner-text {
  flex: 1;
  font-size: var(--text-sm);
  color: var(--text-primary);
  min-width: 0;
}

.banner-open,
.banner-close {
  border: none;
  background: transparent;
  cursor: pointer;
  color: var(--brand-primary);
  font-size: var(--text-sm);
  font-weight: var(--font-weight-semibold);
  padding: var(--space-1);
  transition: opacity 150ms;
}

.banner-open:active,
.banner-close:active { opacity: 0.7; }

.banner-close { color: var(--text-secondary); display: flex; }

.update-overlay {
  position: fixed;
  inset: 0;
  background: var(--overlay);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4);
  z-index: var(--z-update);
  animation: fadeIn 0.3s;
}

@keyframes fadeIn {
  from { opacity: 0; }
  to { opacity: 1; }
}

.update-dialog {
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  padding: var(--space-5) var(--space-4);
  width: 100%;
  max-width: 400px;
  max-height: 80vh;
  overflow-y: auto;
  box-shadow: var(--shadow-lg);
  animation: slideUp 0.3s;
}

@keyframes slideUp {
  from { transform: translateY(50px); opacity: 0; }
  to { transform: translateY(0); opacity: 1; }
}

.update-icon {
  font-size: 56px;
  text-align: center;
  margin-bottom: var(--space-4);
}

.update-title {
  font-size: var(--text-xl);
  font-weight: var(--font-weight-bold);
  text-align: center;
  color: var(--text-primary);
  margin: 0 0 var(--space-4) 0;
}

.version-info {
  background: var(--bg-subtle);
  border-radius: var(--radius-md);
  padding: var(--space-3);
  margin-bottom: var(--space-4);
}

.version-row {
  display: flex;
  justify-content: space-between;
  padding: var(--space-2) 0;
  border-bottom: 1px solid var(--border);
}

.version-row:last-child { border-bottom: none; }

.version-row .label {
  font-size: var(--text-sm);
  color: var(--text-secondary);
}

.version-row .value {
  font-size: var(--text-sm);
  color: var(--text-primary);
  font-weight: var(--font-weight-medium);
}

.version-row .value.highlight {
  color: var(--brand-primary);
  font-weight: var(--font-weight-semibold);
}

.changelog-section { margin-bottom: var(--space-4); }

.changelog-section h3 {
  font-size: var(--text-lg);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  margin: 0 0 var(--space-3) 0;
}

.changelog-list { list-style: none; padding: 0; margin: 0; }

.changelog-list li {
  font-size: var(--text-sm);
  color: var(--text-secondary);
  padding: var(--space-2) 0;
  padding-left: var(--space-2);
  border-left: 3px solid var(--brand-primary);
  margin-bottom: var(--space-2);
  line-height: 1.5;
}

.force-update-notice {
  background: var(--warning-bg);
  border: 1px solid var(--warning);
  border-radius: var(--radius-sm);
  padding: var(--space-3);
  margin-bottom: var(--space-4);
  font-size: var(--text-sm);
  color: var(--warning);
  text-align: center;
}

.dialog-actions { display: flex; gap: var(--space-3); }

.cancel-btn,
.update-btn {
  flex: 1;
  padding: var(--space-3);
  font-size: var(--text-lg);
  font-weight: var(--font-weight-semibold);
  border: none;
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: opacity 150ms;
}

.cancel-btn {
  background: var(--bg-subtle);
  color: var(--text-secondary);
}

.cancel-btn:active { background: var(--border); }

.update-btn {
  background: var(--brand-gradient);
  color: var(--text-inverse);
  box-shadow: var(--shadow-md);
}

.update-btn:active:not(:disabled) { opacity: 0.9; }

.update-btn:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}
</style>
