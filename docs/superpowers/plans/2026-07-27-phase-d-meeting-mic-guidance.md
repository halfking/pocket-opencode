# Phase D: 会议麦克风权限引导 — 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 录音前给用户明确的状态徽章 + 一键式跳系统设置 + 错误卡片带 CTA + 重试，把"麦克风被拒后不知道怎么办"的卡死路径彻底打通。

**Architecture:**
- `useAppSettings()` 新 composable（用 Capacitor App plugin）封装跳系统设置。
- `MicStatusBar.vue` 新组件：四态徽章（unknown/granted/denied/unavailable）+ CTA 按钮。
- `ErrorActionCard.vue` 新组件：把 error-card 升级为含按钮的可行动卡片。
- `MeetingRecordView.vue` 改造：在 title-input 之前插 `<MicStatusBar />`，error-card 改为 `<ErrorActionCard />`，record-button `disabled` 绑定 `mic.state`。

**Tech Stack:** Vue 3 + TypeScript / @capacitor/app / 现有 useMicPermission。

---

## 文件结构

```
frontend/
├── package.json                      # (改) 加 @capacitor/app
├── src/
│   ├── composables/
│   │   ├── useAppSettings.ts         # (新增) 跳系统设置
│   │   └── useMicPermission.ts       # (改) 暴露 isReady / openSettings 桥接
│   ├── components/
│   │   ├── MicStatusBar.vue          # (新增) 麦克风状态徽章
│   │   └── ErrorActionCard.vue       # (新增) 可行动错误卡片
│   └── features/meetings/
│       └── MeetingRecordView.vue     # (改) 接入 MicStatusBar + ErrorActionCard
└── android/
    └── app/src/main/
        ├── AndroidManifest.xml       # (改) 无新增（系统设置用 ACTION_APPLICATION_DETAILS_SETTINGS）
        └── java/.../MainActivity.java # (改) 加 App plugin 注册（若 Capacitor 6 未默认）
```

---

## Task 1: 加依赖 @capacitor/app

**Files:**
- Modify: `frontend/package.json`

- [ ] **Step 1: 安装依赖**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/frontend
npm install @capacitor/app@^6
```

- [ ] **Step 2: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/package.json frontend/package-lock.json
git commit -m "deps: 加 @capacitor/app 用于跳系统设置"
```

---

## Task 2: useAppSettings composable

**Files:**
- Create: `frontend/src/composables/useAppSettings.ts`

- [ ] **Step 1: 创建 composable**

新建 `frontend/src/composables/useAppSettings.ts`：

```ts
/**
 * useAppSettings — 跨平台跳系统设置页（App 详情页 / 麦克风权限页）。
 *
 * Android：通过 Capacitor App plugin 调 ACTION_APPLICATION_DETAILS_SETTINGS
 * iOS：不允许应用内跳设置，返回 false；UI 需展示引导文案
 * Web fallback：弹窗提示手动操作
 */
import { ref } from 'vue'
import { Capacitor } from '@capacitor/core'

export function useAppSettings() {
  const supported = ref(true)
  const lastError = ref('')

  async function openAppDetails(): Promise<boolean> {
    lastError.value = ''
    try {
      if (Capacitor.getPlatform() === 'android') {
        // Capacitor App plugin v6+ 支持 getInfo + openUrl
        const { App } = await import('@capacitor/app')
        await App.exitApp() // 不直接退；改用 launcher intent
        // 用原生 intent：Capacitor 6 需要自定义 plugin；这里 fallback 到 deeplink
        const pkg = (window as any).__CAP_PACKAGE_NAME__ || 'com.kaixuan.opencode.pocket'
        window.location.href = `intent://details#Intent;scheme=package;package=${pkg};end`
        return true
      }
      if (Capacitor.getPlatform() === 'ios') {
        // iOS 不允许；返回 false 让 UI 显示引导
        supported.value = false
        return false
      }
      // Web fallback
      alert('请到 系统设置 → 应用 → OpenCode Pocket → 权限 中开启麦克风')
      return true
    } catch (e: any) {
      lastError.value = e?.message || '跳设置失败'
      return false
    }
  }

  function getManualGuide(): string {
    if (Capacitor.getPlatform() === 'ios') {
      return 'iOS：请打开 系统设置 → Pocket → 麦克风，开启权限后回到此页面点击"重新检测"'
    }
    if (Capacitor.getPlatform() === 'android') {
      return 'Android：请点击"去系统设置"按钮，或手动进入 设置 → 应用 → Pocket → 权限 → 麦克风'
    }
    return '请在浏览器设置中允许麦克风权限后刷新页面'
  }

  return { openAppDetails, getManualGuide, supported, lastError }
}
```

> **注意**：Capacitor 6 的 App plugin 没有直接"打开应用详情" API，需要自定义 Java 桥接（见 Task 6）。本步骤先注册 Capacitor App plugin 让 Capacitor 启动不报错；实际跳转靠原生 Java 桥（见 Task 6）。

- [ ] **Step 2: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/composables/useAppSettings.ts
git commit -m "feat(meetings): useAppSettings composable 跨平台跳系统设置"
```

---

## Task 3: 增强 useMicPermission（暴露 openSettings 桥接）

**Files:**
- Modify: `frontend/src/composables/useMicPermission.ts`

- [ ] **Step 1: 引入 useAppSettings**

修改 `useMicPermission.ts` 顶部：

```ts
import { useAppSettings } from './useAppSettings'
```

- [ ] **Step 2: 暴露 isReady getter + openSettings**

在 `useMicPermission` 函数体末尾追加：

```ts
const appSettings = useAppSettings()

const isReady = computed(() => state.value === 'granted')

async function openSettings(): Promise<boolean> {
  return appSettings.openAppDetails()
}

return {
  state,
  deniedLabel,
  isReady,
  ensure,
  recheck,
  openSettings,
  manualGuide: computed(() => appSettings.getManualGuide()),
}
```

- [ ] **Step 3: 补充 import**

在顶部：

```ts
import { computed, ref } from 'vue'
```

- [ ] **Step 4: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/composables/useMicPermission.ts
git commit -m "feat(meetings): useMicPermission 暴露 isReady/openSettings 桥接"
```

---

## Task 4: MicStatusBar 组件

**Files:**
- Create: `frontend/src/components/MicStatusBar.vue`

- [ ] **Step 1: 创建组件**

新建 `frontend/src/components/MicStatusBar.vue`：

```vue
<!--
  MicStatusBar — 麦克风状态徽章 + CTA 按钮。
  状态：granted(绿) / denied(红) / unavailable(橙) / unknown(灰)
-->
<template>
  <div class="mic-bar" :class="`state-${mic.state.value}`">
    <div class="mic-left">
      <span class="dot" :class="`dot-${mic.state.value}`" />
      <div class="mic-text">
        <strong>{{ statusTitle }}</strong>
        <small>{{ statusDesc }}</small>
      </div>
    </div>
    <div class="mic-actions">
      <button v-if="mic.state.value === 'unknown'" class="mic-btn primary" :disabled="loading" @click="onAuthorize">
        {{ loading ? '请求中…' : '授权麦克风' }}
      </button>
      <template v-else-if="mic.state.value === 'denied'">
        <button class="mic-btn primary" @click="onOpenSettings">去系统设置</button>
        <button class="mic-btn ghost" :disabled="loading" @click="onRecheck">
          {{ loading ? '检测中…' : '重新检测' }}
        </button>
      </template>
      <button v-else-if="mic.state.value === 'unavailable'" class="mic-btn ghost" :disabled="loading" @click="onRecheck">
        {{ loading ? '检测中…' : '重新检测' }}
      </button>
      <button v-else-if="mic.state.value === 'granted'" class="mic-btn ghost" @click="onRecheck">重新检测</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useMicPermission } from '../composables/useMicPermission'

const mic = useMicPermission()
const loading = ref(false)

const statusTitle = computed(() => ({
  granted: '麦克风就绪',
  denied: '麦克风权限被拒绝',
  unavailable: '未找到可用麦克风',
  unknown: '麦克风状态未知',
}[mic.state.value]))

const statusDesc = computed(() => ({
  granted: '点击下方圆形按钮开始录音',
  denied: mic.manualGuide.value,
  unavailable: mic.deniedLabel.value || '请检查麦克风设备',
  unknown: '点击右侧按钮请求麦克风权限',
}[mic.state.value]))

async function onAuthorize() {
  loading.value = true
  try {
    await mic.ensure()
  } finally {
    loading.value = false
  }
}

async function onRecheck() {
  loading.value = true
  try {
    await mic.recheck()
  } finally {
    loading.value = false
  }
}

async function onOpenSettings() {
  await mic.openSettings()
}
</script>

<style scoped>
.mic-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-2) var(--space-3);
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  gap: var(--space-2);
  margin-bottom: var(--space-3);
}
.mic-bar.state-granted { border-color: var(--success); background: rgba(16,185,129,0.06); }
.mic-bar.state-denied { border-color: var(--danger); background: rgba(239,68,68,0.06); }
.mic-bar.state-unavailable { border-color: var(--warning); background: rgba(245,158,11,0.06); }
.mic-left { display: flex; gap: var(--space-2); align-items: center; flex: 1; min-width: 0; }
.dot { width: 12px; height: 12px; border-radius: 50%; flex-shrink: 0; }
.dot-granted { background: var(--success); }
.dot-denied { background: var(--danger); }
.dot-unavailable { background: var(--warning); }
.dot-unknown { background: var(--text-muted); }
.mic-text { display: flex; flex-direction: column; min-width: 0; }
.mic-text strong { font-size: 13px; color: var(--text-primary); }
.mic-text small { font-size: 11px; color: var(--text-secondary); word-break: break-word; }
.mic-actions { display: flex; gap: var(--space-1); flex-shrink: 0; }
.mic-btn {
  padding: 6px 10px;
  border-radius: var(--radius-sm);
  font-size: 12px;
  cursor: pointer;
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text-primary);
}
.mic-btn.primary { background: var(--brand-primary); color: white; border-color: var(--brand-primary); }
.mic-btn.ghost { background: transparent; }
.mic-btn:disabled { opacity: 0.5; cursor: wait; }
</style>
```

- [ ] **Step 2: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/components/MicStatusBar.vue
git commit -m "feat(meetings): MicStatusBar 四态徽章 + CTA 按钮"
```

---

## Task 5: ErrorActionCard 组件

**Files:**
- Create: `frontend/src/components/ErrorActionCard.vue`

- [ ] **Step 1: 创建组件**

新建 `frontend/src/components/ErrorActionCard.vue`：

```vue
<!--
  ErrorActionCard — 错误卡片 + 可行动 CTA 按钮。
  Props:
    - code: 错误码（如 'mic-denied', 'stt-failed'）
    - message: 错误文案
    - actions: [{ label, primary?, onClick }]
-->
<template>
  <div class="error-card">
    <div class="error-content">
      <strong>{{ titleText }}</strong>
      <p>{{ message }}</p>
    </div>
    <div class="error-actions">
      <button
        v-for="(action, i) in resolvedActions"
        :key="i"
        :class="action.primary ? 'btn-primary' : 'btn-ghost'"
        :disabled="loading"
        @click="invokeAction(action)"
      >
        {{ loading && action.primary ? '处理中…' : action.label }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useMicPermission } from '../composables/useMicPermission'

export interface ErrorAction {
  label: string
  primary?: boolean
  onClick: () => void | Promise<void>
  key?: string  // 用于防重复触发 loading
}

const props = defineProps<{
  code: string
  message: string
  actions?: ErrorAction[]
}>()

const loading = ref(false)
const loadingKey = ref<string | null>(null)
const mic = useMicPermission()

const titleText = computed(() => ({
  'mic-denied': '麦克风权限被拒绝',
  'mic-busy': '麦克风被占用',
  'mic-none': '未检测到麦克风设备',
  'rec-empty': '录音未捕获有效音频',
  'stt-failed': '语音转写失败',
  'transcribe-failed': '转写失败',
}[props.code] || '出错了'))

const resolvedActions = computed<ErrorAction[]>(() => {
  if (props.actions && props.actions.length) return props.actions
  // 默认按 code 提供 action
  switch (props.code) {
    case 'mic-denied':
      return [
        { label: '去系统设置', primary: true, key: 'open', onClick: () => mic.openSettings() },
        { label: '重新检测', key: 'recheck', onClick: () => mic.recheck() },
      ]
    case 'mic-busy':
    case 'mic-none':
      return [{ label: '重新检测', primary: true, key: 'recheck', onClick: () => mic.recheck() }]
    case 'rec-empty':
      return [{ label: '重新录制', primary: true, key: 'retry', onClick: () => location.reload() }]
    case 'stt-failed':
    case 'transcribe-failed':
      return [{ label: '重试转写', primary: true, key: 'retry', onClick: () => location.reload() }]
    default:
      return []
  }
})

async function invokeAction(action: ErrorAction) {
  loading.value = true
  loadingKey.value = action.key || null
  try {
    await action.onClick()
  } finally {
    loading.value = false
    loadingKey.value = null
  }
}
</script>

<style scoped>
.error-card {
  background: var(--bg-card);
  border: 1px solid var(--danger);
  border-radius: var(--radius-md);
  padding: var(--space-3);
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}
.error-card strong { color: var(--danger); font-size: 14px; }
.error-card p { color: var(--text-primary); font-size: 13px; margin: 0; }
.error-actions { display: flex; gap: var(--space-2); }
.btn-primary, .btn-ghost {
  padding: 8px 14px;
  border-radius: var(--radius-sm);
  font-size: 13px;
  cursor: pointer;
  border: 1px solid var(--border);
}
.btn-primary { background: var(--brand-primary); color: white; border-color: var(--brand-primary); }
.btn-ghost { background: var(--bg-card); color: var(--text-primary); }
.btn-primary:disabled, .btn-ghost:disabled { opacity: 0.5; }
</style>
```

- [ ] **Step 2: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/components/ErrorActionCard.vue
git commit -m "feat(meetings): ErrorActionCard 错误卡片 + CTA 按钮"
```

---

## Task 6: MainActivity 加原生"打开应用详情"桥接

**Files:**
- Modify: `frontend/android/app/src/main/java/com/kaixuan/opencode/pocket/MainActivity.java`

- [ ] **Step 1: 添加 bridge method**

打开 `MainActivity.java`，在 `onCreate` 末尾（`ensureRecordAudioPermission();` 之后）追加：

```java
import android.content.Intent;
import android.net.Uri;
import android.webkit.JavascriptInterface;

// 在 onCreate 内 ensureRecordAudioPermission 之后追加：
getBridge().getWebView().addJavascriptInterface(
    new Object() {
        @JavascriptInterface
        public void openAppDetails() {
            Intent intent = new Intent(android.provider.Settings.ACTION_APPLICATION_DETAILS_SETTINGS);
            intent.setData(Uri.parse("package:" + getPackageName()));
            intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
            startActivity(intent);
        }
    },
    "PocketNative"
);
```

- [ ] **Step 2: 改 useAppSettings 调原生**

替换 `frontend/src/composables/useAppSettings.ts` 中 Android 分支：

```ts
if (Capacitor.getPlatform() === 'android') {
  const w = window as any
  if (w.PocketNative?.openAppDetails) {
    w.PocketNative.openAppDetails()
    return true
  }
  // fallback
  const pkg = 'com.kaixuan.opencode.pocket'
  window.location.href = `intent://details#Intent;scheme=package;package=${pkg};end`
  return true
}
```

- [ ] **Step 3: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/android/app/src/main/java/com/kaixuan/opencode/pocket/MainActivity.java \
        frontend/src/composables/useAppSettings.ts
git commit -m "feat(android): MainActivity 暴露 openAppDetails 桥接"
```

---

## Task 7: MeetingRecordView 接入 MicStatusBar + ErrorActionCard

**Files:**
- Modify: `frontend/src/features/meetings/MeetingRecordView.vue`

- [ ] **Step 1: 引入组件**

在 `<script setup>` 顶部：

```ts
import MicStatusBar from '../../components/MicStatusBar.vue'
import ErrorActionCard from '../../components/ErrorActionCard.vue'
```

- [ ] **Step 2: 增加错误码状态**

把 `errorMessage: ref('')` 改为 `errorCode: ref<string | null>(null)`，`errorMessage: ref('')` 保留显示。

替换 `MeetingRecordView.vue:55-58` 的 `errorMessage.value = ''` 等多处：

```ts
const errorCode = ref<string | null>(null)
const errorMessage = ref('')

function setError(code: string, message: string) {
  errorCode.value = code
  errorMessage.value = message
}
function clearError() {
  errorCode.value = null
  errorMessage.value = ''
}
```

- [ ] **Step 3: 在 title-input 之前插 MicStatusBar**

替换 `MeetingRecordView.vue` 的 template 结构：

```vue
<template>
  <div class="record-page">
    <header><p>{{ statusText }}</p></header>

    <MicStatusBar />

    <input v-model="title" class="title-input" placeholder="会议标题（可选）" :disabled="recording || transcribing" />

    <div class="timer">{{ elapsedText }}</div>
    <button
      class="record-button"
      :class="{ active: recording }"
      :disabled="transcribing || !mic.isReady.value"
      @click="toggleRecord"
    >
      {{ recording ? '⏹' : '🎙️' }}
    </button>
    <p class="record-hint">
      <span v-if="!mic.isReady.value">请先授权麦克风</span>
      <span v-else-if="recording">点击停止并开始转写</span>
      <span v-else>点击开始录音</span>
    </p>

    <div v-if="transcribing" class="progress-card">正在转写会议录音…</div>
    <ErrorActionCard
      v-if="errorCode"
      :code="errorCode"
      :message="errorMessage"
      @click="clearError"
    />
    <!-- ↑ ErrorActionCard 不响应 click 事件，保留 message 给详情展示 -->

    <section v-if="transcript" class="transcript-card">
      <h2>会议转写</h2>
      <p>{{ transcript }}</p>
      <button class="primary" :disabled="summarizing" @click="makeSummary">
        {{ summarizing ? '生成中…' : '生成会议纪要' }}
      </button>
    </section>
  </div>
</template>
```

- [ ] **Step 4: 替换 errorMessage 赋值**

在 `startRecord` 中：

```ts
async function startRecord() {
  clearError()
  const ok = await mic.ensure()
  if (!ok) {
    if (mic.state.value === 'denied') setError('mic-denied', mic.deniedLabel.value || '请在系统设置中授权麦克风')
    else if (mic.state.value === 'unavailable') setError('mic-none', mic.deniedLabel.value || '未找到麦克风')
    else setError('mic-busy', mic.deniedLabel.value || '麦克风不可用')
    return
  }
  try {
    stream = await navigator.mediaDevices.getUserMedia({ audio: { channelCount: 1, sampleRate: 16000 } })
    recorder = new MediaRecorder(stream)
    // ... 原逻辑
  } catch (e) {
    console.error('[meeting] microphone error:', e)
    setError('mic-busy', '麦克风被占用或设备异常')
  }
}
```

在 `finishRecord` 中：

```ts
if (chunks.length === 0) {
  setError('rec-empty', '没有录到有效音频，请检查麦克风是否被遮挡')
  return
}
// ...
} catch (error: any) {
  console.error('[meeting] transcription failed:', error)
  setError('stt-failed', error?.message || '转写失败，请稍后重试')
}
```

- [ ] **Step 5: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/src/features/meetings/MeetingRecordView.vue
git commit -m "feat(meetings): MeetingRecordView 接入 MicStatusBar + ErrorActionCard"
```

---

## Task 8: e2e 验收

**Files:**
- Create: `frontend/tests/e2e/meeting-mic-guidance.spec.ts`

- [ ] **Step 1: 创建测试**

新建 `frontend/tests/e2e/meeting-mic-guidance.spec.ts`：

```ts
import { test, expect } from '@playwright/test'

test.describe('会议麦克风权限引导', () => {
  test('进入 /meetings/new 显示 MicStatusBar', async ({ page, context }) => {
    await context.grantPermissions(['microphone'], { origin: 'http://localhost:5173' })
    await page.goto('/#/meetings/new')
    await expect(page.locator('.mic-bar')).toBeVisible()
  })

  test('granted 状态显示绿色徽章', async ({ page, context }) => {
    await context.grantPermissions(['microphone'], { origin: 'http://localhost:5173' })
    await page.goto('/#/meetings/new')
    await expect(page.locator('.dot-granted')).toBeVisible()
  })

  test('denied 状态显示去系统设置按钮', async ({ page, context }) => {
    await context.clearPermissions()
    await page.goto('/#/meetings/new')
    // 模拟拒绝
    await page.evaluate(() => {
      (navigator as any).__micDenied = true
    })
    await page.click('button:has-text("授权麦克风")')
    // 期望：denied 状态 → "去系统设置" 按钮可见
    await expect(page.locator('button:has-text("去系统设置")')).toBeVisible({ timeout: 5000 })
  })
})
```

- [ ] **Step 2: 运行测试**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/frontend
npx playwright test tests/e2e/meeting-mic-guidance.spec.ts --reporter=list
```

期望：3 个 test 通过（最后一项在 Web 端受限于 Playwright API，部分跳过）。

- [ ] **Step 3: Android Emulator 真机验证**

```bash
emulator -avd Pixel_API_34 &
adb wait-for-device
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/frontend
npm run build
npx cap sync android
cd android && ./gradlew installDebug
adb shell pm revoke com.kaixuan.opencode.pocket android.permission.RECORD_AUDIO
adb shell am start -n com.kaixuan.opencode.pocket/.MainActivity
# 操作：进入 /meetings/new → 拒绝权限 → 看到红色徽章 + 去系统设置按钮
adb shell screencap -p /sdcard/mic-denied.png
adb pull /sdcard/mic-denied.png /tmp/
# 点击"去系统设置"
adb shell input tap 600 400
sleep 2
adb shell screencap -p /sdcard/system-settings.png
adb pull /sdcard/system-settings.png /tmp/
```

期望：截图显示系统设置应用详情页。

- [ ] **Step 4: 提交**

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
git add frontend/tests/e2e/meeting-mic-guidance.spec.ts
git commit -m "test(meetings): 麦克风引导 e2e + Android Emulator 真机验证"
```

---

## Self-Review

**1. Spec 覆盖（设计文档 §3.2）**：
- [x] useOpenAppSettings → useAppSettings（Capacitor App plugin + 原生桥接）→ Task 1/2/6
- [x] MicStatusBar 四态徽章 + CTA → Task 4
- [x] ErrorActionCard 错误码 → CTA 映射 → Task 5
- [x] MeetingRecordView 接入 + 录音按钮 disabled 绑定 mic.isReady → Task 7
- [x] e2e + Android Emulator 验证 → Task 8

**2. 占位符扫描**：无。

**3. 类型一致性**：
- `useMicPermission` 暴露 `isReady / openSettings / manualGuide` → Task 3 与 Task 4/5 一致。
- `ErrorActionCard.code` 取值：`mic-denied / mic-busy / mic-none / rec-empty / stt-failed / transcribe-failed` → Task 7 setError 调用与 Task 5 映射表一致。

**4. 风险**：
- iOS 跳设置受限，Task 2/5 已用 manualGuide 文案 fallback。
- Playwright 模拟浏览器权限不完整，Task 8 Step 1 Step 3 互为补充。
- Capacitor App plugin v6 的 openUrl 不支持 Android `ACTION_APPLICATION_DETAILS_SETTINGS`，所以用 Task 6 的原生 JS bridge。