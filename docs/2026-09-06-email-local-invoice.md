# 邮箱数据本地化存储与发票自动整理方案

> 2026-09-06 · 随 openpocket 优化批次落地

## 一、目标

1. **确立邮箱数据的存储边界**：服务端 PostgreSQL 为 SSOT，移动端 SQLCipher 为离线镜像（local-first）。
2. **发票自动整理**：账单/发票类邮件同步后自动提取结构化字段（发票号/金额/日期/销售方/类目），
   供用户浏览、归档、报销整理。

## 二、存储架构（邮箱）

| 层 | 位置 | 内容 | 加密 |
|---|---|---|---|
| SSOT | 服务端 PG（`emails`/`email_accounts` 等 8 张表） | 信封 + 分类结果 + 发票结构化记录 | 凭证列 AES-GCM（`POCKET_EMAIL_MASTER_KEY`） |
| 正文缓存 | 服务端磁盘 `dataDir/email-bodies/*.bin` | 完整正文 | AES-GCM + 临时文件原子 rename |
| 离线镜像 | 移动端 SQLCipher（`local_emails`/`local_email_accounts`/`local_email_invoices`） | 信封 + 结构化字段 | SQLCipher AES-256 全库加密 |

原则：
- **凭证永不回传前端**；镜像库不存正文、不存凭证原文。
- 分类/总结只把 snippet（前 ~500 字）发给 LLM/kxmemory。
- 镜像与服务端冲突时以服务端为准（全量重建式同步）。

## 三、发票自动整理

### 3.1 提取链路

```
IMAP 同步 → classifyEmailsAsync
              ├─ extractInvoicesAsync（纯规则，kxmemory 不在线也可用）
              │    关键词命中（发票/invoice/收据/账单…）→ ExtractInvoice → email_invoices 落库
              │    幂等：同 email_id 至多一条，已存在跳过
              └─ kxmemory.ClassifyEmails（AI 分类回写 category=bill 等）
```

手动补充入口：`POST /api/emails/invoices/extract {emailId}`（校验邮件账户归属当前用户/工作区，
摘要未命中时自动尝试解密后的缓存正文）。

### 3.2 规则提取器（`backend/internal/email/invoice.go`）

- 关键词命中：发票 / 增值税 / 开票 / 收据 / invoice / receipt / 账单 / 扣款 …
- 字段正则：发票号码、开票日期（归一化为 `YYYY-MM-DD`）、价税合计（兜底取最大 ¥ 金额）、
  销售方（兜底取发件人名称/地址）、发票抬头。
- 票种：e-invoice / vat-special / paper / receipt / bill。
- 类目推断：餐饮 / 交通 / 住宿 / 通信（含云服务订阅）/ 办公 / 其他（对齐 finance 记账类目）。
- 无金额且无发票号 → 视为营销邮件不落库（防噪音）。

### 3.3 API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/emails/invoices?status=&limit=` | 列表 + 汇总（total/filed/amount） |
| POST | `/api/emails/invoices/extract` | 对指定邮件手动提取 |
| PATCH | `/api/emails/invoices/{id}` | 归档状态 `{"status":"filed"}` |
| DELETE | `/api/emails/invoices/{id}` | 删除记录（不动邮件） |

### 3.4 前端

- 页面 `/email/invoices`（`InvoiceListView.vue`，收件箱头部 `receipt_long` 图标进入）。
- 首屏走服务端；成功后重建本地镜像；服务端不可达时退化为本地镜像离线浏览。
- 后续扩展：发票 → 记账（`finance` 模块）一键入账、按月导出 CSV。

## 四、RedClaw LLM 兜底通道（同期落地）

- `POCKET_REDCLAW_LLM_FALLBACK=true` 且 RedClaw Bridge 已配置时，`/api/llm/stream`
  的动态网关 Provider 失败后自动切换 RedClaw `/api/v1/pocket/llm/chat`（非流式 → 单 delta 降级）。
- 请求方 context 取消/超时不触发兜底（防双倍计费）；切换时下发 `retry` 帧提示前端。
- 设置页「当前连接」卡片显示 RedClaw 连接状态与租户 ID（`/api/redclaw/health`）。
