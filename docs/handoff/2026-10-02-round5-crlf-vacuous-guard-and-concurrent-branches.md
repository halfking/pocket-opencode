# 2026-10-02 round5 —— 判据被 CRLF 静默废掉，以及并发会话下的分支审计

> 触发：`/goal` 定时任务（拉主分支、清理不活跃子分支、批判与审计 24 小时内的提交、本地修正、合并推送）。
> 执行窗口：2026-10-02 06:46 起。
> 基线：本轮开始时本地 `main = 6c3b1bc6`（落后 `origin/main` 13 个提交），fast-forward 后为 `fbdf65b1`。

---

## 0. 一句话结论

本轮**真正的缺陷只有一个，但它的性质比它看起来严重得多**：

`email-refresh-trigger.test.mjs` 里有一条**负控**（用来证明"把时间戳挪到请求之后"这个变异会让判据转红）。
它靠一个**跨行字符串针**去匹配源文件，而这台机器 `core.autocrlf=true` 会把工作区的 `.ts`
检出成 **CRLF**，仓库里存的却是 **LF** —— 于是 `.replace()` **静默不命中**，
负控样本等于"根本没变异过"，**判据从此失效**。

危险的地方在于：**CI 永远抓不到它**。`.github/workflows/*.yml` 全部 `runs-on: ubuntu-latest`，
Linux 检出是 LF，针必然命中。所以这是一个"CI 绿灯几个月、本机一跑就红"的判据；
反过来也一样 —— 谁在 Windows 上把针改成永远匹配不到的样子，CI 也不会拦。

第二个结论关于分支：**本轮不该合并任何分支，也不该删除任何分支**，理由见 §3，
这是本轮最重要的判断（而不是"什么都没做"）。

---

## 1. 根因

### 1.1 现象

```
$ cd frontend; npm.cmd run gates
ℹ tests 1444   ℹ pass 1443   ℹ fail 1

✖ 把时间戳挪到请求之后 → 转红
  AssertionError: 负控样本没有真的挪走时间戳（替换没命中）
```

### 1.2 机制

出问题的两行（`src/features/email/__tests__/email-refresh-trigger.test.mjs`）：

```js
const broken = codeOnly(hostSrc.replace(
  'state.lastAttemptAt = now\n    state.inFlight = true',   // ← 跨行 needle
  'state.inFlight = true',
))
assert.notEqual(broken, hostCode, '负控样本没有真的挪走时间戳（替换没命中）')
```

实测字节（`email-fetch-host.ts`）：

| 位置 | 行尾 | `needle` 能否命中 |
|---|---|---|
| git blob（仓库里存的） | LF | ✅ `true` |
| 工作区（Windows 检出） | **CRLF**（72 CRLF / 0 LF） | ❌ `false` |

```
worktree raw_match:        false
worktree normalized_match: true
STORED    raw_match:       true
```

于是 `.replace()` 返回原串，`broken === hostCode`，`assert.notEqual` 转红。

注意这条断言的**形状是对的**：它没有静默通过，而是**主动报了"替换没命中"**。
这是本仓库这批负控设计得好的地方 —— 作者当年就想过"变异没生效"这件事。
它只是没料到替换会因行尾而失效。所以真正要补的是**让 needle 不依赖行尾**，
以及**一张能抓住"needle 已失效"的卡口**。

### 1.3 为什么 CI 看不见

`.github/workflows/*.yml` 里 6 个 job 全是 `ubuntu-latest`；仓库根**没有 `.gitattributes`**，
而本机 `core.autocrlf = true`。两件事叠加就是这个 bug 的温床：
**行尾由检出机器决定，而判据的有效性依赖行尾。**

同类隐患其实一直存在，只是没人写跨行 needle 所以没爆：
`email-job-runtime-singleton.test.mjs` 与 `note-transcription-cancellable.test.mjs`
读的目标文件同样是 CRLF，只是它们把跨行串用在 `.replace()` 的**第 2 个参数**
（插入文本），插入什么换行风格都不影响命中，所以侥幸没事。

---

## 2. 改动文件与关键行为

| 文件 | 改动 | 行为 |
|---|---|---|
| `frontend/src/features/email/__tests__/email-refresh-trigger.test.mjs` | 读文件后加 `.replace(/\r\n/g, '\n')` 归一化 | 判据不再依赖检出机器的换行配置；Windows / Linux 结果一致 |
| `frontend/scripts/check-crlf-fragile-needles.mjs` | **新增**卡口 | 扫出"跨行 needle 指向 CRLF 源文件、且该测试没归一化"的组合 |
| `frontend/package.json` | 新增 `check:crlf-needles`，并接进 `gates` | 本机 `npm run gates` 会拦 |
| `.github/workflows/frontend.yml` | 新增 CI 步骤 `Guard against EOL-fragile multi-line needles` | 合并前就拦，不等某个 Windows 开发者本地踩 |

### 2.1 卡口为什么长这样（三版才做对）

这张护栏本身**废了两版**，过程值得记下来 —— 它是"绿灯不等于结论成立"的又一例：

**第一版（静态扫"有没有跨行 needle"）**：对**已经修好**的
`email-refresh-trigger.test.mjs` 仍然报警。因为那个测试确实有跨行 needle，
只是它读文件时归一化了。静态扫描**看不见运行时归一化**。
→ 换成"拿 needle 去真实匹配一遍"。

**第二版（动态匹配：原始文本 / LF 归一化文本都试）**：修好后放行，
但把归一化**删掉之后仍然放行** —— 因为 needle 在 LF 文本上照样命中。
两种状态在护栏眼里一模一样，护栏等于没有。
→ 补上第三个问题：**这个测试自己有没有做归一化？**
只有「needle 只在 LF 上命中」**且**「测试没归一化」才算缺陷。

**第三版（当前）**：静态定位 + 动态实测 needle 命中 + 反查测试是否归一化。

另外两个必须记的坑：

- **第一版护栏对事故本体报 0 违规**。原因是路径解析只拿裸字面量去 `resolve`，
  而真实测试写的是 `path.join(SRC, 'features', 'email', 'email-fetch-host.ts')` ——
  末段单独拿去 resolve 根本不存在。**护栏不报警 = 护栏是废的**，
  和"判据静默失效"是同一族病。改成实参拼接后才抓到。
- **护栏一度被自己的注释骗到**。我在修复时把那个 needle 写进了注释里，
  于是护栏开始要求"去改你的注释"。判据必须先剥注释（`stripComments`）再扫。
  同一族：`guards-need-negative-controls-more-than-tests` 里记的
  "注释满足了对 bug 的检查"。

### 2.2 卡口的负控（护栏自己的护栏）

`NEG-1` 重新引入 bug（删掉归一化）→ 卡口 **FIRE** 且指名道姓报出该文件。
`NEG-1b` 源文件强制 CRLF + 有归一化 → 卡口 **PASS**（不误报）。
`NEG-2` 把 needle 改成永远匹配不到 → 卡口仍 **FIRE**（守卫本身不是空转）。

> NEG-2 第一版**没通过**，暴露了一个真问题：单次 `.replace()` 只命中第一处，
> 而第一处在我新写的**注释**里，真 needle 毫发无损。必须 `replaceAll`。
> 这本身就是"注释满足了对 bug 的检查"的又一次现形。

---

## 3. 分支审计：本轮为什么**不**合并不**不**删

这是本轮最需要交代的判断。把结论和依据写清楚，便于下一轮直接接。

### 3.1 事实

开工时（06:46）实测：

| 分支 | 尖端提交时间 | 未合并提交 | 状态 |
|---|---|---|---|
| `feat/mail-config-deploy` | **3 分钟前** | 25 | 🔴 正在被写 |
| `email-pipeline-snapshot-2026-10-01` | **9 分钟前** | 117 | 🔴 正在被写 |
| `ci/wire-style-guards-into-gates` | 6 分钟前 | 0 | 已全合 |
| `feat/2026-10-01-stt-service` | 7 小时前 | 0 | 已全合，但工作区脏 |

并且**文件系统层面证实了并发写入**，不是只靠 git 提交时间：

```
wt-maildeploy  最新写入 06:47:19  backend/internal/email/invoice_attachment_harvest_wiring_test.go
wt-email       最新写入 06:47:13  backend/internal/server/server_email_invoice_dispatch_test.go
（当时 06:47:43）
```

**在我开始审计后的 40 秒内，两个工作树都有秒级写入。**
另有 6 个 worktree 注册（`wt-apkbuild` / `wt-email` / `wt-font` / `wt-maildeploy` / `wt-stt`）。

### 3.2 结论

- **两个有未合并提交的分支，一律不碰。** 它们不是"24 小时内不活跃的子分支"，
  而是**正在被别的会话使用的工作区**。合它们 = 覆盖别人写到一半的工作；
  删它们 = 毁掉别人正在进行的工作。
  （本机已发生过两次实证，见 memory：`parallel-sessions-share-one-git-identity`、
  `push-rejected-after-concurrent-session-pushed`。）
- **`feat/2026-10-01-stt-service`：0 未合并提交（已全量合入 main），但不删。**
  它的 worktree 有 **22 个未提交改动**。逐文件比对（忽略行尾）后：
  - 1 个已与 main 一致；
  - **21 个"与 main 不同"**。但这 21 个里大部分是**比 main 小**的
    （`server_email_pipeline.go` main 18715B / wt 17421B、
    `scheduled_task_integration_test.go` main 11788B / wt 9788B）——
    这是**落后 201 个提交的陈旧副本**，合过去等于**回退** main 上更新的代码。
  - 少数几个 wt 比 main 大（`stt/discovery_test.go`、`docs/handoff/…stt-full-incremental.md`、
    `android/README.md` 等），里面有真实内容（如 modality 从 text 变 audio 的复核结论、
    606 个模型的清单复核），**值得抢救，但不适合在这个落后的基线上整体合**。
  - 结论：**先不动**。抢救应是"按文件取内容、落到当前 main 上"，
    不是"把这个 worktree 合并"。留给下一轮显式处理。
- **`ci/wire-style-guards-into-gates`：0 未合并提交，已全量合入**，但它的工作树
  `wt-font` 6 分钟前还有活动，同样按"不碰"处理。

> 换句话说：**符合"24 小时内 + 超过 1 小时不活跃 + 未合并"三个条件的分支，本轮为 0 个。**
> 那些看起来"该清理"的分支，要么已被别人接手（3/9 分钟前还在写），
> 要么工作区里压着不能安全丢弃的未提交内容。

---

## 4. 对近期提交的审计

对 `origin/main` 近 25 个提交逐条看过，值得记的三条：

### 4.1 `fbdf65b1`（本轮拉下来的最新提交）—— 结论：判断正确，予以保留

它记录了一处**预算死区**：`handleNoteSummarize` 给了
`context.WithTimeout(r.Context(), 60*time.Second)`，但非流式调用走
`llmgateway.Client`，而那个客户端设了 `Transport.ResponseHeaderTimeout = 30s`。
响应头要到上游真正开始回包才发出；对**推理模型**（网关自动路由到 glm-5.2，
先花 token 在 `reasoning_content` 上、正文最后才吐）来说，"开始回包"本来就可能晚于 30s，
于是实际预算是 `min(60s, 30s) = 30s`，30~60s 是死的。

**我独立复核了这条链路，没有发现它的推理有误**：
`handleNoteSummarize` → `llmbff.ChatRequest` → `dynamicGatewayBFFProvider.Chat`
→ `llmgateway.NewClient`（`ResponseHeaderTimeout: 30s`）→ `chatOpenAI` 用 `c.Client.Do`。
非流式路径确实被那个 30s 卡住。

它**只加注释不改数值**、并写清"若将来决定调大要同步改哪条测试"，我认可这个取舍：
`TestNewClient_TransportTimeouts` 锁着 30s，而那个值是为 2026-08-31 的
"上游 model 挂死、前端等满 60s"事故刻意加的。调大会把"快速失败"换回来 ——
**这确实需要人拍板，不该由一轮自动任务单方面改。** 故本轮不动。

> 补充一条它没提到、但对将来决策有用的观察：
> `ResponseHeaderTimeout` 是 **transport 级**的，流式与非流式一视同仁。
> 而 2026-08-31 那次事故是**流式**挂死（错误以 SSE error 事件写回）。
> 也就是说，真正需要 30s 快速失败的是**流式**路径；
> 非流式路径被同一个 30s 连带压住，很可能是**可以分开设**的
> （例如非流式用独立的 Transport / 更宽的 header 预算）。
> 这只是线索，未验证，**留给下一轮**。

### 4.2 `a7fbc545` —— 结论：修复正确，但**它自己带的负控在本机是坏的**

这条提交修了两个真实缺陷（冷启动不拉取 / 断网一次锁死 15 分钟），
并且附了一批负控。修复本身没问题，**问题出在负控的载体**：
`email-refresh-trigger.test.mjs` 就是本轮 §1 的事故现场。
即：**修复有效，护栏有缺陷**。这正是本轮修的东西。

### 4.3 其余

`4ed2d4d8`（679 处 font-size 收敛）、`1641c2e0`（客户端超时表驱动护栏）、
`927eae45`（笔记总结 + 4 条长路由的 30s WriteTimeout）、`b2310d88`（兜底转写可中止）
等，本轮未发现新问题，后端 `go build ./...` 与 `go test ./...` 全绿。

---

## 5. 测试命令与结果

全部在 `main`（= `fbdf65b1`）上执行。

| 命令 | 结果 |
|---|---|
| `cd backend; go build ./...` | ✅ exit 0 |
| `cd backend; go test ./...` | ✅ exit 0，无 FAIL |
| `cd frontend; npm.cmd run gates`（修前） | ❌ exit 1，1444 用例中 1 红（即 §1 那条负控） |
| `cd frontend; node --test src/features/email/__tests__/email-refresh-trigger.test.mjs` | ✅ 9/9 通过 |
| `cd frontend; npm.cmd run gates`（修后） | ✅ 见下 |
| `node scripts/check-crlf-fragile-needles.mjs` | ✅ exit 0，162 个测试文件全部实测命中 |
| `node .scratch/negctl-crlf.mjs` | ✅ NEG-1 / NEG-1b / NEG-2 全过 |
| `node .scratch/negctl-guard.mjs` | ✅ 护栏确实会 FIRE，且对已修代码保持沉默 |

> 注意：本机 PowerShell 会拦 `npm.ps1`（执行策略），**必须用 `npm.cmd`**。
> 用 `npm` 会得到一个假的失败信息（"禁止运行脚本"），不是测试真的挂了。

---

## 6. 遗留风险

1. **仓库根没有 `.gitattributes`。** 行尾完全由每台机器的 `core.autocrlf` 决定。
   本轮只在**消费方**（测试）做了归一化，属于治标。根治办法是加
   `.gitattributes`（例如 `* text=auto eol=lf`）统一规范化，
   但那会**大面积改动行尾**，必须单独一轮做、单独验证，不能夹在这次修复里。
2. **同类隐患可能还有，只是没人写跨行 needle。** 卡口只覆盖
   "读源文件 + 跨行 needle"这一形态。若将来出现"跨行**正则**"
   （`/foo\nbar/`）或 `.split('\n')` 之类的判据，卡口不覆盖。
3. **`feat/2026-10-01-stt-service` 的 21 个未提交文件仍然悬着。**
   其中 `stt/discovery_test.go`、`docs/handoff/2026-10-01-stt-full-incremental.md`、
   `android/README.md`、`android/package.json`、`android/capacitor.config.ts`、
   `frontend/android/capacitor.settings.gradle` 有真实内容待抢救。
   **不要**直接把这个 worktree 合并（会回退 main）。
4. **`fbdf65b1` 记的 30s/60s 预算死区仍在。** 需要人决定是否给非流式路径
   单独放宽 header 预算（见 §4.1 的线索）。
5. **并发会话仍在写这个仓库。** 本轮任何"删除分支/清理 worktree"的动作
   都可能毁掉别人未提交的工作。下一轮开工前应重新确认活动情况，而不是沿用本轮结论。

---

## 7. 下一轮提示词

```
接着 openpocket 的 24 小时审计。先做三件事，再谈别的：

1. 重新确认并发状况（不要沿用上一轮结论）：
   - git fetch --all --prune（本机需
     $env:GIT_SSH_COMMAND='ssh -o ProxyCommand="C:/Progra~1/Git/mingw64/bin/connect.exe -H 127.0.0.1:7897 %h %p"'
     —— 代理端口会漂移，7900 已死，实测前先 Test-NetConnection）
   - 对每个 worktree 扫最近修改的文件 mtime；尖端提交在 1 小时内的分支一律不碰。

2. 抢救 feat/2026-10-01-stt-service 的未提交内容（**不要合并整个 worktree**）：
   那个 worktree 落后 main 200+ 提交，整体合并会回退。
   逐个文件把"wt 比 main 新"的那部分内容（stt/discovery_test.go 的 modality 复核、
   docs/handoff/2026-10-01-stt-full-incremental.md 的 §1.4 606 模型清单、
   android/ 三个指针文件、frontend/android/capacitor.settings.gradle）
   摘出来落到当前 main 上，逐个验证。落后的那些（server_email_pipeline.go 等）直接丢弃。
   抢救完再删分支与 worktree。

3. 决定 fbdf65b1 记的「非流式 30s 预算死区」要不要修。
   线索：ResponseHeaderTimeout 是 transport 级的，流式与非流式共享；
   而 2026-08-31 那次挂死事故是流式的。可以考虑给非流式单独一个更宽的
   header 预算而不动流式的 30s。这条线索未验证，先写个能证明"非流式确实被
   30s 卡住"的测试，再谈改数值——测试要能在改动前转红。

另外两件小事：
- 考虑单独一轮加 .gitattributes（* text=auto eol=lf）根治行尾问题，
  范围大、要单独验证，别夹带。
- 根目录 0 字节 go.mod 那轮的卡口还在，确认它没被回退。
```
