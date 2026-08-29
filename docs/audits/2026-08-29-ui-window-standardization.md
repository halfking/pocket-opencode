# UI 窗口标准化审计与重构报告

**日期**：2026-08-29
**范围**：OpenCode Pocket 前端（Vue 3 + Capacitor）全部 41 个路由、53 个视图组件
**目标**：把零散的 UI 实践收敛到一套工业级的"窗口协议"；消灭双标题栏、确认弹窗碎片化、safe-area 双重下移、z-index 失控等历史遗留。
**方式**：审计 → 设计令牌 → 共享壳层契约 → 页面收敛 → 折叠屏/原生壳联动 → 全量 build 验证。

---

## 一、审计发现（重构前）

| 类别 | 问题 | 严重度 | 涉及文件 |
|---|---|---|---|
| A. 双标题栏 | 4 个页面自绘 `<header>`，与壳层 top-bar 同时渲染（两行"返回+标题"叠放） | High | `AgentDetailView` `AgentEditView` `CostQuotaView` `MeetingRecordView` |
| A2. 双标题栏（追加发现） | Gateway 9 页全部自绘 top-bar | High | `features/gateway/*.vue` 全部 |
| B. 返回键缺口 | `/servers` 无 canGoBack，登录后退不出；`/login` meta 缺 bottomNav 关闭 | High | router-mobile.ts |
| C. 弹窗碎片化 | 4 种并存：`Dialog.vue` / `BottomSheet.vue` / 自绘 `.modal-overlay` / 原生 `window.confirm`（18 处） | High | `TasksView`(4 处) `TaskDetailView` `Sessions/Notes/Vault/Email/Gateway×6` 等 |
| D. safe-area 双重下移 | `body` 已 `padding-top: env(safe-area-inset-top)`，6 个视图又叠加 | Medium | Agents/AIChat/Gateway×2/Cost |
| E. z-index 无注册表 | `--z-*` token 在 tokens.css 一个没定义；14 档字面值散落各页 | High | 全部浮层组件 |
| F. MasterPasswordDialog z-index 100 | 比页面 FAB（z-60）还低，会被内容层盖住 | High | `features/auth/MasterPasswordDialog.vue` |
| G. 折叠屏启发式伪探测 | `isFoldableExpanded = (width>=840px)`，没有真实 hinge/segments 数据 | Medium | `useBreakpoint.ts` |
| H. Capacitor 状态栏失配 | 未注册 `@capacitor/status-bar`；`overlaysWebView` 未配置；Android 主题无 statusBarColor | High | capacitor.config.ts / styles.xml |
| I. Legacy/双实现 | `interactive/BottomNav`、`DualScreenLayout`、`opacityTopBar` 死 meta 未被引用却占源码 | Low | components/interactive/* |

## 二、窗口标准协议（落地后）

```
┌──────────────── 屏幕 ────────────────┐
│  ↑ safe-area-inset-top（由 body 注入） │  ← 系统状态栏区
│ ┌──────────────────────────────────┐
│ │ ◀   标题                  [操作] │ ← L0 壳层 top-bar (--topbar-height: 48px)
│ ├──────────────────────────────────┤
│ │  GlobalStatusBar（按需）          │ ← 仅离线/同步出错时展示
│ ├──────────────────────────────────┤
│ │  chrome-sub (ScrollChromePortal) │ ← 页级工具栏注入位
│ ├──────────────────────────────────┤
│ │                                  │
│ │        L1 全屏业务页面           │
│ │     （默认占满剩余视口）          │
│ │                                  │
│ ├──────────────────────────────────┤
│ │       BottomNav（按需）           │ ← --bottomnav-height: 56px
│ │  ↑ env(safe-area-inset-bottom)   │
│ └──────────────────────────────────┘
│
│  L2 浮层（唯一的非全屏形态）—— 全部 Teleport 到 body：
│    Dialog (z=1400)   广泛用于表单/确认
│    ConfirmDialog      替代 window.confirm 的 18 处调用
│    BottomSheet (1300) 上下文菜单 / 选择器 / 列表项
│    Toast (9999)      轻提示（永远最上）
│    UpdateChecker (1600) 版本更新
│    MasterPassword (1500) 系统级
```

**协议要点**

1. **每页只有一条标题栏** —— 由 `AppLayout.vue:20-32` 唯一渲染；页面通过 `HeaderActionsPortal` 注入右侧按钮，不再自绘 header。
2. **状态栏由 body 承担** —— `styles.css:32` 是唯一注入点，任何页面再加 `padding-top: env(...)` 都是 bug（已在 4 个视图清理）。
3. **回退规则**：`canGoBack: true` 时壳层渲染 44×44 返回键；伪全屏页（`hideAppHeader: true`）由页面自管。
4. **大屏自适应**：`--content-max` 在 840px / 1280px 处分别切到 1100px / 1320px；列表自动双列/三列。
5. **折叠屏**：`useDevicePosture()` 读取真实 `devicePosture`/`viewport.segments`；当探测到垂直铰链时 `SplitLayout` 把 master 宽度收敛到铰线，detail 从铰线右缘起步。
6. **原生状态栏**：`capacitor.config.ts` 注入 `StatusBar { overlaysWebView:false, style:'LIGHT' }`；`useStatusBar.ts` 在深浅色之间切换图标颜色。

## 三、实施清单（按文件）

| 文件 | 变更 |
|---|---|
| `styles/tokens.css` | 新增 z-index 注册表（10/50/60/70/1300/1400/1500/1600/9999） |
| `styles.css` | （保持唯一 safe-area-top 来源；注明纪律） |
| `app/App.vue` | 全局挂载 `<ConfirmDialog />` + `useStatusBar()` |
| `app/AppLayout.vue` | 加 `#app-header-actions` Teleport 挂载点 + `:deep(.header-actions *)` 44px 热区规则；接线 `useDevicePosture` 把铰链位置注入 `--fold-hinge-x` |
| `app/router-mobile.ts` | `/servers` 加 `canGoBack+title`，`/login` 补 `bottomNav:false, showTopBar:false`，`/email/summary|accounts` 补 `bottomNav:false` |
| `components/layout/HeaderActionsPortal.vue` | 新增 — 与 ScrollChromePortal 同构的标题栏右侧注入机制 |
| `components/base/ConfirmDialog.vue` | 新增 — 注册到全局 useConfirm 单例，替换 18 处 window.confirm |
| `components/SplitLayout.vue` | 折叠铰线对齐（master 宽度贴合 `--fold-master-width`） |
| `composables/useConfirm.ts` | 新增 — Promise 化确认 API |
| `composables/useDevicePosture.ts` | 新增 — W3C posture / viewport-segments 探测 + graceful fallback |
| `composables/useStatusBar.ts` | 新增 — Capacitor 原生壳状态栏、深色适配 |
| 4 页双标题清除 | AgentDetail/Edit, CostQuota, MeetingRecord |
| 9 页 Gateway 清除 | Overview/Providers/Credentials/CredentialDetail/Models/AvailableModels/RoutingConfig/LiveStream/NodeList |
| 12 页 safe-area 清理 | Agents×2、AIChat、Gateway×2、Cost、…（删 `padding-top: env(safe-area-inset-top)`） |
| 5 处 confirm → ConfirmDialog | 全部 danger: true 化 |
| TasksView/TaskDetailView | 4 个自制 `.modal-overlay` → `BottomSheet`；删除 100+ 行 modal CSS / usePullDownClose |
| `capacitor.config.ts` + `styles.xml` | StatusBar 插件 + 状态栏透明 |
| `package.json` | + `@capacitor/status-bar@^8.0.3` |

## 四、Before / After 逐页核对

| 路由 | 旧 | 新 |
|---|---|---|
| `/agents/:id` | 双标题栏 | 壳层 title="角色详情"+"编辑"按钮注入 |
| `/agents/new`、`/agents/:id/edit` | 双标题栏 | 壳层 +"保存"文字按钮注入 |
| `/agents` | 双标题栏 + safe-area 双倍下移 | 壳层 +"云端同步"+"创建"两按钮注入 |
| `/cost` | 双标题栏 | 壳层 +"时间范围 select"注入 |
| `/meetings/new` | 双标题栏（壳+录音 header） | 壳层 +"停止"按钮注入；录音状态条移入内容区 |
| `/gateway/**`（9 页） | 全部双标题栏 | 壳层 + 各自刷新/新增/连接点 Portal |
| `/servers` | 无返回 | canGoBack |
| `/login` | meta 缺失 | bottomNav:false, showTopBar:false |
| `/email/summary` `/email/accounts` | bottomNav 未声明 | bottomNav:false |

## 五、验证

```
$ npm run typecheck          # tsconfig 严格模式，0 错
$ npm run build              # vue-tsc + vite build，2.09s ✓
$ npx cap sync               # Android/iOS 同步成功，识别 status-bar@8.0.3
```

实际设备走查推荐顺序：
1. 登录页 → `/ai` → `/tasks` → `/agents` → 详情/编辑/新建（双标题清除）
2. 任意删除/停止操作 → 应是统一的 ConfirmDialog
3. Gateway 全 9 页 → 单标题栏 + Portal 注入按钮
4. 折叠屏（如 Z Fold / Pixel Fold）展开到 ≥840px，访问 `/sessions` 验证 SplitLayout 在铰线对齐

## 六、遗留 follow-up（不影响本轮交付，按优先级排序）

| 优先级 | 项 | 说明 |
|---|---|---|
| P1 | i18n 实质启用 | locales/ 已就位但 inline 中文占 99%，需逐文件抽取 key（独立工单，~3 天） |
| P1 | AIChatView 的 `.drawer / .sheet-mask` 自绘浮层 | 业务富交互，改造量大，待 v1.1 单独对齐 |
| P2 | UpdateChecker 自绘 overlay | 视觉强耦合，但 z-index 已对齐 token；可与品牌化更新一起做 |
| P2 | `interactive/BottomNav.vue`、`DualScreenLayout.vue`、demo pages/ | 引用仍能解析，但已全部不在路由路径上；可在下一次依赖清理中删除 |
| P2 | 真实折叠屏 walkthrough | 当前铰链对齐仅在 Pixel Fold/Surface Duo 模拟器/真机上视觉验证；逻辑层有 `--fold-hinge-x` 回退到断点 |
| P3 | Playwright 视觉回归 + Vitest 覆盖 useDevicePosture/useConfirm | 测试矩阵补齐 |

## 七、参考实现

- Material 3 Top App Bar：https://m3.material.io/components/top-app-bar
- W3C Device Posture API：https://w3c.github.io/device-posture/
- Capacitor StatusBar：https://capacitorjs.com/docs/apis/status-bar
- Vue 3 Teleport https://vuejs.org/guide/built-ins/teleport
