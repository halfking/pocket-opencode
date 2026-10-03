# 门禁分工：本地 `npm run gates` 与 CI

> 2026-10-03 实测后成文。本文**解释机制，不是名单**。
> 名单的权威只有一处：`frontend/gates.json`。本文若与它冲突，以它为准。

## 装 pre-push 钩子：默认只装在**当前 worktree**

`scripts/install-git-hooks.ps1` 默认走 per-worktree 配置：

```powershell
git config extensions.worktreeConfig true
git config --worktree core.hooksPath .githooks
```

**为什么不默认装仓库级**——这是实测出来的，不是风格选择：

```
$ git -C <某个 worktree> config core.hooksPath .githooks
$ git -C <主工作区>       config core.hooksPath
.githooks          # 主工作区立刻看到
```

`git rev-parse --git-common-dir` 在主工作区与 worktree 里指向**同一个** `.git`，
所以裸的 `git config core.hooksPath` 写在共用配置里：**从任何 worktree 装它，
这个仓库所有 worktree 的 push 都会开始跑全量后端测试**。后果要正视——
并行会话正在 push 时，一条既有的红就会把它的 push 挡下来，而它看不到原因。

per-worktree 配置没有这个问题。实测装完之后：

```
[install-git-hooks] other worktree sees: (unset)   <- C:/workspace/openpocket
[install-git-hooks] other worktree sees: (unset)   <- C:/workspace/openpocket/.wt-stt3
```

`extensions.worktreeConfig` 本身是仓库级的，但它只负责「允许按 worktree 存配置」，
本身不改变任何行为。

用法与还原：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/install-git-hooks.ps1              # 装到当前 worktree
powershell -ExecutionPolicy Bypass -File scripts/install-git-hooks.ps1 -Worktree D:\path\to\wt
git config --worktree --unset core.hooksPath    # 还原当前 worktree
```

脚本开头会打印它装的是哪个 worktree——因为它第一版用 CWD 解析目标，
在主工作区调用、想配 side worktree 的话会**静默装错对象**。

## 三条车道，各自负责什么

| 车道 | 触发 | 范围 | 作用 |
|---|---|---|---|
| `npm run gates` | 手工 / 提交前 | **全部 23 条** | 本地全量 |
| `pre-push` 钩子 | `git push` | 改 `backend/` → `go test ./...`；改 `frontend/` → 全量 gates | 阻止「有边界的结论」进主干 |
| CI `gates-parity` job | 每个 push / PR | 12 条 `check:*` | 补上「只在本机跑过」的那部分 |

第三条车道存在的理由是第二条：**2026-10-03 那次真红里，提交信息写的
「internal/rss、flashcards、config、server 全部通过」这句话不假，它只是有边界。
钩子把边界的决定权从人手里拿走了**，剩下的「门禁配没配进 CI」则由规则 5 保证。

## 一句话

本地跑全量 23 条；CI 跑其中的 12 条 `check:*`，另外 11 条由 CI 里已存在的步骤覆盖。
这个划分是**数据**（`gates.json` 的 `ciRuns` / `ciCoveredElsewhere`），不是 workflow 里手写的一串名字。

## 为什么要拆，而不是在 CI 里直接 `npm run gates`

`gates` 的 23 条里，`typecheck` / `build:gate` / `test:all` 与 6 个 `test:*` 子集，
在 `frontend.yml` 的 `frontend-lint` job 里**已经跑过**。全量再跑一遍等于把
`vue-tsc` + `vite build` + 全量 `node --test` 重做一次，而那个 job 的 `timeout-minutes: 20`
是硬约束。所以 CI 侧只补跑它没跑过的那部分。

6 个 `test:*` 子集（`test:native` / `test:stores` / `test:auth` / `test:styles` /
`test:stt` / `test:email-heal`）不是被跳过，是被 `test:all` 覆盖：
`test:all` 枚举并执行全部 `*.test.mjs` / `*.test.ts`，这些子集是它的子集。
`check:test-coverage` 静态断言「每个测试文件都被某个 gates 可达脚本覆盖」，
所以这份覆盖关系不是靠人记的。

## 为什么名单必须是数据，而不是 workflow 里的一串名字

把 12 个名字硬写进 workflow，等于又造一份「加了门禁却忘了同步」的清单。
本仓已经为同一类缺陷付过两次代价：

- 146 个测试文件里只有 32 个被某个 npm script 引用，114 个是孤儿，而 gates 全绿。
- `check:test-coverage` 与 `check:crlf-needles` 在本机 `gates` 里跑，CI 从来不跑
  （CI 逐条手列 `node --test`）。护栏存在但没人执行，等于没有护栏。

所以 `run-gates.mjs` 加了**规则 5**：`gates` 里有任何一条既不在 `ciRuns`、
也不在 `ciCoveredElsewhere`，就退出码 2。于是「新加一条 `check:*` 却没接进 CI」
从静默变成硬失败，**而且加门禁时不需要改 workflow**。

规则 5 的其余三类同样会判死（均有负控实测）：

| 触发条件 | 结论 |
|---|---|
| `ciRuns` 里有名字不在 `gates` 里 | 退出 2（改名或已删） |
| 同一条门禁同时在 `ciRuns` 与 `ciCoveredElsewhere` | 退出 2（CI 会跑两遍） |
| `ciRuns` 内部重名 | 退出 2 |

## 怎么查当前分工

```
cd frontend
node scripts/run-gates.mjs --list   # 打印名单 + 本地/CI 分工 + 接线核对结果
node scripts/run-gates.mjs --ci     # 只跑 CI 负责的那 12 条
```

`--ci` 与 `--only` 的差别在于**名字从数据来**，不需要人同步。

## 两条硬依赖（实测踩出来的，不是想当然）

- **`npm ci` 不能省。** 在 `node_modules` 缺席的情况下实测跑 `--ci`：前 10 条通过，
  第 11 条 `check:icons` 报 `Cannot find package 'harfbuzzjs'`，退出 1。
- **`setup-go` 不能省。** `check:gofmt` 调 `gofmt`；不在 PATH 时它 `exit 1`，
  那是「拒绝给结论」，不是「通过」。

## 本地与 CI 结论不一致时，先信哪个

门禁两侧用的是同一份代码，差异只可能来自**运行环境**：`gofmt` 的行尾、
`check:icons` 的字体子集、`check:i18n*` 的 locale 文件。
`check:gofmt` 已经在自己内部把行尾归一化后再判，所以 Windows 与 Linux 结论应当一致；
若某条门禁在两侧结论不同，先按 `check:*` 名字定位它依赖的环境，再讨论谁对。

## gofmt 门禁的修法

`check:gofmt` 报出的是**文件路径**，并自带修法：

```
node scripts/check-gofmt.mjs          # 只判
node scripts/check-gofmt.mjs --fix    # 判 + 用 gofmt -w 修，然后请重跑确认
```

**用 `--fix`，不要裸跑 `gofmt -w`。** 本机 `core.autocrlf=true`，裸 `gofmt -d`
会把整个文件按 LF 重写（实测 203 行的文件整份报差异），那是门禁专门要滤掉的
行尾伪债；门禁先归一化再判，报出来的才是真债。

「`gofmt -w` 一次就干净了」在本机是**错的**：实测单遍之后仍有一批文件不通过
（单文件 diff 356 行 → 19 行），第二遍才收敛到 0。所以修完必须重跑门禁确认。

## 落地跑道（`audit/gofmt-debt-20261003` -> main）

写这一节是因为落地不是「pull 完 merge 一下」就完事，**有三个文件是必争点**。

### 落地前先做这件事

```bash
# 1. 确认主干工作区是干净的、有没有人在写。不干净就先等，别硬合。
git status --porcelain
git worktree list

# 2. 看远端有没有前移。前移了就不能再指望 fast-forward。
git fetch origin
git rev-list --count HEAD..origin/main
git merge-base --is-ancestor origin/main audit/gofmt-debt-20261003 && echo "仍是 fast-forward"
```

### 三个必争点，以及机械解法

| 文件 | 冲突形态 | 怎么解 |
|---|---|---|
| `backend/internal/email/invoice_harvest.go` | 本分支是 gofmt 空白（对齐 / `em.Date-parsed` 简写 / 注释重排），对方是逻辑改动 | 两边都留：逻辑取对方的，空白取 gofmt 的。跑 `node scripts/check-gofmt.mjs` 确认不红 |
| `backend/internal/server/server_assistant.go` | 本分支只改了一行对齐 | 同上，通常自动合 |
| `docs/handoff/2026-09-30-android-e2e-bug-d-e-f.md` | 两边都在**同一个行号后插入**（§4.125 与 §4.126） | 两段都留，按节号顺序排列。**不要**用 `git checkout <branch> -- <path>`，那会丢一侧 |

第三条最容易出事：它是纯追加冲突，diff 上下文长但**没有一行内容相同**，
所以「看起来像重复」而实际是两个不同的节。判据是节号，不是行号。

### 落地后必须跑的三件事

```bash
cd frontend && node scripts/run-gates.mjs --list   # 规则 1-5 全过、CI 分工仍对
cd frontend && node scripts/run-gates.mjs --ci     # 12 条全绿
cd backend  && go test ./...                       # 全量，不是挑几个包
```

第三条是这一整套机制存在的理由：它就是当初没被跑的那一条。
