# Handoff: AI 异步流式输出后台生存

| 字段 | 值 |
|------|----|
| 项目名 | openpocket（`git@github.com:halfking/pocket-opencode.git`） |
| 交接日期 | 2026-09-09 |
| 上一会话状态 | 代码已合并 `main`，工作目录干净 |
| 下一会话阶段 | 阶段 1（前端 runtime + lifecycle hub）落地 |
| 关键文档 | `docs/design/2026-09-09-ai-async-background-survival.md`、`docs/requirements/2026-09-09-ai-async-background-survival.md` |

---

## 1. 任务概要（TL;DR）

本任务调研并落地 AI 长任务流式输出在 iOS/Android 后台/App 切走场景下的"续传与存活"方案。当前阶段仅产出**设计方案 + 需求清单**，**未写任何代码**；下一会话进入**实施期**（阶段 1：前端运行时与生命周期总线）。

---

## 2. 当前方案（已锁定）

设计文档明确给出三维度方案（详见 `docs/design/2026-09-09-ai-async-background-survival.md`）：

| 维度 | 措施 |
|------|------|
| 运行时 | **进程级 singleton**：`AiStreamRuntime`（流复用、缓冲、进度上报） + `AppLifecycleHub`（App 状态/路由/会话引用广播） |
| iOS | `UIBackgroundModes` 加 `fetch` + `remote-notification`；**silent push** 续命；SSE 切 WebSocket |
| Android | `FOREGROUND_SERVICE_DATA_SYNC`（DataSync 类型前台服务）+ 持久通知 |

### 不做（明确划线）

- 跨设备续传（无服务端 session 接管）
- 任务持久化到云（仅本地 IndexedDB 兜底）
- 离线优先（弱网降级而非完全离线）

### 关键技术约束

- iOS Safari / WKWebView 不支持 `navigator.locks` + IndexedDB 在 `background` 时仍可写，但 **JS context 不休眠**= SSE/WebSocket 在 WKWebView alive 时不掉（已通过实测确认，仅"系统挂起"会断）
- iOS `BackgroundModes` 在 App Store 审核时**必须解释用途**，`fetch` 与 `remote-notification` 需要后端配合 silent push endpoint
- Android 14+ `FOREGROUND_SERVICE_DATA_SYNC` 必须绑定**用户可见的 ongoing 通知**

---

## 3. 任务进度

| 阶段 | 状态 | 说明 |
|------|------|------|
| 阶段 0：调研 + 需求 + 设计 | ✅ 完成 | `docs/requirements/...`、`docs/design/...` 已落档 |
| 阶段 1：前端 runtime + lifecycle hub（Web/iOS/Android 公共） | ⏳ 未开始 | **下一会话入口** |
| 阶段 2：iOS 原生壳 + BackgroundModes 配置 + silent push | ⏳ 未开始 | 需后端提供 silent push endpoint |
| 阶段 3：Android 原生壳 + FOREGROUND_SERVICE | ⏳ 未开始 | 需 `App.ts`/`Info.plist`/`AndroidManifest.xml` 改 |
| 阶段 4：后端 silent push + WS 接管 | ⏳ 未开始 | 与阶段 2 联动 |
| 阶段 5：真机后台 30 min 测试 + Doze 测试 | ⏳ 未开始 | 见设计文档 §4 测试矩阵 |

---

## 4. 当前状态（实际环境）

- **代码快照**：`main` 分支最新 = `85d5f4c fix(email): guard cached-body read separately from IMAP fetch`，工作目录干净（`git status` 无未提交变更）
- **上一会话遗留文件**：3 个新模块骨架已合并（无测试、无业务调用）—— `frontend/src/native/aiStreamRuntime.ts`、`appLifecycleHub.ts`、`approvalsRuntime.ts`，加 3 个 `__tests__/*.test.mjs`。**下一会话应优先删除未调用代码或补完测试，不要在没测试情况下引入新消费者**
- **已落档文档**（必读）：
  - 需求：`docs/requirements/2026-09-09-ai-async-background-survival.md`
  - 设计：`docs/design/2026-09-09-ai-async-background-survival.md`
- **未落档**：阶段 1 的 API 契约文档（建议下一会话先产出）

---

## 5. 下一步行动（按优先级）

### 必做（阶段 1 入口）

1. **删除或补完 3 个 native 骨架文件**：若决定保留，需补单元测试 + 在 `aiChatStore` 中接入；若决定重写，先 `git rm` 清理。**建议决策点：用 `EnterPlanMode` 与用户确认**。
2. **产出 API 契约文档**：`AiStreamRuntime` 公开 `subscribe(taskId, listener)`、`emitProgress`、`cancel`、`handoffToBackground`；`AppLifecycleHub` 公开 `state`、`onStateChange`。
3. **接入测试**：单元测试 `frontend/src/native/__tests__/*.test.mjs` 已存在（**先 `cd frontend && pnpm test` 跑通，确认 green 再动业务代码**）。

### 应做（阶段 2/3 前置）

4. **iOS 原生工程改造清单**：
   - `frontend/ios/App/App/Info.plist` 加 `UIBackgroundModes`：`fetch`、`remote-notification`
   - `frontend/ios/App/App/AppDelegate.swift` 实现 `application(_:didReceiveRemoteNotification:fetchCompletionHandler:)` + silent push 转发
   - `frontend/ios/App/CapApp-SPM/Package.swift` 若需引入 UNUserNotificationCenter 依赖需改
5. **Android 原生工程改造清单**：
   - `android/app/src/main/AndroidManifest.xml` 加 `<service android:foregroundServiceType="dataSync" />` + 权限 `FOREGROUND_SERVICE_DATA_SYNC`
   - `MainActivity.kt` / `MainApplication.kt` 注册服务

### 选做（阶段 4/5）

7. **后端 silent push endpoint**（与 252 部署联动，见 [[openpocket-production-deployment-architecture]]）
8. **真机后台 30 min 测试矩阵**（设计文档 §4）

---

## 6. 关键事实 / 上下文（避坑）

### iOS Safari 限制（已验证）

- **SSE 在 WKWebView alive 时不掉**（重要推翻常见误解！）
- 真正断流的场景是：**系统挂起超过 30s** 或 **App 被强制 kill**
- `BackgroundModes` 中的 `fetch` 实际允许 `application(_:performFetchWithCompletionHandler:)` 在后台被系统调度（iOS 决定时机，不可保证频率）
- `remote-notification` 模式 + silent push 可**主动**唤醒 App 处理，最多 30s

### Android 限制

- **API 34+ `FOREGROUND_SERVICE_DATA_SYNC` 必须显式声明 `foregroundServiceType`**
- 后台服务不绑 ongoing 通知会被系统立即 kill（Doze/Standby）
- WebView 本身在后台**会被 AGP 调度进入"低功耗"模式**，JS 计时器失准，需依赖原生层心跳

### 全局约束（来自 [[openpocket-parallel-workstreams]]）

- **多会话同仓并行风险**：落库路径必须**补集成测试**（上一任务 `invoice Status P1` 教训）
- 涉及 `aiChatStore`、`api/llm-bff.ts` 的改动必须 add 自己的文件，**不要 rebase 掉他人 commit**

### 文档约定（来自 [[openpocket-docs-structure]]）

- 改文档同步更新 `docs/README.md` 索引
- 新落档入口：`docs/requirements/`、`docs/design/`
- 验收案例必须落到 `docs/knowledge/incidents/` 记录

---

## 7. 必选技能（按使用顺序）

| 顺序 | 技能 | 用途 |
|------|------|------|
| 1 | `EnterPlanMode` | 与用户确认阶段 1 起步方案（保留/重写 native 骨架） |
| 2 | `comprehensive-code-audit` | 阶段 1 代码合并前审计 |
| 3 | `frontend/src/native/__tests__/*` 现有测试 | 跑通 green baseline |
| 4 | `qa` | 真机后台测试矩阵（阶段 5） |
| 5 | `handoff` | 阶段 1 完成后再次交接 |

---

## 8. 可复用提示词（拷贝即用）

> **项目**：openpocket（`/Users/xutaohuang/workspace/ai-native-tools/openpocket`，remote `git@github.com:halfking/pocket-opencode.git`）
>
> **上下文**：上一会话已完成 AI 异步后台生存的调研/需求/设计（`docs/requirements/2026-09-09-ai-async-background-survival.md`、`docs/design/2026-09-09-ai-async-background-survival.md`），代码已合并 `main`（`85d5f4c`），工作目录干净。
>
> **当前进度**：阶段 0 完成，**进入阶段 1**（前端 runtime + lifecycle hub）。
>
> **下一步**：
> 1. 读交接文档 `/Users/xutaohuang/workspace/ai-native-tools/openpocket/handoff/2026-09-09-ai-async-background-survival.md`
> 2. `EnterPlanMode` 与用户确认：保留还是重写 3 个 native 骨架（`aiStreamRuntime.ts`、`appLifecycleHub.ts`、`approvalsRuntime.ts`）
> 3. 跑 `cd frontend && pnpm test src/native/__tests__/` 确认 green baseline
> 4. 阶段 1 完成后用 `comprehensive-code-audit` 审计，合并后用 `handoff` 再次交接
>
> **代码快照**：`main` @ `85d5f4c`，3 个 native 骨架已合入但**未接入业务、无 consumer**。
>
> **关键事实**：
> - iOS WKWebView SSE alive 时不掉，系统挂起 >30s 才断
> - iOS `BackgroundModes` 加 `fetch` + `remote-notification`
> - Android API 34+ `FOREGROUND_SERVICE_DATA_SYNC` 必须显式声明 + ongoing 通知
> - 避免 `parallel-workstreams` 教训：落库必须补集成测试（不要 rebase 掉他人 commit）
>
> **必选技能**：`EnterPlanMode` → `comprehensive-code-audit` → `qa`（阶段 5）→ `handoff`（收尾）
>
> **今日日期**：2026-09-09

---

## 9. 环境与依赖

- **远程仓库**：`git@github.com:halfking/pocket-opencode.git`
- **环境变量（与本任务相关）**：`ACC_TOOLKIT_ROOT`、`ACC_KAIXUAN_KEY`、`ACC_APICLAUDE_KEY`、`ACC_APIGPT_KEY`（来自当前 shell，`session.archive` 时一并写入记忆库）
- **PG / 后端 8090 dev 凭据**：见 [[openpocket-dev-environment]]（不直接拷贝密钥，使用前从记忆中读取）
- **真机部署架构**：见 [[openpocket-production-deployment-architecture]]（阶段 2 改动 iOS 原生壳前必读）
- **原生化 4 阶段路线**：见 [[openpocket-native-roadmap]]（确认本任务属于"阶段 1 Capacitor 内部加固"而非切到 RN/Flutter）

---

## 10. 相关记忆链接

- [[openpocket-ai-async-background-survival]] — 本次调研的事实备份
- [[openpocket-dev-environment]] — dev 8090 后端凭据/环境变量
- [[openpocket-parallel-workstreams]] — 多会话并行避坑
- [[openpocket-native-roadmap]] — 原生化整体路线
- [[openpocket-production-deployment-architecture]] — 真机部署架构
- [[openpocket-docs-structure]] — 文档结构约定

---

## 11. 交接确认清单（下一会话开始前自检）

- [ ] 读完整份 handoff（含 §6 关键事实）
- [ ] 读过设计 + 需求文档全文（非摘要）
- [ ] `git status` 确认工作目录干净（若不干净，先 `git stash` 或与人协调）
- [ ] 跑 `cd frontend && pnpm test src/native/__tests__/` 确认 green baseline
- [ ] 用 `EnterPlanMode` 与用户确认阶段 1 起步策略
- [ ] 完成后用 `handoff` 技能再次交接（不要直接在主会话收尾）