# 原生化与跨平台路线图（Native & Cross-Platform Roadmap）

**日期**: 2026-09-08  
**状态**: 方案稿（design-proposed），等评审  
**上游**: [`2026-09-08-feature-inventory.md`](./2026-09-08-feature-inventory.md) §3 §4 + [`2026-09-08-requirements-design.md`](./2026-09-08-requirements-design.md) §1.3 §4  
**现状判定**: **半原生（half-native）**——业务层 100% 跨端，但壳工程仅 Android 完整，iOS / HarmonyOS 仍占位

---

## 0. 阅读约定

- **「原生」= Platform-Native**：iOS Swift/ObjC、Android Kotlin、鸿蒙 ArkTS 写出来的系统能力
- **「准原生」= Web 在 WebView 内通过 Capacitor Plugin 桥到原生**
- **「Web」= 在浏览器里纯 JS 实现**
- 三层堆叠时**底层能力可降级**：原生 → 准原生 → Web

---

## 1. 充分发散：12 个发散维度

> 先穷举所有"半原生"状态下我们**可能**做、**应该**做、**不能**做的事。

### D1. 后台能力（Background Execution）

| 能力 | Web | Capacitor | Native |
|---|---|---|---|
| 后台麦克风持续录音 | ❌ 静音即停 | 🟡 需 ForegroundService / AVAudioSession | ✅ |
| 后台同步 / 拉取 | ❌ Service Worker 限 30s | 🟡 需 @capawesome/background-task | ✅ |
| 后台定时任务 | ❌ | 🟡 @capacitor/local-notifications 兜底 | ✅ WorkManager / BGTaskScheduler |
| 推送到达 | ❌ 仅 Web Push（Safari 限制多） | 🟡 @capacitor/push-notifications + FCM/APNs | ✅ |

### D2. 系统集成深度（System Integration Depth）

| 能力 | 价值 | Web | Cap | Native |
|---|---|---|---|---|
| 应用图标快捷方式 | 高（Android 7+ / iOS 16+） | ❌ | 🟡 @capacitor/app + shortcut plugin | ✅ ShortcutManager / UIWindowScene |
| 桌面 Widget | 高（iOS 14+ / Android 12+） | ❌ | ❌ | ✅ |
| 灵动岛 / Live Activity | 高（iOS 16.1+） | ❌ | ❌ | ✅ ActivityKit |
| 锁屏控制 | 中 | ❌ | ❌ | ✅ |
| 系统分享面板 | 中 | 🟡 Web Share API（弱） | ✅ @capacitor/share | ✅ |
| 语音助手（Siri / Google Assistant） | 高 | ❌ | 🟡 App Actions 需 manifest | ✅ App Intents / Shortcuts |
| Wear OS / Apple Watch | 中 | ❌ | ❌ | ✅ |

### D3. 推送 & 通知（Push & Notification）

| 能力 | 当前 | 缺口 |
|---|---|---|
| 本地通知 | ✅ @capacitor/local-notifications | — |
| 远程推送 | 🟡 仅 Web Push（Safari 限制大） | 缺 FCM/APNs 接入 |
| 静默推送（背景数据同步） | ❌ | 缺 content-available |
| 通知分组 / 频道 | 🟡 默认 | Android 8+ 需 channel |
| 富文本通知（含图片/按钮） | ❌ | 缺 rich notification |

### D4. 输入体验（Input UX）

| 能力 | Web | Cap | Native |
|---|---|---|---|
| 软键盘跟随 | 🟡 viewport-resize | ✅ @capacitor/keyboard | ✅ |
| 触觉反馈（Haptics） | 🟡 navigator.vibrate | ✅ @capacitor/haptics（缺） | ✅ Core Haptics / Vibrator |
| 系统剪贴板 | ✅ | ✅ @capacitor/clipboard（缺） | ✅ |
| 长按菜单 / 上下文菜单 | 🟡 浏览器行为不一致 | 🟡 | ✅ |
| 拖拽 / 多选 | 🟡 HTML5 DnD 弱 | 🟡 | ✅ |
| Apple Pencil / S Pen | ❌ | 🟡 pointer events | ✅ PencilKit |

### D5. 性能（Performance）

| 指标 | Web | Cap WebView | Native |
|---|---|---|---|
| 冷启动 | 1.5-3s | 2-4s（WebView 启动 + JS 加载） | 0.3-1s |
| FCP（首次内容渲染） | 0.5-1.5s | 1-2s | 即时 |
| 长列表滚动（>1000 项） | 🟡 60fps 临界 | 🟡 | ✅ |
| JS 桥延迟 | N/A | 5-30ms/次 | N/A |
| 包体大小 | 8-15MB（web 资源） | 25-40MB（WebView 套壳） | 5-15MB（纯原生） |

### D6. 隐私与权限（Privacy & Permission）

| 项 | Web | iOS | Android |
|---|---|---|---|
| ATT（追踪透明） | ❌ | ✅ SKAdNetwork + ATT prompt | ❌ |
| 隐私清单（Privacy Manifest） | ❌ | ✅ Required since 2024-05 | N/A |
| Data Safety 表 | ❌ | App Store Connect | Google Play Console |
| 权限说明文案 | ❌ | ✅ NSMicrophoneUsageDescription 等 | ✅ runtime permission |
| 单次权限（iOS 13+） | ❌ | ✅ | ❌ |
| 近似位置（iOS 14+） | ❌ | ✅ | 🟡 |

### D7. 离线 & 同步（Offline & Sync）

| 能力 | Web | Cap | Native |
|---|---|---|---|
| IndexedDB | ✅ | ✅ | ✅ |
| SQLite | ✅ sql.js | ✅ @capacitor-community/sqlite | ✅ 直接原生 API |
| Keychain / Keystore | ❌ WebCrypto 弱 | 🟡 需自实现 plugin | ✅ |
| 后台同步 | ❌ Service Worker 弱 | 🟡 | ✅ |
| CloudKit / Drive 备份 | 🟡 | 🟡 | ✅ |

### D8. 平台导航 / 手势（Navigation / Gestures）

| 能力 | Web | Cap | Native |
|---|---|---|---|
| 滑动返回（iOS 边缘 / Android 预测式） | 🟡 history.back 不可控 | 🟡 需拦截 + 原生 nav | ✅ |
| 模态风格（iOS 卡片 vs Android 全屏） | ❌ 一致 | ❌ | ✅ |
| Safe area | ✅ env() | ✅ env() | ✅ |
| 手势栏（Home indicator） | ✅ env() | ✅ | ✅ |
| 分屏 / 多窗口 | 🟡 浏览器行为 | 🟡 | ✅ |
| 折叠屏铰链 | 🟡 media query | 🟡 | ✅ WindowManager |
| 桌面化（macOS Catalyst / Desktop Mode） | ❌ | 🟡 | ✅ |

### D9. 安全模型（Security）

| 能力 | Web | Cap | Native |
|---|---|---|---|
| HTTPS 强制 | ✅ | ✅ | ✅ |
| ATS（iOS App Transport Security） | N/A | ⚠️ 需 NSAllowsArbitraryLoads 配置 | ✅ |
| 代码混淆 | 🟡 JS 不可真混淆 | 🟡 JS 不可真混淆 | ✅ ProGuard / R8 |
| 防调试 | ❌ | 🟡 Capacitor 可检测 | ✅ |
| SSL Pinning | ❌ | 🟡 需 plugin | ✅ TrustManager |
| Web Crypto | ✅ | ✅ | ✅ |

### D10. 设备能力（Device Capabilities）

| 能力 | Web | Cap | Native |
|---|---|---|---|
| NFC | 🟡 Web NFC（Chrome only） | 🟡 | ✅ |
| BLE | ❌ Web Bluetooth 弱 | 🟡 | ✅ |
| 车机（CarPlay / Android Auto） | ❌ | ❌ | ✅ |
| AR / VR | 🟡 WebXR | 🟡 | ✅ |
| 屏幕录制 / 截屏检测 | ❌ | 🟡 | ✅ |
| 低功耗模式检测 | ❌ | 🟡 | ✅ |
| 始终亮屏 | 🟡 WakeLock API（实验） | 🟡 | ✅ |

### D11. 分发 / 上架（Distribution）

| 项 | Web | Cap APK/IPA | Native |
|---|---|---|---|
| OTA 热更新 | ✅ | 🟡 Capacitor Live Update（付费） | ❌ |
| 多渠道分发 | ✅ | ✅ Play Store / App Store | ✅ |
| 内部分发 | N/A | ✅ Firebase App Distribution / TestFlight | ✅ |
| 包大小 | 小 | 中 | 小 |
| 审核 | 无 | Play Store 严 | App Store 极严 |
| 版本管理 | 灵活 | 严格 | 严格 |

### D12. 开发体验（DX）

| 项 | Web | Cap | Native |
|---|---|---|---|
| 热重载 | ✅ 极快 | 🟡 需重启 WebView | 🟡 Xcode 慢 / Compose 较快 |
| 调试工具 | ✅ Chrome DevTools | ✅ Chrome 远程调试 + Safari Web Inspector | ✅ LLDB / Android Studio |
| 跨平台代码共享 | ✅ | ✅ 业务层 | 🟡 需 KMP |
| Build 时间 | 秒级 | 分钟级 | 分钟级 |
| Teams 学习成本 | 低 | 中 | 高（需 Swift / Kotlin） |

---

## 2. 收敛：5 个候选方案

> 从 12 维发散中筛选出 5 个**互斥**的工程方案。

### 方案 A：纯 Capacitor 强化（Status Quo+）

**保留所有现状，补齐 iOS / HarmonyOS 壳工程 + 缺失的 Capacitor 插件**

- ✅ 一套 Vue 代码跨三端
- ✅ 业务零改动
- 🟡 启动稍慢（WebView 套壳）
- 🟡 仍有 Web 边界（动画 / 长列表）
- 🟡 原生 UX 不深（共享平台 UI）

**适合**: 快速补齐 iOS，覆盖当前 95% 场景  
**风险**: 灵动岛 / Widget / App Intents 等深度能力做不了

### 方案 B：Capacitor + 平台原生 Spice 插件（Hybrid 2.0，**推荐**）

**A 的基础上，对 5-8 个高价值场景写平台原生 Plugin**

- ✅ A 的全部优点
- ✅ 高价值场景达到原生体验（mic、push、shortcut、widget）
- 🟡 团队需懂 Swift / Kotlin / ArkTS（每个平台 1-2 人月）
- 🟡 平台版本同步要维护三套 plugin

**适合**: 既要覆盖度，也要核心场景体验  
**风险**: 需明确"哪些场景必须原生"的清单（见 §3）

### 方案 C：原生壳 + WebView 业务（Native-Shell）

**把原生工程作为 App 骨架，关键页面仍是 WebView 加载远程/本地 Vue**

- 🟡 启动快（首屏原生 splash → 加载 WebView）
- 🟡 可以混用 Compose/SwiftUI + WebView
- ❌ Vue Router 与原生导航模型冲突
- ❌ 双技术栈维护成本高

**适合**: 已经有现成原生 App 想增量引入 Web  
**不推荐**: 我们从零开始，没有这个包袱

### 方案 D：全量原生重写（Greenfield）

**iOS 用 SwiftUI + Combine；Android 用 Compose + Flow；后端不变**

- ✅ 完美原生体验
- ❌ 推翻 90% 现有 Vue 代码（~15-20 万行）
- ❌ 团队需补齐 Swift + Kotlin 双栈
- ❌ 4-6 个月起步
- ❌ 主题切换 / 设计系统要重做

**适合**: 项目早期 / 重做时机  
**不推荐**: 当前业务已跑通，重写 ROI 极差

### 方案 E：纯 PWA + 浏览器外壳

**完全 web，跳过 App Store**

- ✅ 0 上架成本
- ✅ OTA 100%
- ❌ iOS 后台 mic / push 不支持
- ❌ 没有应用图标 / 桌面入口体验差
- ❌ 与"原生效果"目标违背

**不推荐**

---

## 3. 收敛决策：方案 B + 三轨路线图

> **采纳方案 B（Hybrid 2.0）**：在 A 的基础上，对**高价值 8 个场景**做原生 Plugin，**低价值场景**继续走 Web。  
> 不做 D（ROI 差），不做 C（双栈负担重），不做 E（违反产品目标）。

### 3.1 三轨路线图

```
Track A · 稳定化（Android 已有基础上）
  ↓ 把当前 Capacitor Android 推到"准生产"
Track B · 平台补齐（iOS 主战场 + HarmonyOS 补底）
  ↓ iOS 落地为第二个一等公民；HarmonyOS 维持 Phase A 兜底
Track C · 原生锦上添花（高价值 8 场景做原生 plugin）
  ↓ 让"会议录音 / 推送 / 快捷入口 / 后台同步"达到原生感
```

### 3.2 高价值 8 场景（必须原生）

> 排序依据：用户感知度 × 频次 × Web 是否做不到

| 序 | 场景 | 必须原生的原因 | 方案 |
|---|---|---|---|
| 1 | **后台会议录音** | Web 不能保持后台 mic | Android ForegroundService（已有）+ iOS AVAudioSession + BackgroundModes `audio` |
| 2 | **远程推送（needs-input）** | Web Push 在 iOS 弱 | FCM（Android）/ APNs（iOS）+ @capacitor/push-notifications |
| 3 | **应用图标快捷方式** | Web 完全无 | Android ShortcutManager / iOS UIWindowScene + quick actions |
| 4 | **本地通知 + Deep Link** | Web 不一致 | @capacitor/local-notifications（已有）+ Universal Links / App Links |
| 5 | **生物认证免密** | Web Crypto API 弱 | @capawesome/biometric + Keychain（iOS） / AndroidKeyStore（Android） |
| 6 | **触觉反馈** | navigator.vibrate 不一致 | @capacitor/haptics |
| 7 | **系统分享 / 拉起** | 已部分有 | @capacitor/share + @capacitor/app-launcher |
| 8 | **后台数据同步** | Service Worker 弱 | @capawesome/background-task / iOS BGTaskScheduler |

### 3.3 低价值 8 场景（继续 Web）

| 场景 | 继续 Web 的原因 |
|---|---|
| 长列表滚动 | 已 60fps；超 1000 项用虚拟列表 |
| 动效 / 过渡 | CSS + Vue Transition 充足 |
| 表单输入 | Web 原生即足够 |
| Markdown 渲染 | marked + highlight.js 性能良好 |
| 图表 | ECharts 已稳定 |
| 模态 / Bottom Sheet | 已自实现 |
| 主题切换 | CSS variables 已 work |
| 设置面板 | 表单为主 |

---

## 4. 平台补齐方案

### 4.1 iOS 落地清单

| 项 | 状态 | 工作量 |
|---|---|---|
| 壳工程编译跑通 | ⏳ 已有 AppDelegate / pbxproj | 0.5d 验证 |
| `Info.plist` 权限文案 | ❌ | 1d |
| ATS 配置（NSAllowsArbitraryLoads） | ❌ | 0.5d |
| Background Modes `audio` | ❌ | 0.5d |
| Keychain Plugin | ❌ | 1d |
| LocalAuthentication Plugin | ❌ | 1d |
| AVAudioSession (背景 mic) | ❌ | 2d |
| APNs + Push Notifications | ❌ | 2d |
| UIWindowScene 快捷入口 | ❌ | 1d |
| LaunchScreen / Splash | ✅ 已就位 | — |
| TestFlight 内测 | ❌ | 0.5d |
| App Store 审核准备 | ❌ | 2d |
| **小计** | — | **12-14 工作日（1 人月）** |

### 4.2 HarmonyOS 落地清单

| 项 | 状态 | 工作量 |
|---|---|---|
| ArkTS WebView 容器加载本地资源 | ✅ 已有 Index.ets | 0.5d |
| `@capacitor/core` 等价（OH 层） | ❌ | 5-7d（需自实现桥接） |
| 关键 Plugin 等价（mic / push / biometric） | ❌ | 5-10d |
| Stage 模型 / UIAbility 适配 | ❌ | 3-5d |
| 上架（华为应用市场） | ❌ | 2d |
| **小计** | — | **15-25 工作日（1.5-2 人月，仅作"补底"）** |

> HarmonyOS 维护成本与用户体量不匹配，建议**延后到 v3.0**，当前保持 Phase A 兜底即可。

---

## 5. 分阶段路线

### P0（1 个月内）· Android 稳定化

- [ ] 全 Plugin 升级到 @capacitor/* v8
- [ ] @capacitor/haptics / @capacitor/keyboard / @capacitor/clipboard
- [ ] Android ForegroundService 类型补全（DATA_SYNC / MEDIA_PLAYBACK）
- [ ] ProGuard 规则 + R8 收缩
- [ ] Crash 上报接入（@capacitor-community/crashlytics 或自建）
- [ ] Capacitor sync / build 流程写入 CI

### P1（2-3 个月）· iOS 落地

- [ ] Info.plist 权限文案 + Background Modes
- [ ] Keychain / LocalAuthentication Plugin
- [ ] AVAudioSession 后台 mic
- [ ] 推送（FCM 跨端 + APNs iOS 专用）
- [ ] Universal Links + Shortcut
- [ ] TestFlight 内测 → App Store 提交

### P2（3-4 个月）· 原生锦上添花

- [ ] 桌面 Widget（iOS WidgetKit + Android App Widget）
- [ ] 灵动岛 / Live Activity（仅 iOS）
- [ ] Siri Shortcuts / Google Assistant App Actions
- [ ] 多窗口 / 桌面化（macOS Catalyst / Desktop Mode）

### P3（按需）· HarmonyOS

- [ ] 等 P0-P2 落地后再评估 ROI
- [ ] 或仅维护 Phase A 兜底（WebView 加载 Vue）

---

## 6. 风险与缓解

| 风险 | 等级 | 缓解 |
|---|---|---|
| Capacitor 升级破坏 API | 中 | Pin minor version / CI 守门 |
| iOS 审核被拒（隐私 / mic 后台） | 高 | 提前 1 个月预审 + 准备豁免说明 |
| Apple Silicon / Intel 编译差异 | 低 | xcodebuild 仅需 `-destination` 切换 |
| 多 Plugin 维护成本 | 中 | 收敛到官方 + @capawesome 系列 |
| 团队学习 Swift / Kotlin | 中 | 2-3 人专项 + 代码 review |
| WebView 版本碎片（Android） | 中 | minSdkVersion 提到 26（WebView 跟 Chrome） |

---

## 7. 与现有方案的不冲突声明

| 文档 | 关系 |
|---|---|
| `2026-08-27-mobile-ux-design-v2.md` | 本方案是其"原生壳层"补足，不修改 IA |
| `2026-09-08-meetings-studio.md` | 本方案的"场景 1 后台录音"对齐其 mic dock 设计 |
| `2026-09-08-master-password-biometric-unlock.md` | 本方案"场景 5 生物认证"是其依赖 |
| `2026-09-08-requirements-design.md` | 本方案是其 §4 风险的细化路线 |

---

## 8. 验证 / 证据

| 维度 | 现状 | 目标 |
|---|---|---|
| Android 启动时间 | 已测 ≤3s | ≤2.5s |
| iOS 启动时间 | 未测 | ≤3s |
| WebView 兼容矩阵 | minSdk 24 (Android 7.0) | minSdk 26 (Android 8.0) |
| Plugin 清单 | 见 §3.2 8 项 | 全部落地 |
| App Store 上架 | ❌ | P1 末达成 |

