# kxmemory 开发与对接指南

> 状态：✅ Phase 1-6 准备完成，等待真实 kxmemory 服务部署。

## 1. 概述

kxmemory 是 pocketd 的 AI 编排服务（FastAPI）。pocketd 在笔记创建/邮件抓取后调它做：
- **笔记分类**：domain / category / tags / 智能链接 / SSOT 冲突检测
- **邮件批量分类**：category / importance / 摘要 / 建议动作
- **每日邮件总结**：21:00 自动触发（可调整）

当 kxmemory 未配置时（`POCKET_KXMEMORY_BASE_URL` 为空），所有 AI 调用降级：
- 同步路径（`POST /api/notes/{id}/classify`）→ 503
- 异步路径（`classifyNoteAsync` / `classifyEmailsAsync`）→ 静默跳过
- `email.Scheduler.runDailySummary` → log-only

## 2. API 契约

kxmemory 实现以下 3 个端点（路径前缀 `/v1`，见 `docs/2026-07-02-kxmemory-api-contract.md`）：

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/v1/notes/classify` | 笔记分类，返回 domain/category/tags/ssot_conflicts |
| POST | `/v1/emails/classify` | 邮件批量分类，返回 category/importance/summary |
| POST | `/v1/emails/daily-summary` | 每日邮件总结，返回 summary/breakdown/todos |
| GET  | `/healthz` | 健康检查 |

请求/响应类型见 `internal/kxmemory/client.go`。

## 3. 重试与错误处理

### 重试策略

`internal/kxmemory/client.go::DefaultRetryConfig`：
- 最多 **3 次**尝试（首次 + 2 次重试）
- 指数退避：**0.5s → 2s**（±20% jitter）
- **仅重试 transient 错误**：5xx、网络错误、超时
- **不重试 permanent 错误**：4xx、context.Canceled、JSON decode 失败

### 错误响应格式

kxmemory 不可达时，前端拿到的响应：

```json
{
  "error": "kxmemory /v1/notes/classify returned 502: <body>",
  "code": "KXMEMORY_UPSTREAM",
  "retryable": true
}
```

- 503（transient）+ `retryable: true` → 前端应允许自动重试或展示"kxmemory 暂时不可达"
- 502（permanent）+ `retryable: false` → 前端应展示明确错误

### WS 失败事件

异步路径（创建笔记后分类）失败时，pocketd 广播 `note.classification_failed`：

```json
{
  "noteId": "note-123",
  "code": "KXMEMORY_UNREACHABLE",
  "retryable": true,
  "error": "..."
}
```

前端可基于 `retryable` 显示重试按钮。

## 4. 本地开发：使用 kxmemmock

在没有真实 kxmemory 服务时，可以用本地 mock 让前端开发与集成测试跑通。

### 启动 mock

```bash
cd backend
go build -o kxmemmock ./cmd/kxmemmock
./kxmemmock                              # 默认监听 :8089
POCKET_KXMEMMOCK_PORT=9000 ./kxmemmock   # 自定义端口
```

mock 默认行为：
- `/v1/notes/classify`：按关键字猜测 domain（work/study/life/idea）+ category
- `/v1/emails/classify`：按 subject 关键字分类（invoice→bill，meeting→work 等）
- `/v1/emails/daily-summary`：统计 breakdown + 提取 todo
- 无外部依赖，毫秒级响应

### 让 pocketd 指向 mock

```bash
export POCKET_KXMEMORY_BASE_URL=http://127.0.0.1:8089
export POCKET_EMAIL_MASTER_KEY=$(openssl rand -base64 32)
./pocketd
```

## 5. 生产部署 checklist

部署真实 kxmemory 服务后，需要：

1. **配置 base URL**
   ```bash
   POCKET_KXMEMORY_BASE_URL=https://kxmemory.your-domain.com
   ```

2. **共享 JWT secret**（kxmemory 用 `POCKET_JWT_SECRET` 校验 Bearer token）
   ```bash
   POCKET_JWT_SECRET=<32+ bytes random>
   ```

3. **健康检查**：kxmemory 启动后 `curl http://kxmemory:8000/healthz` 应返回 200

4. **时区**（影响 21:00 总结触发时间）
   ```bash
   POCKET_TIMEZONE_OFFSET_SEC=28800  # UTC+8 中国
   ```

5. **可选**：调整重试策略（在 `internal/kxmemory/client.go` 改 `DefaultRetryConfig`）

## 6. DailySummary 触发时间表

| 事件 | 时间 | 行为 |
|------|------|------|
| Email fetch | 每分钟 1 次 | IMAP 同步新邮件 |
| 分类 | 笔记 POST / 邮件 sync 后（异步） | kxmemory.ClassifyNote / ClassifyEmails |
| **每日总结** | **每天 21:00（用户时区）** | kxmemory.DailySummary → 写 daily_summaries 表 |

调整 21:00：修改 `internal/email/scheduler.go::dailySummaryLoop` 中的 `nextTime(21, 0, 0)`。

## 7. 已知 Gap（待 Phase 7+ 处理）

- **无 fallback 分类**：kxmemory 完全不可用时，notes/email 的分类会缺失。前端目前展示 `domain=""`。
- **无 DLQ / 持久重试队列**：`classifyNoteAsync` 失败后只 log + WS 通知；用户需手动点重试按钮调用 `/api/notes/{id}/classify`。
- **DailySummary 无去重**：同一用户多次触发会写多条记录（依赖 UNIQUE(user_id, summary_date) 保证不重复，但失败重试可能产生部分写入）。
- **健康检查未对外暴露**：kxmemory 不可达时只有 handler 失败信息；可在 `/api/diagnostics/kxmemory` 加 stats 端点（计划中）。
- **无单元测试覆盖 handler 成功路径**：`email.Store` 是具体 struct，无法 mock；集成测试需在 PostgreSQL 环境运行（SIMULATOR_TEST 覆盖）。

## 8. 调试

### Stats 端点（计划中）

```bash
curl http://localhost:8088/api/diagnostics/kxmemory
```

返回：
```json
{
  "success_count": 42,
  "retry_count": 3,
  "failure_count": 1,
  "last_error": "kxmemory /v1/notes/classify returned 502: ..."
}
```

### 查看日志

```bash
journalctl -u pocketd | grep kxmemory
```

关键日志：
- `kxmemory AI orchestrator enabled: <url>` — 启动时确认配置
- `[kxmemory] classify note %s failed: %v` — 单次失败
- `[kxmemory] classified %d/%d emails` — 批量完成
- `[email/scheduler] daily summary done: %d success, %d failed` — 每日总结