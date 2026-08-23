# 模块设计：Mobile Shell（移动端壳）

---

## 1. 形态策略（分三阶段）

| 阶段 | 形态 | 时间 | 目标 |
|---|---|---|---|
| **v1.0** | PWA（Vue3 + Vite + vite-plugin-pwa） | M1 | 零审核上线，与 todos.dev 同步体验 |
| **v1.1** | Capacitor 包装（iOS / Android） | M3 | 原生 push / 后台保活 / 系统通知 |
| **v1.2** | 可选 SwiftUI / Kotlin 原生壳 | M5 | 极致体验（Live Activity / Dynamic Island） |

## 2. v1.0：PWA 优先

### 2.1 技术栈

- Vue 3 + TypeScript + Vite（沿用既有 `frontend/`）。
- vite-plugin-pwa（Workbox）—— 离线 shell + 后台同步。
- Pinia（状态，沿用既有）。
- Vue Router 4（沿用既有）。
- Vue-i18n（既有）+ 新增 zh-Hans / en-US。
- Tailwind-like CSS（既有样式体系）。

### 2.2 路由

```
/                       → Dashboard / Live（默认页）
/fleet/tasks            → PocketTask 列表
/fleet/tasks/:id        → Task 详情 + 子 Build
/fleet/builds/:id       → Build Live View（实时日志 + 权限请求）
/fleet/pods             → Pod 列表
/fleet/pods/:id         → Pod 详情（capabilities / workspaces）
/fleet/agents           → Agent 团队成员
/fleet/agents/:id       → Agent 编辑（model / skills / permissions）
/fleet/charter          → Charter 编辑（Markdown）
/fleet/skills           → Skill 库
/fleet/cost             → 用量与花费
/fleet/schedules        → 周期任务
/fleet/settings         → 设置（push / 语言 / secrets）
/fleet/messages/:id     → Permission request 详情（deep link）
/fleet/approvals        → 待审批清单
```

### 2.3 关键页面草图

#### 2.3.1 Live（默认页）

```
┌──────────────────────────────────────────────────┐
│  PocketFleet                       🔔  ⚙         │
├──────────────────────────────────────────────────┤
│  🟢 3 builds running   🔵 1 awaiting review      │
│  💰 今日消耗 ¥4.32                              │
├──────────────────────────────────────────────────┤
│ ┌──────────────────────────────────────────────┐ │
│ │ 🟢 Build b_123 · add-oauth                  │ │
│ │ Agent: backend-engineer  Pod: mbp16-居家     │ │
│ │ ▰▰▰▰▰▰▰▱▱▱  73% · Running tests             │ │
│ │ 2 min ago · 4 events                        │ │
│ └──────────────────────────────────────────────┘ │
│ ┌──────────────────────────────────────────────┐ │
│ │ 🟡 Build b_124 · oauth-frontend             │ │
│ │ Awaiting permission: "git push?"            │ │
│ │ [ Review ]                                  │ │
│ └──────────────────────────────────────────────┘ │
│ ┌──────────────────────────────────────────────┐ │
│ │ 🔵 Build b_125 · docs-update                │ │
│ │ Awaiting review                             │ │
│ │ [ View PR ]                                │ │
│ └──────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────┘
```

#### 2.3.2 Build Live View

```
┌──────────────────────────────────────────────────┐
│  ← Build b_123 · add-oauth        ⋮              │
├──────────────────────────────────────────────────┤
│  Status: 🟢 running                             │
│  Agent: backend-engineer (DeepSeek-V4-Flash)    │
│  Pod: mbp16-居家                                 │
│  Started: 2 min ago                             │
├──────────────────────────────────────────────────┤
│  Timeline                                        │
│   ✓ Read oauth.py (1.2s)                        │
│   ✓ Edited test_oauth.py (3.4s)                 │
│   ✓ Running pytest (8.1s)                       │
│   ⟳ Running ruff (in progress)                  │
├──────────────────────────────────────────────────┤
│  💬 Agent says:                                 │
│  "测试通过，正在生成 PR..."                       │
├──────────────────────────────────────────────────┤
│  📂 Files changed: 3                             │
│  + 124 / - 12 lines                              │
│  [ View Diff ]                                  │
├──────────────────────────────────────────────────┤
│  [ Pause ]  [ Send Follow-up ]  [ Cancel ]       │
└──────────────────────────────────────────────────┘
```

#### 2.3.3 Permission Modal（覆盖页）

```
┌──────────────────────────────────────────────────┐
│        🛡️  Permission Request                   │
├──────────────────────────────────────────────────┤
│  Agent: backend-engineer                        │
│  Build: b_123                                   │
│                                                  │
│  Tool:   shell_run                              │
│  Risk:   medium                                 │
│                                                  │
│  Command:                                       │
│  ┌────────────────────────────────────────────┐ │
│  │ git push origin feature/oauth-login        │ │
│  └────────────────────────────────────────────┘ │
│                                                  │
│  Diff summary: + 124 / - 12 lines in 3 files    │
│                                                  │
│   [ Deny ]   [ Allow Once ]   [ Always Allow ]  │
└──────────────────────────────────────────────────┘
```

#### 2.3.4 Charter 编辑

```
┌──────────────────────────────────────────────────┐
│  ← Charter                              [ Save ] │
├──────────────────────────────────────────────────┤
│  ┌────────────────────────────────────────────┐ │
│  │ # My PocketFleet Charter                   │ │
│  │                                            │ │
│  │ ## Priorities                              │ │
│  │ - Test first, code second                  │ │
│  │ - Use existing libraries when possible     │ │
│  │ - ...                                      │ │
│  │                                            │ │
│  │ ## Style                                   │ │
│  │ - TypeScript strict, no `any`              │ │
│  │ - Tests required for every PR              │ │
│  │ - ...                                      │ │
│  │                                            │ │
│  └────────────────────────────────────────────┘ │
│  1247 chars · Last updated 2 days ago by me     │
└──────────────────────────────────────────────────┘
```

#### 2.3.5 Pod 列表

```
┌──────────────────────────────────────────────────┐
│  Pods                              [ + Add Pod ] │
├──────────────────────────────────────────────────┤
│  🟢 mbp16-居家                                   │
│     Apple M3 Max · 64 GB · macOS 15              │
│     2 builds running · region: private           │
│     [ Details ]                                 │
│                                                  │
│  🟢 workstation-office                          │
│     Intel i9 · 32 GB · Ubuntu 24                │
│     Idle · last seen 1 min ago                   │
│     [ Details ]                                 │
│                                                  │
│  🔴 rk3588-edge                                 │
│     ARM · 8 GB                                  │
│     Offline (last seen 3 hours ago)             │
│     [ Details ]                                 │
│                                                  │
│  💤 platform-pod-cn-hangzhou (Pro)              │
│     1× A100 · 80 GB                             │
│     Asleep · wakes on demand                    │
│     [ Wake ]                                    │
└──────────────────────────────────────────────────┘
```

## 3. v1.1：Capacitor 包装

### 3.1 选型

- `@capacitor/core` + `@capacitor/cli` 6.x
- iOS：APNs 通过 `@capacitor/push-notifications`
- Android：FCM 通过 `@capacitor/push-notifications`
- 后台保活：`@capacitor-community/background-mode`（仅在用户允许时）

### 3.2 关键插件

| 插件 | 用途 |
|---|---|
| `@capacitor/push-notifications` | Live activity / permission push |
| `@capacitor/local-notifications` | 离线通知 |
| `@capacitor/preferences` | 持久化设置 |
| `@capacitor-community/secure-storage` | Token 加密存储 |
| `@capacitor/haptics` | 权限审批触觉反馈 |
| `@capacitor/camera` | Design Mode 拍照 |
| `@capacitor/media` | 录音 → 转文字 |
| `@capacitor/share` | 分享 PR / Diff |
| `@capacitor/deep-links` | 点击通知 → 直达 Build |

### 3.3 通知分级

| 级别 | 触发 | 推送策略 |
|---|---|---|
| `silent` | build 状态变化（非阻塞） | 不推送，仅 WS |
| `info` | 完成 / 失败 | Local Notification |
| `critical` | permission request / 错误 | Push Notification + 声音 + 震动 |

### 3.4 离线行为

- App 进入后台 → WS 断开（系统限制）；
- Backend 在断连期间继续执行任务；
- 重连后按 `Last-Event-ID` 增量同步；
- 关键事件（permission request）即使离线也推送本地通知（"X 个待审批"）。

## 4. v1.2：原生壳（可选）

仅在以下情况做：

- 用户量大且 iOS / Android 体验差距明显；
- 需要 Live Activity / Dynamic Island 深度集成；
- 需要 iOS Share Extension / Android Intent Filter。

SwiftUI / Kotlin 实现保留与 PWA 一致的 API 调用层（service module），UI 层重写。

## 5. 设计系统（沿用既有 + 扩展）

- 颜色：深色 + 浅色模式（既有）。
- 字体：iOS SF Pro / Android Roboto / Web Inter（既有）。
- 图标：Lucide / Phosphor（既有）。
- 新增 `PocketFleet` 主题色：执行中 = 绿、待审批 = 黄、完成 = 蓝、失败 = 红、离线 = 灰。

## 6. i18n

新增 i18n key：

```yaml
zh-Hans:
  fleet.title: "PocketFleet"
  fleet.live.running: "运行中"
  fleet.live.permission: "等待审批"
  fleet.permission.title: "权限请求"
  fleet.permission.deny: "拒绝"
  fleet.permission.allow_once: "允许本次"
  fleet.permission.allow_always: "始终允许"
  ...
en-US:
  fleet.title: "PocketFleet"
  ...
```

## 7. 性能指标

- 首屏 LCP ≤ 1.5s（4G）；
- WS 重连 ≤ 3s；
- 列表滚动 60fps；
- 包体 ≤ 3MB（gzipped）；
- 离线 shell 启动 ≤ 200ms。

## 8. 可访问性（A11y）

- 全部交互组件 keyboard / screen reader 友好；
- 颜色对比 ≥ WCAG AA；
- 权限审批提供 "Always allow" + "Always deny" 列表页（撤销历史决策）。

## 9. 测试

- 单元：Vue 组件 snapshot + interaction；
- E2E：Playwright（既有）+ Capacitor simulator（iOS / Android）；
- 真机：内部 dogfood（≥ 3 台设备）+ 1% 灰度。

## 10. 与既有页面共存

既有 `instances/`、`sessions/`、`tasks/` 等页面保留不变；PocketFleet 在导航中作为**新的一级入口** `Fleet`，与 `Sessions`、`Email`、`Notes` 等平行。用户不会失去任何既有能力。
