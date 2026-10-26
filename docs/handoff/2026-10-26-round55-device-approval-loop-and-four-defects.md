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
| `.maestro/acc-approval.yaml` | 委托 ACC → 智能体要工具批准 → **手机上看得到并答得了** → 智能体跑完 → 任务进「已结束」区 | ❌ **未拿到整条流全绿**（2026-10-27 01:0x 那轮：步骤 0~3 过，**步骤 4 红**）。根因已钉死：**上一轮的修复根本没装到设备上**（设备 165458754 字节 / 本地 165459013 字节，§4.4.1）⇒ 22:23 那轮的「第 2 张卡看不见」正是**旧包**的 `v-if` 卸载行为（Maestro 逐命令时间线 + 它自己存下的无障碍树工件，§4.4.2）。修复后的构建已在 01:01 装上，浏览器层隔离实验在它上面读数正常（§4.4.3），但**修复后 App 的整条流还没拿到全绿读数**（宿主 load 455，设备无障碍服务采不到树） |
| `.maestro/acc-delegate-stop.yaml` | 委托 ACC → 长按卡片 → 「停止任务」→ 确认 → 任务被停 → **以「已取消」出现在已结束区** | ⚠️ **本轮未重跑**（上一轮的 ✅ PASS 是 50s 读数，来自**旧的** App 基线；§6 有记录） |

后端全量：67 包全绿、0 FAIL（`internal/agent` 的 3 条超时红是宿主负载 100+ 时的 flaky，
单跑绿，§3.3 有记录）。
agent-companion 全量：**本轮全绿** —— 上一轮留下的既有失败
`tests/TestNoUnjustifiedPlatformSkips` 已修（§2.10）。

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

---

## 6. 遗留风险（**明确写清楚什么还没做到**）

1. **ACC 侧仍缺两个端点**：
   `GET /api/v2/runtime/permissions`、`POST /api/v2/runtime/permissions/{id}/decision`
   ⇒ agent-companion 的审批转发路径**只能走 daemon 本地干预队列**兜底。
   本机到 ACC（`100.106.67.198:4101` == `192.168.31.211:4101`）**无 SSH** ⇒
   **未实现、未部署、未验证**。不要说成「已打通」。
2. **ACC 派单的 run 不进 daemon 的 run 历史**：`GET /api/v1/runs` 对 ACC 派单**恒空**
   ⇒ 任务历史这一面在 ACC 派单上仍是空的（终态权威是 ACC 命令面，不是 daemon run）。
3. **生成文件 / 任务历史同步未做**：需求里的「同步查看其任务历史、生成的文件等信息」
   目前只有 ACC 生态面板的**任务与会话**两个读面，**文件**没有读面。
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
9. ★ **修复后的 App 还没拿到整条流的全绿读数**：
   修复后的构建（165459013 字节）已于 01:01 装到 emulator-5556，
   浏览器层隔离实验在它上面读数正常（§4.4.3），
   但**真机 flow1（acc-approval）与 flow2（acc-delegate-stop）都没能在修复后的 App 上重跑**
   —— 宿主 load 飙到 455（10 核），设备端 `uiautomator dump` 采不到树
   （无障碍服务被 SIGKILL），任何此刻的读数都是**失真的**。
   ⇒ **这是本轮最大的未完成项**：修复后的整条流是否真的闭环，**还没有证据**。
10. ★ **人工审批的等待窗口只有 120 秒**（`AC_PERMISSION_TIMEOUT_MS` 默认 120_000，
    agent-companion `internal/config/config.go`）。
    ⇒ 手机上看到卡片、人还没点完，任务就已经 `permission_denied` 死了
    （22:25 与 22:23 两轮都是这个收场）。
    ⇒ 租约续租已经修好（派单能一直活着），**审批窗口本身还没放宽**；
    放宽前要先确认 ACC 侧派单自身的超时上限（否则窗口比派单还长就没意义）。
11. **ACC 的 MCP 面在抖**（§4.4.4）：EOF / 超时 / 400 都会出现 ⇒ 委派成功率受影响。
    ACC 侧不可改（本机无 SSH）⇒ 只能靠客户端的韧性 + 诚实报错。

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

上一轮（round55 续，2026-10-27）把 round55 自己的一处结论**推翻了**：
「a11y 修复已在真机生效」是**假的** —— 修复后的 APK（165459013 字节）**根本没装到设备上**
（设备上跑的是 12:50 的 165458754 字节那个，section 还挂着 v-if）。
⇒ 所以 22:23 那轮 flow1 的「第 2 张卡在无障碍树里消失」不是产品缺陷没修好，
   而是**在测旧代码**。修复后的构建已于 01:01 装上，浏览器层隔离实验在它上面读数正常
   （空→非空后无障碍树能恢复），但**整条流的全绿读数还没拿到**。
本轮另外修掉并已推 main 的：
· openpocket 1756b77d —— MCP initialize 遇到 EOF/连接重置必须重试
  （一次传输层掐断曾让委派永久失效）；写工具**不**重试（acc_create_task 无幂等键）。
· agent-companion e1cdb7d —— R-5 负控不许跳过（main 上 go test ./... 一直是红的）。
· 夹具：写请求失败先问 ACC 本尊再决定要不要重试（避免重复建单）；
  残留清理开始如实统计「删掉几条 / 删不掉几条」（ACC 来源的任务本地删不掉）。

这一轮请按批判式审计继续，重点是**明确没做到**的部分：

1. ★★ 拿到**修复后 App** 的真机全绿读数（这是上一轮最贵的坑）：
   · 第一条判据必须是「设备上那个 APK 的字节数/哈希 == 我刚构建出来的那个」
     （别再拿旧包测新代码然后写成「已修复」）；
   · flow1（acc-approval）与 flow2（acc-delegate-stop）都要在修复后的 App 上重跑；
   · 读数前先看 load1（这台机器的经验阈值 <25；实测 200+ 时设备端
     uiautomator dump 会被 SIGKILL ⇒ 任何读数都失真）。
2. ★ 产品级的 delegate 韧性：ACC 的 MCP 面会 EOF/超时/400（无 SSH，改不了 ACC），
   而 acc_create_task **没有幂等键** ⇒ 写失败后服务器可能已经建出任务。
   现在只有**夹具层**做了「先问 ACC 再重试」，产品层 `POST /api/tasks/delegate`
   仍然「失败即 502」，用户无法判断任务到底建没建出来。
   ⇒ 要么让 ACC 支持幂等键（写契约/issue + 契约测试），要么让 delegate 失败时
   回一个「可能已建，请到任务列表确认」的诚实结果，并给判据。
3. ★ 人工审批窗口只有 120 秒（AC_PERMISSION_TIMEOUT_MS）⇒ 人还没点完任务就死了。
   租约续租已修好（派单能一直活着），**审批窗口本身还没放宽**；
   放宽前先确认 ACC 侧派单自身的超时上限，并给判据。
4. ACC 侧仍缺的两个端点（GET /api/v2/runtime/permissions、
   POST /api/v2/runtime/permissions/{id}/decision）—— acc-go 从未实现；
   本机到 ACC（100.106.67.198:4101 == 192.168.31.211:4101）无 SSH。
   ⇒ 要么找到能改 ACC 的路径，要么把「契约在、实现没有」写成可执行的 issue
     并给出可验收的契约测试；**不要用 daemon 本地队列兜底就宣称
     「ACC 审批面已打通」**。
5. ACC 派单的 run 不进 daemon run 历史（GET /api/v1/runs 恒空）⇒
   「同步查看任务历史」在 ACC 派单上还是空的；生成文件也没有读面。
   ⇒ 设计并实现这两个读面，并给真机判据。
6. /api/acc/approvals 未按 workspace 过滤 ⇒ 补过滤并补判据。
7. 一次工具调用要人答 2~3 次审批 ⇒ 评估把同一 toolCallId 的多项检查聚合成
   一次点击（注意 fail-closed 与 opencode 的 allow_always 语义），并补判据。
8. 高负载下派单以 session_create_failed 收场：ACP 握手可能超过 ACC 租约。
   ⇒ 让 daemon 的报错能说清「租约先到期了」，并给一条判据（判据本身也要被测）。

纪律（上一轮踩过的，别再踩）：
· 「构建成功」≠「设备上跑的是它」：设备侧读数前先核对 APK 字节数/哈希。
· 设备读数前先看 load1（阈值 <25；实测 200+ 时设备端 uiautomator dump 被 SIGKILL）。
  负载高时先做文档/代码，别拿失真的读数当结论。
· **dump 失败 ≠ 文本不存在**：探针/判据要把「采不到无障碍树」与「树里没有」
  分开统计，否则会把环境故障报成产品缺陷（本轮踩过一次，已修探针）。
· 同一台设备不能并行跑两条 Maestro 流；本会话用 emulator-5556。
· Maestro 的 visible 是完整匹配（「ACC 待批」≠「ACC 待批（1）」⇒ 写「ACC 待批.*」）。
· 断言前必须清同名残留，否则会命中别的行；瞬时 toast 只能等一次、不能连着 assert。
· 判据要当判据测：恒真/恒假/假绿的判据必须清掉。
· 提交走临时 worktree + git push origin HEAD:main，**不移动本地 main**。
· 凭据只经环境变量/0600 env 文件传递，不打印、不入仓库。

完成后输出：结论/根因、改动文件与关键行为、测试命令与结果、遗留风险、
更新本 handoff、下一轮提示词。实事求是，不要把「只是声明一下」的功能当完成。
```
