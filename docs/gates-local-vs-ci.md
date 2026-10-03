# 门禁分工：本地 `npm run gates` 与 CI

> 2026-10-03 实测后成文。本文**解释机制，不是名单**。
> 名单的权威只有一处：`frontend/gates.json`。本文若与它冲突，以它为准。

## 装 pre-push 钩子前必须知道的一件事：它是**仓库级共享**的

`scripts/install-git-hooks.ps1` 做的是 `git config core.hooksPath .githooks`。
这个键写在**共用的 `.git/config`** 里，不是某个 worktree 私有的。实测：

```
$ git -C <某个 worktree> config core.hooksPath .githooks
$ git -C <主工作区>       config core.hooksPath
.githooks          # 主工作区立刻看到
```

`git rev-parse --git-common-dir` 在主工作区与 worktree 里指向**同一个** `.git`，所以
**从任何 worktree 装这个钩子，会让这个仓库所有 worktree 的 push 都开始跑全量后端测试**。

后果要正视：并行会话正在 push 时，钩子会让它的 `go test ./...` 跑十几分钟，
一旦某条既有的红被算到它头上，**它的 push 会被拒绝，而且它不知道为什么**。
所以本轮没有安装它（实测后已把 `core.hooksPath` 还原为空）。

什么时候装：合并进 main 之后、且确认没有并行会话在 push 时。
装完自己验一次：`git config core.hooksPath` 应为 `.githooks`，
然后 `git config --unset core.hooksPath` 可以随时还原。

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
