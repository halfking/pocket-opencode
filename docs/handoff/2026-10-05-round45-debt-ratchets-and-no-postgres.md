# round45 —— 两道债务门禁「永远红、因此永远接不进 CI」的根因修掉了；本机 PostgreSQL 消失，DB 层结论本轮一律不采信

日期：2026-10-05 00:33 → 02:0x（本机时区 +08:00）
范围：定期 24 小时审计（拉取合并 / 分支清理 / 提交总结与批判 / 修正并推送）
基线：`e2d6d8aa`（开工时本地 main，与 origin/main 同一个 commit）

---

## 一、结论先行

| # | 事项 | 结论 | 证据强度 |
|---|---|---|---|
| 1 | 主分支编译/静态检查/全量测试 | `go build` / `go vet` / `go test ./... -count=1` **全 exit 0** | 强（实跑） |
| 1b | ⚠ 那个「0」的可信度 | **只能算「无回归」，不能算「全绿」**：本机已无 PostgreSQL，依赖 DB 的测试全部静默 SKIP | 强（`-v` 逐条钉死，见 §2.1） |
| 2 | 24 小时内未合并的子分支 | **一个都没有**（远端只有 `main`，本地无未合并分支） | 强 |
| 3 | `refactor/maestro-flows-checker` + worktree `openpocket-wt-i18n2` | 0 独有提交、已含于 main、目录干净 ⇒ **已删除** | 强 |
| 4 | 残留目录 `openpocket-wt-a31` / `-upsert` | 前者 0 文件；后者 2559 文件经 blob 哈希证明**无未合并工作** ⇒ 已删除 | 强（见 §3.2） |
| 5 | 缺陷 A：两道债务门禁永远红 | **已修**：改成基线棘轮，并**接进 gates + CI** | 强（自测 + 真仓库负控 + CI 名单核对） |
| 6 | 缺陷 B：3 个真机门禁把「设备不在」报成「判红」 | **已修**：改 exit 3「前置缺失」 | 强（双向分类器负控） |
| 7 | 缺陷 C：`check-dev-pass-sourcing` 的 ROOT 依赖 cwd | **已修**（接线时才暴露，见 §4.3） | 强 |
| 8 | 24h 内 146 个提交 | 逐条读完不现实，也不该假装读完。本轮按**主题分组 + 挑高风险项实测**审计，方法与边界写在 §5 | 如实标注 |

### 1b 必须先说：这是本轮最重要的一条，且它让上一轮的一部分结论**失效**

```
$ Get-Service | ? Name -match postgres      → 无
$ Get-Process | ? ProcessName -match postgres → 无
$ docker ps                                  → daemon 未运行（pipe 不存在）
$ Get-Command psql                          → 无
$ Test-NetConnection 127.0.0.1 -Port 5432   → 不通
本机无 PG 安装目录、无 PG_VERSION、无 ProgramData\PostgreSQL
```

而 round44（2026-10-04 19:xx）是在**能连上 `127.0.0.1:5432`** 的前提下做的，
它那三条头条结论都建立在真实库上：

- 「标注行仍占 CNY 合计 61.1%」
- 「词边界漏判实测为 0 封（982 行语料）」
- 「横幅阈值拿到真票数据点 1.294–1.500」

⇒ **本机现在无法复现这三条中任何一条。** 它们不是被推翻，是**暂时不可复验**。
下一轮要重新动导出口径或阈值，**必须先把 PostgreSQL 装回来**，
否则只能靠推断，而推断在本仓反复被证明是不够的。

实测本轮 `go test ./...` 的绿灯里有多少是 skip：

```
$ go test ./internal/email/ -run 'TestMarkRetry' -v -count=1
--- PASS: TestMarkRetry_CallsComposeBeforeAssign (0.00s)
    invoice_retry_test.go:49: POCKET_TEST_POSTGRES_DSN not set; skipping...
--- SKIP: TestMarkRetry_ConvergesToFailedAfterMaxAttempts (0.00s)
--- SKIP: TestMarkRetry_SuccessPathIsTerminal (0.00s)
--- SKIP: TestMarkRetry_StatusMachine (0.00s)
```

`markRetry` 的**活**判据三条全 SKIP，只有接线判据那条 PASS。
包耗时也自证：`internal/email 27s`（round43 记录：设 DSN 时 172s / 不设 19s）、
`internal/flashcards 0.313s`（设 DSN 时 8s）—— 落在「不设」那一侧。

**⇒ 记法：`go test ./... = 0 FAIL` 在本机当前状态下，等于「没有回归」，
不等于「测试跑过了」。** 两者在本轮被明确分开记账。

---

## 二、基线取证

### 2.1 后端

```
cd backend && go build ./...            → exit 0
cd backend && go vet ./...              → exit 0
cd backend && go test ./... -count=1    → exit 0（见 §1b：含大量 SKIP）
```

⚠️ 一条**我自己的错判**，记下来免得下轮重犯：
我顺手跑了 `gofmt -l .`，得到 **965 个文件「未格式化」**。这是**假报**——
本机工作区是 CRLF，而仓库入库是 LF，裸 `gofmt -l` 会把每个 CRLF 文件都列出来。
权威判据是 `node scripts/check-gofmt.mjs`，它报**真债 0**。
round44 也记过同一件事（「单遍不够，本机 CRLF」）。
⇒ **量 gofmt 债务只能用 `check-gofmt.mjs`，不能裸跑 `gofmt -l`。**

### 2.2 根目录 19 道 `check-*.mjs`（改前 → 改后）

| | 改前 | 改后 |
|---|---|---|
| 绿（exit 0） | 12 | **14** |
| 前置缺失（exit 3，新语义） | 0 | **3** |
| 红（exit ≠ 0/3） | 5 | 2 |

改后仍红的 2 条**都不是判红**，是「工具缺前置」，且都已有据可查：

| 门禁 | exit | 真实原因 |
|---|---|---|
| `check-device-token.mjs` | 1 | 需要命令行参数 `<dumpfile> <port...>`，无参即 usage |
| `check-marketplace-contract.mjs` | 2 | 需要先起隔离后端 `127.0.0.1:18101`，当前 `ERR:fetch failed` |

`check-device-token` 属于**同一类缺陷**（usage 缺参报 1 而不是「前置缺失」），
本轮**没改**——它不是门禁而是工具，改它属于扩大范围。记在 §6 遗留项。

### 2.3 CI 到底跑什么（决定上面这些红算不算数）

- `.github/workflows/backend.yml`：`go build` / `check-smart-quotes --selftest` + 正式跑 /
  `go test -race ./... -count=1`（**带 postgres:17 service，设 `POCKET_TEST_POSTGRES_DSN`**）/
  `go vet` / `go build`。
- `.github/workflows/frontend.yml`：`gates-parity` job 跑 `node scripts/run-gates.mjs --ci`，
  名单由 `frontend/gates.json` 的 `ciRuns` 决定。

⇒ **CI 上 DB 测试是真跑的**（有 service 容器）。本机 skip 是**本机环境问题**，
不是仓库问题。这条要分清，否则会去改不该改的代码。

---

## 三、分支与残留目录清理

### 3.1 结论：没有需要「逐个文件合并」的分支

```
远端：git branch -r  → origin/HEAD, origin/main（其余 3 个已被 --prune 清掉）
本地：pocket-opencode → main（唯一）
      openpocket      → main + refactor/maestro-flows-checker
git branch --no-merged main → 空
```

`refactor/maestro-flows-checker` 的删除依据（四条都查了，不是靠分支名猜）：

```
git rev-list --left-right --count main...refactor/maestro-flows-checker → 0  0
git log --oneline main..refactor/maestro-flows-checker                   → 空
git branch --contains refactor/maestro-flows-checker                     → main, 自身
worktree 目录 git status --porcelain                                      → 0 行
```

分支 tip == main tip ⇒ **零独有提交**，删掉不丢任何东西。
（这正是记忆里那条纪律的用处：删前用 `rev-list --count` / 独有提交核对，
不要用「谁最后 push 的」猜。）

### 3.2 两个残留目录：都不是分支，但更要命——它们能吞掉工作

`openpocket-wt-a31`：0 个文件（只剩一个空的 `frontend/`）。

`openpocket-wt-upsert`：**不是 git 仓库**，所以 `git` 帮不上忙，
最后写入 2026-10-03 23:48。逐文件比对（2559 个文件）：

| 分类 | 数量 | 含义 |
|---|---:|---|
| 逐字与主仓一致 | 2250 | 无信息 |
| **仅行尾不同（CRLF/LF）** | **257** | **不是「独有工作」** |
| 归一化后仍不同 | 52 | 需判方向 |

⚠️ 第一版我把 257 条 CRLF 差异也算成「不同」，得出「240 个文件有独有改动」。
那是**误报**，而且误报得很像真的（连 `scripts/lib/adb-cdp.mjs` 这种主仓确定存在的文件
都被列了进去）。归一化行尾后只剩 52 条才值得看。

再判方向：对这 52 个算 **git blob 哈希**（LF 归一化后）再问对象库：

```
内容在 git 历史里存在（旧快照 / 已被 main 超越）= 51
内容从未进过 git                                 = 1   → scripts/maestro-run.mjs
```

单独看那 1 个：副本 78 489 字节 / mtime 10-03 23:17，主仓 102 671 字节 / mtime 10-05 00:01。
`git diff --no-index` 方向是**主仓 +422 / −4**，副本独有的那 4 行是 harness 重构**之前**的形态：

```
-  const socks = adb(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`], 15000)
-  const sock = socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]
-  if (!sock) throw new Error('NO_DEVTOOLS_SOCKET')
-  const one = runOne(flow)          ← 单流；主仓已改成多流循环
```

⇒ **副本里没有任何未合并的工作**，它是 10-03 23:17 的旧快照。
删除前已逐项取证，不是「看着像重复就删」。

⚠️ 诚实说明后果：删除走的是**回收站**（`rm --` → mavis-trash），
**不释放磁盘空间**。`openpocket-wt-upsert` 是 118.8 MB，要真正腾出来需自行清空回收站。

---

## 四、审计出的三个缺陷与修法

### 4.1 缺陷 A（主项）：两道「债务计数器」门禁永远红 ⇒ 永远接不进 CI

**根因不是代码坏了，是形态选错了。**

| 门禁 | 原形态 | 存量 | 结果 |
|---|---|---:|---|
| `check-fixed-cdp-ports.mjs` | 有任何命中就 exit 1 | 148 | 永远红 |
| `check-dev-pass-sourcing.mjs` | 有任何命中就 exit 1 | 26 | 永远红 |

**而且这不是新问题，是一个 2026-10-01 就落档、至今没做的决定。**
`docs/handoff/_part-4.86 §4.86.2` 原文：

> **刻意没有接进 `frontend/package.json` 的 `gates` 聚合**。原因：现在它报 140 处，
> 接进去会立刻把所有会话的 `gates` 打红。要接必须是**基线棘轮**形态（只对新增违规失败），
> 那是独立一件事，本轮没做。

而 `git log -- scripts/check-fixed-cdp-ports.mjs` **只有一次提交**（`3a848d43`，10-02 23:51），
即写下那天起就是这个形态。⇒ 三个后果，全是同一件事：

1. 它**拦不住任何东西**——真新增违规和存量混在 148 条里，没人看；
2. 它**不能接进 CI**——接进去第一天就红，于是被集体绕过；
3. 「有护栏」和「有护栏」在这两道上被读成了同一件事。

**修法**：抽出 `scripts/lib/baseline-ratchet.mjs`（两道门共用），改成
「基线内的存量不判红，只对**新增**判红」，债务下降只报进度不判红，
并 `--write-baseline` 录基线（148 / 26 条），然后**接进 `gates` 与 `ciRuns`**——
棘轮当初被写下的目的就是这个，只做棘轮不接线等于把承诺停在半路。

#### 关键设计：基线 key **绝对不能含行号**

`file|kind|detail`，同一 key 用计数容忍「同款再多加一处」。
这不是审美选择，是本仓**已经付过学费**的地方：
`z-index-ladder.test.mjs` / `bottom-chrome-gate.test.mjs` 的 ALLOWLIST 用
`rel:line` 作 key，Windows 上路径反斜杠 + 行号漂移让 **11 条全被判「陈旧」**（round43 §2.1）。
行号一漂就误报，久而久之没人看它——那不叫棘轮，叫噪声发生器。

#### 判据自测（两道门各 5 条 + 变盲对照，全部实跑）

| 用例 | 期望 | 实测 |
|---|---|---|
| 存量违规（原行号） | 不判红 | ✅ new=0 removed=0 |
| **行号漂 950 行** | 仍不判红 | ✅ new=0 removed=1 |
| 同 key 计数超出基线 | 判红 | ✅ new=1 |
| 基线外的 key | 判红 | ✅ new=1 |
| 债务下降 | 不判红（只报进度） | ✅ new=0 removed=1 |
| 变盲对照：棘轮失效 | 少报 1 条 | ✅ 1 → 0 |

外加原有的敏感度/特异度用例（3 类硬编码能报、5 类合法写法放过、逐条关规则少报 1）。

#### 真仓库上的负控（不是只在合成样本上演）

| 负控 | 实测 |
|---|---|
| 新建 `scripts/zz_negctrl_probe.mjs` 写 `const PORT = 9977` | 148→149，新增 1，**exit 1** ✅ |
| 撤除后 | 148，新增 0，exit 0 ✅ |
| **在真违规文件顶部插 12 行注释，把所有违规行号整体后移** | 仍 148 / 新增 0 / exit 0 ✅ |
| 第二道门同样加一处真违规 | 26→27，新增 1，**exit 1** ✅ |

行号漂移那条是本项的**决定性证据**：它直接证明新基线不会重蹈
ALLOWLIST 那个覆辙。

#### 两个我自己踩的坑（都已修，且都属同一类）

1. **`writeBaseline` 收的是 Map，却用 `Object.keys` 数它** ⇒ 第一版静默写出
   「0 个 key」的空基线而不报任何错。空基线 = 148 处全算新增 = 门禁一接就红。
   是输出里那句自相矛盾的「key 0 个 / 命中 148 处」暴露的。
   现在 `writeBaseline` 对空基线**直接抛错**。
2. **共享 lib 的自测用例里硬编码了 `file|kind|detail` 的 key 口径**，
   而第二道门字段叫 `rule|text` ⇒ 合成样本 key 全落成 `…|undefined|undefined`，
   5 条棘轮用例一起转红。
   **注意这个 bug 的形状**：真仓库判定当时是**绿的**（真基线用正确 keyOf 录的），
   只有自测抓到。若只跑主流程，这个接线错误会被当成「棘轮工作正常」。
   现在 `keyFn` 是必填参数。

#### 顺带修的第三态

基线文件缺失/损坏时 **exit 2（门禁自身报错）**，不是 exit 1（判红）。
把「判据跑不起来」说成「代码有问题」，方向是反的——
这与记忆里 push 校验循环那个坑（观测失败被当成推送失败，连报 4 次）同族。

### 4.2 缺陷 B：3 个真机门禁把「设备不在」报成 exit 1

`check-unlock-focus` / `check-cdp-pid-strict` / `check-fts-triggers-device`
过去都是 `execFileSync(adb, ...)` 裸调，无 device 时抛**未捕获异常 + 15 行栈**，退出码 1。

问题不在于「它红」，而在于 **exit 1 同时表示三件完全不同的事**：
① 这道门判红了 ② 设备没连上（根本没跑到被检查对象）③ 脚本自己有 bug。
一次全量扫描里 ①③ 值得追、② 该跳过；混在一起时人只会盯着「红」看，
于是要么去查一个没跑过的判定，要么把真红一起放过。

**修法**：新增 `scripts/lib/adb-prereq.mjs`，设备不在 ⇒ 打印一句人话 + **exit 3**
（与 `run-gates.mjs` 头注释的约定一致：**≥3 = 拒绝给结论**，不是普通断言失败）。

⚠️ **只降级「设备确实不在」这一类**。命令拼错、超时、shell 报错、权限被拒**照原样抛**——
把它们一并降级成「前置缺失」，等于把「我的观测手段坏了」说成「环境没准备好」。

双向验证（`isDeviceAbsent` 11 条，全对）：

| 应判「设备不在」=true | 实得 |
|---|---|
| `device '192.168.31.19:5555' not found` / `no devices/emulators found` / `device offline` / `device unauthorized` / `more than one device` / `cannot connect to daemon` | ✅ 全 true |

| 应判「设备不在」=false（关键负控） | 实得 |
|---|---|
| `cannot stat 'no such file or directory'` / `INSTALL_FAILED_UPDATE_INCOMPATIBLE` / `error: closed` / `ETIMEDOUT` | ✅ 全 false |

三道门在当前（无设备）状态下的实测输出：

```
adb.exe: device '192.168.31.19:5555' not found
[前置缺失] check-unlock-focus 没跑到被检查对象：adb 上没有可用设备（serial=192.168.31.19:5555）。
  这是「无法判定」，不是「判定为不通过」——所以退出码是 3 而不是 1。
  接上设备后重跑；不要把这一条当成门禁判红去排查。
exit=3
```

### 4.3 缺陷 C：`check-dev-pass-sourcing` 的 ROOT 依赖 cwd（接线时才暴露）

它写的是 `const ROOT = process.cwd()`。因为**这道门在 2026-10-05 之前从未被任何
runner 调用过**，「必须从仓库根跑」这个隐含前提从没被验证过。
接进 gates 后 `npm run` 的 cwd 是 `frontend/`，它会去扫 `frontend/scripts/`、
去找 `frontend/scripts/baselines/…`，找不到就 exit 2——
**门禁一接线就红，且报错信息与真实原因毫无关系**。

改为与其它根级门禁一致，用 `import.meta.url` 推导。验证：分别从仓库根与
`frontend/` 两个 cwd 跑，判定与 selftest 均 exit 0。

---

## 五、24 小时内 146 个提交：我怎么审的，以及没审什么

```
git rev-list --count --since='2026-10-04 00:00' origin/main  → 146
git shortlog -sn --since='2026-10-04 00:00' origin/main      → 139 halfking / 7 mavis
```

**不假装逐条读完。** 146 个提交里绝大多数是 `docs(handoff)` 与 `fix(email)`
的连续迭代，逐条「批判」只会产出流水账。实际做法：

1. 按主题归类（email 发票链路 / maestro 与 harness / UI 与门禁 / 闪卡 / STT / 文档）；
2. 对**会改变生产行为**的提交，读 diff 找「声明与实现是否一致」；
3. 对**新增护栏**的提交，验它是否真的有区分力（而不是「跑绿了一次」）；
4. 对**已落档但未实现**的决定（本轮抓到 1 条，见 §4.1），当成一等公民去找。

本轮据此定位到的具体问题就是 §4 那三条 + §1b 的环境失效。
**没审到的**如实列在 §6，不假装覆盖。

抽样读过并确认无问题的（读的是 diff，不是 commit message）：

| 提交 | 查了什么 | 结论 |
|---|---|---|
| `0119a735` flashcards DSN 回退 | 回退是否已彻底去掉 | 已去掉，`flashcardsTestDSN()` 只认 `POCKET_TEST_POSTGRES_DSN`；本地 SKIP 是**正确**行为（无 DSN） |
| `560f4a78` 人工标注不被覆盖 | `composeHarvestRetryMessage` 是否真在 `inv.LastError = msg` 之前 | 接线判据 `TestMarkRetry_CallsComposeBeforeAssign` 本轮实跑 PASS |
| `e2d6d8aa` / `3bb4dd7a` maestro | `launchApp` 禁令是否落在检查器里 | `check-maestro-flows.mjs` 本轮 exit 0 |
| `f62b9da2` Windows 路径分隔符 | ALLOWLIST 归一化是否真加了 | `test:styles` 相关门禁在 gates 里 exit 0 |

---

## 六、遗留风险（如实记，不藏）

1. **⚠️ 最重要：本机没有 PostgreSQL。** DB 层的一切结论本轮不可复验，
   包括 round44 的三条头条（61.1% / 0 封漏判 / 阈值 1.294–1.500）。
   **下一轮若要动导出口径或横幅阈值，必须先装回 PG**，否则只能推断。
2. **round44 §四 的第一顺位仍未做**：人工标注没传导到 `invoices-summary-*.md`，
   横幅两行仍以 `downloaded / 已核验` 占 CNY 合计 61.1%。要改的是**导出口径**，
   本轮**未获授权、也未做**。
3. **两条新接进 CI 的门禁在 Linux 上的行为未实测。** 本轮只在 Windows 跑通。
   风险已尽量消除（检测全部按行做、路径已 `replace(/\\/g,'/')` 归一，
   裸 `gofmt`/CRLF 类假报已识别），但**「CI 上真的绿」这件事本轮没有证据**，
   下一轮应看一次 CI 实跑结果。
4. **`check-device-token.mjs` 仍用 exit 1 报 usage**（§2.2），与刚修的
   exit 3 约定不一致。本轮判定它是工具不是门禁，未改。
5. **`git stash` 仍有 11 条**（最旧 2026-10-01，`openpocket` 仓库）。
   内容未审。清理不在本轮授权内。
6. **回收站未清空**：本轮移入 3 个目录，其中 `openpocket-wt-upsert` 118.8 MB。
   磁盘空间**未释放**。
7. **round44 §四 遗留的 GitHub 付款回执未建档**（`em-1298894461-…-5`）
   本轮**未碰**——它需要 DB 才能查。
8. 本轮**没有真机验证**（无 Android 设备）。三道真机门禁只验到
   「设备不在时正确报 exit 3」，**没有验过设备在时它们仍能正常判定**。

---

## 七、下一轮提示词

```
接着 pocket-opencode round45 做，全部要对照证据，不要凭声明：

1. ⚠️ 先把 PostgreSQL 装回本机并起库（round45 实测本机已无 PG：没有服务、
   没有安装目录、psql 不存在、docker daemon 未运行）。这是**一切 DB 结论的前提**。
   装好后用 `go test ./internal/email/ ./internal/server/ ./internal/flashcards/ -v -count=1`
   确认那些 SKIP 变成真跑，并**对比 round43 记录的耗时基线**（设 DSN 时
   email 172s / server 62s / flashcards 8s）——耗时落在「不设」那一侧就说明还是 skip。
   注意：不设 DSN 时 `go test ./...` 报 0 FAIL 是**假绿**，别当通过。

2. 复核 round45 接进 CI 的两条新门禁在 Linux 上真的绿
   （check:fixed-cdp-ports / check:dev-pass-sourcing，在 frontend.yml 的
   gates-parity job 里）。本轮只在 Windows 验过。基线文件在
   scripts/baselines/*.json，key 不含行号是刻意设计，别"顺手"改成含行号——
   那是 z-index-ladder ALLOWLIST 踩过的坑（round43 §2.1）。

3. 仍未获授权、仍是第一顺位：让人工标注传导到交付物。
   `invoices-summary-*.md` / `.csv` 里横幅两行仍占 CNY 合计 61.1%，
   因为 `last_error` 不是导出列。要改的是导出口径，且**改完必须打开新产出的
   汇总单逐行核对**（round44 §1d 的教训：只验库内回读不算数）。
   两个口径要分开打：全表 CNY 68416.21 vs 汇总单口径 CNY 10392.21。

4. GitHub 付款回执未建档（em-1298894461-acct-1790870162079171800-5）：
   round45 没能查，因为没 DB。库回来后查它是否被
   `maxInvoiceBodyFetches=24` 的每轮预算挤掉（拿当轮流水线报告，
   看 InvoiceBodyFetchDeferred 字段），不要猜。

5. `check-device-token.mjs` 无参时仍 exit 1（应与 exit 3 约定一致）。
   round45 判定它是工具不是门禁所以没改；下一轮若接线进任何 runner，先改它。

6. 本机没有 Android 设备（adb 192.168.31.19:5555 not found）。
   round45 把三道真机门禁的"设备不在"改成 exit 3，但**没验过设备在时
   它们仍能正常判定**。接上设备后必须复跑一次，确认改动没破坏正常路径。

红线：main 当前全绿，但那份绿里含大量 SKIP；说"测试通过"之前先说清是
"无回归"还是"跑过了"。
```

---

## 八、提交与推送记录

| 时间 | 事件 |
|---|---|
| 开工 | `git fetch --all --prune`；本地 main == origin/main == `e2d6d8aa`，工作区干净 |
| 清理 | `git worktree remove openpocket-wt-i18n2` + `git branch -d refactor/maestro-flows-checker`（均 exit 0；复核 `git worktree list` 只剩 main） |
| 清理 | 3 个残留目录经取证后 `rm --` 移入回收站（可恢复） |
| 提交 | 见下方 commit 记录 |

两个仓库（`pocket-opencode` 与 `openpocket` 是**两个独立 clone**，同一 remote）
在提交前后都已复核 `git status -sb` 干净且与 origin/main 同步。
一次性取证脚本写在 `D:\temp\`（**不在仓库内**，因为 `scripts/` 是两道门禁的扫描对象）。
