# T4 决策：iOS Safari / WKWebView 的 AI 流后台续传方案

| 字段 | 值 |
|------|----|
| 日期 | 2026-09-10 |
| 任务来源 | handoff 2026-09-10 §2 T4（P1，依赖 T3 结论） |
| 状态 | **决策已定：短期 A（现状，已验收），中期 B（Native+Capacitor 桥）** |
| 关联 | [[openpocket-ai-async-background-survival]]、[T3 诊断](./2026-09-10-sse-120s-watchdog-diagnosis.md) |

---

## 1. 事实基础（已实测，不再假设）

1. **WKWebView 存活期间 SSE 不断**——真正断流只有「系统挂起 App >30s」与「用户 kill」两种
   （2026-09-09 设计阶段实测，设计文档 §2）。
2. iOS 侧 M3 已落地：`UIBackgroundModes=[fetch, remote-notification]`（Info.plist）+
   `AppDelegate.beginBackgroundTask("ai-stream-buffer")` 约 30s 后台缓冲。
3. **iOS Simulator 三档验收已通过**（2026-09-09，iPhone 17 Pro / iOS 26.3）：后台 30s / 2min /
   5min 回前台，进程存活、流持续推进、145s 后台无前端超时误报（watchdog 暂停生效）。
4. T3 结论：120s 看门狗是纯前端预算，与网络层无关 → **iOS 上不存在"120s 断流"问题**；iOS
   唯一要对抗的是「系统挂起后 WebView 网络栈冻结，30s 缓冲耗尽后流必然死」。
5. 模拟器不执行真机的 30s JS 冻结——**真机验收仍是缺口**（需签名设备，P0 残留）。

## 2. 候选方案对比

```
方案 A（现状）              方案 B（Native+Capacitor 桥）         方案 C（切 WebSocket）
┌─────────────┐            ┌──────────────┐                     ┌──────────────┐
│ WKWebView    │            │ WKWebView     │                     │ WKWebView    │
│  fetch SSE   │            │  订阅 delta   │                     │  WS 客户端   │
└──────┬───────┘            └──────▲───────┘                     └──────▲───────┘
       │ 挂起=断                    │ Capacitor 桥事件                    │ 挂起=同样断
┌──────▼───────┐            ┌──────┴────────┐                     ┌──────┴───────┐
│ 后端 SSE     │            │ NSURLSession   │                     │ 后端 WS 网关 │
└──────────────┘            │  原生长连(免冻) │                     └──────────────┘
                            └──────┬────────┘
                                   │ 后端 SSE（不变）
                            ┌──────▼────────┐
                            │ 后端 SSE 服务  │
                            └───────────────┘
```

| 维度 | A. 现状维持 | B. Native 桥（NSURLSession SSE） | C. 切 WebSocket |
|------|------------|--------------------------------|-----------------|
| 开发成本 | 0（已落地） | 中：1 个 Capacitor 插件（对齐 Android `AiStreamKeepalivePlugin` 模式）+ JS fetcher 适配 ~3-5 人日 | 大：服务端 SSE→WS 协议迁移 + 网关/代理配置 + 前端重写流层，数周级 |
| 解决"系统挂起断流" | 部分（30s 缓冲内活着；fetch mode 由系统调度、频率不可保证） | **是**（原生 socket 不随 WebView 冻结，通知/前台服务级保活） | 否——WS 在 iOS 挂起时同样被掐，仍需原生保活，等于回到 B |
| 兼容性 | 全版本 iOS | 需 iOS 13+（项目基线），无新增系统依赖 | 需后端 WS 网关与既有 SSE 并行运行 |
| 可维护性 | 现状即维护基线 | 契约不变：`AiStreamRuntime.setStreamDeps` 注入原生 fetcher，运行时零改动；插件面与 Android 对称 | 两套推送通道（SSE+WS）长期并存，心智负担最重 |
| 审核风险 | `fetch`/`remote-notification` 需解释用途（已知） | 不新增 BackgroundModes，无新增风险 | 无新增 |
| 额外收益 | — | 原生层可顺带做重连/退避/静默推送唤醒（`remote-notification` 已就位） | 双向通道（未来协作编辑类功能） |

## 3. 决策

- **短期（当前季度）：方案 A。** 三档模拟器验收已过、真机验收待做；现有体验（切走几分钟内
  回来不断流）已覆盖绝大多数真实使用。
- **中期（真机验收暴露冻结断流后启动）：方案 B。** 触发条件（满足其一）：
  1. 真机三档验收中「后台 >30s 回前台」出现流死亡且不可自动恢复；
  2. 产品要求后台生成 >1 分钟的长任务（如长文/批量审批）。
- **否决 C。** 关键理由：WS 不解决 iOS 挂起断流的本因，只是把传输层换掉，保活问题原样存在，
  还要付出服务端协议迁移的全量成本。

## 4. 方案 B 实施 PR 计划（启动时照此执行）

1. **PR-1 插件骨架**：`frontend/ios/App/` 新增 `AiStreamNativeStreamPlugin.swift`（注册名
   `AiStreamNativeStream`），API：`start(spec)` / `abort(id)` / 事件 `delta|done|error`；
   NSURLSession dataTask + 手写 SSE 分帧（对齐 `aiStreamRuntime.runChat` 的 `\n\n` 解析）。
2. **PR-2 JS 适配**：`frontend/src/native/` 新增 `nativeStreamFetcher.ts`，实现 `SpawnFetcher`
   契约（`aiStreamRuntime.ts:103`），平台检测 iOS 时经 `setStreamDeps` 注入；Web/Android 走
   现有 fetch。**运行时与业务层零改动。**
3. **PR-3 保活联动**：AppDelegate 在挂起前对活跃流调原生保活（B 方案下原生连接自持，无需
   `beginBackgroundTask` 续命，但保留 30s 缓冲兜底）；`remote-notification` 接 silent push
   唤醒（阶段 4 后端联动，见 handoff §2 阶段清单）。
4. **验收**：iOS 真机三档（30s/2min/5min）+ 后台期间 delta 持续回放（`MAX_REPLAY_FRAMES`
   上限内）。

## 5. 与本会话改动的联动

本会话修复了 `appLifecycleHub` 的 Capacitor 通道 thenable 缺陷（`loadCapacitorApp` 信封化，
见 `aiStreamKeepalive.ts` 头注释）——修复前 iOS 上 `appStateChange` 订阅静默失败，M4 的
watchdog 暂停在真机**只靠 DOM visibilitychange 兜底**。此修复使 B 方案（以及现状 A）依赖的
Capacitor 生命周期通道真正可用，属 T4 的前置修复。

— end of decision doc —
