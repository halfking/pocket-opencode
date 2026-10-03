# round35 — 24h 审计：门禁真红一条、gofmt 分支的合并裁决、并发取证

> 日期：2026-10-03 15:46 – 16:0x
> 工作区：隔离 worktree `C:\workspace\openpocket-wt-audit`（detached @ `origin/main` = `a2c5a4b0`）
> **未触碰主工作区** `C:\workspace\openpocket`——审计全程有并发会话在写（见 §4）

---

## §1 头号结论：`origin/main` 上有一条真红，且 CI 必然是红的

`go test ./...` 在 `origin/main` 原始状态下失败，唯一失败项：

```
--- FAIL: TestNoCommittedSecrets (15.38s)
  ❌ 仓库里出现疑似密钥字面量。
  命中明细：scripts/start-rssdemo-backend.ps1:34  [password-literal]  demo-p…<13 chars, 已打码>
FAIL	github.com/halfking/pocket-opencode/backend/internal/repohygiene	16.581s
```

**这不是我引入的**（三条独立取证）：

1. 该文件在我的 worktree 里**未被修改**（`git status --porcelain scripts/start-rssdemo-backend.ps1` 为空）。
2. 命中行确实存在于 `origin/main` 的 blob 里：`git show origin/main:scripts/start-rssdemo-backend.ps1` 含
   `$env:POCKET_AUTH_PASS   = 'demo-pass-123'`。
3. `git log -S "demo-pass-123" -- scripts/start-rssdemo-backend.ps1` 定位到唯一的引入提交：
   **`a5de5f96`（fix(rss): 修两个会让订阅/日报整体失效的真缺陷）**。

### 根因：提交信息的验收清单是**选划**的，不是全量

`a5de5f96` 的验证段写的是：

> 验证：go build ./... 通过；internal/rss、internal/flashcards、internal/config、
> internal/server 全部测试通过

这份清单**没有包含 `internal/repohygiene`**，所以「全部测试通过」这句话本身不假，但它是一句
**有边界的话**，却被读成了无边界的一句。`repohygiene` 是全仓门禁型测试包（密钥扫描），
任何新增含字面量的脚本都会踩到它，而它恰好不在这四 个包里。

这不是偶发疏漏，是**可复现的机制**：`a5de5f96` 新增了一个 `.ps1`，`.ps1` 里的
`POCKET_AUTH_PASS = 'demo-pass-123'` 命中 `password-literal` 规则；而
`passwordStrength` 把「同时含数字与字母」的串判为「像真口令」，`demo-pass-123` 正好落在这一档。
**任何人在这个仓库里新增带 demo 口令的启动脚本，都会撞上同一条红。**

### 为什么 CI 一定是红的（推得对，但没实测）

`.github/workflows/backend.yml` 里有：

```yaml
- name: Run tests
  run: go test -race ./... -count=1
```

`./...` 包含 `internal/repohygiene`，所以这条红在 CI 上同样成立。

**但我没有实测 CI 的实际运行状态**——本机 `gh` 未安装（`gh : 无法将"gh"项识别为 cmdlet`），
所以「CI 现在是红的」是**从 workflow 定义推出的**，不是从 CI 日志看到的。这一条按未验证记。

---

## §2 修法：按扫描器自己写的规矩逐行豁免，不放宽判据

命中处理方式里，扫描器原文写着：

> 2. 如果是**合成夹具**：就地改写成自解释的合成串，并在同一行加 `// secret-scan-ok`
>    说明它为什么可以豁免。豁免必须逐行写；**不要为了迁就夹具去放宽本文件的判据**。

所以修法是**逐行 `secret-scan-ok` + 理由**，不是改规则、不是加文件白名单：

```powershell
$env:POCKET_AUTH_PASS   = 'demo-pass-123'  # secret-scan-ok: synthetic demo password for this throwaway local instance only; not a real credential
```

### 一条容易踩的约束：这条豁免**必须纯 ASCII**

该文件头部自己写了：

> NOTE: keep this file ASCII-only. PowerShell 5.1 reads BOM-less files as ANSI,
> and a mis-decoded non-ASCII comment can swallow the next line and break parsing.

所以豁免理由**不能写中文**——PS 5.1 按 ANSI 读无 BOM 文件，一个被误解码的中文注释会把
**下一行吞进注释里**，从而让 `$env:POCKET_JWT_SECRET = '...'` 这一行失效、脚本行为静默改变。
这是「加一行注释修红」这个动作本身的风险点，不是理论风险。

### A/B 对照（判据有牙齿）

| 状态 | `go test ./internal/repohygiene/ -run TestNoCommittedSecrets` |
|---|---|
| 改动前（origin/main 原样） | **FAIL**，报 `password-literal` |
| 改动后（加逐行豁免） | **ok** 1.484s |

对照组是红的、正组是绿的，所以这条修复是有承重的，不是把判据改松。

---

## §3 RSS 那条修复的独立复核：结论成立，判据确实有牙齿

`a5de5f96` 声称修了两条「让订阅/日报整体失效」的真缺陷。我独立复核了缺陷 B（item 主键）：

- 实现：`itemID(sourceID, contentHash)` = `"it_" + sha256(sourceID + "\x00" + contentHash)`，
  调试点 `parser.go:98` 已改用。**实现与描述一致**，是真正的「(source, hash) 组合键」。
- 正控：`go test ./internal/rss/...` → **ok 1.502s**。
- **负控（我把实现改回旧写法再跑）**：

```
--- FAIL: TestParserGivesEveryItemItsOwnID (0.00s)
    items "One" and "Two" share id "cb51f000ad01e2f117070e523c302b66f4819d6b981d2252ea3b3992711f9684" — one source can only store one item
    items "Two" and "Three" share id "cb51f000…"
FAIL
```

旧写法 `stableHash(source.ID, h, "", "")` 立刻复现出**同一个 id 三条全撞**，
与提交信息里写的哈希前 8 位完全一致。改完即恢复（`git checkout --`），worktree 已确认干净。

**评价**：这条修复的质量是这 24h 里偏上的一档——真进程实测、负控、真实 PG 覆盖跨用户与转载场景、
对照组专门测「别人的源必须 404 且只有 owner 的刷新真正到达 store」。缺陷 A（`RunNowInScope`
不跨作用域回退）与缺陷 B 是两类不同的根因（作用域找错 vs 主键算错），分开定位是对的。

**但它带着 §1 那条红**——即：修复本身可信，提交动作不完整。这两件事不矛盾。

---

## §4 并发取证：仓库当时**不在安静窗口**，这是本轮多数保守决策的依据

审计全程主工作区在被写。三次独立测量互相印证：

| 时刻 | 观测 |
|---|---|
| 15:46:06 | `email_body_pop3_realdata_test.go` 写入（**比本会话开工晚 6 秒**） |
| 15:50:14 | `logs/handoff-4.126.md` 写入 |
| 15:50:19 | `docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md` 写入（§4.126 已就位） |
| 15:43:03 | 并发会话 push 了 3 个提交到 `origin/main`（`f80fff9a` / `fdbe3e25` / `a2c5a4b0`） |

worktree 清单在审计过程中**变了**：开工时 `feat/meetings-device-flow-20261003` + `.wt-meet` 存在，
15:53 复查时该 worktree 与分支**已被并发会话自行删除**，同时**新增**了
`.wt-grid`（`feat/more-grid-reach-flow-20261003`，15:51 创建）与 `openpocket-wt-b1528`。

⇒ 结论：并发会话不仅在写，而且在**高频创建与回收 worktree/分支**。本轮因此：

- **全程在隔离 worktree 干活**，不碰主工作区的 3 个已改文件 + 4 个未跟踪文件。
- **没有编辑 handoff 主文档**（763KB，正在被写）——本文件是独立 round 文件，编号不占用 §4.x。
- **没有合并 173 个文件的格式化**（理由见 §5）。

---

## §5 分支裁决

### 5.1 `feat/more-grid-reach-flow-20261003` —— 活跃，**不动**

15:51 由并发会话创建，`ahead=0`（== `origin/main`），worktree `.wt-grid` 里有未跟踪的
`.maestro/more-grid-reach.yaml`。**正在施工中**，任何清理都是破坏。

### 5.2 `feat/meetings-device-flow-20261003` —— 已被并发会话自行收掉，无需本轮处理

开工时 `ahead=0`（已完全并入 `origin/main` = `a2c5a4b0`），符合「已合并可删」的条件；
但 15:53 复查时该分支与其 worktree **已不在清单里**——并发会话已经自己删了。
本轮不重复动作。

### 5.3 `audit/gofmt-debt-20261003` —— 内容**确认为真**，但**本轮不合**，理由如下

这是唯一一个真正「未合并且有独立内容」的分支（`ahead=2`）：

- `331ba4f3` 归一 173 个 `.go` 的 gofmt 格式债
- `12a49913` 新增 `scripts/check-gofmt.mjs` 门禁 + handoff §4.125

**我先验证了它声称的「纯格式化、零语义变化」，验证方式是逐文件比对而不是读提交信息**：

```
total .go files      : 173
gofmt(base)==branch  : 173   ← 全部逐字节等于 gofmt(merge-base 版本)
differs (REVIEW)     : 0
```

即 `gofmt(d7172655:<每个文件>) == <分支上的文件>`，**173/173 成立**。这条声称是真的。
另外在分支 worktree 上跑 `node scripts/check-gofmt.mjs` → **exit 0**（真债 0，899 个文件里
899 个是纯 CRLF 伪债）。门禁脚本本身质量不错：带 `selfTest()`（自检不过就 exit 1，
不静默通过）、区分「行尾伪债」与「真实格式债」、`gofmt` 不在 PATH 时**响亮失败**而不是
「通过」、完整清单落盘（`logs/gofmt-debt.txt`，已确认被 `.gitignore` 覆盖）。

**为什么仍然不合**（三条，按重要性排）：

1. **它会正面撞上并发会话正在改的两个文件。** 173 个文件里包含
   `backend/internal/server/server_assistant.go` 与 `backend/internal/email/invoice_harvest.go`——
   正是并发会话 15:44 还在写的两个文件，且**它们当前的版本都不是 gofmt-clean 的**（实测两者均 False）。
   此刻合并，等于把 173 个文件的格式化推到主线，等并发会话提交时必然在这两个文件上撞车。
2. **它不满足本轮任务自己的筛选条件。** 任务限定「1 小时前」「不活跃」的子分支；
   该分支最后一次提交是 15:15，审计时为 38 分钟，且 worktree 干净但分支仍在被引用
   （`origin/audit/gofmt-debt-20261003` 也存在）。它不是「不活跃」。
3. **门禁与格式化是一个不可拆的包，而 CI 根本不跑它。** 见 §6——把一个没人跑的门禁接进
   `gates.json` 只会让 22 项变 23 项，而 22 项本来就不进 CI。

**留待安静窗口的落地步骤**（本轮已验证过每一步的前提）：

```bash
git merge audit/gofmt-debt-20261003        # 预期无冲突：173 个文件是纯格式化
# 然后把门禁真正接上线（当前它谁都不调，见 §6）：
#   frontend/package.json 加 "check:gofmt": "node ../scripts/check-gofmt.mjs"
#   frontend/gates.json 的 gates 数组加 "check:gofmt"
#   ↑ 两处必须同时改：run-gates.mjs 规则 4 会对「新增 check:* 却不在 gates 也不在 notGates」exit 2
node frontend/scripts/run-gates.mjs --list # 校验接线
```

### 5.4 顺带一条：`npm run gates` 没有任何 CI 在跑

`.github/workflows/` 下 5 个 workflow（`backend` / `backend-pg` / `frontend` / `e2e-web` / `docker-smoke`），
**没有一个执行 `npm run gates`**。`frontend.yml` 只单独跑 `check:test-coverage` 一项，
并且注释里明说：

> 为什么必须进 CI：这张卡口本机在 npm run gates 里执行，而 **CI 从来不跑 gates**

⇒ 22 项前端门禁目前是**只在本地/人工跑**的。同理 `check-gofmt.mjs` 即使接进 `gates.json`，
也仍然是「本地门禁」。这不是本轮引入的问题，但它决定了 §5.3 的处置：**接线的收益比看上去小**。

---

## §6 本轮基线与改动汇总

### 基线（在 `origin/main` = `a2c5a4b0` 原样上测，改动之前）

| 检查 | 命令 | 结果 |
|---|---|---|
| 编译 | `go build ./...` | ✅ exit 0 |
| 静态 | `go vet ./...` | ✅ exit 0 |
| 前端门禁 | `npm run gates`（22 项） | ✅ 全绿，98.0s |
| Go 全量测试 | `go test ./...` | ❌ **1 红**（`repohygiene`），其余包 ok |

### 本轮改动（2 个文件）

| 文件 | 改动 |
|---|---|
| `scripts/start-rssdemo-backend.ps1` | 第 34 行加逐行 `secret-scan-ok` 豁免（纯 ASCII 理由） |
| `docs/handoff/2026-10-03-round35-secret-gate-red-and-gofmt-branch-verdict.md` | 本文件 |

**刻意没做的**：没有改 `secrets_test.go` 的任何判据、没有加文件级豁免、没有动那 173 个格式化文件、
没有编辑 handoff 主文档、没有碰主工作区任何文件。

---

## §7 遗留风险

1. **「验收清单是选划的」这个模式没有修。** 本轮只补了 `a5de5f96` 留下的那一条红；
   提交信息里写「全部测试通过」而实际只跑了四个包的做法仍在发生。真正的修法是让
   提交者跑全量，或让 CI 变成唯一裁判——§5.4 已证明后者目前不成立。
2. **`npm run gates` 不进 CI**，22 项门禁的实际保护力远低于其数量给人的印象。
3. **gofmt 债仍在**（169 个文件真实格式债），且未被任何自动化拦住，会继续累积。
4. **并发窗口未关闭**：本轮结束时会话很可能仍在写。下一个进入共享状态（合并/删分支/推 main）
   的人必须重新做 §4 的取证，不能沿用本轮的结论。

---

## §8 下一轮提示词

> 1. 先重做并发取证：`git worktree list` + 主工作区 `git status` + 按 mtime 扫最近 10 分钟写入。
>    若并发会话仍在写，**只读审计**，不要碰共享状态。
> 2. 安静窗口到来后落地 gofmt 分支：merge `audit/gofmt-debt-20261003`，
>    并把 `check:gofmt` **同时**接进 `frontend/package.json` 与 `frontend/gates.json`
>    （只改一处会被 run-gates.mjs 规则 4 判死）。合并前先确认
>    `server_assistant.go` / `invoice_harvest.go` 已被并发会话提交。
> 3. 决定 `npm run gates` 要不要进 CI。若不进，就把 22 项门禁的定位在文档里讲清楚
>    （本地门禁 vs CI 门禁），别让数量制造虚假信心。
> 4. 考虑给提交模板加一条「全量测试命令」，或直接要求 `go test ./...` 绿才允许 push，
>    从机制上消掉「选划验收清单」。
