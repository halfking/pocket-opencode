# 审计项：后台执行的 API 能否被强行终止

日期：2026-10-02
对应用户验收条目：*「检查所有的请求是否在后台执行，确认可以在切换页面后仍能执行」*
与 *「后台执行的 api 可以强行终止」*

## 结论

**一半能，一半不能；而且"能"的那一半用户也没有按钮可按。**

| 任务 | 切页后继续 | 客户端断开会停吗 | 有无用户可点的终止 |
|---|---|---|---|
| `POST /api/email/pipeline/run` | 会 | **会**（走 `r.Context()`） | **无** |
| `POST /api/emails/ops/sync` | 会 | **会**（90s 超时挂在 `r.Context()` 上） | 无 |
| `POST /api/emails/sync` 的收信部分 | 会 | 会（30s 超时 `r.Context()`） | 无 |
| 收信后的**分类 / 发票提取** | 会 | **不会** | **无** |
| AI 对话流 | 会 | 会（store `stop()` → `handle.abort()`） | **有** |

---

## 1. 切页后仍能执行 —— 满足

前端 `api/http.ts` 的 `httpOnce` 正确处理了取消：

```ts
const controller = new AbortController()
if (callerSignal) { ... controller.abort() }
const timer = timeoutMs > 0 ? setTimeout(() => { timedOut = true; controller.abort() }, timeoutMs) : null
```

流式对话的所有权在 `native/aiStreamRuntime`，`aiChatStore.streamHandles`
按 `conv.id:msgId` 注册 handle，**不随组件卸载销毁**——所以切页后流继续，
这是设计意图，不是 bug。

## 2. 能否强行终止 —— 分两种情况

### (a) 走 `r.Context()` 的：客户端断开会停

- `handleEmailPipelineRun` → `s.runEmailPipeline(r.Context(), ...)`
- `executeOpsEntries` → `context.WithTimeout(r.Context(), opsExecTimeout /* 90s */)`
- `handleEmailSync` 的每个账户 → `context.WithTimeout(r.Context(), 30*time.Second)`

请求被中止时 ctx 取消，下游 IMAP / HTTP 调用随之取消。**机制是对的。**

### (b) 脱离 `r.Context()` 的：**停不下来**

收信成功后触发的后处理是裸 goroutine + `context.Background()`：

- `server_assistant.go:2129` — `go func(){ ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute); ClassifyUnclassified(..., 20) ... }()`
- `server_assistant.go:2186` — `classifyEmailsAsync` 同款，60s
- `server_email_invoice.go:108` — 发票提取，30s

`context.Background()` 意味着**客户端断开、切页、关页面都不会取消它们**，
而且没有任何 cancel 端点，也没有 in-flight 去重守卫
（账户级 `Sync` 本身有 `TestSyncSkipsAccountAlreadyInFlight` 那套守卫，
但这个后处理 goroutine 没有）。

实际暴露面有限：它只在 `ShouldProcessAfterFetch(synced, totalSaved)` 为真时
才起（也就是**这轮真的收到了新邮件**），重复同步多半不满足条件。

## 3. 用户侧根本没有终止入口

`InvoiceListView.vue:8` 的「收信整理」按钮：

```vue
<button :disabled="syncing" aria-label="收信整理" @click="runPipeline">
```

`use-invoice-list.ts:185` 里 `runcing.value = true` 直到整轮跑完。
按 `server.go:955` 的注释，这一轮**实测 1m30s**。

- 按钮在此期间是 disabled，**没有第二个按钮可以按**
- `emailApi.runPipeline()` 没有传 `signal`，也没有 `AbortController`，
  所以**即使用户离开页面，fetch 也不会被取消**
- 服务端 handler 同步等这一轮跑完才写响应

也就是说：一次 90 秒的操作，用户只能干等。

## 4. 为什么没有直接改

两个需求本身是冲突的：

- 「切页后仍能执行」→ 后处理**必须**脱离 `r.Context()`
- 「可以强行终止」→ 需要一个独立的、可寻址的取消句柄

所以正确解法不是把 `context.Background()` 换成 `r.Context()`（那会把
"切页后继续" 弄坏），而是**作业注册表 + 取消端点**：

```
POST /api/email/pipeline/run    → 返回 { jobId }
POST /api/email/pipeline/stop   → { jobId } 取消
GET  /api/email/pipeline/status → 进度
```

这属于**加功能**，不是修 bug，需要你点头再动。
本轮只做结论，不擅自改。

## 5. 一条被排除的疑似问题

`aiChatStore.stop()` / `deleteConversation()` 用
`k.startsWith(conv.id)` 匹配流 handle，看着像前缀碰撞隐患
（`c1` 会命中 `c12:...`）。

**排除**：`uid()` = `m-${Date.now().toString(36)}-${6 位定长随机}`，
随机段定长 6，两两不可能互为真前缀。记录在此免得以后重查。
