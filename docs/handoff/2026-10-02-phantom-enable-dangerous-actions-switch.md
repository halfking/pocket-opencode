# 幻影开关 `enable_dangerous_actions`：注释承诺了一个不存在的安全闸门

日期：2026-10-02
分支：`feat/mail-config-deploy`
状态：**文档修正（无行为变更）+ 一个待产品决策的问题**

---

## 结论

`backend/internal/email/rules/engine.go` 长期声称：所有规则 action 的实际副作用
都要由「账户级 `enable_dangerous_actions` 开关」放行，server 层 handler 有校验，
fetcher 只落「建议意图」不直接改信。

**这三句全是假的。** 全仓不存在这个开关。真实行为是：只要账户 rules 命中，
副作用立即发生，没有 opt-in。

---

## 证据

### 1. 开关不存在

全 backend 检索 `enable_dangerous_actions` / `EnableDangerousActions` / `dangerous`，
命中的只有三处**注释**（engine.go 的 Action 说明 + 旧格式黑名单分支），
零实现：

- 账户表 `email_accounts`（`store.go:57` 建表 DDL）无该列
- `Account` 结构体无该字段
- `handleEmailAccountOps` / `updateEmailAccount` 无该校验
- `fetcher.go` 执行 action 前无该判断

### 2. archive 根本不走意图队列

原注释把 archive 归到「fetcher 写 email_action_intents，由 scheduler 消费」，
与实现相反：

```go
// fetcher.go:781-786
case rules.ActionArchive:
    // 归档直接在入库时落地：分类标 archived + 标已读
    em.Category = "archived"
    em.IsRead = true
```

`scheduler.go:449` 自己就写着「archive 不入队（fetcher 落库即归档）」——
两处注释互相矛盾，而实现站在 `scheduler.go` 那边。

### 3. 真实副作用确实会发生

| action | 执行方式 | 是否动真实信箱 |
|---|---|---|
| `mark-important` | fetcher 入库时写 `emails.importance` | 否（只改本地标记） |
| `label-category` | fetcher 入库时写 `emails.category` | 否 |
| `archive` | fetcher 入库时置 `category=archived` + `is_read` | 否（不动 IMAP） |
| `route-folder` | 写 intents → `intentLoop` 每分钟 claim → `intentExecutor.Execute` | **是：真实 IMAP MOVE** |
| `trigger-autoreply` | 同上 | **是：真实 SMTP 发信** |

`intentLoop`（`scheduler.go:455`）每分钟 tick 一次，`processScopedIntents` 按
`(userID, workspaceID)` 认领后逐条 `Execute`，无任何放行判断。

### 4. 风险范围有限，但不是零

`updateEmailAccount` 会先校验账户归属，越权返回 404，所以**只能改自己名下账户
的规则**。这不是「任意用户可触发他人邮箱」的越权问题。

但对一个自托管个人邮件客户端来说，真正的风险是**误配**：

- 一条 `sender-whitelist` 规则同时勾了「标重要」和「移文件夹」，
  用户以为「移文件夹」只是整理，实际会真的在服务器上 MOVE 信；
- `trigger-autoreply` 更重：一条规则就能让后端用真实凭据向发件人发信。

而原注释会让人**以为有闸门**，从而在 UI 里放心勾选。

---

## 已做

只改 `engine.go` 的注释，把上述事实如实写清（分类修正 + 明确「没有开关」）。
**无任何行为变更**，因此未加测试（没有可断言的行为改动；
加一条「断言注释里没有某个词」的测试只会把护栏变成扫描源码文本的那种脆弱物）。

`go build ./...` / `go vet ./internal/email/...` / `go test ./internal/email/...` 全绿。

---

## 待产品决策（未擅自实施）

要不要给 `route-folder` / `trigger-autoreply` 加显式 opt-in？几个方向：

1. **加账户级开关**（兑现原注释的承诺），默认关；UI 上给出明确警示。
2. **只对 `trigger-autoreply` 加开关**（唯一会对外发信的），MOVE 保持开放。
3. **不加**，仅靠文档说明——把注释改成如实描述就是这条路的落点。

这是产品行为变更，未在用户确认前实施。

---

## 附带确认：rules 配置链路是完整的

顺带验证了「给账户配 rules」这条建议确实可落地（不是空话）：

```
EmailSettingsView.vue:259 RULE_ACTIONS 含 { value: 'mark-important', label: '标重要' }
  → saveRules() → emailApi.updateAccount(a.id, { rules })
  → PUT /api/email/accounts/{id}
  → server.go:734 handleEmailAccountOps
  → server_assistant.go:1126/1181 body.Rules → acc.Rules
  → store.go:783 UPDATE email_accounts SET rules=$6
  → fetcher.go:672 rules.ParseRules(acc.Rules)
  → engine.go:273 normalizeAction("mark-important") → ActionMarkImportant
```

前端序列化的形状（`{rules:[{type,pattern,actions}]}`，action 为字符串或对象）
与后端 `ParseRules`（`engine.go:117-140`）+ `decodeActionSpecs`（`:170-198`）对得上。

所以 `2026-10-02-correction-scheduled-pipeline-and-importance-provenance.md`
里「配 rules 就能让重要提醒生效」这个建议是**可执行的**，不是推测。
