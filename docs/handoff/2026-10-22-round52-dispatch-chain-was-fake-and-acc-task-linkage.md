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
| `GET /api/v1/runs` | 本轮共 3 个真实 run（分配/继续/停止各一） |
| `GET /api/v1/runs/:id/artifacts` | 每次文件写入都记到 `file_edit`（含完整路径） |

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
| 4 | **13 条 Maestro 流本轮未重跑** | openpocket 的产品代码与 pocketd 本轮**未改动** ⇒ round51 的 13 绿 / 0 红 结论沿用。这是**推断**，不是本轮实测。 |
| 5 | **本机仍无 USB Android 真机** | 仍是 AVD `emulator-5556`（AOSP Android 14 / WebView 113）。 |
| 6 | **ACC 容器未改动** | `acc-blue`(:4100) 与 `acc-go-host`(:4101) 是共享容器；本轮只**读**它们、没改配置/没重启，避免打断并行会话。 |
| 7 | **openpocket 的 `go test ./...`** | 仍只有 `TestSkipLedger_*` 两包失败，属**并行会话**负责的文件，本轮未触碰。 |
| 8 | **daemon 的 ACC 注册仍是关的** | 日志里 `acc registry registration disabled: ACC_SERVICE_TOKEN not set`（注册面要 `sk-svc.*`，与本轮的 canonical 写入面是两回事）。 |
| 9 | **launchd 的 `install`+`restart` 偶发不拉起** | 表现为 `state = not running`、**`runs = 0`**（一次都没被 spawn 过），`launchctl kickstart -k` 即恢复。排查顺序：**先看 `runs`**。 |

---

## 7. 复现命令

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

# 5) agent-companion 契约测试（锁住本轮四处修复）
cd ~/workspace/ai-native-tools/agent-companion
go test ./deploy/ ./internal/accjwt/ ./internal/acc/ ./internal/acctask/ ./internal/api/
```
