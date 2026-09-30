# 2026-10-01 审计轮第五轮：Windows 测试可运行性修复 + 并发写入现状

> 结论先行：本轮**没有**合并任何并发会话的在途改动。仓库当前有 6 个活跃会话共用
> `C:\workspace\openpocket` 一个工作区，实测三处同时在写。本轮改为：在独立 worktree
> 对 `origin/main` 本身做体检，修掉 17 个存量红的根因，并把已提交的 P0 推到远端保命。

## §1 本轮最重要的发现：并发写入（不是代码问题，是协作问题）

按 cron 提示词原本要「把 95 个未提交文件全并入 main 并推送」，实测**不可行**：

| 位置 | 证据 | 判定 |
| --- | --- | --- |
| 主工作区 `frontend/dist/` | 03:47:15 vite 构建产物连续写入 | 有会话正在构建 |
| `.wt-consolidate/backend/internal/server/server.go` | 03:48:24 被改（正是那 5 个合并冲突文件之一） | 冲突正在被解 |
| `wt3/logs/*.log`、`wt3/.maestro/_unlock-focus-probe.yaml` | 04:18:56（报告时刻前 10 秒）仍在写 | 真机测试在跑 |

`mavis session list` 显示 6 个 session 处于 started/idle 且 `workspaceDir` 全是本仓。
主树 95 个文件 / +2343 行未提交改动分属三个主题，都不是本会话的产物：

- 邮件解析与发票展示（`EmailDetailView.vue`、`server_email_summary.go`、`body_cache.go`…）
- STT 网关与设置（`server_stt_settings.go`、`stt/discovery.go`、`SettingsSTT.vue`…）
- Android 文档内打开（`DocumentPlugin.java`、`usePdfViewer.ts`、`verify-doc-inapp*.mjs`…）

> **教训**：本仓库 09-30 与 10-01 已两次因「并发提交同一 main」出现重复提交与 push 被拒。
> 本轮按用户决策只做安全子集，没有制造第三次。

### 1.1 分支处置结果

| 分支 | 状态 | 本轮动作 |
| --- | --- | --- |
| `fix/bugz-marketplace-conflict` | 已验证是 `origin/main` 的**严格祖先**（`merge-base --is-ancestor` 退出 0，未合并提交数 0） | **未删**。分支本身可删，但其 worktree `wt3` 在 04:18:56 仍在被写，删 worktree 会打断在跑的真机测试。留待 `wt3` 闲置后清理。 |
| `consolidate/2026-10-01` | `0c128c9` 已被其属会话并入 main（`origin/main..该分支` 为空） | 已 `git push -u origin consolidate/2026-10-01`，在远端留了分支锚点。 |
| `wip/2026-10-01-audit` | 本会话所在分支，落后 main 19 | 未动（其快照提交 `88cb5a2` 已被后续会话取代）。 |

## §2 `origin/main` 体检结果（独立 worktree，基线干净）

体检对象：`9fc115c`（轮次开始时的 main）。用 `git worktree add C:\workspace\wt-audit origin/main --detach` 隔离，不碰任何活跃会话的树。

| 检查 | 命令 | 结果 |
| --- | --- | --- |
| 后端编译 | `go build ./...` | ✅ exit 0 |
| 后端 vet | `go vet ./...` | ✅ exit 0 |
| 前端类型 | `npx vue-tsc --noEmit`（实际用 `node node_modules/vue-tsc/bin/vue-tsc.js`，npx 被 PowerShell 执行策略拦） | ✅ exit 0 |
| i18n 卡口 | `node scripts/check-i18n-keys.mjs` | ✅ 8 语言 243 key 齐平 |
| ViewModel 缺口 | `node scripts/check-viewmodel-gaps.mjs` | ✅ 命中 0 |
| 明文后端守卫 | `node scripts/assert-no-plaintext-backend.mjs` | ✅ 通过 |
| 后端全量测试 | `go test ./...` | ❌ **17 红** → 本轮修完 **0 红** |

### 2.1 17 个红的根因（逐个查证，不是"环境问题"了事）

四个独立根因，**全部是测试自身的缺陷或平台不可断言**，不是产品代码 bug：

1. **`buildAgentEcho` 产物缺 `.exe`**（影响 8 个用例：7 个 `TestStdioTransport_*` + `TestACPStdioAdapter_SubscribeEvents`）
   `os/exec` 在 Windows 上对「存在但无扩展名」的绝对路径直接返回
   `executable file not found in %PATH%`。实测对照：同一个 `go build` 产物，
   `agent_echo` 报 LookPath 失败，`agent_echo.exe` 正常执行（打印 usage，exit 2）。
   → **真修复**，非跳过。改 `agentecho_helper_test.go` 补 `.exe`。
   背景：`agentecho_helper_test.go` 头部注释记载，前一个会话为修 Linux CI 的
   `exec format error` 已改成「从源码现编译」，但没覆盖 Windows 扩展名这一层。

2. **pi 适配器夹具是 `/bin/sh` 脚本**（8 个 `TestPiAdapter_*`）
   `fake-pi.sh` / `fake-pi-slow.sh` 在 Windows 报 `%1 is not a valid Win32 application`。
   → Windows 跳过，**语义由 Linux CI 覆盖**。CI 定义
   `.github/workflows/backend.yml`：`runs-on: ubuntu-latest` + `go test -race ./... -count=1`，
   所以 Windows 跳过不会掩盖任何被卡口的回归。

3. **`crypto_atomic` 断言 POSIX 权限位**（1 个）
   Windows 无权限位，`os.Chmod(0600)` 是 no-op，`Stat` 恒 0666（实测 got 666）。
   → 只把**权限位那一条**收窄到非 Windows，密钥内容与原子落盘的断言仍全平台生效。

4. **`TestFetchPOP3MailboxAuthRejected` 假服务器抢跑 close**（1 个）
   原实现一次性写三条响应后立刻 `conn.Close()`；客户端还有未读命令时 Windows 发 RST，
   客户端写操作拿到 `wsasend WSAECONNABORTED`，于是这条用例测到的是传输层错误，
   而不是它想断言的「服务端 -ERR 状态行被透传」。
   → 改成与同文件 `handle()` 一致的「先读后写」节奏，读干到 EOF 再关。

## §3 验证（含负控对照）

修复后：`go test ./...` → **FAIL count 0**。

稳定性不是靠单次绿灯断言的：

```powershell
go test ./internal/agent/ -count=3     # ok，10.5s
go test ./internal/email/ -count=5     # ok，7.9s
```

逐用例去向（`go test ./internal/agent/ -v`），确保是「真跑」而不是「被吞掉」：

- **8 个 PASS**（真修复换来的覆盖）：`TestStdioTransport_{StartClose,CallEcho,Notify,CallAfterClose,CallTimeout,SpawnProcess,SendMalformedFrame}` + `TestACPStdioAdapter_SubscribeEvents`
- **8 个 SKIP**（bash 夹具）：`TestPiAdapter_{SendPromptHappyPath,SendPromptResume,ProviderErrorExitZero,NonZeroExit,CreateSessionPendingAlias,HealthCheck,SubscribeCleanup,InterruptSession}`
- `TestStdioTransport_StartInvalidPath` 仍 PASS——错误路径没被 `.exe` 改动带偏

**负控对照**：把 POP3 假服务器返回的 `-ERR Unable to log on` 改成
`-ERR something else entirely`，用例如期转红：

```
pop3_fetcher_test.go:155: want server -ERR message surfaced, got PASS rejected:  something else entirely
```

证明断言本身仍然在生效，不是把传输错误消掉后顺便放过了检查。

## §4 改动文件

| 文件 | 改动 |
| --- | --- |
| `backend/internal/agent/agentecho_helper_test.go` | Windows 下产物补 `.exe`（真修复，救回 8 个用例） |
| `backend/internal/agent/adapter_pi_test.go` | `writeFakePi` 与 `TestPiAdapter_InterruptSession` 加 Windows 跳过（后者绕过了前者，需单独守） |
| `backend/internal/email/crypto_atomic_test.go` | 权限位断言收窄到非 Windows，其余断言不动 |
| `backend/internal/email/pop3_fetcher_test.go` | 假服务器改「先读后写」，消除 RST 竞态 |

`backend/go.mod` 曾被 `GOFLAGS=-mod=mod` 顺带改动（`golang.org/x/image` 从 indirect 提为直接依赖），
已 `git restore` 还原，**不在本提交内**。

## §5 遗留风险

1. **`wt3` / `fix/bugz-marketplace-conflict` 未清理**。分支已验证是 main 的严格祖先、
   可以随时删，但 `wt3` 仍在跑真机测试，本轮不动。等该 worktree 闲置后：
   `git worktree remove C:\workspace\openpocket\wt3 && git branch -d fix/bugz-marketplace-conflict`。
2. **主树 95 个未提交文件仍未归属**。三个主题各自需要一个会话自行收口成提交；
   在此之前**不要**从别的会话推 main。
3. **`.exe` 修复只在本机 Windows 验证过**。Linux CI 走的是无扩展名路径，
   `runtime.GOOS == "windows"` 分支不会命中，行为与修复前完全一致——但这是推断，
   需要 CI 绿灯确认一次。
4. **pi 适配器的 8 个用例在 Windows 上零覆盖**。本机不再能发现其回归，
   只能靠 Linux CI。若后续有人改 `adapter_pi.go`，请在有 bash 的环境上验证。
5. `origin/main` 在本轮期间仍在前进（`9fc115c` → `514b082`），本轮结论以 `9fc115c` 为基线。

## §5.5 收尾时的仓库快照（提交后再次变化，如实补记）

`3303827` 推送成功并成为 `origin/main` 顶端。推送前后台又发生了这些变化，
**都不是本轮做的，也没有被本轮碰到**：

- `0c128c9`（P0 写权限 + 脱敏）与 `a1c4900` 已由其属会话并入 `origin/main`——
  §1.1 里"P0 已被并入 main"的判断得到确认，本轮推到远端的分支锚点只是冗余保险。
- 新增 worktree `C:\workspace\openpocket-wt-stt`，分支 `feat/2026-10-01-stt-service`
  （相对 main 3 个未合并提交）。**在途，未合并，勿动**。
- 主工作区未提交文件数从 95 降到 31——邮件主题已被其属会话收口成提交。
- 本地 `main` 相对 `origin/main` 为 3 behind / 1 ahead：那个 ahead 提交属于
  在途会话，本轮**没有代为推送**。
- `wt3` 仍占用 `fix/bugz-marketplace-conflict`，本轮按 §5-1 保留。

## §6 下一轮提示词

> 接着 2026-10-01-audit-round5 做：
> 1. 先 `git fetch` 看 `origin/main` 到了哪，再跑 `go test ./...` 确认 Windows 端仍 0 红，
>    并在 CI 上确认 Linux 端 `go test -race ./...` 仍绿（验证 §5-3 的推断）。
> 2. 检查 `wt3` 是否已闲置（`Get-ChildItem -Recurse wt3 | ? LastWriteTime -gt (Get-Date).AddMinutes(-30)`）。
>    连续 30 分钟无写入即可清理：移除 worktree 后删已合并的 `fix/bugz-marketplace-conflict`。
> 3. 确认主工作区还有几个会话活着。**只要还有一个会话在写同一棵树，就不要动那 95 个未提交文件。**
>    等它们各自收口后，再用 `git log --oneline HEAD..origin/main` 核对是否已被抢先提交。
> 4. 三个未收口主题的验收口径：邮件解析/发票、STT 网关、Android 文档内打开——
>    每项要有一条真机或端到端证据，而不是只有单测。
