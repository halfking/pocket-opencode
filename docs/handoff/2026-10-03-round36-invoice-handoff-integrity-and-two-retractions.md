# 2026-10-03 round36 —— 发票交付前完整性 + 两次自我推翻

> 本轮时间 19:36–23:50。目标：自动解析发票 → 整理 → 下载打印 → 汇总 → 交财务。
> 主链路在生产用真实数据验证过（台账 7 张 / 计入 6 张 / 合计 4038.01）。
> **本轮没有新增功能**，新增的是四个只读诊断 + 一次对**判据本身**的修正，
> 以及**两次把我自己推上天的结论撤回**。

## 一、四个新增只读诊断（均已推 `origin/main`）

| 提交 | 文件 | 作用 |
|---|---|---|
| `85f99ba3` | `internal/email/diag_debt_notice_candidates_test.go` | 平安/招商信用卡电子账单在 2026-10-04 08:00 那一轮会不会被建成幽灵发票 |
| `6ca0f618` | `internal/email/diag_invoice_handoff_integrity_test.go` | 交财务前核对「发票目录 vs 台账」：孤儿 / 字节重复 / 日期打架 |
| `e452a42b` | `internal/cmd/invoiceprobe` 注释订正 | 原文把一个**已修**缺陷写成现在时（见下） |
| `cd6593df` | `internal/email/diag_real_exports_test.go` | 修 A4 几何诊断的两条**假阳性**判据 |

运行方式（全部只读、门控、写尝试由数据库拒绝）：

```powershell
$env:POCKET_REAL_MAIL_DSN='postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable'
$env:POCKET_REAL_MAIL_SCHEMA='opencode_pocket'

# ① 交财务前完整性（23:02 基线：孤儿 5 / 重复组 3 / 日期打架 2）
$env:POCKET_DIAG_HANDOFF='1'
$env:POCKET_DIAG_INVOICE_DIR='C:\workspace\openpocket\data\email-invoices\ws_user-admin'
go test ./internal/email/ -run TestDiagInvoiceHandoffIntegrity -v

# ② A4 网格几何（判据已修，见第三节）
$env:POCKET_DIAG_REAL_EXPORTS='1'
$env:POCKET_DIAG_EXPORT_DIR='C:\workspace\openpocket\data\email-invoices\exports\ws_user-admin'
go test ./internal/email/ -run TestDiagRealExportGridGeometry -v

# ③ 幽灵行预测
$env:POCKET_DIAG_DEBT_SHAPE='1'
go test ./internal/email/ -run TestDiagDebtNoticeCandidatesWouldBeFiled -v

# ④ 明早台账预测（不发任何请求、连接强制只读）
$env:POCKET_PROBE_POSTGRES_DSN=$env:POCKET_REAL_MAIL_DSN
$env:POCKET_PROBE_PG_SCHEMA=$env:POCKET_REAL_MAIL_SCHEMA
go run ./cmd/invoiceprobe/
```

## 二、两次自我推翻（**别再沿着被推翻的方向查**）

### 推翻一：A4「打印后不能剪裁」——根因不成立

我曾用 `TestDiagRealExportGridGeometry` 跑出 5 项违规，据此上报「今天 16:11 生成的
A4 拼版超格 11%、剪裁会切开票面」。**两次实验把它推翻**：

1. **对照实验**：剔除全部半高票面（595.3×396.9），只留 7 张统一 A4 竖版再导出
   → 违规一模一样。**尺寸差异不是原因。**
2. **零变异输入**：用**同一个文件的 4 份字节相同副本**导出，落点实测
   `x∈{0, 297.64}`、`f∈{532.375, 111.43}`（差值 420.945 = 格高），
   **是标准 2×2**。此前报的「4 列 4 行」是假阳性。

真实情况：外层 `cm` 恒为 `a=d=0.55904`（grid=2）/ `0.3727`（grid=3），与 `1/grid`
的比值**都是 1.118** —— 这个数与「网格是否正确」无关。Form 的 `/Matrix` 全是单位
矩阵，票面自身内容还带旋转（通行费票面首层变换 `a=0.24 d=-0.24`）与页内白边
⇒ **页框溢出 ≠ 墨迹碰撞**。

### 推翻二：3 张腾讯云发票是「我们伪造的凭证」——方向错了

曾上报「台账里 3 张系统渲染摘要冒充凭证」。时间线证明**采集器是忠实的**：

| 事实 | 证据 |
|---|---|
| 台账行创建于 `2026-10-03 00:12:12`（2 张）/ `15:10:50`（1 张） | 查库 `created_at` |
| 文件写入于 `15:10:51`，**比那次流水线晚 1 秒** | 文件 mtime |
| `file_source = attachment` | 查库 |

PDF 里那句 `This is a system-generated e-invoice.` 是**上游（QQ Wallet / 腾讯云
开票）自己生成的摘要页**，仓库里没有任何代码生成该文案
（`gen_fixture_invoice_test.go` 生成的是 `IMAP fixture`，那是另一批——云服务
1280 元 ×2，round19 交接已定性为测试夹具，不是台账行）。

金额与票号逐项吻合（`Invoice No: 24317200000907012711` / `CNY 58.90` /
`2026-09-07`）。**真正的缺口是台账无法区分「官方票面」与「系统摘要」** ——
`status=downloaded` 的语义只是「拿到某个 PDF」。

## 三、修掉的判据缺陷（`cd6593df`）

`TestDiagRealExportGridGeometry` **对完全正确的产物报红**。一个会对正确产物报红的
判据比没有判据更坏：它会让人去「修」本来正常的代码（本轮我差点就这么做）。

| 判据 | 错在哪 | 改成 |
|---|---|---|
| `缩放必须正好是 1/grid` | 外层 `cm` 的 a/d 不是「页面→格子」比例 | 只记录不判定 |
| `distinctX/distinctY 按 0.1pt 去重` | 页宽实测 595.0 与 595.3 两种（A4 舍入差），居中后左边缘差 0.15pt，0.1pt 留不住 → 正确网格读成 4 列 | 聚类容差 0.5pt（0.18mm） |
| `distinctY ≤ grid` | 把「票面居中」误判成「不在网格上」：3500 那张只有 396.9 高，在 420.9 格子内上下各留 6pt | 行数改为**按格高归行号** |

修完：**20 个真实产物全绿**；负控（格高故意减半）转红
（`落点行数 4 超过 grid=2`），证明判据不是恒真。

⚠ **仍未验证**：墨迹是否真的越格。需要栅格化，而 pdfcpu v0.11 **没有渲染 API**。
本诊断不再替它冒充。**不要再把 `scale≠1/grid` 报成缺陷。**

## 四、明早（2026-10-04 08:00）要核对什么

只读探针 22:44 的预测（967 封语料、11 封会建档、**净新增 5 张**）：

| 邮件 | 金额 | 日期 |
|---|---|---|
| 电子发票下载 ×2 | 283.20 / 6071.00 CNY | 2026-09-15 |
| Apple 提供的收据 ×2 | 12.00 / 12.00 CNY | 2026-09-04 / 09-13 |
| Your receipt from X | **8.00 USD** | ⚠ 无（收信 2026-09-18） |

跨币种**不合并**（`ledger.go` 按币种分组）。八项核对已排 08:15 自动执行。

## 五、待用户拍板的三件事

1. **3 张系统摘要页（合计 513.40 CNY）算不算凭证？** 官方票面多半要去
   腾讯云/QQ 门户取（与工行那封同类：门户返回 HTML 落地页）。
2. **无开票日期时文件名用采集当天还是收信日期？** 磁盘已有前科：
   杭州创客家同一张票存在 `…-2026-09-24.pdf` 与 `…-2026-10-01.pdf` 两份
   **字节相同**的拷贝（`invoice_harvest.go:765` 的 `time.Now()` 兜底），
   而 `pipeline.go:596` 的幂等跳过让补正机会**只有一次**。
   改用收信日期要加一列标注来源，否则是把「收信日期」写进叫「开票日期」的字段。
3. **飞书凭据四项**（`APP_ID`/`APP_SECRET`/`INVOICE_CHAT_ID`/`INVOICE_FOLDER_TOKEN`）
   —— 用户已两次明确暂给不了。**推送腿至今一次没在真实环境跑过。**

## 六、本轮自证

| 项 | 结果 |
|---|---|
| `npm run gates`（24 项） | **全过 144.0s**（在 `.wt-build` 隔离 worktree，node_modules 用目录联接） |
| `go test ./internal/email/` | ok 154.347s / 157.248s |
| `go test ./internal/server/` | ok 58.039s |
| `check:gofmt` | 真债 0（917 个纯行尾伪债是仓库既有约定） |

**`5ba36291` 是修我自己跑红的 gofmt 门禁** —— 新增文件有真格式债（不是行尾噪声），
门禁直接点名后单文件 `gofmt -w` 修好。

## 七、本轮我自己判据/实验自坏六次（都记在提交与对话里）

1. `[IO.File]` 相对路径不跟随 PowerShell 的 `cd` → 第一次哈希跑在错目录。
2. PS 5.1 没有 `Encoding::Latin1`（返回 null）→ MediaBox 扫描静默返空。
3. `<1024` 阈值把 3 张 972 字节的**合法**腾讯票面一起排除 → 基线不基线。
4. `has_snip` 判据用 `left(snippet,0)`，恒为空串 → 「无摘要」是我瞎写的。
5. A4 根因一：对照实验推翻「尺寸不一致」。
6. A4 根因二：零变异输入推翻「落点超网格」，并牵出诊断自身的假阳性。

**通用教训**：判据自报的数字必须能与肉眼可数的事实对上；判据的红与绿**都**
可能是假的，而「测试通过」与「测试报错」在这两种情况下都不构成关于产物的任何证据。
