# round13：把 round11 失实的那两处真正做完，并重写那条护栏

日期：2026-10-02
分支：main（无新分支）
上一轮：round12（并发会话）已记录 `0728aa11` 提交信息与实际树不符
本轮承接：round12 §1 认领的债——「round11 声称删除阿里云白名单，实际两样都没做」

---

## 0. 本轮最重要的一条

**`0728aa11` 描述的两处改动，本轮才真正落到树上。** round12 已经把根因写清楚了
（我在并发会话编辑这两个文件时跑了 `git checkout HEAD --`），但只订正了记录，
没有补做代码。本轮补做。

同时**重写了那条护栏**——不是修好它，是换掉它。原因见 §3：原来那条断言
在「白名单存在」时是绿的，而它守的恰恰是「白名单不该存在」。

---

## 1. 人工拍板的最终口径

用户在 2026-10-02 当天就这个冲突拍板了**两次**，中间还自己推翻过一次：

| 时刻 | 口径 | 落地情况 |
|---|---|---|
| 17:09 | 移除白名单（取规则侧：退订特征命中即 100 分判垃圾） | 提交了，但**被 `git checkout HEAD --` 抹掉** |
| 17:24 | （另一议题）`ResponseHeaderTimeout` 30s → 60s | 由并发会话在 `7a77ae49` 落地 |
| 18:28 | 再次确认：**移除白名单** | 本轮补做 |

并发会话在 `7a77ae49` 里写入过一条「保留本条：误伤代价高于漏判」的注释，
**这不是用户所述的理由**，用户从未这样表述过。本轮已删除该表述并订正。

### 冲突的两侧

- **规则侧**：`spamSubjectPatterns` 的退订段做
  `strings.Contains(subject, p) || strings.Contains(snippet, p)`，
  主题**和摘要都查**，命中即 Score 100。阿里云产品月刊的摘要含
  「点击此处退订。」——按规则它就是垃圾。
- **样本侧**：`aliyun-product-monthly` 曾期望非垃圾（handoff §7e 记为 near-miss）。

### 为什么不留白名单

白名单在 `LooksLikeSpam` 里是**评分之前**就 `return SpamVerdict{}` 的。
所以那封信拿到的是 `score=0, why=""`——

> **「看起来判成非垃圾」，实际是「压根没参与评分」。**

退订规则对它一次都没执行过。而样本断言 `spam:false` 在这种情况下**照样是绿的**。
用豁免同时满足两侧的测试期望，等于**判据绿而实现空转**，比冲突本身更糟：
冲突至少是可见的，豁免把它变成沉默。

### 明确接受的代价

阿里云产品月刊（真实服务通知 + 正文带退订链接）**会被判垃圾，用户收不到**。
真实服务通知这一类兜不住。误伤由 `invoiceCandidate` / `important` 短路
和出票/账单类域名白名单兜住。这是取舍，不是缺陷。

也不要改成放行 `aliyun.com` / `aliuncs.com` 整域：实测那样会把该判的一起豁免，
垃圾判定形同虚设。

---

## 2. 改动文件

### `backend/internal/email/spam.go`

移除 `spamDomainWhitelist` 中的 `"monitor.aliyun.com"`（原第 93 行），
替换为一段解释拍板经过的注释：当天两次推翻、最终口径、明确代价、可判定收回点。

### `backend/internal/email/spam_samples_test.go`

1. `realSpamSamples` 的 `aliyun-product-monthly`：`spam: false` → `spam: true, minScore: 100`。
   注释记录两次因果订正：
   - 更早的注释写「退订规则只看主题，摘要里的『点击此处退订』不计分」——**那是错的**；
   - 改口成「score=0 是白名单豁免的**正确结果**」——**那也不对**（理由见 §1）。
   两版都留着，因为它们各自会把人引去改一个没坏的规则。
2. `senderIsExempt` 上方的注释里「合并后白名单多了 monitor.aliyun.com」
   是一句**已失效的现状描述**，已加注说明那是中间口径。
3. `TestLooksLikeSpam_AliyunExemptionIsTheOnlyReasonForZero` → 重写为
   `TestLooksLikeSpam_AliyunHasNoDomainBackdoor`（见 §3）。

---

## 3. 为什么必须重写那条护栏（这是本轮的技术要点）

旧测试 `TestLooksLikeSpam_AliyunExemptionIsTheOnlyReasonForZero` 断言的是：

> 「豁免地址上的同一封信必须是**严格零值**（不垃圾、不带 Why）」

**它在白名单存在时是绿的。** 它守的不是「该域没有后门」，而是「该域有后门且
短路是正确行为」——恰好是拍板要拿掉的东西。留着它，下次有人把白名单加回来，
它会帮着背书。

更根本的问题：**它只比 `Spam` 字段比不出「压根没评分」**。白名单短路返回的零值
在 `Spam` 这一维上和「评过分判定为非垃圾」无法区分——必须连 `Score` 和 `Why`
一起比。

新测试钉住的不变式：

> 对同一个 `(subject, snippet)`，**任何域名**都必须交出**逐字相同**的
> `(Spam, Score, Why)` 三元组。

对照组取两个，覆盖两种可能的后门：
- `noreply@aliyun.com`——同家但不在白名单（判据是 `strings.Contains(domain, 条目)`）；
- `newsletter@example.com`——完全无关，排除「阿里云整域放行」这种更粗的后门。

### 负控实测（2026-10-02）

把 `"monitor.aliyun.com"` 加回 `spamDomainWhitelist`：

```
--- FAIL: TestLooksLikeSpam_OnRealMailboxSamples/aliyun-product-monthly
    spam_samples_test.go:157: Spam=false, want true (score=0 why="")
--- FAIL: TestLooksLikeSpam_AliyunHasNoDomainBackdoor
    spam_samples_test.go:222: monitor.aliyun.com 必须与普通地址 noreply@aliyun.com
    判定逐字相同（2026-10-02 拍板：不给该域开后门）；实际 spam=false score=0
    why=""，对照 spam=true score=100 why="退订特征:退订"。若 got 是
    score=0/why=""，说明它仍在域名白名单里被提前 return 短路了……
```

两条判据同时转红，错误信息准确指向短路路径。移除后转绿。**判据有承重能力。**

注意 `why="退订特征:退订"` 与 `score=100` 的对照：正是这组值证明了退订规则
**确实查摘要且命中即 100**，排除了「规则没坏」这个解释。

---

## 4. 验证结果

| 命令 | 结果 |
|---|---|
| `cd backend; go vet ./...` | **exit 0** |
| `cd backend; go test ./... -count=1` | **exit 0** — ok **53** / FAIL **0** / 无测试 **18** |
| 负控（白名单加回） | **exit 1** — 2 条判据转红，错误信息符合预期 |
| `cd frontend; npm.cmd run gates` | **GATES_EXIT=0** — 覆盖 170/170、zh-TW 245 key 全覆盖、4 条棘轮（未翻译欠账 / 死能力 / 原始错误上屏 / 孤儿测试文件）全通过 |

邮件包定向复跑（`Aliyun|OnRealMailboxSamples|ExposesScoreBelowThreshold|Newsletter`）：
**全绿**，含 `TestLooksLikeSpam_AliyunHasNoDomainBackdoor` 与
`aliyun-product-monthly` 样本。

**`go test -race` 本轮仍未跑**（承接 round12 §2.1：需要 w64devkit 的 `CC`，
且 `$env:CC` 只在单次 bash 调用内有效）。不要因为看到「全绿」就以为含 race。

---

## 5. 遗留风险

1. **阿里云口径当天被推翻两次**，仓库注释里曾残留多个版本的「人工拍板」表述，
   容易再次被误改。**每次改动请用 Grep 确认实际条目状态，不要依赖注释。**
   判据是 `TestLooksLikeSpam_AliyunHasNoDomainBackdoor` + 样本期望。
2. **`pgSafeWithoutIsolation` 豁免是真盲区**（见 round11）：列入后 PG 隔离护栏
   **完全跳过**该文件，不是「检查后放行」。负控实测注入 `DELETE FROM email_accounts`
   后护栏仍绿。目前 `diag_credential_health_test.go` 与
   `diag_real_invoice_extract_test.go` 两条，已在注释中明写，无机器维持。
3. **并发会话持续在同仓库活动**。本轮开工时 HEAD 已被推进到 `675b171e`，
   我上一轮的两处编辑一度被清空过。提交前必须重新核实 `git status` 与文件实际内容，
   `git diff --cached --stat` 逐条核对。
4. **`remotes/https/main`（`3da3c8fe`）是陈旧引用**：`https` 这个 remote 已不在
   `git remote -v` 中，`git fetch https` 报 fatal。它不是活跃分支，不要去合它。

---

## 6. 下一轮提示词

```
接手 openpocket（仓库 C:\workspace\openpocket，Go module 在 backend/），
继续 24 小时修正任务的审计与完善。上一轮是 round13
（docs/handoff/2026-10-02-round13-aliyun-backdoor-removed-and-verdict-guard-rewritten.md），
它把 round11 失实的那两处（移除 monitor.aliyun.com 域名白名单 + 样本改
spam:true/minScore:100）真正落到树上，并重写了那条护栏
TestLooksLikeSpam_AliyunExemptionIsTheOnlyReasonForZero →
TestLooksLikeSpam_AliyunHasNoDomainBackdoor（原断言守的恰恰是要拿掉的东西）。

本轮请按序做：

1. 【并发前置，务必先做】这个仓库有并发会话长期共用同一 main 工作区。
   动手前先跑：
     git worktree list
     git log --oneline -5
     git status --porcelain
   再看目标文件的 mtime。**禁止**对别人正在编辑的文件跑 `git checkout HEAD --`
   （round11 的失实提交 0728aa11 就是这么来的，已记入 round12 §1）。
   自己的改动放独立 worktree，或先确认文件不在别人的在途集合里。

2. 拉取并核实：git fetch origin 后确认 origin/main 没有你没见过的提交。
   注意 `remotes/https/main`（3da3c8fe）是陈旧引用，`https` remote 已不存在
   （git fetch https 报 fatal），不要去合它。

3. 机械普查尚未闭合的缺陷类别（24h 内数百个提交无法逐一 review）：
   - 不实日期：注意措辞变体。「实测 / 审计 / 审计记录 / 审计实测」都查——
     之前只匹配一种关键词，把剩下的报成了「已清干净」。
   - 空 catch / 吞错（if err != nil 后面什么都不做、只 log 不返回）。
   - 恒假判据：正则没匹配到任何东西的测试不是测试；
     sfnt.GlyphIndex(buf, r) 这类 API 对「缺失」返回 (0, nil)，
     只判 err == nil 会**恒真**——必须连零值一起判并加必然缺失的对照样本。
   - RE2 vs JS 引擎差异：Go 正则无回溯，\d+\.\d{2}-...\.pdf$ 在真实 20 位
     发票号上必然失败，而同一正则在 JS 回溯引擎下能匹配。
     判据必须在最终运行的引擎里验。

4. 【重点遗留】pgSafeWithoutIsolation 豁免是真盲区，不是「检查后放行」：
   负控实测注入 pool.Exec(ctx, "DELETE FROM email_accounts") 后护栏**仍绿**。
   目前 diag_credential_health_test.go 与 diag_real_invoice_extract_test.go
   两条只靠注释维持，无机器保障。考虑给护栏加一条自检：豁免文件里出现
   Exec/DDL 写语句时失败。

5. go test -race 至今没在 HEAD 上跑成过（round12 §2.1）。要跑必须：
     $env:CC='C:\tools\w64devkit\w64devkit\bin\gcc.exe'; $env:CGO_ENABLED='1'
     go test -p 2 -race -count=1 ./...
   $env:CC 只在**单次 bash 调用内**有效，漏设会退化成「-race 跑不了」的假象。

6. 每次改护栏/判据都做负控：注入真实世界会写错的那个版本，确认能转红。
   注意挑**有区分力**的变异——「看起来更严格」的变异常常是语义 no-op，
   转不红不代表断言冗余，先逐格核对两式是否真的不同。

7. 收尾：更新对应 handoff，提交（git diff --cached --stat 逐条核对文件列表与
   提交信息相符），推送时用
     $env:GIT_SSH_COMMAND='ssh -o ServerAliveInterval=15 -o ServerAliveCountMax=20 -o TCPKeepAlive=yes'
   （代理端口会漂，验证结论不要只靠一条命令的退出码）。
```
