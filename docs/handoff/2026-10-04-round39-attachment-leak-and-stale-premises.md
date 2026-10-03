# round39 —— 24h 修正审计：附件字节漏进摘要、一条把主干判红的护栏，以及一批被上游改掉的判据前提

日期：2026-10-04 03:46 → 04:35
分支：`audit/2026-10-04-main-health`
基线：`c02dfb11` → 合并并发会话后为 `a15fd839`

---

## 0. 结论先说

**三件事，两件是真缺陷、一件是判据自身过期。**

1. **新泄漏（本轮修）**：`snippetFromMIMEParts` 在
   `multipart/mixed` 里遇到「PDF 附件排在 text/plain 之前」时，
   摘要是 `%PDF-1.4`。这是本仓的核心业务形态（带发票 PDF 附件的邮件）。
2. **主干 CI 是红的（本轮修）**：`824a0391` 带进来的
   `diag_real_fetch_snippet_stages_test.go` 触发 PG 隔离守卫规则 2。
   **在 pristine origin/main 上实测同样红**，与本轮改动无关。
3. **一批判据的前提被上游改掉（本轮订正）**：主工作区那批未提交的
   `BODY[]` 补拉改动，其用例夹具所依赖的「`DeriveSnippet` 取不到正文」
   已被 `180a157b` 修好 ⇒ 三条用例红的形态是「断言摘要为空」，
   **症状会被读成「补拉逻辑坏了」，而真正的原因是「用例的前提失效」**。

修完 `go build` / `go vet` / `go test ./... -count=1` / `check-gofmt` 全绿。

---

## 1. 新泄漏：PDF 附件的字节被当摘要

### 1.1 现象

`multipart/mixed` 里 **PDF 附件排在 text/plain 之前**时，
列表摘要变成 `%PDF-1.4`。实测读数（`mimeParts` 拆出 3 个 part，
容器头也算一个）：

| part | 声明 | `ParseMIMEMessage` 解出的 `TextBody` |
|---|---|---|
| [0] | `multipart/mixed` | 空 → 被跳过 |
| [1] | `application/pdf` (base64) | `%PDF-1.4\n` ← **泄漏源** |
| [2] | `text/plain` | 真正的正文，被 [1] 盖掉 |

### 1.2 机制

`snippetFromMIMEParts`（`snippet.go` 第 0 步，`180a157b` 引入）
按 part 收集候选，分 plain / html 两组，再按「plain 优先」返回。
收集时**只看**两件事：

```go
normalizeWhitespace(msg.TextBody) != "" && !containsMIMESource(msg.TextBody)
```

`application/pdf` + base64 的 part，解码后是 `%PDF-1.4\n`，
落进 `TextBody`；非空，且**不含** `Content-Type:` / `boundary=`
这类 token（`containsMIMESource` 只查这些）⇒ 两道闸门都放行
⇒ 排进 plain 组 ⇒ PDF 在前，赢了真正的正文。

**判据自己宣称的那件事，和它实际问的那件事，不是同一件事：**

- 宣称：「优先 text/plain、其次 text/html」
- 实问：解出来非空，且不含 MIME token

那句「优先」此前只体现在**收集顺序**上，**没有任何一处排除非文本 part**。
附件解码出的字节「读起来像文本」与它是不是正文毫无关系。

### 1.3 为什么不能靠 `containsMIMESource` 兜住

那条闸门查的是 MIME **源码**痕迹，PDF 二进制里一个都没有。
补 token 是拿一类文件的巧合换另一类文件的确定性。

### 1.4 修法

新增 `partDeclaresTextContent`（`snippet.go`），在收集前剔除非文本 part。
只看 part 头部那一段（在第一个空行之前），不碰正文。三条边界：

1. **没有 `Content-Type` 头** → 按 RFC 2045 §5.2 缺省 `text/plain`，收下。
   「无头」与「声明了 `application/pdf`」不能走同一条路。
2. **声明了但 `mime.ParseMediaType` 读不懂** → 不收。判不清时往
   「少一段摘要」那侧倒。
3. **显式 `text/*`** → 收。charset 一律不看（解码由
   `ParseMIMEMessage` 负责，摘要出口另有 C0 净化）。

用 `mime.ParseMediaType` 而非手写字符串切分，与 `mime.go:492` 既有写法一致。

### 1.5 判据的牙齿：三个变异，各自精确命中

按本仓规矩，**绿灯不算数，除非先见过它红**。
`snippet_attachment_leak_test.go` 共 5 条用例：

| 变异 | 期望 | 实测 |
|---|---|---|
| 原始代码（闸门不存在） | 红，摘要=`%PDF-1.4` | ✅ 红 |
| 变异 1：闸门恒 true | 红 | ✅ 红，主判据 |
| 变异 2：闸门恒 false（过宽，正文被砍） | 红 | ✅ 红，主判据 |
| 变异 3：放宽成「只排除 `application/`」 | 红 | ✅ 红，**两条**边界用例 |

**变异 1 与变异 2 都必须红** —— 只红一条说明闸门过宽或过窄，
而这两者恰好是各自方向的极端。

主判据同时断言「取到真正的正文」与「不含 `%PDF`/`Content-Disposition`」：
只断言后者的话，**返回空串也能过**。

协议层另有一条：`partDeclaresTextContent` 必须**排除**附件 part，
不能只断言最终摘要 —— 「被闸门排除」与「恰好排在后面」
在结果上完全无法区分。

反向用例两条：声明 `application/pdf` 但正文是中文的 part **必须被排除**
（判据看声明不看内容）；畸形 `Content-Type: text`（缺 subtype）必须保守拒收。

---

## 2. 主干 CI 是红的

### 2.1 与本轮改动无关

`TestPGTestsNeverTargetTheProductionSchema` 判
`internal/email/diag_real_fetch_snippet_stages_test.go` 失败。
该文件由 `824a0391` 带入。

**取证**：另开独立 worktree checkout pristine `origin/main`（`2a3bc73a`），
不带本轮任何改动，同一条命令同样红。⇒ 既有缺陷。

### 2.2 逐项核对

- 写语句：扫 `INSERT|UPDATE|DELETE|DROP|TRUNCATE|ALTER|CREATE`，**0 命中**；
  `.Exec(` **0 次**；DB 调用只有 3 处 `pool.Query` / `pool.QueryRow`。
- 门控：`PG_DSN` + `POCKET_REAL_KEYS`，**均无缺省值**，缺任一即 `t.Skip`。
  `PG_DSN` 与 CI 设的 `POCKET_TEST_POSTGRES_DSN` 是**不同变量** ⇒ CI 里恒 skip。
- `search_path` 取自 DSN，并用 `current_schema()` **读回逐字比对**，
  不符即 `t.Fatalf`（L85-90）。文件自己的注释写着「以为钉住了是这个缺陷
  家族的标志，所以当场验一次」。
- 指向生产 schema 是**目的**：它要定位真实库里仍带 QP 源码的那批
  snippet。自建隔离 schema 会让它查成空集，输出「已全部干净」的假结论
  —— 与 `reminder_notified_diag_test.go` 同坑（那条注释记着
  `if highUnnotified == 0 { 不是缺陷 }` 在空库上必然成立）。

### 2.3 为什么是登记而不是改代码

本仓对「打开 PG 连接但不隔离 schema 仍然安全」的文件有既定做法：
`pgSafeWithoutIsolation` 逐个列出并写明可核查的理由。
`diag_schema_present` / `ledger_realdata_diag` / `reminder_notified_diag`
与它同族。

改代码去满足词法判据（硬造一个 `*_test_` 字面量）才是把安全代码改危险
—— 守卫自己的注释明写「不要靠把理由写宽松来绕过」。

### 2.4 负控：证明这条登记不是装饰

往该文件注入 `DELETE FROM emails WHERE id = 'negctl'` 后重跑，
护栏如期转红并点名「没有登记到 `pgAllowlistedWrites`」
（规则 4 独立于规则 2 生效）。撤掉注入后复测绿。

**这条比 round 38 记的那个失效登记强**：那次写进
`pgAllowlistedWrites` 的条目**作用域写错了**（守卫规则 4 的放行条件
限定在 `pgSafeWithoutIsolation` 之内），从写下那天起永远读不到；
本次作用域正确且经负控实测会红。

---

## 3. 一批判据的前提被上游改掉了

### 3.1 背景

主工作区 `C:\workspace\openpocket` 有一批 **2026-10-03 22:51** 的未提交改动，
产出方是会话 `mvs_1fd1f45a`（「修复 fetcher.go 缺失 BODY[] 断言」）。
该会话已归档、**未提交**就结束。

内容：`fetchSnippetOnConnected` 在 `BODY[TEXT]` 取不到正文时补拉一次 `BODY[]`
（上限 1MB），发票链接判定改用整封字节；配三条用例；
另修两处被改动打红的护栏。

### 3.2 前提怎么失效的

那批用例的夹具是「外层分片头 + 内层 part 头 + 正文、**没有终止分隔行**」，
并断言 `DeriveSnippet` 对它返回空串。

**合并 origin/main 之后那个断言不再成立**：`180a157b` 给 `DeriveSnippet`
加了第 0 步 `snippetFromMIMEParts`，它对这一形态**能**取到正文。

实测四个形态的读数：

| 形态 | 修复前 | 现在 |
|---|---|---|
| 分片头 + 内层 part 头 + 正文（无终止行） | 空串 | **正文**（`180a157b` 修好） |
| 分片头 + 内层 part 头 + 空正文 | 空串 | 空串 |
| 只有容器头 | 空串 | 空串 |
| 完整报文 `BODY[]` | 正文 | 正文 |

⇒ 三条用例全红，且红的形态是「断言摘要为空」。

### 3.3 关键：判据红不等于代码坏了

这里红的是**用例的前提**，而前提失效的证据是它旁边那条
`TestZZSnippetFallback_Precondition_*` 的报错文本
（「前置条件不成立：BODY[TEXT] 形态本来就能解析出 …」）。

**若没有那条自检**，用例会以「摘要为空」的形式红，
症状会被读成「补拉逻辑坏了」—— 指向完全相反的方向，
接下来会去改一段本来正确的生产代码。

⇒ 夹具换成 `zzBodyTextContainerOnly`（「服务端只发了容器前缀、
正文压根没发过来」），那是 `BODY[]` 真正覆盖的场景；
前置自检保留在文件里，并写明它红的原因与形态表。

### 3.4 补拉逻辑本身仍有效，负控实测有牙齿

| 变异 | 期望 | 实测 |
|---|---|---|
| 变异 A：去掉补拉，直接返回空摘要 | 红 | ✅ 红，`仍取不到正文：""` |
| 变异 B：无条件补拉（去掉「只在取不到时才补拉」） | 红 | ✅ 红，`已经够用却补拉了整封报文` |

变异 B 是必需的：每封都补一次整封拉取，一轮同步的流量与耗时都会翻倍，
而这种浪费在摘要正确的邮件上**完全看不出来** ——
只断言「摘要对不对」永远发现不了。

### 3.5 顺带修掉两处被改动打红的护栏

- `TestFetcherUsesDeriveSnippetAtEverySnippetSite` 的调用点计数 3 → 4，
  并逐一列名四个点位。
- `TestBodyHasInvoiceLink_IsWiredOnRawMIME` 原来只查 fetcher.go 里的
  **第一个**调用点、且要求实参字面写成 `bs.Bytes`。补拉那次改动把字节
  变量改名成 `raw`/`whole`，于是它在报「实参不是原始 MIME 字节：
  `bodyHasInvoiceLink(raw)`」—— 报的是**变量名**，不是那个要守的性质。
  改成走 `go/ast` 取实参逐个检查「必须是裸字节标识符且不含
  `DeriveSnippet`」，并把调用点数钉死为 2。

  这条第一版用正则收实参（`bodyHasInvoiceLink\(([^()]*)\)`），
  在负控下是**绿的**：负控把实参换成 `[]byte(DeriveSnippet(raw, 500))`，
  而 `[^()]*` 匹配不到带括号的坏实参，那个坏调用点被静默跳过，
  只剩另一个合法调用点通过检查。
  **「判据扫不到坏形态」和「判据没发现缺陷」在结果上完全一样。**

---

## 4. 分支与 worktree 审计（24h 内）

`git rev-list --count origin/main..<branch>` 实测：

| 分支 | 未并入 main | 处置 |
|---|---|---|
| `audit/2026-10-04-main-health` | 2 → 本轮已并入 | 保留（承载本轮 3 个提交） |
| `fix/i18n-nested-placeholder-crash` | 0 | 已并入 |
| `fix/email-upsert-messageid-conflict` | 0 | 已并入 |
| `audit/gofmt-debt-20261003` | 0 | 已并入（远端已 gone） |
| `docs/round37-section10` | 0 | 已并入（内容已在 main） |

**本轮没有删除任何分支或 worktree。** 原因见 §6。

---

## 5. 测试

在 `openpocket-wt-audit38`（`a15fd839` 合并后）实测：

```
go build ./...                                        → 0
go vet ./...                                          → 0
go test ./... -count=1                                → 全绿，无 FAIL
node scripts/check-gofmt.mjs                          → 真债 0（931 个 .go）
node scripts/check-pg-schema-hardcoded.mjs            → OK
node scripts/check-pg-schema-scope.mjs                → OK（19 个脚本）
node scripts/check-cdp-pid-strict.mjs                 → OK
```

关键那两条守卫：

```
go test ./internal/server/ -run TestPGTestsNeverTargetTheProductionSchema -count=1
  修复前：FAIL（点名 diag_real_fetch_snippet_stages_test.go）
  修复后：PASS
  负控（注入 DELETE FROM emails）：FAIL，点名「没有登记到 pgAllowlistedWrites」

go test ./internal/server/ -run TestPGIsolationNegativeControlMatrix -count=1
  PASS：正控 541 个文件全绿；负控B（规则 2）27 个已隔离文件全部转红；
        负控C（规则 4）21 个豁免文件转红
```

### 未验证项（明说，不假装覆盖）

- **泄漏的证据是代码级可复现，不是「真机上看到过」。**
  手段是拆出 3 个 part 并逐个打印 `TextBody` 读数。
  若要确认真库影响，跑 `diag_real_fetch_snippet_stages_test.go`
  （需 `PG_DSN` + `POCKET_REAL_KEYS`，只读）。
- **没有对着真库跑 PG 用例。** 本机 5432 的 Postgres 是另外几个并发
  会话在用的共享资产，刻意没有把 `POCKET_TEST_POSTGRES_DSN` 指过去。
  ⇒ 只验证到「编译通过 + 无 DSN 时干净 skip」。
- **前端门禁没跑**：新 worktree 无 `node_modules`，且不想与并发会话
  抢设备/端口。`check:gofmt` 与三个 `check-*.mjs` 不依赖它，已跑。
- **真机 UI 层没验**：`.maestro/messages-hub.yaml` 的 3.5.2 断言
  仍未启用 —— 那是另一个会话的实验面，本轮不动。

---

## 6. 并发实况

开工时实测**除本会话外还有 2 个 openpocket 会话在跑**：

| 会话 | 标题 | 状态 |
|---|---|---|
| `mvs_8a0f6bf8…` | 完善邮件定时收取与发票处理需求 | idle（net::ERR_CONNECTION_RESET） |
| `mvs_9ba2f519…` | 对齐主分支并用 Maestro 真机测试修复功能 | started（invalid access token） |

另外两个产出过本轮所见改动的会话（`mvs_1fd1f45a`、`mvs_c11edd54`）
**均已归档**。

⇒ 因此本轮**全部工作在独立 worktree `openpocket-wt-audit38` 完成**，
主工作区一个字节都没碰（含那批未提交改动：只读取用于核对，
改动以 patch 形式搬进本 worktree 后重新验证）。

**且没有删任何分支/worktree**：三个已并入 main 的分支仍被 checkout 在
worktree 里，其中 `wt-i18n2` 有 4 个未跟踪文件
（`pocketd-qpfix.exe` 等构建产物），`wt-a32` 曾被写入。
删 worktree 会连带删掉这些。

⇒ 合并 origin/main 时两次撞上并发推送：基线 `c02dfb11` 在我工作期间
被推成 `36c4a4d1` → `2a3bc73a` → `a15fd839`。
两次都先 `git ls-remote` 确认（不用 `rev-list` 的 behind=0，那只代表
「上次 fetch 时没落后」），核对新增提交不碰我改的文件后干净合并。

---

## 7. 遗留风险

1. **`snippetFromMIMEParts` 的闸门只挡「声明了非文本类型」的 part。**
   一封 `Content-Type: text/plain` 但正文是 base64 附件的畸形邮件仍会漏。
   概率极低（本轮只按声明判，与函数自己宣称的口径一致），
   但**这是已知的口径边界，不是「已排除」**。
2. **`824a0391` 那条链上的真库结论本轮未复核。**
   `delete-unsanitized-snippets.sql` 之类的脚本已进 main，
   本轮没有对着真库跑过任何一条。
3. **本机 5432 的 Postgres 是共享资产。**
   任何 PG 集成测试都不应在未确认 DSN 归属时指过去。
4. **三个分支与四个 worktree 仍原样保留。**
   清理要等并发会话收工后单独做，删前先
   `git rev-list --count origin/main..<branch>` 与逐个 worktree 的
   `git status`（含**未跟踪**项）双向确认。
5. **守卫受 go test 缓存影响**：本地必须 `-count=1`（CI 已带）。
6. **`go test -race` 本机跑不了**（需 cgo，本机 CGO 默认关）。
   CI 的 `go test -race ./... -count=1` 是否绿，本轮**未验证**。

---

## 8. 下一轮提示词

> 继续 openpocket 审计（round40），基线 `audit/2026-10-04-main-health`
> 已并入 main 后的 `a15fd839`。
>
> 1. 先 `git fetch` + `git ls-remote origin main` 确认基线，
>    再看 `git status` 有没有并发会话的未提交改动
>    （主工作区那批 22:51 的未提交改动本轮**没有**代为提交，
>    它已被本轮以 patch 形式重做并提交，是否还需要清理残留请判断）。
> 2. **优先做遗留风险 1**：`partDeclaresTextContent` 只看声明的
>    Content-Type。构造「声明 text/plain 但正文是 base64 附件」的畸形
>    邮件，看 `DeriveSnippet` 会不会把附件字节当摘要；
>    要修就补判据 + 负控，不要只加 `looksLikeMIME` 之类的巧合 token。
> 3. **遗留风险 6**：`go test -race` 在本机跑不了（cgo）。
>    查 `CGO_ENABLED=1` 下有没有可用的 gcc；
>    没有就明说「race 未验证」，别用非 race 的绿灯冒充。
> 4. round38 §4 记的 `isolatedSchemaRe` 假绿**已在本轮被 51f9108c 修掉**
>    （收紧成 `"\w+_test_` 且与 `schemaPurposeRe` 取合取，
>    负控矩阵 541 正控 / 27 + 21 负控全绿）。
>    但它**只在本轮跑过一次全量**；若要动这条正则，
>    先按 round38 的要求把每个已隔离文件跑一遍确认收紧后仍绿。
> 5. 前端门禁与真机 UI 一直没人跑（无 node_modules / 抢设备）。
>    若能起独立 worktree 的 `npm ci`，先把
>    `frontend/` 的 lint + 单测跑一遍补上这块空白。
