# 缺陷 13：Email 结构体字段「写了从不读回」——守卫分支在生产里从不执行

日期：2026-10-02
分支：`feat/mail-config-deploy`
提交：`a706bf55`
状态：**已修**

---

## 缺陷

`GetEmailByID` 与 `GetEmailByIDScoped` 的 SELECT 列表漏了 `message_id` 与
`body_purged` 两列，Scan 也没有目标。于是：

- `Email.MessageID` 在生产里**恒为空串**
- `Email.BodyPurged` 在生产里**恒为 false**

两列的写入侧都是好的（`fetcher.go:743` / `:880` 填 MessageID，
`store_inbox.go:113` 置 body_purged），只有「DB → 结构体」这一跳是空的。

## 为什么难发现

**不产生任何错误信号**：SQL 成功、Scan 成功、既有测试全绿、日志无痕。
字段有 json tag、有注释、还有专门的单测覆盖**消费它的纯函数**——
只有中间那一跳是空的。

## 两个后果

### 1. 发票自愈的身份校验退化成弱判据

`invoice_harvest.harvestOne`（`invoice_harvest.go:258`）拿 `GetEmailByID`
的返回值调 `sameEmailMessage(em, raw)`。那条判据本来是：

```
真实 Message-ID 双方都有且相等 -> 强确认（即使主题被服务商改写）
真实 Message-ID 双方都有且不等 -> 强否定（就是另一封）
```

`emHasReal` 恒为 false ⇒ **生产里这两条分支从不执行**，只剩
`subject + from + 同日` 的弱判据。而真实数据里两张同名发票的头部完全一样
（这正是当初拒绝合成 IMAP UID 要防的事故）。

### 2. 已清空正文的邮件会被重新回源

`server_email_summary.summarizeBody`（`server_email_summary.go:177`）
第一道守卫：

```go
if em.BodyPurged { return "" }
```

恒不触发 ⇒ 用户软删并清空正文的邮件会被 IMAP 重新回源、喂给 LLM、
再把摘要写回已删除的行。

> 当前真库里 `body_purged` 为真的行数是 **0**，所以这个闸门**逻辑上一直
> 关着、目前没造成损失**。如实说清这一点，不要写成「已造成数据损坏」。

## 为什么纯函数测试抓不到

`sameEmailMessage` 的用例直接构造 `Email` 字面量，天然带着 `MessageID`。
**只有打真实 PG store 才测得到这一跳。** 这也是新增
`store_getbyid_columns_test.go` 的原因。

## 验证

新增 `store_getbyid_columns_test.go`，三种读法都要拿到两个字段
（含 `SoftDeleteEmailsScoped` 之后的 `BodyPurged = true`），
外加一条把「读回」和「消费」接起来的用例：主题被改写、只有真实
Message-ID 相等能确认是同一封，验证生产路径上真的会走强确认分支。

**负控 2 路，各自干净隔离后恢复：**

| 负控 | 注入 | 结果 |
|---|---|---|
| NEGCTL-A | 拿掉 `GetEmailByID` 的两列 Scan | 仅 `GetEmailByID` 侧 **3 处**转红，Scoped 仍绿 |
| NEGCTL-B | 拿掉 `GetEmailByIDScoped` 的两列 Scan | 仅 Scoped 侧 **2 处**转红，`GetEmailByID` 仍绿 |

> 负控必须能编译。第一版把 Scan 目标改成 `_` 直接 build failed，
> 改成「SELECT 保留两列但不 Scan」才既语义等价又合法。

回归：`go build ./...` 0；`go vet ./...` 0；`go test ./internal/email/` 30.7s ok；
`go test ./internal/server/` 15.4s ok。

## 顺带查清：三个死字段（**不是** bug，未处理）

用「拿结构体字段清单扫非测试代码里的 Scan 目标 / 赋值」系统查了一遍
`Email` 的 22 个字段，剩下三个从未被读回：

| 字段 | 状态 |
|---|---|
| `ActionReason` | 有写入（`fetcher.go:800`）+ 落库（`store.go:538`），**无任何读取方** |
| `Email.DeletedAt` | 同上，无读取方 |
| `Email.Importance` / `AISummary` / `SuggestedAction` | 经**局部变量**中转后 Scan，正常 |

前两个是**死字段**（数据在库里但没人用），与「守卫失效」是不同性质的问题。

> 客户端的 `shouldSkipSyncWrite({deletedAt, bodyPurged})` 确实消费了这两个
> 字段，但读的是**客户端本地 SQLite**（`emails-store.ts:444`），
> 不经过 `GetEmailByID`。所以 Go 侧确实没有读取方。

处置：属清理项（要么接上消费方，要么删字段），未擅自改动。

> 扫描器本身有缺陷：它把 `&e.MessageID` 报成 `scan=false`，
> 因为 Scan 目标常常是**局部变量**而非结构体字段。
> 这个工具只能用来**缩小人工核查范围**，不能直接采信输出。
