# 需求：AI 操作全异步化 + 切换窗口/标签不中断

> 落档日期：2026-09-09
> 状态：Draft，待评审
> 关联：[[../design/2026-09-09-ai-async-background-survival.md]]（设计/方案）

## 1. 背景

openpocket 现行实现里，**所有 AI 相关操作（对话、Agent、Session、Notes AI、Meeting 总结、Prompt 优化、ACC 任务下发、Gateway 实时事件……）都已经是异步发起**（fetch / SSE / WebSocket / 后台 goroutine）。但"发起异步"≠"全程不中断"。当前实现把每个流资源的生命周期绑死在 **Vue 组件**（`onMounted` ↔ `onBeforeUnmount`）上，导致下面这些 **路径会让 AI 操作在用户切走时**：

1. **被静默 abort**（最严重）：用户从 Session 详情页切走 → `SessionConversationView.onBeforeUnmount` 调用 `store.close()` → 关闭 EventSource → 后续 LLM 帧全部丢失。AI 跑得好好的，用户切去刷 RSS，回来发现 "session 已停止"。
2. **被静默"超时报错"**：流式 120s 看门狗（`llm-bff.ts:102`）只对"无字节"计时。WebView/iOS 后台冻结 → JS 计时器暂停但 `fetch` 也被冻结 → 唤醒后继续，但流已经被服务端 timeout 中断 → 用户切回来看到「响应超时」。
3. **被静默"组件销毁"但流未停**：`aiChatStore.controllers: Map<string, AbortController>` 留了 controller 但 `AIChatView.onUnmounted`（`AIChatView.vue:697`）只清理 ResizeObserver/scroll，**不 abort**。结果：流还在跑、UI 已销毁、气泡收不到后续 delta → 用户回来看见"未完成的半截气泡"，且无 stop 按钮。
4. **审批轮询被停**：`usePendingApprovals.stopPolling()` 在会话页 `onBeforeUnmount` 调用 → 用户离开会话页后，AI Agent 在等用户审批而用户没看到通知。
5. **原生层被系统杀**：`frontend/ios/App/App/Info.plist` 没有 `UIBackgroundModes`；`frontend/android/app/src/main/AndroidManifest.xml` 没有 `FOREGROUND_SERVICE_DATA_SYNC` / `WAKE_LOCK`；iOS WebView 后台后 **30 秒内 JS 计时器和网络被系统冻结**（更别说 Capacitor 的 fetch / SSE）。

## 2. 目标

> **所有 AI 相关操作都是异步的，并且不因窗口/标签被切掉而中断。**

可验收的硬指标（DoD）：

1. **Web 切标签不中断**：用户在浏览器里把 openpocket 标签切到后台 ≥ 5 分钟再切回来，所有进行中的 AI 流（chat、agent、session、notes AI、meeting summary、prompt optimizer）的最终结果仍展示在 UI 上，无"超时"、无"中断"、无"半截气泡"。
2. **App 切后台不中断**：iOS / Android App 切到后台 ≥ 30 秒（iOS 典型冻结窗口）再切回前台，AI 任务继续且结果送达。
3. **切回页面不发起重连风暴**：流在后台持续保持，**不**因为切走就 disconnect / reconnect。回前台时能立刻拿到最新一帧（无空白窗口）。
4. **用户主动停止仍生效**：UI 上"停止生成"按钮在切走后再切回仍可用，能真正 abort 掉流。
5. **错误归因正确**：流在后台被服务端 timeout 取消时，UI 提示是"已结束（xx 字节）"或"网络中断"，**不**是误导性的"响应超时"。
6. **PWA 离线降级**：浏览器杀进程后再打开，AI 历史 / 任务快照可恢复（已流到一半的对话不丢，但有"已中断"标记，参考现有 `migrateConversations` 行为）。

## 3. 非目标

- **不**做"浏览器关闭后继续跑"（要 Service Worker Background Sync 级别，超出本次范围；标记为 v2 候选）。
- **不**做"AI 任务优先级队列"——只保证"已开始的不被中断"，不调度尚未开始的。
- **不**改后端协议（SSE / WebSocket / HTTP 仍保持现状），不引入新的长轮询/Server Push。
- **不**改 LLM Provider（豆包/OpenAI/Claude 等），不引入"客户端 resumable stream"。
- **不**做 PWA 离线壳（manifest.json / service worker 全套）——只做"Web 切后台保持"这一项。

## 4. 验收场景（E2E）

| 场景 | 期望 |
|---|---|
| Chat：发出 30s 长 prompt，10s 时切走，30s 时切回 | 看到完整答案，**无"超时"提示** |
| Chat：发出后切走 5 分钟 | 5 分钟后切回，答案完整；UI 状态显示"已完成"，气泡无半截 |
| Chat：发出后切走，用户主动按"停止"再切回 | 看到已收到的内容 + "已停止" |
| Session：在 OpenCode Session 页发起 round，10s 时切走，60s 时切回 | 看到 round.completed 帧，activity timeline 完整 |
| Session：切走后审批请求到达 | 切回页面时 Bottom Sheet 自动弹出（轮询 / WS 持续） |
| Notes AI：在笔记页输入查询，切走 2 分钟，切回 | 看到结果回填到结果框 |
| Prompt Optimizer：弹窗打开后切走 10 分钟，切回 | 结果已就位（DOM 一直在，stream 持续） |
| Meeting Summary：会议纪要生成中按 home 键 30s 再回前台 | 摘要继续生成，结束时正常 toast |
| iOS 真机：发送 AI 请求，按 home 切到桌面等 2 分钟，回 App | 看到完成结果 |

## 5. 风险与约束

- iOS WebView（WKWebView）**30 秒后台冻结**是平台硬约束。光靠 JS 改不动；必须 `UIBackgroundModes: ["fetch", "processing"]` + `beginBackgroundTaskWithName(...)`。
- Android WebView 不像 iOS 那样严格冻结，但**多任务清理**和**Doze 模式**仍可能让 fetch 中断；需要 `FOREGROUND_SERVICE_DATA_SYNC` 兜底。
- 后端 SSE/WebSocket 服务端**没有"用户离线感知"机制**——本方案不引入（会让架构变重），继续由"前端保持连接 + 后端无脑转发"模式工作。
- 120s 看门狗是已有事故兜底（防 LLM 静默挂死），不能撤除；要按"是否被切到后台"做不同处理。

## 6. 关联

- 现行特征大盘：[../2026-09-08-feature-inventory.md](../2026-09-08-feature-inventory.md)
- 设计/方案：[../design/2026-09-09-ai-async-background-survival.md](../design/2026-09-09-ai-async-background-survival.md)
- 移动端 UX v2：[../2026-08-27-mobile-ux-design-v2.md](../2026-08-27-mobile-ux-design-v2.md)
- 原生路线图：[[../../openpocket-native-roadmap]]（memory）
