# OpenCode Pocket 移动端完整工作设计方案 v2

> **版本**: v2.0（取代 2026-07-03《移动端交互优化方案》v2.0 及 2026-07-05 前后的全部 UI 优化报告）
> **日期**: 2026-08-27
> **状态**: 设计定稿（design-accepted）；实现状态以 `docs/governance/STATUS-MATRIX.md` 为准，本文不声称任何功能"已完成"
> **上位规范**: `docs/2026-07-02-ui-ux-design-system.md`（设计系统：颜色/排版/组件/动效）继续有效，本方案在其约束下定义产品流程与交互
> **目标架构对齐**: `docs/新架构v1/02-modules/mobile-shell.md`（PocketFleet v3）、`docs/新架构v1/03-roadmap/里程碑.md`
> **核心用户目标**（用户 2026-08-27 确认）: **快速地了解并处理，看得明白，输入方便**

---

## 0. 本方案整合的需求来源

| 来源文档 | 吸收内容 | 处置 |
|---|---|---|
| `docs/2026-07-02-ui-ux-design-system.md` | 设计原则、导航规范、组件规范 | 继续作为上位规范，不取代 |
| `docs/MOBILE_ARCHITECTURE_V2.md` | 双屏布局、语音交互、审批 Bottom Sheet + 滑动手势 | 本文取代（已存档） |
| `docs/2026-07-03-mobile-interaction-optimization.md` | 信息密度、语音优先、分组折叠 | 本文取代（已存档） |
| `OPENCODE_MOBILE_MANAGEMENT_PLAN.md`（root，已存档） | 会话管理/操作/审批/配置/分析/消息/实时监控 七类需求清单 | 全部吸收进 §3-§6 |
| `NAVIGATION_ARCHITECTURE.md`（root，已存档） | 现有路由结构 | 以 `frontend/src/app/router-mobile.ts` 实际代码为准，本文 §3 重定义 IA |
| P0C/P1/P2/P3 报告（2026-08-15~17） | 设备门、SQLite 离线持久化、outbox 离线队列、diff 性能基线、iOS 验证 | 作为既有能力直接复用 |
| `docs/新架构v1/02-modules/mobile-shell.md` | PocketFleet v3 目标态（Live 页、Build Live View、通知分级、性能指标） | 本文是其"现有 App 渐进演进"路径 |
| 2026-08-27 会话优化分析（本文 §4-§6 的直接来源） | 健康度模型、轮次时间线、折叠屏策略、WS 事件粒度 | — |

---

## 1. 用户画像与核心工作流

### 1.1 画像

**远程驾驶员**：白天多数时间不在电脑前，用手机（直板小屏 / vivo X Fold5 折叠屏）遥控分布在多台服务器上的 OpenCode 实例执行编码任务。两类时刻：

- **碎片监控**（高频，单次 <30s）：掏出手机扫一眼"有没有任务卡住/等审批"；
- **快速介入**（低频，单次 1-3min）：审批一个 `git push`、回答 AI 提问、补一条指令、叫停跑偏的任务。

### 1.2 核心工作流（产品流程主线）

```
通知/习惯性打开
   ↓ (0-3s)
① 指挥中心分诊：有没有事？—— 无事即走
   ↓ 有事
② 两步内介入：审批/回答/停止/补指令
   ↓ 需要细看
③ 会话工作台：轮次时间线看结果
   ↓ 需要输入
④ 输入系统：语音/模板/文本
```

一切页面与组件设计都必须服务这条主线；不服务主线的信息在小屏上让位。

### 1.3 体验承诺（验收锚点）

| 承诺 | 度量 |
|---|---|
| 打开即知 | 冷启动到"知道有没有事需处理" ≤ 3s（含缓存态首屏） |
| 两步处理 | 从任一入口发现待办 → 完成处理 ≤ 2 次点击 / ≤ 5s |
| 一屏判断 | 6 寸屏任务卡片不滚动即可读出健康状态 + 当前动作 + 活跃时长 |
| 拇指输入 | 发送按钮、模板 chips 始终位于拇指热区；语音一键可达 |
| 断线不迷路 | 断网 10 分钟恢复后 ≤ 3s 补齐状态；离线操作不丢（入队） |
| 折叠不断层 | 折叠↔展开切换不丢路由/状态，布局切换 ≤ 300ms |

---

## 2. 设计原则（在 2026-07-02 设计系统四原则上扩展）

沿用：快速访问、极简界面、智能反馈、离线优先。新增四条本方案特有原则：

1. **监控优先（Monitor-first）**：默认态是"读"，不是"操作"。第一屏回答"要不要我管"，其余信息分层披露；没事时用户根本不需要打开 App（靠通知）。
2. **信号即界面（Status-as-UI）**：每个任务/会话收敛为一个健康信号（色点 + 一句话 + 时长）。字段服务信号，不服务信号的字段在 compact 断点删除。
3. **两步介入（Two-tap Action）**：审批、回答、停止等高频处理动作必须出现在发现问题的同一层（卡片内联或通知直达），不允许"先导航再处理"。
4. **形态即意图（Form-follows-fold）**：折叠外屏 = 只读监控；展开内屏 = 双区工作台。展开不是"更宽的同一页面"，而是换一种工作模式。

---

## 3. 信息架构 v2

### 3.1 一级导航（保留现有 5 Tab 结构，遵循设计系统 §2.2）

```
🤖 AI指挥中心(/ai)  📝 笔记(/notes)  📧 邮箱(/email)  🔐 密码箱(/vault)  🎙️ 会议(/meetings)
```

`/ai` 从"任务聚合看板"升级为**指挥中心（Command Center）**——监控与介入的唯一主入口。这是本次改造的核心页面；其余 Tab 不动。

### 3.2 二级路由（现有代码为准 + 新增）

```
/ai                        指挥中心（改造：分诊条 + 健康信号 + 快捷处理）
/sessions                  会话列表（保留）
/sessions/:id              会话工作台（改造：轮次时间线 + 状态条 + 输入区）★ 合并入口
/opencode/sessions/:id     旧会话详情页（deprecated → 301 至 /sessions/:id，统计/导出并入工作台）
/tasks /tasks/:id          任务管理（保留，弱化为后台实体管理入口）
/instances /servers        实例/服务器（保留，收纳进指挥中心"更多"）
/settings                  设置（保留；新增：通知分级开关、折叠屏实验开关）
```

> 与 PocketFleet v3 的关系：v3 的 `/fleet/*` 路由族（Live、Build Live View、approvals）**不另起炉灶**。`/ai` 演进为 `Live`，`/sessions/:id` 演进为 `Build Live View`，ZAG 上线后在同一路由下切换数据源（pocketd → fleetbridge）。IA 一致，迁移成本为零。

### 3.3 指挥中心信息层级（自上而下，严格递进）

```
L0 分诊条（sticky）   "🔴 2 项需要你 · 1 等审批 5min · 1 疑似卡死"   ← 唯一必读层
L1 需介入列表        按等待时长排序，卡片内联操作按钮                  ← 有事才看
L2 运行中            健康卡片横滑（现有 .task-scroll 保留，卡片升级）   ← 瞄一眼
L3 会话              最近会话纵列（现有 Session List 保留）             ← 可折叠
L4 已完成            默认收起（现有行为保留）                          ← 不打扰
语音条               固定于底部导航之上（现有 voice-bar 保留并升级）
```

---

## 4. 核心体验设计

### 4.1 健康度模型（全方案的地基）

统一 `SessionHealth` 五态，前端新模块 `frontend/src/features/tasks/health.ts`：

| 态 | 判定（数据源） | 展示 |
|---|---|---|
| `needs-input` 🔴 | 存在 pending permission/question（WS `approval.*` 事件，已有） | "等审批 · 5 分钟" + 内联按钮 |
| `stalled` 🟠 | `now - lastEventAt > 阈值`（`session.activity` 事件；上线前用 `updatedAt` 近似） | "⚠ 10 分钟无响应" |
| `error` 🟡 | 最近一轮含 error 事件 | "❌ 上一轮失败" + 重试/查看 |
| `running` 🟢 | 有活跃事件流 | "改文件中 · 40s" |
| `idle/done` ⚪ | 无活跃 | 弱化展示 |

优先级：needs-input > stalled > error > running > idle。分诊条 = 对 L1 列表的聚合计数。

### 4.2 指挥中心（/ai）改造清单

保留：任务横滑卡、会话列、已完成折叠、语音条、长按菜单框架、下拉刷新。
改造：

1. **新增 L0 分诊条**（P0）：聚合计数 + 点击展开 L1；无事时显示 "🟢 全部正常 · N 在跑"，绿色弱化。
2. **任务卡片第二行换血**（P0）：`实例名+会话数+时间` → `健康信号 · 当前动作 · 距上次活动`；compact 断点删会话数。
3. **审批/提问卡片内联操作**（P0）：`批准/拒绝/详情` 或候选答案 chips，直接复用 `usePendingApprovals.reply()`（含离线 outbox 与 409 冲突处理）。
4. **长按菜单补控制**（P0）：`停止/继续/归档`，接后端 `plugin_hub.go` 已有的 `session.stop` 命令通道。
5. **本地通知 + Deep Link**（P0）：集成 `@capacitor/local-notifications`；`needs-input` 超阈值（默认 3min）未处理触发；点通知直达 `/sessions/:id?approval=open` 自动弹审批 Sheet。通知分级沿用 mobile-shell §3.3：silent/info/critical。

### 4.3 会话工作台（/sessions/:id）改造清单

保留：ApprovalPanel Bottom Sheet（含服务端确认语义）、输入区、离线审批入队。
改造：

1. **顶部状态条**（P1）：一行式 `🔴 等待审批 · 5 分钟 [查看]` / `🟢 改文件中 · 40s [停止]`，替代静态信息卡；实时驱动。
2. **轮次（Round）时间线**（P1）：事件流按轮折叠——每轮 = 用户 prompt → agent 动作序列 → 结果摘要；默认只展开最新一轮，自动滚动到底；过程事件收进"查看过程（N 个事件）"；工具输出截断 2 行 + 展开 + 复制。轮摘要由后端 `round.completed` 事件下发（§6.1），前端不再自行拼接。
3. **旧详情页收敛**（P1）：`features/opencode/SessionDetailView.vue` 的统计（+/-行数、文件数、消息数）与导出能力并入工作台头部"详情"抽屉；路由 301；两套 Session 详情并存的历史问题就此关闭。
4. **Diff 展示纪律**（P1，承接 P2 性能基线）：小屏只显示统计行，文件列表与 diff 内容在二级页按需加载（P2 报告已建立基线，不得回退为内联全量渲染）。

### 4.4 输入系统（全形态统一）

1. **目标 chip**（P1）：输入框左侧固定目标会话（默认最近活跃会话，可切换），杜绝发错地方。
2. **指令模板**（P1）：输入框上方一行可配置 chips——`继续 / 停下 / 总结当前进展 / 跑测试 / 忽略错误继续`；一点即发，"停下"二次确认。
3. **语音优先**（P0 增强）：现有 voice-bar + STT 保留；转写结果先入草稿可编辑再发送（避免误发）；保留全局录音快捷入口。
4. **键盘与热区**（P1）：KeyboardResize 行为下发送按钮贴拇指区；safe-area 适配（刘海屏既有方案沿用）。
5. **草稿持久化**（P1）：每会话草稿存 SQLite（P1 离线库已就绪），切走/杀进程不丢。
6. **输入离线入队**（P2）：prompt 复用审批 outbox 模式，离线显示 "⏳ 待发送"，恢复后重放（后端需幂等键，§6.1）。

### 4.5 折叠屏与多形态（vivo X Fold5 为基准机型）

断点体系沿用 `useBreakpoint.ts`（compact<560 / medium<840 / expanded<1280 / wide），在其上新增**形态层**：

| 形态 | 布局 | 允许的操作 |
|---|---|---|
| 折叠外屏（~6"，单手） | 纯监控：分诊条 + 健康列表 | 只读 + 点通知跳转；操作按钮替换为"展开处理" |
| 展开内屏（~8"） | 双区工作台：左 = 列表/监控，右 = 工作台/输入（复用 `SplitLayout.vue`） | 全操作 |
| 直板手机 | 现行单列布局（不变） | 全操作 |
| 平板/wide | 双栏（现有 isWide 行为沿用） | 全操作 |

实现要点：

- 铰链避让：`@media (horizontal-viewport-segments: 2)` + `env(viewport-segment-*)`；WebView 支持不完整时降级为"宽度阈值双栏 + 中缝留白"。真机验证清单用 `VIVO_USB_DEBUG_GUIDE.md`。
- 状态保持：折叠↔展开只切换布局组件，路由与 Pinia 状态不动；禁止重挂载导致回到首页。
- 单手纪律：外屏态下所有可点元素位于下 2/3； destructive 操作必须二次确认。

---

## 5. 后台通讯设计（pocketd WS 层演进）

现状（保留并统一）：`backend/internal/websocket/{hub.go, mobile_hub.go, plugin_hub.go}`；前端 `idempotentWsBus`（幂等）+ `api/websocket.ts`；审批事件已有 `approval.permission/question.pending/resolved`；断线轮询兜底与重连补拉已在 `usePendingApprovals` 实现。**`frontend/src/services/websocket-hub.ts` 中过时的 WSMessageType 枚举删除，统一走 idempotentWsBus。**

### 5.1 新增事件（后端 Go，全部带 `event_id` 幂等键）

| 事件 | Payload 要点 | 节流 |
|---|---|---|
| `session.activity` | sessionID, phase(thinking/tool/file_write/pty), lastEventAt, roundIndex | ≥30s 或阶段切换 |
| `round.completed` | sessionID, roundIndex, 一句话结论, 变更统计(+/-/files), 结果状态 | 每轮一条 |
| `task.health` | taskID, 聚合健康度, 待办计数 | 5s 合并 |
| `prompt.queued/rejected` | 幂等键回执（离线重放场景） | 即时 |

`task_updated` 退回"任务属性变更"职责，不再承载心跳。发送侧 500ms coalescing。

### 5.2 订阅与追赶

1. 客户端订阅消息 `{"subscribe":{"sessions":[...]}}`，服务端按订阅路由（省流量省电；审批事件维持现有全推）。
2. 重连后带 `since` 游标，服务端回**快照摘要**（各活跃会话 health + 最新轮次结论）而非事件回放；幂等由 idempotentWsBus 兜底。
3. SSE（`api/sse.ts`）与 WS 双通道并存期间，事件 schema 共用一份 TS/Go 类型定义（contract test 锁定，参照 ZAG 契约测试模式）。

### 5.3 通知链路

- 前台/近期后台：WS + 本地通知（4.2-5）。
- 长期后台/被杀：FCM/APNs 推 `needs-input`（后端 `notifycenter` 模块为起点 + `@capacitor/push-notifications`）；Deep Link 同一目标路由。此为 P3，依赖运维配置。

---

## 6. 分期路线（对齐 `docs/新架构v1/03-roadmap/里程碑.md`，不与其冲突）

| 期 | 内容 | 依赖 | 验收 |
|---|---|---|---|
| **P0 纯前端** | 分诊条 + 卡片健康信号（近似数据）+ 审批内联操作 + 长按控制 + 本地通知/Deep Link + voice-bar 草稿化 | 无后端改动 | §1.3 前四项承诺 |
| **P1 前端为主** | 会话工作台轮次化 + 状态条 + 旧详情页收敛 + 输入系统（目标 chip/模板/草稿/键盘热区）+ 后端 `session.activity`/`round.completed` 事件 | 后端小改 | 一屏判断 + 输入承诺 |
| **P2 双端** | 折叠屏形态层（双区/铰链/外屏只读）+ 订阅收窄 + since 追赶 + 输入离线入队 + 删过时 websocket-hub 枚举 | 前后端 | 折叠不断层 + 断线不迷路 |
| **P3 运维依赖** | FCM/APNs 推送链路 | 证书/运维 | 后台免开 App |

每期完成必须：更新 `STATUS-MATRIX.md` 对应行 + `EVIDENCE-LEDGER.md` 证据 + 通过 `REVIEW-PROCESS.md` 门禁。

---

## 7. Evidence（证据等级：`source-inspected`，本文为设计文档，无能力声称）

| 引用 | 位置 |
|---|---|
| 现有路由 | `frontend/src/app/router-mobile.ts` |
| 指挥中心现状 | `frontend/src/features/tasks/TasksView.vue` |
| 会话工作台/审批现状 | `frontend/src/features/sessions/SessionConversationView.vue`、`ApprovalPanel.vue`、`frontend/src/composables/usePendingApprovals.ts` |
| 幂等事件总线 | `frontend/src/services/idempotentWsBus.ts`、`frontend/src/services/approvalEvents.ts` |
| 断点体系 | `frontend/src/composables/useBreakpoint.ts` |
| 离线持久化/队列（已交付能力） | `P1_MOBILE_PERSISTENCE_2026_08_15.md`、`frontend/src/native/{schema,outboxStore,approvalStore}.ts` |
| WS 后端现状 | `backend/internal/websocket/{hub.go,mobile_hub.go,plugin_hub.go}` |
| 目标架构 | `docs/新架构v1/02-modules/mobile-shell.md`、`docs/新架构v1/03-roadmap/里程碑.md` |

## 8. 本方案取代的文档

见 `docs/governance/SUPERSEDED.md` Group 6（移动端设计文档）与 Group 5（历史冲刺报告）。被取代文档一律加 `STATUS: superseded` 横幅，不删除。
