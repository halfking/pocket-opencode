# 2026-10-02 round4 —— 根目录 0 字节 go.mod：「后端全绿但根目录任何 go 命令都死」

> 触发：`/goal` 定时任务（拉主分支、清理不活跃子分支、批判性审计 24 小时内的提交与本地修改、提交推送）。
> 执行窗口：2026-10-02 03:46 – 04:20。
> 基线：本轮开始时本地 `main = b1e47b24`，`origin/main = 0b9cfc95`（领先 7 个提交，fast-forward 合并）。

---

## 0. 一句话结论

本轮**没有发现需要合并的分支，也没有发现回归**——真正值得记的是一个
**0 字节的 `go.mod` 躺在仓库根**：它让 `go build ./...` / `go list ./...` /
`go env` 在仓库根**全部硬失败**，而后端 CI 与本地开发都在 `backend/` 下跑，
所以**53 个包全绿、没有任何一道现有卡口会红**。

这是本包 `doc.go` 早就写下的那类事故的翻版：缺陷在、CI 绿、无人察觉。
已删除该文件并补上第一道能真正拦住它的卡口。

---

## 1. 根因

```
$ go env GOMOD
C:\workspace\openpocket\go.mod          ← 0 字节，是它遮蔽了 backend/go.mod

$ go list ./...
go: error reading go.mod: missing module declaration.
```

Go 的模块搜索从当前目录逐级向上找 `go.mod`，**第一个命中就赢**。根目录那个
0 字节文件命中后，解析失败是**硬错误**（不是"忽略、继续往下找"），于是
根目录下每一条 go 命令都死。

杀伤力评估的关键在于**它对现有门禁不可见**：

| 门禁 | 跑在哪 | 会红吗 |
|---|---|---|
| `go build ./...` / `go vet ./...` | `backend/` | 否（`backend/go.mod` 正常） |
| `go test ./...`（53 包） | `backend/` | 否 |
| `npm run gates` | `frontend/` | 否（不跑 go） |
| 任何 `scripts/*.ps1` | 各自 `Set-Location` | 否 |

也就是说，**只有"在仓库根敲一条 go 命令"的人会撞上**，而那恰好也是最容易
被忽略的入口（`go build ./...` 看着很自然）。该文件 mtime 是 2026-10-02 00:49，
是某个被强杀/重定向的命令留下的产物——同批还有一个 0 字节的
`sec7bm_ins.txt`，两者都是同一类残留。

---

## 2. 改动

### 2.1 删除两个 0 字节残留

| 文件 | 大小 | 处置 |
|---|---|---|
| `go.mod`（根） | 0 B | 删除（不可恢复风险：无内容可丢；已移入回收站） |
| `sec7bm_ins.txt` | 0 B | 删除（同上） |

### 2.2 新增卡口 `backend/internal/repohygiene/stray_go_mod_test.go`

放在 `repohygiene` 的理由见该包 `doc.go`：后端 CI 跑 `go test ./...`，放这里
意味着**每次 CI 必然执行**；放进 npm gates 则只有本地手动跑才会执行。

**最容易写错的一点（也是本卡口成立与否的命门）**：
旁边那道 `TestNoCommittedSecrets` 用 `git ls-files`，只枚举**受跟踪**文件。
照抄它会让本卡口**永远绿**——出事的 `go.mod` 恰恰是命令产物，是**未跟踪**的
本地垃圾，`git ls-files` 根本看不见。所以这里刻意走文件系统 walk（含未跟踪）。

判据：任一 `go.mod` 若第一条指令不是 `module`（含 0 字节、只剩换行、
`modul` 半个词等形态）即判损坏，**fail-closed**（读不出来也算损坏）。

配套 `TestGoModDeclaresModuleHandlesTheThreeWaysToBeCorrupt` 用 7 条合成样本
锁住判据语义——判据太宽（护栏永远绿）与太窄（误报真模块）**都不会**让上面
那道护栏变红，只能靠对输入直接断言发现。

---

## 3. 证据

### 3.1 负控：护栏真的会红（不是又一个"永远绿"的门禁）

先确��注入生效，再看结果——注入没生效时"绿"无法区分"护栏失效"与"实验没跑"。

```
# 负控 1：制造事故形态（仓库根 0 字节 go.mod）
INJECTED zero-byte go.mod, size=0
--- FAIL: TestNoCorruptGoMod
      go.mod —— 0 字节：命令被强杀/重定向留下的产物
EXIT=1                                       ← 护栏真的抓到了

# 负控 2：改放**嵌套**目录 negctl-tmp/go.mod
INJECTED nested 0-byte go.mod, size=0
--- FAIL: TestNoCorruptGoMod
      negctl-tmp/go.mod —— 0 字节：命令被强杀/重定向留下的产物
EXIT=-1                                      ← 顺带证明 walk 不是只看根目录

# 清理后
--- PASS: TestNoCorruptGoMod    已扫描 4 个 go.mod（含量身定制的判据，含未跟踪文件）
```

扫描数下限设为 **3**（backend / services/zagent-gateway / opencode-manager，
删掉任一个都是架构级变更）。刻意**不**拿
`backend/third_party/identity-go` 与 `.scratch/genpdf` 当下限：后者是
可增可减的第三方/暂存目录，拿它当下限等于给将来埋一个误报。

### 3.2 顺带验证了 8cc01dfd 的护栏（本轮不做修改）

`8cc01dfd` 声称把 `pg_test_isolation_guard_test.go` 规则 1 从"等于不存在"
修成"真能拦住"。本轮用**它自己声称覆盖的切片回退形态**做负控复核：

```
# 往 internal/task/store_test.go 注入
var negctlProdDSNRead = []string{"POCKET_TEST_POSTGRES_DSN", "POCKET_POSTGRES_DSN"}
INJECTION APPLIED: True      LITERAL PRESENT: True

--- FAIL: TestPGTestsNeverTargetTheProductionSchema
      internal/task/store_test.go: 测试读取了 POCKET_POSTGRES_DSN 1 处。
--- PASS: TestStripGoCommentsHandlesTheThreeWaysToHideCode (4 子用例)
```

**结论属实**，无需再改。另外用 `git grep` 复核了 14 个 PG 测试助手：
`_test.go` 里剩下的 `POCKET_POSTGRES_DSN` 全部是注释、护栏自身，
或 allowlist 里的 `config_test.go` 两处 `t.Setenv` 写入。回退读取确已清除。

---

## 4. 测试命令与结果

| 命令 | 结果 |
|---|---|
| `go build ./...`（backend） | **exit 0** |
| `go vet ./...`（backend） | **exit 0** |
| `go test ./...`（backend） | **exit 0 — 53 ok / 0 FAIL** |
| `npm.cmd run typecheck` | **exit 0**（`vue-tsc --noEmit`） |
| `npm.cmd run gates` | **exit 0**（typecheck + build:gate + test:all + 10 组 ratchet） |
| `go test ./internal/repohygiene/ -run GoMod` | 新卡栏 PASS |

gates 内的关键数字：孤儿测试卡口 **152/152 覆盖**（154 个测试文件，2 个在
具名豁免名单里）；原始错误上屏 **0 处**；死能力棘轮基线 8 条；图标子集
133 个名字全部在字体内。

### 4.1 两个必须写进口径的环境限制

**(a) `-race` 在本机根本跑不了** —— `CGO_ENABLED=0` 且 PATH/常见路径均无
gcc（`C:/Progra~1/Git/mingw64/bin/gcc.exe` 不存在）：

```
$ go test -race ./...
go: -race requires cgo; enable cgo by setting CGO_ENABLED=1
```

所以"53 包全绿"是**不带竞态检测**的绿。仓库历史 handoff 里写的
`go test -race ./...` 53 包全绿，在**当前这台机器上复现不出来**。
下一轮若要复跑竞态检测，需要先装 gcc 并 `CGO_ENABLED=1`。

**(b) `npm`（PowerShell 下的 `npm.ps1`）被执行策略拦截** —— 第一版
`npm run typecheck` 报 **exit 0**，但那是**假绿**：脚本根本没跑，退出码来自
PowerShell 自身。必须用 `npm.cmd`：

```
npm : 无法加载文件 ...\npm.ps1，因为在此系统上禁止运行脚本。
```

这个坑与本仓库已有的"空跑检查 → 变量留空 → 等式恒真"是同一族：
**绿灯的前提是实验确实执行了**。

---

## 5. 分支审计结论

`git rev-list --count origin/main..<branch>` 全量核对（0 = 已完全并入）：

| 分支 | 最后活动 | 未合并提交 | 处置 |
|---|---|---|---|
| `origin/ci/wire-style-guards-into-gates` | 4h | **0** | 已并入，无需动作 |
| `origin/fix/email-cache-backfill` | 13h | **0** | 已并入 |
| `origin/codex/platform-goal-20260930` | 21h | **0** | 已并入 |
| `origin/consolidate/2026-10-01` | 24h | **0** | 已并入 |
| `feat/2026-10-01-stt-service`（本地） | 4h | **0** | 上一轮已并入 |
| 其余 8 个远端分支（3–5 周前） | — | **0** | 早已并入 |
| `email-pipeline-snapshot-2026-10-01`（本地） | **16min** | 100 | **不活跃窗口外，故意不动** |
| `feat/mail-config-deploy`（本地） | **24min** | 6 | **不活跃窗口外，故意不动** |

**本轮没有任何分支满足"1 小时前且未合并"**，所以既没有可合并的，也没有
该删的。最后两个分支虽然有未合并提交，但最后活动都在 1 小时内，且各自
挂在活跃 worktree 上（`openpocket-wt-email` / `openpocket-wt-maildeploy`），
属于并发会话的在制品——**并发未结束前不收编、不删除**。

删除已并入的旧分支属于破坏性操作且本轮无授权，**未执行**。候选见上表
（`git branch -d` 即可，不需要 `--force`，因为已完全并入）。

---

## 6. 遗留风险

1. **并发会话未结束**：`email-pipeline-snapshot-2026-10-01`（100 提交）、
   `feat/mail-config-deploy`（6 提交）仍在被写。下一轮开工前必须重新
   `git fetch` 并复查这两个分支的未合并数。
2. **本机代理端口会漂移**：`~/.ssh/config` 里的 7900 **已失效**
   （`errno=10061`），实测可用端口是 **7897**。本轮 fetch 用
   `$env:GIT_SSH_COMMAND='ssh -o ProxyCommand="C:/Progra~1/Git/mingw64/bin/connect.exe -H 127.0.0.1:7897 %h %p" -o ServerAliveInterval=15'`
   临时覆盖，**没有改全局 config**（会再次失效）。7890/10809/1080/10808/33210 均不通。
3. **竞态检测缺口**：见 §4.1(a)。当前本机无法验证 `-race`。
4. **凭据轮换仍未做**（沿用上一轮结论，本轮未动）：仓库里四把明文提交过的
   凭据已从工作树清除但**从未轮换**，其中 dev 旁路 admin 口令权限最高。
   `git log -S` 可取回。**删字面量 ≠ 作废。**
5. **生产库残留**：2026-10-01 的两次 `DROP SCHEMA` 事故余波里，
   生产库中仍有 2 个 `meeting_test_*` schema（跑测试留下的）。
   8cc01dfd 堵住了再生的路，但**存量没清**。

---

## 7. 下一轮提示词

```
拉主分支并合并，跑 go build/vet/test 与 npm.cmd run gates；
按 git rev-list --count origin/main..<branch> 复查全部分支的未合并数，
只收编「最后活动 >1h 且未合并」的分支，并发会话正在写的分支不要动；
批判性审计 24 小时内的提交与本地修改——对每一条"已修复/已拦住"的声明
亲自做负控复核，不采信提交说明；输出结论/根因、改动文件、测试命令与结果、
遗留风险，并更新 handoff 后推送。
```

**下一轮开工前先做的三件事**：

1. `git fetch` 后确认 `origin/main` 是否又前进（本仓库 24h 内 216 个提交，
   并发极密）。
2. 复查 §6.1 两个并发分支的未合并提交数。
3. 若要声称"后端全绿"，必须同时给 **PASS / SKIP / FAIL** 三个数，
   并注明**是否带 `-race`**（本机目前带不了）。
