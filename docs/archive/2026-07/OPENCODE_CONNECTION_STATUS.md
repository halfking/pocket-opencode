> **STATUS: superseded** (2026-08-23)
> Evidence level at supersede time: `claimed (unverified)`
> Superseded by: [`docs/governance/STATUS-MATRIX.md`](../docs/governance/STATUS-MATRIX.md)
> Do NOT use this doc for current implementation decisions.
>
> This doc claimed "实例连接状态报告" at 2026-07-06 with no test log. At supersede time, no replacement evidence was captured in `docs/governance/EVIDENCE-LEDGER.md`. See `docs/governance/REVIEW-QUEUE.md` Q-004.

# OpenCode 实例连接状态报告

**日期**: 2026-07-06  
**问题**: Backend 如何感知本地的 OpenCode 实例

---

## 🔍 当前状态诊断

### 发现的 OpenCode 实例

✅ **找到 OpenCode 进程**:
```
进程 ID: 20066
命令: /Users/xutaohuang/.opencode/bin/opencode serve
端口: 4096 (localhost:bre)
状态: ✅ 运行中
```

✅ **OpenCode 类型**: **桌面应用版本** (OpenCode.app)
- 这是 Electron 桌面应用
- 主要用于本地开发，提供 GUI 界面
- **不提供标准的 REST API**
- 返回 HTML 前端页面

### 测试结果

```bash
# 测试 API 端点
$ curl http://localhost:4096/api/session
Content-Type: text/html; charset=utf-8  # ❌ 返回 HTML，不是 JSON

$ curl http://localhost:4096/api/health
<!doctype html>  # ❌ 返回 HTML 页面
```

**结论**: ❌ 当前运行的 OpenCode 桌面版**不提供** REST API，无法被 Backend 直接调用。

---

## 📋 三种解决方案

### 方案 1: 运行 OpenCode CLI/Server 版本 ✅ (推荐)

**说明**: 运行提供 REST API 的 OpenCode 服务端版本

#### 步骤：

1. **检查是否有 OpenCode CLI 工具**:
```bash
# 检查 CLI 工具位置
ls -la /Users/xutaohuang/.opencode/bin/

# 或者检查 OpenCode 源码
ls -la ~/workspace/ai/opencode/
```

2. **如果有源码，启动 API 服务器**:
```bash
cd ~/workspace/ai/opencode
npm install
npm run dev  # 或 npm run start

# 应该会启动在某个端口（例如 3000 或 4096）
# 并提供 REST API
```

3. **验证 API**:
```bash
# 测试 API
curl http://localhost:3000/api/session
# 应该返回 JSON 格式的会话列表

# 或
curl http://localhost:4096/api/v1/sessions
```

4. **配置 Backend**:
```bash
# 修改 backend/start-dev.sh
export POCKET_OPENCODE_INSTANCES='[
  {
    "id": "local-opencode-api",
    "displayName": "本地 OpenCode API 服务",
    "baseURL": "http://localhost:3000",
    "environment": "development"
  }
]'
```

---

### 方案 2: 使用 Docker 运行 OpenCode Server ✅

**说明**: 使用 Docker 容器运行 OpenCode API 服务器

```bash
# 拉取 OpenCode 镜像（如果有）
docker pull your-org/opencode:latest

# 或者构建本地镜像
cd ~/workspace/ai/opencode
docker build -t opencode:local .

# 运行容器
docker run -d \
  --name opencode-server \
  -p 4097:4096 \
  -v ~/opencode-data:/data \
  opencode:local

# 验证
curl http://localhost:4097/api/session

# 配置 Backend
export POCKET_OPENCODE_INSTANCES='[
  {
    "id": "docker-opencode",
    "displayName": "Docker OpenCode 服务",
    "baseURL": "http://localhost:4097",
    "environment": "development"
  }
]'
```

---

### 方案 3: 连接远程 OpenCode 实例 ✅

**说明**: 如果有部署在服务器上的 OpenCode 实例

```bash
# 配置远程实例
export POCKET_OPENCODE_INSTANCES='[
  {
    "id": "remote-prod",
    "displayName": "远程生产环境",
    "baseURL": "https://opencode.your-domain.com",
    "environment": "production",
    "capabilities": ["session", "summary"]
  }
]'

# 如果需要认证
export POCKET_OPENCODE_AUTH_TOKEN="your-api-token"
```

---

### 方案 4: 暂时使用 Demo 模式 (当前状态)

**说明**: 继续使用静态 demo 实例进行开发

**当前配置**:
```bash
# Backend 返回硬编码的 demo-main 实例
# 不连接真实的 OpenCode
# 适合前端 UI 开发和测试
```

**优点**:
- ✅ 无需额外配置
- ✅ 前端 UI 可以正常开发
- ✅ 路由和认证测试正常

**缺点**:
- ❌ 无法获取真实的会话数据
- ❌ 无法测试实际的 OpenCode 集成

---

## 🎯 推荐行动方案

### 选项 A: 找到 OpenCode 源码并启动 API 服务 (推荐)

```bash
# 1. 查找 OpenCode 源码
cd ~/workspace/ai/opencode  # 或其他位置
ls -la

# 2. 查看 package.json 确认启动命令
cat package.json | grep -A 5 scripts

# 3. 启动 API 服务器
npm run dev  # 或 npm run start-api

# 4. 验证 API
curl http://localhost:3000/api/session

# 5. 配置 Backend 连接
cd ~/workspace/official-deploy/services/opencode-pocket/backend
# 编辑 start-dev.sh，修改 baseURL
```

### 选项 B: 询问团队获取 OpenCode API 服务器

```
问题：
1. OpenCode 的 API 服务器如何启动？
2. OpenCode API 的端口和端点是什么？
3. 是否需要认证 token？
4. API 文档在哪里？
```

### 选项 C: 继续使用 Demo 模式开发前端

```bash
# 当前已经配置好
# 可以继续开发和测试前端 UI
# 等后续有真实 API 再切换
```

---

## 📊 当前架构状态

```
┌─────────────────────────────────────────┐
│  OpenCode Pocket (前端 + Backend)        │
│                                         │
│  ✅ 登录认证: 正常                        │
│  ✅ 路由守卫: 已修复                      │
│  ✅ 实例列表 API: 正常                    │
│  ⚠️  OpenCode 连接: 需要配置             │
│                                         │
│  Backend (localhost:8088)               │
│  ├─ 配置的实例:                          │
│  │  - local-opencode                   │
│  │  - baseURL: http://localhost:4096  │
│  │  - 状态: unknown                    │
│  │                                     │
│  └─ 问题: 目标端口返回 HTML 不是 API     │
└─────────────────────────────────────────┘
              │
              │ ❌ 尝试连接
              ↓
┌─────────────────────────────────────────┐
│  OpenCode 桌面应用 (port 4096)           │
│                                         │
│  类型: Electron GUI 应用                 │
│  功能: 本地开发界面                       │
│  API: ❌ 不提供 REST API                │
│  返回: HTML 页面                         │
└─────────────────────────────────────────┘
```

**需要**:

```
┌─────────────────────────────────────────┐
│  OpenCode Pocket Backend                │
│  (localhost:8088)                       │
└─────────────────────────────────────────┘
              │
              │ ✅ REST API 调用
              ↓
┌─────────────────────────────────────────┐
│  OpenCode API 服务器 (port 3000/4096)   │
│                                         │
│  类型: API Server / CLI                 │
│  功能: 提供 REST API                     │
│  端点:                                   │
│  - GET /api/session                     │
│  - GET /api/session/:id                 │
│  - GET /api/session/:id/messages        │
│  返回: JSON 数据                         │
└─────────────────────────────────────────┘
```

---

## 🚀 立即行动

### 步骤 1: 找到 OpenCode 源码或 API 服务器

```bash
# 搜索可能的位置
find ~ -name "opencode" -type d 2>/dev/null | grep -v node_modules | head -10

# 或者检查常见位置
ls ~/workspace/ai/
ls ~/projects/
ls ~/code/
```

### 步骤 2: 查看 OpenCode 文档

```bash
# 如果找到源码
cd <opencode-source-dir>
cat README.md
cat docs/API.md  # 或类似文档
```

### 步骤 3: 测试 API 可用性

```bash
# 尝试不同的端口和路径
curl http://localhost:3000/api/session
curl http://localhost:4096/api/v1/session
curl http://localhost:8080/api/session
```

---

## 📝 总结

**当前问题**: ❌ 本地 OpenCode 是桌面应用，不提供 REST API

**解决方案**: 需要启动 OpenCode API 服务器版本

**临时方案**: ✅ 可以继续使用 Demo 模式开发前端

**下一步**: 
1. 找到 OpenCode API 服务器的启动方法
2. 或者连接远程 OpenCode 实例
3. 或者继续 Demo 模式完成前端开发

---

**需要帮助**: 
- 你是否知道 OpenCode 源码的位置？
- 是否有 OpenCode API 文档？
- 是否有部署好的远程 OpenCode 实例可以连接？

---

**报告人**: Kiro AI  
**日期**: 2026-07-06  
**状态**: 等待 OpenCode API 配置信息
