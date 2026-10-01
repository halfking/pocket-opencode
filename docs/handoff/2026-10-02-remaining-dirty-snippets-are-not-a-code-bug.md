# 剩余脏摘要：不是代码缺陷，是历史数据 + 原文未落盘

日期：2026-10-02
分支：`feat/mail-config-deploy`
状态：**结论已定，无代码改动**

---

## 结论一句话

收件箱里剩下的 25 封脏摘要，**当前代码能正确处理**；它们是摘要修复
（`064ce29`）之前写入的历史脏数据，且**原文没有落盘**，所以无法就地重算。
让它们变干净的唯一办法是重置账户同步进度、重新从 IMAP 抓一次原文。

---

## 证据链

### 1. 现状复核

```
emails total=120  rawMIME=23  empty=0  clean=97
```

账户分布（`dirty / 原文已缓存`）：

| 账户 | 脏 | body_path 非空 |
|---|---|---|
| `acct-…-2` | 10 | 0 |
| `acct-…-3` | 12 | 0 |
| `acct-…-5` | 3 | 0 |

**`body_path` 全空、`body_purged` 全 false** —— 原文既没落盘也没被清理，
就是从来没存过。没有原文，就没法拿 `DeriveSnippet` 重算。

### 2. 一个我走错的方向（如实记录）

先用「把旧摘要本身喂给 `DeriveSnippet`」做代理测试，得到
`currentCodeCleans=10 / stillDirty=15`，据此以为是解码器缺陷，
还改了 `decodeWholeQuotedPrintable` 成两段式（先接软换行再解转义）。

**这个诊断方法本身是错的。** 逐字节打印真库摘要才发现：

```
em-10409 len=503  softBreaks(=\n)=0  CR=0  LF=0
```

存进库的是**已经被 `normalizeWhitespace` 压成一行**的摘要，**折行信息
早就没了**。真库这批数据根本没有软换行可接，两段式改动对它是**零改善**——
而我基于错误诊断动了生产解码器。已 `git checkout -- snippet.go` 撤销。

> 教训：这个坑和「拿压缩后的值当输入去测解压逻辑」是同一类。
> 代理输入必须先验证它**仍然保留**了被测机制所依赖的信息。

### 3. 决定性验证（用真实 rawMIME 形态）

按 IMAP `BODY[TEXT]` 的真实形态重建夹具（首行 boundary + 头 + 空行 +
在多字节字符中间折行的 QP 正文）：

```
looksLikeMIMEStructure = true
candidate 0: parse err = malformed header line "------=_Part_…"   <- 原样不行
candidate 1: parse err = <nil>                                     <- 剥掉首行就对了
DeriveSnippet = "以下= 是您的验证码。请注意，该验证码将在 10 分钟内失效。"
readable? true   hasCJK? true
```

**当前代码路径完全正确**：`dropLeadingBoundaryLine` 剥掉首行 →
`ParseMIMEMessage` 成功 → QP 折行由标准 reader 正确还原 → 摘要干净可读。

### 4. 顺带查清的两个既有小瑕疵（非本轮引入，不影响可用性）

- `&=` 会原样残留在解码结果里（如 `&zwnj; &zwnj;&= nbsp;`）。
  撤销我的改动后**行为完全相同**，确认是既有行为，不是回归。
- `&zwnj;` 这类 HTML 实体在 QP 分支里没被 `htmlToText` 解掉。
  影响很小（摘要里多个不可见字符），真库样本中仅 2 封。
  彻底修需要把 QP 解码结果也过一遍实体替换，属独立优化，未做。

---

## 怎么让这 25 封变干净

自愈路径已经就位（`store.go:515`）：

```sql
ON CONFLICT (id) DO UPDATE SET
   snippet = CASE WHEN EXCLUDED.snippet <> '' THEN EXCLUDED.snippet ELSE emails.snippet END
```

所以只要让 sync 重新抓到原文，摘要就会被刷新。做法是把相关账户的
`last_synced_uid` 归零，再跑一次同步：

```go
// backend/internal/email/diag_snippet_leak_test.go 已内置这个开关
POCKET_DIAG_RESET_ACCOUNT=<account_id 或 email>    // 或 ALL
```

**我没有执行**，因为它会真的登录这 5 个真实邮箱重新拉取 120 封邮件。
这属于对生产邮箱的动作，等你确认。

预期：涉及的 3 个账户（`…-2` / `…-3` / `…-5`）重同步后，脏摘要降到 0；
其余账户不受影响（它们本来就干净）。

---

## 复现命令

```bash
cd C:\workspace\openpocket-wt-maildeploy\backend
$env:POCKET_REAL_MAIL_DSN='postgresql://postgres@127.0.0.1:5432/postgres?sslmode=disable'
$env:POCKET_DIAG_SCHEMA='opencode_pocket'
go test ./internal/email/ -run TestDiagnoseSnippetLeak -v -count=1
```
