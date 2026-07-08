# OpenCode Pocket — 最终功能总结与流程图

## 一、项目概述

OpenCode Pocket 是一个移动端 OpenCode/ZCode 实例管理平台，核心能力：
- **实例感知**：自动发现本机/网络的 OpenCode 实例
- **会话管理**：拉取真实会话、消息、状态
- **跨主机迁移**：将会话从一台主机迁移到另一台，带续接提示词
- **ACC 集成**：让 ACC 统一编排 OpenCode 实例后台执行任务

---

## 二、核心架构图

```
┌──────────────────────────────────────────────────────────────────┐
│                        ACC (Agent Control Center)                 │
│  ┌─────────────────────────────────────────────────────────────┐ │
│  │  Runtime Adapter Registry                                    │ │
│  │  k8s │ openclaw │ hermes │ opencode/zcode ← 本次新增        │ │
│  └──────────────────────────┬──────────────────────────────────┘ │
└─────────────────────────────┼─────────────────────────────────────┘
                              │ dispatch(agentId, {prompt, workDir})
                              ▼
┌──────────────────────────────────────────────────────────────────┐
│                     Pocket Backend (Go, :8088)                    │
│                                                                   │
│  ┌─────────────┐  ┌──────────────┐  ┌────────────────────────┐  │
│  │ Registry    │  │ Migration    │  │ Dispatch               │  │
│  │ + Discovery │  │ Service      │  │ /api/opencode/dispatch │  │
│  │             │  │              │  │  CreateSession +       │  │
│  │ 自动扫描    │  │ Preview +    │  │  SendPrompt            │  │
│  │ :4096       │  │ Migrate +    │  └────────────────────────┘  │
│  │ /global/    │  │ Complete     │                              │
│  │  health     │  │              │  ┌────────────────────────┐  │
│  └──────┬──────┘  └──────┬───────┘  │ REST API               │  │
│         │                │           │ /api/instances         │  │
│         │                │           │ /api/sessions          │  │
│         │                │           │ /api/migration/preview │  │
│         │                │           │ /api/tasks             │  │
│         │                │           └────────────────────────┘  │
└─────────┼────────────────┼───────────────────────────────────────┘
          │                │
          ▼                ▼
┌──────────────────────────────────────────────────────────────────┐
│              OpenCode / ZCode Instances (:4096)                  │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │  discovered-local-4096  (OpenCode v1.14.33)               │  │
│  │  GET  /global/health  → {healthy, version}                │  │
│  │  GET  /session        → [SessionInfo]                     │  │
│  │  GET  /session/:id/message → [{info, parts}]              │  │
│  │  POST /session        → 创建会话                           │  │
│  │  POST /session/:id/message → 发送prompt                   │  │
│  │  GET  /event          → SSE 实时事件流                     │  │
│  │  数据: ~/.local/share/opencode/opencode.db (SQLite)       │  │
│  └────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────┘
```

---

## 三、核心流程图

### 流程 1：实例自动发现

```
Pocket Discovery Worker（每 60 秒）
    │
    ├─→ 扫描端口 [4096, 14096, 3000, 8080]
    │     对每个 host:port 并发探测（50 并发限流）
    │
    ├─→ GET http://{host}:{port}/global/health
    │     超时 500ms
    │     期望: {"healthy": true, "version": "1.14.33"}
    │
    ├─→ 命中 → Registry.RegisterInstance()
    │     ├── id = "discovered-{host}-{port}"
    │     ├── version = "1.14.33"
    │     ├── origin = "discovered"
    │     └── health = "healthy"
    │
    └─→ 未命中 → 标记已有实例为 offline（不删除）
```

**验证结果**：
```
✅ discovered-local-4096    | v1.14.33 | healthy | discovered
✅ discovered-localhost-4096| v1.14.33 | healthy | discovered
```

---

### 流程 2：ACC 调度执行（dispatch）

```
ACC Runtime Adapter
    │
    │  dispatch(agentId, {prompt, workingDir, agent, model})
    │
    ├─→ [优先] POST Pocket /api/opencode/dispatch
    │     │
    │     ├─→ Registry.GetInstanceAPIBase(agentId)
    │     │     → "http://127.0.0.1:4096"
    │     │
    │     ├─→ CreateSession(baseURL, {agent, model, location})
    │     │     POST http://127.0.0.1:4096/session
    │     │     → {id: "ses_0bf51e4f9..."}
    │     │
    │     ├─→ SendPrompt(baseURL, sessionId, {text: prompt})
    │     │     POST http://127.0.0.1:4096/session/{id}/message
    │     │     → {enqueued: true}
    │     │
    │     └─→ 返回 {accepted: true, sessionId: "ses_xxx"}
    │
    └─→ [降级] enqueue task_bus（Pocket 不可用时）
          INSERT INTO task_bus (runtime_type='opencode', state='inbox')
          → 返回 {accepted: true, via: 'task_bus'}
```

**验证结果**：
```
✅ ACC dispatch → Pocket → OpenCode
✅ 真实创建新会话: ses_0bf51e4f9ffeAjtAJLvLyhWaW6
```

---

### 流程 3：会话跨主机迁移

```
用户/ACC 发起迁移
    │
    │  POST /api/migration/preview
    │  {fromInstanceId, sessionId, promptTemplates}
    │
    ├─→ 从源实例拉取会话数据
    │     ├─ GET /session/{id} → 标题、目录
    │     └─ GET /session/{id}/message?limit=50 → 消息流
    │         每条消息: {info:{role}, parts:[{type, text}]}
    │
    ├─→ 组装迁移包 (SessionResumeBrief)
    │     ├─ currentState: 最后一条 assistant 文本（≤500字符）
    │     ├─ nextAction: 末尾 200 字符
    │     ├─ turnCount: 消息数
    │     └─ title, summary
    │
    ├─→ 拼接 4 类提示词模板
    │     ├─ env_sync: "进入工作目录 → git status → 检查依赖"
    │     ├─ task_resume: "当前状态：xxx → 下一步：xxx"
    │     ├─ result_verify: "验证上次产物文件是否存在"
    │     └─ acc_report: "完成后调 acc_task_complete"
    │
    └─→ 返回 {pack, prompt, turnsMigrated}
```

```
用户确认迁移
    │
    │  POST /api/migration
    │  {fromInstanceId, sessionId, toInstanceId?, taskId?}
    │
    ├─→ Preview（组装迁移包 + 提示词）
    │
    ├─→ 选择目标实例
    │     ├─ 健康 = healthy
    │     ├─ 负载最低（ActiveSessions 最少）
    │     └─ origin 优先级: registered > discovered > static
    │
    ├─→ 下发迁移命令
    │     PluginHub.SendCommandToInstance(toInstance, {
    │       type: "session.migrate_to",
    │       payload: {promptText, packInline, workingDir}
    │     })
    │
    └─→ 记录逻辑映射
          task_session_links:
          ├── (taskID, fromInst, fromSession, role='migrated_from')
          └── (taskID, toInst,   newSession,  role='migrated_to')
```

**验证结果**：
```
✅ 源会话: ses_0db6a767dffeHVLLw6iYIesF17 (50轮消息)
✅ currentState: "## ✅ 任务完成总结\n### 提交信息..."
✅ nextAction: "...安全修复（fetcher.go...）..."
✅ 提示词: 766 字符（env_sync + task_resume）
```

---

## 四、审计修正记录

### 审计发现 9 个问题，全部修复

| # | 严重度 | 问题 | 文件 | 修复 |
|---|--------|------|------|------|
| 1 | **致命** | opencodeMessage JSON 解析错误（Data 永远空） | adapter/opencode_http.go | 自定义 UnmarshalJSON |
| 2 | **致命** | adapter 测试 mock 路径全错（7个 FAIL） | adapter/opencode_http_test.go | 修正 mock 路径 |
| 3 | **高** | GetSessionMessages 不支持裸数组响应 | adapter/opencode_http.go | 双格式兼容 |
| 4 | **高** | /api/question/request 路径错（404） | adapter/opencode_http.go:849 | → /question/request |
| 5 | **高** | /api/event 路径错（返回 HTML） | adapter/opencode_http.go:975 | → /event |
| 6 | **高** | migration 路由丢失（404） | server/server.go | 恢复注册 |
| 7 | **中** | SessionResumeBrief 缺 Summary 字段 | model/model.go | 新增字段 |
| 8 | **中** | dispatch 未校验 info.ID 空 | server_opencode_dispatch.go | 加空值检查 |
| 9 | **中** | fetchMessagesRaw 重复造轮子 | migration/service.go | 复用 adapter.GetMessages |

---

## 五、功能测试结果

### API 功能测试（全部通过）

```
=== 1. 登录测试 ===
✅ 登录成功 (token: eyJhbGciOiJIUzI1NiIsIn...)

=== 2. 实例发现测试 ===
✅ 发现 2 个实例:
   - discovered-local-4096 | v1.14.33 | healthy | discovered
   - discovered-localhost-4096 | v1.14.33 | healthy | discovered

=== 3. 会话列表测试 ===
✅ 获取到 9 个会话:
   - ses_0bf51e4f9... | New session - 2026-07-08T07:42:59
   - ses_0bf5514c3... | New session - 2026-07-08T07:39:31
   - ses_0bf5545af... | New session - 2026-07-08T07:39:18

=== 4. 迁移预览测试 ===
✅ 迁移预览成功
   会话: ses_0db6a767dffeHVLLw6iYIesF17
   轮次: 50
   当前状态: ## ✅ 任务完成总结...
   下一步: ...安全修复（fetcher.go...
   提示词长度: 766 字符

=== 5. 任务列表测试 ===
✅ 任务列表为空（正常，无PostgreSQL）

=== 6. 健康检查 ===
✅ 后端健康: ok
```

### 编译与测试

```
✅ go build ./...          — 编译通过
✅ go vet ./internal/...   — 静态分析通过
✅ go test ./internal/adapter/ — 7个测试全部通过
```

---

## 六、提交记录

| 提交 | 说明 |
|------|------|
| `43a9173` | fix(critical): 修正3个致命bug + 恢复migration路由 |
| `034a880` | fix(audit): 修正6个审计发现的问题 |
| `8ad1271` | feat: 补全Phase1-3遗漏的核心代码 |
| `25e5afe` | fix: 提交遗漏的Phase1核心源码 |
| `7d49a81` | docs: Phase 4 最终交付总结 |
| `d51e141` | feat(runtime): 新增OpenCode/ZCode runtime adapter (ACC) |

---

## 七、本地部署指南

### 1. 启动 OpenCode 实例

```bash
cd /path/to/project
opencode serve --port 4096 --hostname 127.0.0.1
# 验证: curl http://127.0.0.1:4096/global/health
```

### 2. 启动 Pocket 后端

```bash
cd opencode-pocket/backend
export JWT_SECRET=your-secret
export POCKET_DEV_AUTH=true
export POCKET_AUTH_USER=admin
export POCKET_AUTH_PASS=admin
export POCKET_HTTP_PORT=8088
export POCKET_DISCOVERY_PORTS=4096
./pocketd
```

### 3. 启动前端

```bash
cd opencode-pocket/frontend
npm run dev
# 访问 http://localhost:4174
# 登录: admin / admin
```

### 4. 启动 ACC（可选）

```bash
cd agent-control-center
export POCKET_BASE_URL=http://127.0.0.1:8088
export POCKET_API_TOKEN=<JWT token>
npm start
```

---

**文档日期**: 2026-07-09  
**版本**: v1.0.0-audited
