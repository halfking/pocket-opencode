# 2026-10-01 · 24h 整合轮 handoff

> 基线 `origin/main` = `3598357`；整合分支 `consolidate/2026-10-01`
> 详细证据见 `docs/学习muse/evidence/2026-10-01-consolidation-audit.md`
> 本文是**结论层**，证据层在上一份文件里。完成度只以本文的「已验证 / 未验证」两节为准。

---

## 0. 本轮最重要的一句话

**这 24 小时的改动此前从未编译通过、从未跑过一次完整测试。**
并且它带着一把**明文租户网关密钥**等着被推上 main。
本轮把两件事都办完了：合进 main、编译绿、测试只剩既知环境性失败、密钥移除。

---

## 1. 关键决策（3 条，都有对照依据）

### 1.1 移除内置网关密钥，恢复 env-only —— 且这把密钥**早已在 git 历史里**

`opencode/config_writer.go` 曾新增
`const DefaultLLMGatewayAPIKey = "sk-6tGL…K51YV"` 并**删掉了仓库原有的
「禁止把租户密钥写进仓库」那句注释**。

**⚠️ 更正**：我第一轮核对时说「`origin/main` 上没有这把密钥、尚未泄露」——**那是错的**。
我只 grep 了 `backend/` 与 `frontend/`，没 grep `test-evidence/`。
实测这把密钥早在两个提交里就进了 `origin/main`：

| 文件 | 行 | 引入提交 |
|---|---|---|
| `test-evidence/2026-09-01-ai-gateway-test/REPORT.md` | 129（可复制的 `pocketd` 启动命令） | `ab2e71b` |
| `test-evidence/P15plus-real-2026-08-31/verification-report.md` | 196（「用户需求」原文引用） | `1319229` |

本轮已把两处替换为 `<REDACTED-2026-10-01-see-handoff>`。
**但删文件不等于密钥失效**，两件事必须用户处理（见 §5.0）。

代码侧改法：`defaultLLMGatewayState()` 改读 `os.Getenv("POCKET_LLM_GATEWAY_API_KEY")`，
**无内建默认**；`cmd/pocketd/main.go` 去掉 `gwKey` 兜底。

**行为变化（不是 bug，是诚实）**：没有 env 时网关即「未配置」，
`GET /api/integration/status` 报 `Enabled=false / Configured=false`。
拿别人的密钥假装可用才是缺陷。

被这个改动推翻的既有护栏：`llm_gateway_default_init_test.go` 的
`TestDefaultLLMGatewayStateNeedsNoEnv` 原本断言「必须携带内置 key」，
护栏的是「全新装完开箱即用」。**已改写**为
`TestDefaultLLMGatewayStateHasNoBuiltinKey` + `TestGatewayConfigGETReturnsInitializedDefaults`
断言 `apiKeySet == false`。理由写进了测试注释：「开箱即用」的合理形态是
**地址与模型预置好、key 留空并提示**，而不是替用户预置一把不属于他的 key。

### 1.2 并行会话的 broken 产物不提交

`server_stt_settings_test.go`（引用不存在的 `wsAToken`、调用未导出的 `sttDiscovery.Put`）
与 `internal/stt/discovery_test.go`（3 个分类断言失败）——两文件是主工作区的**未跟踪文件**，
未随合并进入整合分支。

### 1.3 快照整合必须走 `git merge`，不能走 `git checkout -- <paths>`

**这是本轮最贵的教训。** 见 §4。

---

## 2. 本轮修的东西

| 级别 | 问题 | 修法 |
|---|---|---|
| CRITICAL | 待推提交含明文租户网关密钥，且原「禁止入库」注释被删 | 删常量，改 env-only，注释写明理由 |
| HIGH | `email.fetchHintHttpError` 在 **9 个语言包全缺** | 9 语言全补 + 逐个 JSON 解析验证 |
| MEDIUM | `ErrNoUsableInvoiceFile` 返回处没用 `%w` → 400 路径不可达 | 改 `%w` |
| MEDIUM | `emailPipelineOnce` 单例的 `SpamDryRun` 无锁改写 → data race | 新增 `emailPipelineMu`，重构为 `runEmailPipeline(ctx, spamOverride)` |
| MEDIUM | `GET /api/emails/invoices/summary` 每次刷新新建飞书表格 | `LedgerPublisher` 加 `PublishedURL`/`RememberPublished` + 进程级 `published map[ws|user]url` 复用 |
| MEDIUM | `EmailPipelineReport` 缺 `shareDocUrl`（前端拿不到分享链接） | `frontend/src/api/email.ts` 补字段 |
| — | 我自己早前 PowerShell 造成的 2 处 mojibake | 编辑工具修回 |
| — | `TestMeetingWorkspaceIsolation` 因移除密钥而红 | 改写成「未配置→502 且不伪造转写」+「测试自建 ASR→200 且落库」两段 |
| — | `server_stt_settings.go:293` 值/指针类型错配（合并带回的旧版） | 传 `&res` |
| MEDIUM | `ProbeEndpointMissing` 是死常量 → 设置页把上游原始 JSON 当「探测失败」甩给用户 | 应用 `74b629c` 交付的补丁里的生产修复；测试另写自包含版（补丁的测试依赖被排除的坏 fixture） |

---

## 3. 已验证清单

| 项 | 判据 | 结果 |
|---|---|---|
| 编译 | `go build ./...` | **EXIT 0** |
| 静态 | `go vet ./...` | 无 undefined / cannot use |
| 后端全量 | `go test ./... -count=1`（带真库 DSN） | 仅 18 项失败，**全部既知环境性** |
| 前端类型 | `npx vue-tsc --noEmit`（整合树上跑） | 退出 0 |
| 隔离测试 | `TestMeetingWorkspaceIsolation` / `TestFinanceWorkspaceIsolation` / `TestSttTranscribeAuthAndUnknownFields` | 全 PASS |
| 网关状态 | `TestIntegrationStatus_LLMGatewayReportsUnconfiguredWithoutEnv` / `...EnvSource` | 全 PASS |
| 网关默认值 | `TestDefaultLLMGatewayStateHasNoBuiltinKey` / `TestGatewayConfigGETReturnsInitializedDefaults` | 全 PASS |
| 台账复用 | `TestPublishLedgerScoped_ReusesPublishedSheet`（真库） | PASS |
| STT 探测分类 | `TestProbeClassifiesBothTransportsMissing` / `TestProbeNoProviderIsNotEndpointMissing` | 全 PASS（**修复前红已用 `git stash` 实测对照**） |
| 密钥不在工作区 | 全仓扫该密钥字面量（模式见 §5.0，只写前缀不写全文） | 无命中（已脱敏） |

18 项失败的构成（`origin/main` 基线同样红，非本轮引入）：

- `internal/agent` 16 项：`TestPiAdapter_*` / `TestStdioTransport_*` /
  `TestACPStdioAdapter_SubscribeEvents`。Windows 上拉不起子进程。
- `internal/email` 2 项：`TestWriteKeyAtomic_CreatesFileWithCorrectMode`
  （文件权限位 666 vs 0600 在 Windows 不成立）、
  `TestFetchPOP3MailboxAuthRejected`（本地 TCP 偶发）。

> 注：交接单 `2026-10-01-audit-round.md` §4 记的
> 「`TestFetchPOP3MailboxAuthRejected` 在 origin/main 基线是绿的、尚未定性」——
> 本轮实测它在**两个基线上都红**，属环境性偶发，**该疑问可以关闭**。

---

## 4. 本轮最贵的坑：用旧快照 `git checkout` 逐文件取回，静默回退了 main

- 做法：从 `wip/2026-10-01-audit` 逐路径 `git checkout` 147 个文件到新 worktree。
- 后果：其中 **16 个文件在 `origin/main` 上已有更新版本**（BUG-AQ / BUG-AP / i18n 卡口），
  快照的 base 更早，checkout 把它们**盖回旧版**。
  受害名单含 `ScheduledTaskListView.vue`、`ScheduledTaskDetailView.vue`、
  `settings-store.ts`、`docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md`（**-298 行**）。
- 检测（可复用）：
  ```
  $mb = git merge-base <snapshot> origin/main
  git diff --name-only $mb origin/main     # main 期间改过的
  # 再逐个比对是否被我暂存了不同版本
  ```
- 正解：`git merge --no-ff <snapshot>`。26 个文件里**只有 `.gitignore` 冲突**，
  其余 15 个 git 自动正确三方合并。
- **附带发现**：合并把 `server_stt_settings.go:293` 的编译错误也带回来了
  ——说明那处错误在快照提交里一直没修，只是主工作区的未提交文件里被修好了。
  「工作区能编译」和「提交能编译」是两件事。

### PowerShell 三个陷阱（都踩了）

| 陷阱 | 后果 | 正解 |
|---|---|---|
| `Set-Content -Encoding UTF8` 改含中文的文件 | mojibake 且不可逆 | 用编辑工具 |
| `cmd > file` 重定向 git 输出 | PS 5.1 按控制台编码写，破坏非 ASCII | `git update-index --cacheinfo` + `git checkout-index` |
| `gofmt -l` | 本仓库几乎列出所有文件（CRLF），**不是有效信号** | 忽略 |

---

## 5. 未验证 / 已知欠账（诚实清单）

### 5.0 【最高优先级·需用户操作】网关密钥必须轮换

这把 key（`sk-6tGL…K51YV`）**自 2026-08-31 起就在 git 历史里**，
最早由 `1319229` / `ab2e71b` 引入。本轮只做了**工作区脱敏**，
以下两件事本轮做不了、也不该由我擅自做：

1. **在网关侧吊销并轮换这把密钥。** 这是唯一真正让旧 key 失效的动作。
   在轮换完成前，任何拿到仓库读权限的人都能用它调用 `https://llm.kxpms.cn/v1`。
2. **历史重写**（`git filter-repo` / BFG）会改写所有 commit sha，
   且已推送到 `origin/main`，需要 force-push + 全员重新克隆。
   **不可逆且影响所有人，本轮不执行。** 若决定做，应在轮换密钥**之后**做。

> 教训（已写进证据文件 §3.1）：我第一轮只 grep 了 `backend/` 和 `frontend/`
> 就断言「仓库里没有这把密钥」。**用局部证据支撑全局结论**是本仓库反复出现的失效模式。

### 5.1 审计发现、**本轮未修**、需要产品或用户定夺

| 位置 | 问题 | 为什么没修 |
|---|---|---|
| `internal/learning/store.go:439-461` | `ActiveDayTimestamps` 的 WHERE 用 `OR`，Go 侧把 `captured_at` 与 `updated_at` **都** append → 窗口外旧时间戳被算入，**虚增连续活跃天数** | 该按哪个时间戳算属产品语义 |
| `internal/learning/store.go:395-405` | `SnoozeReminder` 用 `now + minutes*60` → 24h 后的提醒顺延 2h 会变成 **2h 后到** | 「从现在起」还是「从原定时间起」属产品语义 |
| `server.go handleTaskOperations` | PATCH / DELETE **无任何 `CanWriteWorkItem` 校验**，只有 workspace 维度 `GetTaskScoped` → 同 workspace 普通成员可改/删他人 private 工作项；POST 可指定 `ownerId` | 不在本轮授权的 P0/P1/P2 清单内 |

### 5.2 低危 / 清理项

`LedgerTotalText` 死代码；gif 分支不可达；harvest 端点无前端调用；
`server_email_pipeline.go:267` 缺路径包含检查；`scheduler.go:250` `startMu` 保护不完整；
`syncGatewayUserSetting` 伪造未来时间戳；`scripts/patch-export-grid-signature.mjs` 应删。

**`invoice_realworld_test.go` 含真实发票号与 OSS URL** —— 下一轮应换成合成夹具。

**`backend/internal/server/llm_gateway_mask_key_test.go:15` 有一个 `sk-` 格式的
完整密钥字面量**（`sk-hMv1qI…`，与本轮那把租户 key 不同）。
它作为掩码函数的测试夹具，未在本轮定性与核实是否对应真实密钥，**留档待查**。

**`scripts/` 本轮新增 30 个脚本**，相当一部分是一次性探针（`probe-*` / `diag-*`）。
清理留给下一轮，本轮不扩大范围。

### 5.3 行为变更需要下游确认

移除内置密钥后，**未设 `POCKET_LLM_GATEWAY_API_KEY` 的部署会从
「网关可用」变成「网关未配置」**。这是刻意的，但部署文档/默认值清单需要同步更新。

### 5.4 飞书表格复用是进程级

§2 的台账复用在**进程内**有效，进程重启后仍会新建一张表。
彻底解决需持久化，未做。

---

## 6. 分支处置（实测 `git rev-list --count`）

> 方法坑：`git branch --contains <sha> origin/main` **查不出远端分支是否已合入**，会误判成「都没合」。

| 分支 | ahead | 处置 |
|---|---|---|
| `fix/post-delivery-audit-highs` | 0 | 已合入，**已删** |
| `integrate/bugu-and-main` | 0 | 已合入，**已删** |
| `backup/bug-u-051562b` | 0 | 已合入，**已删** |
| `fix/bugz-marketplace-conflict` | 0，diff 空 | 无独有变更，但挂在**活跃 worktree `wt3`**（`scripts/maestro-run.mjs` 45 秒前还在被写）→ **不删** |
| `origin/codex/platform-goal-20260930` | 4 | 活跃远端分支，不在「不活跃」清理范围 → 保留 |
| `wip/2026-10-01-audit` | 2 | 内容已并入 `consolidate/2026-10-01`；但它是主工作区当前分支，**有活跃会话在写** → **不删** |

**诚实结论：实际只确认了 3 个分支已删（并发会话执行）。
另 2 个虽定性为「无用」，但因挂在活跃 worktree 上，本轮不动。**

推送完成后（02:17）复查，两者对 `origin/main` 已是 `ahead=0 / behind=0`，
但**都仍在被写**：

- `wt3`：`scripts/adb-install-confirm.mjs` 02:13 写入，`logs/maestro-conn8.log` 02:16:53（24 秒前）。
- 主工作区：`.gitignore` 02:12 写入。

**所以本轮一个分支都没删。** 删分支会连带打断正在跑的会话——
「分支 ahead=0」只说明内容已合入，不说明没人还在用它工作。

### 6.1 附带发现：worktree 建到了 workspace 外面

`git worktree list` 显示存在 `C:/workspace/openpocket-baseline-wt`（detached HEAD @ `74b629c`）。
**这不是我建的**（我的整合 worktree 在 workspace 内的 `.wt-consolidate`）。
写到 workspace 之外违反本机安全策略，建议下一轮清理时一并处理。

---

## 7. 并发事实（下一轮开工前必读）

- 本轮全程另一个 Agent 在同一仓库写文件。`origin/main` 在审计期间从 `5053106` 前进到 `3598357`。
- **所有提交动作都在 workspace 内的独立 worktree `C:\workspace\openpocket\.wt-consolidate` 上做**，
  分支 `consolidate/2026-10-01`，完全不碰对方工作区。
- 落地用 `git push origin consolidate/2026-10-01:main`（fast-forward），**不在脏 main 上 merge**。
- 主工作区 `C:\workspace\openpocket`（分支 `wip/2026-10-01-audit`）**至今未被我改动**。

---

## 8. 下一轮提示词

> 接着 `docs/学习muse/evidence/2026-10-01-consolidation-audit.md` §5 的欠账清单做。
> 开工前先做三件事：
>
> 1. `git fetch && git log --oneline -1 origin/main`，确认并发会话是否又推了新东西；
>    确认**没有别的会话正在写同一个工作区**（`git status` 连查两次 + 比对 mtime）。
>    有则先等，不要提交。
> 2. **先跑 `go build ./...` 建基线**——本轮最大的发现就是
>    「工作区能编译」≠「提交能编译」。任何快照整合都必须走 `git merge`，
>    不能 `git checkout <branch> -- <paths>`（会静默回退 main，见 §4）。
> 3. 逐条处理，按这个优先级：
>    - **产品决策类**（阻塞项，需用户先定）：
>      `ActiveDayTimestamps` 虚增活跃天数、`SnoozeReminder` 往回拨、
>      PATCH/DELETE 缺 `CanWriteWorkItem` 写权限校验。
    - **安全/隐私类**：
>      **【最高优先级】轮换 `sk-6tGL…K51YV` 网关密钥**——它自 2026-08-31 起就在
>      git 历史里（`test-evidence/` 两个文件，本轮已脱敏但历史仍在）。
>      轮换之后再决定要不要做 `git filter-repo` 历史重写（需 force-push + 全员重克隆）。
>      然后：`invoice_realworld_test.go` 的真实发票号与 OSS URL 换成合成夹具；
>      补 `handleTaskOperations` 的写权限校验（**注意这是 P0 级越权，
>      与本仓库此前的 B-1~B-4 同类**）。
>    - **清理类**：
>      删 `scripts/patch-export-grid-signature.mjs`；
>      评估 `scripts/` 里 30 个新脚本里的一次性探针该留哪些；
>      `LedgerTotalText` 死代码、gif 不可达分支、`scheduler.go:250` `startMu`、
>      `syncGatewayUserSetting` 伪造未来时间戳、`server_email_pipeline.go:267` 路径检查。
>    - **文档类**：
>      部署文档同步「网关密钥改为 env-only，不设 `POCKET_LLM_GATEWAY_API_KEY`
>      则网关未配置」这一行为变更。
>    - **门禁类**（本轮 §1 提过、还没做）：
>      把 `go build ./...` 变成 CI 卡口。本轮那 6 处编译错误能一路走到
>      「已完成」的叙事里，缺的就是这道卡口。
>
> 另：并发会话若已结束，可以安全清理 worktree `wt3` 与分支
> `fix/bugz-marketplace-conflict`（ahead=0、diff 为空），
> 以及分支 `wip/2026-10-01-audit`（内容已并入 main）。
> **清理前务必先确认没有会话还在写**——本轮就是因为 `wt3` 还在被写才没删。
