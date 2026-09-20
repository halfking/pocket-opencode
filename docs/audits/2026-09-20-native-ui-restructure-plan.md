# 原生化与 UI 整体重构方案（2026-09-20）

> 状态：**审计与方案稿（design-proposed）**，待用户拍板阶段优先级与门槛
> 上游：
>
> - [`../2026-09-08-native-and-cross-platform.md`](../2026-09-08-native-and-cross-platform.md)（方案 B：Hybrid 2.0，不重写）
> - [`../design/2026-09-19-native-smoothness-audit.md`](../design/2026-09-19-native-smoothness-audit.md)（P0/P1 已落地、清单照单全收）
> - [`../design/2026-09-09-ai-async-background-survival.md`](../design/2026-09-09-ai-async-background-survival.md)（AI 流后台生存，骨架未接业务）
>
> 本次范围：把用户**四个并列目标**对应到**现在已经存在的能力**+**接下来要做的事**，形成可执行方案。

---

## 0. TL;DR

| 用户目标 | 现实路径 | 已落地 | 待落地（按 ROI） |
|---|---|---|---|
| 转 native、Android 优先 | **Hybrid 2.0**：Capacitor + 8 个原生插件；不重写为 RN/Flutter | 12 个 Android 原生 plugin，10 个壳层 @capacitor/* | ① `useAIBackgroundWork` 业务接线；② WorkManager backup 周期任务；③ iOS 整套补齐（v2） |
| 流畅 UI 交互 | **P0/P1 已落地**，主包 900KB→364KB，冷启动 ~1.0–1.7s | 路由转场 / 真滑动返回 / splash 就绪即隐 / 触觉 / 按压态 / 调试收口 / 列表兜底 / 滚动恢复 | ① 骨架屏（配合 list-sync 落地）；② Perfetto 实测回填；③ 内联 SVG（收益中等） |
| UI 与数据分离 / 数据放后台 | **三层架构**：Pinia Store（编排）+ Service（数据出口）+ Runtime（跨页/后台） | runtime-platform / mobileSync / mobileSyncRuntime / outboxStore / aiStreamRuntime / appLifecycleHub 等 12 个模块 | ① **业务接线**（最关键，所有 skeleton 0 consumer）；② Service 层"统一错误模型 + 最后写入保护"补完；③ AI 流在 lifecycle / FGS 之间切换的契约定型 |
| App 整体后台可执行 | **Android FGS 矩阵**（microphone / dataSync）+ WorkManager 兜底 | MeetingRecordService (mic)、AiStreamService (dataSync)、EmailFetchReceiver（已注册） | ① WorkManager 周期任务（16+min 间隔）；② Battery-optimization 引导页（doze 防误杀）；③ Silent Push 通道（v2） |

---

## 1. 现状审计（不要被"看似空"误导）

代码里**早就有**下面这些原生与后台能力，下面给具体证据，避免重复造轮：

### 1.1 已存在的原生 Android 能力位（`frontend/android/app/src/main/java/.../plugins/`）

| 插件 | 用途 | 已对前端暴露？ |
|---|---|---|
| `AiStreamKeepalivePlugin` + `AiStreamService` (dataSync FGS) | AI 流切后台保活 | ✅ `aiStreamKeepalive.ts`，**未接业务** |
| `BackgroundMicPlugin` + `MeetingRecordService` (microphone FGS) | 会议后台录音 | ✅（会议工作室在用） |
| `EmailFetchPlugin` + `EmailFetchReceiver` + `EmailFetchRunner` | 后台拉邮件 + periodic | ✅ |
| `BiometricAuthPlugin` | 生物认证 | ✅ |
| `SherpaPlugin` | 离线 ASR/TTS | ✅ |
| `AppSettingsPlugin` / `PermissionSettingsLauncher` / `AudioDeviceRank` | 设置/权限/音频 | ✅ |

> **结论**：Android 原生壳并不"空"。架构上已经在 Hybrid 2.0 路上走得相当扎实。下一步不是"造 native"，而是"**让这些 plugin 在新场景下被消费**"。

### 1.2 已存在的前端 native 模块（`frontend/src/native/`，共 32 个文件）

分组速览：

- **Runtime 编排层**（业务无 consumer，骨架阶段）：
  - `aiStreamRuntime.ts` — 进程级流复用/进度/取消
  - `appLifecycleHub.ts` — DOM + Capacitor 生命周期统一语义
  - `approvalsRuntime.ts` — 审批长任务跟踪
  - `aiStreamKeepalive.ts` — AI 流后台保活 JS 桥
- **数据出口层**（已经在被业务用）：
  - `mobileSync.ts` / `mobileSyncRuntime.ts` / `outboxDrain.ts` / `outboxStore.ts`
  - `local-db.ts` / `sqlDb.ts` / `schema.ts`
- **设备能力层**：
  - `background-mic.ts` / `meeting-audio.ts` / `audio-inputs.ts`
  - `biometricAuth.ts` / `keystore.ts` / `localNotifications.ts`
  - `sherpa.ts` / `vad-segmenter.ts` / `speaker-diarization.ts`
  - `runtime-platform.ts` / `capabilities.ts` / `crypto.ts`

> **结论**：**12 个 runtime 模块都已有骨架**（含 16 个 `.test.mjs`/`.ts` 测试），下一步是把骨架真正接上业务。这一步比再造一个 native shell ROI 高得多。

### 1.3 已经落地的 UI 顺滑工程（2026-09-20，PR `62f4d96`）

照搬审计清单，**不要重复做**：

- 路由转场（push/pop + tab fade），CSS transform-only
- 真·右滑返回（位移接力 + 立即 back，触觉提交）
- splash 就绪即隐（200ms fade，**不再 2s 强制**）
- 触觉（5 处：`useHaptics.ts`）
- 按压态（`scale(0.97) + opacity 0.85`，primary ripple）
- 调试收口（`BuildConfig.DEBUG` 分流）
- 主包 900KB→364KB，懒加载覆盖 21 个静态 import
- 列表渲染兜底（`content-visibility: auto` × 7 个容器）
- 滚动恢复（守卫记忆离场 scrollTop）

---

## 2. 用户目标的工程映射

### 2.1 "将这个项目转成 native，首先支持 Android"

**真实选项（互斥）**：

| 选项 | 工作量 | 收益 | 风险 | 推荐度 |
|---|---|---|---|---|
| A. **继续 Hybrid 2.0**：在原生 plugin 上深耕 | 1–2 人×季度 | 已落地 70%；补 WorkManager / Battery-optimization 即可覆盖后台 | 中（Doze / OEM 厂商后台策略） | **✅ 在 UI 端未达标时正确选择** |
| B. **重写为 Compose**：用 Jetpack Compose 重写页面 | 4–8 人×月 | 原生编译产物 + 完美 Liquid Glass | 推翻 ~15 万行 Vue；3+ 月无新业务 | 仅当 A 跑完后仍有 1–2 个页面救不回时，按页立项 |
| C. **跨端框架 RN/Flutter 重写** | 4–6 人×月 | 一套代码覆盖 iOS+Android | 双平台学习曲线；失去现有 Capacitor plugin | 当前不做（Vue 业务稳态，迁移成本不抵收益） |

**结论**：**采纳 A**。把"native"理解为"在用到原生能力时调原生 plugin，而不是字面意义的纯原生 UI"。

### 2.2 "流畅的 UI 交互"

P0/P1 已落地。当前剩**未达标的次原生项**：

1. 骨架屏（P1#10，配合 list-sync 一起做；收益高、立即可见）
2. Perfetto/systrace 实测（不补就是凭感觉）
3. 列表虚拟化（仅 `@tanstack/vue-virtual` 一处掉地，未接 4 个高频列表）
4. 触感反馈扩点位（现仅 5 处；长按菜单、开关、刷新钉子位等待补）
5. 内联 SVG sprite（收益中等、靠后排期）

> 上述五项先**骨屏 → Perfetto → 虚拟化**，3 周内可达 **90 分位**。

### 2.3 "将整个 UI 重构与优化，数据操作与 UI 分离"

**目标分层**（参考业界 Pinia 4 层状态分层 + 后端 DDD 思路）：

```
┌─────────────────────────────────┐
│  View（Vue Component）           │ ← 只渲染 props / 发事件；无业务
├─────────────────────────────────┤
│  ViewModel（composable）         │ ← 视图状态、用户交互胶水
│  - useEmailListVM.ts            │
│  - useAiChatVM.ts               │
├─────────────────────────────────┤
│  Store / Pinia                  │ ← 业务编排（缓存、订阅、订阅合并）
│  - useEmailStore                │
│  - useAiChatStore               │
├─────────────────────────────────┤
│  Service / Runtime              │ ← 与后台交界（运行时、FGS、DB、API）
│  - emailService.ts              │
│  - aiStreamRuntime.ts（已有）   │
└─────────────────────────────────┘
```

**当前实情**：很多 View 里直接 `await fetch(...)` / 直接写 SQL，**没有 Service / ViewModel 这一层**。

**改造动作**（按 ROI 排序）：

| 序 | 动作 | 收益 | 周期 |
|---|---|---|---|
| 1 | **AI 流会话语义场景**：把 `aiChatStore` 改为消费 `aiStreamRuntime` + 接入 `appLifecycleHub` | 后台生存 + UI 真正与"网络流"解耦 | 3 天 |
| 2 | **邮件收件箱场景**：抽 `emailService.ts`（与 `EmailFetchPlugin` 对接），`useEmailListVM` 接管列表渲染 | 大列表 + 后台拉取接缝 | 3 天 |
| 3 | **会议录音场景**：把 `meeting-audio.ts` / `useVoiceRecording.ts` 与 `appLifecycleHub` 串起来，统一生命周期 | 录音中切后台不再掉 UI 状态 | 2 天 |
| 4 | **统一错误模型**：`ServiceResult<T>` 取代裸 Promise；ErrorBus → Toast | 跨页面 bug 减少 | 1 周 |
| 5 | **Store→View 只读契约**：所有 mutate 都经 action，View 不得 `store.items.push(...)` 直接改 | 排查速度提升 | 持续 |

### 2.4 "数据可在后台进行，UI 切换没有问题，并且可将整个 App 放到后台执行"

**模型**（按"运行时分类"组织）：

| 运行时 | 后台保活 | 前端责任 | 后端责任 |
|---|---|---|---|
| **AI 流**（ask / 生成） | AiStreamService (dataSync FGS) | `aiStreamRuntime` 订阅 | 长连 SSE/WS；离线缓冲续传 |
| **会议录音** | MeetingRecordService (microphone FGS) | `meeting-audio.ts` | whisper / sherpa；上传 |
| **周期同步**（邮件 / RSS / 网关） | WorkManager 16+min | `emailFetch.registerPeriodic()` | IMAP / RSS |
| **推送**（通知到达） | FCM/silent push（v2） | silent push 唤醒 wakeup | 注册 token |

**整体 App 放后台**指：

- 进程级：`AppLifecycleHub` 单一权威；其他模块不再自己监听 `visibilitychange`（**这块已大半到位**）
- 流级：`aiStreamRuntime` 切到 hidden 不 cancel，UIDisconnected 才 cancel
- 通知级：所有"任务完成"统一走本地通知 → tap 回页面

---

## 3. 接下来的落地阶梯（4 周速赢 + 4 周深耕）

### 周 1-2（**MVP：把骨架接上业务**）

1. **AI 流接线**（关键路径）
   - `aiChatStore` 改为消费 `aiStreamRuntime.subscribe`
   - `appLifecycleHub.start()` 在 `main.ts` 启动一次
   - 新增 `useAIBackgroundWork` composable；UI 仅显示，不再发请求
   - 验证：`adb shell am start` + 切后台 + 等 30s 回前台消息完整
2. **会议录音接线**
   - `meeting-audio.ts` 监听 `appLifecycleHub.on('hidden')`，自动起 `BackgroundMicPlugin`
   - 在 ListView 上提示"录音中"
3. **统一错误模型**（第一段）
   - `ServiceResult<T> = { ok: true; value: T } | { ok: false; error: ServiceError }`
   - ErrorBus 在 `App.vue` 顶层订阅 → Toast

### 周 3-4（**后台生存加固**）

4. **WorkManager 周期任务**
   - 把 `EmailFetchRunner` 接入到 WorkManager（`PeriodicWorkRequest`，最小 15min）
   - 前端注册 → 后端同步 → 通知到达全套
5. **电池优化引导页**
   - 在 `useDevicePosture` 触发的"首次发现录像/录音权限拒绝"情境，给出"白名单引导"
   - 直接打开 `Settings`（OEM 路径表）

### 周 5-6（**UI 层解耦**）

6. **邮件列表 Service 层抽取**
   - `emailService.ts`（API + 本地 DB 二选一）
   - `useEmailListVM` 把过滤/排序/分页/详情预加载管起来
   - View 仅渲染 `state` + 触发 `event`
7. **会议列表 Service 层抽取**（同套范式）

### 周 7-8（**质感打磨**）

8. **骨架屏**（P1#10）：`<SkeletonList :rows="6" />` 组件；列表页 fetch 期间替代空 DOM
9. **Perfetto 实测**：长列表 fling、长流式、路由转场三场景抓帧
10. **微调**：触感反馈扩点位 / 主操作按钮 ripple 改触点级

---

## 4. 不要做（明确划线）

| 不做 | 原因 |
|---|---|
| 整体重写为 Compose/RN/Flutter | 推翻 15 万行 Vue，3+ 月无新业务 |
| iOS 一等公民补齐 | v2 议程；当前用户目标仅 Android |
| Unity/Game 风格的 120fps 滚动动效 | WebView 做不到，强行做是浪费 |
| 跨设备 session 接管 | 必须后端配套，1+ 月不落地 |
| 完全离线优先 | 弱网降级即可；PC 上的"真离线"不适合移动 UI |

---

## 5. 度量与完成判定

| 维度 | 现状 | 目标 | 验证手段 |
|---|---|---|---|
| AI 流切后台 30s 不断 | 已有 FGS，但业务未接 | 实测 30s 后回前台，msg 不丢 | adb + 真机实测 |
| 冷启动 TTI | 1.0–1.7s | <1.5s 仍达标 | `am start -W` |
| 千封邮件 fling | 未复测 | <1% 掉帧 | `dumpsys gfxinfo` |
| 触觉反馈点位 | 5 | ≥12 | grep audit |
| Service / ViewModel / View 分层 | 局部有 | 4 个高频域全覆盖 | grep 反模式 |
| 后台任务续航 | mic FGS 8 小时已测 | 不退化 | Power monitor |

---

## 6. 与既有路线图的耦合

- 与 [`../2026-09-08-native-and-cross-platform.md`](../2026-09-08-native-and-cross-platform.md) §3.1 三轨关系：
  - **Track A（Android 稳定化）**对应本文周 1-4 → 性价比最高的延续
  - **Track C（高价值 8 场景 native plugin）**已在 §1.1 落地 8/8，**不需要新增工作**
- 与 [`../design/2026-09-19-native-smoothness-audit.md`](../design/2026-09-19-native-smoothness-audit.md) §七：本文 §3 周 7-8 完全补全 P1 #10/#11 剩余项
- 与 [`../design/2026-09-09-ai-async-background-survival.md`](../design/2026-09-09-ai-async-background-survival.md) §3 阶段 1：本文周 1-2 完成度直接对接 1 阶段 5 阶段测试

---

## 7. 待用户拍板的 4 个决策点

> 见 `ask_user` 卡片（同时给出建议默认值）

1. **总体节奏**：4 周 MVP + 4 周深耕 ✅  / 还是只先做 4 周 MVP？
2. **是否整体重写 native**：否（采纳 Hybrid 2.0）✅  / 是
3. **iOS 是否同期铺开**：否（仅 Android）✅  / 是（v2 提前）
4. **是否值得做"AI 流保活"的强校验**：是（必须 30min 真机 + Perfetto）✅  / 否（仅功能验证即可）

---

**写于**：2026-09-20，**审计作者**：Mavis / mavis orchestrator
**下次更新**：用户决策后 + 周 2 阶段性回填
