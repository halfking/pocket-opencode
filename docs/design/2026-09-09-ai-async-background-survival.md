# 设计/方案：AI 操作全异步化 + 切换窗口/标签不中断

> 落档日期：2026-09-09
> 状态：Draft，待评审
> 需求：[../requirements/2026-09-09-ai-async-background-survival.md](../requirements/2026-09-09-ai-async-background-survival.md)

## 0. 一句话总结

把 AI 流的**生命周期从 Vue 组件解绑**到**进程级 singleton 运行时**（`AiStreamRuntime`，仿 `mobileSyncRuntime.ts`），同时给 iOS/Android 加上 `UIBackgroundModes` / `FOREGROUND_SERVICE_DATA_SYNC`，让 OS 不杀进程。120s 看门狗改成"隐藏态禁用 + 显式停用态延长"。组件只负责"订阅/退订"流更新，**不**负责创建/销毁流。

## 1. 当前问题与根因

### 1.1 问题盘点

| # | 问题 | 表现 | 根因 |
|---|---|---|---|
| 1 | Session 切走会停流 | 用户切回发现流停了 | `SessionConversationView.onBeforeUnmount`（L177-183）调用 `store.close()` 关 SSE |
| 2 | 审批轮询被停 | 切走时无通知 | `stopApprovalPolling` 在 unmount 调用 |
| 3 | Chat 流 UI 半截 | 切走回来看到半截气泡 | `aiChatStore` 留 controller，`AIChatView.onUnmounted`（L697）不 abort → 流在跑但 UI 丢了 |
| 4 | Chat 假超时 | 切回看到"响应超时" | 120s 看门狗在隐藏态仍计时（`llm-bff.ts:102`） |
| 5 | iOS 后台冻结 | 后台 30s 后流停 | `Info.plist` 无 `UIBackgroundModes`，`AppDelegate` 空 |
| 6 | Android 杀进程 | Doze / 多任务清理 | 缺 `FOREGROUND_SERVICE_DATA_SYNC` / `WAKE_LOCK` |
| 7 | Prompt Optimizer 没法停 | 关弹窗后流继续 | `usePromptOptimizer` 无 `onBeforeUnmount` 调 `abort()` |
| 8 | Gateway Live 切走停 | 切回要等重连 | `GatewayLiveStreamView.onUnmounted`（L196）关 SSE |
| 9 | OpenCode WS 泄漏 | 一直 reconnect | `stores/opencode.ts:248-287` 无清理 |

### 1.2 根因一句话

> **资源所有权错位** —— 流/轮询/WebSocket 的"创建"和"销毁"被绑死在 Vue 组件生命周期上，而组件生命周期 = 用户的"当前关注页"。把所有权上移到进程级 singleton，所有"切走/切回"就只是"暂停/恢复订阅"，而不是"关流"。

## 2. 总体架构

```
┌──────────────────────────────────────────────────────────────────────────┐
│                       WebView / Native App 进程                            │
│                                                                          │
│   ┌─────────────────── main.ts (启动一次) ────────────────────────┐      │
│   │                                                                 │      │
│   │   ┌─────────────────────────┐    ┌────────────────────────┐    │      │
│   │   │  AiStreamRuntime        │    │  AppLifecycleHub       │    │      │
│   │   │  (singleton)            │◄───┤  (singleton)           │    │      │
│   │   │                         │    │  - visibilitychange    │    │      │
│   │   │  - chat/stream registry │    │  - appStateChange      │    │      │
│   │   │  - AbortController map  │    │  - freeze/resume       │    │      │
│   │   │  - WS bus passthrough   │    │  - 停用 120s 看门狗    │    │      │
│   │   │  - 进程级 lifetime      │    │  - 事件总线            │    │      │
│   │   └────────┬────────────────┘    └────────────────────────┘    │      │
│   │            │ subscribe(convId, { onDelta, onDone, onError })     │      │
│   │            ▼                                                     │      │
│   │   ┌──────────── 组件订阅 (Vue refs) ─────────────┐              │      │
│   │   │  AIChatView    SessionConversationView       │              │      │
│   │   │  PromptOpt     GatewayLiveStreamView         │              │      │
│   │   │  NotesSearch   MeetingSummary                │              │      │
│   │   └────────────────────────────────────────────────┘              │      │
│   │                                                                 │      │
│   └─────────────────────────────────────────────────────────────────┘      │
│                                                                          │
│   ┌─────────── Native 进程保活 ──────────────┐                           │
│   │  iOS: UIBackgroundModes=[fetch,processing]│                           │
│   │       + beginBackgroundTaskWithName        │                           │
│   │  Android: FOREGROUND_SERVICE_DATA_SYNC     │                           │
│   │       + partial wake lock (短时长)         │                           │
│   └────────────────────────────────────────────┘                           │
└──────────────────────────────────────────────────────────────────────────┘
```

## 3. 关键设计决策

### 3.1 D1 — AI 流所有权上移（`AiStreamRuntime`）

**做什么**：新建 `frontend/src/native/aiStreamRuntime.ts`，**单例**（同 `mobileSyncRuntime.ts` 模式），`main.ts` 启动时 `aiStreamRuntime.start()` 一次。

**接口**：

```ts
// 伪代码，体现契约；具体实现见 §4
type StreamId = string  // e.g. `${kind}:${entityId}` = `chat:c-123` / `session:s-456`

interface StreamHandle {
  id: StreamId
  abort(): void                 // 用户主动停止
  pause(): void                 // 隐藏态时停 UI 订阅（流仍跑）
  resume(): void                // 显式恢复 UI 订阅
  status(): 'running' | 'paused' | 'done' | 'error' | 'aborted'
}

interface AiStreamRuntime {
  // 启动一个流（幂等；同 id 已存在则返回旧 handle，不重启）
  spawnChat(input: ChatInput, onDelta, onDone, onError): StreamHandle
  spawnSession(sessionId, instanceId, onEvent): StreamHandle
  spawnGatewayLive(nodeId, onEvent): StreamHandle
  // 订阅已有流（订阅 = 接管 onDelta 回调；订阅者离场不杀流）
  subscribe(streamId: StreamId, subscriber: Subscriber): Unsubscribe
  // 用户主动停
  stop(streamId: StreamId): void
  // 进程级启停
  start(): void   // main.ts 调用一次
  stop(): void    // 仅在 logout / 异常退出调用
}
```

**契约要点**：
- `spawnXxx` 是幂等的：`spawnChat({convId: 'c-123', ...})` 第二次调用返回**第一次的 handle**（流在跑就不要再开）。
- 取消 = `abort()`，调用方只剩三种：(a) 用户按"停止"按钮；(b) 任务服务端明确失败且不可恢复；(c) 显式 dispose（如删除会话）。
- 组件 `onBeforeUnmount` **只调 `unsubscribe`**，**不**调 `abort/stop`。这与 `MobileSyncRuntime` 一致。
- 流收到 `onDelta` 时，把 delta 推入内部缓冲 + 通知所有当前 subscriber。Subscriber 离场后下次再 subscribe，会先拿到"未消费的 delta replay"（≤ N 帧防爆）。

### 3.2 D2 — AppLifecycleHub + 120s 看门狗重写

**做什么**：新建 `frontend/src/native/appLifecycleHub.ts`，把所有"页面/原生生命周期"事件统一收口，向订阅者派发：
- `event: 'hidden'`  —— 切到后台（iOS / Android / 浏览器）
- `event: 'visible'` —— 切回前台
- `event: 'frozen'`  —— `freeze` 事件（iOS Safari / PWA）
- `event: 'resumed'` —— `resume` 事件

**关键**：`AiStreamRuntime` 订阅 `hidden/frozen`：
- 收到 `hidden`：`runtime.markBackground()` —— 暂停 120s 看门狗计时（记下"已运行 X 秒"，唤醒后接着 X+30s 总和判定，而不是从 0 重置）。
- 收到 `visible`：`runtime.markForeground()` —— 重启看门狗计时（剩余预算），但**不**断开 SSE / fetch。

**为什么这么改**：JS 计时器在 iOS 后台会被冻结，所以"切走 5 分钟"实际不会触发 setTimeout（这点对 120s 看门狗是利好！）。问题是**网络**会被冻结 → 流被服务端 timeout 关掉 → 切回看到 EOF。我们要做的不是"延长看门狗"，而是"在流被切断时**不要**给 UI 报'超时'"，改为报"网络中断/已停止"，并让 store 在切回时主动尝试**续连**（见 D3）。

**实现要点**（`llm-bff.ts:101-102` 改造）：
- 旧：`setTimeout(() => ctrl.abort(), 120_000)` → `onError('响应超时')`
- 新：`setTimeout(() => ctrl.abort(), 120_000)`，但 `runtime.markBackground()` 期间不重置 / 不触发；切回前台后用"实际活跃时间"重算。abort 走 `markAborted(reason)` 通道，由 `aiChatStore` 根据 reason 决定文案：
  - `reason === 'watchdog'` + 最近 60s 内 `hidden` 事件 → 文案"网络中断，可重试"（不红字）
  - `reason === 'watchdog'` + visible → 文案"响应超时"
  - `reason === 'user'` → 文案"已停止"
  - `reason === 'server-error'` → 服务端 error frame 透传

### 3.3 D3 — Session 切走不关 SSE（**最关键**的一处）

**现状问题**：`SessionConversationView.onBeforeUnmount`（L177-183）调 `store.close()` → `sseClient.close()`。

**改造**：
- `stores/session.ts` 新增 `detach()`（清空 UI 引用但**不**关 SSE）和原 `close()`（关 SSE，**只在删除会话 / 显式 logout 调用**）。
- 路由变化时：先 `detach()`，新会话页 `open()` 时检查 "同 sessionId + instanceId 已有活跃 SSE？" → 有则 `attach()`（恢复 UI 订阅 + 拿 replay），无则新建。
- SSE 续命规则：同 `id` 的 SSE 允许后台保持 30 分钟（`AI_STREAM_BACKGROUND_TTL_MS`，可配），超时后才真正关掉（避免用户半年不回来导致连接泄漏）。
- 后端 `opencode.EventStreamManager` 已支持 `Subscribe` 返回 `<-chan DomainEvent`（`event_stream.go:111-137`），**无需后端改动**。

### 3.4 D4 — 审批轮询 + 通知解绑到进程级

**做什么**：把 `usePendingApprovals` 的"轮询"从 SessionConversationView 上移。改造成 `runtime.subscribeApprovals({ onPending })`，单例运行在 `AiStreamRuntime` 上。**只要 App 进程活着**就持续轮询，**不**依赖当前页面。
- iOS 限制：后台冻结会让轮询暂停；**真后台**靠本地通知（`@capacitor/local-notifications`），轮询作为前台补充。
- Android：前台服务 + 通知渠道。

### 3.5 D5 — 原生层保活（iOS / Android）

**iOS 改动**（`frontend/ios/App/App/Info.plist`）：

```xml
<key>UIBackgroundModes</key>
<array>
    <string>fetch</string>          <!-- BGTaskScheduler 拉数据 -->
    <string>processing</string>      <!-- 短任务 (< 30s) -->
    <string>remote-notification</string>  <!-- 推送唤醒 -->
</array>
```

**iOS 改动**（`AppDelegate.swift`）：
- `applicationDidEnterBackground` → 调 `[application beginBackgroundTaskWithName:@"ai-streams" expirationHandler:^{...}]`，30s 缓冲 + 通知 Capacitor 派发 `hidden` 事件。
- `applicationDidBecomeActive` → `[application endBackgroundTask:]` + 派发 `visible`。

**Android 改动**（`AndroidManifest.xml`）：
- 新增 `android.permission.FOREGROUND_SERVICE_DATA_SYNC`
- 新增 `android.permission.WAKE_LOCK`（仅用于"AI 流进行中"时短时持有锁）
- 现有 `MeetingRecordService` 已示范 ForegroundService 写法；新建 `AiStreamService.java`（参考实现，**MVP 仅在 iOS 上做**——Android 通过 `FOREGROUND_SERVICE_DATA_SYNC` + WebView 自身可短时保活）。

**重要警告（iOS App Store 审核）**：`UIBackgroundModes: processing` 需要声明"用户可见的长时间任务"，且每次进入后台要 `UIApplication.shared.beginBackgroundTask`。滥用会被拒。MVP 阶段只声明 `fetch` + `remote-notification`，**不**用 `processing`，靠推送拉醒。

### 3.6 D6 — Vue 组件迁移（**所有 AI 页面统一改造**）

| 组件 | 现状 | 改后 |
|---|---|---|
| `AIChatView.vue` | `onUnmounted` 只清 ResizeObserver（`AIChatView.vue:697`） | 改 `unsubscribe`；**不**abort 任何 controller |
| `SessionConversationView.vue` | `onBeforeUnmount` 调 `store.close()` + `stopApprovalPolling` + `stopLive`（L177-183） | 改 `detach()`（仅清 UI 引用）；审批轮询在 runtime 层继续 |
| `GatewayLiveStreamView.vue` | `onUnmounted` 关 SSE（L196） | 改 `unsubscribe`；SSE 仍在 runtime |
| `usePromptOptimizer.ts` | 暴露 `abort()` 但无消费方调 | 弹窗 `onBeforeUnmount` 调 `unsubscribe`（**不** abort）；abort 仅用户点"取消" |
| `stores/opencode.ts:248-287` | WS 无清理 | 改 `subscribeToRealTimeUpdates` → 走 runtime（OpenCode Hub 已在 Store 内，**这处不重构**，仅补 leak fix：把 WS 引用存到模块级 `let ws: WebSocket \| null` + `subscribe/unsubscribe`） |

## 4. 详细实现

### 4.1 新增 `frontend/src/native/aiStreamRuntime.ts`

骨架（伪代码，落地时补注释）：

```ts
// 参考 mobileSyncRuntime.ts 的 singleton + 事件订阅风格

type Subscriber<T> = (delta: T) => void

class AiStreamRuntime {
  // 流注册表：id → { ctrl, buf, subs, status, kind, spawnAt }
  private registry = new Map<StreamId, StreamEntry>()

  // 公开 API
  spawnChat(input: ChatInput, handlers: ChatHandlers): StreamHandle { ... }
  spawnSession(sid: string, iid: string, handlers: SessionHandlers): StreamHandle { ... }
  spawnGatewayLive(nodeId: string, handlers: GatewayHandlers): StreamHandle { ... }
  subscribe<T>(id: StreamId, sub: Subscriber<T>): () => void { ... }
  stop(id: StreamId, reason: 'user' | 'server-error' | 'watchdog'): void { ... }
  start(): void { ... }
  stop(): void { ... }

  // 内部：被 AppLifecycleHub 调用
  markBackground(): void {
    // 1. 暂停所有流的 120s watchdog
    // 2. 不 abort SSE / fetch
    // 3. iOS 推本地通知占位（如果有 .pendingApproval / .streaming > 0）
  }
  markForeground(): void {
    // 1. 重启 watchdog 剩余预算
    // 2. 触发流做"心跳探测"：写一帧 ping 到 buf 让 UI 切回时立即拿到"还在跑"
  }
}
```

### 4.2 新增 `frontend/src/native/appLifecycleHub.ts`

```ts
// 单一职责：把 visibilitychange / appStateChange / freeze / resume 收口成
// 4 个语义事件。runtime 只订阅 hub，不知道 DOM 还是 Capacitor。
class AppLifecycleHub {
  on(event: 'hidden'|'visible'|'frozen'|'resumed', cb: () => void): Unsubscribe
  start(): void  // 注册 visibilitychange / freeze / resume / Capacitor appStateChange
  stop(): void
}
```

### 4.3 改造 `frontend/src/api/llm-bff.ts` 的 streamChat

关键改动点：
1. `streamChat` 内部不再直接 `fetch`，改为调 `aiStreamRuntime.spawnChat(...)`，返回 `StreamHandle`。
2. `AbortController` 仍归 runtime 所有（不是 UI 组件）。
3. 120s watchdog：调用方传 `{ signal, onBackground, onForeground }` 钩子；runtime 在 `onBackground` 暂停 watchdog、`onForeground` 恢复剩余预算。
4. 错误信息由 runtime 决定（"用户停止" / "网络中断" / "响应超时"），流层只传 `reason`。

### 4.4 改造 `frontend/src/stores/session.ts`

```ts
function detach() {
  // 切走：清 UI 状态（currentAssistantId、scroll 等），但**不**关 SSE
  currentAssistantId.value = null
  status.value = 'idle'
}
function close() {
  // 真正关闭（删除会话 / 显式登出时才调）
  if (sseClient.value) sseClient.value.close()
  sseClient.value = null
  sessionID.value = null
  instanceID.value = null
  status.value = 'idle'
  currentAssistantId.value = null
  aiStreamRuntime.stop(`session:${sessionID.value}`, 'user')
}
```

### 4.5 改造 `SessionConversationView.vue` L177-183

```ts
onBeforeUnmount(() => {
  unbindLocalChrome?.()
  dockRO?.disconnect()
  store.detach()       // 改：close → detach
  // 删除：stopApprovalPolling / sessionEvents.stopLive / store.close
  // （审批轮询和 session activity 订阅在 runtime 持续）
})
```

### 4.6 iOS Info.plist 改造

```xml
<key>UIBackgroundModes</key>
<array>
    <string>fetch</string>
    <string>remote-notification</string>
</array>
```

注：**不**加 `processing`（App Store 审核风险）。`fetch` 模式靠 `BGTaskScheduler` 注册短任务拉数据；MVP 阶段不接 BGTaskScheduler，仅靠 iOS 系统在 `applicationDidEnterBackground` 后给的约 30s 缓冲。

### 4.7 Android Manifest 改造

新增权限：
```xml
<uses-permission android:name="android.permission.FOREGROUND_SERVICE_DATA_SYNC" />
<uses-permission android:name="android.permission.WAKE_LOCK" />
```

新建服务占位（**MVP 不实现**，留 TODO）：
```xml
<service
    android:name=".plugins.AiStreamService"
    android:exported="false"
    android:foregroundServiceType="dataSync" />
```

## 5. 迁移路径（落地顺序）

| 阶段 | 内容 | 风险 | 估时 |
|---|---|---|---|
| **M1** | 新建 `appLifecycleHub.ts` + `aiStreamRuntime.ts` 骨架（仅 chat 流 + watchdog 重写） | 低（与现有并存） | 0.5d |
| **M1** | 改造 `aiChatStore` + `AIChatView` 走 runtime | 中（核心路径） | 1d |
| **M1** | 跑通验收场景 1-3（chat 切标签 30s/5min） | — | 0.5d |
| **M2** | Session 切走不关 SSE（`session.ts` + `SessionConversationView`） | 中 | 1d |
| **M2** | `usePendingApprovals` 上移到 runtime | 中 | 0.5d |
| **M2** | 跑通场景 4-5 | — | 0.5d |
| **M3** | iOS `UIBackgroundModes: [fetch, remote-notification]` + `AppDelegate` 派发 hidden/visible | 高（审核 + 真机验证） | 1d |
| **M3** | iOS 真机验收 9 号场景 | — | 0.5d |
| **M4** | Prompt Optimizer / Gateway Live / Notes AI 切到 runtime | 低（外围） | 1d |
| **M4** | OpenCode Hub WS leak 修复 | 低 | 0.5d |
| **M5** | Android `FOREGROUND_SERVICE_DATA_SYNC` + `AiStreamService`（可选；MVP 跳过） | 高 | 1d |
| **M5** | PWA `freeze`/`resume` 处理（Safari 桌面 PWA 切后台） | 中 | 0.5d |

## 6. 验证方案

### 6.1 单元 / 集成

- `tests/ai-stream-runtime.spec.ts`：vitest
  - 同 id `spawnChat` 两次 → 返回同一 handle
  - `subscribe` → 收到 delta；`unsubscribe` → 后续 delta 静默；再 `subscribe` → 拿到 buf replay
  - watchdog 在 `markBackground()` 期间不触发；`markForeground()` 后剩余预算继续
  - 错误 reason 分类正确

### 6.2 E2E（playwright）

`tests/e2e/ai-stream-background.spec.ts`：
- 打开 chat 页 → 发起流 → `page.evaluate(() => Object.defineProperty(document, 'visibilityState', {value:'hidden', configurable:true}))` + 派发 `visibilitychange` → 等 5s → 模拟 visible → 断言气泡完整
- Session：同上，断言 SSE 仍 connected（`runtime.registry.get(...)` status === 'running'）
- 切回时无"超时" toast

### 6.3 真机

- iPhone（Safari + Capacitor App）：按 home 30s / 2min / 5min 三档
- Android（同上）
- 网络断网 → 恢复 模拟

## 7. 风险登记

| 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|
| iOS App Store 审核拒（UIBackgroundModes 滥用） | 中 | 高 | **MVP 只加 `fetch` + `remote-notification`**，不声明 `processing`；提交时附"AI 流持续是用户主动行为"说明 |
| iOS 真机 30s 冻结仍断流 | 高 | 中 | M3 不保证 5min，仅保证 ≤ 30s（系统给的窗口）；超 30s 切回会触发"网络中断"提示，**不**是"超时" |
| Android 厂商 ROM 杀进程 | 高 | 中 | MVP 不做厂商适配；用户教育（"AI 进行中请勿清理后台"）；M5+ 接入厂商推送通道 |
| 内存：SSE 缓冲膨胀 | 低 | 中 | 流内 buf 上限 1MB；超过则强制落盘 IndexedDB（用现有 `local-db.ts`） |
| Runtime 单例与 SSR / 测试冲突 | 低 | 低 | 与 `mobileSyncRuntime` 同样模式：用 `globalThis.__aiStreamRuntime__` 兼容 HMR |
| Watchdog 改造引入新 bug | 中 | 高 | M1 跑回归：`tests/api/llm-bff.spec.ts` 已有的 12s 超时用例 + 新增"隐藏态不超时"用例 |

## 8. 不在本方案范围

- 浏览器关闭后继续跑（Service Worker Background Sync）—— v2 候选
- AI 任务优先级队列 —— 不变
- 客户端 resumable stream（SSE 续 last-event-id）—— 已在 `api/sse.ts` 有 `?after=` 续命；本方案复用
- 厂商推送通道（小米/华为/vivo/OPPO）—— M5+ 单独专题
- HarmonyOS 后台保活 —— 跟随 [[../../openpocket-native-roadmap]] 走

## 9. 关联

- 需求：[../requirements/2026-09-09-ai-async-background-survival.md](../requirements/2026-09-09-ai-async-background-survival.md)
- 现行模式参考：`frontend/src/native/mobileSyncRuntime.ts`（singleton + 事件订阅 + Capacitor resume）
- 关键文件清单：
  - 前端：`frontend/src/api/llm-bff.ts` · `frontend/src/api/sse.ts` · `frontend/src/api/gateway-live.ts` · `frontend/src/features/ai-chat/aiChatStore.ts` · `frontend/src/features/ai-chat/AIChatView.vue` · `frontend/src/features/sessions/SessionConversationView.vue` · `frontend/src/features/sessions/useSessionEvents.ts` · `frontend/src/stores/session.ts` · `frontend/src/composables/usePromptOptimizer.ts` · `frontend/src/composables/usePendingApprovals.ts` · `frontend/src/stores/opencode.ts`
  - 原生：`frontend/ios/App/App/Info.plist` · `frontend/ios/App/App/AppDelegate.swift` · `frontend/android/app/src/main/AndroidManifest.xml` · `frontend/android/app/src/main/java/.../plugins/MeetingRecordService.java`
  - 后端（无需改动，仅参考契约）：`backend/internal/llmbff/service.go` · `backend/internal/llmgateway/stream.go` · `backend/internal/opencode/event_stream.go` · `backend/internal/websocket/mobile_hub.go`
