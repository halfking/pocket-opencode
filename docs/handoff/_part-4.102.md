
## §4.102 三条外部审计缺口的定性：两条证伪、一条转化为新门禁

外部审计回了三条缺口。逐条查证，结论是**两条不成立、一条成立但被我错误归类**。
成立的那条又牵出一个此前没记录的前置条件。

### §4.102.1 「闪卡入口『新建卡组』跳到卡片编辑页」—— 不复现

审计说这是「本轮新发现、只记录未修」。逐层查下来它在当前代码里不存在：

| 层 | 证据 | 结论 |
|---|---|---|
| i18n 值 | `list.create`="新建卡片"、`deck.create`="新建卡组"（zh-CN）；en-US 为 "New card"/"New deck" | 未对调 |
| 9 种语言 | de/en/es/fr/ja/ko/pt/zh-CN/zh-TW 的 `list.create` 与 `deck.create` **全部互不相同** | 一致 |
| 模板绑定 | `.add` → `goCreate()` → `/flashcards/new`（卡片编辑页）；`deck-create-toggle` → `showDeckForm = !showDeckForm`（**页内展开**，不导航） | 语义正确 |
| 后端路由 | `server.go` 里无 `/api/marketplace/agents` 之类字面量 | — |

历史：**§4.14 BUG-K** 就是这个缺陷（「按钮写新建卡组、实际跳到新建卡片页」），
修法是把 `list.create` 从「新建卡组」改成「新建卡片」并新增 `deck.create`；
**§4.28 BUG-AA** 补齐了 BUG-K 只改对 2/9 语言的问题。两轮都已闭环。

**没做过的是真机侧**。新增 `scripts/probe-flashcards-entries-readonly.mjs`，
**严格只读**（只 openCdp + 读 DOM，不导航、不点击、不碰 `adb reverse`），
实跑结果：

```
CDP 已连：pid=21096 socket=webview_devtools_remote_21096 port=52330
当前页面：origin=https://localhost hash=#/more
当前不在闪卡页。**不跳转** —— 跳转是状态变更……
本次结论：闪卡两入口的真机渲染「未验证」（不是「通过」，也不是「不通过」）。
exit=2
```

`exit=2` 是刻意设计的：它把「没验」和「验过不通过」区分开，
免得下一轮在汇总表里把这一项当成通过。跳转属于共享设备上的状态变更，
而设备的 `adb reverse` 指向并发会话的后端，动手前必须先确认对方没在跑。

跑完确认共享状态未动：`reverse` 仍是 `tcp:18099 → tcp:18099`，
`forward --list` 为空（`close()` 清理干净），App 仍停在同一个 Activity。

### §4.102.2 顺带查出一个没记录的前置条件：设备跑的是**生产 https 包**

上面那次附着读到 `origin=https://localhost` —— 设备上装的是**生产包**，
而 `verify-finance-writepath.mjs` / `verify-instances-readpath.mjs` 的
`POCKET_EXPECT_ORIGIN` 默认值是 `http://localhost`（开发包）。

⇒ **用默认值跑这两个脚本，会在第一关 `origin 与预期不符` 就 exit 5，
根本走不到真正要验的判据上。** 跑它们必须带
`POCKET_EXPECT_ORIGIN=https://localhost`（脚本本来就为「生产 https 回归」留了这个口子）。

全仓只有这 2 处涉及该默认值，所以不是普遍问题，但它是**实跑前置条件**，
之前从没记进 handoff。

### §4.102.3 「/api/marketplace/agents 到底是 404 还是 401」—— 两个都不是缺陷

审计说「本次只读探测下无法证实（返回 401）」。实测三种请求（隔离后端 18101）：

```
① 不带凭证      -> 401  {"code":"unauthenticated","error":"missing authorization token"}
② 带错 token    -> 401  {"code":"unauthenticated","error":"invalid or expired token"}
③ 带有效 token  -> 404  {"error":"not found"}
```

源码对照：`server.go` 里**没有** `/api/marketplace/agents` 这个字面量。

所以两轮旧结论各自错在哪：

- 「404 = 路由没注册」——**这句是对的**，但当时是**没带 token** 探测的，
  被 `requireAuth` 先挡成 401，压根没走到路由判定。
- 「市场接口 401」——同样是没带凭证的只读探测，401 只是鉴权中间件在工作。

**真正的结论**：`frontend/src/features/marketplace/api.ts` 的
`base = '/api/marketplace'` 下只列了 `packages` / `releases` /
`packages/{id}/versions` / `submit` / `review` / `publish` / `install` / `revoke` / `rate`
—— **没有任何地方调 `/api/marketplace/agents`**。它是一个**契约里不存在的 URL**。
「智能体市场」页（`AgentMarketView`）走的是 `/api/marketplace/packages?kind=agent`，实测 200。

⇒ 这不是缺陷，是个**反复被人当成缺陷讨论的幻影 URL**。为此新增门禁
`scripts/check-marketplace-contract.mjs`：从 `api.ts` 抽出真实路径，
逐个拿**有效 token** 打一遍，必须 2xx；写路径（submit/publish/install/revoke/review/rate）
只 SKIP 不探。实跑 5/5 PASS：

```
200  /api/marketplace/packages          {"packages":[...]}
200  /api/marketplace/releases          {"releases":[]}
200  /api/marketplace/packages/X/versions {"versions":[]}
404  /api/marketplace/agents（有效 token）
401  /api/marketplace/agents（无凭证）
```

### §4.102.4 「真机 Maestro 从未成功执行一次（零安装包、零运行产物）」—— 不成立

审计这条说「零安装包、零运行产物」。`~/.maestro/tests/` 下实际有 **155 个运行目录**：

| flow | 运行次数 |
|---|---|
| flashcards-write | 26 |
| notes-crud | 22 |
| tasks-crud | 18 |
| smoke-login | 9 |
| login-gesture | 2 |

每次运行的产物结构完整，例如 `2026-10-02_223553/flashcards-write/`：
`commands.json` (21.9KB)、`manifest.json`、`logs/maestro.log` (24.4KB)、
`logs/device-logcat.txt` (**980KB**)。`2026-10-02_231342/login-gesture/` 另有
`takeScreenshot/logs/maestro/login-gesture-rejected.png` (197KB，本轮之前已目视核对)。

设备侧 logcat 是**真机上的 Maestro 进程**留下的，不是模拟：

```
10-02 22:36:15.711 D/Maestro ( 9060): Requesting view hierarchy
10-02 22:36:15.732 I/Maestro ( 9060): Skipping invisible child: … boundsInScreen: Rect(38, 10 - 38, 68) …
```

一点如实说明：Maestro 的 `manifest.json` 里把设备来源标成 `"source": "emulator"`，
但设备是 `192.168.31.19:5555` 这台真机（前面那张截图里能看到真机状态栏与电量）。
**那是 Maestro 自己的固定标签，不是设备类型。**

### §4.102.5 这轮新门禁又把自己的作者判红了一次

`check-marketplace-contract.mjs` 第一版把
`${base}/packages${query}` 里的 `${query}` 当成路径参数替换成了 `X`，
于是拼出 `/api/marketplace/packagesX` —— 一个**根本不存在的 URL**，
然后门禁红灯，输出「有接口不可达 —— 这才是真缺陷」。

真凶是抽取器，不是后端。修法：`${…}` 出现在**捕获串末尾**时它是查询串占位符
（listPackages 专门拼 `?kind=`），不是路径段，去掉即可。

**这条值得单独记**：门禁把作者的错报成后端的错，而那行结论写得很有把握
（「这才是真缺陷」）。判据出错时，它输出结论的**语气**不会变——
这跟 §4.101 的迁移脚本把文件改坏而 `node --check` 放行是同一类：
**判据的错误会以结论的口气出现，而不会以「我不确定」出现。**
