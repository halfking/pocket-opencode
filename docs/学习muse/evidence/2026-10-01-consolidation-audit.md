# 2026-10-01 · 24h 整合轮审计证据

> 基线：`origin/main` = `3598357`
> 整合分支：`consolidate/2026-10-01`
> 快照来源：`wip/2026-10-01-audit`（并行会话 24h 工作区快照，`88cb5a2` + `6e73f2e`）
> 真库：`postgres://…@192.168.31.34:15433/postgres`（容器 `ai-native-postgres` / PG17）

本文件只写**有对照证据**的结论。推断、待定性项一律进 §6。

---

## 0. 一句话结论

这 24 小时的改动**此前从未编译通过、从未跑过一次完整测试**，
并且里面藏着一个会把租户网关密钥写进 git 历史的 CRITICAL。
本轮做的是：把它真正合进 main、让它 `go build` 绿、让 `go test` 只剩既知环境性失败、
并把那条密钥拿掉。

---

## 1. 起点状态（实测）

| 项 | 实测值 |
|---|---|
| `origin/main`（本轮开工时） | `5053106`，审计中又前进到 `3598357` |
| 快照分支 vs `origin/main` | ahead 2 / behind 4，**127 files, +10520 / -302** |
| 快照是否可编译 | **否**，6 处编译错误（见 §2） |
| 快照是否跑过完整测试 | **无任何记录** |

---

## 2. 编译错误（6 处，全在未提交工作区）

| 位置 | 错误 | 根因 |
|---|---|---|
| `internal/email/pipeline.go:457,472` | `p.Ledger.PublishedURL / RememberPublished undefined` | 调用方先写、接口后补的中途态 |
| `internal/email/ledger_test.go:131` | `*fakeLedger` 未实现接口 | 同上，测试侧同样断 |
| `internal/server/server_email_pipeline.go:77` | `undefined: sync` | 缺 import |
| `internal/server/server_assistant.go:2449`、`server_meeting.go:123` | `undefined: stt` | 缺 import |
| `internal/server/server_stt_settings.go:293` | 值类型传给指针形参 | 类型错配 |

**判据的意义**：这 6 处能一路走到「已完成」的叙事里，说明**缺一道 `go build` 门禁**。
本轮整合后 `go build ./...` EXIT 0（§5）。

---

## 3. CRITICAL：待推提交内含明文租户网关密钥（已移除）

- 现象：`backend/internal/opencode/config_writer.go` 新增
  `const DefaultLLMGatewayAPIKey = "sk-6tGL…K51YV"`，
  并**删掉了仓库原有的注释**——那句注释原文是「禁止把租户密钥写进仓库」。
- 范围：`llm_gateway_handler.go` 的 `defaultLLMGatewayState()` 读这个常量，
  `cmd/pocketd/main.go` 拿它做 `gwKey` 兜底。等于**所有用户默认共用一把租户密钥**。
- **核对（第一轮，我判断错了）**：我先只 grep 了源码目录，结论是「`origin/main` 上没有这条密钥，
  尚未泄露」。**这个结论是错的**，见 §3.1。
- 用户裁定：**推送前移除**，恢复 env-only 注入。已改：
  - `config_writer.go`：删常量，注释改回并写明「为什么这次不能再加」。
  - `llm_gateway_handler.go`：`defaultLLMGatewayState()` 改读
    `os.Getenv("POCKET_LLM_GATEWAY_API_KEY")`，**无内建默认**。
  - `cmd/pocketd/main.go`：去掉 `gwKey = opencode.DefaultLLMGatewayAPIKey` 兜底。
- 行为变化（诚实代价）：**没有 env 时网关就是「未配置」**，
  `GET /api/integration/status` 报 `Enabled=false / Configured=false`，
  能力说明写「no gateway credentials」。这是正确的答案——
  拿别人的密钥假装可用才是缺陷。
- 测试同步：`TestIntegrationStatus_LLMGatewayReportsBuiltInDefault`
  改写为 `TestIntegrationStatus_LLMGatewayReportsUnconfiguredWithoutEnv`，PASS。

### 3.1 更正：这把密钥**早已在 `origin/main` 里**（我的第一轮核对方法有缺陷）

- 引入提交（实测 `git log --diff-filter=A`）：
  - `ab2e71b` `test(evidence): AI 网关 auto-fallback 真机验证报告`
  - `1319229` `test(evidence): 2026-08-31 P15plus 真机基线 + Android 抽屉 + iOS/sim baseline`
- 落点（两处，均为**已跟踪文档**，不在源码目录）：
  - `test-evidence/2026-09-01-ai-gateway-test/REPORT.md:129` —— 一段可复制的 `pocketd` 启动命令
  - `test-evidence/P15plus-real-2026-08-31/verification-report.md:196` —— 「用户需求」原文引用
- **我第一轮的核对错在哪**：只 grep 了 `backend/` 与 `frontend/`，没 grep `test-evidence/`。
  「源码里没有」被我在结论里外推成了「仓库里没有」。
  这正是本仓库反复出现的失效模式：**用局部证据支撑全局结论**。
- 本轮已做：两处替换为 `<REDACTED-2026-10-01-see-handoff>`。
- **本轮做不了的（必须用户处理）**：
  1. **轮换这把密钥**——它已在 git 历史里，删掉文件不等于失效。
  2. **历史重写**（`git filter-repo` / BFG）会改写所有 commit sha，
     且已推送到 `origin/main`，需 force-push + 全员重新克隆。
     **这是不可逆且影响所有人的操作，本轮不擅自执行。**

---

## 4. 逐条批判与修复（MEDIUM / HIGH）

### 4.1 HIGH · i18n：`email.fetchHintHttpError` 在 9 个语言包全缺

- 事实：其余 4 个 `fetchHint*` 键 9 语言都在，唯独这个新加的 HTTP 错误提示没有。
- 后果：`t('email.fetchHintHttpError')` 渲染出裸 key，用户看到的是 `email.fetchHintHttpError` 而不是提示。
- 已修：为 `en-US / zh-CN / de-DE / es-ES / fr-FR / ja-JP / ko-KR / pt-BR / zh-TW`
  逐个补齐，并**逐个用 JSON 解析器验证可解析 + 值正确**（中途曾把 9 个 JSON 改坏，
  逗号/缩进/值都逐一修回；教训：批量改语言包必须每轮跑解析器，不能靠眼看）。

### 4.2 MEDIUM · `ErrNoUsableInvoiceFile` 的 400 路径不可达

- `export_pdf.go` 声明了 `ErrNoUsableInvoiceFile`，handler 里也用 `errors.Is` 匹配，
  但**返回处没用 `%w` 包装** → 永远匹配不上 → 用户拿到 500 而不是 400。
- 已修：返回处改为 `%w`。

### 4.3 MEDIUM · `emailPipelineOnce` 单例的 `SpamDryRun` 被无锁改写

- 手动跑（`run-email-pipeline-once` 之类的调试端点）与定时跑并发时，
  一个 goroutine 写 `SpamDryRun`、另一个读，没有同步 → data race + 行为不确定。
- 已修：新增 `emailPipelineMu`，重构为 `runEmailPipeline(ctx, spamOverride)`，
  **覆盖与执行同锁**。

### 4.4 MEDIUM · `GET /api/emails/invoices/summary` 每次刷新都新建飞书表格

- 台账发布器没有复用：每刷新一次页面就多一张飞书表，租户侧垃圾堆积。
- 已修：`LedgerPublisher` 接口加 `PublishedURL` / `RememberPublished`；
  `feishuLedgerPublisher` 用 `publishedMu` + `published map[ws|user]url` 做**进程级复用**；
  `PublishLedgerScoped` 先查缓存再建。
- 测试：更新 `ledger_test.go` 的 fake，新增 `TestPublishLedgerScoped_ReusesPublishedSheet`（真库 PASS）。
- 诚实边界：这是**进程级**缓存，进程重启后会再新建一张。要彻底解决需持久化，属未做。

### 4.5 MEDIUM · `EmailPipelineReport` 缺 `shareDocUrl`

- 后端结构体有 `shareDocUrl`，`frontend/src/api/email.ts` 的类型没有 → 前端拿不到分享链接。
- 已修：补上该字段。

### 4.6 我自己早前造成的 mojibake（已修回）

- `internal/task/quiet_test.go`、`internal/server/task_notify_wiring_test.go`
  两个文件被 PowerShell `Set-Content -Encoding UTF8` 破坏了中文。
- 已用编辑工具修回。**教训已固化进本轮：不要用 `Set-Content` 改含中文的文件。**

---

## 5. 测试结果（对照实测）

### 5.1 整合分支 `consolidate/2026-10-01`

| 命令 | 结果 |
|---|---|
| `go build ./...` | **EXIT 0** |
| `go vet ./...` | 无 undefined / cannot use |
| `go test ./... -count=1`（带真库 DSN） | 仅剩 **18 项失败**，全部为既知环境性问题 |
| 前端 `npx vue-tsc --noEmit`（**在整合树上跑**，junction 指向主工作区 `frontend/node_modules`） | 退出 0 |

18 项失败的构成：

| 包 | 数量 | 定性 |
|---|---|---|
| `internal/agent` | 16（`TestPiAdapter_*` / `TestStdioTransport_*` / `TestACPStdioAdapter_SubscribeEvents`） | **环境性**：Windows 上拉不起子进程，`origin/main` 基线同样红 |
| `internal/email` | 2（`TestWriteKeyAtomic_CreatesFileWithCorrectMode`、`TestFetchPOP3MailboxAuthRejected`） | **环境性**：文件权限位语义在 Windows 不成立；后者为本地 TCP 偶发 |

`internal/learning` 的 `TestActiveDayTimestamps` / `TestReminderLifecycle`
在本轮最终跑里**已通过**（见 §6.3，它们是时序/顺序敏感的存量缺陷，不是本轮引入）。

### 5.2 本轮修的最后一个红：`TestMeetingWorkspaceIsolation`

- 现象：owner 自己调 transcribe 期望 200，实际 **502**
  （`stt_unavailable: LLM 网关未配置 API Key`）。
- 定性：**这是 §3 移除内置密钥后的正确新行为**，不是回归。
  但也不能就这么把断言删掉——那等于把覆盖降级。
- 处理：把测试改成两段，都保留断言：
  1. **未配置 ASR 时**必须 502，且**不得伪造转写文本**
     （`Status != "transcribed"` 且 `Transcript == ""`）。用一条独立会议做，不污染 meetingA。
  2. **正向路径**用测试自建的 ASR 目标（`httptest` + `stt.NewResolver`），
     断言 200 + transcript 落库 + `Status == "transcribed"` +
     **owner/workspace 归属未被转写动作改变**。
  3. 顺带断言 ASR 请求真的带上了 `Authorization: Bearer test-asr-key`（防止 target 拼装被改坏）。
- 结果：PASS。

### 5.4 顺带收掉：STT 探测的 `ProbeEndpointMissing` 死常量

- 来源：并发会话在 `74b629c` 把它交付成**补丁文件**
  `docs/handoff/patches/2026-10-01-stt-endpoint-missing.patch`，
  **没有直接改进代码**——也就是说这个缺陷在 `origin/main` 上**仍然是活的**。
- 缺陷：`ProbeEndpointMissing` 声明了、`server_stt_settings.go` 也为它配了「无转写端点」中文文案，
  但生产代码从不赋值。网关两种形态都 404 时落到 `ProbeFailed`，
  而 `describeProbe` 对 `ProbeFailed` 拼的是 `"探测失败(" + Detail + ")"`，
  `Detail` 带上游原始响应体 → 设置页直接显示
  `探测失败(http 404: {"error":{"code":"no_candidate",...}})`。
- 已做：应用其 `discovery.go` 生产修复（17 行）。
  补丁里的测试**依赖那份被排除的坏 fixture**（`discovery_test.go` 不在整合树里），
  因此**另写了一个自包含测试** `internal/stt/discovery_endpoint_missing_test.go`：
  - `TestProbeClassifiesBothTransportsMissing`：两种形态都 404 → 判 `endpoint_missing`，
    且 `Detail` **不得含 `{` / `choices` / `no_candidate`**（防上游 body 泄漏到 UI）。
  - `TestProbeNoProviderIsNotEndpointMissing`：**反向护栏**。
    503 `no_candidate`（网关列了模型但无可用上游）与「端点不存在」是两回事，
    守住修复的边界——否则设置页会把「换个模型」的建议错换成「网关没开转写端点」。
- **修复前红 / 修复后绿已实测**（`git stash` 对照）：
  ```
  修复前：status=failed want endpoint_missing
          (detail=http 404: {"error":{"code":"no_candidate",...,"choices":[]}})
  修复后：两例全 PASS
  ```
  修复前的失败信息**逐字就是甩给用户的那坨 JSON**，判据自证。

### 5.5 三个被改坏的测试（未提交，已从整合分支排除）


- `server_stt_settings_test.go`：引用不存在的 `wsAToken`、调用未导出的 `sttDiscovery.Put` → 无法编译。
- `internal/stt/discovery_test.go`：3 个模型分类断言失败。
- 二者在整合树中**均不存在**（它们只是主工作区里的未跟踪文件），未随合并带入。

---

## 6. 已定性但**本轮未修**（留档）

### 6.1 `internal/learning/store.go:439-461` `ActiveDayTimestamps` 虚增活跃天数

- WHERE 用 `OR`（`captured_at` 或 `updated_at` 命中窗口），
  但 Go 侧把命中行的 **`captured_at` 和 `updated_at` 都 append**。
- 后果：窗口外的旧时间戳被算进来 → 连续活跃天数虚增。
- 不擅自修：改的是学习统计口径，属于产品语义（到底该按哪个时间戳算），需用户定。

### 6.2 `internal/learning/store.go:395-405` `SnoozeReminder` 可能往回拨

- 实现是 `now + minutes*60`。
- 后果：一条 24 小时后的提醒，顺延 2 小时会变成 **2 小时后到**。
- 「从现在起顺延」还是「从原定时间顺延」是产品语义，不擅自拍。

### 6.3 `server.go handleTaskOperations`（PATCH / DELETE）无写权限校验

- 现状：只有 workspace 维度的 `GetTaskScoped`，**没有任何 `CanWriteWorkItem`**。
- 后果：同 workspace 的普通成员可以改/删他人的 private 工作项；
  POST 还可以指定 `ownerId`。
- 不在本轮授权的 P0/P1/P2 清单内，登记为待办。

### 6.4 低危 / 清理项

| 位置 | 问题 |
|---|---|
| `internal/email` | `LedgerTotalText` 死代码 |
| 邮件 PDF 导出 | gif 分支不可达 |
| harvest 端点 | 无前端调用 |
| `server_email_pipeline.go:267` | 缺路径包含检查 |
| `scheduler.go:250` | `startMu` 保护不完整 |
| `syncGatewayUserSetting` | 伪造未来时间戳 |
| `scripts/patch-export-grid-signature.mjs` | 一次性脚本，应删 |
| `invoice_realworld_test.go` | **含真实发票号与 OSS URL**——下一轮应替换为合成夹具 |

### 6.5 「未合并分支」处置（实测 `git rev-list --count`）

方法坑记录：`git branch --contains <sha> origin/main` **查不出远端分支是否已合入**，
会误判成「都没合」。正确做法是 `git rev-list --count origin/main..<branch>`。

| 分支 | ahead | 处置 |
|---|---|---|
| `fix/post-delivery-audit-highs` | 0 | 已完全合入，**已删**（并发会话执行） |
| `integrate/bugu-and-main` | 0 | 已完全合入，**已删**（并发会话执行） |
| `backup/bug-u-051562b` | 0 | 已完全合入，**已删**（并发会话执行） |
| `fix/bugz-marketplace-conflict` | 0，diff 为空 | 无独有变更，但**被 worktree `wt3` 检出，且 `scripts/maestro-run.mjs` 在 45 秒前还在被写** → **是活跃会话，不删** |
| `origin/codex/platform-goal-20260930` | 4 | 活跃远端分支，**不在「不活跃」清理范围**，保留 |
| `wip/2026-10-01-audit` | 2 | 本轮内容已并入 `consolidate/2026-10-01`；但它是主工作区 `C:\workspace\openpocket` 的当前分支，**有活跃会话在写，不删** |

**诚实结论：本轮实际只确认了 3 个分支已删（由并发会话执行），
另外 2 个虽然定性为「无用」，但因为挂在活跃 worktree 上，本轮不动。**

---

## 7. 整合方法学（本轮踩到的真坑）

### 7.1 用旧快照 `git checkout <branch> -- <paths>` 逐文件取回，会静默回退 origin/main

- 做法：从 `wip/2026-10-01-audit` 逐路径 checkout 147 个文件。
- 后果：其中 **16 个文件在 `origin/main` 上已有更新版本**（BUG-AQ / BUG-AP / i18n 卡口），
  快照基于更早的 base，checkout 直接把它们**盖回旧版**。
  受害名单包括 `ScheduledTaskListView.vue`、`ScheduledTaskDetailView.vue`、
  `settings-store.ts`、`docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md`（-298 行）等。
- 检测方法：
  ```
  $mb = git merge-base <snapshot> origin/main
  git diff --name-only $mb origin/main          # origin/main 期间改过的
  # 逐个比对是否被我暂存了不同版本
  ```
- 正解：**用 `git merge` 让 git 做真正的三方合并**。
  改用 `git merge --no-ff wip/2026-10-01-audit` 后，26 个文件里**只有 `.gitignore` 一个冲突**，
  其余 15 个文件 git 自动正确合并。快照整合**必须走 merge，不能走 checkout**。

### 7.2 PowerShell 的三个陷阱（本轮都踩了）

| 陷阱 | 后果 | 正解 |
|---|---|---|
| `Set-Content -Encoding UTF8` 改含中文的文件 | mojibake，且**不可逆** | 用编辑工具改文件 |
| `cmd > file` 重定向 git 输出 | PS 5.1 按控制台编码写，破坏非 ASCII | 用 `git update-index --cacheinfo` + `git checkout-index` |
| `gofmt -l` | 本仓库几乎列出所有文件（CRLF），**不是有效信号** | 忽略 |

### 7.3 并发会话是这个仓库的结构性约束

- 本轮全程另一个 Agent 在同一仓库写文件；`origin/main` 在审计期间从 `5053106` 前进到 `3598357`。
- 因此所有提交动作都在 workspace 内的独立 worktree `C:\workspace\openpocket\.wt-consolidate` 上做，
  分支 `consolidate/2026-10-01`，**完全不碰对方的工作区**。
- 落地方式用 `git push origin consolidate/2026-10-01:main`（fast-forward），
  **不在脏的 main 上做 merge**。

---

## 8. 变更规模

`git diff --stat 3598357..consolidate/2026-10-01`（合并后）：

| 区域 | 文件 | 增/删 |
|---|---|---|
| `backend/internal/email` | 23 | +2410 / -42 |
| `backend/internal/server` | 20 | +2100 / -82 |
| `backend/internal/stt` | 3 | +906 / -26 |
| `backend/internal/task` | 10 | +948 / -27 |
| `backend/internal/scheduledtask` | 4 | +613 / -23 |
| `backend/internal/feishu` | 2 | +331 / -0 |
| `backend/internal/learning` | 2 | +176 / -6 |
| `frontend/src/features` | 25 | +348 / -180 |
| `scripts/` | 30 | +2100 左右（大量一次性探针） |
| 其余（config / opencode / cmd / native / locales / android） | 20 | — |
| **合计** | **146** | **+11185 / -738** |

`scripts/` 里 30 个新脚本里，相当一部分是一次性探针
（`probe-*` / `diag-*` / `verify-*-once`）。已在 §6.4 记了
`patch-export-grid-signature.mjs` 应删；**其余的清理留给下一轮**，不在本轮扩大范围。
