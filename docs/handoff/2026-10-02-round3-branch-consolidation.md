# 2026-10-02 round3 —— 分支归并与「只在设了 DSN 时才运行」的护栏审计

> 触发：`/goal` 定时任务（拉主分支、清理不活跃子分支、批判性审计 24 小时内的提交与本地修改、提交推送）。
> 执行窗口：2026-10-02 00:46 – 01:15。
> 基线：本轮开始时 `origin/main = e079261`，本地 `main = e768a53`。

---

## 0. 一句话结论

本轮真正值得记的不是"合并了两个分支"，而是：**一批护栏只在设置
`POCKET_TEST_POSTGRES_DSN` 时才会执行，而不设 DSN 时 `go test` 照样打印
`ok`**。这让 `TestPGTestsNeverTargetTheProductionSchema`（专门防"测试打
到生产 schema"的护栏）在合并前是绿的，合并后才变红——而**这次合并的 25
个提交正是它转红的唯一原因**。

---

## 1. 根因

### 1.1 「全绿」的含义取决于环境变量，而默认环境恰好把最危险的一半关掉了

```
$ go test ./internal/server/ -v      # 不设 DSN
--- SKIP: TestPGAuditWriterFlows / TestC8_* / TestTaskWriteGuard* …（25 个）

$ POCKET_TEST_POSTGRES_DSN=postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable \
  go test ./internal/server/ -v
412 PASS / 0 SKIP / 1 FAIL          # 1 FAIL 就是下面 §2.1 那个护栏
```

两种跑法都合法，CI 只跑后者（`backend-pg.yml` 设了 DSN，`make test-pg` 跑
`go test ./... -race -count=1`）。**缺陷在于本地默认跑法给出的是残缺的绿**：
本机 `internal/email` 默认 74 个 SKIP、`internal/server` 25 个 SKIP，其中
包含本轮 25 个提交里几乎全部修复的回归测试（`TestHarvestOne_CachedRawStillCountsAttempt`、
`TestMarkRetry_ConvergesToFailedAfterMaxAttempts`、`TestInsertEmailConflictKeepsOldSnippetWhenNewIsEmpty`…）。

**给下一轮的口径**：凡是说"后端全绿"，必须同时给出
`PASS / SKIP / FAIL` 三个数。`ok` 与「全绿」不是同义词。

### 1.2 合并前绿 ≠ 合并后绿

`TestPGTestsNeverTargetTheProductionSchema` 扫描全仓 `_test.go`，要求任何
打开 PG 连接的测试都必须把 `search_path` 钉到自建的 `*_test_` schema。
`feat/mail-config-deploy` 新增的 3 个真库诊断探针没钉，于是：

- 在合并前的 main 上跑（不设 DSN）→ **PASS**（护栏本身也被同一条 DSN 门控住，根本没执行）；
- 合并后我第一次带 DSN 跑 → **FAIL**，点名 3 个文件。

这就是记忆里那条教训的又一次复现：**护栏自己有环境门控时，"绿"可能来自
"没跑"，而不是"没问题"**。

### 1.3 三个被判红文件其实安全（逐个核过才放行）

| 文件 | 写语句 | 门控 | 处置 |
| --- | --- | --- | --- |
| `diag_schema_present_test.go` | 0（`INSERT\|UPDATE\|DELETE\|DROP\|CREATE` 仅出现在注释，护栏本身会剥掉注释行） | `POCKET_REAL_MAIL_DSN` + `POCKET_DIAG_SCHEMA` | allowlist |
| `ledger_realdata_diag_test.go` | 0（只用 `pool.Query`） | `POCKET_REAL_MAIL_DSN` + `POCKET_REAL_MAIL_SCHEMA` | allowlist |
| `reminder_notified_diag_test.go` | 0（只用 `pool.Query`） | `POCKET_REAL_MAIL_DSN` | allowlist |

放行的可核查依据：**门控变量是 `POCKET_REAL_MAIL_DSN`，与测试用的
`POCKET_TEST_POSTGRES_DSN` 是两个不同变量，而 CI 只设后者**——所以这三个
文件在任何 CI 与常规 `go test` 中都 skip，不可能被误触发。它们要的就是读
真实 schema，自建隔离 schema 反而会让诊断输出"数据没了"的假结论
（`ledger_realdata_diag_test.go` 的注释记的就是这个坑）。

### 1.4 长请求白名单是同一类缺陷的三个实例

`longLivedPaths` 里 `http.Server` 的 `WriteTimeout=30s` 掐断长请求的故障，
本轮一次性收进三个来源，**症状完全一样**（服务端日志记 `200`，客户端收到
零字节），但分散在三条分支上：

- 邮件 `/api/email/pipeline/run`（实测 1m30.67s）
- 邮件 `/api/emails/invoices/extract`（单封发票补日期就可能超 30s）
- STT `/api/stt/{transcribe,probe,discover}`（79 秒会议录音实测 30.17s 被掐）

**踩坑点**：三条分支都改同一个 `server.go` 的同一个 slice，冲突两次都是
同一形状。若按"二选一"解冲突，会静默丢掉另外两条白名单——而症状是
"时好时坏、卡在 30s 边界"，极难归因。**解法是逐条保留两侧增量。**

---

## 2. 改动文件与关键行为

### 2.1 合并进 main 的分支

| 分支 | 提交数 | 冲突 | 处置 |
| --- | --- | --- | --- |
| `feat/2026-10-01-stt-service` | 10 | `server.go`、`server_stt_settings.go` | 两侧增量都保留，已并入 `83ecfa4` |
| `feat/mail-config-deploy` | 25 | `server.go` | 两侧增量都保留，已并入 `5e8d899` |
| `wip/pdf-document-plugin` | 0（worktree 未提交） | — | 逐文件提取 3 个 + 1 处接线，拒 1 处 |
| `origin/main` | 3 次快进 | 无 | 均为并行会话的新提交 |

### 2.2 关键行为变更

**`config.ResolveDataDir`（新）** —— 数据目录此前只有 `Dir(DBPath)` 一条路，
而 DBPath 在 PG 迁移后**不再真的开 SQLite**、默认值又是相对的
`./data/pocket.sqlite`，于是数据目录随进程 CWD 跑。同一根因咬过两次且
症状毫无关联：

- master key 落到别的目录 → 全部账户 `decrypt credential: cipher: message
  authentication failed`（两实例打出来的路径**看着一样**）；
- 发票采集写在 A 目录、下载按 B 目录 `os.Stat` → 单张 404、A4 导出 400。

现在按 `POCKET_DATA_DIR` > `Dir(DBPath)` 取值并一律转绝对路径。
`ResolveDataDir` 有 3 处真实调用点 + 专门的 `datadir_test.go`（含
`TestResolveDataDir_NotAffectedByCWD`），**不是死代码**（对照 `pickAPIKey` 那个教训）。

**`stt/full.go buildWAV`** —— RIFF 块长度此前多写 8 字节。宽容解码器
（PyAV/ffmpeg）默默截掉末尾 8 字节当作填充，**看起来一切正常**；严格的上游
API 直接拒收整段。

### 2.3 护栏改动

`backend/internal/server/pg_test_isolation_guard_test.go` 的 `pgSafeWithoutIsolation`
新增 3 条 allowlist，**每条都写明可核查理由**（不是"应该没事"）。

---

## 3. 测试命令与结果

```bash
# 后端编译
cd backend && go build ./...                    # 通过

# 后端全量（默认环境：PG 测试全部 SKIP）
cd backend && go test -race ./...                # 无 FAIL；53 包 ok

# 后端带 DSN（本轮新增的关键验证）
$env:POCKET_TEST_POSTGRES_DSN='postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable'
go test ./internal/email/ -v                    # 178 PASS/74 SKIP → 239 PASS/13 SKIP/0 FAIL
go test ./internal/server/ -v                   # 412 PASS/0 SKIP/1 FAIL → 修护栏后 0 FAIL

# 前端全量门禁
cd frontend && npm.cmd run gates                 # 通过；孤儿测试 147/147 无遗漏
node --test src/native/__tests__/document.test.mjs   # 6/6 通过
```

### 3.1 两个负控（证明护栏不是"永远绿"）

```
# 负控 1：造一个未豁免的开 PG 测试文件
→ FAIL，护栏精确点名 internal/email/negctl_probe_test.go
→ 删除后恢复 PASS

# 负控 2：把 buildWAV 的 RIFF 长度改回有缺陷的 +8
→ FAIL: 段 0 的 RIFF 块长度 = 77640，应为 文件总长-8 = 77632（差 8 字节）
        段 1 … 段 4 同样报出（逐段定位）
→ 还原后恢复 PASS，git diff 确认为空
```

### 3.2 两个"假绿"陷阱（本轮实际踩到）

1. **`npm` 解析到 `npm.ps1` 被执行策略拦下，但退出码是 0** —— 第一轮
   `npm run gates` 13 秒就"成功"结束，实际一个检查都没跑。必须用
   `npm.cmd`。**`$LASTEXITCODE=0` 不能证明命令跑过。**
2. **PowerShell 的 `cd` 不改 `[Environment]::CurrentDirectory`** —— 负控注入
   时用相对路径调 `[System.IO.File]::ReadAllLines`，异常被吞后继续，
   产出"注入成功"的假象（`NEGCTL_INJECTED=False` 却仍往下跑）。一律用绝对路径。

---

## 4. 遗留风险

### 4.1 明确决定**不做**的事，以及为什么

| 事项 | 决定 | 理由 |
| --- | --- | --- |
| 本机带 DSN 跑 `go test ./...` | **不做** | 本机 `opencode_pocket` schema **已存在**（被重启的 pocketd 重建）。记忆中记录过 `go test -race ./...` 带 DSN 清空该 schema 的事故。只对**已核实使用隔离 schema 夹具**的包（`internal/email`、`internal/server`）跑 DSN 测试 |
| `.wt-pdf` 对 `test_database_detect.sh` 的改动 | **不取** | 它让该套件在 Windows 整体 SKIP（9 个 case 变 0）。既有记录显示 Git Bash 下已 7/7（`7a3a7d7` 专门修好的），且上一轮审计已**拒绝过**同类回退。该改动的技术前提（MSYS 无法 exec 无扩展名 fake）**在本机无法验证——本机根本没有 bash**。用未验证的前提削覆盖面，不做 |
| `document.test.mjs` 加进 `package.json` 的 `test:native` | **不改** | 当前 main 的 `run-mjs-tests.mjs` 用 `src/**/*.test.mjs` glob，已自动覆盖（`check:test-coverage` 报 147/147）。改了会重复跑 |
| `scripts/start-pocketd-uiinspect.ps1` | **不入库** | 硬编码 `C:\workspace\openpocket` 绝对路径，且开启 `POCKET_DEV_AUTH=true` + `POCKET_AUTH_LEGACY_ONLY=true` 的 dev 旁路 |
| 删 `openpocket-wt-stt` worktree | **不删** | 里面有 **174 个未提交文件**（`last activity 23:52`）。删 worktree 会连带销毁。分支本身已 0 未合并提交，但 worktree 里的在制品是另一个会话的工作 |
| 合 `email-pipeline-snapshot-2026-10-01` | **不合** | 89 个未合并提交且**仍在被写入**（本轮观察到 00:46 → 01:04 连续推进） |
| 重建 `opencode_pocket` schema / 灌数据 | **不做** | 记忆明文：重建空 schema 会把"表不见了"变成"表空了"，应用侧症状一样但更难查 |

### 4.2 未经本轮验证的

- **`DocumentPlugin.java` 未编译过**。本机无 Android SDK，Java 侧只能靠 CI
  的 `android-assemble`（`./gradlew --no-daemon assembleDebug`，ubuntu）兜底。
  该任务**必然执行**（不设 `continue-on-error`），所以不是死代码路径。
- **`resolveFile` 接受绝对路径**（`p.startsWith("/")`），理论上可读 App 沙箱外
  的可读文件。当前前端是可信代码、调用方固定，风险可接受；记为待审项。

### 4.3 本轮清点但未解决的

- `go.mod`（**0 字节**，仓库根，2026-10-02 00:49 出现）—— 并行会话在错误
  目录跑 `go` 命令留下的污染。空 `go.mod` 会让根目录看起来像个 Go 模块。
  未删（可能仍在被写），未入库。
- `sec7bm_ins.txt`（0 字节，未跟踪，2026-10-01 22:36）—— 来源不明，
  已在 `2026-10-02-round2-dev-bypass-password.md` 记录，本轮仍未动。
- `openpocket-wt-maildeploy/backend/internal/email/snippet_partial_fetch_test.go`
  —— 未跟踪、**内容是乱码**（GBK/UTF-8 混淆，中文字符串已损坏）。意图看起来
  是覆盖"真实 IMAP `BODY[TEXT]` 的三种取回形态"（首行与正文之间无空行的
  完整 MIME 会被 `ParseMIMEMessage` 拆错），是个真问题，但这份文件不可用，
  未合入。建议由其作者用正确编码重写。

---

## 5. 下一轮提示词

```
继续 openpocket 的分支归并与护栏审计。重点：

1. 【最高优先】把「只在设了 DSN 时才运行的测试」变成显式可见。
   当前本机默认 `go test` 打印 ok，但 internal/email 有 74 个、
   internal/server 有 25 个 SKIP，含本轮几乎全部修复的回归测试。
   建议加一个「PG 门控普查」卡口：统计跳过数并在**跳过数增长**时转红，
   避免新测试默认 skip 混进绿里。
   跑 DSN 测试前先确认本机 opencode_pocket schema 是否存在——
   存在就不要跑 ./...（记忆里有清库事故）。

2. openpocket-wt-stt 有 174 个未提交文件（分属 8 个分支的 STT 会话）。
   需要其所有者先提交或明确放弃，才能安全删除 worktree 与分支。

3. email-pipeline-snapshot-2026-10-01 仍有 89 个未合并提交且在持续写入。
   等它停稳后逐文件归并，特别注意它与已合入的邮件白名单/config 改动重叠。

4. 修 snippet_partial_fetch_test.go 的编码，并补上 BODY[TEXT] 三形态的
   回归测试（首行与正文之间无空行时 ParseMIMEMessage 会拆错，
   正向断言必须"真的把正文还原出来"，否则钉掉修复后函数返回空壳仍全绿）。

5. 仓库根的 0 字节 go.mod 与 sec7bm_ins.txt 需要定性后清理。
```

---

## 6. 本轮未遵守的既有约定（自查）

- 用了 `npm` 而非 `npm.cmd`，得到一次退出码为 0 的假绿；
- 负控注入用相对路径调 .NET API，得到一次"NEGCTL_INJECTED=False 但流程继续"。

两条都已在 §3.2 记录，根因都是**判据/前置条件没被显式验证**，
而不是工具本身有问题。
