# 跨端方案研究：iOS 与 HarmonyOS（鸿蒙）的落地路径（2026-11-01）

> 目标（来自本轮 goal）：「需要研究当前项目开发 iOS 版本与华为版本的可能性，
> 找到一种能够支持 Android/iOS/HarmonyOS 系统的方案。」
>
> 结论先行：**当前「单 Vue 前端 + Capacitor(Android/iOS) + 鸿蒙 WebView 壳 +
> pocket-native 平台抽象」的架构就是正确方案**，三端不需要分叉代码库；
> 差距只在两端的壳与插件镜像完成度。下文给出逐端证据、缺口与落地顺序。

## 1. 现状盘点（以 main @ 2026-11-01 为准）

| 层 | Android | iOS | HarmonyOS |
|---|---|---|---|
| 壳工程 | ✅ `frontend/android/`（Capacitor + 13 个原生插件/服务） | 🟡 `frontend/ios/App/`（Capacitor + SPM，AppDelegate.swift 就绪） | 🟡 `frontend/harmony/`（真实 hvigor 工程：EntryAbility.ets / pages/Index.ets / module.json5 / rawfile 内置 dist） |
| 运行时身份 | ✅ `Capacitor.getPlatform()==='android'` | ✅ `'ios'` 分支存在 | ✅ `runtime-platform.ts` 定义 `'harmony'` RuntimePlatform + HarmonyBridge + 能力钳制（clampHarmonyNativeCapabilities） |
| pocket-native 抽象 | ✅ `createAndroidNative()`（复用 6 个 Java 插件） | ❌ `createIosNative()` 是 stub：抛 "not implemented yet; awaiting Swift plugin mirror (Phase 7.1)" | 🟡 未进 `PocketPlatform` 类型（仍是 `'android'\|'ios'\|'web'`），运行时走 web fallback + 能力钳制 |
| 原生插件镜像 | 13 个（AiStreamKeepalive / AiStreamService / AppSettings / AudioDeviceRank / BackgroundMic / BiometricAuth / Document / EmailFetch(+Receiver/Runner) / MeetingRecordService / PermissionSettingsLauncher / Sherpa） | 3 个已镜像：AppSettingsPlugin.swift、BackgroundMicPlugin.swift、BiometricAuthPlugin.swift | 0 个（WebView 壳 + rawfile，靠 web 兜底实现） |
| 构建链 | ✅ `node scripts/build-mobile.mjs android dev\|prod`（本轮真机在用） | 🟡 同脚本支持 `ios dev`（需 macOS + Xcode，本机具备） | 🟡 `npm run build:harmony`（scripts/build-harmony.mjs prod）产出 rawfile，hvigor 打包需 DevEco/hvigor CLI |
| 后台保活 | ✅ FGS（dataSync + microphone 双型）真机 30min 验证过 | ❌ 未接（设计走 iOS BackgroundModes + BGTaskScheduler，iOS 无 FGS 等价物） | ❓ 待验（WebView 壳常驻策略由鸿蒙侧后台任务管理决定） |

## 2. iOS：可行性 = 高，剩余工作量集中在插件镜像 + 后台策略

**已具备**：iOS 壳工程、SPM、3 个关键 Swift 插件（设置 / 后台麦克风 / 生物识别——
正好覆盖「录音转写」与「解锁」两个最吃原生能力的功能）、构建脚本入口、
pocket-native 的 iOS 类型分支（只是实现体是 stub）。

**缺口（Phase 7.1 的收尾清单）**：
1. 镜像剩余插件到 Swift（按业务优先级）：
   `DocumentPlugin`（文件选择）→ `EmailFetchPlugin`（邮件后台拉取；iOS 用
   BGTaskScheduler 代替 AlarmManager+Receiver）→ `AppSettings` 已有 →
   `AiStreamKeepalive`（iOS 用 `UIApplication.beginBackgroundTask` + Background
   Modes 的 audio/fetch 声明，无 FGS 通知，形态不同但能力可保）→
   `SherpaPlugin`（本地 ASR；sherpa-onnx 有 iOS pod，可镜像）。
2. `frontend/src/native/pocket-native.ts` 的 `createIosNative()` 从 stub 变为
   与 Android 对称的实现（插件方法一一对应；这是纯 TS 工作，有 Android 版做镜像模板）。
3. 真机验证链复用本轮 Android 的 8 层静态证据思路（typecheck → build →
   DEX 审计换成 Swift 符号审计 → 模拟器冒烟）。

**平台约束（要在设计里写死，避免返工）**：
- iOS 没有常驻前台服务：长任务保活用 BGTaskScheduler/ProcessingTask + 远程推送
  唤醒；AI 流式会话在前台照常，切后台只能尽力而为（这是 OS 限制，不是缺陷）。
- 邮件后台拉取映射到 BGAppRefreshTask；通知走 APNs（后端已有通知中心，加 APNs
  provider 即可）。
- 本地加密存储（SQLCipher）在 iOS 换 SQLCipher for iOS + Keychain 存主密钥，
  接口不变（pocket-native 的 keystore 面已抽象）。

## 3. HarmonyOS：可行性 = 中高，WebView 壳路线已铺好

**已具备**：真实 hvigor 工程（`frontend/harmony/`），`EntryAbility.ets` 加载
rawfile 内置的 `dist/index.html`（与 Capacitor 同一份 Vite 产物，
`npm run build:harmony` 负责同步），运行时身份与能力钳制在 TS 侧已实现——
即「鸿蒙 = 自管壳的 WebView + web 兜底 native」这条路线的骨架是通的。

**缺口**：
1. `PocketPlatform` 类型加入 `'harmony'`（detectPlatform 依据 UA/bridge 探测；
   纯类型与分派工作，1 个 PR 量级）。
2. 原生能力按需映射（ArkTS 侧）：加密存储（关系型数据库 + Asset Store）、
   通知（notificationManager）、后台任务（backgroundTaskManager continuousTask，
   对应 FGS 的鸿蒙等价物）、麦克风（AVSession + mic 权限）。
   不必 13 个全镜像——先满足 top 场景：设置/存储/通知/录音。
3. 推送：鸿蒙 Push Kit；回调侧与现有 notificationDispatcher 的 deepLink 约定对齐。
4. 构建环境：DevEco Studio（或 hvigorw CLI）+ 签名证书；当前仓库已有
   `build:harmony` 的产物同步段，缺的是本机 hvigor 工具链（与 iOS 的 Xcode 同理，
   属环境安装而非代码工作）。

## 4. 推荐方案（排序即落地顺序）

1. **iOS 先收尾（Phase 7.1）**：壳与 3 个插件已在，把 `createIosNative()`
   从 stub 做成实现 + 镜像 Document/EmailFetch 两个高频插件即可出可用的
   TestFlight 包；AiStream/Sherpa 随后。理由：单位工作量换来的可用面最大，
   且与 Android 共享 100% 业务代码。
2. **鸿蒙按 WebView 壳路线推进**：类型收编（`'harmony'` 进 PocketPlatform）→
   四个 top 原生能力映射 → hvigor 工具链装机。鸿蒙侧不应复制 Android 的
   全部 13 插件，按 clampHarmonyNativeCapabilities 的能力钳制逐步放开。
3. **两端的「后台长任务」差异**必须走平台适配层（pocket-native 的
   aiStreamKeepalive 面），业务层不感知：Android=FGS，iOS=BGTask+APNs，
   Harmony=continuousTask。这与「所有 AI/长任务在后台以任务/服务形式执行」
   的规范在三个 OS 上各自的合法形态对齐。

## 5. 证据与口径

- 插件清单：`ls frontend/android/.../plugins/`（13 项，2026-11-01 实取）；
  iOS 镜像：`frontend/ios/App/App/Plugins/`（3 项）。
- stub 证据：`frontend/src/native/pocket-native.ts` createIosNative 抛
  "not implemented yet; awaiting Swift plugin mirror (Phase 7.1)"。
- 鸿蒙工程证据：`frontend/harmony/entry/src/main/ets/entryability/EntryAbility.ets`、
  `pages/Index.ets`、`module.json5`；`runtime-platform.ts` 的 `'harmony'`
  与 `clampHarmonyNativeCapabilities`。
- 构建入口：`frontend/package.json` scripts `build:harmony`；
  `scripts/build-mobile.mjs` 支持 ios/android × dev/staging/prod。
