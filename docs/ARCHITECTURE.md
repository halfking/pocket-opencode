# OpenCode Pocket 系统架构文档

**版本**: v1.1.0
**更新日期**: 2026-07-24
**状态**: Phase 2 Agent 模块已完成

---

## 📋 目录

1. [系统概览](#系统概览)
2. [架构分层](#架构分层)
3. [核心模块](#核心模块)
4. [数据流](#数据流)
5. [API 参考](#api-参考)
6. [安全模型](#安全模型)
7. [部署架构](#部署架构)

---

## 系统概览

```
┌─────────────────────────────────────────────────────────────────┐
│                    OpenCode Pocket 架构图                        │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌──────────────┐     ┌──────────────────────────────────┐     │
│  │   Mobile     │     │         Backend (Go)             │     │
│  │   App        │◄────│                                  │     │
│  │  (Capacitor) │     │  ┌────────────┐ ┌───────────┐  │     │
│  └──────────────┘     │  │ REST API   │ │ WebSocket │  │     │
│         │              │  │  Server    │ │   Hub     │  │     │
│         │ HTTPS/WSS   │  └─────┬──────┘ └─────┬─────┘  │     │
│         ▼              │        │               │        │     │
│  ┌──────────────┐     │        ▼               ▼        │     │
│  │   Backend    │◄────│  ┌────────────────────────────┐ │     │
│  │   Gateway    │     │  │     Service Layer          │ │     │
│  └──────────────┘     │  │  ┌────────┐ ┌─────────┐  │ │     │
│                        │  │  │ Agent  │ │  Email  │  │ │     │
│                        │  │  │Module  │ │ Module  │  │ │     │
│                        │  │  └────────┘ └─────────┘  │ │     │
│                        │  │  ┌────────┐ ┌─────────┐  │ │     │
│                        │  │  │ kxmem- │ │  AI     │  │ │     │
│                        │  │  │  ory   │ │ Gateway │  │ │     │
│                        │  │  └────────┘ └─────────┘  │ │     │
│                        │  └────────────────────────────┘ │     │
│                        └──────────────────────────────────┘     │
│                                    │               │           │
│                        ┌───────────┴───┐   ┌───────┴────────┐  │
│                        ▼               ▼   ▼                ▼  │
│                   ┌─────────┐    ┌─────────┐   ┌─────────────┐ │
│                   │  ACP    │    │PostgreSQL│   │ External    │ │
│                   │ Agent   │    │(Optional)│   │ Services    │ │
│                   │(Codex)  │    └─────────┘   │(Google/     │ │
│                   └─────────┘                   │ Outlook)    │ │
│                                                └─────────────┘ │
└─────────────────────────────────────────────────────────────────┘
```

---

## 架构分层

### 1. 接入层 (Gateway Layer)

| 组件 | 协议 | 功能 |
|------|------|------|
| REST API Server | HTTPS | 业务 API、认证 |
| WebSocket Hub | WSS | 实时消息、流式事件 |
| OAuth Callback | HTTPS | 第三方 OAuth 回调 |

### 2. 服务层 (Service Layer)

| 模块 | 职责 |
|------|------|
| **Agent Module** | ACP 协议适配、会话管理、流式事件 |
| **Email Module** | OAuth 认证、IMAP 同步、邮件分类 |
| **AI Gateway** | 嵌入向量、LLM 聊天、kxmemory 编排 |
| **Identity Module** | 工作空间、用户身份、JWT 签发 |

### 3. 数据层 (Data Layer)

| 存储 | 类型 | 用途 |
|------|------|------|
| PostgreSQL | 关系型 | 主数据存储 (可选) |
| SQLite | 嵌入式 | 本地缓存、会话 |
| memory | 内存 | 临时状态、WebSocket clients |

### 4. 外部服务集成

| 服务 | 协议 | 用途 |
|------|------|------|
| Google OAuth | OAuth 2.0 | Gmail 访问 |
| Microsoft OAuth | OAuth 2.0 | Outlook 访问 |
| kxmemory | HTTP REST | AI 分类、总结 |
| llm-gateway | HTTP REST | 企业 LLM 网关 |
| ACP Agent | Stdio JSON-RPC | Codex/Claude CLI |

---

## 核心模块

### Agent Module (Phase 2)

**路径**: `backend/internal/agent/`

```
┌─────────────────────────────────────────────────────────────┐
│                    Agent Adapter 架构                        │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌──────────────────────────────────────────────────────┐  │
│  │                   AgentAdapter 接口                    │  │
│  │  - AdapterType()                                    │  │
│  │  - Capabilities()                                    │  │
│  │  - HealthCheck()                                    │  │
│  │  - Session Management                                │  │
│  │  - SubscribeEvents()                                 │  │
│  │  - PermissionCapable                                 │  │
│  │  - QuestionCapable                                   │  │
│  └──────────────────────────────────────────────────────┘  │
│                            │                               │
│          ┌─────────────────┼─────────────────┐            │
│          ▼                 ▼                 ▼            │
│  ┌───────────────┐ ┌───────────────┐ ┌───────────────┐  │
│  │ ACPStdioAdapter│ │OpenCodeAdapter│ │ MockAdapter   │  │
│  │  (Stdio)      │ │  (HTTP)       │ │  (测试用)     │  │
│  └───────────────┘ └───────────────┘ └───────────────┘  │
│                                                              │
│  ┌──────────────────────────────────────────────────────┐  │
│  │                 Transport 层                          │  │
│  │  - StdioTransport (stdio JSON-RPC)                  │  │
│  │  - HttpTransport (HTTP JSON-RPC)                    │  │
│  │  - WsTransport (WebSocket)                         │  │
│  └──────────────────────────────────────────────────────┘  │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

#### ACP Stdio Adapter

通过标准输入输出连接实现 ACP JSON-RPC 2.0 协议的 agent (如 Codex、Claude CLI)。

**核心功能**:

| 方法 | 描述 |
|------|------|
| `NewACPStdioAdapter()` | 构造新的 Stdio 适配器 |
| `Capabilities()` | 返回支持的功能列表 |
| `CreateSession()` | 创建新会话 |
| `LoadSession()` | 加载已有会话 |
| `ListSessions()` | 列出所有会话 |
| `DeleteSession()` | 删除会话 |
| `SendPrompt()` | 发送提示词 |
| `SubscribeEvents()` | 订阅流式事件 |
| `ListPendingPermissions()` | 列出待处理权限请求 |
| `ReplyPermission()` | 回复权限请求 |
| `ListPendingQuestions()` | 列出待处理问题 |
| `ReplyQuestion()` | 回复问题 |
| `RejectQuestion()` | 拒绝问题 |

### WebSocket Hub

**路径**: `backend/internal/websocket/hub.go`

定向广播支持，按 `UserID` 和 `WorkspaceID` 精确推送。

```go
// 广播目标定义
type BroadcastTarget struct {
    UserID      string  // 空=不按用户过滤
    WorkspaceID string  // 空=不按工作空间过滤
}

// 定向广播示例
hub.BroadcastTo(BroadcastTarget{UserID: "user123"}, "event.type", payload)
hub.BroadcastToUser("user123", "event.type", payload)  // 便捷方法
```

### Email Module

**路径**: `backend/internal/email/`

```
┌─────────────────────────────────────────────────────────────┐
│                    Email 模块架构                            │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐   │
│  │ OAuth 认证  │───►│ IMAP 同步   │───►│   邮件分类  │   │
│  │ (刷新/轮换) │    │ (抓取/存储) │    │ (kxmemory)  │   │
│  └─────────────┘    └─────────────┘    └─────────────┘   │
│                                                              │
│  ┌─────────────────────────────────────────────────────┐    │
│  │                   Store 层                           │    │
│  │  - accounts (邮箱账户)                              │    │
│  │  - emails (邮件)                                   │    │
│  │  - oauth_tokens (加密token)                        │    │
│  │  - daily_summaries (每日总结)                      │    │
│  └─────────────────────────────────────────────────────┘    │
│                                                              │
└─────────────────────────────────────────────────────────────┘
```

### AI Gateway

**路径**: `backend/internal/llmbff/`, `backend/internal/llmgateway/`

无状态代理，仅转发请求，不存储用户数据。

| 端点 | 功能 |
|------|------|
| `POST /api/embed` | 文本嵌入向量 |
| `POST /api/llm/chat` | LLM 聊天代理 |
| `POST /api/notes/classify` | 笔记 AI 分类 |
| `POST /api/emails/classify` | 邮件 AI 分类 |

---

## 数据流

### 1. Agent 会话流程

```
┌────────┐     ┌─────────────┐     ┌──────────────┐     ┌─────────┐
│ Mobile │────►│ REST API    │────►│ ACPStdio    │────►│  ACP    │
│  App   │     │ CreateSession│     │ Adapter     │     │ Agent   │
└────────┘     └─────────────┘     └──────────────┘     └─────────┘
     │                                    │
     │         ┌──────────────────────────┘
     │         │
     ▼         ▼
┌─────────────┐
│ WebSocket   │◄──── SubscribeEvents (流式推送)
│   Hub       │
└─────────────┘
     │
     ▼
┌────────┐
│ Mobile │ (实时事件: session/update, permission, question)
│  App   │
└────────┘
```

### 2. OAuth 刷新流程

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│  Scheduler  │────►│OAuthRefresher│────►│   Google    │
│  (定时触发) │     │  (HTTP)     │     │ OAuth API   │
└─────────────┘     └─────────────┘     └─────────────┘
                          │
                          ▼
                    ┌─────────────┐
                    │   Store    │
                    │ (加密存储)  │
                    └─────────────┘
```

### 3. 邮件同步流程

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   定时器   │────►│IMAP Fetcher │────►│   Store     │
│ (SyncInterval)│    │  (OAuth2)  │     │ (落库)      │
└─────────────┘     └─────────────┘     └─────────────┘
                                            │
                                            ▼
                                    ┌─────────────┐
                                    │  kxmemory   │
                                    │ (AI 分类)   │
                                    └─────────────┘
```

---

## API 参考

### 认证

| 端点 | 方法 | 描述 |
|------|------|------|
| `/api/auth/login` | POST | JWT 登录 |
| `/api/auth/workspace` | GET | 获取当前工作空间 |

### Agent

| 端点 | 方法 | 描述 |
|------|------|------|
| `/api/agent/sessions` | GET | 列出会话 |
| `/api/agent/sessions` | POST | 创建会话 |
| `/api/agent/sessions/:id` | GET | 加载会话 |
| `/api/agent/sessions/:id` | DELETE | 删除会话 |
| `/api/agent/sessions/:id/prompt` | POST | 发送提示 |
| `/api/agent/sessions/:id/permissions` | GET | 待处理权限 |
| `/api/agent/sessions/:id/permissions/:pid` | POST | 回复权限 |
| `/api/agent/sessions/:id/questions` | GET | 待处理问题 |
| `/api/agent/sessions/:id/questions/:qid` | POST | 回复问题 |

### Email

| 端点 | 方法 | 描述 |
|------|------|------|
| `/api/email/oauth/start` | POST | 启动 OAuth |
| `/api/email/accounts` | GET/POST | 账户列表/创建 |
| `/api/email/accounts/:id` | PUT/DELETE | 更新/删除账户 |
| `/api/emails` | GET | 邮件列表 |
| `/api/emails/sync` | POST | 触发同步 |
| `/api/email/summaries` | GET | 每日总结 |

### AI

| 端点 | 方法 | 描述 |
|------|------|------|
| `/api/embed` | POST | 嵌入向量 |
| `/api/llm/chat` | POST | LLM 聊天 |
| `/api/notes/:id/classify` | POST | 笔记分类 |

---

## 安全模型

### 认证与授权

1. **JWT Token**
   - 24小时有效期
   - 包含 `user_id`, `role`, `workspace_id` claims
   - 签名算法: HS256

2. **工作空间隔离**
   - 每个用户默认工作空间: `ws_<user_id>`
   - 数据按 `workspace_id` 隔离

3. **OAuth 安全**
   - PKCE + State 参数防 CSRF
   - Token 加密存储 (AES-GCM)
   - Refresh token 轮换

### WebSocket 安全

| 机制 | 描述 |
|------|------|
| Origin 验证 | 配置允许的 origin 列表 |
| 心跳保活 | 54秒 ping/pong |
| 连接超时 | 60秒读超时 |
| 消息大小限制 | 32KB max |

### 生产环境要求

```go
// config.go Validate()
- JWT_SECRET >= 32 bytes
- DevAuth = false
- MCPInsecureTLS = false
- PostgresDSN 必须配置
- AllowedOrigins 必须配置
```

---

## 部署架构

### 开发环境

```
┌─────────────────────────────────────────────────────┐
│                  开发环境                             │
├─────────────────────────────────────────────────────┤
│                                                      │
│   Mobile App (localhost:8080)                       │
│            │                                         │
│            ▼                                         │
│   pocketd (localhost:8088)                           │
│     │                                              │
│     ├── kxmemory (localhost:8081) [可选]           │
│     └── PostgreSQL (localhost:5432) [可选]          │
│                                                      │
└─────────────────────────────────────────────────────┘
```

### 生产环境

```
┌─────────────────────────────────────────────────────┐
│                  生产环境                             │
├─────────────────────────────────────────────────────┤
│                                                      │
│   ┌─────────────┐     ┌─────────────────────────┐  │
│   │   Mobile    │     │     Load Balancer        │  │
│   │    App      │────►│     (HTTPS/WSS)          │  │
│   └─────────────┘     └───────────┬───────────────┘  │
│                                   │                  │
│                    ┌──────────────┴───────────────┐ │
│                    │                              │ │
│              ┌─────▼─────┐              ┌──────▼────┐│
│              │  pocketd  │              │   pocketd  ││
│              │  (Node 1) │              │  (Node 2) ││
│              └─────┬─────┘              └──────┬─────┘│
│                    │                              │     │
│              ┌─────▼──────────────────────────▼─────┐│
│              │           PostgreSQL Cluster          ││
│              │         (主从复制)                    ││
│              └──────────────────────────────────────┘│
│                              │                        │
│                    ┌──────────┴──────────┐           │
│                    ▼                      ▼           │
│              ┌──────────┐        ┌──────────────┐    │
│              │ kxmemory │        │  llm-gateway │    │
│              │ (FastAPI)│        │  (企业网关)   │    │
│              └──────────┘        └──────────────┘    │
│                                                      │
└─────────────────────────────────────────────────────┘
```

---

## 配置参考

### 环境变量

| 变量 | 默认值 | 描述 |
|------|--------|------|
| `POCKET_HTTP_PORT` | 8088 | HTTP 端口 |
| `POCKET_ENV` | development | 环境 (production/development) |
| `POCKET_JWT_SECRET` | - | JWT 签名密钥 (生产必需 >=32 bytes) |
| `POCKET_DEV_AUTH` | false | 开发环境 admin/admin 登录 |
| `POCKET_POSTGRES_DSN` | - | PostgreSQL 连接字符串 |
| `POCKET_KXMEMORY_BASE_URL` | - | kxmemory 服务地址 |
| `POCKET_EMAIL_MASTER_KEY` | - | 邮件 token 加密密钥 |
| `POCKET_EMAIL_GOOGLE_CLIENT_ID` | - | Google OAuth Client ID |
| `POCKET_EMAIL_GOOGLE_CLIENT_SECRET` | - | Google OAuth Client Secret |
| `POCKET_ALLOWED_ORIGINS` | - | 允许的 WebSocket origin (逗号分隔) |

---

**文档版本**: v1.1.0
**最后更新**: 2026-07-24
**维护者**: OpenCode Pocket Team
