# 2026-09-23 · iOS Plugin 镜像 runbook（Phase 7）

> **作者**：Mavis / mavis orchestrator
> **状态**：partial scaffold · 待 Mac + Xcode + 真机接通
> **配套文档**：`2026-09-23-hybrid-tabbar-and-anki-integration.md` §4.3

---

## 1. 现状

| 项 | Android | iOS |
|---|---|---|
| AppDelegate | Java `MainActivity.java`（safe area + 6 plugin 注册）| Swift `AppDelegate.swift`（已含 30s 后台缓冲 + Capacitor 派发）|
| 业务 plugin 数 | 6 个 Java | **0 个**（Phase 7 起步）|
| 录音 | `BackgroundMicPlugin.java`（Foreground Service）| `BackgroundMicPlugin.swift`（AVAudioRecorder，**需 Info.plist 配置**）|
| 生物识别 | `BiometricAuthPlugin.java`（BiometricPrompt + Keystore）| `BiometricAuthPlugin.swift`（LocalAuthentication，**无 Keystore 等价物**）|
| 设置跳转 | `AppSettingsPlugin.java` + `PermissionSettingsLauncher.java` | `AppSettingsPlugin.swift`（`UIApplication.openSettingsURLString`）|
| 后台流保活 | `AiStreamKeepalivePlugin.java`（Foreground Service）| `AppDelegate.swift`（`beginBackgroundTask`，30s 缓冲已就位）|
| Sherpa 语音转写 | `SherpaPlugin.java`（Sherpa-onnx Android binding）| **未镜像**（Sherpa iOS binding 待评估）|

**Phase 7 落地状态**：3 个 Swift plugin stub（AppSettings / Biometric / BackgroundMic）已写入
`frontend/ios/App/App/Plugins/`；未在 Xcode 项目中注册（需手动 addFiles + Info.plist 配置）。

---

## 2. TypeScript 侧抽象：`pocket-native.ts`

业务代码统一走 `getPocketNative()`，不直接 import Capacitor —— 这是设计目标：存量仍有 19 处直接 import（Phase 9 已迁移 `flashcardMedia.ts`），新增代码应只调 `getPocketNative()`：

```ts
import { getPocketNative } from '@/native/pocket-native'

const native = getPocketNative()
const session = await native.recorder.start() // Android → BackgroundMic plugin
                                          // iOS     → BackgroundMicPlugin.swift
                                          // Web     → MediaRecorder fallback
```

实现路径（Phase 7）：
- `detectPlatform()` 通过 `Capacitor.getPlatform()` 判定。
- 三个 factory：`createAndroidBridge` / `createIosStub` / `createWebFallback`。
- Android 桥接复用现有 Java plugin（零迁移成本 stub）。
- iOS 当前为 stub，**Phase 7.1 在 Mac 上接通**后切换为真实现。
- Web fallback 走 MediaRecorder / Notification API / base64 加密。

---

## 3. iOS Plugin 编译接通步骤（Mac + Xcode）

### 3.1 前置

- macOS 14+, Xcode 15+, CocoaPods 或 SPM
- 1 台 iOS 13+ 真机（Face ID / Touch ID 验证）
- Apple Developer 账号（真机签名）

### 3.2 工程接入

```bash
# 1) 在 Xcode 中打开项目
open frontend/ios/App/App.xcodeproj

# 2) 拖入 Plugins/ 目录的 3 个 Swift 文件
#    右键 App group → Add Files to "App"... → 勾选 Create groups
#    → 选 Plugins/AppSettingsPlugin.swift / BiometricAuthPlugin.swift / BackgroundMicPlugin.swift

# 3) Bridging Header（如 Xcode 提示）：
#    选 "Create Bridging Header"，把 frontend/ios/App/App/App-Bridging-Header.h 留空即可。
#    Capacitor 项目通常不需要 bridging（plugin 通过 @objc 暴露）。
```

### 3.3 Info.plist 必填项

```xml
<!-- 权限说明文案（App Store 审核会看） -->
<key>NSMicrophoneUsageDescription</key>
<string>需要麦克风用于会议录音、语音笔记</string>
<key>NSCameraUsageDescription</key>
<string>需要相机用于拍照记录</string>
<key>NSPhotoLibraryUsageDescription</key>
<string>需要相册权限用于选择图片</string>
<key>NSFaceIDUsageDescription</key>
<string>需要 Face ID 用于解锁 OpenPocket</string>

<!-- 后台模式：录音保活 + 后台 AI 流 -->
<key>UIBackgroundModes</key>
<array>
    <string>audio</string>          <!-- BackgroundMicPlugin 必须 -->
    <string>fetch</string>          <!-- 后台 AI 流 -->
    <string>processing</string>     <!-- BGTaskScheduler -->
</array>
```

### 3.4 registerPlugin 调用

在 `AppDelegate.swift` 的 `application(_:didFinishLaunchingWithOptions:)` 末尾添加：

```swift
// Phase 7：注册 5 个 iOS plugin 镜像
let bridge = self.bridge
bridge?.registerPlugin(AppSettingsPlugin.self)
bridge?.registerPlugin(BiometricAuthPlugin.self)
bridge?.registerPlugin(BackgroundMicPlugin.self)
// Phase 7.1：补 AiStreamKeepalivePlugin（URLSessionConfiguration.background）
// Phase 7.2：补 SherpaPlugin（sherpa-onnx-ios SPM 评估）
```

### 3.5 编译 + 部署

```bash
cd frontend/ios/App
xcodebuild -workspace App.xcworkspace -scheme App \
    -configuration Debug \
    -destination 'platform=iOS,name=iPhone 15 Pro' \
    -derivedDataPath build

# 或直接 Xcode → Product → → Run
```

### 3.6 真机验证 5 步（参照 Android runbook）

1. 装载 → 启 Activity → 权限弹窗 → 全部 Allow
2. 进入 /flashcards/browser → 搜索 → 验证搜索高亮
3. 新建卡片 → 切到 Cloze 模板 → 输入 `{{c1::test}}` → 验证挖空
4. 进入复习 → 验证 FSRS 评分 + 图片渲染（如果有图片）
5. 录音指示条跨页存续 → 切到后台 30s → 回前台验证状态

---

## 4. iOS 与 Android 的关键差异点（设计文档 §4.3 落地）

| 差异 | Android | iOS | 业务影响 |
|---|---|---|---|
| Keystore | AndroidKeyStore（硬件）| Keychain（`kSecAttrAccessibleWhenUnlockedThisDeviceOnly`）| 加密凭据实现需双端分别写 |
| 后台服务 | Foreground Service（无限时长）| beginBackgroundTask（30s）+ BGTaskScheduler | 后台录音 / AI 流需适配 |
| 推送 | FCM | APNs | 已通过 Capacitor Push 插件统一 |
| 通知 | NotificationCompat | UNUserNotificationCenter | 已通过 Capacitor LocalNotifications 统一 |
| 生物识别 | BiometricPrompt + CryptoObject | LAContext.evaluatePolicy | Phase 7.1 加密集成需分写 |
| APK / IPA | Gradle assemble | xcodebuild | CI 双轨 |

---

## 5. 已知缺口（Phase 7 → Phase 7.1 / 7.2 / 7.3）

| 缺口 | 影响 | 优先级 |
|---|---|---|
| SherpaPlugin（语音转文字）| iOS 端无法用会议录音转写 | P1 |
| AiStreamKeepalivePlugin（iOS）| 后台 AI 流保活能力差（30s vs Android 无限）| P0 |
| BiometricAuthPlugin Keychain 加密集成 | iOS 端无硬件加密凭据 | P0 |
| Audio Session 中断处理（电话 / 其他 App）| 录音偶发中断未恢复 | P1 |
| BGTaskScheduler `BGAppRefreshTask` 注册 | iOS 后台周期任务未实装 | P2 |

---

## 6. 给下一位工程师（Mac 环境）的接力

| 序 | 动作 |
|---|---|
| 1 | 读本文档 + §3.1-3.5 |
| 2 | 拉代码 + 在 Xcode 打开 frontend/ios/App/App.xcodeproj |
| 3 | 拖入 3 个 Swift plugin → 编译验证 |
| 4 | 改 AppDelegate 注册 plugin |
| 5 | 配置 Info.plist 权限说明 + UIBackgroundModes |
| 6 | 真机装机 + 走 §3.6 五步验证 |
| 7 | 把验证结果写进 `docs/audits/2026-09-23-ios-plugin-mirror.md` |
| 8 | 修改 `frontend/src/native/pocket-native.ts` 的 `createIosStub` → `createIosNative` 切换 |

---

**写于**：2026-09-23
**作者**：Mavis / mavis orchestrator
**状态**：scaffold landed · awaiting Mac verification