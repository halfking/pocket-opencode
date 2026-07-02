# 📨 邮箱助手模块设计

**版本**: v1.0.0
**日期**: 2026-07-02
**状态**: 设计方案
**归属**: OpenCode Pocket 个人助理 APP — 邮箱模块

> 配套：主方案 [`2026-07-02-android-personal-assistant-plan.md`](./2026-07-02-android-personal-assistant-plan.md)

---

## 📌 概要

一个智能邮箱聚合助手：接入用户多个邮箱账户，后台自动抓取，AI 自动分类筛选重要邮件，每天定时生成"今日邮件摘要"。让用户不必逐封翻阅，只看真正重要的内容。

---

## 🎯 核心能力

1. **多邮箱聚合**：IMAP 协议接入 Gmail / QQ 邮箱 / Outlook / 企业邮箱等
2. **智能抓取**：后台定时拉取，按规则过滤（重要发件人、关键词、未读优先）
3. **AI 分类**：LLM 自动打标签（工作 / 账单 / 通知 / 私人 / 营销 / 垃圾）
4. **重要邮件识别**：基于发件人白名单 + 关键词 + LLM 判断
5. **每日总结**：每天固定时间生成"今日邮件摘要"，推送通知
6. **快速处理建议**：每封重要邮件给出"建议操作"（回复 / 归档 / 待办）

---

## 🏗️ 架构

```
┌──────────────────────────────────────────────────────────┐
│                    APP (Vue3)                             │
│  邮箱列表 │ 每日摘要 │ 账户配置 │ 分类筛选              │
└──────────────┬──────────────────────────┬────────────────┘
               │ REST + WebSocket         │
   ┌───────────▼──────────┐    ┌──────────▼─────────────┐
   │  pocketd (Go)         │    │  kxmemory FastAPI       │
   │  - IMAP fetcher       │    │  - /email/classify      │
   │  - cron scheduler     │───▶│    (LLM 分类)           │
   │  - emails store (SQL) │    │  - /email/daily-summary │
   │  - 规则引擎           │    │    (LLM 总结)           │
   │  - WebSocket 推送     │    │  - /email/important     │
   └──────────┬────────────┘    │    (重要性判断)         │
              │                 └──────────▲──────────────┘
   ┌──────────▼──────────┐
   │  各邮箱 IMAP 服务器   │
   │  (Gmail/QQ/Outlook)  │
   └──────────────────────┘
```

### 为什么 IMAP 抓取放服务端（pocketd）而非客户端
- **后台可靠性**：Android 后台任务受系统限制（Doze 模式），服务端 cron 稳定
- **凭证安全**：邮箱密码/OAuth token 集中在服务端，不分散到每个客户端
- **多客户端一致**：手机/平板共享同一抓取状态
- **省电**：客户端无需常驻连接

> 注：pocketd 在本场景作为**用户自部署的私有服务端**（单用户或小团队），凭证存服务端是可接受的。若需完全客户端方案，可作为后续选项（Capacitor background-runner + 客户端 IMAP）。

---

## 🔄 数据流

### 抓取流程（每 15 分钟）

```
pocketd cron 触发
    ↓
遍历已配置的邮箱账户
    ↓
IMAP 登录（凭证明文从 Keystore/服务端 env 解密）
    ↓
SEARCH UNSEEN + 按规则（白名单发件人/关键词）过滤
    ↓
FETCH 信封 + 正文（前 5KB）+ 附件列表
    ↓
去重（Message-ID）→ 存 emails 表
    ↓
批量 POST /api/email/classify (kxmemory)
    ↓
LLM 返回 category + importance + suggested_action
    ↓
更新 emails 表 + WebSocket push email.fetched
    ↓
若 importance=high → 本地通知 (cap local-notifications)
```

### 每日总结流程（每天 21:00）

```
pocketd cron 触发（21:00）
    ↓
查询今日所有邮件（按账户/分类聚合）
    ↓
POST /api/email/daily-summary (kxmemory)
    ↓
LLM 生成：
  - 今日共 N 封，重要 M 封
  - 重要邮件逐条摘要（标题 + 发件人 + 一句话内容 + 建议操作）
  - 待处理事项汇总
  - 可忽略的批量通知归类
    ↓
存 daily_summaries 表
    ↓
WebSocket push email.summary_ready → APP
    ↓
本地通知 "今日邮件摘要已生成"
```

---

## 📦 数据模型

### pocketd PostgreSQL（emails + accounts + summaries）

> Phase 0 起 pocket 后端迁移到 PostgreSQL。下面的 DDL 已采用 PG 方言（BOOLEAN 原生、TIMESTAMP、partial index 用 `WHERE`）。

```sql
-- 邮箱账户
CREATE TABLE email_accounts (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    display_name TEXT NOT NULL,        -- "工作邮箱"
    email_address TEXT NOT NULL,
    imap_host TEXT NOT NULL,
    imap_port INTEGER DEFAULT 993,
    auth_type TEXT CHECK(auth_type IN ('password', 'oauth2')),
    -- 凭证加密存储（用服务端 master key 加密）
    credential_encrypted TEXT NOT NULL,
    -- 抓取配置
    sync_interval_min INTEGER DEFAULT 15,
    last_synced_uid INTEGER,
    last_synced_at TIMESTAMP,
    -- 过滤规则（JSON）
    rules JSON,                         -- {whitelist:[], keywords:[], blacklist:[]}
    enabled BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- 邮件
CREATE TABLE emails (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    message_id TEXT,                    -- IMAP Message-ID，用于去重
    uid INTEGER,                        -- IMAP UID
    from_address TEXT NOT NULL,
    from_name TEXT,
    to_addresses TEXT,                  -- JSON array
    subject TEXT,
    snippet TEXT,                       -- 正文前 ~500 字
    body_path TEXT,                     -- 完整正文存文件（节省 DB）
    has_attachments BOOLEAN DEFAULT FALSE,
    attachments JSON,                   -- [{filename, size, content_type}]
    date TIMESTAMP NOT NULL,
    is_read BOOLEAN DEFAULT FALSE,
    is_starred BOOLEAN DEFAULT FALSE,
    -- AI 处理结果
    category TEXT,                      -- work/bill/notification/personal/marketing/spam
    importance TEXT CHECK(importance IN ('high','medium','low')),
    ai_summary TEXT,                    -- 一句话摘要
    suggested_action TEXT,              -- reply/archive/todo/ignore
    action_reason TEXT,                 -- 建议原因
    processed_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (account_id) REFERENCES email_accounts(id) ON DELETE CASCADE,
    UNIQUE(account_id, message_id)
);

CREATE INDEX idx_emails_date ON emails(date DESC);
CREATE INDEX idx_emails_category ON emails(category);
CREATE INDEX idx_emails_importance ON emails(importance);
CREATE INDEX idx_emails_unread ON emails(is_read) WHERE is_read = FALSE;

-- 每日总结
CREATE TABLE daily_summaries (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    summary_date DATE NOT NULL,
    total_count INTEGER,
    important_count INTEGER,
    content TEXT NOT NULL,              -- Markdown 格式的总结
    action_items JSON,                  -- 提取的待办
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(user_id, summary_date)
);
```

---

## 🤖 AI 分类与总结（kxmemory 侧）

### 分类 Prompt（/api/email/classify）

输入：subject + from + snippet（前 500 字）
输出：JSON `{category, importance, ai_summary, suggested_action, action_reason}`

分类体系：
| category | 说明 | 示例 |
|----------|------|------|
| work | 工作相关 | 同事邮件、项目通知、会议邀请 |
| bill | 账单/支付 | 银行账单、订阅扣款、发票 |
| notification | 系统通知 | 注册确认、密码重置、告警 |
| personal | 私人 | 朋友家人、个人事务 |
| marketing | 营销推广 | 促销、newsletter、活动 |
| spam | 垃圾 | 可疑邮件、钓鱼 |

importance 判断要素：
- 发件人是否在用户白名单
- 是否包含截止日期/紧急关键词
- 是否是直接沟通（非群发）
- 是否需要回复（疑问句、action required）

### 每日总结 Prompt（/api/email/daily-summary）

输入：今日所有邮件的元数据 + 分类结果
输出：Markdown 总结，结构：

```markdown
# 📬 2026-07-02 邮件摘要

共收到 **23** 封，其中 **5** 封重要，需关注。

## ⭐ 需要处理（2）
1. **[张经理]** Q3 预算审批需周五前确认
   → 建议操作：回复确认
2. **[AWS]** 账单异常，本月超支 30%
   → 建议操作：查看明细

## 📋 待跟进（3）
- 项目周会纪要待整理
- 合同 v3 待审核
...

## 🗂️ 可忽略（18）
- 营销推广 12 封（已自动归档）
- 系统通知 6 封
```

---

## 🎨 UI 设计

### 邮箱列表页（EmailInboxView）
- 顶部：账户切换 tab（多账户）+ 筛选（分类/重要性/未读）
- 列表：紧凑邮件卡片（发件人 + 主题 + 时间 + 分类色标 + 重要性星标）
- 重要邮件置顶，高重要性用品牌色边框
- 底部 FAB：手动刷新

### 邮件详情页
- 发件人/时间/主题
- AI 摘要卡片（一句话 + 建议操作按钮）
- 正文（Markdown 渲染）
- 附件列表
- 操作：标记已读、加星、归档、删除

### 每日摘要页（EmailSummaryView）
- 卡片式展示当日总结（Markdown）
- 待办项可一键转为 todo（联动笔记模块）
- 历史摘要按日期浏览

### 账户配置页（EmailAccountSetup）
- 添加账户向导：选邮箱类型 → 填 IMAP 配置 → 测试连接 → 设置规则
- 预设模板：Gmail（imap.gmail.com:993）、QQ（imap.qq.com:993）、Outlook、163 等
- 规则配置：白名单发件人、关键词、黑名单、抓取频率

---

## 🔐 凭证安全

- 邮箱密码/OAuth token 在服务端用 master key 加密存 `credential_encrypted`
- master key 来自环境变量 `POCKET_EMAIL_MASTER_KEY`（部署时设置，不入库）
- 客户端永远不接触明文凭证
- IMAP 连接强制 TLS（端口 993）
- Gmail 优先用 OAuth2（而非应用专用密码）

---

## ⏰ 调度与可靠性

- **cron**：pocketd 内置调度器，每账户独立间隔（默认 15 分钟，可调 5-60 分钟）
- **增量抓取**：记录每账户 `last_synced_uid`，只拉取新邮件
- **失败重试**：IMAP 连接失败指数退避，3 次失败暂停该账户并通知用户
- **配额保护**：限制单账户每次最多拉取 50 封，避免突发流量
- **去重**：`(account_id, message_id)` 唯一约束

---

## 🔌 接口（pocketd 新增）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/email/accounts` | 列出邮箱账户 |
| POST | `/api/email/accounts` | 添加账户（含 IMAP 测试） |
| PUT | `/api/email/accounts/{id}` | 更新配置/规则 |
| DELETE | `/api/email/accounts/{id}` | 删除账户 |
| GET | `/api/emails` | 邮件列表（支持 filter: account, category, importance, unread） |
| GET | `/api/emails/{id}` | 邮件详情（含完整正文） |
| PATCH | `/api/emails/{id}` | 标记已读/加星/归档 |
| POST | `/api/emails/sync` | 手动触发抓取 |
| GET | `/api/email/summaries` | 每日总结列表 |
| GET | `/api/email/summaries/{date}` | 指定日期总结 |

WebSocket 事件：`email.fetched`、`email.summary_ready`、`email.account_error`

---

## ⚠️ 风险与缓解

| 风险 | 缓解 |
|------|------|
| IMAP 被邮箱风控（频繁登录） | 单次连接复用，遵守 15 分钟间隔，错误退避 |
| 邮件正文过大 | 仅存前 5KB 到 DB，完整正文存文件 |
| LLM 分类成本 | 仅对未读 + 非营销类邮件调用 LLM；批量调用；缓存相似模板 |
| 凭证泄露 | 服务端加密 + TLS + env 管理 |
| Gmail OAuth 复杂 | 首版支持应用专用密码，OAuth2 作为后续增强 |
| 抓取延迟感 | 支持 APP 内手动刷新按钮 |

---

## 📅 实施计划（第 3-4 周）

1. **第 3 周上半**：pocketd IMAP fetcher + email_accounts/emails 表 + 凭证加密
2. **第 3 周下半**：cron 调度 + 增量抓取 + 去重 + WebSocket 推送
3. **第 4 周上半**：kxmemory 分类/总结 API + APP 列表/详情 UI
4. **第 4 周下半**：每日总结 + 账户配置向导 + 规则引擎

---

## 🔭 后续演进

- **OAuth2 接入**：Gmail / Outlook 原生 OAuth，免应用密码
- **发送邮件**：SMTP 集成，支持直接回复（AI 起草）
- **智能回复建议**：基于邮件内容生成回复草稿
- **订阅管理**：自动识别营销邮件，一键退订
- **附件智能处理**：发票自动归档、合同送审
- **客户端 IMAP 模式**：对不愿自部署服务端的用户，提供纯客户端抓取选项
