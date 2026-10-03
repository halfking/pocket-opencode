# round7 —— 两条「已修复/已拦住」声明的负控复核：一条真修好了，两条护栏比声称的弱

日期：2026-10-02
分支：main（`73dbf596` 之上）
本轮改动文件：`frontend/src/api/__tests__/api-timeout-budget-table.test.mjs`（+11 行）

---

## 0. 开工前的一处环境事实：GitHub 通道又翻转了一次

`git fetch origin` 一开始报 `Connection closed by UNKNOWN port 65535` ——
这是 `ProxyCommand` 挂掉的典型症状，**不是**权限或仓库问题。

实测端口存活：

| 端口 | 状态 |
|------|------|
| 7900 | **死**（`~/.ssh/config` 里当时写着的端口） |
| 7897 | **活** |

与 round6 handoff 及 agent 记忆里 2026-10-02 的记录**一致**：
「7900 已死、实际在 7897」。也就是说 config 在两轮之间又漂回了死端口 7900
（本轮未查到是谁改的）。已把 `~/.ssh/config` 两处 github 主机
从 7900 改回 `127.0.0.1:7897`，fetch 随即成功。

**教训**：端口号在这台机器上不是稳定事实，`~/.ssh/config` 会被别的东西改动。
每次遇到 `port 65535` 先 `Test-NetConnection 127.0.0.1 -p <配置里的端口>`
验活，再决定改哪一头；不要照抄任何一处记录里的端口号，包括记忆里的。

---

## 1. 拉主分支与合并

`git fetch origin --prune` 成功后：

```
git rev-list --left-right --count origin/main...main
0	0
```

**main 与 origin/main 完全一致，无新提交可拉、无需合并。**

## 2. 分支未合并数复查

```
git rev-list --count origin/main..<branch>
```

| 分支 | 最后活动 | ahead | behind | 判定 |
|------|---------|-------|--------|------|
| `feat/mail-config-deploy` | 10-02 10:00:08 | **47** | 114 | 5 分钟前仍在提交 → 并发会话在写，**不动** |
| `email-pipeline-snapshot-2026-10-01` | 10-02 09:59:31 | **141** | 206 | 6 分钟前仍在提交 → 并发会话在写，**不动** |
| `ci/wire-style-guards-into-gates` | 10-02 06:47:30 | 0 | 3 | 无未合并提交，无可收编 |
| `feat/2026-10-01-stt-service` | 10-01 23:54:55 | 0 | 205 | 无未合并提交，无可收编 |
| `audit/2026-10-02-24h` | 10-02 09:58:10 | 0 | 1 | 无未合并提交，无可收编 |

**本轮收编了 0 个分支**，因为没有任何一个分支同时满足「未合并」与「最后活动 >1h」：
有未合并提交的那两个（47 / 141）都只有 5~6 分钟的活动量，属于并发会话正在写；
活动超过 1h 的三个 ahead 全为 0。

佐证并发会话仍在动的旁证：主工作区有一个未跟踪目录 `.scratch-sttdev/`
（不是本轮产生的，未触碰）；`check-test-coverage` 的豁免说明里也写着
「wt-stt 上还有一个 STT 会话在动这块」。

---

## 3. 测试结果

### Go（`backend/`）

| 命令 | 结果 |
|------|------|
| `go build ./...` | exit 0 |
| `go vet ./...` | exit 0 |
| `go test -count=1 ./...` | **exit 0**；53 包 ok、0 cached、11 包无测试文件 |

> **一个必须记下来的假绿来源**：第一次 `go test ./...` 退出码是 0，但那是错的读法 ——
> 命令写成了 `go test ./... | Select-String ...`，PowerShell 的 `$LASTEXITCODE`
> 取的是**管道末端 `Select-String`** 的退出码，不是 `go test` 的。空输出配 0 极易被当成通过。
> 改用无管道写法重取后才拿到真值；且那一轮 53 个包**全是 `(cached)`**，
> 也就是测试本轮根本没执行。`go test -count=1` 才是有意义的证据（cached=0 已确认）。

### 前端 gates

```
cd frontend; npm.cmd run gates   →  exit 0
```

末段输出：`被覆盖 160 / 160`、`✅ 无孤儿测试文件（gates 可达脚本 19 个，覆盖 160/160）`。

> 第一次跑 gates 的 stdout 被会话输出上限截断（最后一行断在半个词 `fuzzI`，
> 且没有退出码），**不能据此判定成败**。改为重定向到文件再取 `$LASTEXITCODE`，
> 才拿到 exit 0。

---

## 4. 审计对象与覆盖度（先说清楚没做什么）

24 小时内 main 上有 **169 个提交**，本轮**没有**逐条复核。实际深挖的是
2 条带「已修复 / 已拦住」措辞、且**自带负控声称**的声明——这类最危险，
因为提交说明本身就是一份未经复核的证据：

- `f8fbd83b` fix(email): background= 与 data-original 被抓取下载后从不替换
- `1641c2e0` fix(api): 五条长路由的客户端超时短于/等于服务端预算

其余 167 条**未审计**，见 §7 遗留风险。

---

## 5. 结论一：`f8fbd83b` 的修复是真的（但护栏比声称的弱）

### 5.1 独立探针（不采信提交说明）

在仓库外写探针直接调 `preloadRemoteImages`，对 **21 个形态**逐个断言
**输出侧**不再残留远程地址：护栏自己列的 9 个形态，外加我另找的 12 个
真实邮件写法（等号两侧有空格、属性名大写、标签内换行、`url()` 大写带空格、
`background` 前有别的属性、协议相对 `//cdn.com/…`、URL 带 `&amp;` 查询串、
同一 URL 同时出现在 `src` 与 `data-original` 等）。

```
baseline: 21 形态，失败 0
```

**21/21 通过。修复本身成立。**

### 5.2 负控（自校验变异是否落盘）

只把**替换侧**的 alternation 退回 `(?:src)`（收集侧不动）：

```
mutate: replacement-side ALT occurrences 2 -> 0; write verified=true
MUTATED=true
NEG(substitution->src): 失败 3 / 9
  RED img data-original
  RED td background（带引号）
  RED td background（无引号）
```

3/9 转红，与提交说明声称的数字一致。**但暴露了两件提交说明没提的事：**

#### 5.2.a `data-src` / `data-lazy-src` 是**死清单项**

上面那 3 条里**没有** `data-src` 与 `data-lazy-src（无引号）`——
因为 `\bsrc` 在 `-` 之后本来就成立（提交说明自己也承认「`data-src` 之所以
碰巧能用，是因为 `\b` 在 `-` 后成立」）。直接验证：

```
mutate: 替换侧删去 data-src / data-lazy-src 两项清单
MUTATED=true
NEG(删死清单项): 护栏内 9 形态失败 0
```

**删掉这两项，0/9 转红。** 也就是说清单里这两行对行为没有任何贡献，
行为完全由 `\bsrc` 的巧合匹配兜着。这不是功能缺陷（今天的行为是对的），
但它意味着**护栏钉不住这两项**：谁把 `\b` 改成别的东西、或者"顺手清理"
这份看似冗余的清单，护栏都不会响。

#### 5.2.b 「清单不得漂移」护栏是**硬编码抽样**，不是派生式检查

那条用例名叫「收集侧与替换侧的形态清单不得漂移（护栏）」，逐个形态端到端跑。
但形态是写死的 9 个字符串，不是从两侧清单**推导**出来的。注入一个真实漂移：

```
mutate: collection gains data-full, substitution unchanged
MUTATED=true
DRIFT(data-full 未列入护栏): 护栏内 9 形态失败 0
```

收集侧多认一个 `data-full`、替换侧没有 → **全绿**。而这正是 f8fbd83b
要消灭的那一类缺陷（下载了却没换上 = 白付一次往返 + 界面仍缺图）。

**根因**：护栏把「清单」写成了「用例表」。要真守住漂移，两侧必须共用同一份
常量/同一张表，护栏从那份表生成用例；否则它只能守住它手写的那几个。

### 5.3 两侧清单在源码层面目前确实是对齐的

我另外把 `collectRemoteImageRefs` 与 `inlineDataUri` 的属性 alternation
逐条对读（都是 `src|data-src|data-original|data-lazy-src|background`，
顺序一致、引号可选、`i` 标志一致）。**今天没有发现新的漂移。**
探针的 21/21 也是这一点的行为侧佐证。

---

## 6. 结论二：`1641c2e0` 的「表驱动护栏」漏了两条路由（含它自己点名的那条）

### 6.1 提交说明 vs 实际

提交说明的表格列了 **6 条**路由，补了 **6 个**客户端常量，并称
「从『逐个断言』改成『一张表』」。实际的 `TABLE` 只有 **4 行**：

| 路由 | 在表里？ |
|------|---------|
| `/api/notes/{id}/summarize` | ✅ |
| `/api/meetings/{id}/summary` | ✅ |
| `/api/meetings/{id}/refine` | ✅ |
| `/api/stt/probe` | ✅ |
| `/api/stt/transcribe` | ❌ **缺** |
| `/api/emails/invoices/extract` | ❌ **缺** |

全仓库 grep 确认：`STT_TRANSCRIBE_TIMEOUT_MS` 与 `INVOICE_EXTRACT_TIMEOUT_MS`
这两个常量**没有任何测试文件引用**。

### 6.2 负控：退回修复前的值，护栏全绿

```
mutate: STT_TRANSCRIBE_TIMEOUT_MS 180s -> 120s（即 1641c2e0 修复前的值）
MUTATED=true
# tests 8 / # pass 8 / # fail 0
```

**把常量改回它被修好之前的值，这条护栏一声不响。**
`/api/stt/transcribe` 恰恰是提交说明里专门用一整段论证「**相等即错**」的那两条之一
（客户端 120s == 服务端 120s，客户端实际总是先到点）。它现在值是对的，
但**没有任何东西守着它是对的**。

### 6.3 已修：把 `/api/stt/transcribe` 补进表

服务端 `handleSttTranscribe`（`server_assistant.go:2455`）里有
`context.WithTimeout(r.Context(), 120*time.Second)`，可从源码反推，
所以这一行能进同一张表。+11 行：

```js
{
  route: 'POST /api/stt/transcribe',
  tsFile: 'api/stt.ts',
  constName: 'STT_TRANSCRIBE_TIMEOUT_MS',
  server: { goFile: 'server_assistant.go', handler: 'handleSttTranscribe' },
  note: '会议长录音转写（「相等即错」那一条）',
}
```

验证：

```
# tests 9 / # pass 9 / # fail 0        ← 补行后基线绿
# 负控：常量退回 120_000
not ok 5 - POST /api/stt/transcribe 的客户端超时严格大于服务端预算
  error: 'STT_TRANSCRIBE_TIMEOUT_MS=120000ms 必须大于服务端 120000ms
          （handleSttTranscribe: 120S on r.Context()）…'
# pass 8 / # fail 1                    ← 新行承重，不是装饰
```

**新行实测转红，且报的是「120000 必须大于 120000」这条相等即错的原话。**

### 6.4 未修（有意留下）：`/api/emails/invoices/extract`

这条**没有**补，理由是补不了而不是漏了：`handleEmailInvoiceExtract`
根本没有派生自 `r.Context()` 的 `WithTimeout`（耗时全在回 IMAP 的
`FetchMessageRaw` 上）——同一文件里那条自检用例正是在断言这件事。
本表的推导方式是「从 `context.WithTimeout(r.Context(), …)` 反推」，
对它返回 `null`。

有意思的是，表的注释里**描述了一种 `server` 为 null 的行类型**
（「这时只能给一个下限依据 —— 这里用 harvest 的 5 分钟作为同类操作的参照」），
但 `TABLE` 里**没有任何一行是这种形状**。也就是说：写注释的时候
以为自己写了，实际没写。要覆盖它需要先定一个诚实的判据
（例如与同类 IMAP 操作对表、或改成显式白名单并写明「无上限」是合法取值），
那是设计决定，不该在审计轮里现编一个数字上去。

`INVOICE_EXTRACT_TIMEOUT_MS = 3 * 60_000` 现状正确，缺的是护栏。

---

## 7. 遗留风险

1. **本轮只审计了 169 个提交里的 2 条。** 其余 167 条「已修复/已拦住」的说法
   仍是**未复核的声明**。特别是同样自带负控声称、且形制与本次两条相似的：
   `c01e555e`（CRLF 静默废掉负控）、`6eb03095`、`8cc01dfd`、`c1d1e675`（归属校验）。
2. **图片形态护栏仍是抽样。** §5.2.b 证明了新增一个未列形态造成真实漂移时它全绿。
   要真守住，两侧共用一份清单常量、由它生成用例。
3. **`data-src` / `data-lazy-src` 的行为靠巧合维持**（§5.2.a）。
4. **`/api/emails/invoices/extract` 仍无护栏**（§6.4）。
5. **两个并发分支仍有大量未合并提交**：`feat/mail-config-deploy` 47 个、
   `email-pipeline-snapshot-2026-10-01` 141 个。其中已知包含
   `last_synced_at` 推进修复与 `Scheduler.Stop` 幂等性修复——**这些都还不在 main 上**。
   它们活跃度 <1h，按判据不动，但下次收编轮要优先处理。
6. **gates 的一条豁免依赖外部状态**：`flashcardIo.test.ts` 的豁免理由写明
   另一个 STT 会话正在补这块；补完或删除后需从豁免里移除，否则会变成永久静默。

---

## 8. 本轮用到的命令

```powershell
# 通道
Test-NetConnection 127.0.0.1 -Port 7900 / -Port 7897
git fetch origin --prune
git rev-list --left-right --count origin/main...main
git for-each-ref --sort=-committerdate --format='%(refname:short)|%(committerdate:iso8601)' refs/heads
git reflog show --date=iso <branch>          # 判「最后活动」用 reflog 而非 committerdate
git rev-list --count "origin/main..<branch>"

# 测试
cd backend;  go build ./... ; go vet ./... ; go test -count=1 ./...
cd frontend; npm.cmd run gates

# 负控（探针在仓库外 C:\Users\86133\probe\image-probe.mjs，不进 gates）
node image-probe.mjs baseline
node image-probe.mjs mutate-substitution
node image-probe.mjs drop-datalazy
node image-probe.mjs drift-unlisted-form
node --test src/api/__tests__/api-timeout-budget-table.test.mjs
```

**负控纪律**（本轮实际生效的）：每个变异脚本先回读目标文件断言标志位
（`MUTATED=true` / `write verified=true`），不匹配就 `exit 2` **拒绝继续**。
第一次跑负控时它正是因为我自己把 alternation 当正则逐项计数、写错了期望值而
拒绝执行——那次拒绝是对的，若当时接着跑测试就会拿到一个无意义的绿。
所有变异用完立刻 `git checkout --` 还原，并复核还原后的取值。
