# 交接：批判式审计轮（五）—— 真机审批环闭环：无障碍树失联、MCP 客户端毒化、四处判据缺陷（2026-10-26）

> 本文是 round54 的**续篇**。上一轮（`2026-10-26-round54-acc-approval-loop-closed.md`）
> 修的是「ACC 没有 command 级 permission 端点 ⇒ 审批环断了」，结论是**脚本层**闭环。
> 这一轮做的是：**真机侧的闭环 + 把上一轮没审到的缺陷挖出来**。
>
> **一句话结论**：这一轮**拿到了真机全绿读数**（两条 Maestro 流都过），
> 挖出并修掉 1 个**产品级**缺陷（待批卡片在屏幕上，却不在 Android 无障碍树里 ⇒
> Maestro 点不到、**读屏用户也看不到**）、1 个**健壮性**缺陷（MCP 客户端一次握手失败
> 即永久毒化）、1 个**信息缺失**缺陷（failed / cancelled 的任务在手机上彻底消失）、
> 1 个**死卡片**缺陷、4 处**判据缺陷**（恒真 / 恒假 / 竞态 / 从未执行），
> 外加 2 次**环境事故**（都在 §5，如实记录，不甩锅也不掩饰）。

---

## 0. 这一轮最终拿到的读数（先看这张表）

| 流 | 覆盖的东西 | 读数 |
|---|---|---|
| `.maestro/acc-approval.yaml` | 委托 ACC → 智能体要工具批准 → **手机上看得到并答得了** → 智能体跑完 → 任务进「已结束」区 | ✅ **全绿**（2026-10-27 05:43~05:45，**2/2 Flows Passed in 1m 57s**，新 APK 165459023 字节 + agent-companion `9863034`）。证据链见 §4.6 |
| `.maestro/acc-delegate-stop.yaml` | 委托 ACC → 长按卡片 → 「停止任务」→ 确认 → 任务被停 → **以「已取消」出现在已结束区** | ✅ **全绿**（2026-10-27 05:56~05:57，**2/2 Flows Passed in 1m 9s**）。判据先修过一处恒真（§4.6.3） |
| `.maestro/acc-history-files.yaml`（**本轮新增**） | 委托 ACC → 智能体跑完 → **任务历史里看得到这次运行** → 展开 → **看得到产出的文件** → 打开文件 → **看得到内容**；顺带验「▶ 继续跟进」入口在 | ✅ **全绿**（2026-10-27 07:37~07:41，**2/2 Flows Passed in 2m 44s**，新 APK 165463351 字节 + agent-companion `a58aa73`）。改了四版才绿，**每一版红的原因都不是产品缺陷**（§4.7.6 有逐条取证） |

| `.maestro/acc-resume-prompt.yaml`（**本轮新增**） | 委托 ACC → 智能体跑完 → 长按卡片 →「▶ 继续跟进」⇒ **打开这次派单的原生会话**（读得到对话正文）→ 预填「继续」→ **改写**成别的提示词 → 发出 ⇒ **智能体照改写后的提示词回答** | ✅ **全绿**（2026-10-27 19:21~19:25，**2/2 Flows Passed in 3m 50s**，APK 165465327 字节）。逐条断言见 §4.8.8 |

★ **本轮（续五）全部读数都跑在同一份 APK 上：165465327 字节**
（设备 `ls -l` 与本地 `stat -f%z` **逐字节相等**，且 APK 内含新 bundle
`assets/public/assets/index-DTgOS7l1.js`）—— 满足 §6.1 的纪律：
**先核对设备上的 APK 是我刚构建的那个，再谈功能**。

四条流的最终读数（同一台 emulator-5556、同一份 APK、agent-companion `a58aa73`）：

| 流 | 读数 |
|---|---|
| `.maestro/acc-resume-prompt.yaml`（新增） | ✅ **2/2 Flows Passed in 3m 50s**（19:21~19:25） |
| `.maestro/acc-approval.yaml` | ✅ **2/2 Flows Passed in 1m 38s** |
| `.maestro/acc-delegate-stop.yaml` | ✅ **2/2 Flows Passed in 2m 15s** |
| `.maestro/acc-history-files.yaml` | ✅ **2/2 Flows Passed in 4m 31s**（**重跑过一次**：首跑红在 ACC 的 MCP 面 EOF，产品的行为是**诚实 502**，见 §4.8.8 v3 与 §6 第 13 条） |

本轮其它读数：

- **openpocket 前端门禁 50/50 全过**（`node scripts/run-gates.mjs`，103.9 秒）。
  ⇒ 途中**清掉了两个此前被挡在后面的既有失败**：第 31 项 `check:gofmt`（6 个真债文件）
  与第 34 项 `check:env-example`（两个 `POCKET_ACC_DISPATCH_*` 变量没写进 `.env.example`）。
- **openpocket 前端全量测试连跑三次全绿**（4732 pass / 0 fail）⇒
  途中一次红过的 `src/native/__tests__/outboxDrain.test.mjs` 判定为**负载相关的 flaky**
  （本轮没碰 `src/native/`），**如实记下来**。
- **openpocket 后端全量**：67 包全绿、0 FAIL（`OP_GO_TEST_EXIT=0`）。
- **agent-companion 全量**：**56 包全绿、0 FAIL**、`AC_GO_TEST_EXIT=0`（本轮该仓**代码未改动**，
  这是复验读数；完整读数集中在 §4.8.10）。

---

## 1. ★ 本轮最重要的发现：卡片在屏幕上，无障碍树里却没有

### 1.1 现象

`.maestro/acc-approval.yaml` 在真机上**始终红在最后一条断言**
（`id=completed-task-title` + `text=maestro-acc-approve`），
前面所有步骤都过。attempt9（`~/.maestro/tests/2026-10-26_135801/acc-approval/`）的工件给出了决定性对照：

* **同一时刻**的**截图**（`screenshots/step-017-*.png`）：
  `ACC 待批（1）` 卡片、`/tmp/maestro-acc-approve` 标题、
  `✓ 允许一次` / `✕ 拒绝` 两个按钮 —— **全都好好地画在屏幕上**。
* **同一时刻**的**无障碍快照**（`screen-hierarchy/step-017-*.json`）：
  `main` 的子节点从「下拉同步邮件」**直接跳到「ACC 生态」** ——
  中间**整个 section 节点不见了**，"ACC 待批" / "允许一次" 一个字都没有。

Maestro 的 `visible` 判据读的就是无障碍树 ⇒ 它看不见那张卡 ⇒
`runFlow when visible` 条件为假 ⇒ **tap 被跳过** ⇒ 那张卡永远没人点
⇒ daemon 120s 预算耗尽 ⇒ `permission denied (fail-closed)` ⇒ 任务失败。

attempt9 的 daemon 时间线把这条因果链坐实了：

```
13:57:52.170  PARKED     ← 第 1 个审批请求挂起（App 轮询后卡片出现）
13:58:48.891  ANSWERED   ← tap#1（13:58:44 点，7.9s）真的到了 daemon
13:58:48.917  PARKED     ← 第 2 个审批请求（同一个 toolCallId！）
13:59:13.623  ANSWERED   ← tap#2（13:59:10 点，5.5s）
13:59:22.538  PARKED     ← 第 3 个审批请求 ⇒ **没人点**（Maestro 看不见它）
14:01:22.539  DENIED     ← 120s 超时 fail-closed ⇒ 任务失败 ⇒ 末态断言红
```

Maestro 自己的逐命令时间线（`commands.json`）也印证了「看不见」：

```
tapOn 允许一次 → COMPLETED
notVisible 允许一次 → WARNED（15s 超时：卡片被下一张顶掉了，它并没有消失）
visible 允许一次 → COMPLETED     ← 第 2 张看得见
tapOn 允许一次 → COMPLETED
notVisible 允许一次 → COMPLETED
visible 允许一次 → WARNED         ← ★ 第 3 张：15s 内**始终看不见**
tapOn 允许一次 → SKIPPED          ← 条件为假 ⇒ 跳过
（此后 visible 连续 WARNED、tap 连续 SKIPPED，until 结束）
```

> ⚠ 注意这里还藏着一个**假绿**：`notVisible` 在树失联时**必然成立**
> （元素不在树里 ⇒ 「不可见」为真）。所以「卡片被划掉了」这条判据在树失联期间
> 是不成立的证据。这一条也一并修了（见 §2.5 与流文件注释）。

### 1.2 真根因（两个原因叠在一起）

**① 一次危险工具调用会让 opencode 连发 2~3 个审批请求，而且是同一个 `toolCallId`。**

daemon 的 ACP 原始帧（诊断开关 `AC_DEBUG_ACP_FRAMES=1` 才能看到）实测：

```
18:27:17.977  frame in   {"id":0,"method":"session/request_permission", ... "kind":"other",
                            "rawInput":{"command":"mkdir -p /tmp/probe-late && printf 'l\n' > /tmp/probe-late/l.txt && ls -la /tmp/probe-late",
                                        "directories":["/tmp"],"patterns":["/tmp/*"]}}   ← 写路径检查
18:28:34.475  frame out  {"id":0,"result":{"outcome":{"outcome":"selected","optionId":"once"}}}
18:28:34.495  frame in   {"id":1,"method":"session/request_permission", ... "kind":"execute",
                            "rawInput":{"command":"..."}}                                            ← 执行检查（同一个 toolCallId！）
18:28:37.477  frame out  {"id":1,...}
18:28:37.863  tool_call_update in_progress → 18:28:38.435 completed      ← 工具真的跑了
18:28:43.566  frame in   {"id":2,...}                                     ← 读检查
18:30:43.570  frame out  {"id":2,"result":{"outcome":{"outcome":"selected","optionId":"reject"}}}
```

⇒ **一个工具调用 ⇒ 多个审批请求，每个都要单独应答，漏一个就 120s 超时被拒。**
⇒ 「答一条 ⇒ 列表清空 ⇒ 下一条又来」是**常态**，不是异常。

> 这一条推翻了本轮中途的一个错误结论：当时读到 `id=0` 的应答后 20ms 就来了 `id=1`，
> 曾判断「daemon 把 `id=0` 的应答丢了」。**错**：`id=1` 不是重发，
> 是**另一个**权限检查（载荷形状不同：kind=other/带 patterns vs kind=execute/只有 command）。
> 直连 opencode 的探针也印证了：探针跑的是 `echo`（只触发一个检查），
> 所以「75 秒晚答也不重问」——不是 daemon 丢应答，是那条命令**只需要一个检查**。

**② App 的 ACC 待批 `<section>` 挂着 `v-if` ⇒ 列表空时整段被卸载，下一条待批到达时又重建；
而 Android WebView 的无障碍树在「节点被卸载后同位置重建」时会丢一整段子树。**

这解释了为什么 step-012（有卡片时）树里有 ACC 节点、step-017（有卡片时）却没有：
树里 `main` 的子节点列表还指着**那个已被卸载的旧 section**，新 section 从未进入缓存。
卡片被拒后（14:01:22）树又自己好了 —— 因为那时 section 又被卸载一次，缓存被刷新。

> 这**不是测试专属问题**：Android 的无障碍树就是 TalkBack / Switch Access 读屏用户的入口。
> 读屏用户在这段时间里同样看不到那张待批卡片 —— 也就是说，
> 「人在手机上能看到并应答审批」这个需求，在读屏场景下**当时是不成立的**。

### 1.3 修法（openpocket / `frontend/src/features/tasks/TasksView.vue`）

* **`<section>` 元素常驻 DOM**，只让里面的内容随列表进出：
  模板里把 `v-if` 下沉到 `<template>`（Vue 的 `<template v-if>` 只切换子节点，不动父元素）；
  空的时候靠 `.acc-approval-card.is-empty` 去掉外观。
* **不用 `display:none`**：那会把节点从无障碍树里也摘掉，等于换个写法再犯同一个错
  （`display:none` 的子树在 Blink 里同样会被摘出 AXTree）。
* 列表容器加 **`aria-live="polite"`**：内容变化时强制发一次无障碍事件，
  读屏软件也会播报「新增待批项」。

### 1.4 判据

`.maestro/acc-approval.yaml` 现在是这条链的判据：
**连答 2~3 张卡必须全绿**（`repeat times: 10`，每次先等按钮出现再点），
末态断言用 `id=finished-task-title-completed` + `text=maestro-acc-approve`
（带状态后缀 ⇒ 「这条已完成」和「这条失败」分得开 ⇒ 漏答一轮导致的
`permission_denied` 一定红，不会被别的失败任务冒充）。

---

## 2. 同一轮里修掉的其它缺陷

### 2.1 MCP 客户端：一次瞬时故障 = 整个进程的 ACC 委派永久失效（**健壮性缺陷**）

```go
// 改前（backend/internal/mcp/client.go）
c.initOnce.Do(func() { c.initErr = c.doInitialize(ctx) })
return c.initErr
```

`sync.Once` 保证那段闭包**一辈子只跑一次** ⇒ initialize 失败一次，
`initErr` 被永久钉住 ⇒ 此后每次 `CallTool` 都返回「MCP not initialized: …」，**没有任何自愈**。

修法（按**幂等性**分档，这是关键）：

* 握手失败**不缓存**（`initMu` 只做串行化，不做一次性）。
* `initialize`（幂等）⇒ 429/502/503/504/超时都可重试；
* `tools/call`（**写**）⇒ **只**重试 dial 失败（请求根本没到服务器）；
  超时/EOF **不重试** —— `acc_create_task` 没有幂等键（实测入参只有
  `{kind,title,description}`），重发会**建出重复任务**。

### 2.2 Android 上「⏹ 停止任务」根本按不到（**产品缺陷**，上一轮修）

长按任务卡片弹出的是 **Android 的文本选择菜单**（Copy | Share | Select all | Read aloud），
不是 App 的菜单。根因：App 的长按用 `touchstart` + 500ms 计时器，
而 Android WebView 长按**文本**会先起文本选择 ⇒ 派发 `touchcancel`/`touchmove`
⇒ 计时器被清掉 ⇒ 事件被系统吃掉。App 侧**从未处理 `touchcancel`**。

修法：`.task-card.compact` / `.session-item` 加 `user-select: none` +
`-webkit-touch-callout: none`；新增 `onTouchCancel` 清计时器并挂到 4 处卡片。

### 2.3 failed / cancelled 的任务在手机上**彻底消失**（**信息缺失缺陷**）

`completedTasks` 只收 `status === 'completed'` ⇒ 被拒、被停、超时的任务
在手机上**一行都不剩** ⇒ 用户分不清「我停掉了」和「它不见了」，
而需求里明写着「识别它的状态」「同步查看任务历史」。

修法：终态三兄弟（completed / failed / cancelled）一起进「已结束」区，逐条标状态；
标题的 testid 带状态后缀（`finished-task-title-<status>`）⇒ 判据才分得开。
分区标题从「已完成」改成「已结束」：把失败写进「已完成」是把失败藏进成功桶。

### 2.4 待批项应答失败后留下一张**永远点不掉**的死卡片（**产品缺陷**）

daemon 侧那条已经不是 pending 时（被别人答了 = 409、或压根不存在 = 404），
再点只会再失败一次，而条目**留在列表里** ⇒ 用户怎么点都没用。
修法：`decide()` 遇到 404/409 ⇒ **把条目从列表里摘掉**并说清楚发生了什么；
其它错误（网络抖、5xx）才保留条目 —— 那种情况它**也许**还能答。

### 2.5 诊断用的 ACP 原始帧日志**默认开着**（**数据暴露风险**）

排查审批环时把 `AC_DEBUG_ACP_FRAMES` 的默认值改成了「非 0 就开」，
于是**默认开着**的构建被部署到了线上 daemon：帧里带着工具标题、命令行和
option id（业务细节 —— 而命令里可能有用户输入的任意内容），量大且噪声高。

修法：默认改回「只有 `=1` 才开」，并补一条判据 `TestDebugACPFramesIsOffByDefault`
把默认值钉住（**这正是当时回归的那个默认值**）。开关本身保留 ——
这一轮就是靠它才定位到 §1.2① 的。

### 2.8 租约续租失败后要等 **14 秒** 才重试 ⇒ 一次网络抖动就让派单被判死（**产品缺陷**）

**这是本轮第二个真正的产品缺陷**（在 agent-companion 仓）。现场读数（21:22 那轮）：

```
21:22:28  executing dispatch            ← ACC 给了 120 秒租约
21:22:58  第 1 个审批请求挂进本地干预队列
21:24:19  permission answered locally    ← 人在手机上答了第 1 张卡
21:24:19  第 2 个审批请求到达（同一个 toolCallId 的第 2 个权限检查）
21:24:21  parked in the local intervention queue
21:24:33  lease expired mid-execution   ← 租约没了 ⇒ fail-closed ⇒ 任务被判死
```

⇒ **租约在人还在审批流程里就到期了**（还剩 13 秒时第 2 个请求才到）。

根因在 `internal/acc/lease.go` 的续租器：续租**失败**后只是 `continue`
⇒ 回到循环顶部按「剩余租约的 60%」再等一轮。对 120 秒租约：
第 1 次续租在 72s 失败 ⇒ 下一次要等到 ~101s；而续租请求本身又要耗时
（ACC 抖时是 7 秒超时）⇒ 剩下的机会在过期前就烧完了 ⇒ **租约必丢**。

而这个函数自己的注释写着「renew immediately on first failure」——
**注释与行为不符**，这就是它长期没被发现的原因。

修法：失败后**短退避**立即重试（200ms 起，翻倍，上限 5s，
且永远不超过剩余租约的一半）；成功与失败**都留日志**
（原来续租失败一行日志都没有，现场只剩「lease expired mid-execution」，
分不清是「没续」「被 fence」还是「ACC 网络抖」）。

判据 2 条（先红后绿）：

```
① TestLeaseRenewRetryDelay_IsShortAndBounded            纯函数：短、有上限、不超过剩余租约的一半
② TestLeaseState_RetriesRenewalSoonAfterTransientFailure 行为：一次 500 之后必须很快再试
```

> 对照读数（诚实版）：把修复整个收起后，判据是**编译不过**（纯函数不存在），
> 那不算「红」。所以另做了一次**更严格的对照**：保留新辅助函数、
> 只把重试调度还原成旧行为 ⇒ **行为判据红**：
> 「续租失败后 **1.441s** 才重试（上限 600ms）」；恢复修复 ⇒ 绿（~0.2s）。

**顺带查明的事实（对下一轮很重要）**：

* ACC 的租约 TTL **不认 `requested_ttl_ms`**：daemon 请求 30 秒，
  ACC 实测仍给 **120 秒**（请求 120s/600s 时因为派单已被 lease 而拿不到新样本）。
  ⇒ 想让人有更长的审批窗口，**只能靠续租**，不能靠请求一个更长的租约。
* ACC 的续租端点 `POST /api/v2/runtime/lease {action:"renew"}` **是存在的**
  （对已过期的租约返回 409 `lease_expired`）⇒ 续租这条路是通的，
  上面那个缺陷就是路被堵死的地方。

### 2.9 harness 的登录这段有三个真缺陷（**工具缺陷**，每一处都让整轮白跑）

| 缺陷 | 现象 | 修法 |
|---|---|---|
| **弹窗判定错了** | `need==='no'`（此刻页面上没有「创建主密码」弹窗）被当成「弹窗不需要处理」⇒ break。但**登录是异步的**：点「登录」后弹窗往往几百毫秒后才出现，第一轮检查（sleep 800ms 后）常常正好落在「还没来」的窗口里 ⇒ 后面没人点「确认」⇒ 弹窗后来弹出来 ⇒ 卡住登录 ⇒ 报「登录后仍停在登录页（30s）」。实测两次读数：21:09 那轮「confirmed → 登录成功」，21:20 那轮「no-confirm → 仍停在登录页」，**纯时序** | 用 **hash 是否已离开 `#/login`** 区分两种「弹窗不在」：已离开 ⇒ 账号本来就有主密码；还在登录页 ⇒ 弹窗可能马上来 ⇒ 继续等（最多 15×800ms）而不是 break |
| **假「已确认」** | 确认按钮**可能是 disabled**（两个框没都拿到值时它是灰的），而 disabled 按钮上的 `b.click()` 是**静默无效**的 —— 原实现照样打印 `confirmed`，于是「点了确认」是个**假陈述** | 重查按钮 + 读 `disabled` 状态 + 如实报告（`confirm-disabled` / `no-confirm`），还没生效就再等一轮（最多 6 轮） |
| **driver APK 缺失被报成「设备策略问题」** | `logs/maestro/driver-extracted/` 被清空（/tmp 被重启清过）时只报一句「APK 不存在」，而那句与「MIUI 拦截安装」**同形**（同一行日志、同样非 0 退出）⇒ 真因被读成设备问题，实测白跑一轮（21:05 那轮） | 缺 APK 时自己从 `maestro-client.jar` 解包（`extract-maestro-driver.mjs` 已跨平台化），解不出来才报错，并写明「这不是设备策略问题」 |

### 2.10 agent-companion 的 `go test ./...` 在 main 上**一直是红的**（既有失败，本轮修掉）

`tests/TestNoUnjustifiedPlatformSkips` 把
`internal/runtime/scenario_matrix_test.go` 的 `TestR5CriterionCanSeeALeftoverChild`
判为「**平台门跳过**」⇒ agent-companion 的 `go test ./...` 在 main 上就是红的。

* **不是审计误报**：那个 skip（`if !exact { t.Skip("no parent-PID source on this host") }`）
  确实让 **R-5 的负控**在拿不到父 PID 表时静默跳过 ⇒ 判据退化成恒真
  ⇒ 正是审计要防的「读起来像通过」。
* **也不是平台限制**：internal/runtime 自己的 prober 用的就是同一族来源
  （darwin: `ps -axo pid=,comm=`；windows: `Win32_Process` via CIM）
  ⇒ 读不到父 PID 的主机上**产品自己也是瞎的** ⇒ 那是坏环境，不是「本平台不适用」。
* **修法**：负控改成拿不到父 PID 表就**响亮失败**；探针子进程的平台差异挪进
  helper `startProbeChild`（测试函数体里不再有 `runtime.GOOS` 分支）；
  新增前提判据 `TestParentPIDSourceIsAvailable`。
* **审计判据自身仍有鉴别力**（改完专门验过）：临时塞一个
  `runtime.GOOS` + `t.Skip` 的测试进去 ⇒ 审计**仍然 FAIL**；删掉后恢复 PASS
  ⇒ 改的是真跳过，不是绕过审计。
* 提交：`agent-companion` `e1cdb7d`（已推 main）。

### 2.11 MCP 客户端：`initialize` 遇到 EOF 不重试 ⇒ 一次传输层掐断就让委派永久失效

* **实测证据**（2026-10-26 22:09 真机夹具）：
  `acc_create_task failed: MCP not initialized: initialize failed:
   failed to send request: Post "http://100.106.67.198:4101/api/v2/mcp": EOF`
  ⇒ 修这一条之后，同一台设备、同一份夹具，**委托当场成功**、待批项真的挂上来了。
* **根因**：initialize 的重试集是「dial 失败 | 429/502/503/504 | 超时」，
  **EOF / unexpected EOF / ECONNRESET / EPIPE 都不在里面**。
  而 initialize 是**幂等**的（只是握手，重做一次没有副作用）⇒ 必须重试。
* **写工具不跟着重试**：EOF 同样可能发生在「服务器已经建好任务、只是响应没回来」之后，
  而 `acc_create_task` **没有幂等键**（§4.4.5）⇒ 重发会建出重复任务。
* **判据**：新增 7 条（`backend/internal/mcp/client_eof_retry_test.go`），
  含「写工具遇到 EOF 一律不许重试」「重试预算必须封顶」「分类器本身要被测」
  「超时与 EOF 必须是两个独立的类」。
  **先红后绿的诚实对照**：只回退行为（保留分类器）⇒ 4 条行为判据红；
  恢复修复 ⇒ `go test ./internal/mcp/` 全绿。
* 提交：`openpocket` `1756b77d`（已推 main）。

### 2.12 产品级：`POST /api/tasks/delegate` 在「写请求结果未知」时**直接 502**，任务卡在 queue（**产品缺陷**）

★ 现场（2026-10-27 01:32 真机夹具，逐字）：

```
[fixture] 委托返回 502，但 ACC 里已经有同标题任务（3e513bcd-…）⇒ 视作已建，不重发
[fixture] 已委托：task_id=3e513bcd-… dispatch_id=null（失败后从 ACC 侧确认已建）
Error: 120s 内没有待批项出现 ⇒ 前提没造出来（这条流必须响亮失败）
```

⇒ 委托在 **`acc_create_task` 的响应没回来**时就 502 了 ⇒ **派发根本没发生**
⇒ 智能体没被派活 ⇒ 永远不会有待批项 ⇒ 整条流的前提造不出来。
而 ACC 侧其实**已经建出任务**了（只是响应没回来）。

* **为什么不能盲重试**：`acc_create_task` **没有幂等键**
  （ACC 侧 `acc-go/internal/mcp/server.go` 只吃 `payload` / `parent_task_id`），
  而 ACC 的 MCP 面实测会回 EOF/超时/400 ⇒ 重发会建出重复任务。
* **为什么不能直接报失败**：服务器**可能已经建好了** ⇒ 对用户说「失败了」是错的，
  任务就在那儿摆着（下次同步就会出现在任务列表里）。

★ **修法**：写请求**结果未知**（EOF / 连接被重置 / 超时）时，
**先问 ACC 本尊**：有没有「标题相同 **且** 创建时间不早于本次请求」的任务？
  · 有 ⇒ 服务器其实建好了 ⇒ 用它**继续往下走**（派发 + 落库 + 绑定），
    不重复建单；
  · 没有 ⇒ 才是真失败，照实 502。

★ **认领判据必须是两个条件同时成立**：
  · 只按标题找 ⇒ 会认领**上一轮的同名残留**（ACC 里同名能堆 27 条）
    ⇒ 夹具层已经踩过这个坑（先写成只按标题匹配，后补了时间下界）；
  · 只按时间找 ⇒ 会认领**别人**的任务。

★ **改动**：
  · `backend/internal/mcp/client.go`：`ParsedTask` 增加 `CreatedAt`；
    新增 `MatchTaskCreatedSince` / `FindTaskCreatedSince` / `IsAmbiguousWriteFailure`；
  · `backend/internal/server/server_task_delegate_acc.go`：写请求结果未知 ⇒ 复查 ACC ⇒ 认领或如实失败。

★ **判据**（先红后绿，诚实对照）：
  · `backend/internal/mcp/client_find_task_test.go`（新，9 条）：标题不同不认领、
    更早的不认领、**没有 created_at 不认领**、多个候选取最新、时钟偏差容忍、
    空标题不认领、解析要真的带上 created_at、缺 created_at 不丢任务、
    「结果未知」的分类（EOF/重置/超时 vs 业务错误）；
  · `backend/internal/server/task_delegate_acc_ambiguous_test.go`（新，3 条）：
    **认领后必须继续派发**（这是「任务卡在 queue」的根因所在）、
    认不出来必须如实失败、**同名但更早的任务不得认领**；
  · **只回退产品修复**（保留判据）⇒ `TestDelegateTask_AmbiguousCreateFailure_RecoversCreatedTask`
    红：`status = 502, want 200`；恢复修复 ⇒ 3 条全绿。

### 2.13 ★★ Maestro 的 `id:` 判据在这个平台上**结构性地永远不成立**（恒假判据）

★ 这是本轮挖到的最深的一条，**影响所有真机流的写法**，必须写清楚。

★ 证据（Maestro 自己存下的无障碍树，2026-10-26 12:28 那轮 acc-delegate-stop）：

```
text = maestro-acc-stop
text = · 38 秒
text = maestro-acc-approve
text = 已完成 1
```

⇒ 树里**只有文本**；App 的 `data-testid`
（`finished-task-title-cancelled` / `completed-section-toggle` / …）
**一个都没有变成 resource-id** —— 同一棵树里的 resource-id 全是
Android 系统那几个（`com.kaixuan.opencode.pocket:id/action_bar_root`、
`android:id/content`、`status_bar_*` …）。

★ 为什么：**Android WebView 不把 DOM 的 `data-testid` 映射成无障碍节点的 view id。**
⇒ 于是 `visible: {id: "completed-section-toggle"}` 这种判据**不是「这次没赶上」，
   是恒假** —— 它永远不成立。

★ 这条为什么重要：上一轮为了修「`visible: "已完成"` 是**恒真**判据」
（它命中的是分区标题），把判据换成了 id 判据 ⇒ **等于把恒真换成了恒假**，
两种都验不到东西，而报告上看起来都像「判据更精确了」。
⇒ 真机流的判据**只能靠文本**（外加空间关系 rightOf/below 把两个文本节点绑起来）。

★ 2026-10-27 本轮改成的写法（两条流都改了）：

```yaml
# 等分区标题（文本节点「已结束 N」）
- extendedWaitUntil:
    visible: "已结束.*"
    timeout: 180000
# 只在「一个状态标签都看不见」时展开（= 分区确实折叠着）
# ⚠ 不能用「有没有这一张卡片」判断 ⇒ 会把已展开的分区点回折叠
- runFlow:
    when:
      notVisible: "已完成|已取消|已失败"
    commands:
      - tapOn: "已结束.*"
# 滚到本条
- scrollUntilVisible:
    element:
      text: "maestro-acc-approve"
    direction: DOWN
    timeout: 90000
# 把状态标签与**本条标题**绑起来（rightOf）⇒ 不是恒真
- assertVisible: "maestro-acc-approve"
- assertVisible:
    text: "已完成"
    rightOf:
      text: "maestro-acc-approve"
```

★ 顺带修掉的第二个同源缺陷：`acc-delegate-stop.yaml` 原来**无条件**点 toggle
（`when: visible`）⇒ 分区**已经展开**时会被**点回折叠** ⇒ 末尾断言被自己搞红。

★ 判据形态的教训（写给下一轮）：
  · 「已完成」单独用 = 恒真（历史里别的已完成卡片也命中）；
  · `id:` 单独用 = 恒假（这个平台上没有 view id）；
  · ⇒ 要用**文本 + 空间关系**（rightOf / below）把「这条卡片的这个状态」绑死。

### 2.14 任务列表接口**被远端拖死**：ACC 一抖，App 主屏整个打不开（**产品缺陷**）

★ 现场（2026-10-27 02:23 真机夹具，逐字）：

```
2026/10/27 02:23:03 [WARN] writeJSON: 响应体写出失败（status=200）：
                write tcp 127.0.0.1:18099->127.0.0.1:58394: i/o timeout
2026/10/27 02:23:03 [SLOW] GET /api/tasks - 200 (41.239039542s)
⇒ 客户端侧：TypeError: fetch failed / SocketError: other side closed
⇒ 夹具的 clearResidual 直接崩掉 ⇒ 整条真机流白跑。
```

* **根因**：`GET /api/tasks` 是**三源聚合**（ACC + OpenCode + 本地 PG），
  而 ACC 那一源是**同步**调 MCP 的、且**没有超时预算** ——
  同一个函数里 OpenCode 那一源**反而有** `listSessionsTimeout` ⇒ 不一致。
  ⇒ ACC 一抖（EOF / 超时 / 400 都会），列表接口就卡 41 秒、响应写不出去
  ⇒ **App 主屏整个打不开**（不是「慢」，是打不开）。
* **修法**：远端源一律**有预算**（`accTasksListTimeout = 5s`，
  比 OpenCode 源的 3s 宽一点：ACC 一次往返实测 1.2~4.3s）；
  超了就跳过该源，本地源照常返回，ACC 的数据由后台 `tasksync`（15s 一跳）补齐。
* **判据**（先红后绿，诚实对照）：
  · `backend/internal/server/task_list_acc_budget_test.go`（新，3 条）：
    ACC 源挂住 ⇒ 列表接口**按时**返回 200（实测 **5.01s**）；
    本地源数据**照常**返回（远端挂住不能把本地数据也拖没）；
    预算常量本身被钉住（≤10s，别被悄悄改成 30s）。
  · 只回退预算（保留判据）⇒ 红：`接口耗时 30.01s ⇒ 远端源没有预算`；
    恢复修复 ⇒ 绿（5.8s）。

### 2.15 ★★ 「取租约的重试」修在了**死路径**上 —— 一次**假绿**（agent-companion `75755d3` → `0ffe17c`）

★ 本轮最贵的教训，单独一节。

**缺陷原型**（2026-10-27 02:38 真机夹具那轮的 daemon 日志，逐字）：

```
02:38:58 ERROR command lifecycle failed dispatch_id=dispatch_4e6338dd-…
       error="Post \"http://100.106.67.198:4101/api/v2/runtime/lease\": EOF"
⇒ 派单**已经到了 daemon**（SSE 收到了 dispatch.created），
  但**首次取租约**被一次 EOF 打中 ⇒ 整个派单生命周期直接判死
  ⇒ 智能体从没被启动 ⇒ 任务永远不会有审批请求
  ⇒ 真机流的前提（待批项出现）根本造不出来。
```

**`75755d3` 的修法**：把重试加在 `internal/acc/lease.go` 的 `AcquireOrRenew` 上，
判据也只测 `AcquireOrRenew`。

**但 daemon 的真实路径是**：

```
command_loop.go:546 → client.AcquireLease(...)   ← 直调，绕过 AcquireOrRenew
```

**机械证据**（`go tool nm` 查 agent-companion 可执行文件）：

```
github.com/kaixuan/agent-companion/internal/acc.(*RuntimeControlClient).AcquireLease   ← 在
github.com/kaixuan/agent-companion/internal/acc.AcquireOrRenew                        ← 不在
```

⇒ `75755d3` 的修复在生产路径上**一次都没生效**，而**判据是绿的**。
⇒ **根因不是「重试逻辑写错了」，是「我测的函数不是产品用的那个函数」。**
⇒ 误导来源：`internal/acc/lease_test.go` 里一句**假注释** ——
   「AcquireOrRenew is the unified entry point the command loop uses」。假的。

**修法**（`0ffe17c`）：

* `command_loop.go` 的取租约改走 `acquireWithRetry`；
* 新增 `internal/acc/acc_lease_path_guard_test.go`（**源码级守卫，防复发**）：
  仓库里任何 `.AcquireLease(` 调用点都必须位于 `acquireWithRetry` 内，
  出现别的直调点就判红；并断言「至少找到一个调用点」以免扫描规则失效导致守卫恒真；
* 订正 `AcquireOrRenew` 的注释（它**不是** command loop 的入口，目前只有测试在用），
  并要求它的 acquire 分支同样走 `acquireWithRetry`（两条路径不许分叉）。

**判据**（先红后绿 + 鉴别力自检）：

| 判据 | 层级 | 回退实现后 |
|---|---|---|
| `TestDriveCommand_LeaseAcquireRetriesAfterTransportEOF` | **真实路径** `driveCommand` | 红 |
| `TestDriveCommand_LeaseAcquireDoesNotRetryBusinessRejection` | 真实路径 | 绿（业务拒绝本就不该重试） |
| `TestDriveCommand_LeaseAcquireRetryBudgetIsBounded` | 真实路径 | 红 |
| `TestLeaseAcquireCallSitesAllGoThroughRetry` | 源码级守卫 | 红 |
| `TestLeasePathGuardStillSeesDriveCommand` | 守卫自身的**前提判据** | 红 |

### 2.16 ★★ 租约韧性：续租请求**没有超时上限** + 租约 TTL **比审批窗口还短**（agent-companion `dce8a35`）

★ 「人还在答题、派单先被判死」的真正根因。现场（2026-10-27 03:28 真机那轮，逐字）：

```
03:27:17 executing dispatch
03:27:35 lease renewed  expires_at=03:28:04.785
03:27:53 lease renewed  expires_at=03:28:22.393
03:28:10 lease renewed  expires_at=03:28:40.168
03:28:33 lease renewal failed attempt=1 remaining_ms=6255 error="Post \".../lease\": EOF"
03:28:37 accbridge permission answered locally allow=true option_id=once   ← 人在手机上答了
03:28:39 lease renewal failed attempt=2 remaining_ms=796  error="Post \".../lease\": EOF"
03:28:41 lease expired mid-execution; flagging dispatch as unknown
03:28:41 command lifecycle failed error="execution stopped after lease loss; outcome unknown"
```

**缺陷一：单次续租请求没有客户端超时上限。**
两次失败之间隔了 **5.46 秒** ⇒ 每次续租请求都卡了约 **5.2 秒**才被掐断
（EOF 不是立刻来的）。而那时的 deadline 就是**租约到期时刻**
（`context.WithDeadline(ctx, expiresAt)`）⇒ **一次卡死的请求可以吃掉整段余量**，
下面那段「短退避重试」策略（`leaseRenewRetryDelay` 的注释明写「永远不超过
剩余租约的一半」）**形同虚设** —— 在请求返回之前，根本没机会去算那个退避。

> ★ 顺带钉住一个事实：**5 秒是这条链路上的一个固定截止**。
> 2026-10-27 04:00 之后直连 ACC，`POST /api/v2/runtime/heartbeat`
> 连续三次都是 **5.00s** 超时（`HTTP=000 time=5.004s`）——
> 与续租那次的 5.2s 是同一个特征 ⇒ 隧道/前置代理有一个 5s 截止。

**缺陷二：致命的那次失败没有留痕。**
原来的顺序是「先判 `IsExpired()`，再打日志」⇒ 租约在请求期间到期时，
**致命的那一次失败不留下任何日志**，现场只剩一句「lease expired mid-execution」——
分不清是「被 fence」「网络抖」还是「续租根本没发出去」。
2026-10-27 03:29:43 那次正是这样：整个租约周期**一条**「lease renewal failed」都没有。

**缺陷三：租约 TTL（30 秒）比审批窗口（120 秒）还短。**
产品自己的两个时间常量互相打架：

| 常量 | 值 | 含义 |
|---|---|---|
| `AC_PERMISSION_TIMEOUT_MS` | 120 秒 | 人可以用 120 秒回答一次审批 |
| 租约 TTL | 30 秒（续租在 TTL 的 60% 处） | 一次续租失败只剩 **12 秒**余量 |

⇒ ACC 连续抖 12 秒，「人还在答题、任务先被判死」。
⇒ 实测 ACC 的租约端点 **03:28:27~03:28:49 连续 22 秒**不正常（EOF / connection reset）。

**修法**（agent-companion `dce8a35`）：

* 新增 `leaseRenewAttemptTimeout(remaining)`：单次续租尝试的上限
  **永远不超过剩余租约的一半**（⇒ 至少还留得下一次重试的位置），
  也不超过 5 秒（⇒ 卡死的端点吃不掉整段余量）；
* 续租失败**先留痕再分支**（带 `fenced` / `expired_now`）；
* 租约 TTL 默认 **30s → 120s**（`acc.DefaultLeaseTTLMS`），接成配置
  `AC_LEASE_TTL_MS`；「租约 TTL 必须覆盖审批窗口」这条**产品不变量**
  由 `tests/lease_ttl_invariant_test.go` 钉住（3 条，跨包）。

**判据**（先红后绿 + 鉴别力自检）：

| 判据 | 性质 | 回退实现后 |
|---|---|---|
| `TestLeaseRenewAttemptTimeout_NeverEatsMoreThanHalfTheLease` | 纯函数（上限 ≤ 剩余的一半） | 绿（常量本身没动） |
| `TestLeaseState_HungRenewEndpointStillGetsRetried` | **行为**：租约到期前还能再试 | **红**（旧实现只有一次） |
| `TestLeaseState_EveryRenewFailureIsLogged` | **行为**：每次失败都留痕 | **红**（旧实现致命那次没留痕） |
| `TestHungRenewServerActuallyHangs` | 判据自身的**前提判据** | 绿 |
| `tests/TestDefaultLeaseTTLCoversThePermissionWindow` | 跨包不变量（与环境无关） | 绿（默认常量没动） |
| `tests/TestEffectiveLeaseTTLCoversTheEffectivePermissionWindow` | 跨包不变量（生效值） | 绿 |
| `tests/TestCommandLoopConfigUsesTheSameDefaultLeaseTTL` | 单一事实来源 | **红** |

### 2.7 四个「只是声明一下」

| 现象 | 真因 | 修法 |
|---|---|---|
| 「上一轮清理了残留」在仓库里**没有兑现** | `scripts/acc-delegate-stop-fixture.py` 从未被执行：harness 只推导 `scripts/<name>-fixture.mjs`，而 `.maestro-flows.json` 里当时也没有 `fixture` 字段 | 换成 .mjs + 登记，本轮 flow 2 的清场日志真实可见（`[fixture] ✅ acc-delegate-stop --induce`） |
| App 真的被杀掉时报不出该报的话 | `maestro-run.mjs` 的 `ensureAppAlive()` 引用了外层循环变量 `i` ⇒ 抛未捕获的 `ReferenceError` | 改成由调用方传入「还剩几条 flow」 |
| `visible: "已完成"` 是**恒真**判据 | 「已完成」三个字只出现在**分区标题**里，任务卡片自己只写标题 + 相对时间 ⇒ 那条断言在任何时刻都成立（失败现场的无障碍树里就有「已完成 1」—— 那是**别的**已完成任务） | 改成定位到**本条**卡片 + 状态后缀 testid |
| 瞬时 toast 被断言两次 | `extendedWaitUntil` 等到了 toast，紧接着的 `assertVisible` 它已经消失 ⇒ 竞态红（13:28 那轮就红在这里，而屏上任务列表已空 ⇒ 停止其实成功了） | 去掉重复断言，保留持久判据 |

---

## 3. 判据（先红后绿）

### 3.1 MCP 客户端韧性（4 条）

```bash
cd /tmp/op-ac-test/backend
go test ./internal/mcp/ -run 'TestCallTool_' -count=1 -v
```

改前（红 3 条）：

```
--- FAIL: TestCallTool_InitializeFailureDoesNotPoisonClient
    第二次调用应当成功：初始化失败被永久缓存了（sync.Once + initErr）
--- FAIL: TestCallTool_RetriesTransientInitializeFailure
    initialize 只试了 1 次 ⇒ 瞬时故障没有重试
--- PASS: TestCallTool_DoesNotRetryPermanentFailure
--- FAIL: TestCallTool_RetriesDialFailureForWriteTool
    第三次应当成功（dial 失败必须重试）
```

改后：4/4 PASS。

### 3.2 agent-companion 的 ACP 权限应答（4 条）

```bash
cd /tmp/ac-e2e
go test ./internal/acp/ -run 'TestOpenCode(PermissionRequestKeepsItsID|AnswerPermission)' -count=1 -v
```

* `TestOpenCodePermissionRequestKeepsItsID` —— HTTP 适配器必须把审批请求的 `id` 转发出来
* `TestOpenCodeAnswerPermissionUsesJSONRPCResponse` —— 有 id 就回**同 id 的 JSON-RPC 应答帧**
* `TestOpenCodeAnswerPermissionFallsBackToNotificationWithoutID` —— 无 id 才退回旧的通知帧
* `TestOpenCodeAnswerPermissionCancelledOutcome` —— cancelled 的结局形状

改前红 ①②④、绿 ③；改后 4/4 绿。
外加 `TestDebugACPFramesIsOffByDefault`（§2.5）⇒ 5/5 绿。

> ⚠ **如实说明**：这 4 条修的是 **HTTP 适配器**（`internal/acp/opencode_http.go`），
> 而线上 opencode 走的是 **ACP stdio** 路径
> （`internal/agentfacade/known.go`：opencode ⇒ `ModeACPStdio`，args=`["acp"]`），
> stdio 路径本来就带 id 回包 ⇒ **它不是本轮真机那轮重复问的活因**。
> 但它确实是一个真缺陷（该路径上的应答永远匹配不上），修掉并留判据是对的。

### 3.3 后端全量

```bash
cd /tmp/op-ac-test/backend
DSN=$(grep -o 'postgresql://[^ ]*' ~/kaixuan/acp/logs/rerun-round52/pocketd-18099.env | head -1)
env -i HOME=$HOME PATH="$PATH" POCKET_TEST_POSTGRES_DSN="$DSN" go test ./... -count=1
```

⇒ **67 包全绿、0 FAIL**。

> ⚠ 有一次全量跑出 3 条红（`internal/agent` 的 `TestPiAdapter_SendPromptHappyPath` 等），
> 全部是 `agent request timed out: context deadline exceeded`，
> 当时宿主 load1 = **110**（模拟器 QEMU 413% + 并行会话的 MiniMax Code 渲染进程）。
> 单包复跑 ⇒ 全绿。**这是负载型 flaky，不是回归** —— 但也说明
> 这台机器在负载高时，智能体侧的响应会被拖到超时（§6 有对应风险）。

### 3.4 agent-companion 全量

```bash
cd /tmp/ac-e2e && go test ./... -count=1
```

⇒ 只有 `tests/TestNoUnjustifiedPlatformSkips` 红。
**这是既有失败**：把本轮改动全部收起后它仍然红（darwin 上
`TestR5CriterionCanSeeALeftoverChild` 的平台门禁），与本轮无关。
另有 `internal/toolmgr/TestConcurrentAppliesDoNotLoseEachOther` 偶发红（并发/负载相关，单跑绿）。

---

## 4. 设备侧读数

### 4.1 ★ a11y 修复的直接证据：**Maestro 点中了**（修复前是点不到）

同一台设备、同一条流、同一套判据，只换 App 的构建：

| | 修复前（attempt9，13:58） | 修复后（21:23 那轮） |
|---|---|---|
| 第 1 张卡 | `visible 允许一次` COMPLETED → tap COMPLETED | COMPLETED → tap COMPLETED |
| 第 2 张卡 | COMPLETED → tap COMPLETED | COMPLETED → tap **COMPLETED** |
| 第 3 张卡 | `visible` **WARNED ×6**、tap **SKIPPED ×6**（按钮**再没出现**） | ——（这一轮卡在租约，见 §4.2） |

修复后那轮的 daemon 时间线（**两次应答都真的到了 daemon**）：

```
21:22:28  executing dispatch
21:22:55  facade acp permission request received  request_id=0
21:22:58  accbridge: acc permission endpoint not wired; parking the request in the local intervention queue
21:24:19  accbridge permission answered locally    allow=true option_id=once      ← ★ App 的点按到了
21:24:19  facade acp permission request received  request_id=1                    ← 第 2 个权限检查
21:24:21  parking the request
21:24:33  lease expired mid-execution ⇒ fail-closed                              ← ★ 卡点变成租约
```

⇒ **「卡片在屏幕上、无障碍树里却没有」这个缺陷已消除**：按钮重新可见、可点，
且点按确实到达了 daemon（`permission answered locally`）。

### 4.2 本轮**没拿到**整条流全绿的原因（不是产品回归，是环境 + ACC）

1. **ACC 服务本身在抖**（对权威 ACC 的直连实测，22:02）：
   `GET /api/v2/approvals` 三次分别 **200(3.8s) / 200(6.4s) / 连接被 reset**；
   daemon 的心跳连续报 `context deadline exceeded` / `connection reset by peer`。
   ⇒ 派单虽然被 ACC 受理（返回了 dispatch_id），但命令**送不到 daemon**
   （夹具 120s 内看不到待批项 ⇒ 「前提没造出来」⇒ 夹具响亮失败、跳过该流 ——
   这是 harness 的正确行为，不是假绿）。
2. **ACC 的租约只有 120 秒**（实测：请求 30s，ACC 仍给 120s），
   而「智能体启动 → 第一个审批请求 → 人作答 → 第二个审批请求」这条链在
   本机负载下要 100 秒以上 ⇒ 租约在人在流程里就到期。
   ⇒ 已修（§2.8 的续租重试；修完后 daemon 日志里能看到
   `lease renewed` 每 18 秒一次、租约到期时间一直往后延 ⇒ 修复在生产里生效）。
3. **宿主负载 36~110**（并行会话 + 模拟器软渲染）⇒ Maestro 的登录阶段
   反复超时（`last hash=(超时)`）；设备端 `uiautomator dump` 一度被 **SIGKILL**
   （无障碍服务整体卡死，重启模拟器后恢复）。
4. **设备重启后 adb 连接掉了一次**：`Device emulator-5556 was requested,
   but it is not connected.` ⇒ 整条流没跑就退出了（这一轮没有假绿）。

### 4.3 判据用的真值来源

daemon 的 `parking the request` / `permission answered locally` /
`permission denied` / `lease renewed` 四条日志 + Maestro 工件
（`commands.json` 逐命令时间线、`screen-hierarchy/*.json` 无障碍快照、
`screenshots/*.png` 屏幕截图）+ ACC 命令面
（`GET /api/v2/runtime/commands/{dispatch_id}`）。

---

## 4.4 ★★ 续（2026-10-27 00:40~01:30）：**上一轮「修好了」的结论被推翻**，而且是**自己的错**

这一节是本轮的**核心产出**。上一轮（§4.1）写的是
「a11y 修复的直接证据：Maestro 点中了 ⇒ 缺陷已消除」。
**这个结论是错的**，而且错在一个很基础的地方：*修好的代码没装到设备上*。

### 4.4.1 决定性证据：**设备上跑的是旧 APK**

| | 大小 | 产出时间 | 内容 |
|---|---|---|---|
| 设备上装着的 | **165458754** | 2026-10-26 **12:50** | `section` 挂着 `v-if` ⇒ 列表空时**整段卸载** |
| 本地重建的 | **165459013** | 2026-10-26 **20:56** | `section` 常驻 DOM + `aria-live` |

差 **259 字节**。两边都验过：

* 设备上的 App（CDP 读 DOM，空态）**根本没有** `acc-approval-card` 这个 section：
  ```
  sections: ["triage-card", "triage-card acc-eco-card",
             "section running-section", "section sessions-section", "section completed-section"]
  ```
  —— 一个 `acc-approval-card` 都没有 ⇒ 它是被 `v-if` 卸载掉的。
* 本地新包的 bundle（`unzip` 出 `assets/public/assets/*.js`）里
  `acc-approval-card` **出现 2 次** ⇒ 修复确实在新包里。

⇒ **上一轮 21:23 与 22:22 两次真机跑，用的都是 12:50 的旧包。**
⇒ 上一轮 handoff 里「APK 重建成功（165459013 字节）并已装到 emulator-5556」
这句**是假的**：包重建了，但**没装**（装的是 12:50 那个）。
⇒ 教训（写下来给下一轮）：**「构建成功」和「设备上跑的是它」是两件事**，
   判据只能是「设备上那个 APK 的字节数/哈希 == 我刚构建出来的那个」。

### 4.4.2 22:23 那轮 flow1 为什么红（Maestro 自己的工件给出逐命令时间线）

`~/.maestro/tests/2026-10-26_222242/acc-approval/commands.json`：

```
22:23:07 seq=8   COMPLETED  assertCondition(允许一次)          ← 第 1 张卡看得见
22:23:08 seq=10  COMPLETED  tapOnElement(.*允许一次.*)         ← 点得中
22:23:13 seq=12  WARNED     assertCondition(允许一次)          ← 第 2 张卡**看不见**了
22:23:23 seq=13  SKIPPED    runFlow（“有点就点”）              ← 点不到
…               (8 轮全是 SKIPPED)
22:23:48 seq=40  FAILED     assertCondition(ACC 待批 清空)     ← 60s 超时
```

daemon 侧完全对上：

```
22:22:28  facade acp permission request received  request_id=0
22:22:29  parking the request
22:23:10  accbridge permission answered locally   allow=true option_id=once   ← App 的点按到了
22:23:10  facade acp permission request received  request_id=1                 ← 第 2 个权限检查
22:23:12  parking the request
22:25:10  accbridge permission denied (fail-closed)  context deadline exceeded ← 没人答 ⇒ 120s 超时
```

⇒ **同一个 toolCallId 的第 2 个权限检查**（`request_id=1`）一直没人答
⇒ 120s 后 fail-closed ⇒ 任务 `permission_denied` ⇒ 末尾断言「任务完成」必红。
⇒ 而「第 2 张卡看不见」的直接证据是 **Maestro 自己存下的无障碍树**：
`screen-hierarchy/step-013..037-assertCondition-允许一次.json` 里，
`main` 的子节点是「下拉同步邮件」→「收起」→「疑似卡死 Maestro任务…」→「ACC 生态」，
**中间整段 ACC 待批 section 不在树里**；同一时刻的截图上，卡片和「✓ 允许一次」
**明明在屏幕上**。

### 4.4.3 浏览器层隔离实验（把根因从「Vue」与「WebView」分开）

用 CDP 把 ACC section **克隆**成一个脱离 Vue vnode 树的节点（Vue 的 5s 轮询重渲染
碰不到它），再在克隆体上做纯 DOM 操作，然后 `uiautomator dump` 看它在不在树里。

**新 APK（= 修复后的构建，01:01 装上）上的读数（01:05，宿主 load 200 上下）：**

| 实验 | 做什么 | 无障碍树里有「允许一次」吗 |
|---|---|---|
| A | 克隆体清空 → 再填内容 | ✅ **有** |
| B | 克隆体清空 → 留占位 → 再填 | ✅ 有（占位本身也在树里） |
| C | 同 A，但去掉 `aria-live` | ✅ 有 |

⇒ 在**修复后的构建**上，「空 → 非空」这个跳变**不会**让无障碍树丢子树
⇒ 「section 常驻 DOM」这个修法是对的，**而且它本来就在源码里**（§1.3），
   只是**没装到设备上**。

**同一实验在 load 367 时的读数**：A/B/C/D 全 ❌ ——
但那不是「树里没有」，是 **`uiautomator dump` 根本采不到树**（无障碍服务被 SIGKILL）。
⚠ 这里暴露了**探针自身的一个假阴性**：把「dump 失败」当成「文本不存在」，
就会把**环境故障**报成**产品缺陷**。已修（`/tmp/probe-a11y-repro.mjs`：
dump 失败单独计数，判定只认采到了的树；一次都没采到 ⇒ 退出码 4 = 本轮读数无效）。

### 4.4.4 ACC 的 MCP 面确实在抖（直连实测，01:07~01:12）

按客户端的同一套协议（HS256 JWT + initialize + tools/call）直连
`http://100.106.67.198:4101/api/v2/mcp`：

```
initialize → ERR (fetch failed, 5038ms)      ← 握手直接失败
acc_get_tasks → 200 (3321ms)
acc_get_tasks → ERR (fetch failed, 5074ms)
acc_create_task → 200 (4255ms)               ← 写成功
initialize → 200 (136ms) / notifications/initialized → 400 / acc_create_task → 20s 超时
```

⇒ EOF、超时、400 都会出现。**不是**客户端会话过期的问题
（客户端 5min TTL 到点会重握手，01:04 的日志里能看到重握手发生过）。

### 4.4.5 写请求失败**不能盲重试**（ACC 侧没有幂等键）

ACC 侧源码（`agent-control-center/acc-go/internal/mcp/server.go` 的
`acc_create_task` 分支）只吃 `payload` / `parent_task_id`，
**没有 idempotency / source_ref** ⇒ 一次失败之后，服务器**可能已经建好任务**了
⇒ 盲重试会建出重复任务。
⇒ 所以夹具改成 **「失败先问 ACC 本尊，再决定要不要重试」**：
`scripts/lib/acc-fixture-common.mjs` 新增 `accTaskList()`（直连 ACC 读任务）
与 `delegateWithVerify()`（失败 → 按标题查 ACC → 查到了就算成功，查不到才重试）。
⚠ 判定**不能**用本地 `/api/tasks`：本地列表由 tasksync 每 15s 拉一次，
   有 15s 滞后 ⇒ 会把「其实建出来了」误判成「没建」⇒ 于是重复建单。

### 4.4.6 夹具的残留清理一直是**假的**（诚实记录）

`clearResidual()` 逐条 `DELETE /api/tasks/{id}`，对 **ACC 来源的任务**
（`source=acc`，id 是 ACC 的 uuid）**一律 404** —— 本地行的 id 是 `task-xxx` 形式，
两个 id 不是一回事，删不到。
⇒ 上一轮日志里那一串「删除 xxx -> 404」**不是清理成功**，是**什么都没删**。
⇒ 影响可控但要写清楚：残留的 ACC 任务停在 `status=queue`（没有派单，不会自己跑完），
   所以不会落进「已结束」区，末尾断言（`finished-task-title-<status>` + 标题）
   仍然只可能命中本轮那条；**但同名卡片可能不止一张**这件事要知道。
⇒ 现在 `clearResidual()` 会分别统计「删掉几条 / 删不掉几条」并打印，
   不再让一串 404 看起来像清理成功了。

---

## 4.5 ★★ 续（2026-10-27 02:45~04:05）：**真机 flow1 第一次跑到最后一条断言**，红因从「a11y」换成了「租约」

### 4.5.1 这一轮真正拿到了什么（**逐条，别当成「全绿」**）

| 环节 | 读数 | 证据 |
|---|---|---|
| 登录 | ✅ | preflight 通过（此前 03:04 那轮卡在这里，见 §4.5.3） |
| 夹具委派 | ✅ | `已委托：task_id=task-639ab88a9441a6c39c1b8e453ef47e6e dispatch_id=dispatch_83e03322-…` |
| 待批项出现 | ✅ | `✅ 待批项已出现：int-b5d035d6-… title="mkdir -p /tmp/maestro-acc-approve && …"` |
| **Maestro 的应答真的到了 daemon** | ✅ | daemon 日志 `03:28:37 accbridge permission answered locally dispatch_id=dispatch_83e03322-… tool_call_id=call_pWhJyFzGNm2bKe2omV2ZoyGb allow=true option_id=once` |
| 待批清空 | ✅ | flow1 步骤 3（`notVisible: "ACC 待批.*"`）过 |
| 任务跑完并进「已结束」区 | ❌ | flow1 步骤 4 红：`Assertion is false: "已完成", Right of "maestro-acc-approve" is visible` |

⇒ **审批环（App 看得到 → 人答得 → daemon 收到了）在真机上是真的通了**，
且这次读数是在**新 APK（165459013 字节，01:01 装的）**上拿的
（读数前核对过设备上 APK 字节数 == 本地构建，见 §6.1 纪律）。
⇒ **红在最后一步，而且红因是「租约丢了 ⇒ 任务被判 cancelled/unknown」**
   ⇒ 卡片当然不是「已完成」—— 那是**如实**的状态。

### 4.5.2 红因的完整时间线（daemon 日志，逐字）

```
03:27:17 executing dispatch dispatch_id=dispatch_83e03322-… fencing_token=1
03:27:35 lease renewed  expires_at=03:28:04.785
03:27:53 lease renewed  expires_at=03:28:22.393
03:28:10 lease renewed  expires_at=03:28:40.168
03:28:33 lease renewal failed attempt=1 remaining_ms=6255 error="Post \".../lease\": EOF"
03:28:37 accbridge permission answered locally allow=true option_id=once
03:28:39 facade acp permission request received request_id=1        ← 第 2 轮审批
03:28:39 lease renewal failed attempt=2 remaining_ms=796  error="Post \".../lease\": EOF"
03:28:41 lease expired mid-execution; flagging dispatch as unknown
03:28:41 accbridge permission denied (fail-closed) reason="acc emit failed: … context canceled"
03:28:41 command lifecycle failed error="execution stopped after lease loss; outcome unknown"
```

⇒ 第 1 轮审批**人已经答了**，第 2 轮审批到达时租约正好到期 ⇒ fail-closed。
⇒ 根因与修法见 §2.16（agent-companion `dce8a35`，已推 main）。

### 4.5.3 ★ 一次**基础设施事故**：Docker VM 被 poweroff ⇒ Postgres 断 ⇒ 登录超时 ⇒ flow 起步就红

03:04 那轮 flow1 在 preflight 就红了：「登录后仍停在登录页（30s），last hash=(超时)」。
**当时报的是登录失败，真因在三层之下**：

1. `scripts/diag-preflight-login-stuck.mjs`（改成 macOS 的 adb 路径后）报
   「情况 B：CDP 正常、hash 真的停在 #/login」，页面文案是
   **「登录失败，请检查网络连接与后端地址后重试」**；
2. 宿主直连 `POST /api/auth/login` ⇒ **HTTP=000 time=15.011s**（被 curl 掐断），
   而 pocketd 日志里那条是 `POST /api/auth/login - 200 (29.98s)`
   ⇒ 登录接口要 **15~30 秒**；
3. 再往下：`GET /api/notifications - 500 (607ms)`、
   `PUT /api/user-settings/app_prefs/default - 500 (13m58s)`、
   `POST /api/v2/runtime/lease` 连续 EOF ⇒ **所有碰 DB 的接口都在挂**。

**根因**：`~/Library/Containers/com.docker.docker/Data/log/host/com.docker.virtualization.log`
里逐字 ——

```
[2026-10-26T18:51:59.767711000Z][com.docker.virtualization] Requesting VM termination
[2026-10-26T19:08:39…] "stopReason":"service pl061 failed: poweroff requested"
```

⇒ **Docker 的 Linux VM 在 02:51:59 被 poweroff**，而 Postgres（`ac-audit-pg17`）
   跑在容器里 ⇒ 数据库从 02:51 起就断了 ⇒ 后端全挂 ⇒ 登录超时 ⇒ App 报「网络错误」。
⇒ 也就是说：**App 的报错是诚实的，是后端真的挂了**（这一点值得肯定 ——
   它没有假装成功，也没有把 500 吞掉）。

**恢复过程**（记录下来，下一轮可以直接照做）：

1. `docker desktop status` ⇒ **stopped**（UI 进程在，但 VM 没起）；
2. `docker desktop start` ⇒ 「already running」（**它只看 UI 进程，不看 VM**）
   ⇒ 这条命令在「UI 活着、VM 死了」这个状态下**没有用**；
3. `osascript -e 'quit app "Docker"'` + `pkill -f com.docker.backend` + `open -a Docker`
   ⇒ **10 秒内** Docker 29.8.2 起来，容器自动恢复，`ac-audit-pg17` healthy；
4. 恢复后 `POST /api/auth/login` ⇒ **8.9ms**（原先 15~30 秒）。

**但当时 VM 起不来**，因为宿主内存被打光（64G 里只剩 **58MB** 空闲、swap 用掉 12.2/13.3G），
而 Docker VM 配的是 **6144 MiB**（`settings-store.json` 的 `MemoryMiB`）。
腾内存的办法（**可逆**）：

```bash
# 卸载 Ollama 里已加载的模型（模型留在磁盘，下次用会自动重新加载）
curl -s http://127.0.0.1:11434/api/generate -d '{"model":"qwen3.8:27b-mlx","keep_alive":0}'
curl -s http://127.0.0.1:11434/api/generate -d '{"model":"qwen2.5:7b","keep_alive":0}'
# ⇒ 空闲页从 3636（≈58MB）涨到 1,464,690（≈23GB）
```

> ★ 为什么敢卸载 Ollama 的模型：派单智能体用的是**远端** provider
> （`~/.config/opencode/opencode.json`：`kaixuan/minimax-m3`），
> **不在 Ollama 链路上**；Ollama 当时加载的 `qwen2.5:7b` / `qwen3.8:27b-mlx` /
> `qwen3-embedding:8b` 与本次验证无关。
> ⇒ 顺带实测到：同一时刻**两个** Android 模拟器**都被内存风暴打死了**
>   （`adb devices` 里 5556/5558 双双消失），重新起了 5556（`pocket-test` AVD，4 核）。

### 4.5.4 ACC 在 04:00 之后**完全无响应**（本轮最后的阻塞）

04:00~04:02 直连 `http://100.106.67.198:4101/api/v2/runtime/heartbeat`：
**连续三次 `HTTP=000 time=5.00s`**（03:22 时还是 1.56s 的 401）。
⇒ daemon 的注册请求也全部被 reset ⇒ `command_loop_ready` 起不来
（`cannot acquire run_id (ACC unreachable); retrying`）⇒ 派单根本到不了 daemon。
⇒ 与 §2.16 记的「续租请求卡 5.2 秒才 EOF」是**同一个 5 秒特征**
⇒ **这条链路（隧道 utun4 / 前置代理）上有一个 5 秒的截止**，
   ACC 后端一慢，请求就被 5 秒后掐断。
⇒ ACC 主机本机**无 SSH** ⇒ 改不了、部署不了、验证不了 ⇒ 只能等它自己好。
---

## 4.6 ★★★ 续（2026-10-27 05:41~05:57）：**两条真机流都全绿了**

### 4.6.1 最终读数

| 流 | 读数 | 环境 |
|---|---|---|
| `.maestro/acc-approval.yaml` | ✅ **2/2 Flows Passed in 1m 57s** | 新 APK **165459023 字节**（设备上 `stat` 核对过）+ agent-companion `9863034`（`vcs.revision=9863034`、`vcs.modified=false`、pid 16503） |
| `.maestro/acc-delegate-stop.yaml` | ✅ **2/2 Flows Passed in 1m 9s** | 同上（判据先修过一处恒真，见 §4.6.3） |

flow1 的**完整证据链**（不是「看起来绿」）：

```
05:43:24 accbridge permission answered locally dispatch_id=dispatch_617dcb98-… allow=true option_id=once
05:43:34 accbridge permission answered locally …（同一 toolCallId 的第 2 次）
05:43:50 / 05:44:03 / 05:44:16 / 05:44:28 accbridge permission answered locally …（共 6 次，跨 3 个 toolCallId）
05:44:38 dispatch execution finished dispatch_id=dispatch_617dcb98-… outcome=succeeded executor_success=true
05:44:52 [tasksync] acc-bound task task-5f315d98… -> completed (dispatch=dispatch_617dcb98-… acc_status=succeeded)
05:44:52 [tasksync] acc-bound task status backfilled: 1 row(s)
⇒ 卡片状态回填到位 ⇒「已完成」+ rightOf 断言成立 ⇒ 末尾断言真绿
```

⇒ 也就是说：**App 看得到待批项 → 人答得完 → 智能体跑完 → 任务状态回填 →
卡片进「已结束」区并带「已完成」标签**，这条链路**在真机上跑通了**。

### 4.6.2 打通它靠的是这一轮的四笔修复（缺一不可）

| # | 修复 | 仓库 / 提交 | 缺了会怎样 |
|---|---|---|---|
| ① | 取租约的重试打在 **daemon 真实路径**上 + 源码级守卫 | agent-companion `0ffe17c` | 派单在首次取租约被一次 EOF 打中时**判死** ⇒ 待批项根本不出现 |
| ② | 续租请求有**超时上限** + 致命失败留痕 + **租约 TTL 覆盖审批窗口** | agent-companion `dce8a35` | 人还在答题，租约先到期 ⇒ 任务被判 cancelled ⇒ 卡片不是「已完成」 |
| ③ | **终态上报**能重试 | agent-companion `9863034` | 派单成功了但 ACC 不知道 ⇒ 状态回填读不到终态 ⇒ **任务永远停在「进行中」** |
| ④ | App 侧：已结束区的卡片**不丢无障碍树**（`role="list" aria-live="polite"`） | openpocket `df8df527` | 卡片在屏上、状态也对，但**无障碍树里没有它** ⇒ Maestro（和 TalkBack 用户）读不到 |

### 4.6.3 flow2 的判据有一处**恒真**（已修）

2026-10-27 05:46~05:48 那轮 flow2 红在「`.*停止任务.*` 不可见」，**红在错的地方**：

* 后端：`GET /api/acc/agents` ⇒ ACC 的 `/runtimes/{id}/agents` 连着 502；
  `POST /api/tasks/delegate` ⇒ `acc_create_task` 超时 ⇒ 复查 ACC 列表也 EOF
  ⇒ **照实 502**；
* App：「委托给 ACC」**弹窗还开着**，里面写着
  「读不到智能体清单（可留空由服务端选）：操作失败，请稍后重试」；
* Maestro：`visible: "maestro-acc-stop"` 命中的是**弹窗标题输入框里的那个字符串**
  ⇒ **绿（恒真）**；接着 `longPressOn` 长按的是**输入框** ⇒ 没有上下文菜单。

⇒ 修法：断言卡片出现**之前**先断言**弹窗关闭了**
（`notVisible: "ACC 会接管后续会话与执行.*"`）⇒ 之后标题断言只可能命中任务卡片。
⇒ ACC 恢复后重跑 ⇒ 全绿。

### 4.6.5 最终校验读数（2026-10-27 06:05~06:08，干净环境）

| 校验 | 命令 | 结果 |
|---|---|---|
| agent-companion 全量 | `cd /tmp/ac-e2e && go test ./... -count=1` | ✅ **EXIT 0**，56 包全绿 |
| openpocket 后端全量 | `cd /tmp/op-ac-test/backend && env -i … POCKET_TEST_POSTGRES_DSN=… go test ./... -count=1` | ✅ **EXIT 0**，67 包全绿 |
| 部署态 daemon | `go version -m $T` / `/healthz` | `vcs.revision=9863034`、`vcs.modified=false`、pid 16503、`command_loop_ready=true` |
| 设备 APK | `adb shell stat -c %s`（读数前后都核对） | **165459023** 字节 == 本地构建 |

> ⚠ 第一次跑这组全量时**出现过 4 条红**，全部是**我自己在跑测试的同时重启了
> Docker/Postgres** 造成的噪音，不是产品缺陷：
> `internal/sandbox` 的两条 Docker E2E 报
> `write /var/lib/desktop-containerd/daemon/io.containerd.metadata.v1.bolt/meta.db: read-only file system`
> （我在 §4.5.3 里用 `pkill -f com.docker.backend` 硬杀后端，把 containerd 的
> 元数据库弄成了只读）；后端那批报
> `the database system is shutting down`（Postgres 正在重启）。
> ⇒ 处置：`docker desktop stop` + `docker desktop start`（**不再硬杀**）⇒
>   `docker run` 恢复（`docker-run-ok`）；Postgres 恢复后再跑全量 ⇒ **两仓全绿**。
> ⇒ **纪律**：**别在跑全量测试的同时动 Docker/Postgres**；而且
>   `docker desktop start` 在「UI 活着、VM 死了」时**没用**，
>   硬杀后端则会把 containerd 弄成只读 ⇒ 恢复要用 **stop + start**。

### 4.6.4 这一轮踩到并已固化的两条纪律

1. **「判据绿」≠「产品路径被修了」**（§2.15）：判据必须打在**产品真的会走的那个
   函数**上，用 `go tool nm` 查二进制里有没有那个符号来确认，并加**源码级守卫**。
2. **判据要打在「用户能操作的那个对象」上**：flow2 的标题断言命中了**输入框里的
   文本** ⇒ 委托失败时照样绿。⇒ 先断言「承载输入的那个东西没了」，再断言目标。


---

---

## 4.7 ★★★★ 续（2026-10-27 06:20~07:5x）：**「任务历史」与「生成的文件」两个读面从零到真机**

### 4.7.1 ★ 根因一（agent-companion 侧）：ACC 派单**从不留痕** ⇒ run 列表恒空

**现场**（2026-10-27 05:44，一次成功的 ACC 派单之后）：

```
GET /api/v1/runs                                  → {"runs":null,"total":0}
GET /api/v1/artifacts/<dispatch>/output.txt       → 1607 字节（真实产出）
```

⇒ 主机**刚跑完一个任务**，任务历史却说「从来没跑过任何东西」。

**根因**：ACC 派单走 `accbridge.FacadeExecutor.Execute`，而 API 层只在
**自己**发起的 operate 上留痕（`internal/api/operate.go` 的 `retainRun`）
⇒ 一个 ACC 派单跑完了、给 ACC 报了终态、写了产出文档，**daemon 自己的
历史里一行都没有**。

**修法**（agent-companion `a58aa73`）：新增 `accbridge.RunRecorder` 接缝 ——
执行器在 run 结束时把终态 run 推回 daemon 自己的历史
（`api.Server.RecordTerminalRun`）。依赖方向是「执行器 → API 层」：
执行器不知道历史怎么存，API 层不需要知道 ACC 派单的存在。

**判据打在产品真会走的路径上**（不是单测 recorder）：
`internal/api/acc_run_history_test.go` 真的跑一次 ACC 派单
（accbridge → facade → recorder → HTTP 读回 `/api/v1/runs`）。
**回退接线后该用例真红**（`Total:0`），恢复后绿。

**接线守卫**（`cmd/agent-companion/acc_run_history_wiring_test.go`）：
`internal/api` 里那个用例是**手工接线**的 —— main.go 忘了接的话，单测照样绿、
生产照样是空的（这正是上一轮「取租约重试修在死路径上」的同一种假绿）。
守卫查三件事：调用点只允许在 `recordTerminalRun` 里、`Execute` 真的调它、
main.go 真的把 apiServer 当 recorder 交出去。

> ⚠ **守卫第一版是恒真的，自己抓出来了**：它用 `strings.Contains` 找
> `RunRecorder: runRecorder` ⇒ 把那一行**注释掉**之后源码里仍然含有这个
> 子串 ⇒ 守卫**照样绿**。实测确认（注释掉接线，守卫没红）⇒ 改成 **AST 判定**
> （`CompositeLit` 里真的有那个 key、`AssignStmt` 真的是那条赋值）
> ⇒ 再测，注释掉就红。**守卫自己也要被测。**

### 4.7.2 ★ 根因二（agent-companion 侧）：产出**列不出来**

daemon 只有 `GET /api/v1/artifacts/:dispatch_id/:name`（取字节），
**没有清单面** ⇒ 调用方必须**已经知道**文件名才能拿到产出。
补 `GET /api/v1/artifacts/:dispatch_id`：文件名 + 字节数 + sha256 + 可直接用的
`read_path`。清单里的摘要不是装饰 —— 完成包声明的是 `content_hash`，
这是让「拿到的就是声明的」变成**可核对事实**的唯一途径。

### 4.7.3 ★ 根因三（agent-companion 侧，既有缺陷）：并发写配置的 TOCTOU

`internal/toolmgr` 的 `atomicWrite` 先 `rename(path→backup)` 再
`rename(staged→path)` ⇒ **两步之间文件在目录里根本不存在**。
而 claude 的 agent surface 是靠**列目录**发现的 ⇒ 并发 Apply 时另一条
goroutine 扫目录扫不到该 agent ⇒ 答
`unknown tool: "claude:agent:implementer:WebFetch"`。

**取证**（clean origin/main `9863034` 的独立 worktree）：

```
go test ./internal/toolmgr -count=20   → FAIL（unknown tool: claude:agent:implementer:WebFetch）
```

**修法**：备份改成**复制**（不是改名），然后一次原子 rename 替换目标
⇒ `path` 从不缺席。修后同一条命令 ⇒ 绿。**回退修复 ⇒ 重新变红**（鉴别力自检）。

### 4.7.4 ★ 根因四（openpocket 侧）：读面**根本没有出口**

`CompanionClient.ListRuns` 早就在，但**没有任何路由用它**；
产出文件连客户端方法都没有 ⇒ 手机上两头都空。
本轮补三个路由（`backend/internal/server/companion_history.go`）：

| 路由 | 作用 |
|---|---|
| `GET /api/acc/runs` | 任务历史（daemon 的 run 列表） |
| `GET /api/acc/runs/{id}/artifacts` | 一次拿全：工具调用 + 产出文件 |
| `GET /api/acc/artifacts/{dispatch}/{name}` | 产出文件的内容 + sha256 |

### 4.7.5 ★ 活链路读数（真机前的 API 级取证）

真机部署后，宿主上从 daemon 一路打到 pocketd：

```
daemon  GET /api/v1/runs
        → {"run_id":"dispatch_230b4450-…","dispatch_id":"dispatch_230b4450-…",
           "task_id":"acs-ac30-01:dfa987ac-…","agent_id":"opencode","kind":"opencode",
           "state":"succeeded","session_id":"ses_e85903cd9…",
           "answer":"好的","started_at":"…","ended_at":"…"}

daemon  GET /api/v1/artifacts/dispatch_230b4450-…
        → {"count":1,"exists":true,
           "files":[{"name":"output.txt","size":6,
                     "sha256":"sha256:536edaf7…",
                     "read_path":"/api/v1/artifacts/…/output.txt"}]}

pocketd GET /api/acc/runs
        → {"count":1,"runs":[{…,"answer":"好的"}],"source":"companion"}

pocketd GET /api/acc/runs/dispatch_230b4450-…/artifacts
        → {"artifacts":[],"count":0,"file_count":1,
           "files":[{"name":"output.txt","size":6,"sha256":"sha256:536edaf7…"}]}

pocketd GET /api/acc/artifacts/dispatch_230b4450-…/output.txt
        → {"content":"好的","sha256":"sha256:536edaf7…","size":6}
```

daemon 侧同一时刻的日志（**证明它真的走了新代码**，不是缓存）：

```
07:58:31.276 terminal run recorded in the daemon history
              run_id=dispatch_230b4450-… task_id=acs-ac30-01:dfa987ac-… state=succeeded
07:58:31.279 dispatch execution finished … outcome=succeeded executor_success=true elapsed_ms=8535
```

### 4.7.6 ★ 真机流 `acc-history-files.yaml`：三处**判据缺陷**（都是实测抓的）

这条流改了四版才绿，**每一版红的原因都不是产品缺陷**：

| 版 | 红在哪 | 真相 |
|---|---|---|
| v1 | 「文件内容」不可见 | **点歪了**：`output.txt` 那一行落在**底部导航栏的上沿之下**（实测 bounds `[115,2149][309,2215]`，导航栏从 2170 起）⇒ 按文本点按的是元素中心（y≈2182）⇒ 命中的是**导航栏**。⇒ 先滚一下再点。 |
| v2 | 「opencode.*进行中.*」120 秒等不到 | **历史是一次快照**，点一次「读取任务历史」就不再取；而且派单刚发出时 run 状态是 `queued`（标签「排队中」），跑起来才是 running。⇒ 判据必须**反复重读**。 |
| v3 | 同上，但派单 4 分钟没跑完 | **夹具名当提示词**：标题写 `maestro-acc-history`，智能体以为「这是要找命令/技能」⇒ 在工作区里翻（`ls -la …`）⇒ 5 分钟超时 `outcome_unknown`。改成 `只回复HISTORYOK` ⇒ 85 秒跑完、答复正好 `HISTORYOK`。 |
| v4 | — | 全绿（见 §0 表） |

> ★ **「派单超时」和「读面坏了」是两回事**：v3 那次红的时候，
>   daemon 日志里清清楚楚写着 `dispatch execution finished … outcome_unknown`
>   ⇒ 派单本身失败了 ⇒ 读面没有东西可显示是**正确的**行为。
>   ⇒ 判据红的时候第一件事是去看 daemon 侧那条终态日志。

### 4.7.6.1 ★ 回归：加了卡片之后，**前两条流必须重跑**

本轮在任务页**新增了一张卡片**（「任务历史与产出」）⇒ 页面布局变了 ⇒
前两轮拿到的全绿读数**不再自动成立** ⇒ 必须重跑（不是「应该没事」）。

读数（同一台 emulator-5556、同一份新 APK 165463351 字节、agent-companion `a58aa73`）：

| 流 | 读数 |
|---|---|
| `.maestro/acc-approval.yaml` | ✅ **2/2 Flows Passed in 1m 25s** |
| `.maestro/acc-delegate-stop.yaml` | ✅ **2/2 Flows Passed in 1m 26s** |
| `.maestro/acc-history-files.yaml`（新增） | ✅ **2/2 Flows Passed in 2m 44s** |

flow3 的关键断言逐条 COMPLETED（取自 Maestro 工件
`~/.maestro/tests/2026-10-27_073728/acc-history-files/commands.json`）：

```
visible "output.txt"                    COMPLETED
tapOn  "output.txt"                     COMPLETED
visible "文件内容"                        COMPLETED   ← 文件内容真的读回来了
visible "答复：.*HISTORYOK.*"            COMPLETED   ← 这次运行的答复
visible "HISTORYOK"                     COMPLETED   ← 产出文件的**字节**
```

### 4.7.7 ★ 前端的三条**判据纪律**（写在代码注释里，因为它们直接决定别的流会不会假绿）

1. **卡片默认收起**：展开时屏上会出现 run 行文本，而 flow1/flow2 用的正是
   `notVisible: "maestro-acc-stop"` 与 `notVisible: "已完成|已取消|已失败"`
   ⇒ 展开会污染**别的**流的判据。
2. **不在行里渲染智能体的答复原文**：实测 `maestro-acc-approve` 的答复就是
   「已完成」这三个字 ⇒ 渲染它等于往屏上放一个「已完成」节点。
3. **状态标签用「成功 / 失败 / 取消」**，而**不是**「已完成 / 已失败 / 已取消」：
   Maestro 的 `visible` 是**完整匹配**，那三个带「已」的词正是 flow1 的匹配目标。
4. **展开态在 `onActivated` 时复位**（TasksView 在 KeepAlive 名单里，
   离开页面只是**失活**不卸载 ⇒ 复位点不能挂 `onMounted`）
   ⇒ 否则上一条流展开后留下的文本会污染下一条流。

---

## 4.8 ★★★★★ 续（2026-10-27 18:00~18:5x）：**「继续」与「提示词的修改」端到端打通**

> 这一节关的是原始需求里最后两个没打通的字：
> 「**分配任务，继续、停止及进行提示词的修改**」。
> 分配 ✅、停止 ✅（§4.6 已全绿）；**继续**与**提示词的修改**到这一节之前
> 只有「入口在、点得动」的假绿（§6 第 12 条）。这一节把它们做成了真的。

### 4.8.1 ★ 根因五：**两处**，而且第二处比第一处更致命

**根因 5a：ACC 派单的会话，从来没有被链到本地任务行。**

实测（不是推测）：`GET /api/tasks/task-844306048a60860bf8e2111a3c161bfd/sessions`
⇒ `{"sessions":[]}`，而这个任务**确实**是跑完的 ACC 派单
（`acc_dispatch_id=dispatch_11ea983c-…`，`status=completed`，
`sessionCount=0`）。⇒ App 的「▶ 继续跟进」拿不到任何会话。

**但数据是齐的**，三处都能读到：

| 源 | 实测读数 |
|---|---|
| agent-companion 的 run 历史 | `GET /api/v1/runs/dispatch_11ea983c-…` ⇒ `{"session_id":"ses_e856b5fc6ffelsAgCf6yPt21h4","kind":"opencode",…}` |
| 原生会话元信息 | `GET /api/v1/native/sessions/ses_e856b5fc…` ⇒ `{"kind":"opencode","resumable":true}` |
| 原生会话正文 | `GET /api/v1/native/sessions/ses_e856b5fc…/transcript` ⇒ 2 条消息（`只回复HISTORYOK` → `HISTORYOK`） |

⇒ **缺的不是数据，是接线**：没人把「这次派单用了哪个原生会话」写进
`task_session_links`。

**根因 5b（更致命）：App 的全局会话视图，**根本发不了消息**给这类会话。**

`frontend/src/stores/session.ts` 的 `sendPrompt`：

```ts
async function sendPrompt(text: string, agent?: string, model?: any) {
  if (!sessionID.value || !instanceID.value) return          // ← 原生会话没有 instance
  … await http(`/api/mobile/sessions/${sessionID.value}/prompt?${qs}`)  // ← 实例作用域端点
```

而 ACC 派单出来的是 **companion 原生会话**：它**没有 instance**
（它不属于 registry 里的任何 opencode 实例）。
⇒ 即便把会话链接补上、把「继续跟进」指到 `/sessions/:id`，
用户看到的也只是**一个发不出消息的空会话**。

⇒ **「继续」必须走任务域的出口**，而不是全局会话视图。

### 4.8.2 修法：任务域的两个出口

**① 自动挂链**（`backend/internal/server/companion_task_session.go`）

`GET /api/tasks/{id}/sessions` 现在会：任务绑了 ACC 派单 ⇒ 从 companion 的
run 历史解出那次派单的 `session_id` ⇒ 幂等地写进 `task_session_links` ⇒
并在响应里带上 `kind` 与 `native` 两个字段。

- 为什么用 **run 历史**而不是 companion 的 `/api/v1/session-links`：
  session-links 是**会话自己**的链接表，只在会话被 operate/bind 过之后才有行；
  **ACC 派单本身不写它**（实测 dispatch 跑完，links 里没有那一行）。
  run 历史是**派单的产物**，每条都带 dispatch_id ⇒ session_id，
  且 ACC 派单的 `run_id` **就是** `dispatch_id`
  （accbridge 的 `TaskRequestFromEvent` 里 `req.RunID = event.DispatchID`）。
- 为什么**写进库里**而不是每次现算：链接一旦建成就**持久**
  （openpocket 自己的 Postgres），不随 daemon 重启而失忆。

**② 续写出口**（`POST /api/tasks/{id}/sessions/{sid}/message`）

往这次派单用到的原生会话里投一条**可改写**的提示词，返回 run 收据。
三条硬约束：

1. **会话必须挂在这个任务上**，否则 404 —— 否则这就是一个
   「给任意 native session 发消息」的后门。
2. **会话必须 resumable**，否则 409（只读观察会话要**如实说**不能投，不是静默成功）。
3. **提示词不能为空**，否则 400 —— 且**不投递**。

**③ 一个必须记下来的坑：kind 要放**查询参数**，不是 body。**

daemon 侧的 `operateNativeSession` 是

```go
sess, err := s.native.Get(c.QueryParam("kind"), pathParam(c, "id"))
```

⇒ 它**只读查询参数**，body 里的 `kind` 它**根本不看**。
只放 body 的话，kind 被**静默丢掉**（不报错、只是没生效）。
⇒ 改法是把 kind 放到 `?kind=…`，并为此专门写了一条判据（§4.8.3）。

### 4.8.3 ★ 判据：6 条**路由级**测试 + 2 处**鉴别力自检**

`backend/internal/server/companion_task_session_test.go`（6 条，全绿）：

| 判据 | 断言的是什么 |
|---|---|
| `TestACCDispatchTask_GetsNativeSessionLinked` | 绑了 ACC 派单的任务读会话列表 ⇒ 拿到那次派单的原生会话（`native=true`、`kind=opencode`），且**再读一次仍然只有一行**（持久、幂等），且库里真的有一行 |
| `TestTaskSessionMessage_OperatesLinkedSession` | 投提示词 ⇒ daemon 真的收到了 prompt，**且 kind 落在查询参数上** |
| `TestTaskSessionMessage_RejectsUnlinkedSession` | 没挂到这个任务上的会话 ⇒ 404，且 **daemon 收到 0 次 operate** |
| `TestTaskSessionMessage_RejectsEmptyPrompt` | 空提示词 ⇒ 400，且 **daemon 收到 0 次 operate** |
| `TestTaskSessionMessage_RejectsObserveOnlySession` | 只读会话（`resumable=false`）⇒ 409，且 daemon 收到 0 次 operate |
| `TestTaskSessionMessage_UnboundTaskHasNoSession` | 没绑 ACC 派单的任务 ⇒ **不会凭空长出链接** |

**鉴别力自检（判据本身也要当判据测）**：

| 自检 | 回退实现 | 判据读数 |
|---|---|---|
| ① | 去掉「自动挂链」 | ❌ `sessions = 0, want 1` + ❌ `status = 404, want 200` ⇒ **真红** |
| ② | kind 只放 body（daemon 读不到） | ❌ `daemon got kind(query) = "", want opencode` ⇒ **真红** |

复原后两条都绿。

### 4.8.4 ★ 前端：会话抽屉里的续写区（「提示词的修改」的落点）

- `api/client.ts`：新增 `sendTaskSessionMessage()`；`SessionLink` 补 `kind` / `native`。
- `useTaskSessionSheet.ts`：新增 `draft` / `sending` / `sendError` / `sentHint` 与 `send()`。
- `TaskSessionSheet.vue`：新增 `initialPrompt` 属性（**预填「继续」**）与续写区
  （textarea + 发送按钮），**只在 `row.native` 时渲染** ——
  实例会话的消息走全局会话视图，在这里发会发不出去。
- `TasksView.vue`：`ctxResume` 分流 ——
  **原生会话** ⇒ 打开任务域的会话抽屉（预填「继续」，可编辑再发）；
  实例会话 ⇒ 保持原行为（跳 `/sessions/:id`）。

**判据纪律**（写进代码注释）：

- 「已发送」这个回执标签**只在真的投出去之后才渲染** ⇒ 看得见它 = 真的投了。
- 「答复：」/「文件内容」这类标签**只在非空时渲染** ⇒ 真机「看得见」=「非空」。
- 原始错误**不能直接上屏**（`check:raw-error` 门禁第 26 项）⇒ 走 `apiError` 映射。

### 4.8.5 ★ 活链路 API 级取证（不是单元测试，是真的 daemon）

对**活着的** daemon（agent-companion `a58aa73`）：

```bash
POST /api/v1/native/sessions/ses_e856b5fc6ffelsAgCf6yPt21h4/operate
  {"prompt":"只回复RESUMEOK"}
⇒ {"agentKind":"opencode","nativeSessionId":"ses_e856b5fc6ffelsAgCf6yPt21h4",
    "runId":"run-30c8e6ff-…","state":"queued"}

# 随后再读同一个会话的正文：
GET /api/v1/native/sessions/ses_e856b5fc6ffelsAgCf6yPt21h4/transcript
⇒ 4 条消息（原来 2 条）：
     user      | 只回复HISTORYOK
     assistant | HISTORYOK
     user      | 只回复RESUMEOK      ← 新投的
     assistant | RESUMEOK           ← 智能体真的回了
```

⇒ **「改提示词 + 继续」在 agent-companion 侧是通的**；
这一轮补的是 openpocket 到它的**接线**。

### 4.8.6 ★ 顺带清掉的**两个既有门禁失败**（此前被前面的失败**挡住**没显形）

`run-gates.mjs` 遇红即停 ⇒ 前面有一项红，后面几项根本没跑到。
修掉前面的之后，后面的既有债**自己冒出来了**：

| 项 | 现象 | 判定 | 本轮处理 |
|---|---|---|---|
| 第 31 项 `check:gofmt` | 6 个文件归一化行尾后仍不符合 gofmt | **既有失败**：逐个版本回放（`e029fad3` → `e793704f`）确认这 6 个文件在**我这一轮改动之前就已经 dirty** | 用门禁自带的 `--fix` 清掉（只碰真债文件，不统一全仓行尾）⇒ **纯格式化、零行为变化** |
| 第 34 项 `check:env-example` | `POCKET_ACC_DISPATCH_AGENT_ID` / `POCKET_ACC_DISPATCH_RUNTIME_ID` 被 backend 读了却没写进 `.env.example` | **既有失败**：由 `d0807c74 feat(acc):「委托给 ACC」与「停止任务」第一次真的通了` 引入 | 补进 `.env.example`（留空 = 沿用「现查可派槽位」的默认行为） |

⇒ 修完之后 **50/50 全过**（103.9 秒）。

**另有一条** `src/native/__tests__/outboxDrain.test.mjs`（network down 退避那条）
在一次全量跑里红过一次 ⇒ **连跑三次全绿（4732 pass / 0 fail）** ⇒ 判定为
**负载相关的 flaky**（本轮没碰 `src/native/`，与本轮改动无关）。
**如实记下来，不当它不存在，也不当它是本轮改坏的。**

### 4.8.7 ⚠ 一个**必须说清楚**的限制：链接的解析窗口

解析「这次派单用了哪个原生会话」用的是 **daemon 的 run 历史**，
而 run 历史是**内存态**：

- daemon 重启即失忆；
- 保留期 1 小时（`retainedRunTTL`）。

⇒ **在派单之后 1 小时内**第一次读这个任务（或后台同步先跑到），
链接就建好了 ⇒ 此后**永久**有效（因为链接写在 openpocket 自己的库里）。
⇒ **但如果 daemon 在这段时间内重启过、或一直没人碰过这个任务**，
这个链接就**再也建不起来** ⇒ 「继续跟进」会退回到「查看详情」。

这一条**没有在本轮解决**（要解决得让 daemon 把
「dispatch ⇒ session」的映射**落盘**，或者给 ACC 的 session ledger 加一个读面）。
**不要把这一条说成已经解决。**

### 4.8.8 ★ 真机流 `.maestro/acc-resume-prompt.yaml`：**两次红，各有各的根因**

这条流守的是「继续」+「提示词的修改」。它一共跑过三版，**红的原因一次比一次靠后**：

| 版本 | 设备 APK | 红在哪 | 根因 | 性质 |
|---|---|---|---|---|
| v1（19:06 前那轮的第一次跑） | 165463351 | 「委托面板没关闭」 | ACC 的 `POST /api/v2/runtime/dispatch` **502（5.01 秒）** ⇒ 派单失败 | **环境**（ACC 抖） |
| v2 | 165465220 | 最后一条 `visible: "RESUMEOK"` | 抽屉的正文是**快照** ⇒ 智能体的异步回答不显示 | **产品缺陷**（已修，§4.8.9） |
| v3 | 165465220 | 「委托面板没关闭」，且**这次连任务都没建出来** | tap 打到了按钮（截图确认按钮**清晰可见**、坐标也对），但**请求没发出去** ⇒ 按钮的点击没触发处理 | **待查**（见下） |
| **v4** | **165465327** | —— | —— | ✅ **2/2 Flows Passed in 3m 50s** |

v4 的逐条断言（取自工件 `~/.maestro/tests/2026-10-27_192152/acc-resume-prompt/maestro.log`）：

```
19:23:32  Tap on "^委托给 ACC$", enabled            COMPLETED
19:23:32  Assert that "ACC 会接管后续会话与执行.*" is not visible  COMPLETED  ← 派单成功
19:23:32  Assert that "只回复HISTORYOK" is visible   COMPLETED      ← 卡片出现
19:24:44  Long press on "只回复HISTORYOK"            COMPLETED
19:24:44  Assert that ".*继续跟进.*" is visible       COMPLETED      ← 长按菜单里有「继续跟进」
19:24:52  Tap on ".*继续跟进.*"                      COMPLETED
19:24:52  Assert that "只回复HISTORYOK" is visible   COMPLETED      ← ★ 会话抽屉里读到了**这次派单**的用户消息
19:24:53  Assert that "HISTORYOK" is visible         COMPLETED      ← ★ 读到了智能体的那句答复
19:24:55  Assert that "继续，或改写提示词后发送" is visible  COMPLETED ← ★ 续写区在
19:25:06  Erase 2 characters                        COMPLETED      ← 擦掉预填的「继续」
19:25:12  Input text 只回复RESUMEOK                 COMPLETED      ← ★ **改写**提示词
19:25:17  Tap on "发送"                             COMPLETED
19:25:18  Assert that "已发送.*" is visible          COMPLETED      ← ★ 真的投出去了
19:25:44  Assert that "RESUMEOK" is visible          COMPLETED      ← ★★ 智能体照**改写后**的提示词回答了
```

⇒ 这条流是**原需求最后一个字**（「继续」+「提示词的修改」）的**端到端真机证据**：
从派单 → 长按 → 继续跟进 → **读到那次派单的对话** → 改写提示词 → 发出 →
**看到智能体按新提示词的回答**。

★ 其中「已发送」与「RESUMEOK」是**两个不同的断言**：
  「已发送」只证明**提示词投出去了**；
  「RESUMEOK」才证明**智能体读了改写后的提示词并照着回答**。
  v2 正是红在后者（那时正文还是快照）⇒ 两者缺一不可。

**v1 的取证**（pocketd 日志，18:44:58）：

```
[acchttp] POST /api/v2/runtime/dispatch attempt=1 status=502 duration=5.011460208s
[tasks/delegate] dispatch failed (acc_task=31a09f81-2819-42fa-ba72-07c0095346d2
                runtime=acs-ac30-01 agent=opencode): retries exhausted: HTTP 502
[ERROR] POST /api/tasks/delegate - 502 (12.352658375s)
```

⇒ `acc_create_task` **成功了**、派发**失败了** ⇒ ACC 里多一个**没人跑的孤儿任务**，
而用户只看到「失败」⇒ 这就是 §6 第 13 条。
⇒ **同一时刻直连复测**：`POST /api/tasks/delegate` **0.19 秒成功** ⇒
ACC 的 runtime-control 面是**间歇性** 502，不是产品缺陷。

**v3 的取证**：
- Maestro 工件截图（`~/.maestro/tests/2026-10-27_190627/…/step-011-*.png`）：
  「委托给 ACC」按钮**清晰可见**、没被导航栏盖住；
- 无障碍树里按钮 bounds = `[175,2157][425,2268]`，tap 的落点 (300,2212) **在按钮上**；
- pocketd 侧**没有**这条 delegate 请求（且**没有**建出任务行）⇒
  **点击没变成请求**；
- 同一轮里 `GET /api/acc/agents` **502（30.03 秒）** ⇒ ACC 的智能体清单面此刻也在抖。

⇒ **诚实结论**：v3 的红**还不能归到某一个确定的原因**上。
已排除的：按钮被遮挡（截图反证）、判据恒真（断言本身是"面板关闭"这条负向判据）、
后端报错（没到后端）。
未排除的：点击事件没绑定到处理函数（overlay/手势层吃掉了 touch）、
请求在客户端就没发出去、键盘 inset 与 sheet 定位的交互。
⇒ **本轮不把它写成"已修复"**，作为下一轮的待查项（§7 第 0 条）。

### 4.8.9 ★ 顺带修掉的一个**真产品缺陷**：抽屉的正文不能是快照

v2 的红暴露的是真缺陷：原生会话的答复是**异步**落库的，而抽屉只在
「打开时」「切换 kind 时」重读 ⇒ 用户发完提示词，**看不到**智能体接着回的那句
⇒ 用户会以为没发成功，于是**重发一遍**。

修法：抽屉打开期间**定时重读**（5 秒一次），页面隐藏时停掉（省电 + 免得后台空转）。

⚠ 注意：这条红**留在了工件里**
（`~/.maestro/tests/2026-10-27_185553/maestro.log` 有逐条时间线：
`Tap on "发送" COMPLETED` → `Assert that "已发送.*" is visible COMPLETED`
→ `Assert that "RESUMEOK" is visible FAILED`），
**不是靠改判据绕过去的**。

### 4.8.10 ★ 本轮的**全部**读数（一处集中，便于核对）

**设备侧纪律（§6.1）先过**：设备上那个 APK 的字节数 **165465327**
== 本地刚构建出来的 `app-debug.apk` 的 `stat -f%z` **165465327**，
且 APK 内含本轮新 bundle `assets/public/assets/index-DTgOS7l1.js`。
⇒ 下面所有设备读数**测的就是交付代码**，不是「同字节数的旧内容」。

**四条真机流**（同一台 emulator-5556、agent-companion `a58aa73`）：

| 流 | 覆盖的东西 | 读数 |
|---|---|---|
| `.maestro/acc-resume-prompt.yaml`（本轮新增） | 派单 → 跑完 → 长按 →「继续跟进」⇒ **打开原生会话并读到正文** → 预填「继续」→ **改写** → 发出 ⇒ **智能体照改写后的提示词回答** | ✅ **2/2 Flows Passed in 3m 50s**（19:21~19:25） |
| `.maestro/acc-approval.yaml` | 派单 → 要工具批准 → 手机上答得掉 → 跑完 → 任务进「已结束」区 | ✅ **2/2 Flows Passed in 1m 38s** |
| `.maestro/acc-delegate-stop.yaml` | 派单 → 长按 →「停止任务」→ 确认 → 以「已取消」出现在已结束区 | ✅ **2/2 Flows Passed in 2m 15s** |
| `.maestro/acc-history-files.yaml` | 派单 → 跑完 → 任务历史里看得到 → 展开 → 产出的文件 → 打开看内容 | ✅ **2/2 Flows Passed in 4m 31s**（**重跑过一次**：首跑红在 ACC 的 MCP 面 EOF ⇒ 产品按设计回**诚实 502**） |

**代码侧读数**：

| 命令 | 读数 |
|---|---|
| `cd frontend && node scripts/run-gates.mjs` | **50/50 全过**，103.9 秒 |
| `cd frontend && node scripts/run-mjs-tests.mjs`（连跑三次） | **4732 pass / 0 fail**（三次） |
| `cd backend && POCKET_TEST_POSTGRES_DSN=… go test ./... -count=1` | **67 包全绿，0 FAIL**，`OP_GO_TEST_EXIT=0` |
| `cd backend && go test ./internal/repohygiene/ -count=1` | **绿**（§952 钉数已按其惯例更新：267 / 143 / 113 / 264，**无界处仍是台账那 3 条**） |
| `cd backend && go test ./internal/server/ -run 'TestACCDispatchTask_GetsNativeSessionLinked\|TestTaskSessionMessage_' -count=1` | **6 条全绿** |
| `cd /tmp/ac-e2e && go test ./... -count=1`（agent-companion） | **56 包全绿，0 FAIL**，`AC_GO_TEST_EXIT=0`（本轮该仓**代码未改动**，这是复验） |

**鉴别力自检**（回退实现 ⇒ 判据必须变红）：

| 自检 | 回退 | 读数 |
|---|---|---|
| ① | 去掉「自动挂链」 | ❌ `sessions = 0, want 1` + ❌ `status = 404, want 200` |
| ② | operate 的 kind 只放 body（daemon 读不到） | ❌ `daemon got kind(query) = "", want opencode` |

**活链路 API 级取证**（真 daemon + 真 pocketd，不是单元测试）：

```
POST /api/tasks/delegate            ⇒ dispatch_b62b3000…（0.19s）
GET  /api/v1/runs/{dispatch}         ⇒ state=succeeded, session_id=ses_e83058481…, answer=APIOK
GET  /api/tasks/{id}/sessions        ⇒ 从此前的**恒空**变成返回那条原生会话链接
                                       {sessionId:"ses_e83058481…", kind:"opencode", native:true, instanceId:""}
POST /api/tasks/{id}/sessions/{sid}/message {"prompt":"只回复RESUMEOK"}
                                    ⇒ {"runId":"run-d015a97d-…","state":"running","agentKind":"opencode"}
GET  /api/v1/native/sessions/{sid}/transcript
                                    ⇒ 从 2 条变 4 条：
                                        user      | 只回复APIOK
                                        assistant | APIOK
                                        user      | 只回复RESUMEOK     ← 改写后投进去的
                                        assistant | RESUMEOK          ← 智能体真的照着回答了
```
---

## 5. 环境事故（**不是产品缺陷**，但让本轮多花了不少时间，如实记录）

1. **Docker Desktop 被手动暂停** ⇒ PG「端口在监听但不回包」⇒ 委派 502 / tasksync 失败 /
   邮件调度超时，全部指向同一个原因。判别：`docker desktop status` = paused，
   而 `psql select 1` 超时。⇒ `docker desktop start` 恢复。
2. **`mediaanalysisd` 分析用户自己的视频库** ⇒ 116% CPU + 19 个 I/O 阻塞线程
   ⇒ 宿主 load 30~70 ⇒ 模拟器 QEMU 主循环 18~23 秒拿不到 CPU
   ⇒ **被看门狗判死**（`detected a hanging thread 'QEMU2 main loop'`）
   ⇒ 设备侧读数不可信。⇒ 终止该守护进程（launchd 会按需重启）后磁盘降到 18MB/s。
3. **AOSP 系统 ANR 对话框**（「System UI isn't responding」）盖住整个 App
   ⇒ Maestro 无障碍树里只剩这个对话框 ⇒ flow **第一条**断言就红。
   ⇒ 单独拆了 `.maestro/_dismiss-anr-dialog.yaml`，每条 flow 前先清场。
4. **宿主负载尖峰把 ACP 握手拖过了 ACC 的租约**（21:10 那轮）：
   daemon 21:10:22 开始执行派单，`session/new` 却在 ~37 秒后才发出 →
   ACC 租约（~70s）先到期 ⇒ `lease expired mid-execution` ⇒
   `facade acp session/new failed` ⇒ `error_code=session_create_failed`、结局 unknown。
   **同一时刻单独复测握手：initialize 1.07s / session/new 1.03s** ⇒ 不是产品缺陷，是负载尖峰。
   ⇒ 教训（已写进下一轮提示词）：**设备读数前先看 load1**，并用握手耗时做交叉验证。
5. ★★ **（2026-10-27 02:51~03:20）Docker 的 Linux VM 被 poweroff** ⇒ Postgres 断 ⇒
   后端所有 DB 接口挂掉（登录 15~30 秒、`app_prefs` 500 挂 13 分钟）⇒
   **真机 flow 在 preflight 的登录处就红了**，报错文案是
   「登录失败，请检查网络连接与后端地址后重试」（**那条文案是诚实的** ——
   后端真的挂了）。
   ⇒ 判别与恢复见 §4.5.3（`docker desktop status` = stopped 而 UI 进程还在；
   `docker desktop start` 在这个状态下**没用**，要 quit + pkill backend + open）。
   ⇒ VM 起不来的真因是**宿主内存被打光**（剩 58MB）⇒ 卸载 Ollama 已加载的模型
   （可逆）后 10 秒内恢复。
6. ★★ **（我自己造成的）Docker containerd 元数据库被弄成只读**：
   在 §4.5.3 恢复 Docker 时用了 `pkill -f com.docker.backend` 硬杀后端
   ⇒ containerd 的 `meta.db` 变成只读 ⇒ `docker run` 全部失败
   （`exit status 125`）⇒ `internal/sandbox` 的两条 Docker E2E 判据红。
   ⇒ 已用 `docker desktop stop` + `docker desktop start`（**不再硬杀**）修好，
   `docker run` 恢复（`docker-run-ok`），两仓全量重跑全绿。
   ⇒ **这是我的操作失误，不是产品缺陷**，但它消耗了时间，记在这里。
7. ★★ **（2026-10-27 04:00 起）ACC 完全无响应**：直连连续三次 `HTTP=000 time=5.00s`
   （03:22 时还是 1.56s 的 401）⇒ daemon 注册被 reset ⇒ `command_loop_ready` 起不来
   ⇒ **派单根本到不了 daemon** ⇒ 真机流无法进行（§4.5.4）。
   ⇒ 5 秒这个固定特征与 §2.16 的「续租卡 5.2 秒才 EOF」是同一件事。
   ⇒ ACC 本机**无 SSH** ⇒ 只能等。

---

## 6. 遗留风险（**明确写清楚什么还没做到**）

1. **ACC 侧仍缺两个端点**：
   `GET /api/v2/runtime/permissions`、`POST /api/v2/runtime/permissions/{id}/decision`
   ⇒ agent-companion 的审批转发路径**只能走 daemon 本地干预队列**兜底。
   本机到 ACC（`100.106.67.198:4101` == `192.168.31.211:4101`）**无 SSH** ⇒
   **未实现、未部署、未验证**。不要说成「已打通」。
2. ✅ **ACC 派单的 run 现在进 daemon 的 run 历史了**（本轮关闭，§4.7.1）：
   `GET /api/v1/runs` 对 ACC 派单**不再恒空**，真机上有读数（§4.7.5/§4.7.6）。
   ⚠ 但历史是**内存态**（daemon 重启即失忆，保留期 1 小时）⇒ 需要跨重启的
   历史就得落盘（operations 台账里已有终态行，可以从那里重建）。
3. ✅ **生成文件 / 任务历史现在有读面了**（本轮关闭，§4.7.2/§4.7.4）：
   daemon 侧有清单面，pocketd 侧有三个路由，App 上有「任务历史与产出」卡片，
   真机流跑通。⚠ 但**「生成的文件」目前只有输出文档（output.txt）**——
   智能体在**工作区里创建的那些文件**（这才是用户心里的「生成的文件」）
   **仍然没有读面**：daemon 侧没有把它们登记成产物。
4. **`/api/acc/approvals` 未按 workspace 过滤** ⇒ 多工作区部署时待批项会互相可见。
5. **一次工具调用要人答 2~3 次**：这一轮只保证了「App 能答得完」，没有把它做成
   「一次点击答完同一个工具调用的全部检查」。opencode 的 `always`（allow_always）
   选项在 App 里**没有单独按钮**（UI 只有「允许一次」/「拒绝」，option 由智能体给出）。
6. **写工具没有幂等键**：`acc_create_task` 的入参只有 `{kind,title,description}`
   ⇒ 写路径遇 EOF/超时不自动重试（**留在客户端，风险自担**）。
   ⇒ 本轮只在**夹具层**补了「失败先问 ACC 再决定要不要重试」（§4.4.5），
   **产品层的 `POST /api/tasks/delegate` 仍然是「失败即 502」**
   ⇒ 用户在 ACC 抖动时点「委托 ACC」仍可能看到失败，而且**无法判断任务到底建没建出来**
   ⇒ 这是**产品级待办**（要么让 ACC 支持幂等键，要么 delegate 失败时也回一个
   「可能已建，请到任务列表确认」的诚实结果）。
7. **高负载下智能体侧会被拖到超时**（§3.3）：宿主 load1 > 100 时，
   ACP 握手可能超过 ACC 的租约 ⇒ 派单以 `session_create_failed` 收场。
   daemon 侧目前报的是「agent did not answer session/new within Ns」，
   **没有把「租约先到期了」这件事说清楚** —— 可读性还能再修。
8. **既有失败已修**：`tests/TestNoUnjustifiedPlatformSkips`（agent-companion）
   本轮修掉（§2.10）⇒ agent-companion 的 `go test ./...` 现在**全绿**。
   `internal/acp/partial_outcome_code_test.go` 本身不是 gofmt 干净的
   （与 origin/main 一致，属既有状态，**没有动别人的文件**）。
9. ✅ **两条真机流都已拿到全绿读数**（2026-10-27 05:45 / 05:57，§4.6）：
   flow1（acc-approval）与 flow2（acc-delegate-stop）都是 **2/2 Flows Passed**，
   跑在**新 APK（165459023 字节）** + **agent-companion `9863034`** 上。
   ⇒ 这条遗留**本轮关闭**。
10. ★ **人工审批的等待窗口只有 120 秒**（`AC_PERMISSION_TIMEOUT_MS` 默认 120_000，
    agent-companion `internal/config/config.go`）。
    ⇒ 手机上看到卡片、人还没点完，任务就已经 `permission_denied` 死了
    （22:25 与 22:23 两轮都是这个收场）。
    ⇒ **租约侧已经对齐**（§2.16：租约 TTL 也改成 120 秒，覆盖整个审批窗口，
      并由 `tests/lease_ttl_invariant_test.go` 钉住不变量）；
      **审批窗口本身还没放宽**（仍是 120 秒）；
    放宽前要先确认 ACC 侧派单自身的超时上限（否则窗口比派单还长就没意义）。
    ⇒ 另有一个未解的**交互缺陷**：一次危险工具调用会连发 2~3 个审批请求
      （同一个 toolCallId），而 App 的「允许一次」只答了一轮
      ⇒ 第 2 轮仍要人再点（03:28 那轮的 `request_id=1` 就是它）。
11. **ACC 的 MCP 面在抖**（§4.4.4）：EOF / 超时 / 400 都会出现 ⇒ 委派成功率受影响。
    ACC 侧不可改（本机无 SSH）⇒ 只能靠客户端的韧性 + 诚实报错。
12. ✅ **「继续」与「提示词的修改」已端到端打通**（本轮关闭，§4.8）：
    「▶ 继续跟进」现在**真的**打开这次派单用到的原生会话（抽屉里能看到
    对话正文），**预填「继续」**且**可编辑**后发出去，智能体会照新提示词回答。
    活链路取证（真 daemon）：派单 ⇒ run `state=succeeded/answer=APIOK` ⇒
    `GET /api/tasks/…/sessions` 从**恒空**变成返回那条原生会话链接 ⇒
    `POST /api/tasks/…/sessions/{sid}/message {"prompt":"只回复RESUMEOK"}`
    ⇒ 收据 `runId/state=running` ⇒ 同一会话的 transcript 从 2 条变 4 条
    （`只回复RESUMEOK` → `RESUMEOK`）。
    ⚠ **但有一个窗口限制**（§4.8.7）：解析「这次派单用了哪个原生会话」
    依赖 agent-companion 的 **run 历史**，而它是**内存态**（daemon 重启即失忆、
    保留期 1 小时）⇒ **在派单后 1 小时内**第一次读这个任务，链接就建好并**永久**有效
    （写在 openpocket 自己的库里）；若 daemon 在这期间重启过、或一直没人碰过
    这个任务 ⇒ 链接**再也建不起来**，「继续跟进」退回到「查看详情」。
    ⇒ 要彻底解决，得让 daemon 把「dispatch ⇒ session」**落盘**，
      或给 ACC 的 session ledger 加一个读面。**这一条没解决。**
13. ★★ **`POST /api/tasks/delegate` 失败时会留下**孤儿任务**（第 6 条的实证）。
    2026-10-27 18:44 的真机 flow4 首跑红在「派单面板没关闭」，daemon 侧日志：
    ```
    [acchttp] POST /api/v2/runtime/dispatch attempt=1 status=502 duration=5.011460208s
    [tasks/delegate] dispatch failed (acc_task=31a09f81-2819-42fa-ba72-07c0095346d2
                    runtime=acs-ac30-01 agent=opencode): retries exhausted: HTTP 502
    [ERROR] POST /api/tasks/delegate - 502 (12.352658375s)
    ```
    ⇒ **`acc_create_task` 成功了（acc_task=31a09f81…），派发失败了**
    ⇒ ACC 里多了一个**没人跑的孤儿任务**，而用户只看到一个「失败」。
    ⇒ 这正是第 6 条说的「用户无法判断任务到底建没建出来」，**现在有实证了**。
    ⇒ 同一时刻**直连复测**：`POST /api/tasks/delegate` 0.19 秒成功 ⇒
    ACC 的 runtime-control 面是**间歇性** 502（5.01 秒，与 §4.4.4 的抖动同源），
    不是本轮改动引入的。
    ⇒ 修法仍然是第 6 条说的那条：要么 ACC 支持幂等键，要么 delegate 失败时
      也回一个「可能已建，请到任务列表确认」的**诚实结果**。
14. ★ **「生成的文件」仍然只有输出文档**（第 3 条的延续）：智能体在**工作区里
    创建的文件**（用户心里的「生成的文件」）daemon 侧仍没登记成产物。
15. ★ **queue 状态的任务在 UI 无处渲染**、**ACC 侧同名卡片堆了几十张**
    （ACC 来源任务本地 DELETE 404，只统计不删）、**三套重试策略未合并**、
    **审批窗口仍是 120 秒**、**`/api/acc/approvals` 未按 workspace 过滤**
    —— 全部沿用上轮结论，本轮未动。

---

## 6.1 ★★ 一条**纪律**（本轮踩得最贵，记在这里）

**「构建成功」≠「设备上跑的是它」。**
上一轮把「APK 重建成功（165459013 字节）并已装到 emulator-5556」写进了 handoff，
**而设备上装的是 12:50 的 165458754 字节那个** ⇒ 两轮真机跑都在测**旧代码**，
而结论却写成了「修复已生效」。
⇒ 下一轮凡是「设备侧读数」，**第一条判据必须是**
「设备上那个 APK 的字节数/哈希 == 我刚构建出来的那个」，
再谈功能。判据要当判据测（判据本身也要被测）。

---

## 7. 下一轮提示词（可直接整段复制）

```
请接取最新代码合并到本地，然后提交代码并推送。

上一轮（round55 续五，2026-10-27 18:00~19:1x）把原需求里**最后两个没打通的字**
做成了真的：「继续」与「提示词的修改」。

· 根因是**两处**（第二处比第一处更致命）：
  ① ACC 派单的会话**从来没被链到本地任务行**
     （GET /api/tasks/{id}/sessions 对 ACC 任务恒返回 {"sessions":[]}）。
     数据其实齐：agent-companion 的 run 历史里带着 session_id，原生会话的
     元信息与正文都读得到 ⇒ 缺的是**接线**。
  ② App 的**全局会话视图根本发不了消息**给这类会话
     （stores/session.ts 的 sendPrompt 要求 instance_id，
      而 companion 原生会话没有 instance）
     ⇒ 即便补上链接、把「继续跟进」指到 /sessions/:id，
       用户看到的也只是**一个发不出消息的空会话**。
  ⇒ 修法：任务域的两个出口 ——
     GET  /api/tasks/{id}/sessions            自动挂原生会话链接（幂等、持久）
     POST /api/tasks/{id}/sessions/{sid}/message 投一条**可改写**的提示词
     前端在会话抽屉里加续写区（**预填「继续」且可编辑**）。
  ⇒ 一个必须记住的坑：kind 必须放**查询参数**，不是 body
     （daemon 的 operateNativeSession 只读 c.QueryParam("kind")，
      body 里的 kind 被**静默丢掉**，不报错）。
  ⇒ 还有一个 UI 层的坑：原生会话的答复是**异步**落库的 ⇒ 正文不能是快照
     ⇒ 抽屉要定时重读，否则用户发完看不到回答（会以为没发成功而重发）。
  ⇒ 新增真机流 `.maestro/acc-resume-prompt.yaml`。

这一轮还清掉了两个**此前被挡在后面的既有门禁失败**
（run-gates 遇红即停 ⇒ 前面红，后面根本没跑到）：
  · 第 31 项 check:gofmt（6 个真债文件；逐版本回放确认是既有，不是本轮引入）
  · 第 34 项 check:env-example（POCKET_ACC_DISPATCH_* 两个变量没写进
    .env.example，由 d0807c74 引入）⇒ 现在 **50/50 全过**。

★ 下一轮请接着做（按优先级），并继续用批判式审计的方式：

0. ★★★ **先查这个还没查清的**（§4.8.8 的 v3）：有一轮真机流里，tap **打到了**
   「委托给 ACC」按钮（Maestro 工件的截图确认按钮**清晰可见**、无障碍树里的 bounds
   `[175,2157][425,2268]` 与 tap 落点 (300,2212) **完全吻合**），
   但**请求根本没发出去**（pocketd 侧既没有这条请求、也没有建出任务行）。
   ⇒ 已排除：按钮被遮挡（截图反证）、判据恒真（断言是"面板关闭"这条负向判据）、
     后端报错（根本没到后端）。
   ⇒ 未排除：点击事件没绑定到处理函数（overlay / 手势层吃掉了 touch）、
     请求在客户端就没发出去、软键盘 inset（`--kb-inset`）与 sheet 定位的交互。
   ⇒ **这一条本轮没有定性，不许当成"已修复"**。
   ⇒ 建议手法：真机连上 **CDP**（仓库里已有 `scripts/adb-cdp-eval.ps1`），
     在点击前后读 `document.elementFromPoint(300, 2212)` 与按钮的
     `getBoundingClientRect()`，看点击到底落在谁身上。
1. **把「dispatch ⇒ session」的映射落盘**（§4.8.7 的窗口限制）：
   解析「这次派单用了哪个原生会话」依赖 agent-companion 的 run 历史，
   而它是**内存态**（daemon 重启即失忆、TTL 1 小时）
   ⇒ 只有在派单后 1 小时内第一次读那个任务，链接才建得起来。
   要么让 daemon 把映射写进它自己的 sessionlink store（持久），
   要么给 ACC 的 session ledger 加一个读面。
   ⚠ 坑：ACC canonical task id 与 acc_create_task 返回的 work item id
     **不是同一个 id**（实测本地 acc_task_id=f3776696… 在 ACC canonical
     任务表里查不到）⇒ 对接前先把这两个 id 的对应关系查清楚。
2. **delegate 失败时的诚实报错**（§6 第 13 条，已有实证）：
   acc_create_task 成功 + dispatch 502 ⇒ ACC 里留下一个**没人跑的孤儿任务**，
   而用户只看到失败。要么 ACC 支持幂等键，要么失败时也回
   「可能已建，请到任务列表确认」。
3. **「生成的文件」的写集读面**：现在只有 output.txt（输出文档）；
   智能体在**工作区里创建的文件**（用户心里的「生成的文件」）还没被登记成产物。
4. **queue 状态的任务在 UI 无处渲染**；ACC 侧同名卡片堆了几十张
   （ACC 来源任务本地 DELETE 404，只统计不删）。
5. 审批窗口 120 秒放宽（先确认 ACC 侧派单自身的超时上限）；
   `/api/acc/approvals` 按 workspace 过滤；三套重试策略合并。

★ 纪律（继续沿用，别丢）：
· **设备侧读数的第一条判据是「设备上那个 APK 的字节数 == 我刚构建出来的那个」**
  （本轮已按这条核对：165465327 字节，设备 ls -l 一致，且 APK 内含新 bundle
  index-DTgOS7l1.js）。「构建成功」≠「设备上跑的是它」。
· **加了卡片/改了 UI 就必须重跑全部流**，不要拿上一轮的绿读数顶。
· **改了 CSS 变量定义 ⇒ 视觉变了 ⇒ 设备读数必须重跑**。
· **id: 判据在 Android WebView 上恒假**（不把 data-testid 映射成 view-id）
  ⇒ 只能用**文本 + 空间关系**（rightOf / below）。
· **判据要打在用户能操作的那个对象上**；瞬时 toast 只能等一次；
  判据红了先看 Maestro 工件（截图 + screen-hierarchy/*.json，文本在
  attributes.text），别急着改产品代码。
· **宿主 load1 偏高时 uiautomator dump 会被 SIGKILL** ⇒ 读数前先看 load1。
· **ACC 的 runtime-control 面会间歇性 502（5.01 秒）** ⇒ 派单红先看
  accruntime 日志；同一时刻**直连复测**能区分「抖动」与「真缺陷」
  （本轮 flow4 首跑红就是这个）。
· **「跳过了」≠「通过了」**：每条读数都要能指出它验的是哪一步。
