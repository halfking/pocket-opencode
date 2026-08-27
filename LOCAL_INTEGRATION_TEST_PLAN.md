> **STATUS: superseded** (2026-08-27)
> Evidence level at supersede time: `claimed (unverified)`
> Superseded by: [`docs/新架构v1/03-roadmap/里程碑.md`](docs/新架构v1/03-roadmap/里程碑.md)
> Do NOT use this doc for current implementation decisions.
> This doc is a historical plan/analysis from a completed sprint; 规划以 v3 里程碑为准。

# OpenCode Pocket 本地集成测试方案

**测试日期**: 2026-07-03  
**测试范围**: opencode-pocket + memora + acc + llm-gateway-go 完整集成

## 一、系统架构概览

```text
┌─────────────────────────────────────────────────────────────────┐
│                     OpenCode Pocket 前端                          │
│              (Vue 3 移动端控制面板 - :5173)                        │
└────────────────────────┬────────────────────────────────────────┘
                         │ HTTP/WebSocket
┌────────────────────────▼────────────────────────────────────────┐
│                 OpenCode Pocket 后端 (:8088)                      │
│  ┌──────────┬──────────┬──────────┬──────────┬──────────┐       │
│  │ 任务管理 │ 笔记存储 │ 邮箱集成 │ 飞书回调 │ AI网关   │       │
│  └────┬─────┴────┬─────┴────┬─────┴────┬─────┴────┬─────┘       │
└───────┼──────────┼──────────┼──────────┼──────────┼─────────────┘
        │          │          │          │          │
        │          │          │          │          │
┌───────▼──────┐ ┌─▼────────┐ ┌─────────▼──┐ ┌─────▼────────┐
│   ACC MCP    │ │ PostgreSQL│ │  Memora    │ │ llm-gateway  │
│  任务系统    │ │   数据库  │ │ 嵌入/LLM   │ │   -go        │
│ (mcp端点)    │ └───────────┘ │   服务     │ │  企业网关    │
└──────────────┘               └────────────┘ └──────────────┘
        │
┌───────▼──────────────────────────────────────────────────────┐
│              OpenCode 实例（被管理的开发会话）                  │
│          (可以是本地或远程的 OpenCode 服务)                     │
└──────────────────────────────────────────────────────────────┘
```

## 二、各服务职责与端口分配

| 服务名称 | 职责 | 默认端口 | 必需性 |
|---------|------|---------|--------|
| **opencode-pocket-backend** | 核心 API、任务管理、笔记、邮箱 | 8088 | ✅ 必需 |
| **opencode-pocket-frontend** | 移动端控制面板 UI | 5173 (dev) | ✅ 必需 |
| **PostgreSQL** | 数据持久化 | 5432 | ✅ 必需 |
| **memora (kxmemory)** | 嵌入向量 + LLM 服务 | 8000 | ⚠️ 可选* |
| **acc-mcp** | 任务系统 MCP 端点 | 按实际部署 | ⚠️ 可选* |
| **llm-gateway-go** | 企业 LLM 流量治理 | 按实际部署 | ⚠️ 可选* |
| **OpenCode 实例** | 被管理的开发会话 | 3000+ | 🔧 测试需要 |

> *可选服务：根据测试场景选择性启动

## 三、环境准备清单

### 3.1 基础环境

```bash
# 1. 检查必需工具版本
go version       # 需要 >= 1.22
node --version   # 需要 >= 18
psql --version   # PostgreSQL 客户端

# 2. 检查服务目录结构
ls -la /Users/xutaohuang/workspace/official-deploy/services/
# 应包含：opencode-pocket, llm-gateway-go, acc-toolkit 等
```

### 3.2 PostgreSQL 准备

```bash
# 启动 PostgreSQL（如使用 Homebrew）
brew services start postgresql@14

# 创建测试数据库
createdb pocket_test

# 验证连接
psql pocket_test -c "SELECT version();"
```

### 3.3 环境变量配置文件

创建 `.env.local` 用于本地测试（基于 `.env.example`）：

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
cp .env.example .env.local
```

编辑 `.env.local`（核心配置项）：

```bash
# ---- 核心配置 ----
POCKET_HTTP_PORT=8088
POCKET_POSTGRES_DSN=postgresql://your_user:your_pass@localhost:5432/pocket_test?sslmode=disable

# ---- 认证（开发模式）----
POCKET_JWT_SECRET=local-test-secret-please-change-in-production
POCKET_DEV_AUTH=true
POCKET_AUTH_USER=admin
POCKET_AUTH_PASS=admin

# ---- Phase 0: 个人助理模块 ----
# 邮箱加密密钥（32字节十六进制）
POCKET_EMAIL_MASTER_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef

# ---- 集成服务（按需配置）----
# Memora/kxmemory
POCKET_KXMEMORY_BASE_URL=http://localhost:8000
POCKET_GROQ_API_KEY=<your_groq_key_if_needed>

# ACC MCP
POCKET_MCP_BASE_URL=https://mcp.kxpms.cn/acc/mcp
POCKET_MCP_API_KEY=<your_acc_mcp_key>
POCKET_MCP_INSECURE_TLS=false

# LLM Gateway
POCKET_LLM_GATEWAY_URL=http://localhost:8080
POCKET_LLM_GATEWAY_API_KEY=<your_gateway_key>

# OpenCode 实例目录（示例）
POCKET_INSTANCE_CATALOG_JSON='[{"id":"local-dev","displayName":"Local Dev","apiBaseURL":"http://localhost:3000","environment":"development"}]'

# ---- Phase C: AI 网关配置 ----
POCKET_EMBED_BASE_URL=https://api.openai.com/v1
POCKET_EMBED_API_KEY=<your_openai_key>
POCKET_EMBED_MODEL=text-embedding-3-small

POCKET_LLM_BASE_URL=https://api.groq.com/openai/v1
POCKET_LLM_API_KEY=<your_groq_key>
POCKET_LLM_MODEL=llama-3.3-70b-versatile
```

## 四、服务启动步骤

### 测试场景分级

#### 🟢 Level 1: 核心功能测试（最小依赖）
- **目标**: 验证 opencode-pocket 基础功能
- **依赖**: PostgreSQL + 本地 SQLite + OpenCode 实例
- **测试内容**: 任务 CRUD、实例管理、会话列表

#### 🟡 Level 2: AI 功能测试
- **目标**: 验证笔记分类、摘要、嵌入功能
- **依赖**: Level 1 + memora
- **测试内容**: 笔记智能分类、语音转文字、相似度搜索

#### 🟠 Level 3: 任务系统集成测试
- **目标**: 验证与 ACC 任务系统的对接
- **依赖**: Level 1 + ACC MCP
- **测试内容**: 任务同步、状态更新、任务关联

#### 🔴 Level 4: 完整生产环境模拟
- **目标**: 验证所有服务完整集成
- **依赖**: Level 1 + memora + ACC + llm-gateway-go
- **测试内容**: 端到端工作流、流量治理、监控

---

### 4.1 Level 1: 核心功能测试

#### Step 1: 启动 PostgreSQL
```bash
# 如已在 3.2 启动则跳过
brew services start postgresql@14
```

#### Step 2: 启动 OpenCode Pocket 后端
```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend

# 加载环境变量
export $(cat ../.env.local | grep -v '^#' | xargs)

# 运行数据库迁移（如有）
# go run cmd/migrate/main.go up

# 启动后端
go run cmd/pocketd/main.go
```

**验证后端启动成功**:
```bash
# 健康检查
curl http://localhost:8088/api/health

# 登录测试（开发模式）
curl -X POST http://localhost:8088/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin"}'
```

#### Step 3: 启动前端开发服务器
```bash
# 新开一个终端
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/frontend

# 安装依赖（首次）
npm install

# 启动开发服务器
npm run dev
```

访问 `http://localhost:5173` 验证前端加载。

#### Step 4: 准备 OpenCode 实例（可选使用现有实例）
```bash
# 如果需要本地启动一个 OpenCode 实例用于测试
# cd ~/path/to/opencode
# npm run dev
```

#### Level 1 测试检查点 ✅

- [ ] 后端 `/api/health` 返回 200
- [ ] 登录接口返回 JWT token
- [ ] 前端页面正常加载
- [ ] 任务 CRUD API 正常工作
  ```bash
  # 创建任务
  curl -X POST http://localhost:8088/api/tasks \
    -H "Authorization: Bearer <token>" \
    -H "Content-Type: application/json" \
    -d '{"title":"测试任务","description":"本地测试"}'
  
  # 列出任务
  curl http://localhost:8088/api/tasks \
    -H "Authorization: Bearer <token>"
  ```
- [ ] OpenCode 实例列表可获取
  ```bash
  curl http://localhost:8088/api/instances \
    -H "Authorization: Bearer <token>"
  ```

---

### 4.2 Level 2: AI 功能测试

#### Step 5: 启动 Memora 服务

```bash
# 假设 memora 在另一个位置，根据实际路径调整
cd ~/path/to/memora

# 启动 memora FastAPI 服务（默认端口 8000）
# 具体启动命令根据 memora 项目而定，示例：
python -m uvicorn main:app --host 0.0.0.0 --port 8000
```

**验证 Memora 启动**:
```bash
curl http://localhost:8000/health
# 或查看 API 文档
open http://localhost:8000/docs
```

#### 配置 opencode-pocket 连接 Memora

确保 `.env.local` 中已配置：
```bash
POCKET_KXMEMORY_BASE_URL=http://localhost:8000
```

重启 opencode-pocket 后端使配置生效。

#### Level 2 测试检查点 ✅

- [ ] Memora 服务健康检查通过
- [ ] 笔记分类接口调用成功
  ```bash
  # 创建笔记并测试分类
  curl -X POST http://localhost:8088/api/notes \
    -H "Authorization: Bearer <token>" \
    -H "Content-Type: application/json" \
    -d '{"title":"会议记录","content":"讨论了项目架构和技术选型"}'
  ```
- [ ] 语音转文字功能可用（如果集成了 Groq Whisper）
- [ ] 笔记嵌入向量生成成功
- [ ] 相似笔记搜索返回结果

---

### 4.3 Level 3: 任务系统集成测试

#### Step 6: 配置 ACC MCP 连接

确保 `.env.local` 中已配置：
```bash
POCKET_MCP_BASE_URL=https://mcp.kxpms.cn/acc/mcp
POCKET_MCP_API_KEY=<valid_mcp_api_key>
POCKET_MCP_INSECURE_TLS=false  # 生产环境必须 false
```

重启 opencode-pocket 后端。

#### Level 3 测试检查点 ✅

- [ ] ACC MCP 连接测试通过
  ```bash
  # 通过 opencode-pocket 的 MCP 代理接口测试
  curl http://localhost:8088/api/mcp/tasks \
    -H "Authorization: Bearer <token>"
  ```
- [ ] 任务列表可从 ACC 同步
- [ ] 任务状态更新可推送到 ACC
- [ ] OpenCode 会话与 ACC 任务关联成功

---

### 4.4 Level 4: 完整集成测试

#### Step 7: 启动 llm-gateway-go

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/llm-gateway-go

# 根据 llm-gateway-go 的启动方式，示例：
# 1. 如果有配置文件
cp config.example.yaml config.yaml
# 编辑 config.yaml 配置端口和上游 LLM 提供商

# 2. 启动服务（假设默认端口 8080）
go run cmd/gateway/main.go
```

**验证 llm-gateway-go 启动**:
```bash
curl http://localhost:8080/health
# 或
curl http://localhost:8080/v1/models
```

#### 配置 opencode-pocket 使用 llm-gateway-go

在 `.env.local` 中：
```bash
POCKET_LLM_GATEWAY_URL=http://localhost:8080
POCKET_LLM_GATEWAY_API_KEY=<gateway_api_key>
```

#### Level 4 测试检查点 ✅

- [ ] llm-gateway-go 健康检查通过
- [ ] opencode-pocket 通过 gateway 调用 LLM 成功
  ```bash
  # 测试 LLM 代理接口
  curl -X POST http://localhost:8088/api/ai/completion \
    -H "Authorization: Bearer <token>" \
    -H "Content-Type: application/json" \
    -d '{"prompt":"Hello, world!","model":"llama-3.3-70b-versatile"}'
  ```
- [ ] 流量限流和配额管理生效（根据 gateway 配置）
- [ ] 监控指标可获取

---

## 五、集成测试场景

### 场景 1: 完整任务工作流

**目标**: 验证从任务创建到 OpenCode 会话关联的完整流程

1. **创建任务** → opencode-pocket API
2. **同步任务到 ACC** → 通过 MCP 接口
3. **启动 OpenCode 会话** → 调用 OpenCode 实例 API
4. **关联任务与会话** → opencode-pocket 后端记录
5. **查看任务状态** → 前端展示

```bash
# 示例脚本
TOKEN=$(curl -X POST http://localhost:8088/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin"}' | jq -r '.token')

# 1. 创建任务
TASK_ID=$(curl -X POST http://localhost:8088/api/tasks \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"title":"集成测试任务","description":"验证完整流程"}' \
  | jq -r '.id')

echo "Created task: $TASK_ID"

# 2. 获取 OpenCode 实例列表
curl http://localhost:8088/api/instances \
  -H "Authorization: Bearer $TOKEN" | jq

# 3. 创建会话（假设通过 OpenCode 实例）
# ... 调用 OpenCode API

# 4. 关联任务与会话
curl -X POST http://localhost:8088/api/tasks/$TASK_ID/sessions \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"sessionId":"ses_xxx","instanceId":"local-dev"}'
```

### 场景 2: 笔记智能处理

**目标**: 验证笔记从创建到 AI 分类、摘要、搜索的完整流程

1. **创建笔记** → opencode-pocket API
2. **自动分类** → 调用 memora 分类服务
3. **生成摘要** → 调用 memora LLM 服务
4. **生成嵌入** → 调用 memora 嵌入服务
5. **相似度搜索** → 使用向量搜索

```bash
# 1. 创建笔记
NOTE_ID=$(curl -X POST http://localhost:8088/api/notes \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title":"项目架构设计",
    "content":"本项目采用微服务架构，包含 API 网关、任务管理、笔记系统等模块。使用 Go 开发后端，Vue 3 开发前端。"
  }' | jq -r '.id')

# 2. 触发分类（如果是自动的，检查结果）
curl http://localhost:8088/api/notes/$NOTE_ID \
  -H "Authorization: Bearer $TOKEN" | jq '.category'

# 3. 搜索相似笔记
curl -X POST http://localhost:8088/api/notes/search \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"query":"微服务架构","limit":5}' | jq
```

### 场景 3: LLM 网关流量治理

**目标**: 验证通过 llm-gateway-go 的流量管理和监控

1. **配置租户配额** → llm-gateway-go 配置
2. **发起 LLM 请求** → opencode-pocket → gateway → LLM 提供商
3. **触发限流** → 超出配额时被拒绝
4. **查看监控指标** → gateway 监控接口

```bash
# 发起多个请求测试限流
for i in {1..10}; do
  curl -X POST http://localhost:8088/api/ai/completion \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"prompt":"Test request #'$i'"}' &
done
wait

# 检查 gateway 指标
curl http://localhost:8080/metrics
```

---

## 六、故障排查清单

### 6.1 后端启动失败

```bash
# 检查端口占用
lsof -i :8088

# 检查数据库连接
psql $POCKET_POSTGRES_DSN -c "SELECT 1"

# 查看详细日志
go run cmd/pocketd/main.go 2>&1 | tee logs/backend.log
```

### 6.2 前端连接后端失败

```bash
# 检查 CORS 配置
# 查看浏览器控制台错误

# 验证 API 基础 URL
cat frontend/.env.development | grep VITE_API_BASE_URL
```

### 6.3 外部服务连接失败

```bash
# 测试 Memora 连接
curl -v http://localhost:8000/health

# 测试 ACC MCP 连接
curl -v -H "Authorization: Bearer $POCKET_MCP_API_KEY" \
  $POCKET_MCP_BASE_URL/tasks

# 测试 llm-gateway 连接
curl -v http://localhost:8080/health
```

### 6.4 常见错误码

| 错误 | 可能原因 | 解决方案 |
|------|---------|---------|
| `connection refused` | 服务未启动 | 检查服务状态，查看启动日志 |
| `401 Unauthorized` | Token 无效或过期 | 重新登录获取新 token |
| `500 Internal Server Error` | 后端逻辑错误 | 查看后端日志详细信息 |
| `timeout` | 网络或服务响应慢 | 检查网络连接，增加超时时间 |

---

## 七、测试完成验收标准

### 7.1 功能验收

- [ ] **认证授权**: 用户登录、JWT 验证、权限控制正常
- [ ] **任务管理**: 创建、读取、更新、删除任务无异常
- [ ] **笔记系统**: 笔记 CRUD、分类、搜索功能正常
- [ ] **OpenCode 集成**: 实例列表、会话管理、消息获取正常
- [ ] **ACC 集成**: 任务同步、状态更新正常（如启用）
- [ ] **AI 功能**: 嵌入生成、LLM 调用、摘要生成正常（如启用）
- [ ] **邮箱集成**: OAuth 认证、邮件获取正常（如启用）

### 7.2 性能验收

- [ ] API 响应时间 < 500ms (P95)
- [ ] 前端首屏加载 < 2s
- [ ] WebSocket 连接稳定，心跳正常
- [ ] 并发 10 用户无明显性能下降

### 7.3 稳定性验收

- [ ] 连续运行 2 小时无崩溃
- [ ] 外部服务短暂故障后可自动恢复
- [ ] 数据库连接池正常回收
- [ ] 内存无明显泄漏（监控堆内存）

---

## 八、测试报告模板

测试完成后填写：

```markdown
## OpenCode Pocket 本地集成测试报告

**测试时间**: YYYY-MM-DD HH:MM  
**测试人员**: [姓名]  
**测试环境**: macOS / Linux / Docker  

### 测试结果汇总

| 测试级别 | 通过率 | 备注 |
|---------|-------|------|
| Level 1: 核心功能 | X/Y | ... |
| Level 2: AI 功能 | X/Y | ... |
| Level 3: 任务集成 | X/Y | ... |
| Level 4: 完整集成 | X/Y | ... |

### 发现的问题

1. **问题描述**: ...
   - **严重程度**: 高/中/低
   - **复现步骤**: ...
   - **解决方案**: ...

### 性能指标

- API 平均响应时间: XX ms
- 内存占用: XX MB
- CPU 占用: XX%

### 结论

[ ] ✅ 通过，可进入下一阶段  
[ ] ⚠️ 有问题但可继续  
[ ] ❌ 未通过，需修复
```

---

## 九、下一步计划

完成本地测试后的后续工作：

1. **Docker 化部署**: 编写 `docker-compose.yml` 统一管理所有服务
2. **CI/CD 集成**: 配置自动化测试和部署流水线
3. **生产环境部署**: 准备生产环境配置和部署脚本
4. **压力测试**: 使用 k6/ab 进行压力测试
5. **监控告警**: 配置 Prometheus + Grafana 监控

---

## 附录

### A. 快速启动脚本

保存为 `scripts/start-local-test.sh`:

```bash
#!/bin/bash
set -e

echo "🚀 Starting OpenCode Pocket Local Test Environment..."

# 1. 启动 PostgreSQL
echo "📦 Starting PostgreSQL..."
brew services start postgresql@14

# 2. 启动后端
echo "🔧 Starting Backend..."
cd backend
export $(cat ../.env.local | grep -v '^#' | xargs)
go run cmd/pocketd/main.go > ../logs/backend.log 2>&1 &
BACKEND_PID=$!
echo "Backend PID: $BACKEND_PID"

# 等待后端启动
sleep 5
curl -f http://localhost:8088/api/health || { echo "Backend failed to start"; exit 1; }

# 3. 启动前端
echo "🎨 Starting Frontend..."
cd ../frontend
npm run dev > ../logs/frontend.log 2>&1 &
FRONTEND_PID=$!
echo "Frontend PID: $FRONTEND_PID"

echo "✅ All services started!"
echo "Backend: http://localhost:8088"
echo "Frontend: http://localhost:5173"
echo ""
echo "To stop: kill $BACKEND_PID $FRONTEND_PID"
```

### B. 环境变量检查脚本

保存为 `scripts/check-env.sh`:

```bash
#!/bin/bash

echo "🔍 Checking environment configuration..."

REQUIRED_VARS=(
  "POCKET_POSTGRES_DSN"
  "POCKET_JWT_SECRET"
)

OPTIONAL_VARS=(
  "POCKET_KXMEMORY_BASE_URL"
  "POCKET_MCP_BASE_URL"
  "POCKET_LLM_GATEWAY_URL"
)

for var in "${REQUIRED_VARS[@]}"; do
  if [ -z "${!var}" ]; then
    echo "❌ Missing required: $var"
  else
    echo "✅ $var is set"
  fi
done

for var in "${OPTIONAL_VARS[@]}"; do
  if [ -z "${!var}" ]; then
    echo "⚠️  Optional not set: $var"
  else
    echo "✅ $var is set"
  fi
done
```

---

**文档版本**: v1.0  
**最后更新**: 2026-07-03
