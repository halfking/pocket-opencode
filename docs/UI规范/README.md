# OpenCode Pocket UI 规范

> 专题目录：`docs/UI规范/`
> 版本：1.0（2026-10-04）
> 来源：学习 `~/workspace/yatao/nbjl3/docs/UI规范`（v2.1，16 篇）后，按 openpocket
> 的**真实代码状态**改写，不是照抄。
> 工程门禁：`frontend/gates.json` + `npm run gates`

本专题是 openpocket 移动端的 UI/交互规范。**它与参考仓最大的不同**：参考仓的壳只打包
一个引导页、业务前端从远端 origin 加载；openpocket 把构建产物打进 `webDir: 'dist'`，
API 地址由构建期 `VITE_API_BASE` 注入。因此参考仓「壳内不打包业务前端」「壳层零凭据」
两条红线**对 openpocket 不适用**，本专题不继承它们；本专题继承的是它的**交互协议**
与**双镜像防漂移的工程纪律**。

## 读哪一篇

| 文档 | 解决什么 | 状态 |
| --- | --- | --- |
| [01-原则与断点.md](./01-原则与断点.md) | 产品形态、四档窗口、CSS/JS 双镜像与防漂移门禁 | 现行约束 |
| [02-布局导航与账户.md](./02-布局导航与账户.md) | 侧栏/底栏/顶栏/账户入口、返回来源收敛 | 现行约束 + 已接线 |
| [03-列表卡片与弹窗.md](./03-列表卡片与弹窗.md) | 列表双形态、连续加载、弹窗 PC/手机分形 | 现行约束 + 部分已实现 |
| [04-Hybrid壳与Android构建.md](./04-Hybrid壳与Android构建.md) | Capacitor 壳、8 个原生插件、Android 构建链 | 现行约束 |
| [05-落地清单与禁止事项.md](./05-落地清单与禁止事项.md) | 新页面检查表、组件落点、明确禁止 | 现行约束 |
| [06-Hyper导航上下文.md](./06-Hyper导航上下文.md) | 标题真源、导航上下文、返回仲裁、前进 | **已实现**（`lib/shell`） |
| [07-Hyper滚动与专注.md](./07-Hyper滚动与专注.md) | 下拉刷新、连续加载、吸顶坐标、专注工作区 | **已实现**（`lib/shell`） |
| [08-Hyper框架与能力协商.md](./08-Hyper框架与能力协商.md) | 分层、能力协商 vs 形态开关、原生与 AI 的边界 | 部分实现 |
| [09-审计与实施路线.md](./09-审计与实施路线.md) | 代码级差距、需求追踪、分期 | 现状台账 |
| [10-门禁与真机验收.md](./10-门禁与真机验收.md) | 自动门禁清单、变异自测、真机验收矩阵 | 现行门禁 |

## 一句话产品形态

**一套 Vue SPA + 一个 Capacitor Android 壳，两种入口：**

1. 桌面 / 宽屏浏览器（wide ≥1280px：专业顶栏、表格、侧栏）
2. 手机 / 窄屏浏览器与 PWA（compact：卡片、底栏、全屏层）

移动端在此之上叠加 **Hyper 运行时**（`frontend/src/lib/shell/`）：导航上下文、
连续加载、吸顶、专注工作区、能力协商。**不写第二套移动页面副本**——本仓只有一个
router（`src/app/router-mobile.ts`，历史命名），全部视图走 `features/*` 共享组件。

## 状态标注纪律（本专题最重要的一条）

每条能力都必须标注**下列之一**，不得含糊：

| 标注 | 含义 |
| --- | --- |
| **已实现** | 有代码落点 **且** 有单测，且判据经过变异自测确认能红 |
| **已接线，未验证** | 有代码落点与单测，但**没有**真机/端到端证据 |
| **spec-only** | 只有本文档描述，**代码里没有**。不得当作现有能力对外承诺 |

本轮（1.0）交付：`lib/shell` 运行时内核 + 断点漂移门禁 + 本专题 10 篇。

判据：**114 条**单测，全在门禁内（逐文件实测数）：

| 文件 | 条数 | 文件 | 条数 |
| --- | --- | --- | --- |
| `shell/navigationContext` | 17 | `shell/dockCoordinator` | 10 |
| `shell/continuousList` | 27 | `shell/focusWorkspace` | 10 |
| `shell/backDispatcher` | 12 | `shell/runtime` | 11 |
| `shell/titleResolver` | 11 | `shell/capabilities-detect` | 7 |
| `styles/breakpoint-mirror` | 9 | `styles/z-index-ladder` | 7 |
| `styles/bottom-chrome-gate` | 9 | **本轮单测合计** | **130** |
另有 **响应式视口矩阵** 16 条（`e2e/web/specs/responsive-shell.spec.ts`，需 dev server）
与 **登录后外壳** 3 条（`e2e/web/specs/authenticated-shell.spec.ts`，需真后端）；
两者都登记进 `gates.json` 的 `notGates`，用 `./e2e/web/e2e-auth-stack.sh` 一键起栈。

接线状态（**已实现 ≠ 已接线**，这里逐项说清）：

| 能力 | 运行时 | 接线 |
| --- | --- | --- |
| 标题真源 | `titleResolver.ts` | ✅ `AppLayout` 顶栏 |
| 返回仲裁 4 来源 | `backDispatcher.ts` | ✅ 返回钮 / Android backButton / Esc / 左滑 |
| 账号域隔离 | `runtime.setScope()` | ✅ watch `auth.userId/workspaceId` |
| 连续加载 | `continuousList.ts` + `useContinuousList` | ✅ `MeetingListView`、`NoteListView`、`EmailInboxView`（3/3 页；2026-10-06 逐个复核三处均有 `useContinuousList` 调用） |
| 吸顶坐标 | `dockCoordinator.ts` | ❌ 未接 —— **本仓无表格**，无对象（见 07 §3.1） |
| 专注工作区 | `focusWorkspace.ts` | ❌ 未接 —— 同上，缺可接的表格区域 |
| 能力协商 | `lib/shell/capabilities.ts` | ⚠️ 已实现 + 7 条单测 + 真探测 `Capacitor.isPluginAvailable()`，但**无 UI 消费者 ⇒ 不进 bundle**。另存在既有的 `src/native/capabilities.ts`（管 biometric/keystore/push，**同样无消费者**）；两者边界见 [08](./08-Hyper框架与能力协商.md) §3.2 |

未接线的三项**不得**当作现有页面能力对外承诺。

## 与相邻文档的分工

| 文档 | 角色 |
| --- | --- |
| 本专题 | **UI/交互规范**（设计与前端共同遵守） |
| `frontend/gates.json` | 门禁权威名单（改这里，不要改 package.json 的 gates 行） |
| `docs/design/2026-09-19-native-smoothness-audit.md` | 原生顺滑度上游审计 |
| `docs/harmonyos-build-and-test.md` | 另一条壳的构建说明 |

实现改断点、导航席位、列表模式或壳契约时：**同轮修改本专题对应篇 + 门禁 + 代码与验收证据。**
文档提交不代表功能已实现。
