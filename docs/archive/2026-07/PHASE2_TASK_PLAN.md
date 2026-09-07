# ACP 通用化改造 Phase 2 任务计划

## 1. 服务现状检查

### 1.1 服务状态总览

| 服务 | 状态 | 端口 | 备注 |
|------|------|------|------|
| **smm-postgres** | ✅ healthy | 5432 | 有 `kaixuan` 和 `pocket_local` 数据库 |
| **kxmemory-go-local** | ⚠️ Restarting | 8080 | 启动失败：需要 `postgres` 主机名的 Docker 网络 |
| **kxmemory-go-pyroscope** | ✅ Up | 4040 | 监控 |
| **kxmemory-go-prometheus** | ✅ Up | 9090 | 监控 |
| **kxmemory-go-grafana** | ✅ Up | 3000 | 可视化 |
| **pocketd** | ❌ 未运行 | 9088 | server 包有 WIP 编译错误 |
| **agent_echo** | ✅ Built | - | 测试用 fake agent |

### 1.2 关键发现

#### kxmemory-go 网络问题
```
ERROR ping postgres: failed to connect to `user=kxuser database=kaixuan`:
hostname resolving error: lookup postgres on 127.0.0.11:53: no such host
```

**根因**: kxmemory-go-local 容器配置 `POSTGRES_HOST=postgres`，但它不在 kxmemory-go 自己的 Docker 网络中（与其他 kxmemory-go-* 容器共享），所以 DNS 解析 `postgres` 失败。

**解决**:
- 选项 A: 将 kxmemory-go-local 加入有 `postgres` 主机的网络
- 选项 B: 修改容器环境变量 `POSTGRES_HOST=smm-postgres`（但不在同一网络）
- 选项 C: 在 pocketd 端禁用 kxmemory（remote-only 模式）

#### pocketd 编译错误
```
internal/server/server.go:959:14: s.claimsFromContext undefined
internal/server/server.go:980:15: undefined: ws.NewClientWithIdentity
internal/server/server_assistant.go:191:11: s.wsHub.BroadcastTo undefined
```

**根因**: 工作区有其他 session 的 WIP 修改（stash@{1}），缺少 `BroadcastTo` 等方法。

**影响**: **不影响 ACP 框架本身**（`internal/agent/` 独立编译通过）。

---

## 2. 引用产品服务的研发情况

### 2.1 kxmemory-go (本任务核心依赖)

**产品状态**: 
- ✅ 框架完整 (`internal/agent/`, 4,790 行新代码)
- ✅ 编译通过（独立模块）
- ⚠️ 本地 kxmemory-go 容器启动失败（基础设施问题）
- ✅ Mock agent_echo 可完全替代进行测试

**已知依赖**:
- PostgreSQL (kaixuan DB) — ✅ 已有
- kxmemory-go service — ⚠️ 网络问题

**研发方向**:
- [x] ACP 通用化（已完成 Phase 1+1.5）
- [ ] SubscribeEvents 流式响应
- [ ] Permission/Question capability
- [ ] 多 transport 集成测试

### 2.2 pocketd (主项目)

**产品状态**:
- ✅ 核心框架稳定
- ✅ 9 个后端 API TODO 全部补全
- ✅ OpenCode 集成完整（HTTP API + WebSocket）
- ⚠️ ACP 集成待 server 编译修复后端到端验证
- ❌ 真实 ACP agent（Codex/Claude）未实现

**研发方向**:
- [x] ACP 框架集成（已完成）
- [ ] handler 迁移到 AgentAdapter
- [ ] 前端适配新 API

### 2.3 opencode-manager (新)

**产品状态**:
- 🆕 新产品线
- 概念：OpenCode 的管理平面
- 待需求明确

### 2.4 ACC (Adaptive Code Composer)

**产品状态**:
- ⚠️ MCP 客户端集成（待启用）
- ⚠️ 任务同步待配置

---

## 3. Phase 2 任务列表

### 任务 2.1: 修复 pocketd 编译（优先级 P0）

**目标**: 让 `go build ./...` 完全通过，验证 ACP 集成可端到端工作

**步骤**:
1. 暂存 WIP 修改（`git stash`）
2. 重新构建 pocketd
3. 验证 `/api/diagnostics/agents` 端点工作
4. 提交修复

**预估**: 1-2 小时

---

### 任务 2.2: 实现 SubscribeEvents 流式响应（优先级 P1）

**目标**: 完整支持 ACP `session/update` notification 流式推送

**步骤**:
1. 修改 `ACPStdioAdapter.SubscribeEvents`:
   - 启动 goroutine 读取 transport.Recv()
   - 解析 `session/update` notifications
   - 转换为 `AgentEvent` 推送到 channel
2. 添加 event 类型映射（tool_call, message_chunk, plan）
3. 实现流式消息累积（v2 协议）
4. 单测：模拟 agent 输出流验证事件正确性

**预估**: 4-6 小时（200 行代码 + 150 行测试）

---

### 任务 2.3: 实现 Permission/Question Capability（优先级 P1）

**目标**: 让 ACPStdioAdapter 实现 `PermissionCapable` + `QuestionCapable`

**步骤**:
1. 添加 `ListPendingPermissions` / `ReplyPermission` 方法
2. 添加 `ListPendingQuestions` / `ReplyQuestion` / `RejectQuestion` 方法
3. 实现 ACP `session/request_permission` 协议映射
4. 单测覆盖

**预估**: 3-4 小时

---

### 任务 2.4: 修复 kxmemory-go 容器网络（优先级 P2）

**目标**: 让本地 kxmemory-go 启动成功

**步骤**:
1. 创建 `kxmemory-go-network` Docker 网络
2. 将 `smm-postgres` 加入该网络
3. 将 `kxmemory-go-local` 容器加入该网络
4. 重启 kxmemory-go-local，验证

**预估**: 30 分钟

**命令**:
```bash
docker network create kxmemory-go-network
docker network connect kxmemory-go-network smm-postgres
docker network connect kxmemory-go-network kxmemory-go-local
docker restart kxmemory-go-local
```

---

### 任务 2.5: 真实 Claude CLI 集成测试（优先级 P1）

**目标**: 用本地 Claude CLI 验证完整 ACP 流程

**前置条件**:
- Claude CLI 已安装（✅ `/Users/xutaohuang/.local/bin/claude`）
- 需 `claude login` 才能调用

**步骤**:
1. 运行 `claude login`（需用户交互）
2. 配置 `CLAUDE_CLI_PATH=/Users/xutaohuang/.local/bin/claude`
3. 启动 pocketd + Claude CLI 集成
4. 验证 session/prompt 流

**预估**: 1-2 小时（含登录）

---

### 任务 2.6: Handler 迁移到 AgentAdapter（优先级 P1）

**目标**: 把 18+ 处 `s.opencode.X()` 调用改为 `s.agents.Get(ref).X()`

**步骤**:
1. 创建 helper `s.agentForRequest(r)` 根据 `instance_id` 选 adapter
2. 修改 `mobile_session_handler.go`, `server_opencode*.go` 等
3. 保持向后兼容（instance_id 仍工作）
4. E2E 测试

**预估**: 6-8 小时（~300 行修改 + 测试）

---

### 任务 2.7: OpenCode `acp` HTTP 适配（优先级 P2）

**目标**: 用 HTTPTransport 连接 OpenCode `acp` WebSocket 端点

**发现**: OpenCode `acp --port 14096` 启动的是 WebSocket + HTTP 服务

**步骤**:
1. 用浏览器 DevTools 抓取 OpenCode Web UI 与后端的 WebSocket 通信
2. 识别真实的 JSON-RPC 端点（可能是 `/api/agents/default/session` 等）
3. 实现 `WSTransport` 客户端连接 OpenCode
4. 验证 session/prompt 通过 WebSocket 工作

**预估**: 4-6 小时

---

## 4. 优先级矩阵

| 任务 | 优先级 | 工作量 | 风险 | 价值 |
|------|--------|--------|------|------|
| 2.1 修复 pocketd 编译 | P0 | 1-2h | 低 | 高（解锁端到端测试）|
| 2.2 SubscribeEvents | P1 | 4-6h | 中 | 高（核心功能）|
| 2.3 Permission/Question | P1 | 3-4h | 低 | 中（capability 完整）|
| 2.4 修复 kxmemory-go 网络 | P2 | 0.5h | 低 | 中（解锁集成测试）|
| 2.5 真实 Claude CLI | P1 | 1-2h | 中 | 高（生产验证）|
| 2.6 Handler 迁移 | P1 | 6-8h | 高 | 高（完整迁移）|
| 2.7 OpenCode WebSocket | P2 | 4-6h | 中 | 中（OpenCode 集成）|

**总工作量**: 约 20-30 小时（1-2 周）

---

## 5. 建议执行顺序

### Week 1 (1-2 天)
- ✅ 2.1 修复 pocketd 编译（解锁所有测试）
- ✅ 2.4 修复 kxmemory-go 网络（解锁集成测试）

### Week 2 (2-3 天)
- ✅ 2.2 SubscribeEvents 流式响应
- ✅ 2.3 Permission/Question capability

### Week 3 (3-5 天)
- ✅ 2.5 真实 Claude CLI 集成
- ✅ 2.7 OpenCode WebSocket 适配

### Week 4 (1-2 天)
- ✅ 2.6 Handler 完整迁移
- ✅ E2E 测试 + 文档

---

## 6. 成功标准

### 6.1 功能完成
- [ ] 真实 Claude CLI 通过 ACP 协议工作
- [ ] SubscribeEvents 流式响应延迟 <100ms
- [ ] Permission/Question 端到端流程
- [ ] Handler 全部迁移到新接口
- [ ] 前端无破坏性变更

### 6.2 质量
- [ ] `go build ./...` 通过
- [ ] `go test ./...` 通过
- [ ] `go test -race` 无 data race
- [ ] 真实 E2E 测试通过
- [ ] 文档完整

### 6.3 部署
- [ ] pocketd 配置文件示例（启用 Claude）
- [ ] Docker compose 包含 kxmemory-go
- [ ] 部署文档更新

---

## 7. 不在 Phase 2 范围

- ❌ OpenCode Manager 产品线（待需求）
- ❌ ACC MCP 集成（待启用）
- ❌ 完整 server handler 迁移（Phase 3）
- ❌ 前端 SPA 适配（Phase 3）

---

**任务计划生成时间**: 2026-07-23 02:30 UTC+8
**当前状态**: Phase 1 + 1.5 已完成推送
**下一阶段**: Phase 2（SubscribeEvents + Capability + 集成）
