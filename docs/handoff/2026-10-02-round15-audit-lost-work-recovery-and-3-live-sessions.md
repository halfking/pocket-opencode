# 2026-10-02 · round15 审计：11 个 stash 里确实有丢失的工作，但只有 1 份值得救

> 本轮由 cron `/goal` 触发。**本轮没有向共享 `main` 提交任何东西**，理由见 §1。
> 所有验证都在独立 detached worktree（`C:\workspace\openpocket-audit`）里做的，
> 没有碰别人正在写的工作区。

---

## 0. 一句话结论

- 24h 内 334 个提交，`HEAD`（`47286a27`）在**干净检出**上 build / vet / test 全绿，
  数字与提交信息里声称的完全一致（ok 53 / FAIL 0 / 无测试 18 / SKIP 0）。
- 「清理不活跃分支」这一项是**空操作**：没有任何未合并分支可收编，也没有可删的分支。
  两个看起来像残留的东西都被证明已经完整包含在 main 里。
- 真正有价值的是第三项：**11 个 stash 里有 8 个含「从未进过 main」的文件**。
  逐个查证后，**只有 1 份值得救**（真机证据 handoff），另 2 个测试文件已被后来
  的会话以更好的形式重新落地，救回来反而是倒退。
- **没有推送**，因为有 3 个会话正在同一个工作区并发写，其中一个跑的是同一条 cron 任务。

---

## 1. 为什么本轮不提交、不推送（先说这个）

这不是谨慎，是有实测证据的。

| 证据 | 观测 |
|---|---|
| 文件 mtime | `backend/internal/wecom/crypto.go` = **18:47:34**，而我 18:46 开始工作 |
| 会话列表 | 3 个 `status=started` 且 workspaceDir 都是 `C:\workspace\openpocket`：<br>· `mvs_b9297fbb…`「Maestro 真机全项目测试与功能修复」<br>· `mvs_63728240…`「审计并清理不活跃分支，修正问题后推送主分支」← **与本轮同任务**<br>· `mvs_8a0f6bf8…`「完善邮件定时收取与发票处理需求」 |
| reflog | 我 `git worktree add HEAD` 时检出的是 `47286a27`，而 30 秒前 `main` 还是 `7c82b594` —— **有人在这 30 秒里提交了** |
| 未推送 | `git rev-list --left-right --count HEAD...origin/main` = `1  0`：`47286a27` 已提交**但没推送** |
| 在途代码 | `backend/internal/wecom/` 5 个文件全是未跟踪，且 `crypto.go` 仍在被改 |

此刻在共享工作区跑 `git add -A`，会把**别人写了一半的 wecom 包**扫进 main。
本仓库已经因为并发吃过两次亏：

- `0728aa11`：提交信息写「删了白名单 + 改了阿里云样本期望」，**两样都没做**——
  因为我对别人在途的文件跑了 `git checkout HEAD --`。
- 一次 push 被远端多出的 5 个提交拒绝。

所以本轮改为：**在独立 worktree 上产出可合并的成果，不碰共享 main。**

---

## 2. 验证：HEAD 是真的绿（干净检出，不是脏工作区）

这一项专门针对「绿灯只对工作区成立」这个坑：在
`git worktree add C:\workspace\openpocket-audit HEAD --detach` 的**干净检出**上跑。

| 命令 | 结果 |
|---|---|
| `go build ./...` | **exit 0** |
| `go vet ./...` | **exit 0** |
| `go test ./... -count=1` | **exit 0** — ok **53** / FAIL **0** / 无测试 **18** / SKIP **0** |

对照 round14 提交信息里的声称（「ok 53 / FAIL 0 / 无测试 18」）：**数字逐项吻合**。
这条很重要——round11/round12 栽过的坑正是「白名单条目留在工作区没提交，
HEAD 上其实是 FAIL」。**round14 没有重蹈覆辙。**

### 2.1 我自己做的负控（不采信它自己的负控）

round14 声称护栏规则 4 有承重能力。我不想只凭它自述，所以在**独立 worktree** 里
复现了同一注入（用**能编译**的代码，否则红可能来自编译错误而不是护栏）：

```go
// 注入到 internal/email/diag_credential_health_test.go
func zzNegControl(pool *pgxpool.Pool, ctx context.Context) {
	_, _ = pool.Exec(ctx, "DELETE FROM email_accounts")
}
```

结果 **`exit 1`**，护栏点名该文件并给出两条修法；扫描了 457 个 `_test.go`。
**独立确认：规则 4 有承重能力。** 注入已撤销（`git status` 已干净）。

> 顺带记一个本次踩到的环境坑：`go test` 的输出经 PowerShell 读出来是
> `鍙涓缓` 这类乱码——那是 **GBK 控制台渲染 UTF-8**，不是文件坏了。
> 判据要引用的标识符（`pgAllowlistedWrites`、`DELETE FROM`）本身是 ASCII，不受影响。
> 需要可靠读中文日志时用 `cmd /c "... > file"` 再按 UTF-8 读。

---

## 3. 分支审计：这一项是空操作（且差点被误当成有活干）

| 目标 | 结论 |
|---|---|
| `git branch -a --no-merged main` | **空**。没有任何未合并分支。 |
| `refs/remotes/https/main` | `https` 这个 remote **已不在 `git remote -v` 里**，这是个孤儿 ref。落后 main 555 个提交、领先 0 个 → **已完整并入，无可回收**。`fetch --all --prune` 不会清它（remote 不存在时 prune 无从下手）。 |
| worktree `openpocket-wt-apkbuild` | detached 在 `d95b4a68`，`status` **0 个改动**，且 `git merge-base --is-ancestor d95b4a68 main` = 0 → **已完整并入，无可回收**。 |
| 11 个 stash | **有货**，见 §4。 |

> 一开始我用 `git diff --stat main stash@{N}` 判「stash 是否还有独有内容」，
> 结果每个都报 715~1264 个文件差异。这个判据**问错了问题**：stash 是 09-30/10-01
> 的旧快照，main 在其后又落了 300 多个提交，diff 反映的是「main 变新了」，
> 不是「stash 有独有内容」。正确问法是拿 stash 的**第三个父提交**（`-u` 才会建）
> 里的未跟踪文件去比对 main 的文件集合。

---

## 4. 真正的发现：stash 里的丢失工作（逐个查证后才下结论）

8 个 stash 含「从未进过 main」的文件。逐个判定：

### 4.1 值得救：`docs/handoff/2026-10-01-android-doc-open-in-app.md`（stash@{8}）✅ 已恢复

真机「打开方式」弹窗归零 + 文档打开内置化，两个独立缺陷：

- **D1** 导出走 `@capacitor/share` → 任何导出都拉起系统选择框；
  修法是新增原生 `Document` 插件，MediaStore 静默落盘。
- **D2** 发票用相对 URL 取，真机上取回来的是 `index.html`；
  修法是 `resolveRuntimeApiBase()` 拼绝对地址 + `assertNotHTML` 守卫。

**判定依据（缺的是证据，不是代码）**：

- 代码**在** main 里：`frontend/src/native/document.ts` 存在；
  `download.ts:104` 走 `documentNative.saveToDownloads`，Android 分支注释明确写着
  「Share.share 会拉起系统『打开方式』选择框」；`assertNotHTML` 在多个 api 模块里。
- 但**主证据文档没进 main**：`git grep "MiuiChooser\|打开方式" main -- docs` → **0 命中**。
  `Document 插件` 只在 4 份其它 handoff 里被顺带提到，Redmi 2411DRN47C 的
  before/after 对照、`pageCount: 1` 所以多页翻页条没验到、`cap sync` 偶发失败
  导致 APK 用了旧资源这类**踩坑边界**，全都没有落盘。

这份文档还带一条很值钱的教训：**差点被骗过去的证据**——接手时设备上已经停着一个
上一轮会话用 adb shell 测语音留下的 `MiuiResolverActivity`，不是本 App 触发的；
清掉 `voice-test.wav` 并重建干净基线之后才拿到可信对照。

→ 已恢复到本分支 `docs/handoff/2026-10-01-android-doc-open-in-app.md`（180 行，
UTF-8 校验无乱码）。

### 4.2 不该救：2 个 email 测试文件 ❌ 已被更好地覆盖

`email-backfill-paging.test.mjs` 与 `email-cache-heal-negative.test.mjs`（stash@{1}）
**逐条比对后确认是重复品**，救回来是倒退：

- 负控文件里的 3 个用例，在 main 的 `email-cache-heal.test.mjs` 里
  **逐字存在**：`负控：旧逻辑在「本地残留几封」时放弃…`、
  `负控：旧逻辑唯一的救赎路径（本地全空）…`、
  `负控：新逻辑不会把「本地确实最新」误判成缺口`。
- 丢掉的翻页测试测的是 `nextPageSince` 的锚点推进。main 的
  `email-cache-heal.test.mjs` 不仅有 `nextPageSince：锚点取本页最早一封…` 和
  `nextPageSince：误用「本地最新」当锚点会被拦下（no-progress）`，
  `email-cache-heal-run.test.mjs` 还覆盖了**连续三页严格递减**。

而且丢的那个版本**更差**：它在测试里**复制了一份 `nextPageSince` 实现**
（`const advanced = oldestDateMs - 1`），测的是副本不是被测代码——
正是本项目 handoff 里反复记录的「测副本」反模式。已被后来会话用直接测真实
函数的写法取代。**结论：这是 stale 重复品，不救。**

### 4.3 不值得救：其余 6 项 ❌

- `.verify-stt-data/pocketd.{out,err}.log`（stash@{7}）——运行日志，不是资产。
- `scripts/run-pipeline-once.mjs`（stash@{7}、@{8}）——一次性跑批脚本。
- `scripts/llm-chain-check.mjs` / `llm-direct-probe.mjs`（stash@{3}）——
  探针脚本，且 `scripts/gw-*.mjs` 一族在最近 24h 已被重写过一轮。
- `scripts/cdp-*.mjs` / `verify-doc-inapp*.mjs`（stash@{8}）——取证脚本，
  其产出的证据已由 §4.1 那份 handoff 记录。
- `frontend/icon-ligature-check.html`（stash@{4}）——手工验证页，
  字体子集已有 `npm run check:icons` 门禁（round13 重建过 material-symbols 子集）。

---

## 5. 未提交的在途改动（**不属于本轮，我没有碰**）

共享工作区有 34 项改动/未跟踪，全部是上面 3 个会话的在途工作。记录一下以防漏看：

- `backend/internal/wecom/`（未跟踪，5 文件）——企业微信接入，**正在写**。
- `backend/internal/config/config.go`、`backend/internal/server/server.go`——应为上面那个接入铺路。
- `frontend/src/constants/llm-gateway.ts` + `scripts/gw-*.mjs` + `seed_llm_gateway.sh`——
  LLM 网关常量与脚本同步（配 `llm_gateway_frontend_parity_test.go` 新护栏）。
- `backend/internal/opencode/config_writer.go`、两个 `_test.go`。
- 一堆 `.scratch-*.txt` 未跟踪临时文件（`.scratch-wecom.txt` 于 18:47 新增），
  **这些是临时产物，不该进仓库**——建议提交前清理或加进 `.gitignore`。

---

## 6. 遗留风险

1. **【最高】并发写入共享 main。** 3 个会话同工作区，其中一个同任务。
   `47286a27` 已提交未推送。在有人把 `wecom` 写完并自己提交之前，
   任何 `git add -A` 都可能扫进半成品。**建议：等并发收敛，或给每个会话独立 worktree。**
2. **孤儿 remote ref `refs/remotes/https/main` 仍在**（`https` remote 已删）。
   555 个提交前的快照，无独有内容，可安全删；本轮未删，因为它对别人也是可见状态。
3. **11 个 stash 仍在**（最老 09-30）。已查证：除 §4.1 外无独有资产。
   在确认 §4.1 那份已合并后，可清理 0~10 全部。
4. **`go test -race` 仍未在 HEAD 上跑过。** 本轮同样没跑。
   要跑必须 `$env:CC='C:\tools\w64devkit\w64devkit\bin\gcc.exe'`，
   且该变量**只在单次 bash 调用内有效**，漏设就会退化成「跑不了」的假象。
5. **round14 遗留**：护栏规则 4 保证「写语句被登记」，不保证「登记的理由今天仍然成立」。
   理由是手写散文，机器守得住「不许偷偷加 DELETE」，守不住「三重开关今天还拦得住吗」。
   `diag_snippet_leak_test.go` / `diag_merge_exec_test.go` 的闸门位置需人工复核。
6. 本轮**没有**逐条审完 334 个提交——只对最新提交 `47286a27` 做了逐行审计 + 独立负控，
   并对 HEAD 做了全量验证。「全部提交逐条批判」在单轮内做不到可信，如实说明。

---

## 7. 下一轮提示词

> 前提：先 `git fetch && git log --oneline HEAD..origin/main` 与 `mavis session list`
> 确认 3 个并发会话是否已收敛。**在收敛前不要在共享工作区跑 `git add -A`。**
>
> 1. 合并本分支 `audit/round15-lost-work-recovery`（只含一份 handoff 文档，
>    无代码变更，零风险），随后清理 11 个 stash 与孤儿 ref `refs/remotes/https/main`、
>    worktree `openpocket-wt-apkbuild`（三者均已验证无独有内容）。
> 2. 确认 `backend/internal/wecom/` 已完工并自行提交；若仍半成品，先把它移出工作区
>    （`git stash push -u -- backend/internal/wecom/`）再让 main 恢复可提交状态。
> 3. 补上 round14 遗留：人工复核 `diag_snippet_leak_test.go` / `diag_merge_exec_test.go`
>    的闸门是否仍写在写操作之前。
> 4. 首次在 HEAD 上跑 `go test -p 2 -race -count=1 ./...`（务必设 `CC`）。
