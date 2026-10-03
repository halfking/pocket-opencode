# 汇总台账 CSV 的合计金额落在了「文件名」列

日期：2026-10-02
分支：`feat/mail-config-deploy`

---

## 缺陷

需求原文：「需要整理一个列表，记录必要信息并**汇总金额**」。

飞书没配凭证时走本地兜底（CSV + Markdown），这条路径正在生产使用中。
实测产物 `invoices-summary-20261002-053833.csv`：

```
费用类型,对方单位,金额,币种,发票号,日期,状态,文件名,来源邮件
其他,杭州创客家投资管理有限公司,3500.00,CNY,26332000008261110741,2026-09-24,downloaded,其他-...pdf,您收到...
合计,,,,,,,3500.00,
```

用 CSV 解析器（`Import-Csv`，不数逗号）读合计行：

```
col1=费用类型 col2=对方单位 col3=金额 col4=币种 col5=发票号
col6=日期 col7=状态 col8=文件名 col9=来源邮件
合计行 → 费用类型=合计、文件名=3500.00
```

**合计金额落在第 8 列「文件名」，「金额」列是空的。** 在 Excel / WPS 里
打开就是：金额列没东西，文件名列写着 3500.00，对不上账。

根因是手写的格式串：

```go
csv.WriteString(fmt.Sprintf("合计,,,,,,,%.2f,\n", total))
//                      7 个逗号 ⇒ 金额落到第 8 列
```

7 个逗号把值推到了「文件名」。要落在「金额」需要 2 个。

---

## 修法

不改成「把逗号数对」（改对了但下次调整列顺序还会再错），而是让列位置变成**结构性的**：

- 抽出 `invoiceSummaryHeader []string` 作为列定义，表头由它 join 生成；
- 抽出 `invoiceSummaryTotalRow(total)`，按表头**定位**「金额」列并写值，
  长度恒等于表头长度；
- 合计行只写进 CSV。

**合计行不能混进 `rows`**：Markdown 表格按 7 列渲染每一行，塞进去会多出
一张空壳的「合计 | | | 3500.00 | ...」行，且金额会落在状态列位置。
我第一版就犯了这个错，被自己写的用例当场抓住（见下 NC2）。

---

## 顺带发现：既有用例在为 bug 背书

`invoice_harvest_test.go:210` 原本断言的是**字面量**：

```go
if !strings.Contains(csv, "合计,,,,,,,100.00,") {
```

也就是说，它把「金额落在第 8 列」这个错误格式**固化成了期望值**。
测试全绿，但绿得没有意义——它锁的是 bug 的字面形状，不是需求要的语义。

这是本项目第三次遇到「断言锁住了错误形状」：
`CachedRawStillCountsAttempt`（名不副实）、`account-push-field-symmetry`
（`imapHost: undefined` 骗过字段名判据）、以及这次的字面量格式串。

**教训**：断言格式的**字面量**，等于给实现细节背书。断言**语义**
（用解析器读、按列名取值），实现怎么改都不会误判。

已改为用 `encoding/csv` 解析、按表头找「金额」列、断言该格是 `100.00`。

---

## 测试与负控

新增 `invoice_summary_total_column_test.go` 2 例：

1. `TestInvoiceSummaryCSV_TotalLandsInAmountColumn`
   合计落在「金额」列；「文件名」列必须为空（回归钉）。
   夹具里放一条 `failed` 且金额 999 的发票，验证它**不进合计但仍出现在列表**。
2. `TestInvoiceSummaryCSV_TotalRowNotLeakedIntoMarkdown`
   合计行不得混进 Markdown 表格。

两路负控均实测转红后还原：

| 注入 | 结果 |
|---|---|
| `invoiceSummaryTotalRow` 定位到「文件名」列 | 金额用例红：`「金额」列不是数字：""` |
| 把合计行 `append` 进 `rows` | MD 用例红：抓出空壳行 `\| 合计 \|  \|   \|  \|  \|  \| 120.00 \|` |

注入均确认只落在预期那一处。`go build` / `go vet` / `internal/email`
全量绿（含 `TestWriteInvoiceSummaryDocs` 改造后）。

---

## 附带记录（未修，需产品决策）

`WriteInvoiceSummaryDocs` 的注释说「文件总在每轮流水线末尾**重建**」，
但实现是每次生成**带时间戳的新文件**、从不清除。实测：

```
C:\workspace\openpocket\data\email-invoices\exports\ws_user-admin\
  136 个文件 / 1.6 MB（59 CSV + 59 MD + 18 个 A4 PDF）
  最早 09-30 11:16，最新 10-02 05:38
```

全仓检索 `exports` 的 prune/cleanup/retain 逻辑：**零命中**。

按每日定时跑，2 个文件/天 ≈ 730 对/年。单文件 ~1KB，体积不是问题，
但有两个实际困扰：

1. 「哪一份是当前台账」要靠时间戳猜；
2. 与注释描述的「重建」不符，误导运维。

修法是保留策略（保留最近 N 份，或改成固定文件名 `invoices-summary.csv`）。
这会**删除用户数据目录里的文件**，未经确认不实施。
