# round12：遗留四项的结论 + 一条失实提交记录的订正

日期：2026-10-02
分支：main（无新分支）
上一轮：round11（`0728aa11`）

本轮承接的是上一轮自己列的四条遗留，外加一件上一轮没意识到的事：
**round11 的提交信息描述了它没有做的改动**（见 §1）。

---

## 0. 先说本轮最重要的一条

## 1. `0728aa11` 的提交信息与它实际提交的代码不符

round11 的提交信息写着：

> 本次删除该白名单条目，并把阿里云样本期望改为 spam:true / minScore:100。

**两样都没做。** HEAD 实际状态：

- `spam.go:80` 仍有 `"monitor.aliyun.com"`；
- `spam_samples_test.go` 仍是 `spam: false`、无 `minScore`。

### 怎么造成的

round11 那个并发会话正在工作区里改这两个文件；我在它提交**之前**执行了

```
git checkout HEAD -- backend/internal/email/spam.go backend/internal/email/spam_samples_test.go
```

把它的在途改动还原了。它随后照原计划提交并推送，于是一条**描述 A 的信息**配着
**内容 B 的树**进了 main。已推送的提交信息不能改（不该为这个 force-push 共享的
main），所以本轮用新提交 + 本文档订正。

### 教训（比结论本身更值得记）

**在有并发会话的共享工作区里，不要对别人正在编辑的文件跑 `git checkout HEAD --`。**
它不只丢工作，还会让对方的提交信息与树脱钩——而提交信息是别人（和未来的我）
判断「这事到底做没做」的主要依据。

替代做法：把自己的改动放到独立 worktree，或者先确认该文件不在别人的在途集合里。

---

## 2. 四条遗留的逐条结论

### 2.1 `go test ./... -count=1 -race` —— 上一轮「未跑成」的真实原因

**不是缺 gcc。** 真实原因是在**仓库根**跑的，而 Go module 在 `backend/`：

```
$ go test ./...          # 在 C:\workspace\openpocket
pattern ./...: directory prefix . does not contain main module or its selected dependencies
```

正确入口是 `cd backend && go test ./... -count=1`。修正后全量跑通，
`go build ./...` 与 `go vet ./...` 均干净。

`-race` 是另一回事：Windows 上必须 cgo + C 编译器，本机 PATH 无 gcc/clang/tcc，
winget/scoop 也未装。经用户授权已装 mingw-w64，见 §5。

### 2.2 ResponseHeaderTimeout 30s → 60s：**已拍板并落地**

`211e590a` 确实只改了注释没改数值——parked 分支的提案从未合入。本轮落地：

| 文件 | 改动 |
|---|---|
| `llmgateway/client.go` | `30 * time.Second` → `60 * time.Second`，并把「为什么是 60s」写进函数头 |
| `llmgateway/client_test.go` | 断言 30s → 60s（该文件与实现由并发会话同步改到位，结论一致） |
| `server/server_assistant.go` | 注释从「预算死区 min(60s,30s)=30s」改为「死预算已消除」，并记下代价 |

理由：非流式调用要等上游**真正开始回包**才发响应头；推理模型（网关路由到
glm-5.2）先把 token 花在 reasoning_content 上（实测一句 63 字总结用掉 985 个
reasoning token），30s 会把 `handleNoteSummarize` 的 60s 预算压成「前 30s 可用」。

**代价（明确接受）**：上游真挂死时失败时间从 30s 变最多 60s；整体 Timeout 仍
90s，不会无界挂死。

### 2.3 q3 的 `invoiceLinkScoreThreshold=20` / `strongInvoiceHints`：**用真实语料复核完毕**

上一轮记的是「无真实样本」。本轮用 `<dataDir>/email-bodies/*.bin` 里 **41 封真实
newsletter 正文**（aes-gcm 加密，用磁盘 master key 解开）跑了一遍只读探针：

```
=== 语料：41 个缓存文件，解密成功 41，抽出链接 119 条 ===
=== bodyHasInvoiceLink 判为 true 的文件：2 ===
--- 命中路径分布 ---
  SCORE>=20(弱特征叠加)       1
  BOTH(强特征+分数)           1
--- 逐条命中 ---
  https://17dz.com/einvoice-web-pc/einvoice-details/check-=          score=20 via=SCORE>=20
  https://servu-invoice.oss-cn-hangzhou.aliyuncs.com/in=             score=20 via=BOTH
=== 只靠 score>=20 过线（无专属性词支撑）的链接数：1 ===
```

结论：

- **误报 0**：119 条真实营销/newsletter 链接里，没有一条被误判成发票链接。
  这正是「有链接就算」会翻车的那一类语料。
- **真命中 2 条**，且都是真发票链接（`einvoice-web-pc`、`servu-invoice.oss`）。
- 「只靠弱特征叠加过线」这条最没样本支撑的路径，41 封里只出现 1 次，且判对了。

`invoiceLinkHints` 含 `inv` / `fp` 这类**子串**，理论上「两个弱特征叠加」容易
误判（`inviter` 含 `inv`）。真实语料上没发生，但这是**样本量 41 的结论，不是
证明**——`hasStrongInvoiceHint` 的整词判定（`containsWholeWord`）才是真正兜住
这一类的那道闸。

> 语料局限：这 41 封是 newsletter，**不是**真实发票邮件库。真实库
> `public.email_accounts` / `public.emails` 现为 0 行（见
> `openpocket-local-pg-schema-missing`），所以「真实发票邮件的正文链接命中率」
> **仍然没有证据**，本轮不声称已覆盖。
>
> 探针本体在 `backend/internal/email/diag_q3_reallinks_test.go`
> （`-tags=q3probe`，只读，不连库不连邮箱），未提交——它是一次性诊断，
> 提交价值低于其过期风险。

### 2.4 提醒窗口 2→90 天与扫描上限的耦合：**已核实，耦合真实但卡在边界**

`importantReminderLookbackDays` 2→90（`37c53e6d`）已落地，
`importantReminderScanLimit = 2000`（`pipeline.go:863`）。

耦合是真的：`Store.ListEmailsSince` 是

```go
if limit <= 0 || limit > 2000 { limit = 500 }
```

**`> 2000` 才重置**，而常量正好取 2000 —— 差一个数就会被静默打回 500，
90 天窗口形同虚设。`important_reminder_lookback_test.go` 钉住了这条
（`> 500` 且 `<= 2000`，并断言调用点用的是常量而非字面量），实测通过。

保留的已知不足：若 90 天内邮件数超过 2000，`ORDER BY date DESC LIMIT 2000`
仍会截断，「重要但很老」的那批依旧扫不到。`CountHighImportanceOutside`
（`store_pipeline.go:119`）就是为暴露这一点存在的。

---

## 3. 阿里云月刊：口径订正 + 一条写错了三年的因果

**2026-10-02 拍板：保留 `monitor.aliyun.com` 白名单**，阿里云产品月刊判**非垃圾**。
理由：MOVE 进垃圾箱对真实邮箱不可逆，误杀正式服务通知的代价高于漏判一份月刊。

### 3.1 订正 `spam_samples_test.go` 里那段错误的因果说明

原注释写的是：

> 摘要里的「点击此处退订」是**正文**，退订规则只看主题，不计分。

**这是错的。** `spam.go:245` 明确是

```go
if strings.Contains(subject, p) || strings.Contains(snippet, p) {
    return SpamVerdict{Spam: true, Score: 100, Why: "退订特征:" + p}
}
```

主题**和**摘要都查，命中即 100。那封信本该拿满分判垃圾。

`score=0` 的真正原因是**顺序**：`spamDomainWhitelist` 的判定在
`LooksLikeSpam` 里位于评分**之前**（`spam.go:156-163` 直接 `return SpamVerdict{}`，
`score` 变量到 L166 才声明），所以对这一个域，退订规则**一次都没执行过**。

原注释会把人引去改一个没坏的规则，而真正该拍板的是「这个域要不要豁免」。

### 3.2 把这段因果变成可执行断言

新增 `TestLooksLikeSpam_AliyunExemptionIsTheOnlyReasonForZero`：同一封信，
换到未被豁免的 `noreply@aliyun.com` 必须拿满 100 分判垃圾；换回豁免地址必须是
严格零值。注释不会自己保持正确，断言会。

**负控实测**：把退订段改回 `if strings.Contains(subject, p)`（只查主题），
该用例如期转红；还原后转绿。判据有承重能力，不是恒真断言。

---

## 4. 分支清理

12 个远端分支逐个跑 `git merge-base --is-ancestor <branch> origin/main`，
**全部 Ahead=0**（已并入 main，零未合并工作），经用户批准后 `git push origin --delete`
删除完毕。删除脚本对每个分支**先验证再删**，未合并的会跳过而不是硬删。

本地 `audit/2026-10-02-pending-human-decisions`（parked，2 个提交）判定为**冗余**：

- `tokens.css` 补 `--text-2xs/--text-smd`：main 的 `7698b6f4` 已做，且版本更全
  （补了 10/11/12/13/14/15/16/18 整档并替换 348 处写死像素）；
- `server_assistant.go`：只是一份更早的注释版本，已被本轮注释取代；
- `client.go/client_test.go` 的 60s 提案：本轮直接落在 main 上。

---

## 5. `-race` 仍**未跑成**，以及未验证项

### 5.1 `-race` 的阻塞点（本轮唯一没做到的遗留）

`go test ./... -count=1 -race` **本轮没有跑成**。Windows 上 `-race` 必须 cgo +
C 编译器，本机 PATH 无 gcc/clang/tcc。经用户授权尝试安装，**两个包都在
非交互会话里停滞**：

| 包 | 现象 |
|---|---|
| `BrechtSanders.WinLibs.POSIX.UCRT` | 45 分钟零输出，CPU 仅从 246s 涨到 267s，未安装 |
| `MartinStorsjo.LLVM-MinGW.UCRT` | 25 分钟零输出，CPU 仅从 6.9s 涨到 12.1s，未安装 |

**不是网络问题**：`HTTPS_PROXY=http://127.0.0.1:7897` 生效，
`HEAD https://cdn.winget.microsoft.com/cache/source.msix` 返回 200。
两次停滞的签名一致（近零 CPU 增长 + 零输出 + 目标目录始终不出现），
指向 winget 自身的安装环节在非交互会话里挂住。

**结论：并发/竞态风险本轮无任何证据覆盖。** 复现与绕过：
手动装一个 mingw-w64（或用 `CC=zig cc` 之类替代 C 编译器）后，
在 `backend/` 下跑 `go test ./... -count=1 -race`。
注意入口目录：`./...` 必须在 `backend/` 下跑，否则报
`directory prefix . does not contain main module`（见 §2.1）。

### 5.2 其余未验证项

- **真实发票邮件的正文链接命中率仍无证据**（§2.3 语料局限）。
- **源码里还有约 20 个文件写着 `2026-10-03`**（今天 10-02），同一「不实日期」缺陷类。
  `b3cdd819` 修了 11 处「实测（2026-10-04）」，`0728aa11` 修了 6 处换措辞的
  「审计/审计记录」，**剩余约 20 个文件未修**。本轮**故意没做**这批清扫：
  并发会话正在其中若干文件上工作，批量改会再次制造 §1 那类冲突。
  复现命令：`git grep -l "2026-10-0[3-9]" -- backend frontend`

---

## 6. 本轮提交与推送

`7a77ae49`，已推送 `0728aa11..7a77ae49 main -> main`。
提交时只 `git add` 本轮自己的 5 个文件 + 本文档；并发会话在途的
`diag_real_invoice_extract_test.go` / `pg_test_isolation_guard_test.go`
**未被本轮提交**。

推送后并发会话又提交了 `16e10087`（发票误判影响面量化），本轮**没有**推送它——
它未经本轮验证，且属于另一个会话的工作。

