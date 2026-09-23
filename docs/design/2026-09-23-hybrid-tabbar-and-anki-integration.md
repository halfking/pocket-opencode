# 2026-09-23 · 融合重构方案：原生 + H5 混合架构 + TabBar 重组 + Anki 功能注入

> **作者**：Mavis / mavis orchestrator
> **状态**：design-accepted · 进入逐步实施
> **范围**：openpocket 全局架构调整（TabBar + 混合架构 + Anki 能力注入 + iOS/Android 抽象）

---

## 0. 一句话总览

把 openpocket 从「Capacitor + Vue 3 的 H5 应用 + 几条原生桥」重构为「**以原生壳为骨、H5 为肉**」的融合应用 —— 硬件相关能力（录音、拍照、智能体、后台任务、推送）走原生代码，**交互层**（列表、详情、表单、设置、复习）走 H5。**TabBar 重组为 4 个一级目的地 + 1 个「更多」入口抽屉**，所有次要功能通过该抽屉与扩展组件抵达。**iOS / Android 通过 capability 接口实现底层不同、UI 一致**。**Anki 的核心 SRS 模型、牌组树、卡类型、标签、Cloze、复习统计逐项落地**。

---

## 1. 现状再理解

### 1.1 现状盘点

| 维度 | 当前 | 痛点 |
|---|---|---|
| **前端框架** | Vue 3 + Vite + TS + Pinia | OK |
| **原生壳** | Capacitor 8（Android / iOS / HarmonyOS 三端） | 三端就位但 Native 写得很薄 |
| **底部导航** | 6 tab（AI / AI Chat / Notes / Meetings / RSS / Email） | 6 个太多，夺用户注意力；扩展入口分散到 SettingsMenuDrawer 但仍需拖拽 |
| **硬件能力** | 录音 / 拍照 / 推送 / 后台任务 全部走 Capacitor Plugin | Plugin 调用层级合理，但 UI 反馈（如录音条跨页存续）已通过 `RecordingPill` 解决 |
| **Flashcards** | 已存在 `features/flashcards`，FSRS + outbox 离线友好 | 缺 Anki 的卡类型/牌组树/标签/Browser/统计/导入导出 |
| **iOS** | `frontend/ios/App/` 存在（Xcode 工程 + SPM） | 缺乏 tabbar parity 文档 + 原生 module 落地证明 |
| **Android** | `MainActivity.java` 自定义、17 关键权限（源 manifest 口径） | OK |

### 1.2 当前 TabBar 痛点

```ts
// frontend/src/components/BottomNav.vue
const items: NavItem[] = [
  { to: '/ai', icon: 'smart_toy', label: 'AI' },
  { to: '/ai-chat', icon: 'forum', label: '对话' },
  { to: '/notes', icon: 'edit_note', label: '笔记' },
  { to: '/meetings', icon: 'mic', label: '会议' },
  { to: '/rss', icon: 'rss_feed', label: '订阅' },
  { to: '/email', icon: 'mail', label: '邮箱' },
]
```

**问题**：
1. **6 个 tab 违反 iOS HIG「3-5 tab」、Material 3「3-5 destinations」**。
2. AI 与 AI Chat 同质化严重，用户心智「这是两个 AI 入口」。
3. RSS / Email 是低频场景，挤占黄金位。
4. Flashcards / Vault / PKM / Scheduled-tasks / 智能体市场 等完全不在 TabBar，藏在抽屉里发现性差。

### 1.3 Anki 与 openpocket flashcards 的差距

| Anki 能力 | openpocket 现况 | 缺口 |
|---|---|---|
| **FSRS / SM-2 算法** | ✅ ts-fsrs 接入 | OK |
| **复习会话** | ✅ `FlashcardReviewView` | OK |
| **Note + Card 模型** | ✅ `FlashcardNote / FlashcardCard` | OK |
| **Deck 配置**（newPerDay / learningSteps） | ✅ `FlashcardDeckConfig` | OK |
| **Basic 卡类型** | ✅ `front / back` | OK |
| **Reversed / Cloze** | ❌ 仅 Basic | 缺 |
| **Note types（模板）** | ❌ | 缺 |
| **牌组树（decks 嵌套）** | ❌ 平铺 | 缺 |
| **Tags** | ✅ 字段有，UI 缺 | 部分缺 |
| **Card Browser（搜索/过滤）** | ❌ | 缺 |
| **统计图（mature / young / lapse）** | ❌ | 缺 |
| **导入 .apkg / .csv** | ❌ | 缺 |
| **导出** | ❌ | 缺 |
| **媒体（图片 / 音频）** | ❌ front/back 仅文本 | 缺 |
| **CRUD 完整 deck-options** | ❌ 学习阶梯 / 毕业间隔硬编码 | 缺 |

### 1.4 当前 `~/workspace/ai/anki`（Anki 上游）可借鉴资产

`~/workspace/ai/anki` 是 Anki 上游仓库（Svelte + Rust + PyQt），其 TypeScript 端位于 `ts/`：

```
ts/src/
  backend/           # SvelteKit 后端接口（Rust 封装）
  lib/               # 通用 lib（time / string / diff）
  card-info/         # 卡片信息弹窗
  deck-options/      # 牌组配置 UI
  editor/            # 卡片编辑器
  graphs/            # 统计图
  image-occlusion/   # 图片遮挡
  import-anki-package/  # .apkg 导入
  import-csv/        # .csv 导入
  reviewer/          # 复习器
```

**借鉴原则**：**模型与算法**直接借鉴（FSRS 已经在用），**UI 实现**不直接复制（Anki 是 Svelte，Openpocket 是 Vue），**数据格式**借鉴 .apkg 规范（sqlite + JSON collection），**导入流程**做 .apkg / .csv 解析器。

---

## 2. 新方案：4 Tab + 1 抽屉 + 能力分层

### 2.1 TabBar 新设计（4 tab）

| 序 | 图标 | 名称 | 路由 | 入口归属 | 交互形态 |
|---|---|---|---|---|---|
| 1 | `home` | **首页** | `/home` | 重命名 `/ai` 为 `/home`，聚合 Tasks + AI Chat 入口 | 任务聚合 + AI 入口 |
| 2 | `style` | **学习** | `/study` | **新建**：Flashcards + PKM（笔记）合并入口 | FSRS 复习 + 笔记列表 |
| 3 | `mic` | **会议** | `/meetings` | 不变 | 录音 + 转写 + 总结 |
| 4 | `apps` | **更多** | `/more` | **替换**：抽屉式聚合页（替代 BottomSheet 抽屉） | 9 大功能网格 |

> **设计动机**：
> 1. **4 tab 是 iOS HIG 与 Material 3 共同推荐的「黄金数字」**（微信 / 钉钉 / Notion / Slack Mobile 都是 3-5 tab）。
> 2. 「首页 + 学习 + 会议 + 更多」覆盖 80% 用户场景。
> 3. **「更多」改 tab 而非抽屉** —— 用户只需 1 次点击，且可在 H5 内排序、自定义（类微信）。
> 4. Email / RSS / Vault / Settings 等次要功能全部入「更多」。

### 2.2 「更多」Tab 设计（9 宫格）

```
┌─────────────────────────────────────┐
│  OpenPocket                          │  ← 顶栏标题
├─────────────────────────────────────┤
│  [AI 对话]      [笔记 PKM]           │  ← 主功能
│  [邮件]         [订阅 RSS]           │
│  [密码箱]       [定时任务]           │
│  [智能体市场]    [技能市场]           │
│  [本地智能体]                          │
├─────────────────────────────────────┤
│  ── 设置与运维 ──                    │  ← 分割线
│  [设置]         [通知中心]           │
│  [服务器]       [账户中心]           │
└─────────────────────────────────────┘
```

**实现要点**：
- 入口列表从 `SettingsMenuDrawer` 平移过来，但**改为页面级**（可被深链、可被分享、可被搜索）。
- 「设置与运维」分组与「主功能」分组视觉分割。
- 自定义排序：长按拖拽，Pinia store 持久化。

### 2.3 「学习」Tab 设计（合并 Flashcards + 笔记）

```
┌─────────────────────────────────────┐
│  学习                       [新建]    │  ← 顶栏
├─────────────────────────────────────┤
│  [今日复习 12 张]   按钮大字          │  ← 复习入口（Hero）
├─────────────────────────────────────┤
│  📚 我的牌组                          │
│   • 英语词汇        8 due            │
│   • 操作系统         3 due            │
│   • 面试题          12 due            │
│   [全部牌组]                          │
├─────────────────────────────────────┤
│  📝 我的笔记                          │
│   • 今日待办 5                     │
│   • 长期项目 3                      │
│   [打开笔记]                          │
└─────────────────────────────────────┘
```

**实现要点**：
- `FlashcardListView` 与 `NoteListView` **首页级合并**（不再是 2 个独立路由），详情仍走独立路由。
- 「今日复习」Hero 卡是主交互点，点击进入 `FlashcardReviewView`（不变）。

---

## 3. 原生 + H5 混合架构

### 3.1 分层原则

| 层级 | 技术 | 典型功能 | 路径 |
|---|---|---|---|
| **L1 原生 Activity / UIKit** | Kotlin / Swift / ArkTS | 启动屏、权限申请、系统对话框、Widget、Live Activity | `android/app/src/main/java/...MainActivity.java`、`ios/App/.../AppDelegate.swift` |
| **L2 原生 Module（Capacitor Plugin）** | Kotlin / Swift / ArkTS | 录音、拍照、智能体循环、后台任务、推送、文件系统、加密 | `frontend/android/app/src/main/java/.../plugins/`、`frontend/ios/App/.../Plugins/` |
| **L3 WebView 容器** | Capacitor Bridge | 加载 dist/、状态栏、安全区域、键盘避让、返回键 | `frontend/capacitor.config.ts` |
| **L4 H5 业务层** | Vue 3 + Pinia | 列表 / 详情 / 表单 / 设置 / 复习 UI / 统计图 | `frontend/src/features/**` |
| **L5 跨端能力抽象** | TS Interface | `Recorder` / `Camera` / `BackgroundTask` / `AgentRuntime` | `frontend/src/native/capabilities.ts` |

**关键原则**：
- **L1 / L2 不做业务**，只暴露原子能力 + Promise / Observable。
- **L4 通过 L5 调用 L2**，**H5 层不知道底层是 Kotlin 还是 Swift**。
- **L4 通过 Teleport / Capacitor Bridge 调用 L3**，WebView 与原生共享 `window.PocketNative` 全局对象。

### 3.2 硬件能力 → 原生映射

| 能力 | Android | iOS | H5 调用方式 |
|---|---|---|---|
| **录音** | `MediaRecorder` + `Foreground Service`（已有 `recordingRuntime.ts`） | `AVAudioRecorder` + `AVAudioSession` | `window.PocketNative.recorder.start()` |
| **拍照** | `CameraX` | `AVCaptureSession` | `window.PocketNative.camera.capture()` |
| **智能体循环** | `WorkManager` + Foreground Service | `BGTaskScheduler` | `window.PocketNative.agent.runLoop()` |
| **后台任务** | `WorkManager` | `BGTaskScheduler` | `window.PocketNative.background.schedule()` |
| **本地通知** | `NotificationCompat` + `Foreground Service` | `UNUserNotificationCenter` | `window.PocketNative.notify.show()` |
| **文件加密** | `AndroidKeyStore` + AES-GCM | `Keychain` + CryptoKit | `window.PocketNative.crypto.encrypt()` |
| **生物识别** | `BiometricPrompt` | `LocalAuthentication` | `window.PocketNative.biometric.auth()` |
| **本地 SQLite** | `jeep-sqlite`（Capacitor SQLite 插件） | `jeep-sqlite` | 直接 Pinia store 透明使用 |

### 3.3 跨端抽象接口

`frontend/src/native/capabilities.ts`（已存在 `runtime-platform.ts` 的扩展）：

```ts
export interface PocketRecorder {
  start(opts: { sampleRate?: number; format?: string }): Promise<{ sessionId: string }>
  stop(sessionId: string): Promise<{ uri: string; durationMs: number }>
  pause(sessionId: string): Promise<void>
  resume(sessionId: string): Promise<void>
  onState(cb: (s: RecorderState) => void): () => void
}
export interface PocketCamera {
  capture(opts: { quality?: number; facing?: 'front' | 'back' }): Promise<{ uri: string }>
  pickFromGallery(): Promise<{ uri: string } | null>
}
export interface PocketBackgroundTask {
  schedule(name: string, opts: { intervalMs?: number; constraints?: object }): Promise<void>
  cancel(name: string): Promise<void>
  listPending(): Promise<string[]>
}
export interface PocketAgentRuntime {
  runLoop(opts: AgentLoopOpts): Promise<{ sessionId: string }>
  abort(sessionId: string): Promise<void>
  onEvent(sessionId: string, cb: (e: AgentEvent) => void): () => void
}
```

**底层实现**：
- **Android**：每个接口对应一个 Capacitor Plugin（Kotlin），已存在 `recordingRuntime.ts` 的对应实现路径在 `frontend/android/app/src/main/java/.../plugins/`。
- **iOS**：镜像实现，每个接口对应一个 Swift Plugin（`@objc public class ...`）。

### 3.4 H5 何时承担、何时调用原生

| 场景 | H5 | 原生 |
|---|---|---|
| 列表渲染（牌组列表 / 邮件列表 / 任务列表） | ✅ | — |
| 表单（新建牌组 / 编辑笔记 / 添加任务） | ✅ | — |
| 设置页（开关 / 滑块 / 选项） | ✅ | — |
| 复习会话（FSRS 评分） | ✅（CPU 纯计算） | — |
| 录音条 跨页存续 | UI 是 H5 | **录音 + 通知 + 后台 是原生** |
| 拍照（笔记插图 / 头像） | UI 是 H5 | **相机是原生** |
| 智能体思考过程 | UI 是 H5 | **循环是原生（保活）** |
| 推送通知 | 通知到达是原生 | **注册 / 调度是原生** |
| 本地数据库（牌组状态 / 笔记离线） | — | ✅ SQLite |
| 加密 / 生物识别 | UI 是 H5 | **加解密 / 验证是原生** |

---

## 4. iOS / Android 抽象

### 4.1 现状

- **Android**：`frontend/android/` 完整，`MainActivity.java` 有自定义（safe area 注入 + WebView bind）。
- **iOS**：`frontend/ios/App/` 存在（Xcode 工程 + CapApp-SPM），需要补充：
  - 自定义 `AppDelegate.swift`（safe area 注入 + WebView bind 镜像）。
  - 原生 Plugin 镜像（Recording / Camera / AgentRuntime / Background / Notify）。
  - TabBar parity（iOS TabBar Controller 的设计文档）。

### 4.2 抽象契约

**目标**：Android 与 iOS 上，**H5 层（业务代码 0 改动）看到的 API 完全一致**。

```ts
// frontend/src/native/capabilities.ts
export interface PocketNative {
  readonly platform: 'android' | 'ios' | 'web'
  readonly recorder: PocketRecorder
  readonly camera: PocketCamera
  readonly background: PocketBackgroundTask
  readonly agent: PocketAgentRuntime
  readonly notify: PocketNotifier
  readonly crypto: PocketCrypto
  readonly biometric: PocketBiometric
  readonly db: PocketDb
}
```

**实现路径**：
1. Android 通过 Capacitor Bridge（`@capacitor/core`）调用 Java Plugin。
2. iOS 通过 Capacitor Bridge（`@capacitor/core`）调用 Swift Plugin。
3. Web 浏览器走 Web API fallback（`MediaRecorder` / `getUserMedia` / `IndexedDB`）。
4. 业务代码只 import `usePocketNative()`，**不 import Capacitor**，便于未来 Web 端独立运行。

### 4.3 iOS 待办清单

| 项 | 优先级 | 工期估计 |
|---|---|---|
| `AppDelegate.swift` 镜像 `MainActivity.java` 的 safe area 修复 | P0 | 0.5d |
| `Recorder` Plugin Swift 实现（AVAudioRecorder） | P0 | 1d |
| `Camera` Plugin Swift 实现 | P0 | 1d |
| `Background` Plugin Swift 实现（BGTaskScheduler） | P1 | 1d |
| `Notify` Plugin Swift 实现（UNUserNotificationCenter） | P0 | 0.5d |
| `Biometric` Plugin Swift 实现（LocalAuthentication） | P1 | 0.5d |
| iOS TabBar parity 文档 + 真机验收 runbook | P1 | 1d |

---

## 5. Anki 功能注入路线图

### 5.1 数据模型扩展

```ts
// frontend/src/types/flashcards.ts —— 新增字段
export type FlashcardTemplate = 'basic' | 'basic_reversed' | 'cloze'

export interface FlashcardNote {
  // ... 已有字段
  template: FlashcardTemplate        // 新增
  clozeText?: string                  // Cloze 专用（与 front/back 二选一）
  parentDeckId?: string | null        // 新增：嵌套牌组
}

export interface FlashcardDeckConfig {
  // ... 已有字段
  parentDeckId?: string | null        // 新增
  // learningStepsMin / graduatingIntervalDays 已有
  maximumIntervalDays?: number        // 新增
  easyBonus?: number                  // 新增（FSRS easy bonus）
  hardInterval?: number               // 新增（FSRS hard interval）
}
```

### 5.2 UI 增量（按优先级）

| P0 | P1 | P2 |
|---|---|---|
| Cloze 卡类型 + 编辑器 | 牌组树（嵌套） | 导入 .apkg |
| Tag 管理 UI | Card Browser（搜索/过滤） | 导入 .csv |
| 牌组配置完整 deck-options | 统计图（mature / young / lapse） | 导出 |
| | 图片 / 音频插入（`@capacitor/filesystem`） | Note types 自定义模板 |

### 5.3 Anki 借鉴的算法与模型（直接复用）

- **FSRS 调度**：`ts-fsrs` 已用，沿用 Anki 现代默认（`ts-fsrs` 是 Anki 官方推荐的 JS 移植）。
- **.apkg 格式**：SQLite + collection.anki21，导入器用 `sql.js` 解析。
- **学习阶梯 / 毕业间隔**：Anki 默认值作为 openpocket 默认值。

---

## 6. 实施阶段（7 步走）

### Phase 1 · TabBar 重组（本周）
- **目标**：4 tab + 1 「更多」tab；抽屉收编为页面。
- **范围**：
  - `BottomNav.vue` items 数组改为 4 项。
  - 新增 `features/more/MoreHubView.vue`（9 宫格聚合页）。
  - `SettingsMenuDrawer` 转为「账户 + 通知 + 运维」3 项精简版（删掉主功能入口）。
  - `/ai` 重命名为 `/home`，`TasksView` 标题改「首页」。
- **风险**：路由迁移可能断深链；用 301 + localStorage 重定向。
- **验收**：`cd frontend && npm run gates` + 手动 `/home` 走查。

### Phase 2 · 学习 Tab 合并（本周）
- **目标**：Flashcards + 笔记合并到 `/study` tab。
- **范围**：
  - 新增 `features/study/StudyHubView.vue`（Hero 复习入口 + 牌组 + 笔记）。
  - `/flashcards` 与 `/notes` 路由保留（深链与详情）。
- **风险**：老用户习惯独立 tab；保留 `/flashcards` 与 `/notes` 直达链接。

### Phase 3 · Cloze 卡类型 + 编辑器（下周）
- **目标**：Anki 标志性的 `{{c1::answer}}` 语法支持。
- **范围**：
  - `FlashcardEditView` 增加 Cloze 模式切换。
  - Cloze 解析器（`{{c1::text::hint}}` → 多张 card）。
  - `FlashcardReviewView` Cloze 渲染（挖空 + 「显示答案」）。
- **风险**：Cloze 模型与 Basic 互斥；用 `template` 字段切换。

### Phase 4 · 标签 + 牌组树 + 牌组配置（下下周）
- **目标**：Anki 三大基础结构对齐。
- **范围**：
  - `FlashcardEditView` 加 tag 输入（chip 形态）。
  - `FlashcardDeckConfig` 加 `parentDeckId`，UI 树形选择。
  - `FlashcardDeckView` 加「设置」入口 → `DeckOptionsView.vue`。
- **风险**：嵌套深度无上限；UI 限制 3 层。

### Phase 5 · Card Browser + 统计（第 3 周）
- **目标**：Anki 的 `/` 浏览器 + 复习统计图。
- **范围**：
  - `features/flashcards/CardBrowserView.vue`（搜索 + tag 过滤 + 状态过滤）。
  - `features/flashcards/StatsView.vue`（mature / young / lapse 三张图，用 SVG / Chart.js）。

### Phase 6 · 媒体 + 导入导出（第 4 周）
- **目标**：图片 / 音频 / .apkg 导入导出。
- **范围**：
  - `FlashcardEditView` 加图片 / 音频按钮（用 `PocketCamera` + `PocketRecorder`）。
  - 媒体存储走 `jeep-sqlite`（独立 media 表，hash 去重）。
  - `.apkg` 导入器：`sql.js` 解析 → `FlashcardNote/Card/DeckConfig`。
  - `.apkg` 导出器：`sql.js` 写回 + 文件系统落盘。

### Phase 7 · iOS Plugin 镜像（第 5 周起，可并行）
- **目标**：与 Android 平齐。
- **范围**：见 §4.3 清单。

---

## 7. 给下一位工程师的接力

| 序 | 动作 | 命令 / 文件 |
|---|---|---|
| 1 | 读本文档 | `docs/design/2026-09-23-hybrid-tabbar-and-anki-integration.md` |
| 2 | 跑 gates | `cd frontend && npm run gates` |
| 3 | 从 Phase 1 开始 | `frontend/src/components/BottomNav.vue` + `frontend/src/features/more/MoreHubView.vue`（新建） |
| 4 | 路由表改名 | `frontend/src/app/router-mobile.ts` `/ai` → `/home`（保留 alias） |
| 5 | iOS 缺失项 | `frontend/ios/App/.../AppDelegate.swift`（需新建） |
| 6 | 提交 | `git add -A && git commit -m 'feat(tabbar): Phase 1 — 4 tab + 1 more hub'` |

---

## 8. 不在本方案范围内（明确排除）

- **HarmonyOS Phase B**（已有独立方案 `docs/audits/2026-09-01-harmonyos-phase-a.md`，本方案不重复）。
- **后端 Go 服务的 Anki 同步协议**（用现有 `FlashcardsSyncEnvelope` 即可，不另起炉路）。
- **桌面端**（Anki 桌面端是 Qt/Rust，Openpocket 不进）。

---

## 9. 度量 / 验收

| 度量 | 基线（现状） | 目标 |
|---|---|---|
| TabBar 项数 | 6 | **4** |
| 抽屉点击 → 功能 路径 | 平均 2 跳 | **1 跳**（More 页面直访） |
| Flashcards 复习功能 | Basic + FSRS | Basic + Reversed + Cloze + FSRS |
| iOS 真机运行 | 未验证 | **4 真机 + 1 模拟器** 通过 Phase 7 runbook |
| TabBar 切换到首屏耗时 | 当前 <100ms（实测） | **保持 <100ms** |
| 主包 JS 体积 | 364 KB | **保持 ≤400 KB**（More 页面懒加载） |

---

**写于**：2026-09-23
**作者**：Mavis / mavis orchestrator
**下一步**：从 Phase 1 开始（TabBar 重组），预计 1 周闭环。