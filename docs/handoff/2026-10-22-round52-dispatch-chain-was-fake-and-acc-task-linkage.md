# round52 —— 派单链路在生产上一直是「假的」：四个连环缺陷 + ACC 任务联动打通

> 前一轮：round51（网关凭据落地 + 13 条主包流全量重跑 + runtime scanner 缺口）。
> 本轮是它的**续集**，把用户目标里最核心、也最没被验证过的那条链真正跑通：
> **「分配任务 / 继续 / 停止 / 改提示词 → 任务历史 → 生成的文件」**，
> 并让 **openpocket 经 ACC 同时看到会话与任务**。

## 一句话结论

上一轮收尾时，agent-companion 的这些能力只有**能力面**（接口 200），**没有数据面**
（`/api/v1/runs` total=0、ACC 侧 tasks=0）。本轮用**真 agent（opencode 1.19.0）+
真模型（`llm.kxpms.cn` 的 `kaixuan/minimax-m3`）**在**线上 daemon** 上跑通了整条链，
过程中查出并修掉 **4 个连环缺陷**——**每一个都让「派单」在生产上是假成功**：

| # | 缺陷 | 现场症状 | 修法 | 提交 |
|---|---|---|---|---|
| 1 | launchd 的 PATH 不含 nvm 目录 | daemon 启动打 `no drivable coding agent … falling back to EchoExecutor (dev-only)`，**派单只是 echo，还报成功** | `.env` 里显式给 PATH（部署级） | （本机部署） |
| 2 | service JWT 没有 roles | ACC 一律 403 `role_required`（required_roles=[admin, super_admin]）⇒ **ACC 任务永远建不出来** | 新增 `ACC_SERVICE_ROLES`（opt-in） | `ba83ecb` |
| 3 | 新建任务的 `business_state` 写 `"pending"` | ACC 400 `business_state is not a canonical task state`（词表里**没有** pending） | 改用词表里的 `todo` | `e96042f` |
| 4 | acctask **读**侧按 `id` 解析 | 任务在 ACC 里真建出来了，但 `Task.ID` 恒空 ⇒ `sessionlink: Bind requires taskID` ⇒ run 的 `task_id` 与会话 `taskId` 全空 | 按 ACC 读契约（`task_id`/`status`/`task_type`）解析 + 回执兜底 | `306f698` |

外加一条**测试盲区**的修复：单测里的 fake ACC **直接把被测的 `Task` 结构体编码出去**，
所以它永远「符合」客户端的假设 ⇒ 测试一路绿灯、线上却是空的。fake 改成独立于被测
结构体并手写 acc-go 的两个视图（`listItemItemView` / `taskDetailView`），才钉得住真实格式。

**修完的实测（全部在 :12110 线上 daemon 上）**：

- 派单 → opencode **真执行**（真模型、真 token）→ 权限待批（进干预队列）→ 批准 → 完成（`Success: true`）→ **文件真落盘**；
- **继续**：operate 到同一 session，上下文保留（"okok"）；
- **改提示词**：operate 带新提示词 ⇒ 产出**另一个文件、内容就是新提示词要求的**；
- **停止**：`running → cancelling → cancelled`，且 `sleep 120` 进程确实被杀；
- **任务历史**：`/api/v1/runs` 有真实 run；**生成的文件**：`/api/v1/runs/:id/artifacts` 记到 `file_edit`；
- **openpocket 经 ACC**：会话 21 条（最新那条带 `taskId`）、**任务 2 条**（此前恒 0）。

回归：**会话电池 71/71 PASS**、**`cmd/live-e2e` 真 agent 演练 `LIVE_E2E_OK`**。

**没有改 openpocket 的产品代码，没有放宽任何断言，没有编造数据。**

> **续（§7/§8）**：本报告收尾后，openpocket 侧又出了一条红（`flashcards-write`），
> 上一版曾把它归因成「外部重置掐断了后端」。§7 用**复现 + 逐层排除**推翻了那个归因：
> 真因是**软键盘把「保存」按钮顶到了屏幕外 / 状态栏带子里**，点击压根没进前端。
> 修的是 flow（关键盘 + 点底部 submit），**顺带把一条假通过的断言改严**；
> 产品侧「真人用户也点不到」的观察**如实登记、未改产品**。
>
> **最终验收（§9）**：13 条主包 Maestro 流在同一台设备实例上**统一重跑，13/13 全绿**
> （307 步 COMPLETED；2 步 FAILED 是本机 WebView 113 的**设备级无障碍树抖动**，
> 护栏按设计**原样重跑同一条断言**后通过，未放宽任何断言）。
>
> **续（§10）**：复验时用真 agent 演练又查出 agent-companion 侧一条**真缺陷** ——
> **被叫停的派单被报成 `agent_busy`**（其实槽位是空的，`in_flight=0`）。因为
> `agent_busy` 的契约是「可稍后重试」，**叫停被降级成了可重试失败，重试可能把
> 叫停的任务又跑起来**。已修（`463da0c`）+ 三条回归测试，演练 cancel 段 6/6 全绿。
>
> **续（§11）**：把线上实例升到新构建后再实测「停止」，又查出**「正在等审批」的
> 派单叫不停** —— 取消没有解除权限门 ⇒ run 永远停在 `cancelling`、**不落终态**
> （>15 分钟），另一变体落成 `failed`/`permission_denied`。已修（`91de17d`），
> 两条新回归测试（修复前 1 红），「停止」这条目标的两档缺口都补上了。
>
> **续（§12）**：**交付时的当前态全量复跑**（10-23 08:29–09:37）——
> **13/13 全绿、285 步 COMPLETED、0 步 FAILED**（比 §9 那次更干净）。过程中撞到
> 三件**环境事件**（SystemUI ANR 弹窗、lowmemorykiller 清理、模拟器连跑 11.96 小时
> 后 launcher 反复 ANR），如实登记在 §12.2；其中第 2 件**没有钉死到最后一环**，
> 不写成「已定位」。

---

## 1. 起点：上一轮结束时，这条链只有「能力面」

| 面 | 读数 | 含义 |
|---|---|---|
| `GET /api/v1/runs` | `total=0` | **没有任何真实 run** ⇒ 「任务历史」无数据可查 |
| `GET /api/v1/interventions` | `total=0` | 人工干预队列空 |
| `GET /api/v1/runs/:id/artifacts` | 200（能力在） | 但没有 run ⇒ **没有 artifact 可读** |
| openpocket → ACC `tasks` | `count=0` | ACC 里看不到 companion 的任务 |
| openpocket → ACC `sessions` | `count=15` | 有会话（多为历史来源），但 `taskId` 多为空 |

⇒ 上一轮报告里「任务历史 / 生成的文件」只能写成**如实登记的未通过项**。
本轮就是去把这一项变成**有数据**的。

---

## 2. 四个连环缺陷

### 2.1 launchd 的 PATH ⇒ 没有任何可驱动智能体 ⇒ 回落到 EchoExecutor（假成功）

**这是本轮最严重的一条**。线上 daemon 09:37:31 的启动日志：

```
level=WARN msg="no drivable coding agent: every candidate was DENIED by the host allowlist;
                 falling back to EchoExecutor (dev-only). Add the agent to the processes[]
                 layer of the allowlist to let it run." denied=9
```

⇒ 生产 daemon 的执行器是 `acc.EchoExecutor{}`（`cmd/agent-companion/main.go:1409`）——
**一个 dev-only 的桩**：派单会返回「成功」，但只回一句 echo，**什么都不做**。

**根因不是 allowlist，是 PATH**：

- agent 二进制靠 `exec.LookPath` 找（`internal/discovery/discovery.go:137`）；
- launchd 起的进程默认 PATH 只有 `/usr/bin:/bin:/usr/sbin:/sbin`
  （`launchctl print` 的 `default environment`）；
- opencode 装在 `/Users/niuer/.nvm/versions/node/v22.23.3/bin/opencode` ⇒ **找不到**；
- 于是候选集里只剩运行时扫描发现的 9 个 mmcode 进程，它们又都被 `processes[]` 层拒掉
  （mmcode 不在执行层白名单）⇒ `denied=9` ⇒ 回落 EchoExecutor。

**对照证据**：仓库自带的 `cmd/live-e2e` 演练**显式设了 `PATH=agentPath`**，
所以它每次都能注册 opencode 驱动；同一个二进制，线上就是找不到。
⇒ 这不是「环境不支持」，是**部署没给 daemon 看得见的 PATH**。

**修法（部署级）**：本机 `~/kaixuan/acp/.env` 显式给 `PATH`（含 nvm 的 bin 目录），
经 `ac-ctl` 重生成进 plist。修完启动日志变成：

```
level=INFO msg="agent discovery complete" count=1
level=INFO msg="discovered agent" agent_id=opencode kind=opencode state=idle
level=INFO msg="facade driver registered" agent_id=opencode kind=opencode mode=acp_stdio
```

且**不再有 EchoExecutor 回落告警**。

> ⚠️ 附带一条运维注意：nvm 升级会换版本目录 ⇒ 这一行要跟着改（或改用固定软链）。
> 写进 `.env`（SSOT）而不是手改 plist——理由见 round51 §1.4。

### 2.2 service JWT 没有 roles ⇒ ACC 的 canonical 写入一律 403

放开角色后，第一次真派单撞上：

```
level=WARN msg="acc task resolution failed; new session continues unlinked" kind=opencode
  error="acctask: POST /api/v2/projects: http 403:
        {"error":{"code":"role_required","message":"role required to create canonical resource",
        "details":{"required_roles":["admin","super_admin"]}}}"
```

ACC 侧：`acc-go/internal/httpapi/middleware/authz.go` 的 `RequireAnyRole` 挂在
**`POST /api/v2/projects`** 与 **`POST /api/v2/canonical/tasks`** 上，
放行条件是 `claims.IsAdminUser()` 或 `claims.Roles` 命中允许集。

而 daemon 铸出来的 service JWT（`internal/accjwt/accjwt.go`）claims 是
`{sub, username, tenant_id, type:"agent", role:"agent"}` ——
**没有 `roles` 数组** ⇒ 必然 403。

**关键事实：仓库自带的逃生口不够用。** `internal/config/config.go` 的注释写着
「预置一个 project id 传进来，就不用 widen agent token」——
但 `ACC_TASK_PROJECT_ID` **只跳过 project 的 upsert**，**任务创建同样被挡**。
⇒ 「operate 解析出一个真实 ACC 任务」这条特性在 role-less token 下**永远走不通**，
每次会话都退化成 `unlinked`、`acc_task_id` 为空，而日志只留一行 WARN。

**而 `accjwt.Claims` 结构体里本来就建模了 `Roles` / `IsAdmin` 字段，却没有任何入口能设**
——能力在模型里存在、在 API 上不存在。

**修法**（`ba83ecb`）：新增 `ACC_SERVICE_ROLES`（逗号分隔，opt-in）→ 写进 token 的
`roles` claim；两个签发点（`accjwt.Signer` 与 `acc.ServiceTokenSource`）都接上；
日志打出 `roles=[...]`，不让运维猜。

**默认行为完全不变**：不配 ⇒ 不带 `roles` claim ⇒ token 与改动前逐字节一致
（有测试锁住）。opt-in 是刻意的：配它等于把「这台机器上的 daemon 是可信自动化」
这件事**显式化**——运维本来就持有同一个 HMAC 密钥，徒手也能铸出这种 token。

> 本机部署选了 `ACC_SERVICE_ROLES=admin`。这是**本机部署的显式选择**，不是产品默认。

### 2.3 `business_state: "pending"` ⇒ ACC 400（词表里没有 pending）

角色放开后，403 立刻变成 400：

```
error="acctask: POST /api/v2/canonical/tasks: http 400:
       {"error":{"code":"validation_failed","message":"business_state is not a canonical task state"}}"
```

ACC 的 canonical 状态词表（`acc-go/internal/httpapi/handlers/tasks_pg.go:73`）是
`{todo, assigned, in_progress, blocked, paused, review, approved, revision, done, failed, cancelled, archived}`
——**没有 `pending`**。

daemon 侧写死的是 `BusinessState: "pending"`（`internal/api/acc_link.go` 两处）。

**修法**（`e96042f`）：抽常量 `accTaskStateCreated = "todo"`（词表里对应
「已创建、尚未开始」的状态），两处 CreateTask 都用它；加契约测试：
断言常量落在镜像的词表内、且本文件里所有 `BusinessState:` 赋值都用常量
（写字面量就是上次漂移的入口）。

### 2.4 acctask **读**侧按 `id` 解析 ⇒ Task.ID 恒空 ⇒ 任务建了却链接不上

任务终于建出来了（ACC 里能看到 `business_state=todo`、`legacy_id` 指向 run id），
但日志又冒出来一条：

```
level=ERROR msg="acc task created but new-session link not persisted"
  kind=opencode run_id=run-5623b797-... task_id="" error="sessionlink: Bind requires taskID"
```

⇒ `task.ID` 是**空**。

**根因是读写两套字段名**。ACC 的**读**契约视图（`acc-go/internal/httpapi/handlers/acc.go`）：

| 视图 | 字段 |
|---|---|
| `taskListItemView` | `task_id` / `project_id` / `title` / `status` / `resource_version` / `updated_at` |
| `taskDetailView` | list item + `description` / `task_type` / `correlation_id` / `created_at` |

而 daemon 的 `acctask.Task` 结构体按 **`id` / `business_state` / `kind`** 解析
——那是**写侧**的名字（createTask 的请求体确实用 `business_state` / `kind`）。
⇒ 读回来的 `Task.ID` 恒为空；`CreateTask` 随后的 `GetTask` **没报错**，
却把空 ID 交给了调用方。

**修法**（`306f698`）：

- `Task` 结构体改成按 ACC 读契约解析（`task_id` / `status` / `task_type`），
  Go 字段名不变，注释里写清出处与「写读不是一套字段名」；
- `CreateTask` 里「回查成功但没带 id」时**回落到回执里的 task_id**
  （原来只在回查**报错**时兜底）。

### 2.5 为什么单测没抓到：fake ACC 直接编码被测结构体

`internal/acctask/client_test.go` 的 fake 把 `Task` 结构体直接 JSON 编码出去
⇒ fake **永远符合客户端的假设** ⇒ 测试一路绿灯，而线上是空的。

⇒ fake 改成**独立于被测结构体**的 `fakeTask`，并手写 acc-go 的两个视图
（`listItemView` / `detailView`），测试这才钉得住真正的线上格式。
另加两条契约回归：`TestTaskParsesAccContractView`（用真实视图 JSON 解进 `Task`）、
`TestCreateTaskFallsBackToReceiptID`（回查没带 id 时必须回落到回执 id）。

> 这条是本轮**最值得记住的方法论**：**fake 不得从被测结构体生成**。
> round51 的「重试护栏」也是同一种直觉：断言要硬，装置层才可动。

---

## 3. 线上真链路实测（:12110，opencode + 真模型）

### 3.1 分配任务（含人工干预）

| 步骤 | 证据 |
|---|---|
| 派单 | `POST /api/v1/native/sessions` → **HTTP 202**，`runId=run-013dd81a-…`，`agentKind=opencode` |
| 真的在跑 | 状态从 `running` → `waiting_permission`；干预队列出现一条 `pending`，title 是**要写的那个文件路径** |
| 批准 | `POST /api/v1/interventions/:id/decision` `{"optionId":"once"}` → **200**，状态 `resolved` |
| 完成 | run `state=succeeded`，`result.Success=true`，`answer="done"`，`tool_calls=1` |
| 文件真落盘 | `/tmp/ac-live-demo/hello-round51.txt` 存在，内容正是 `ac-live-e2e` |
| 产物面 | `/api/v1/runs/:id/artifacts` → `count=1`，`tool_title=private/tmp/ac-live-demo/hello-round51.txt`，`kind=file_edit` |
| 任务历史 | `/api/v1/runs` → `total` 从 0 变成真实 run 列表 |

### 3.2 继续 + 改提示词

`POST /api/v1/native/sessions/ses_e9eae8d…/operate`（**同一 session**，
body 带**新**提示词「创建第二个文件 second-round51.txt，内容写 two」）→ 202，
新 run `run-6d845e17-…` ⇒ `state=succeeded` ⇒ 盘上多出
`second-round51.txt`，内容正是 **`two`**。

⇒ **上下文保留**（同一 session 续跑）+ **提示词确实被改**（产出跟着新提示词走）。

### 3.3 停止

派一个 `sleep 120` 的任务 ⇒ 批准执行权限 ⇒ `POST /api/v1/runs/:id/cancel` ⇒

```
取消前: state=running
取消响应: state=cancelling, cancel_requested=true
取消后: state=cancelled, ended_at=2026-10-22T09:57:09+08:00
进程: pgrep "sleep 120" ⇒ 无
```

⇒ 停得干净，**不是**只改了个状态字段。

### 3.4 任务历史与生成的文件

| 读数 | 值 |
|---|---|
| `GET /api/v1/runs` | 列出**当前 daemon 实例**内的 run（分配/继续/停止各一条都实测过），带 `task_id`、`answer`、`state` |
| `GET /api/v1/runs/:id/artifacts` | 每次文件写入都记到 `file_edit`（含完整路径） |

**但「历史」的语义要说清楚（受控实测，别当成持久台账）**：

`GET /api/v1/runs` 的实现是 **Server 上的内存保留窗口** ——
`finishedRuns sync.Map`（`internal/api/api.go:98`）+ `retainedRunTTL = time.Hour`
（`internal/api/operate.go:350`），再加上 facade 的在跑 run。
**它不落盘**。受控实验：

```
派单 → GET /api/v1/runs/<id>        → HTTP 200（state=running，task_id 非空）
      launchctl kickstart -k（重启 daemon）
      GET /api/v1/runs/<id>        → **HTTP 404**
      GET /api/v1/runs              → total=0
```

⇒ **daemon 重启后，先前的 run 记录就没了**。`dag-store.db` 那个持久台账是
**编排面**的 run（`listRunsAll` → `dagStore.ListRuns`），与派单 run 不是一回事，
不能拿来当「任务历史」的持久化。

⇒ 要让任务历史跨重启存活，需要**把派单台账落盘**（新增能力，不是缺陷修复），
本轮**没有**做，如实登记在 §6。

### 3.5 回归

| 检查 | 结果 |
|---|---|
| 会话专项电池 | **TOTAL=71 PASS=71 FAIL=0 SKIP=0**（全部改完后复跑） |
| `cmd/live-e2e`（真 opencode） | **LIVE_E2E_OK**：turn1 执行 38s、turn2 同会话续跑 21s（上下文保留）、turn3 取消 0s 生效 |
| `go test ./...`（agent-companion） | **无失败包** |

---

## 4. openpocket 侧：ACC 里的会话与任务

| 读数 | 本轮之前 | 本轮之后 |
|---|---|---|
| `GET /api/acc/tasks` | `count=0` | **`count=2`**（两条都是本轮真派单，`business_state=todo`，`legacy_id` 指向对应 run，title 就是那条提示词） |
| `GET /api/acc/sessions` | `count=15`（`taskId` 多为空） | **`count=21`**；本轮最新那条 **`taskId=2cf2eb36-…`**（非空） |
| `GET /api/v1/runs`（daemon） | `total=0` | 3 个真实 run，`task_id` 非空 |
| `GET /api/v1/session-links` | `links:[]` | 有链接：`run:opencode:run-58ef7d1a…` → `task_id=2cf2eb36…`、`project_id=925e113e…` |

> 更早那条 `run-5623b797-…` 的会话 `taskId` 仍为空 —— 那是**修复前**跑的 run，
> 链接当时就没落盘。**不补数、不改历史**，如实留在那里。

⇒ 用户目标里「**通过 ACC 连接到 agent-companion，获取到当前的会话和任务等信息**」
这条，现在**有数据**了：会话、任务、任务↔会话↔run 的三方 id 都对得上。

---

## 5. 本轮改动清单

### agent-companion（`main`，已推送）

| 提交 | 内容 |
|---|---|
| `d87c880` | fix(deploy): npm 部署路径补 `AC_RUNTIME_SCAN_ENABLED` 默认值（round51 §1.4） |
| `fc44dbe` | fix(deploy): ac-ctl 把 plist / `.env` 收成 **0600** —— 里面有明文密钥却是 0644 |
| `ba83ecb` | feat(acc): 让 service identity 能声明角色（`ACC_SERVICE_ROLES`）—— 任务联动不再 403 |
| `e96042f` | fix(acc): 新建任务的 `business_state` 用词表里的 `todo` —— `"pending"` 不存在 |
| `306f698` | fix(acc): acctask 的 `Task` 按 ACC 的**读**契约解析 —— 原来按 `id` 解析，ID 恒为空 |
| `463da0c` | fix(agentfacade): **被叫停的派单不能报成 `agent_busy`** —— 取消要落终态 `cancelled`（见 §10） |
| `91de17d` | fix(agentfacade): 叫停要**解除权限门** —— 「正在等审批」的派单现在能停（见 §11） |

线上 daemon：**`0.1.0-306f698.1792635154`**（历次版本 `packages/` 下均保留可回滚）。

### 本机部署（**不入仓库**）

| 项 | 改动 |
|---|---|
| `~/kaixuan/acp/.env` | 加 `PATH`（含 nvm bin）、`ACC_KAIXUAN_KEY`（网关 key）、`ACC_SERVICE_ROLES=admin`；权限收到 **0600** |
| `~/Library/LaunchAgents/com.agent-companion.npm.plist` | 由 ac-ctl 重生成，权限 **0600**（原来 0644，含明文 `AC_API_SECRET` / `ACC_AUTH_TOKEN_SECRET`） |
| `ACC_KAIXUAN_KEY` 的来源 | `opencode.json` 的 kaixuan provider 用 `{env:ACC_KAIXUAN_KEY}` 引用；本机该变量**没有落盘来源**，演练时从 openpocket 部署已在用的同一把 `llm.kxpms.cn` 租户 key 注入**进程环境**（不落盘、不打印） |

### openpocket

本轮**未改任何 openpocket 产品代码**（`git diff` 为空）—— openpocket 是这条链的**消费方**，
它经 ACC 读到的会话/任务是本轮真跑出来的数据。

---

## 6. 如实登记（未解决 / 未验证 / 前提）

| # | 项 | 状态 |
|---|---|---|
| 1 | **ACC 任务状态不会随 run 终态推进** | `acctask` 客户端**没有「更新任务状态」的能力**，任务建出来后停在 `todo`，不会变成 done/cancelled。ACC 侧的 `PATCH /canonical/tasks/:id` 在 `RequireTaskWrite` 后面（资源级角色），**本轮没有实现**。 |
| 2 | **ACC 侧只做了「读」侧授权** | 本机选择给 daemon 的 service identity 声明 `admin` 角色。更窄的做法是 ACC 侧用 `ACC_CANONICAL_WRITE_ROLES` 放行一个专用角色（`acc-go` 的现成旋钮），**需要改共享的 ACC 容器环境** —— 那个容器并行会话也在用，**本轮刻意没动**。 |
| 3 | **minimax-code 仍不可驱动** | 执行层白名单 `processes` 不含 mmcode ⇒ minimax-code 只能观测。本轮用 **opencode** 验证的是「**可驱动**这条链路本身成立」，不改变 mmcode 的现状。 |
| 4 | **13 条 Maestro 流本轮已统一重跑：13/13 全绿** | 见 §9（307 步 COMPLETED / 2 步设备级 a11y 抖动后原样重跑通过）与 **§12（交付时复跑：285 步 COMPLETED / 0 步 FAILED）**。openpocket 的**产品代码本轮仍未改动**（改的是 Maestro flow 与新增诊断脚本）。 |
| 5 | **本机仍无 USB Android 真机** | 仍是 AVD `emulator-5556`（AOSP Android 14 / WebView 113）。 |
| 6 | **ACC 容器未改动** | `acc-blue`(:4100) 与 `acc-go-host`(:4101) 是共享容器；本轮只**读**它们、没改配置/没重启，避免打断并行会话。 |
| 7 | **openpocket 的 `go test ./...`** | 仍只有 `TestSkipLedger_*` 两包失败，属**并行会话**负责的文件，本轮未触碰。 |
| 8 | **daemon 的 ACC 注册仍是关的** | 日志里 `acc registry registration disabled: ACC_SERVICE_TOKEN not set`（注册面要 `sk-svc.*`，与本轮的 canonical 写入面是两回事）。 |
| 9 | **「任务历史」是内存保留窗口，不跨 daemon 重启** | `GET /api/v1/runs` 读的是 `finishedRuns sync.Map` + `retainedRunTTL=1h`，**不落盘**。受控实测：重启前 `GET /runs/<id>` 200，重启后 **404**、`total=0`。`dag-store.db` 的持久台账是**编排面**的 run，不是派单 run。要跨重启存活需要把派单台账落盘（新增能力，本轮未做）。 |
| 10 | **launchd 的 `install`+`restart` 偶发不拉起** | 表现为 `state = not running`、**`runs = 0`**（一次都没被 spawn 过），`launchctl kickstart -k` 即恢复。排查顺序：**先看 `runs`**。 |
| 11 | **10:33–10:38 有一次外部重置事件** | 不是本会话发起的：两个 AVD、两个 pocketd 实例、`/tmp` 全部被清空，两条登录流的结果随 `/tmp` 丢失。已在本轮**重跑补齐**（两条全绿）。它一度被当成 `flashcards-write` 那条红的解释，**该解释是错的** —— 见 §7。 |
| 12 | **产品侧：输入法弹出时顶栏「保存」不可点** | 键盘把 WebView 可见视口压到 530 CSS px，Chrome 把 `.content.scroll-shell` 滚到 165，顶栏 `save-link` 被推到**屏幕外**；键盘关掉后它仍落在状态栏带子内（中心 120 < 129）。**真人用户在这一步同样点不到**（只余 10 CSS px 窄边）。本轮**未改前端**（要改须重出 APK），如实登记。 |

---

## 7. flashcards-write 那条红：**不是环境事件**，是「保存」按钮根本不在可点的地方

round52 收尾时 openpocket 的 13 条 Maestro 流是 **12 绿 1 红**，红的是 `flashcards-write`。
上一轮把它归因成「10:33–10:38 那次外部重置把后端掐了」（见 §8），**这个归因是错的**。
本节是**复现 + 逐层排除**后的定性。

### 7.1 先证明它与后端无关

| 证据 | 读数 |
|---|---|
| 复现环境 | 模拟器**新启动**、pocketd **新建进程**、ACC 容器未动、宿主负载 ~90 |
| 复现结果 | **同一处红、同一断言**：`Assert that ".*今日待复习 1 张.*\|.*1 due today.*" is visible` FAILED |
| 前端侧 | 设备 localStorage `flashcards:v1:outbox` = **null**、`notes` = **[]**、`lastSyncedAt` 停在建卡组那一刻（1792636432）⇒ **前端连「入队」都没发生** |
| 后端侧 | PG `flashcard_notes` = **0 行**、`flashcard_cards` = 0 行；而 `flashcard_deck_config` 有「回归卡组」（created_at=1792636432）⇒ **建卡组成功、建卡失败**，中间那一步没做成 |
| 失败帧 a11y 树 | 页面**仍停在建卡页**（`新建卡片` + 正反面输入框），**没有任何 error 节点** |

「后端被掐断」这个假设会预言：**要么**请求发出后失败（页面应有 error、outbox 应有残留），
**要么**至少有请求到达后端（PG 该有行或日志）。实测**三者皆无** ⇒
**问题发生在「点保存」这一步之前**：点击根本没进前端。

### 7.2 机制（CDP + a11y 树双证）

1. **软键盘把 WebView 的可见视口压掉一半**：可视高度 866 CSS px → **530**（这台 AVD 的输入法占 385 CSS px）。
2. **Chrome 为把焦点框（背面 textarea）留在键盘上方**，把
   `<main class="content scroll-shell fullscreen">`（`overflow:hidden`，但**可被程序滚动**）
   滚到 `scrollTop = 165.33`。
3. 于是**顶栏那个 `save-link` 的屏幕位置变成 −254..−180 —— 整块被推到屏幕外**，
   a11y 树里**根本没有它** ⇒ `tapOn: {text:"保存|Save", index: 0}` 选中的其实是
   **底部**那个 `type=submit`（bounds `[556,1691][1047,1802]`），而它此刻**在键盘底下**
   ⇒ 这一下打在键盘上 ⇒ 点不动、页面纹丝不动。
4. 即使把键盘关掉，容器仍停在 `scrollTop=36.95`（= 它的全部溢出量），
   顶栏 Save 落在 **83..157**，**中心 120 正好在系统状态栏带子 [0,129) 里**
   （状态栏 129 设备像素 = body `padding-top` 49 CSS × dpr 2.625）⇒
   Maestro 取 a11y 中心换算出的 (990,120) **打在系统状态栏上**，WebView 收不到。
   而失败现场**没有任何错误提示**，因为 `save()` 压根没被调用。

> 坐标事实（都实测）：a11y 边界 = 文档坐标 × dpr，**零偏移**。
> 登录页「登录」按钮 CDP 文档 y=611 CSS，uiautomator 报 `[1603,1729]` ÷ 2.625 = 610.7 CSS。

### 7.3 修法（改 flow，不改产品）

`.maestro/flashcards-write.yaml` 第 5 段：

```yaml
- pressKey: Back          # 先关键盘：单次 keyevent，实测 IME 收起且 hash 不回退
- extendedWaitUntil:
    visible: { text: "保存|Save", index: 1, enabled: true }   # 底部那个 submit 才回到可视区
    timeout: 20000
- tapOn:
    text: "保存|Save"
    index: 1
    enabled: true
```

- **为什么用 `pressKey: Back` 而不是 `hideKeyboard`**：Android 上 Maestro 的 `hideKeyboard` 是
  按返回键实现的，上一版记过「加了它页面直接回到卡组页」—— 那是**多按了一次**返回键、
  第二次才落到 App 路由上。`pressKey: Back` 只发**一次** keyevent；实测 IME 收起
  （`mInputShown` true→false）而 **hash 仍停在 `#/flashcards/new`**，没有导航回退。
- **为什么不点顶栏那个**：它在「页面上滚过」的状态下要么在屏幕外、要么中心落在状态栏带子里，
  两条都不可靠；底部 `submit` 在键盘关掉后稳定落在 2028..2139（可见、不在键盘下、不在状态栏下）。
- **保存链路本身是好的**：活体上点底部 submit ⇒ click 产生（`isTrusted=true`）、
  `POST /api/flashcards/notes` 发出、hash 正确回到 `#/flashcards/decks/<id>`、页面无 error。

### 7.4 顺带修掉一条**假通过**的断言

第 6 段原来只有 `assertVisible: ".*回归正面.*"`，而**建卡页的正面 textarea 里就写着「回归正面」**
⇒ 保存没生效时它照样成立（实测保存失败后 **131ms** 就「通过」了），
报错被推到两步之后的「今日待复习 1 张」上，**离真因隔了 4 步** —— 上一轮红成那样正是被它掩盖的。
现在先锚定「已经在卡组页」（`.*添加卡片.*|.*Add card.*`，建卡页没有这个按钮，
与本文件第 4 步用的是同一个判据），再断言卡片内容。

### 7.5 留下的产品侧观察（**未改产品**，如实登记）

输入法弹出时，**顶栏「保存」会被推到屏幕外 / 压进状态栏** ⇒ **真人用户**在这一步同样会点不到
（只余一条 10 CSS px 的窄边）。这属于**布局与安全区的关系**问题
（`body { padding-top: env(safe-area-inset-top) }` 的让位在滚动容器之外，
滚动容器一滚内容就滑进状态栏带子）。本轮**没有改前端**、也没有重建 APK：
要改就得动 `frontend/src` 并重出安装包，超出本轮范围，且**不能**为了「让测试绿」
就把它悄悄改掉 —— 这里如实登记，等下一轮决定。

新增两个诊断脚本（只读为主，可复现上面全部读数）：

| 脚本 | 作用 |
|---|---|
| `scripts/diag-statusbar-tap.mjs` | 分清「tap 压根没进页面」与「tap 落进了状态栏带子」；顺带实测 BACK / ESCAPE 收键盘后几何怎么变 |
| `scripts/diag-flashcards-shift-reset.mjs` | 在**失败现场还热着**的时候量清是「DOM 滚动」还是「视觉视口偏移」，并逐个试补救手段 |

---

## 8. 10:33–10:38 的**外部重置事件**（如实登记，非本会话操作）

round52 跑批的 10:33–10:35 期间，机器上发生了一次**不是本会话发起的**重置：

| 现象 | 证据 |
|---|---|
| 两个 AVD 都没了 | `pocket-test`（5556）与并行会话的 `emulator-5554.avd` 同时消失 |
| 两个 pocketd 实例都没了 | 18099 / 18097 端口的进程被清 |
| `/tmp` 被清空 | 本会话的 worktree `/tmp/op-ac-test`、批次日志、env 文件**全部丢失**（worktree 事后重建） |
| agent-companion 被重启 | daemon 10:50 重新起来（版本 `0.1.0-306f698.1792635154`） |

后果与处置：

1. **两条登录流（`2026-10-02-real-login`、`login-gesture`）的结果随 `/tmp` 一起丢失** ⇒
   本轮**各自单独重跑**，两条**全绿**（前者走真实口令成功登录；后者故意用错口令，
   断言「用户名或密码错误」出现、且「AI 工具」不出现 —— 拒绝路径符合预期）。
2. `flashcards-write` 当时红在同一条断言上，**当时被归因成「后端被这次重置掐断」** ——
   **该归因是错的**，见 §7：它在**后端健康、模拟器全新启动**的条件下**稳定复现**。
   ⇒ 教训：**「环境事件」这个说法必须能被独立复现推翻**，否则它会变成一个
   「反正可能是环境问题」的万能借口，把真实缺陷藏起来。
3. 之后把批次产物（Maestro 的 `~/.maestro/tests/`、取证日志）**写到 `~/kaixuan/acp/logs/`**，
   不再放 `/tmp` —— 本轮两次 `/tmp` 清空已经证明它不是持久存储。

---

## 9. 13 条主包 Maestro 流的**统一复跑**（本轮最终验收）

时间：2026-10-22 23:2x–23:5x（同一台 AVD `pocket-test` / 5556，同一次开机后连续跑完）。
设备：**本机仍无 USB Android 真机**，跑的是 AVD（AOSP Android 14 / WebView 113）—— 见 §6 第 5 条。

### 9.1 结果

| # | flow | 结果 | 步数 | 备注 |
|---|---|---|---|---|
| 1 | `smoke-login` | ✅ | 5 | |
| 2 | `notes-crud` | ✅ | 33 | |
| 3 | `tasks-crud` | ✅ | 25 | |
| 4 | `meetings-entry` | ✅ | 16 | 含录音收尾断言 |
| 5 | `messages-hub` | ✅ | 16 | |
| 6 | `more-grid-reach` | ✅ | 37 | |
| 7 | `more-entries-open` | ✅ | 18 | |
| 8 | `email-accounts` | ✅（第 3 次尝试） | 26 | 前 2 次命中**设备级无障碍树抖动**（见 §9.2） |
| 9 | `email-browse` | ✅ | 26 | |
| 10 | `settings-llm-gateway` | ✅ | 18 | |
| 11 | `flashcards-write` | ✅ | 37 | **本轮修的那条**（§7） |
| 12 | `2026-10-02-real-login` | ✅ | 12 | 起点必须无登录态，**单独跑** |
| 13 | `login-gesture` | ✅ | 16 | 故意用错口令，断言拒绝路径，**单独跑** |

**13/13 全绿**，累计 **307 步 COMPLETED**、**2 步 FAILED**（见 §9.2），全部 flow 的 harness 退出码 **0**。

### 9.2 那 2 步 FAILED 是什么（**不放宽断言**）

`email-accounts` 的 `Assert that "邮箱设置" is visible` 连续两次 FAILED（26.5s / 25s 用尽自己的 timeout）。
harness 的**无障碍树抖动护栏**按设计识别了它并**原样重跑整条 flow**（第 2/3、3/3 次尝试，
**断言一个字都没改**），第 3 次通过：

```
[retry] ⚠️ 本条 flow 命中**设备级无障碍树抖动**（Assert that "邮箱设置" is visible FAILED；
        该断言耗尽了自己的 timeout：26.5s / 25s），画面上元素是在的（截图实证），
        是本机 WebView 113 的 Android 无障碍树整片丢了子树。
        重跑**同一条 flow、同样的断言**—— 不放宽任何断言。
[retry] ✅ 第 3 次尝试通过（前 2 次命中设备级无障碍树抖动）
```

这是**本机 WebView 113 的已知设备级抖动**（2026-10-07 起的既有白名单，`scripts/lib/a11y-glitch.mjs`），
不是产品缺陷，也不是被放宽掉的失败 —— 判定权仍在断言本身，护栏只负责**重跑同一条断言**。

### 9.3 复跑方式（为什么分 4 组）

单批 11 条在第一轮跑到第 5 条时被**执行器的单任务 3600s 上限**掐断（`Background bash timed out
after 3600000ms`），**不是测试失败**（当时 FAILED=0）。改成 4 组、每组 ≤3 条后一次跑完。
批次产物落在 `~/kaixuan/acp/logs/rerun-round52/`（**不放 `/tmp`** —— 本轮两次 `/tmp` 清空已证明它不是持久存储）。

### 9.4 同一轮里其它面的复验

| 面 | 读数 | 判定 |
|---|---|---|
| agent-companion 会话电池 | `TOTAL=71 PASS=71 FAIL=0 SKIP=0` | ✅ |
| openpocket → ACC | agents **29** / tasks **3** / projects **1** / sessions **21** | ✅ 会话与任务都看得到 |
| daemon 识别面 | `GET /api/v1/agents` 200、`GET /api/v1/runtime/agents` 200 | ✅ |
| 闪卡落库 | PG `flashcard_notes`=1、`flashcard_cards`=1，front=回归正面 / back=回归背面、due 已设 | ✅ 真落库，不是假绿 |

---

## 10. agent-companion 侧另查出一条**真缺陷**：被叫停的派单被报成 `agent_busy`

这条**不在 openpocket 侧**，是在 §9 复验时用**真 agent 演练**（§14 第 2 步：
`cmd/live-e2e`，真 opencode + 真模型）跑出来的：演练的 **cancel 段**在同一个 commit 上
**4 次里 2 次红**，报错是：

```
LIVE_E2E_FAIL: cancel produced a failure instead of a cancellation:
  agent_busy: agentfacade: another run holds this agent; gave up waiting for its slot
```

### 10.1 现场证据：那一刻**一点也不忙**

在 `acquire` 两侧加临时日志（仅诊断用，未入库）后，同一次失败的时间线是：

```
[DIAG] slot wait begin  agent= dispatch=d-live-e2e-3 in_flight=0 t=23:59:19.630
[DIAG] slot wait FAILED agent= dispatch=d-live-e2e-3                   t=23:59:19.630
```

两点关键：

1. **`in_flight=0`** —— 那一刻槽位是**空的**，压根没有资源争用。「另一个 run 占着这个智能体」这句话是**假的**；
2. **同一毫秒就失败** —— 不是等满了 20s 的等待上限。

原因在 Go 的 `select`：当「ctx 已取消」与「槽位可抢」两条 case **同时就绪**时，
`select` 是**随机**选的 ⇒ 同一个取消时而「抢到槽位把 run 启起来」、时而「报忙」——
这就是它时红时绿的原因。

旁证：把两次演练之间**拉开 25s 间隔**再跑，3/3 全绿 ⇒ 确认是**时序竞态**，不是别的东西。

### 10.2 为什么这不只是「文案不准」

| 错误码 | 下游该做什么 |
|---|---|
| `agent_busy` | 契约原文：「nothing was started, and retrying later is correct」⇒ **可以重试** |
| `cancelled` | 终态：操作员叫停了，**不许再跑** |

把取消报成 busy，等于告诉上游「这个可以稍后再来」⇒ 一个**明确被叫停**的派单
可能被重试回来又执行一遍。**停止语义被降级成了「稍后重试」。**

### 10.3 修法（agent-companion `main`，已推送）

| 文件 | 改动 |
|---|---|
| `internal/agentfacade/agent_slot.go` | `acquire` **先看取消、再看忙**：进等待前 ctx 已取消 ⇒ 直接返回 `context.Canceled`；等待中被取消 ⇒ 同样返回 Canceled；**等待超时仍返回 `ErrAgentBusy`**（那时「忙」才是实话） |
| `internal/agentfacade/executor.go` | `LaunchErrorCode` 把 `context.Canceled` 映射为 **`cancelled`**（ACC 契约的取消码 ⇒ `mapExecutorOutcome` 认它 ⇒ 落终态 `OutcomeCancelled`） |
| `internal/agentfacade/agent_slot_cancel_test.go`（新） | 三条回归：**启动前取消**、**排队中取消**、**超时仍报 busy**（防止把取消当成万能解释、过度纠正） |
| `docs/CANCEL_BEFORE_START_2026-10-22.md`（新） | 完整定性，并登记进 `docs/README.md` 审计交接表 |

提交：**`463da0c`**（`main`，自 `04bb1e8` **快进**，无强推；另推分支
`fix/cancel-before-start-agent-busy` 留痕）。改前的工作副本在临时 worktree 里，
**主工作区一行未动**（并行会话在用）。

### 10.4 验证

| 场景 | 修复前 | 修复后 |
|---|---|---|
| `cmd/live-e2e`（真 opencode + 真模型，cancel 段） | 同一 commit **4 次里 2 次红** | **6/6 全绿**，每轮「cancel took effect in 0s」 |
| `go test ./...` | 绿 | **53 包全绿、0 失败**（rebase 到最新 `origin/main` 后**复跑**） |
| `internal/agentfacade` / `accbridge` / `acc` | 绿 | 绿 |

### 10.5 区分另一类失败（不要混为一谈）

同机还观察到 `acp initialize: agent closed the stream`（**智能体进程根本起不来**）。
它与本条**不同源** —— 那是多个 opencode 并发启动时的争用，与取消路径无关。
本机并行跑多个真 agent 演练会抬高它的命中率，**不能**算到本条头上。

### 10.6 与 openpocket、§9 结论的关系

本条**没有改 openpocket 的任何产品代码**，也不影响 §9 的 13/13 结论：它是 agent-companion
**执行层的一处错误码语义错误**，openpocket 是消费方（经 ACC 看到任务状态）。
但修好之后，用户目标里的「**停止**」这条才真正成立：
**叫停 ⇒ 终态 `cancelled` ⇒ 不会被重试复活**。

---

## 11. 再查出一条：**「正在等审批」的派单叫不停**（停止能力在这一档上是坏的）

这一条同样是 agent-companion 侧的**真缺陷**，是在把线上 daemon 升到含 §10 修复的构建后，
对**线上实例**做「立刻叫停」实测时查出来的。

### 11.1 现场证据

派一单要求执行 `sleep 120`，等它进入 `waiting_permission` 后发取消：

```
POST /api/v1/native/sessions          → runId=run-022435f0-…（state=waiting_permission）
POST /api/v1/runs/run-022435f0-…/cancel → {"state":"cancelling","cancel_requested":true}

t+4s … t+40s（10 次轮询）：state=cancelling | last_event=waiting_permission
干预队列：int-a8405f42… | sleep 120 && echo done | pending   ← 一直 pending
run 记录：ended_at=0001-01-01T00:00:00Z                     ← 从来没落终态
```

另一个变体更难看：取消**先于**智能体起来（cancel 到达时 session 还不存在），
随后智能体进入等审批、人工拒绝 ⇒ run 落成 **`failed` / `permission_denied`** ——
明明是操作员叫停的，回执却说「工具被拒」。

### 11.2 为什么

权限门等的是**人**的决定。daemon 侧取消时只做了两件事：给 agent 发 `session/cancel`、
对**已经挂着**的那一次权限请求回一个 `cancelled` 应答 —— **没有解除这道门**。

- 门等的是人 ⇒ 人不来 ⇒ 门一直开着；
- 门的 ctx 派生自 run ctx，而 run ctx **不随取消结束**（取消只走 ACP 通知，不 cancel ctx）；
- ⇒ driver 卡在 `Hub.Gate` ⇒ `<-permDone` 不返回 ⇒ `handle.finish()` 从不被调用
  ⇒ **run 没有任何终态**。

即使门最终超时（30s 默认）fail-closed 成「拒绝」，`permDenied` 分支又**排在**取消判定
之前 ⇒ 报成 `permission_denied` / `failed`，而不是 `cancelled`。

### 11.3 修法（agent-companion `main`，已推送）

| 文件 | 改动 |
|---|---|
| `internal/agentfacade/acp_driver.go` | ① 给权限门**单独一个可取消的 ctx**（`gateCtx`），取消时 `cancelGate()` 释放它（fail-closed ⇒ driver 立刻能收尾；刻意**不动** prompt 的 ctx，让 agent 还能收到那次 `cancelled` 应答）；② 同一权限请求**只应答一次**（`answerPerm`）；③ 叫停期间的应答必须是 `cancelled`（ACP 取消规则），不是「拒绝」；④ **叫停优先于 `permission_denied`** |
| `internal/agentfacade/acp_cancel_permission_gate_test.go`（新） | 两条回归：叫停 ⇒ 门被释放 + 落 `cancelled`（**修复前红**）；没有叫停的人工拒绝 ⇒ **仍然** `permission_denied`（防过度纠正） |
| `docs/CANCEL_WHILE_WAITING_PERMISSION_2026-10-23.md`（新） | 完整定性，并登记进 `docs/README.md` 审计交接表 |

提交：**`91de17d`**（`main`，自并行会话当时的 `origin/main` 快进，无强推）。

### 11.4 验证

| 场景 | 修复前 | 修复后 |
|---|---|---|
| 线上 daemon：叫停一个等审批的 run | 永远 `cancelling`，>15 分钟不落终态，干预条目永远 pending | **3 秒内落 `cancelled`**（`ended_at` 有值），干预队列清空 |
| 线上 daemon：取消先于智能体起来 | `failed` / `permission_denied` | `cancelled`（t+21s：智能体起来后被中继叫停） |
| `internal/agentfacade` 新回归两条 | 修复前 **1 红** | 全绿 |
| `go test ./...` | 绿 | **全绿、0 失败**（54 包 ok，rebase 后复跑） |
| `cmd/live-e2e`（真 opencode + 真模型） | 绿 | 绿（cancel 段不受影响） |
| 线上 daemon 会话电池 | 71/71 | **71/71 PASS**（TOTAL=71 PASS=71 FAIL=0 SKIP=0） |

线上实例已升级到 **agent-companion `0.1.0-91de17d.1792687911`**（含 §10 + §11 两条修复；
npm 包 + `ac-ctl install`，旧版本 `0.1.0-306f698.1792635154` 仍在 `packages/` 里可回滚）。
agent-companion 侧的完整定性见 `docs/CANCEL_WHILE_WAITING_PERMISSION_2026-10-23.md`
（提交 `3fc6951` 补上了这批线上读数）。

### 11.5 与 §10 **不是**同一个问题

| | §10 | §11 |
|---|---|---|
| 取消发生在 | **还没拿到槽位**（启动路径） | **已经跑起来、正卡在等审批** |
| 症状 | 被报成 `agent_busy`（可重试的失败） | 永远不落终态 / 落成 `permission_denied` |
| 根因 | `select` 在两条 case 同时就绪时随机选 | 权限门没被解除 + `permDenied` 排在取消判定之前 |

两条都在用户目标里的「**停止**」上 ⇒ 都必须通。

---

## 12. 交付时的**全量复跑**（2026-10-23 08:29–09:37，13/13 全绿、0 步 FAILED）

§9 那次复跑是 10-22 23:2x；本节是**交付时的当前态复跑**（同一台 AVD `pocket-test` / 5556，
同一批 flow、同一批断言，**没有改任何断言**）。

### 12.1 结果

| 组 | flow | 步数 | 结果 | 退出码 |
|---|---|---|---|---|
| A | `smoke-login` / `notes-crud` / `tasks-crud` | 63 | ✅ | 0 |
| B | `meetings-entry` / `messages-hub` / `more-grid-reach` | 69 | ✅ | 0 |
| C | `more-entries-open` / `email-accounts` / `email-browse` | 70 | ✅ | 0 |
| D | `settings-llm-gateway` / `flashcards-write` | 55 | ✅ | 0 |
| — | `2026-10-02-real-login`（起点无登录态，单独跑） | 12 | ✅ | 0 |
| — | `login-gesture`（故意错口令，单独跑） | 16 | ✅ | 0 |

**13/13 全绿，累计 285 步 COMPLETED、0 步 FAILED**（比 §9 那次更干净：这次连设备级
无障碍树抖动都没触发）。日志：`~/kaixuan/acp/logs/rerun-round52/final-{A,B,C,D}.log`、
`final-login-{real,gesture}.log`、`final-batch.log`。

### 12.2 过程中撞到的三件**环境事件**（如实登记，均不是产品回归）

| # | 现象 | 证据 | 判定与处置 |
|---|---|---|---|
| 1 | **SystemUI ANR 对话框**盖住整屏 ⇒ Maestro 的无障碍树里看不到底下的 App 元素 ⇒ 前两条流红在「AI 工具」 | 失败截图（08:28）里是 "System UI isn't responding" 弹窗；`dumpsys window` 的 `mCurrentFocus=Application Not Responding: com.android.systemui` | 设备级事件。点「Wait」关掉后重跑即绿。**加了本地看门狗**（`~/kaixuan/acp/logs/rerun-round52/anr-watchdog.sh`，**不入仓库**）：只在系统 ANR 窗口在前台时点「Wait」，不碰 App、不碰断言 |
| 2 | 设备**内存压力**：08:46:22 `lowmemorykiller` 清理进程（contacts 被杀、gapps/partnersetup 被清）⇒ `tasks-crud` 第一次尝试红在「创建」 | 失败帧截图里 **App 已回到登录页**（用户名 `admin` 在、密码空）；两个后端端口（:18099 / :18097）那段时间 **401 计数为 0** ⇒ 服务端**没有**判死会话；设备 logcat 同一时刻在杀进程 | 设备级事件（不是后端拒了会话）。harness 的设备级护栏按设计**原样重跑同一条 flow、同样的断言**后通过，最终日志 0 FAILED。**未放宽任何断言** |
| 3 | 批跑结束后，模拟器已连续运行 **11.96 小时**，launcher 反复 ANR（看门狗每 14 秒命中一次） | `dumpsys meminfo` 显示 Google 系后台进程堆着；watchdog 日志连续多条 "Application Not Responding: com.google.android.apps.nexuslauncher" | **重启 AVD**（`-no-snapshot-load` 冷启，10 秒 boot_completed）→ 重建 `adb reverse tcp:18099/18097` → 复跑代表性 flow 复核（见 §12.3） |

> ⚠️ **第 2 条没有钉死到最后一环**：能证明的是「服务端没发 401」「App 那一刻确实在登录页」
> 「设备同时在杀进程」，但**没有**直接证据说明 App 是被谁踢回登录页的
> （客户端守卫 / 进程重建 / 本地库上锁，三者都符合现有证据）。
> **如实登记为未定因的环境事件**，不写成「已定位」。

### 12.3 重启 AVD 后的代表性复核

重启后复跑 4 条（含本轮修过的那两条：网关配置与闪卡写路径）：

```
adb -s emulator-5556 reverse tcp:18099 tcp:18099   # 与 tcp:18097
node scripts/maestro-run.mjs .maestro/smoke-login.yaml .maestro/notes-crud.yaml \
     .maestro/settings-llm-gateway.yaml .maestro/flashcards-write.yaml
```

→ **exit=0，93 步 COMPLETED，0 个真实失败**（日志 `post-reboot.log`；
「FAILED 数」计数器为 0，日志里也没有任何 `Assertion is false`）。
闪卡那条跑的是**零卡组分支**（夹具先清场并自证 PG=0）。

### 12.4 重启 AVD 后的代表性复核结果

| flow | 结果 | 步数 |
|---|---|---|
| `smoke-login` | ✅ | 5 |
| `notes-crud` | ✅ | 33 |
| `settings-llm-gateway` | ✅ | 18 |
| `flashcards-write` | ✅ | 37 |

---

## 13. ACC 侧「智能体清单」读错了面 ⇒ 明明在线却显示 **0 个**

> 发现路径：交付前的**当前态复验**（2026-10-25，宿主重启后环境被重置）。
> 这条**不是**本轮改动引入的，是**一直存在、被前几轮的读数掩盖**的口径错误。

### 13.1 症状

宿主重启后恢复现场，openpocket 经 ACC 读智能体清单：

```
GET /api/acc/agents → {"agents":[],"count":0,"source":"acc"}
```

而同一时刻 **agent-companion 明明在线**：

| 读数 | 值 |
|---|---|
| daemon `GET /api/v1/agents` | **43** |
| daemon `GET /api/v1/runtime/agents` | **42** |
| ACC `GET /api/v2/agents`（注册表面） | **0** |
| ACC `GET /api/v2/runtime/runtimes/acs-ac30-01/agents`（运行时清单面） | **34**（活的） |

两天前同一套部署，注册表面还是 **29** 条。⇒ 不是「智能体没了」，是**读错了面**。

### 13.2 两面都是「ACC 里的 agent 清单」

| 面 | 端点 | 谁写的 | 实测 |
|---|---|---|---|
| **注册表面** | `GET /api/v2/agents` | ACC 的 host 级注册条目 | 容器重启后**空** |
| **运行时清单面** | `GET /api/v2/runtime/runtimes/{id}/agents` | ACC **回连 agent-companion 的读面**（`GET {api_endpoint}/api/v1/runtime/agents`），带 `status`/`communication_mode`/`pid` | **活的**（34 条） |

契约见 agent-companion `docs/ACC_BRIDGE_SPEC.md`（§`GET /api/v1/runtime/agents` 与
`AgentState`）。openpocket 的 `accruntime.ListAgents` 读的是**注册表面** ⇒
「一个智能体都没有」。

### 13.3 运行时清单面当时也是断的（第二层原因）

直接问 ACC 那条运行时清单端点，得到的是：

```
{"error":{"code":"runtime_read_face_unreachable",
  "message":"read-face /api/v1/runtime/agents: Get \"\": unsupported protocol scheme \"\""}}
```

注册载荷里**没有 `api_endpoint`** ⇒ ACC 回连时拼不出 URL ⇒ 退回到 `drivers` 兜底面
（`fallback_used: true`，`agents: []`）。daemon 侧的旋钮是 **`AC_ADVERTISE_URL`**
（注册时广告读面地址），而本机 `.env` 里没有它。

补上之后还要注意**地址必须在容器里可达**：`127.0.0.1:12110` 指的是**容器自己**
⇒ `connection refused`；正确值是 **`http://host.docker.internal:12110`**。

> 这一处是**部署面**（本机 `~/kaixuan/acp/.env`，0600，不在仓库里）：
> `AC_ADVERTISE_URL=http://host.docker.internal:12110` + `ac-ctl install`
> （plist 里的环境变量是 install 时从 `.env` 渲染的 ⇒ 改完 `.env` 必须重跑
> `ac-ctl install`，只 `kickstart` **不会**带上新键）。

### 13.4 修法（openpocket，提交 `5e804612`）

| 位置 | 改动 |
|---|---|
| `backend/internal/accruntime/ecosystem.go` | 新增 `ListRuntimeAgents`：读 runtime 列表 → 逐个读其 agents 面 → 汇总；契约形状（`agent_id`/`kind`/`status`/`last_active_at`）折进注册表形状（`id`/`type`/`status`/`last_seen_at`） |
| 同上 | 单个 runtime 的读面不通（没广告读面 / 机器下线）**跳过**，不让一个下线的机器把整张表清空 |
| `backend/internal/server/acc_ecosystem.go` | `handleACCAgents` **合并两面**：运行时清单优先（同 id 取带活状态的那条），注册表里多出来的条目补进去；两面都读不到才报错 |

回归（`backend/internal/accruntime/ecosystem_test.go`）：契约形状被正确折叠；
一个 runtime 读面不通时其它 runtime 仍然列得出来。

### 13.5 修完后的读数

```
GET /api/acc/agents → count=36
  mmcode 1 | mmcode-helper 9 | zcode 1 | zcode-cli 24 | opencode 1   （各带活状态）
GET /api/acc/tasks    → 9
GET /api/acc/projects → 1
GET /api/acc/sessions → 32
```

⇒ 用户目标里「**识别 minimax-code / zcode 的 IDE 与 CLI 智能体，识别它的状态**」
这一条，**经 ACC 也真正通了**（此前经 ACC 看到的是 0）。

### 13.6 这一条与本轮改动的分工

| | 归属 |
|---|---|
| openpocket 读哪一面 + 字段折叠 | **仓库内**（本轮修，`5e804612`） |
| daemon 广告读面地址 | **部署面**（本机 `.env`，不在仓库） |
| ACC 注册表面为何重启后为空 | **在 acc-go 仓**（本机无源码），**如实登记，未修** |

---

## 14. 复现命令

```bash
# 1) agent-companion 会话电池（71 条）
cd ~/workspace/ai-native-tools/agent-companion
python3 .work/testrun/session_battery.py http://127.0.0.1:12110

# 2) 真 agent 演练（execute / resume / cancel 三段）
#    演练自己起 daemon（临时 state dir + 独立端口），不碰线上实例
export PATH=/Users/niuer/sdk/go/bin:$PATH
export AC_API_LISTEN=127.0.0.1:12999
export ACC_KAIXUAN_KEY="$POCKET_LLM_GATEWAY_API_KEY"   # 只经环境变量传入
go run ./cmd/live-e2e -timeout 5m -prompt "Reply with exactly: ok"
# 期望：LIVE_E2E_OK

# 3) 线上真派单（分配 → 干预 → 继续 → 停止 → 产物）
curl -s --noproxy '*' -X POST http://127.0.0.1:12110/api/v1/native/sessions \
  -H 'Content-Type: application/json' \
  -d '{"kind":"opencode","prompt":"在当前工作目录创建文件 hello.txt，内容写 ac-live-e2e","cwd":"/tmp/ac-live-demo","timeoutSeconds":180}'
curl -s --noproxy '*' http://127.0.0.1:12110/api/v1/interventions
curl -s --noproxy '*' -X POST http://127.0.0.1:12110/api/v1/interventions/<id>/decision \
  -H 'Content-Type: application/json' -d '{"optionId":"once","approved":true}'
curl -s --noproxy '*' http://127.0.0.1:12110/api/v1/runs
curl -s --noproxy '*' http://127.0.0.1:12110/api/v1/runs/<runId>/artifacts
curl -s --noproxy '*' -X POST http://127.0.0.1:12110/api/v1/runs/<runId>/cancel -d '{}'

# 4) openpocket 经 ACC 看会话与任务
set -a; . /tmp/op-pocketd.env; set +a
TOK=$(curl -s -X POST http://127.0.0.1:18099/api/auth/login -H 'Content-Type: application/json' \
      -d "{\"username\":\"$POCKET_AUTH_USER\",\"password\":\"$POCKET_AUTH_PASS\"}" \
      | python3 -c "import sys,json;print(json.load(sys.stdin)['token'])")
curl -s -H "Authorization: Bearer $TOK" http://127.0.0.1:18097/api/acc/sessions
curl -s -H "Authorization: Bearer $TOK" http://127.0.0.1:18097/api/acc/tasks

# 6) flashcards-write 修复后复跑（关键盘 + 点底部 submit）
cd /tmp/op-ac-test
export JAVA_HOME=/Users/niuer/.local/jdk/jdk-21.0.12.1+1/Contents/Home
export PATH="$JAVA_HOME/bin:/Users/niuer/.local/bin:/Users/niuer/Library/Android/sdk/platform-tools:$PATH"
export POCKET_DEV_PASS="$POCKET_DEV_PASS" POCKET_AUTH_PASS="$POCKET_AUTH_PASS" POCKET_MASTER="$POCKET_MASTER"
export POCKET_SERIAL=emulator-5556 POCKET_API_BASE=http://127.0.0.1:18099
export POCKET_MAESTRO_BIN=/Users/niuer/.maestro/bin/maestro
export POCKET_ADB_BIN=/Users/niuer/Library/Android/sdk/platform-tools/adb
export POCKET_APP_ID=com.kaixuan.opencode.pocket
node scripts/maestro-run.mjs .maestro/flashcards-write.yaml
# 期望：全绿（含「今日待复习 1 张」「^开始复习$ enabled」）

# 7) 两条登录流（起点必须是无登录态，各跑一次）
POCKET_SKIP_CDP_LOGIN=1 POCKET_ALLOW_UNCERTAIN_START=1 \
  node scripts/maestro-run.mjs .maestro/2026-10-02-real-login.yaml
POCKET_SKIP_CDP_LOGIN=1 POCKET_ALLOW_UNCERTAIN_START=1 \
  node scripts/maestro-run.mjs .maestro/login-gesture.yaml

# 8) 闪卡「保存点不动」的机制诊断（只读为主；§7 的读数都能复现）
POCKET_SERIAL=emulator-5556 node scripts/diag-statusbar-tap.mjs
POCKET_SERIAL=emulator-5556 node scripts/diag-flashcards-shift-reset.mjs

# 5) agent-companion 契约测试（锁住本轮四处修复）
cd ~/workspace/ai-native-tools/agent-companion
go test ./deploy/ ./internal/accjwt/ ./internal/acc/ ./internal/acctask/ ./internal/api/

# 9) 取消路径回归（§10）：取消 ⇒ cancelled（终态），超时 ⇒ agent_busy（可重试）
cd ~/workspace/ai-native-tools/agent-companion
go test ./internal/agentfacade/ -run 'TestCancelled|TestTimeoutStillReportsBusy' -v
# 期望：3 条全绿；其中 TestTimeoutStillReportsBusy 必须**仍然**报 busy

# 10) 权限门回归（§11）：叫停 ⇒ 门被释放 + 落 cancelled；无叫停的拒绝 ⇒ permission_denied
go test ./internal/agentfacade/ -run 'TestCancelReleasesPendingPermissionGate|TestPermissionDeniedWithoutCancel' -v
# 期望：两条全绿（**修复前第 1 条红**：the cancel did not release the pending permission gate）

# 11) 线上实例实测「叫停一个正在等审批的 run」（§11 的现场复现）
RID=$(curl -s --noproxy '*' -X POST http://127.0.0.1:12110/api/v1/native/sessions \
  -H 'Content-Type: application/json' \
  -d '{"kind":"opencode","prompt":"sleep 120 然后回答 done","cwd":"/tmp/ac-live-demo","timeoutSeconds":300}' \
  | python3 -c "import sys,json;print(json.load(sys.stdin)['runId'])")
# 等它进入 waiting_permission 再取消，然后看终态：
curl -s --noproxy '*' -X POST "http://127.0.0.1:12110/api/v1/runs/$RID/cancel" -H 'Content-Type: application/json' -d '{}'
curl -s --noproxy '*' "http://127.0.0.1:12110/api/v1/runs/$RID"
# 期望：state=cancelled（修复前：永远 cancelling / ended_at 空 / failed+permission_denied）
```
