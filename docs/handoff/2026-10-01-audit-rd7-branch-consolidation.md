# 2026-10-01 审计轮（rd7）：分支收敛 + STT 并入修复

> 本轮主线：把 24 小时内散落在各分支/工作树里的改动收敛回 main，编译测试，
> 逐条批判审计，修掉暴露出的真实缺陷。
>
> **本轮开始时仓库里同时有另一个会话在同一个工作树上写代码**（邮件流水线
> BUG-AU~AX）。本轮所有提交都用显式路径 `git add -- <文件>`，不使用 `git add -A`；
> 涉及 email/ 的未提交改动一律不碰。

## 0. 结论速览

| 项 | 结果 |
|---|---|
| 并入分支 | 2 个（origin/main 的 PKM 修复、feat/2026-10-01-stt-service） |
| 删除分支 | 7 个（6 个已合入 + 1 个内容被完全吸收） |
| 移除 worktree | 2 个干净的（`.wt-consolidate`、`openpocket-wt-snippet`） |
| 挖出并修复的真实缺陷 | 3 个（见 §2） |
| 修掉本轮自己引入的断裂 | 1 个（§3） |
| 后端 | `go build ./...` 通过；`go test ./...` 52 包全绿 |
| 前端 | `typecheck` 通过；STT 相关 28+22 例测试通过 |
| 未并入 | `origin/codex/platform-goal-20260930`（5 处冲突，见 §5） |

## 1. 分支审计：逐个判定

判定口径不是「提交在不在 main 的拓扑历史里」，而是**内容是否已被 main 吸收**。
`feat/2026-10-01-stt-recover` 就是反例：它不是 main 的祖先，但逐文件比对后
12 个文件里 10 个字节一致、另 2 个是本轮修过的更正确版本 —— 内容已被完全吸收。

### 已删除（7 个）

| 分支 | 判定依据 |
|---|---|
| `consolidate/2026-10-01` | main 的祖先，worktree 干净 |
| `docs/round5-addendum` | main 的祖先 |
| `feat/email-list-motion-and-body-render` | main 的祖先 |
| `fix/email-snippet-callsites` | main 的祖先，worktree 干净 |
| `fix/windows-test-enablement` | main 的祖先 |
| `wip/2026-10-01-audit` | main 的祖先 |
| `feat/2026-10-01-stt-recover` | 内容被 `stt-service` 完全包含（见上） |

### 保留（4 个，**都不是因为「懒得删」，是因为删了会丢东西**）

| 分支 | 保留原因 |
|---|---|
| `feat/2026-10-01-stt-service` | 其 worktree `openpocket-wt-stt` 有**大量未提交工作**：流式/增量转写（`server_stt_stream.go`、`full.go`、`incremental.go`）、`stt-presentation.ts`、voice-prompt 测试等。删 worktree = 销毁这些。 |
| `fix/bugz-marketplace-conflict` | 其 worktree `wt3` 有未提交改动（`.maestro/flashcards-write.yaml`、`scripts/maestro-run.mjs` + 一批 diag 脚本）。 |
| `email-pipeline-snapshot-2026-10-01` | 并发会话正在使用的快照分支（已推进到快照 #15），是它对抗 `git clean` 的保护手段。 |
| `audit-snapshot-rd7` | 本轮建立的救援快照。**注意它已过时**（见 §6）。 |

## 2. 并入 STT 分支时挖出的三个真实缺陷

并入 `feat/2026-10-01-stt-service`（3381 行，含被 `git clean` 卷走后恢复的
12 个文件）后，`stt` / `server` 两包直接编译不过。修的过程中挖出三个此前
没人发现的缺陷。

### 2.1 `Server.sttHTTPClient` 是个从不生效的死字段

字段声明着，注释还承诺「测试注入一个拒绝出网的实现，保证单测不打真实网关」，
但**全仓库没有任何代码读它**：`discoverGatewayASR` 与 `/api/stt/probe`
各自硬编码 `gatewayHTTPClient(...)`。

两个后果：

1. 恢复出来的 `server_stt_settings_test.go` 依赖 `SetSTTHTTPClient`，
   而那个 setter 从来就不存在 → 整包编译不过。
2. 那条注释承诺的「单测不真实打网关」**从来没有兑现过**。

修：新增 `sttHTTPClientOr(timeout)`，两条出网路径统一走它。回落分支是必须的
——生产（`cmd/pocketd`）从不注入该字段，直接返回字段值会让出网拿到 nil 客户端
而 panic。

### 2.2 `GET /api/stt/config` 在探测缓存冷时必定 500（用户可见）

`resp.Gateway` 只在缓存命中时才非 nil，而 `Best()` 是**值接收者**：

```go
resp.Gateway = &res          // 仅缓存命中时执行
...
if best, ok := resp.Gateway.Best(); ok {   // 缓存冷时 nil 解引用 → panic
```

panic 被中间件兜成 `500 internal server error`。命中条件不是边缘情况：
**首次打开语音转写设置页、纯外部通道用户、网关没配 key** —— 全部落在这个分支。
也就是说语音转写设置页在多数情况下打不开。

修：补 nil 判空。抓它的是恢复出来的 `TestSttConfigListsBothRecommendedGroups`。

### 2.3 `missingAudioRe` 漏掉 `don't see`（静默数据污染）

原正则覆盖 `didn't receive/get/hear` 与 `not see`，却漏掉网关最常见的一句：

> I don't see any audio file attached to this message.

这**正是该函数存在的目的**要拦的幻觉回复。漏掉意味着它被当成转写结果写进
会议记录，而且不报任何错。补 `don'/do not/does not` 形式 + `see|find|detect|listen`。

## 3. 本轮自己引入的断裂：恢复提交漏了一个文件

`2340f3f` 声称「恢复 12 个被 git clean 清掉的 STT 未跟踪文件」，实际漏了
`recording-voice-prompt.ts`。而 `recordingRuntime.ts` 已经 import 它 —— 该文件
当时只以**未跟踪文件**的形式活在 `wt-stt` 工作树里，从未进入任何提交。

后果：并入后 main 上 `npm run typecheck` 直接失败

```
src/native/recordingRuntime.ts(49,8): error TS2307:
  Cannot find module './recording-voice-prompt'
```

**这次并入在类型检查层面是坏的**，`vite build` 同样过不去。已从 `wt-stt`
取回模块本体入库（`e5e3d85`）。

> **教训**：「恢复被 git clean 卷走的文件」这类提交，恢复者手里没有清单，
> 只能凭记忆列出自己写过的文件名。**引用方进了仓库、被引用方没进**是最容易漏
> 的一种，因为编译器要到 typecheck 才报。合并任何「恢复型」分支后，
> 必须跑完整构建，不能只看 `go build` / 局部测试。

### 测试文件的一处有意截断

`wt-stt` 里的 `recording-voice-prompt.test.mjs` **不是本模块专属**：它混入了
「录音停止链路的转写能力」「即时转写接线」两组源码级断言，测
`transcribeFull` / `sliceChain` / `stt-filename.ts` —— 那是 `wt-stt` 上仍在推进的
流式与增量转写工作流，代码不在 main 上。原样入库会得到 12 个必红用例。

因此只保留前 6 个 describe（22 例全绿），被截掉的两组留在 `wt-stt` 原处，
等流式工作流落地时随其一起进入 main。

## 4. 测试与验证

| 命令 | 结果 |
|---|---|
| `go build ./...` | 通过 |
| `go test ./...` | **52 包全绿，0 FAIL** |
| `npm run typecheck` | 通过（修 §3 之前是 TS2307 失败） |
| `MOBILE_ALLOW_EMPTY_API_BASE=1 npm run build` | 通过，`✓ built in 17.03s` |
| `node --test` STT 三件套 | 28/28 |
| `node --test` recording-voice-prompt | 22/22 |

> `npm run build` **不加环境变量会失败**，但那不是回归：`vite.config.ts` 里有一道
> 故意设置的安全闸，production 模式下 `VITE_API_BASE` 为空就拒绝构建（防止移动端
> bundle 静默回落到 WebView 同源、所有 `/api` 请求拿到 index.html 而不是 JSON）。
> 报错信息自己给出了 Web 同源部署的合法绕法。加上该变量后完整构建通过。
> 这道闸要保留，别为了「让 build 变绿」去删它。

### 负控（防止「假绿」）

- **§2.1**：把 `/api/stt/probe` 改回硬编码 `gatewayHTTPClient` 后，
  `TestSttProbeUsesRecordedAudio` **立刻转红** → 证明该修复承重而非空转。
  改回后复测转绿。
- **§2.2 / §2.3**：均已实测「红 → 修 → 绿」（修复前测试直接失败：
  `GET /api/stt/config status=500`、英文 don't see 用例不匹配）。

> 这里踩了一个坑：用 PowerShell 做「改文件 → 测 → 还原」时用了
> `Get-Content -Raw` + `WriteAllText` 往返，**PowerShell 5.1 默认按 ANSI 读文件，
> 把 `server_stt_settings.go` 里的中文注释全写成了乱码**，编译直接坏掉。
> 只能用 `git checkout --` 恢复后重做。**在本仓库做文件往返一律用文件工具
> 或 `node`，不要用 PowerShell 的 Get-Content/Set-Content。**

## 5. 未并入：`origin/codex/platform-goal-20260930`

**判定：不并入，但保留，且这是下一轮的第一优先项。**

它是 5 个提交（2026-09-30 16:43 ~ 2026-10-01 06:22）的部署工作流，
自带 342 行测试，**不碰 email/stt/pkm**，与本轮的混乱区域零重叠 —— 质量上是好的。

不并入的依据是实测的两条硬约束，不是「看起来有风险」：

1. **5 处真实冲突**（`git merge-tree` 干跑，零副作用）：
   `deploy-local.sh`、`deploy/bin/lib/database-detect.sh`、
   `frontend/package.json`、`frontend/package-lock.json`（502 行）、
   `frontend/src/config/api-base.ts`。
   它从 28 个提交前的基线分出来，解冲突需要人工判断，不能自动合。
2. **它的测试在本机跑不了**：`test_database_detect.sh` / `test_frontend_stop.py` /
   `deploy-integration-test.sh` 依赖 bash（本机无 bash）与 Docker 编排。
   也就是说合进去之后**无法在本轮给出任何验证证据**。

下一轮配方：`git merge origin/codex/platform-goal-20260930` →
逐个解 5 处冲突（lockfile 建议 `git checkout --theirs` 后重跑
`npm install` 重新生成，别手改）→ 在有 bash/Docker 的环境跑
`deploy/bin/tests/*` 与 `tests/deploy-integration-test.sh` → 再合。

## 6. 遗留风险（重要）

### 6.1 邮件流水线的在途改动仍未提交

工作树里 email/ 下仍有大量未提交改动（`fetcher.go`、`mime.go`、
`pop3_fetcher.go`、`scheduler.go`、`invoice_harvest.go`、`pipeline.go`、`spam.go`
+ 9 个新测试文件 + `body_cache.go`），对应 handoff `2026-09-30-email-pipeline-verify.md`
的 §7c~7j（BUG-AU/AT/AV/AW/AX）。**那是并发会话的工作，本轮刻意没有碰。**
它们由 `email-pipeline-snapshot-2026-10-01` 保护（已到快照 #15）。

### 6.2 本轮的救援快照已过时

`audit-snapshot-rd7`（3f67491）建立于 06:47，之后并发会话在 06:48 把 handoff 文档
恢复成了更完整的版本、06:59:49 又把 5 个 STT 文件整体回退。
**该分支保存的是 06:47 时刻的状态，不含后续演进**，`git clean`/`git checkout`
清不掉它，但不要把它当成「当前工作树」的镜像。

### 6.3 同一个工作树上仍有两个会话

本轮实测：06:59:49 并发会话把我已修好并跑绿的 5 个 STT 文件**整体回退**到合并态
（我的三处修复与两个测试辅助函数全部消失），随后本轮重做并**立即提交**才固定下来。

> **给后续轮次的硬规则**：
> 1. 只用 `git add -- <显式路径>`，**永远不要 `git add -A`**。
> 2. 修完立刻提交。**未提交的修改在这个仓库里活不过一轮对话**。
> 3. 提交前后用 `git log --oneline HEAD..origin/main` 与 `git status` 复查
>    是否被他人抢先提交/推送；push 被拒先 fetch 看远端新增了什么。
> 4. 不动 `wt-stt` / `wt3` 两个 worktree，它们有未提交工作。

## 7. 下一轮提示词

```
审计第 8 轮。请严格按以下顺序做，不要自由发挥：

1) 先 git fetch，读 git log --oneline HEAD..origin/main 与 git status --porcelain，
   确认有没有别的会话已经抢先提交/推送。若 push 被拒，先 fetch 看远端新增提交
   与你的改动是否重叠，无冲突就 merge 后再推。

2) 头号任务：合并 origin/codex/platform-goal-20260930。它有 5 处冲突
   （deploy-local.sh、database-detect.sh、package.json、package-lock.json、api-base.ts），
   必须逐个人工解；package-lock.json 不要手改，checkout 后重跑 npm install 重新生成。
   合完在有 bash/Docker 的环境跑 deploy/bin/tests/* 与 tests/deploy-integration-test.sh，
   给出真实测试输出再提交。注意本机没有 bash。

3) 第二号任务：邮件流水线那批未提交改动（email/ 下 fetcher.go、mime.go、
   pop3_fetcher.go、scheduler.go、invoice_harvest.go、pipeline.go、spam.go、
   body_cache.go 及 9 个测试）。审一遍 spam 弱词分级（§7e 明确「没有把阈值降到
   30，订阅 newsletter 算不算垃圾是产品判断」——不要自己替用户做这个决定），
   然后提交。注意：这些是另一个会话的工作，先确认它已经收工。

4) 第三号任务：wt-stt 的流式/增量转写工作流（server_stt_stream.go、full.go、
   incremental.go、stt-presentation.ts、recording-voice-prompt.test.mjs 被截掉的两组
   describe）。它的 discovery_test.go 与 discovery_endpoint_missing_test.go 存在
   重构中途状态（它删了后者、前者里有重名副本），合入时注意与我这一轮
   删重名的处理保持一致。

5) 硬规则：只用 git add -- <显式路径>，永远不要 git add -A；修完立刻提交；
   不动 wt-stt 与 wt3 两个 worktree；不要用 PowerShell 的 Get-Content/Set-Content
   做文件往返（会把中文写成乱码），用文件工具或 node。

6) 收尾输出：结论/根因、改动文件与关键行为、测试命令与真实结果、遗留风险、
   更新 handoff、下一轮提示词。
```
