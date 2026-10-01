# 2026-10-02 分支收编轮 + 密钥泄漏处置 + 密钥卡口上线

> 本轮是一次**例行收编 + 审计**。前半段（分支收编）结论是「上一轮做得干净」，
> 后半段（审计）挖出一把此前完全没人知道的明文凭据，并因此新增了一道卡口。
> 记录两件事的理由不一样，所以分开写。

---

## 0. 一句话结论

分支层面：11 个分支里 **4 个已完全被 main 覆盖、0 个需要逐文件合并**，删掉即可。
审计层面：上一轮 13 个提交的修复经负控验证**全部真实有效**，但仓库里存在一把
**自 2026-06-29 起就明文提交**的 MCP 凭据（11 个文件、20 处），本轮已打码并
新增卡口防止复发。**该凭据仍需轮换——打码不等于作废。**

---

## 1. 分支收编

### 1.1 判定方法

没有用 `git diff` 的任何一种形式判「分支带来了什么」。理由见
`docs/handoff/2026-10-01-audit-round4.md` 与既往教训：三点 diff 在落后 main
几十个提交的分支上会给出上千行「新增」假象，两点 diff 则会把 main 的新增报成
「删除」。本轮逐文件问两个问题：

```
git cat-file -e origin/main:<file>            # 不存在 = 该文件是分支真正带来的
git diff --stat origin/main <branch> -- <file> # 有输出 = 仍有差异
git merge-base --is-ancestor <branch> origin/main
```

### 1.2 结论表

| 分支 | 判定 | 依据 | 处置 |
|---|---|---|---|
| `audit-snapshot-rd7` | 已覆盖 | 5 个文件里 2 个与 main **逐字节相同**（`reminder_diag_test.go`、`invoice_harvest_test.go`）；另 3 个的「差异」是 main 前进了（反向 diff 是 main +724/-53），其标志物 `spamWeakWords` / `weakHits` 计分已在 main 且更完善 | 删除 |
| `audit/round8-2026-10-01` | 已覆盖 | 唯一「未合并」的提交是一个 **merge commit**（把当时的 origin/main 合进来），无任何独有非 merge 提交；其 `pop3_fetcher.go` / `store_pg_regression_test.go` 的引入提交 `a1dd901`/`166ca96` 都在 origin/main 上 | 删除 + 移除 worktree `wt-rd8` |
| `audit/backend-red-baseline` | 已合并 | `merge-base --is-ancestor` 为真，ahead=0 | 删除 + 移除 worktree `wt-head` |
| `fix/forgot-password-silent-deadend` | 已合并 | ahead=0，且**无 worktree 占用** | 删除 |
| `ci/wire-style-guards-into-gates` | 已合并但**有活跃会话** | ahead=0（tip 就是 origin/main head）；但 `wt-font` worktree **3 分钟前还在写** | 保留分支，未动 worktree |
| `fix/bugz-marketplace-conflict` | 已合并但**脏** | ahead=0；`wt3` 有 **42 个未提交文件**、110 分钟前还在写 | 保留，未动 |
| `fix/email-cache-backfill` | 已合并 | ahead=0；`wt-mailfix` 空闲 430 分钟，仅 1 个未跟踪垃圾文件 `COMMIT_MSG.txt` | **本轮未删**：worktree 在仓库外 `C:\workspace\wt-mailfix`，`git worktree remove` 需 `--force` 才能删那个未跟踪文件，不愿在仓库根之外强制删目录。列为遗留项 |
| `wip/pdf-document-plugin` | tip 已合并但**有在制品** | tip ahead=0；`.wt-pdf` 有 5 个未提交文件（PDF 文档插件的 `DocumentPlugin.java` 等） | 保留，未动 |
| `email-pipeline-snapshot-2026-10-01` | **活跃** | ahead=61；`wt-email` 审计期间 HEAD 从 `76400a6` 变到 `8c94f0d`（有人在提交），1 分钟前还在写 | 未动 |
| `feat/mail-config-deploy` | **活跃** | ahead=24；`wt-maildeploy` 4 分钟前还在写 | 未动 |
| `feat/2026-10-01-stt-service` | **脏且体量大** | ahead=8；`wt-stt` 有 **173 个未提交文件** | 未动 |

另移除：detached worktree `wt-822f`（`8223306`，ahead=0、干净、空闲 713 分钟）。

**净结果：4 个分支 + 4 个 worktree 清理掉，7 个保留。** 保留的每一个都给出了
「为什么不能动」的具体理由（活跃写入 / 未提交在制品），不是笼统一笔带过。

### 1.3 刻意没做的事

**没有改写 git 历史。** 当前有 5 个 worktree 里的并发会话正在活动，历史改写会
直接破坏它们；而且这属于需要显式授权的破坏性操作。

---

## 2. 对上一轮 13 个提交的审计

`origin/main` 在本轮之前比本地多 13 个提交。逐个读过，**结论是它们都站得住**。
下面记的是「怎么验的」，不是「相信了」。

### 2.1 邮件归属校验（8d119a1 / c1d1e67）——无遗漏

新加了 `Store.AccountOwnedBy` 作为统一判据。我把**所有**从请求里取 `account_id`
的入口找出来逐个核对，而不是只看被改的那两处：

| 入口 | 归属如何保证 | 结论 |
|---|---|---|
| `server_email_folders.go:49` 目录登记 | `AccountOwnedBy` 直接校验 | 已修 |
| `server_email_ops.go:111` ops 日志（会被 `ops/sync` 真的执行） | 同上 | 已修 |
| `email_cleanup.go` 清理 | `ListEmailsForCleanupScoped` 用 `JOIN email_accounts ... a.user_id=$1 AND a.workspace_id=$2`；IMAP MOVE 用的 accountID **来自该查询的结果行**，不是请求体 | 安全 |
| `server_email_backfill.go` 回补 | `ListAccountsScoped` 过滤后匹配，匹配不到返回 **404**（不静默成功） | 安全 |
| `server_email_folders.go:264` 整理 | `ListEmailsScoped` 带作用域 | 安全 |
| `server_assistant.go:1326` 列表 | 同上 | 安全 |
| `server_assistant.go:2027` 模式 B 推送 | `GetAccountByIDScoped` | 安全 |
| `server_assistant.go` 模式 A 主动 IMAP 抓取 | `GetAccountByIDScoped` → 404 | 安全 |

这一类**已经闭合**。

### 2.2 网关自愈三连（8e44206 / eacf2ed / 3ea1840）——负控有效

三个提交来自三个并发会话、改的是同一块逻辑，所以重点看它们是否互相打架。
读下来是一致的：`eacf2ed` 给 `LoadConfig` 失败分支加了「env 无 key 就只告警
不落库」的守卫，而这个守卫在**两个地方**都存在（`EnsureLLMGatewayDefaults`
与 `LoadLLMGatewayFromDB`），两处都被同一批改到了。

**负控实验**（把两处守卫的 `def.APIKey == ""` 改成永不成立的哨兵串）：

```
--- FAIL: TestEnsureLLMGatewayDefaults_SelfHealDoesNotWipeKeyWhenEnvEmpty
--- PASS: TestEnsureLLMGatewayDefaults_SelfHealStillRunsWhenEnvHasKey
--- FAIL: TestLoadLLMGatewayFromDB_SelfHealDoesNotWipeKeyWhenEnvEmpty
--- PASS: TestLoadLLMGatewayFromDB_SelfHealStillRunsWhenEnvHasKey
```

正是预期的形状：两个「不许抹掉 key」转红，两个「env 有 key 时自愈仍要跑」
保持绿。说明这些测试**真的在测那件事**，不是摆设。

顺带确认了读路径有兜底：`pickGatewayState` → `effectiveGatewayState` 会在本
workspace 无 key 时向 `default` workspace 借，再借不到才回落 env。
所以真机 audit 发现的「active 行 key 长度为 0」不是死局。

### 2.3 其它

- **bootstrap 默认口令（336c883）**：没有为了「能建号」而放宽 8 字符下限，而是
  拒绝用内置口令建号并把后果写进 WARN。`bootstrapDecision` 已在
  `main.go:244` 真实接线（不是又一个 `pickAPIKey` 式死函数）。**认可。**
- **reminder 夹具日期（c073519）**：`workitem_reminder_quiet_test.go` 里已无
  `time.Date(20..)` 硬编码，改为 `time.Now().In(loc)` 派生并 `AddDate(0,0,1)`
  兜底陈旧窗。**这类定时炸弹的正解。**
- **PG 测试助手 search_path（da4b5bb）**：普查了全部 24 处
  `search_path = schema+",public"`，**每一处都是无条件覆盖**；只有
  `server_auth_extended_test.go` 是「沿用调用方」的原缺陷，现已显式拒绝并注释。
  **这一类已闭合。**

---

## 3. 本轮真正的发现：一把明文 MCP 凭据

### 3.1 怎么找到的

不是靠又一次人手 grep。是因为发现**没有任何机制**在检查这件事，于是补了一道
卡口（`backend/internal/repohygiene/secrets_test.go`），卡口第一次运行就报了
28 处命中。

### 3.2 内容

一把 39 位 `sk-mcp-` 形态的凭据，出现在 **11 个受跟踪文件、20 处**，用途是：

- `POCKET_MCP_API_KEY` —— MCP 客户端访问 ACC（`https://mcp.kxpms.cn/acc/mcp`）
- `POCKET_INSTANCE_DISCOVERY_AUTH_TOKEN` —— 实例发现鉴权

文件：`backend/config/mcp-config.md`（活跃配置文档，2 处）+
`docs/archive/2026-07/` 下 10 份部署/测试报告（18 处）。

最早可追到 `2026-06-29`（`DEPLOYMENT_REPORT_2026-06-29.md`）。

### 3.3 为什么此前一直没被发现

2026-10-01 那一轮把仓库里另一把网关 key（`sk-6tGL…`）从工作树清掉了，还补了
`TestDefaultLLMGatewayStateHasNoBuiltinKey`。但那道护栏的**作用域只有一个函数**。
人手 `git grep` 又只查过 `backend/ frontend/ scripts/`——**这把在
`backend/config/` 和 `docs/archive/`**。

这与 `docs/handoff/2026-10-02-debt-round2.md` §1.2 记的教训是**同一条**：
「用局部证据支撑全局结论」。上一轮刚写完这条教训，下一轮又以另一种形式复发。

### 3.4 处置

- **20 处全部就地打码**为 `sk-mcp-<REDACTED-ROTATE-ME>`。
- `backend/config/mcp-config.md` 另加了一段显式警告：说明这把 key 必须视为已泄漏、
  `git log -S` 仍可取回、换新 key 只能走环境变量注入。
- `docs/handoff/2026-10-02-debt-round2.md` 里残留的网关 key **前缀指纹**
  （`sk-6tGLjzlzUIOu…` 与核查命令里的 `sk-6tGLjzlz`）一并去掉，
  核查命令改写成不硬编码前缀的通用式。

---

## 4. 新增卡口：`backend/internal/repohygiene`

### 4.1 为什么放在 Go 包而不是 `frontend/scripts/check-*.mjs`

仓库既有的卡口（`check-raw-error-text.mjs`、`check-dead-api.mjs`）都在 npm
`gates` 里，而 **CI 并不跑 `npm run gates`**（`frontend.yml` 是一条条列具体
命令）。放进 Go 包则会被 `.github/workflows/backend.yml` 的
`go test -race ./... -count=1` **必然执行**。

本轮之前的教训正是「护栏写好了但没有任何东西会执行它」
（`8869943` 与 `4cd6e7e` 修的都是这一条）。放在没有执行者的地方等于没写。

### 4.2 与既有卡口的设计差异：**没有基线棘轮**

`check-raw-error-text.mjs` 用棘轮（存量钉基线、只许减不许增），那对代码风格债是对的。
但**泄漏的密钥不能用基线豁免**——把密钥记进基线文件等于把它又抄了一遍，还给了它
「已登记」的合法感。真实密钥的正确处置是**轮换**，不是登记。

故：**零容忍，命中即 exit 1，没有 `--update-baseline`。**

### 4.3 一条容易忽略但很要命的实现约束

报告里**不回显密钥本身**，只输出前缀 6 字符 + 长度。理由：这份输出会进 CI 日志，
而公开仓库的 Actions 日志无需登录即可读。一个「检测密钥泄漏的工具」如果在报告里
把密钥原样打出来，等于把它泄露到一个新地方，而且**每次运行泄露一次**。

### 4.4 判据与豁免

规则：`sk-` 形态、AWS `AKIA/ASIA`、GitHub `gh[pousr]_` / `github_pat_`、
Slack `xox*`、PEM 私钥块、以及「把长字面量赋给名字像凭据的变量」。

两处非显然的设计：

1. **`sk-` 必须前面不是字母。** 第一版没加这个边界，一开卡口就误报
   `server.go` / `task.go` / `disk_task_fallback_test.go`——因为
   `ta**sk-**acceptance-evidence-design.md` 和 `di**sk-**fallback-test-secret`
   都含有子串 `sk-`。
2. **豁免必须逐行**（`secret-scan-ok`），不提供目录级跳过。当前 4 处逐行豁免
   全部是**负控夹具**（故意写错的密钥，用来证明网关会拒绝错密钥）与 maskKey 合成串。
   另有 2 处**逐文件**豁免且各带理由：`agents.json` 与 `chat_agents_seed.sql`——
   它们是「高级安全运营工程师」agent 的 system prompt，**内容本身就是一份凭据
   模式清单**（`-----BEGIN RSA PRIVATE KEY-----`、`AKIA[0-9A-Z]{16}` 等）， <!-- secret-scan-ok：本行只是引用模式名 -->
   删掉那些行等于删掉这个 agent 的职责。

### 4.5 卡口自己踩的坑（值得单独记）

`placeholderValue` 第一版写成：

```go
regexp.MustCompile(`(?i)^(?:|x{4,}|\*{4,}|...)`)   // ← 第一个分支是「空」
```

那个空分支能匹配空串，而 `^` 同时满足，于是**整个正则匹配一切**。后果是每条命中
都被当成占位符过滤掉，测试**永远绿**。

> 「一个永远通过的门禁」比没有门禁更危险：它让人以为已经防住了。

正确写法是让「空值」这一支同时锚住首尾：`^(?:$|...)`。

**这个 bug 只靠负控发现**：往受跟踪文件里真塞一把合成 key，看它转不转红。
只读代码看不出问题，只看绿灯也看不出问题——只有故意弄坏它才能。

### 4.6 性能

第一版对 2700+ 个文件逐行跑 6 条正则，光两个 3.6 MB 的 seed 文件就够拖到
**56.57 秒**——放进 `go test -race ./...` 不可接受，门禁太慢就会被跳过，
于是又回到「没人执行」。

改为：整行小写化一次 → 先用十几次 `strings.Contains`（无回溯、线性）做前置判据
→ 只有含 trigger 的行才上正则。**56.57s → 1.10s（51×）。**

---

## 5. 测试命令与结果

```
cd backend
go build ./...                                   # exit 0
go vet ./...                                     # exit 0
go test ./...                                    # exit 0（63 包，0 FAIL，11 包无测试）
go test ./internal/repohygiene/ -count=1 -v      # PASS，1.12s，扫描 2703/3051 文件（整文件豁免 3）

cd frontend
npm.cmd run gates                                # 本轮开始时 exit 0；见下方说明
```

负控（都已执行并记录在上面）：网关自愈守卫短路 → 2 红 2 绿；
植入合成 key → 卡口转红并精确报出 `file:line`；移除 → 恢复绿。
**加了「扫描器自身」豁免之后又复测了一次负控，确认豁免没有把门禁的牙拔掉。**

> 注：PowerShell 下必须用 `npm.cmd`，`npm.ps1` 会被执行策略拦掉。

### 5.1 前端 gates 现在的状态：**红的，但不是本轮造成的**

本轮开始时（21:5x）跑 `npm.cmd run gates` 是 exit 0。23:1x 再跑变成 exit 1：

```
TypeError: inner.on is not a function
    at frontend/scripts/test-file-census-reporter.mjs:17:7
❌ 没拿到测试普查结果 —— reporter 没落盘，不能声称覆盖完整。
```

原因是**并发会话在本会话期间落了一批未提交的前端改动**（都在主 worktree 里）：

```
 M frontend/package.json          # test:all 从 `node --test "src/**/*.test.mjs"`
                                  # 改成了 `node scripts/run-mjs-tests.mjs`
?? frontend/scripts/run-mjs-tests.mjs
?? frontend/scripts/test-file-census-reporter.mjs   # ← 报错的就是它
?? frontend/scripts/check-test-coverage.mjs
?? frontend/scripts/test-coverage-baseline.json
?? frontend/.scratch-*.mjs
```

`test-file-census-reporter.mjs` 的自定义 reporter 与当前 Node v22.23.2
不兼容（`inner.on is not a function`）。

**本轮没有碰 `frontend/` 下任何文件**，也没有提交这批改动——它们属于并发会话。
需要注意的是：这批改动一提交，`npm run gates` 就会真的红，而
`frontend.yml` 并不跑 `npm run gates`（它逐条列命令），所以 CI 未必会拦住。
**归属该会话自行修复。**


---

## 6. 遗留风险（按严重度）

### P0 —— 需要你在服务侧操作，我做不了

1. **轮换 MCP 凭据 `sk-mcp-…`**。它自 2026-06-29 起就在 git 历史与 origin 远端里，
   任何拿到仓库的人 `git log -S` 都能取回。**本轮的打码只清理了工作树。**
   换新后只走 `POCKET_MCP_API_KEY` 环境变量注入。
2. **轮换网关凭据 `sk-6tGL…`**（2026-10-01 那一轮已从工作树清除，但历史仍在）。
   这一条此前已被标记为「最高优先级」，**至今未轮换**。

两条都属于「删文件不等于密钥失效」。是否改写历史我没有做，也不建议在有 5 个并发
会话活动时做。

### P1

3. `fix/email-cache-backfill` 已合并且空闲，但 worktree `C:\workspace\wt-mailfix`
   有一个未跟踪文件导致 `git worktree remove` 需要 `--force`。我没有在仓库根之外
   强制删目录。清理方式：确认 `COMMIT_MSG.txt` 可弃后
   `git worktree remove --force C:/workspace/wt-mailfix && git branch -D fix/email-cache-backfill`。
4. `wt3`（`fix/bugz-marketplace-conflict`）有 42 个未提交文件、110 分钟前还在写；
   `.wt-pdf` 有 5 个。归属并发会话，本轮未动。
5. 卡口的 PEM 私钥规则对 `agents.json` / `chat_agents_seed.sql` 整文件豁免。
   若将来往这两个文件里加**真实**私钥，卡口不会报。复查成本：每次改动这两个文件时。

### P2

6. `pickAPIKey`（`llm_gateway_handler.go:88`）仍是**零调用点的死函数**，注释写明
   「不再回退仓库内写死的租户 key」。它本身无害，但留着等于给后来者留一个
   「把写死的 key 接回去」的接口。本轮未删——它属于上一轮改动的收尾，
   建议下一轮连同本 handoff 一起清理。
7. `openpocket` 主 worktree 里 `docs/handoff/2026-10-01-stt-gateway-discovery.md`
   有 131 行未提交改动（非本轮产生），本轮未动。

---

## 7. 本轮改动文件

**新增**
- `backend/internal/repohygiene/doc.go` — 包说明
- `backend/internal/repohygiene/secrets_test.go` — 密钥卡口

**打码（20 处 / 11 文件）**
- `backend/config/mcp-config.md`（+ 警告说明）
- `docs/archive/2026-07/` 下 10 份报告

**豁免标记**
- `backend/internal/server/llm_gateway_mask_key_test.go:21`
- `scripts/llm-endpoint-shapes.mjs:57`
- `scripts/probe-chat-endpoint-by-model.mjs:81`
- `docs/handoff/2026-10-01-audit-round4.md:74`

**文档卫生**
- `docs/handoff/2026-10-02-debt-round2.md` — 去掉网关 key 前缀指纹

**删除**：4 个分支、4 个 worktree（见 §1.2）

---

## 8. 下一轮提示词

```
继续 2026-10-02 的收编/审计轮（先读 docs/handoff/2026-10-02-branch-consolidation-and-secret-gate.md）：

1. 先确认 MCP 凭据 sk-mcp-… 与网关凭据 sk-6tGL… 是否已轮换。
   这是 P0，打码不等于作废。未轮换则本轮其它工作价值有限。
2. 收尾遗留项：
   - 删掉 llm_gateway_handler.go:88 的死函数 pickAPIKey（零调用点，
     留着等于给后来者留一个「把写死 key 接回去」的接口）。
   - 清理 fix/email-cache-backfill + worktree C:\workspace\wt-mailfix
     （需 --force，先确认 COMMIT_MSG.txt 可弃）。
3. 普查新一批分支。判定「分支带来了什么」继续用逐文件法
   （git cat-file -e origin/main:<file> + git diff --stat），
   不要用 git diff 的三点/两点形式。
4. 每收编一个分支，对**它新加的护栏测试**做一次负控：
   故意弄坏被测逻辑，确认测试转红。上一轮发现三个会话的网关修复里，
   有两个测试是「绿灯即安全」的错觉；负控是唯一能分辨的办法。
5. 新增任何护栏前先问：谁会执行它？CI 跑不跑？
   放进 frontend/scripts/*.mjs 不会被 CI 跑（frontend.yml 是逐条列命令的），
   放进 backend Go 包才会被 backend.yml 的 go test -race ./... 必然执行。
6. 负控纪律同样适用于新写的门禁/扫描器：植入一个已知样本，
   确认它转红。一个永远通过的门禁比没有门禁更危险。
```
