# OpenCode Supreme — 程序员移动办公平台设计文档

> 日期: 2026-07-24
> 状态: Draft / 待评审

---

## 1. 产品定位

**品牌名称（建议）：** OpenCode Supreme

**一句话定位：** 程序员的移动办公平台——让程序员在手机上完成一切工作，从 AI 编程到代码审查，从会议总结到笔记记账，一个平台搞定。

**目标用户：** 全栈/后端/前端开发者，技术管理者，自由职业程序员

**核心价值主张：**
- 🚀 **移动办公** — 通勤、会议间隙、出差，随时推进工作
- 🤖 **AI 原生** — 所有功能都有 AI 加持，语音输入、自动总结、智能分类
- 🔒 **企业就绪** — 多租户、审计、安全合规，可选私有化部署
- 🌐 **开源开放** — 社区驱动，可自建，不绑定任何商业服务

---

## 2. 用户场景矩阵

| 场景 | 用户痛点 | 解决方案 | 涉及模块 |
|------|---------|---------|---------|
| **通勤途中的代码工作** | 灵感来了但没电脑，想改代码 | 语音输入需求 → AI 生成代码 → 审查 diff → 提交 PR | AI 编程工作台、语音输入 |
| **On-call 值班应急** | 线上故障，手机不方便定位修复 | 查看 CI/CD → 分析日志 → AI 修复建议 → 一键提交 | AI 编程、通知中心、CI/CD 集成 |
| **代码审查 (PR Review)** | 手机上看 diff 体验差，无法有效 review | 结构化 diff 展示 + AI 自动审查 + 语音/快捷评论 | 代码审查模块 |
| **会议录音与纪要** | 开完会记不住决策和待办 | 录音 → 自动转写 → AI 总结 → 生成待办事项 | 会议录音室、任务管理 |
| **聊天记录回顾** | 群聊太多，重要信息被淹没 | 一键总结聊天记录 → 提取决策、待办、链接 | 聊天总结、IM 集成 |
| **产品方案撰写** | 手机上写方案文档不方便 | 语音输入需求 → AI 生成方案 → 导出 PPT | 产品方案 & PPT 生成 |
| **笔记管理混乱** | 笔记散落各处，找不到、不会归类 | 自动分类 + 语义搜索 + 智能推荐 | 笔记空间、AI 分类 |
| **日常记账** | 程序员懒得记账，月底对不上账 | 语音记账"中午吃饭38块"→ 自动分类统计 | 语音笔记 & 记账 |
| **出差办公** | 没有开发环境，无法处理代码相关事务 | 连接到 RedClaw 企业实例或远程桌面 | 实例管理、远程连接 |
| **团队协作** | 代码讨论在 IM 中碎片化 | 按仓库/PR/Issue 聚合的代码协作空间 | 协作空间、IM 集成 |

---

## 3. 功能模块全景

### 3.1 AI 编程工作台

**状态：** 已有基础，需升级

**已有能力：**
- ACP Stdio Adapter (JSON-RPC 2.0 over stdio)
- 会话管理 (Create/Load/List/Delete)
- 流式事件推送 (SubscribeEvents)
- 权限/问题管理 (ListPending/Reply/Reject)
- WebSocket 定向广播 (UserID/WorkspaceID)

**升级目标：**

| 子功能 | 优先级 | 说明 |
|--------|--------|------|
| 语音 Prompt 输入 | P0 | 语音 → STT → 作为 AI Prompt 发送 |
| 代码 Diff 结构化展示 | P0 | 手机端友好展示增删改，逐行评论 |
| 代码片段管理 | P1 | 保存/搜索/分享常用代码片段 |
| 多实例同时会话 | P1 | 在多个代码仓库间快速切换 |
| 会话归档与搜索 | P1 | 历史会话按项目/时间/标签检索 |
| AI 响应语音播放 | P2 | 将 AI 回复用 TTS 语音读出来 |

**交互流程：**
```
用户语音输入
  → STT 识别为文本
  → ACP Adapter 发送 prompt
  → AI 流式响应
  → WebSocket 推送至手机
  → 结构化 Diff 展示
  → 用户审查 → 确认/修改/评论
```

### 3.2 会议录音及总结 🆕

**状态：** 全新模块

**功能设计：**
1. **录音管理**
   - 手机端直接录制会议音频
   - 支持暂停/继续/分段录制
   - 录音文件本地加密存储
   - 历史录音列表，按日期/项目检索

2. **语音转写 (STT)**
   - 云端 STT 引擎（首选，精度高）
   - 本地离线 STT 兜底（无网络时可用）
   - 支持中文/英文混识别
   - 说话人分离（识别谁说了什么）

3. **AI 总结**
   - 输入：转写文本
   - 输出：会议摘要、关键决策、待办事项
   - 支持自定义总结模板（周会、1-on-1、技术评审等）

4. **待办同步**
   - 自动提取待办事项
   - 创建 Task → 关联到项目
   - 推送通知提醒

**输出格式示例：**
```json
{
  "meeting_title": "Sprint Planning - Week 30",
  "duration": "45min",
  "attendees": ["张三", "李四", "王五"],
  "summary": "讨论了 Q3 的 AI 功能优先级，决定优先开发会议总结模块...",
  "key_decisions": [
    "优先开发会议总结功能",
    "API 升级推迟到下一轮 Sprint"
  ],
  "action_items": [
    {"owner": "张三", "task": "完成会议总结模块设计", "deadline": "2026-07-28"},
    {"owner": "李四", "task": "调研 STT 服务方案", "deadline": "2026-07-26"}
  ],
  "tags": ["sprint-planning", "q3"],
  "next_meeting": "2026-07-31 10:00"
}
```

### 3.3 聊天记录总结 🆕

**状态：** 全新模块

**功能设计：**
1. **消息聚合**
   - 聚合多平台聊天：飞书、Telegram、Slack、Discord
   - 按群组/频道/私聊分类
   - 本地缓存最近 N 条消息

2. **智能总结**
   - 一键触发：用户点击"总结"按钮
   - 自动阈值：未读消息超过 50 条自动提示总结
   - 定时总结：每日/每周自动生成群聊摘要

3. **提取能力**
   - 决策点列表
   - 待办事项
   - 重要链接/文件/代码片段
   - 未读重点摘要

4. **与现有模块集成**
   - 复用已有的 IM 集成（飞书等）
   - 复用 kxmemory AI 编排能力
   - 生成的任务自动同步到 Task Store

### 3.4 产品方案及 PPT 生成 🆕

**状态：** 全新模块

**功能设计：**
1. **方案生成**
   - 输入：需求描述（语音/文字）
   - AI 多轮对话完善方案细节
   - 支持模板：PRD、技术方案、周报、季度总结、架构设计
   - 输出：结构化 Markdown 文档

2. **PPT 生成**
   - 方案文档 → 自动分页
   - 内置模板库（商务/技术/产品风格）
   - 渲染为 HTML 页面（WebView 展示）
   - 导出为 PDF 或图片
   - 可分享链接

3. **编辑与协作**
   - 方案在线编辑（多轮 AI 对话调整）
   - 方案版本管理
   - 支持分享给团队成员评论

### 3.5 笔记自动分类处理 🆕

**状态：** 已有 Notes Store，需升级 AI 分类能力

**功能设计：**
1. **统一笔记入口**
   - 所有笔记集中管理
   - 快速记录（语音/文字/拍照）
   - 支持 Markdown 编辑

2. **AI 自动分类**

   | 笔记类型 | 识别特征 | 自动处理 |
   |---------|---------|---------|
   | 技术笔记 | 含代码块、技术术语、架构图 | 关联到代码仓库 |
   | 会议记录 | 含时间、参会人、议程 | 关联到会议模块 |
   | 待办事项 | 含"需要""记得""别忘了"等 | 自动创建 Task |
   | 灵感想法 | 简短、创意性、非结构化 | 标记为"灵感" |
   | 产品需求 | 含"用户""功能""优化"等 | 关联到产品文档 |
   | 学习笔记 | 含教程、知识点、参考资料 | 整理到知识库 |
   | 会议纪要 | 含决策、待办、结论 | 归类到会议记录 |

3. **智能推荐**
   - 基于 pgvector 语义搜索相关笔记
   - 关联代码片段、任务、会话
   - 上下文感知：打开某个项目时推荐相关笔记

4. **搜索**
   - 全文搜索（SQLite FTS）
   - 语义搜索（pgvector）
   - 按标签/项目/时间过滤

### 3.6 语音笔记及记账 🆕

**状态：** 全新模块

**功能设计：**
1. **语音快速记录**
   - 打开 App → 长按说话 → 释放自动识别
   - 自动分类为笔记/记账/待办
   - 支持添加标签和项目关联

2. **智能记账**
   - 语音识别："中午吃饭花了38块"
   - AI 解析：{type: "expense", category: "餐饮", amount: 38, note: "午餐"}
   - 分类体系：餐饮、交通、购物、项目收入、工资等
   - 支持手动添加/编辑

3. **统计报表**
   - 按月/周/日查看收支
   - 分类饼图/趋势折线图
   - 预算设置与超支提醒
   - 导出 CSV/Excel

4. **数据存储**
   - 本地 SQLite 优先
   - 可选云端同步（加密）
   - 支持导出备份

### 3.7 代码协作空间

**状态：** 需新增

**功能设计：**
1. **PR 审查**
   - 结构化 diff 展示（增/删/改高亮）
   - AI 自动审查：代码质量、安全漏洞、风格检查
   - 快捷评论模板 + 语音评论
   - 一键 Approve/Request Changes

2. **CI/CD 状态**
   - 查看构建/测试/部署状态
   - 失败时 AI 分析日志原因
   - 通知推送

3. **Issue 管理**
   - GitHub/GitLab Issue 查看与回复
   - AI 辅助分类和优先级建议
   - 关联到本地 Task

### 3.8 通知中心

**状态：** 已有 NotifyCenter，需升级

**升级目标：**
- 聚合所有通知：AI 回复、PR 更新、CI/CD 结果、会议提醒、记账提醒
- 精细化通知规则：按项目/类型/优先级过滤
- Live Activities / 锁屏通知
- 通知摘要：每日/每周汇总

---

## 4. 系统架构

### 4.1 六层架构

```
┌─────────────────────────────────────────────────────────────────────────┐
│   L1: 用户界面层 (Mobile App)                                          │
│   Android (Vue 3 + Capacitor)  |  iOS (未来, Capacitor 编译)           │
│   ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────────┐│
│   │ AI 编程  │ │ 代码协作  │ │ 会议总结  │ │ 笔记记账  │ │ 仪表盘/通知  ││
│   │ 工作台   │ │ PR/审查   │ │ 录音室    │ │ 语音笔记  │ │ 统一入口     ││
│   └──────────┘ └──────────┘ └──────────┘ └──────────┘ └──────────────┘│
├─────────────────────────────────────────────────────────────────────────┤
│   L2: 通讯层 (Pocket Backend - Go)                                     │
│   ┌────────────┐ ┌──────────┐ ┌──────────┐ ┌────────────┐ ┌──────────┐│
│   │ WebSocket  │ │ REST API │ │ SSE Push │ │ ACP Stdio  │ │ Auth/JWT ││
│   │ Hub        │ │ 路由     │ │ 通知推送  │ │ Adapter    │ │ 认证     ││
│   └────────────┘ └──────────┘ └──────────┘ └────────────┘ └──────────┘│
├─────────────────────────────────────────────────────────────────────────┤
│   L3: 核心服务层 (Go Backend)                                           │
│   ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────────┐│
│   │ Agent    │ │ Session  │ │ Task     │ │ Email    │ │ STT 语音     ││
│   │ Adapter  │ │ Manager  │ │ Manager  │ │ OAuth    │ │ 识别模块     ││
│   ├──────────┤ ├──────────┤ ├──────────┤ ├──────────┤ ├──────────────┤│
│   │ Notes    │ │ Vault    │ │ Identity │ │ 飞书/IM  │ │ AI 网关      ││
│   │ Manager  │ │ 密码库    │ │ 身份管理  │ │ 集成     │ │ LLM Gateway  ││
│   ├──────────┤ ├──────────┤ ├──────────┤ ├──────────┤ ├──────────────┤│
│   │ 会议     │ │ 笔记     │ │ 记账     │ │ 通知     │ │ Agent        ││
│   │ 录音室   │ │ 分类引擎  │ │ 引擎     │ │ 中心     │ │ Bridge       ││
│   └──────────┘ └──────────┘ └──────────┘ └──────────┘ └──────────────┘│
├─────────────────────────────────────────────────────────────────────────┤
│   L4: 企业集成层 (RedClaw Bridge)  —— 可选，企业部署时激活              │
│   ┌────────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌───────────┐│
│   │ 多租户     │ │ LLM 路由 │ │ 知识库   │ │ 审计     │ │ Code       ││
│   │ 治理       │ │ LiteLLM  │ │ 检索     │ │ 计量     │ │ Interpreter││
│   └────────────┘ └──────────┘ └──────────┘ └──────────┘ └───────────┘│
├─────────────────────────────────────────────────────────────────────────┤
│   L5: 数据层                                                            │
│   ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────────┐│
│   │PostgreSQL│ │ pgvector │ │ S3 对象  │ │ 本地缓存  │ │ 加密存储     ││
│   │ + 关系   │ │ 向量记忆  │ │ 存储     │ │ SQLite   │ │ (Vault)      ││
│   └──────────┘ └──────────┘ └──────────┘ └──────────┘ └──────────────┘│
├─────────────────────────────────────────────────────────────────────────┤
│   L6: 基础设施层                                                        │
│   Docker / Kubernetes / AWS / 火山 / 阿里云 / 私有化部署                │
└─────────────────────────────────────────────────────────────────────────┘
```

### 4.2 RedClaw 集成方案

**集成架构：**

```
RedClaw 企业后端
  ┌──────────────────────────────────────────────┐
  │  RedClaw Gateway (Go)                        │
  │  ┌──────────┐ ┌──────────┐ ┌──────────────┐ │
  │  │ H2 Proxy │ │ Tenant   │ │ Pocket 集成   │ │
  │  │ :8091    │ │ Router   │ │ 网关 :8092    │ │
  │  │          │ │ :8090    │ │ (新增)        │ │
  │  └──────────┘ └──────────┘ └──────────────┘ │
  │                           │ REST API + WSS   │
  │                           ▼                  │
  │  ┌──────────────────────────────────────────┐│
  │  │  核心服务: LLM Router / 知识库 / 审计     ││
  │  └──────────────────────────────────────────┘│
  └──────────────────────────────────────────────┘
           │ HTTPS
           ▼
Pocket Backend (Go)
  ┌──────────────────────────────────────────────┐
  │  RedClaw 客户端模块 (新增)                    │
  │  - /internal/redclaw/client.go               │
  │  - /internal/redclaw/bridge.go               │
  │  - /internal/redclaw/auth.go                  │
  └──────────────────────────────────────────────┘
```

**RedClaw Pocket 集成网关 API 设计：**

```
POST /api/v1/pocket/llm/chat          # LLM 对话（带多租户上下文）
POST /api/v1/pocket/knowledge/search  # 知识库检索
POST /api/v1/pocket/memory/save       # 保存记忆
POST /api/v1/pocket/memory/query      # 查询记忆
GET  /api/v1/pocket/tenant/info       # 获取当前租户信息
POST /api/v1/pocket/audit/log         # 审计日志写入
GET  /api/v1/pocket/health            # 健康检查
```

**数据同步策略：**

| 数据类型 | 同步方向 | 策略 |
|---------|---------|------|
| AI 会话 | 双向 | 实时 WebSocket 流式推送 |
| 笔记 | Pocket → 可选同步 | 本地优先，按需同步 |
| 任务 | 双向 | 双向同步，冲突本地优先 |
| 知识库 | RedClaw → Pocket | 缓存 + 按需查询 |
| 审计日志 | Pocket → RedClaw | 批量上报 |
| 用户身份 | RedClaw → Pocket | 登录时 JWT 同步 |

---

## 5. 通讯协议

### 5.1 协议选择

| 通讯方向 | 协议 | 用途 | 状态 |
|---------|------|------|------|
| Pocket → RedClaw | REST API (HTTPS) | 查询企业服务、提交任务 | 需新增 |
| RedClaw → Pocket | WebSocket | 推送 AI 响应、通知 | 需新增 |
| Pocket → Mobile | WebSocket | 实时双向通讯 | ✅ 已有 |
| Mobile → RedClaw | 通过 Pocket 中转 | 间接调用企业能力 | 链路打通即可 |

### 5.2 WebSocket 消息格式 (已有，标准化)

```json
{
  "type": "session.updated",
  "sessionId": "sess_abc123",
  "data": {
    "status": "streaming",
    "content": "...."
  },
  "timestamp": "2026-07-24T10:00:00Z"
}
```

**标准消息类型：**
- `session.created` / `session.updated` / `session.deleted`
- `message.added` / `message.stream`
- `task.created` / `task.updated`
- `meeting.summary_ready`
- `chat.summary_ready`
- `presentation.ready`
- `notification.push`

---

## 6. 技术实现方案

### 6.1 开发路线图（4 阶段，约 3-4 个月）

#### Phase 1: 基础集成 (2-3 周)

| 任务 | 说明 | 涉及文件 |
|------|------|---------|
| RedClaw Pocket 集成网关 | 新增 :8092 网关，暴露企业能力 | `redclaw/enterprise/gateway-go/` |
| Pocket RedClaw 客户端模块 | 新增 RedClaw API 调用封装 | `pocket/backend/internal/redclaw/` |
| 通讯链路打通 | Pocket ↔ RedClaw 双向通信验证 | 端到端测试 |
| 多租户身份同步 | JWT 中嵌入 tenant_id | `pocket/backend/internal/auth/` |
| 基础 CI/CD | 集成测试 + 部署脚本 | 各项目 CI 配置 |

**交付物：** 端到端 Hello World 验证，Pocket 能调用 RedClaw LLM 路由

#### Phase 2: 核心功能升级 (3-4 周)

| 任务 | 说明 | 优先级 |
|------|------|--------|
| AI 编程工作台升级 - 语音输入 | 语音 → STT → ACP Prompt | P0 |
| AI 编程工作台升级 - Diff 展示 | 结构化展示代码增删改 | P0 |
| 会议录音 - 录音管理 | 手机端录音 + 文件管理 | P0 |
| 会议录音 - STT 转写 | 云端 STT 集成 | P0 |
| 会议录音 - AI 总结 | 生成摘要 + 待办提取 | P0 |
| 聊天记录总结 | 消息聚合 + AI 摘要 | P1 |
| 代码片段管理 | 保存/搜索/分享 | P1 |

**交付物：** Phase 2 功能可用，E2E 测试通过

#### Phase 3: 智能办公工具 (3-4 周)

| 任务 | 说明 | 优先级 |
|------|------|--------|
| 产品方案生成 | AI 方案生成 + 模板系统 | P0 |
| PPT 生成 | HTML 渲染 + PDF 导出 | P0 |
| 笔记自动分类 | AI 分类引擎 + 标签系统 | P0 |
| 语义搜索 | pgvector 集成 | P1 |
| 智能推荐 | 关联笔记/代码/任务 | P1 |
| 语音笔记 | 快速语音记录 + 自动分类 | P0 |
| 智能记账 | 语音识别 + 分类统计 | P0 |
| 记账统计报表 | 图表 + 导出 | P1 |

**交付物：** Phase 3 功能可用，E2E 测试通过

#### Phase 4: 企业集成与优化 (3-4 周)

| 任务 | 说明 | 优先级 |
|------|------|--------|
| 企业知识库检索 | RedClaw 知识库 → Pocket 查询 | P0 |
| 多租户治理 | 身份同步 + 权限校验 | P0 |
| 审计日志 | 操作审计 + 上报 | P1 |
| 成本计量 | 企业版计费 | P1 |
| iOS 应用开发 | Capacitor 编译 iOS | P0 |
| 性能优化 | 启动速度、内存、网络 | P0 |
| 稳定性 | 错误处理、重连、数据一致性 | P0 |
| 文档 & 测试 | 完整文档 + 自动化测试 | P0 |

**交付物：** v1.0 正式版

### 6.2 新增文件结构

#### Pocket Backend 新增模块

```
backend/internal/
├── meeting/                    # 会议录音及总结
│   ├── recorder.go             # 录音管理
│   ├── recorder_test.go
│   ├── transcriber.go          # STT 转写
│   ├── transcriber_test.go
│   ├── summarizer.go           # AI 总结
│   ├── summarizer_test.go
│   ├── store.go                # 会议记录存储
│   └── store_test.go
├── chat_summary/               # 聊天记录总结
│   ├── aggregator.go           # 消息聚合
│   ├── aggregator_test.go
│   ├── summarizer.go           # 摘要生成
│   └── summarizer_test.go
├── presentation/               # 产品方案 & PPT
│   ├── generator.go            # 方案生成
│   ├── generator_test.go
│   ├── renderer.go             # PPT 渲染 (HTML → PDF)
│   ├── renderer_test.go
│   ├── templates/              # 模板目录
│   │   ├── prd.md              # PRD 模板
│   │   ├── tech-spec.md        # 技术方案模板
│   │   ├── weekly.md           # 周报模板
│   │   └── quarterly.md        # 季度总结模板
│   └── store.go
├── notes/                      # 已有，升级
│   ├── store.go                # 已有
│   ├── classifier.go           # AI 分类 (新增)
│   ├── classifier_test.go
│   ├── recommender.go          # 智能推荐 (新增)
│   └── recommender_test.go
├── finance/                    # 记账
│   ├── store.go                # 记账存储
│   ├── store_test.go
│   ├── recognizer.go           # 语音识别记账
│   ├── recognizer_test.go
│   ├── stats.go                # 统计报表
│   └── stats_test.go
├── redclaw/                    # RedClaw 集成 (新增)
│   ├── client.go               # RedClaw API 客户端
│   ├── client_test.go
│   ├── bridge.go               # 桥接服务
│   ├── bridge_test.go
│   └── auth.go                 # 多租户身份同步
└── server/
    ├── server_meeting.go       # 会议 API 路由 (新增)
    ├── server_chat_summary.go  # 聊天总结 API (新增)
    ├── server_presentation.go  # 方案/PPT API (新增)
    ├── server_finance.go       # 记账 API (新增)
    └── server_redclaw.go       # RedClaw 集成 API (新增)
```

#### Mobile App 新增页面

```
frontend/src/
├── views/
│   ├── MeetingRoom.vue         # 会议录音室页面
│   ├── MeetingDetail.vue       # 会议详情/总结页
│   ├── ChatSummary.vue         # 聊天总结页
│   ├── Presentation.vue        # 方案/PPT 生成页
│   ├── NotesSpace.vue          # 笔记空间页
│   ├── NoteEditor.vue          # 笔记编辑页
│   ├── Finance.vue             # 记账首页
│   ├── FinanceStats.vue        # 记账统计页
│   └── RedClawSettings.vue     # 企业集成设置页
├── components/
│   ├── DiffViewer.vue          # 代码 Diff 展示组件
│   ├── VoiceRecorder.vue       # 语音录音组件
│   ├── MeetingSummary.vue      # 会议总结卡片组件
│   ├── NoteCard.vue            # 笔记卡片组件
│   ├── FinanceChart.vue        # 记账图表组件
│   └── FinanceForm.vue         # 记账表单组件
└── router/
    └── index.ts                # 路由表 (新增页面路由)
```

### 6.3 关键决策点

| 决策 | 选项 | 推荐 | 理由 |
|------|------|------|------|
| 移动端框架 | Vue 3 + Capacitor vs Flutter vs React Native | **保持 Vue 3 + Capacitor** | 已有大量代码，避免重写 |
| iOS 支持 | 同一套代码编译 vs 原生开发 | **Capacitor 编译 iOS** | 最快路径，v1.0 后考虑原生优化 |
| 会议录音处理 | 云端 STT vs 本地 STT | **云端为主 + 本地离线兜底** | 云端精度高，本地保证可用性 |
| 记账数据存储 | 本地 SQLite vs 云端同步 | **本地优先 + 可选云同步** | 隐私敏感，用户控制 |
| PPT 生成方案 | HTML 转 PDF vs 调用 Office API | **HTML 渲染 + 截图/PDF** | 无外部依赖，跨平台 |
| RedClaw 通讯 | REST vs gRPC vs 消息队列 | **REST API + WebSocket** | 简单可靠，WebSocket 保证实时性 |
| 搜索方案 | SQLite FTS vs pgvector | **两者结合** | FTS 全文搜索 + pgvector 语义搜索 |
| 语音识别 | 云端 API vs 本地模型 | **云端 API (首选) + 本地 Vosk (兜底)** | 精度与离线可用性兼顾 |

---

## 7. 数据模型设计

### 7.1 会议记录 (Meeting)

```go
type Meeting struct {
    ID          string    `json:"id"`           // UUID
    Title       string    `json:"title"`         // 会议标题
    Duration    int       `json:"duration"`      // 时长（秒）
    Recordings  []string  `json:"recordings"`    // 录音文件路径列表
    Transcript  string    `json:"transcript"`    // 转写文本
    Summary     string    `json:"summary"`       // AI 总结
    KeyDecisions []string `json:"key_decisions"` // 关键决策
    ActionItems []TaskRef `json:"action_items"`  // 待办事项引用
    Tags        []string  `json:"tags"`          // 标签
    ProjectID   string    `json:"project_id"`    // 关联项目
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
}
```

### 7.2 聊天摘要 (ChatSummary)

```go
type ChatSummary struct {
    ID          string    `json:"id"`
    Channel     string    `json:"channel"`      // feishu / telegram / slack
    ChannelID   string    `json:"channel_id"`   // 群组/频道 ID
    PeriodStart time.Time `json:"period_start"` // 总结时间范围
    PeriodEnd   time.Time `json:"period_end"`
    MessageCount int      `json:"message_count"`
    Summary     string    `json:"summary"`
    KeyDecisions []string `json:"key_decisions"`
    ActionItems  []TaskRef `json:"action_items"`
    Links       []string  `json:"links"`
    CreatedAt   time.Time `json:"created_at"`
}
```

### 7.3 产品方案 (Presentation)

```go
type Presentation struct {
    ID          string    `json:"id"`
    Title       string    `json:"title"`
    Type        string    `json:"type"`         // prd / tech-spec / weekly / quarterly
    Content     string    `json:"content"`      // Markdown 方案内容
    Slides      []Slide   `json:"slides"`       // PPT 分页
    Status      string    `json:"status"`       // draft / completed / archived
    Tags        []string  `json:"tags"`
    ProjectID   string    `json:"project_id"`
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
}

type Slide struct {
    Title   string `json:"title"`
    Content string `json:"content"`  // HTML 片段
    Note    string `json:"note"`     // 演讲备注
}
```

### 7.4 笔记 (Note) — 升级

```go
type Note struct {
    ID          string    `json:"id"`
    Content     string    `json:"content"`      // Markdown
    Type        string    `json:"type"`         // tech / meeting / todo / idea / product / learning
    Source      string    `json:"source"`       // manual / voice / import / auto
    Tags        []string  `json:"tags"`
    ProjectID   string    `json:"project_id"`
    RelatedIDs  []string  `json:"related_ids"`  // 关联笔记/任务/会话 ID
    Embedding   []float32 `json:"-"`            // pgvector 向量 (不序列化到 JSON)
    CreatedAt   time.Time `json:"created_at"`
    UpdatedAt   time.Time `json:"updated_at"`
}
```

### 7.5 记账 (Transaction)

```go
type Transaction struct {
    ID          string    `json:"id"`
    Type        string    `json:"type"`         // income / expense
    Amount      float64   `json:"amount"`
    Category    string    `json:"category"`     // 餐饮 / 交通 / 购物 / 工资 / 项目收入 / 其他
    Note        string    `json:"note"`
    Tags        []string  `json:"tags"`
    ProjectID   string    `json:"project_id"`   // 可选关联项目
    Source      string    `json:"source"`       // manual / voice / auto
    CreatedAt   time.Time `json:"created_at"`
}

type Budget struct {
    ID        string    `json:"id"`
    Category  string    `json:"category"`
    Month     string    `json:"month"`       // "2026-07"
    Limit     float64   `json:"limit"`
    Spent     float64   `json:"spent"`       // 计算字段
    AlertAt   float64   `json:"alert_at"`    // 达到多少百分比时提醒 (如 80)
}
```

---

## 8. 安全与隐私

| 数据类别 | 存储策略 | 传输加密 | 访问控制 |
|---------|---------|---------|---------|
| 录音文件 | 本地加密存储 | TLS | 仅 App 可访问 |
| 转写文本 | 本地 + 可选云端 | TLS | 用户控制 |
| 记账数据 | 本地 SQLite | N/A (本地) | 应用锁 |
| 笔记 | 本地 + 可选云同步 | TLS | 用户控制 + 应用锁 |
| 会话数据 | 本地 + 可选 RedClaw | WSS | JWT 认证 |
| 密码库 | 加密存储 (Vault) | TLS | 主密码 + 生物识别 |
| 企业数据 | 通过 RedClaw 审计 | HTTPS | 多租户隔离 |

---

## 9. 测试策略

| 测试类型 | 覆盖范围 | 工具 |
|---------|---------|------|
| 单元测试 | 所有新增模块 | Go testing + testify |
| 集成测试 | 模块间交互 + RedClaw 通讯 | Go httptest + testcontainers |
| E2E 测试 | 完整用户场景 | Playwright (Web) + Appium (Mobile) |
| 语音测试 | STT 准确率、不同口音 | 测试录音集 |
| 性能测试 | 启动时间、API 延迟、内存 | pprof + k6 |
| 安全测试 | JWT 绕过、数据泄露、XSS | OWASP ZAP + 手动审计 |

---

## 10. 里程碑与交付物

| 里程碑 | 时间 | 交付物 |
|--------|------|--------|
| P1 基础集成完成 | 2-3 周 | Pocket ↔ RedClaw 通讯链路打通 |
| P2 核心功能完成 | 5-7 周 | AI 编程升级 + 会议总结 + 聊天总结 |
| P3 智能工具完成 | 8-11 周 | 方案/PPT + 笔记分类 + 语音记账 |
| P4 企业集成完成 | 12-15 周 | 企业版 + iOS + 性能优化 |
| **v1.0 正式发布** | **~15 周** | 完整产品 + 文档 + 自动化测试 |

---

*本设计文档由 OpenCode Supreme 团队编写，2026-07-24*