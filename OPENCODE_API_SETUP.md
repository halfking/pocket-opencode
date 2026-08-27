> **STATUS: superseded** (2026-08-27)
> Evidence level at supersede time: `claimed (unverified)`
> Superseded by: [`docs/新架构v1/03-roadmap/接口规范.md`](docs/新架构v1/03-roadmap/接口规范.md)
> Do NOT use this doc for current implementation decisions.
> （补横幅：SUPERSEDED.md Group 1 已登记）

# OpenCode API 服务器配置指南

**发现**: ✅ OpenCode 源码位于 `~/workspace/ai/opencode`  
**状态**: ✅ 源码包含完整的 REST API 实现  
**目标**: 启动 OpenCode API 服务器并连接到 Pocket Backend

---

## 🎯 API 端点分析

从源码发现的 API 端点：

```typescript
// packages/server/src/groups/session.ts
GET  /api/session                      // 获取会话列表
POST /api/session                      // 创建新会话
GET  /api/session/:sessionID           // 获取会话详情
POST /api/session/:sessionID/prompt    // 发送提示

// packages/server/src/groups/message.ts
GET  /api/session/:sessionID/message   // 获取会话消息
```

**结论**: ✅ OpenCode 提供完整的 REST API，完全满足需求！

---

## 🚀 启动 OpenCode API 服务器

### 方案 1: 启动开发服务器（推荐）

```bash
# 1. 进入 OpenCode 目录
cd ~/workspace/ai/opencode

# 2. 安装依赖（如果还没有）
bun install
# 或
npm install

# 3. 启动服务器
bun run dev
# 这会启动 OpenCode CLI 服务器，监听某个端口（通常是 4096）
```

### 方案 2: 启动 Web 应用（如果需要 UI）

```bash
cd ~/workspace/ai/opencode
bun run dev:web
```

### 方案 3: 启动控制台（Console）

```bash
cd ~/workspace/ai/opencode
bun run dev:console
```

---

## 🔍 检查 API 服务器配置

### 查找实际监听端口

```bash
# 1. 启动服务器后，检查端口
cd ~/workspace/ai/opencode
bun run dev &

# 2. 等待几秒，然后检查端口
sleep 5
lsof -i -P | grep opencode

# 3. 测试 API
curl http://localhost:4096/api/session

# 4. 如果 4096 不行，尝试其他端口
curl http://localhost:3000/api/session
curl http://localhost:8080/api/session
```

### 检查配置文件

```bash
# 查找端口配置
cd ~/workspace/ai/opencode
grep -r "port\|PORT" packages/server/src/ --include="*.ts" | grep -E "4096|3000|8080"

# 查找环境变量配置
cat .env 2>/dev/null || echo "No .env file"
cat packages/server/.env 2>/dev/null || echo "No server .env"
```

---

## ⚙️ 配置 Pocket Backend 连接

### 步骤 1: 启动 OpenCode API 服务器

```bash
# 在新终端窗口
cd ~/workspace/ai/opencode
bun run dev

# 验证 API
curl http://localhost:4096/api/session
# 应该返回 JSON 格式的会话列表
```

### 步骤 2: 修改 Pocket Backend 配置

Backend 已经配置好了！只需要确认端口：

```bash
cd ~/workspace/official-deploy/services/opencode-pocket/backend

# 当前配置（在 start-dev.sh 中）:
# export POCKET_OPENCODE_INSTANCES='[
#   {
#     "id": "local-opencode",
#     "displayName": "本地 OpenCode 实例",
#     "baseURL": "http://localhost:4096",  # ← 确认这个端口
#     "environment": "development"
#   }
# ]'

# 如果端口不同，修改 baseURL
```

### 步骤 3: 重启 Backend 并测试

```bash
cd ~/workspace/official-deploy/services/opencode-pocket/backend
./start-dev.sh

# 测试连接
TOKEN=$(curl -s -X POST http://localhost:8088/api/auth/login \
    -H "Content-Type: application/json" \
    -d '{"username":"admin","password":"admin"}' | jq -r '.token')

# 获取实例列表
curl -s http://localhost:8088/api/instances \
    -H "Authorization: Bearer $TOKEN" | jq .

# 如果实例状态变为 "healthy"，说明连接成功！
```

---

## 🧪 完整测试脚本

创建测试脚本验证整个流程：

```bash
#!/bin/bash
# test-opencode-connection.sh

set -e

echo "=========================================="
echo "OpenCode API 连接测试"
echo "=========================================="

# 1. 检查 OpenCode API 是否运行
echo ""
echo "1. 检查 OpenCode API..."
if curl -sf http://localhost:4096/api/session > /dev/null; then
    echo "✅ OpenCode API 正常运行"
    SESSION_COUNT=$(curl -s http://localhost:4096/api/session | jq '.data | length' 2>/dev/null || echo "0")
    echo "   会话数量: $SESSION_COUNT"
else
    echo "❌ OpenCode API 未运行"
    echo ""
    echo "请启动 OpenCode:"
    echo "  cd ~/workspace/ai/opencode"
    echo "  bun run dev"
    exit 1
fi

# 2. 检查 Pocket Backend
echo ""
echo "2. 检查 Pocket Backend..."
if curl -sf http://localhost:8088/healthz > /dev/null; then
    echo "✅ Backend 正常运行"
else
    echo "❌ Backend 未运行"
    echo ""
    echo "请启动 Backend:"
    echo "  cd ~/workspace/official-deploy/services/opencode-pocket/backend"
    echo "  ./start-dev.sh"
    exit 1
fi

# 3. 测试登录
echo ""
echo "3. 测试登录..."
TOKEN=$(curl -s -X POST http://localhost:8088/api/auth/login \
    -H "Content-Type: application/json" \
    -d '{"username":"admin","password":"admin"}' | jq -r '.token')

if [ -n "$TOKEN" ] && [ "$TOKEN" != "null" ]; then
    echo "✅ 登录成功"
else
    echo "❌ 登录失败"
    exit 1
fi

# 4. 测试实例列表
echo ""
echo "4. 测试实例发现..."
INSTANCES=$(curl -s http://localhost:8088/api/instances \
    -H "Authorization: Bearer $TOKEN")

echo "$INSTANCES" | jq .

INSTANCE_COUNT=$(echo "$INSTANCES" | jq '.instances | length')
echo ""
echo "实例数量: $INSTANCE_COUNT"

if [ "$INSTANCE_COUNT" -gt 0 ]; then
    HEALTH=$(echo "$INSTANCES" | jq -r '.instances[0].health')
    echo "实例状态: $HEALTH"
    
    if [ "$HEALTH" = "healthy" ]; then
        echo "✅ OpenCode 实例连接成功！"
    else
        echo "⚠️  实例状态: $HEALTH"
        echo "可能原因："
        echo "  - OpenCode API 端口不是 4096"
        echo "  - OpenCode API 格式不匹配"
        echo "  - 网络连接问题"
    fi
fi

echo ""
echo "=========================================="
echo "测试完成"
echo "=========================================="
```

保存为 `test-opencode-connection.sh` 并运行：

```bash
cd ~/workspace/official-deploy/services/opencode-pocket
chmod +x test-opencode-connection.sh
./test-opencode-connection.sh
```

---

## 📋 常见问题排查

### 问题 1: OpenCode API 启动失败

```bash
cd ~/workspace/ai/opencode

# 检查 bun 是否安装
bun --version

# 如果没有，安装 bun
curl -fsSL https://bun.sh/install | bash

# 或使用 npm
npm install
npm run dev
```

### 问题 2: 端口被占用

```bash
# 检查 4096 端口
lsof -i :4096

# 如果被占用，杀掉进程
kill -9 <PID>

# 或者配置 OpenCode 使用其他端口
# 修改 OpenCode 配置文件
```

### 问题 3: API 格式不匹配

```bash
# 测试 OpenCode API 格式
curl -s http://localhost:4096/api/session | jq .

# 如果返回格式不是预期的，检查 OpenCode 版本
cd ~/workspace/ai/opencode
git log --oneline -5

# 查看 API 文档
cat AGENTS.md
cat CONTEXT.md
```

---

## 🎯 快速启动指南（两个终端）

### 终端 1: 启动 OpenCode API

```bash
cd ~/workspace/ai/opencode
bun run dev

# 等待看到类似输出:
# OpenCode server listening on http://localhost:4096
```

### 终端 2: 启动 Pocket Backend

```bash
cd ~/workspace/official-deploy/services/opencode-pocket/backend
./start-dev.sh

# 应该看到:
# ✅ Backend 启动成功
# ✅ 登录测试通过
# ✅ 实例列表: 1 个实例
# 实例状态: healthy  ← 这个很重要！
```

### 验证

```bash
# 在任意终端运行
cd ~/workspace/official-deploy/services/opencode-pocket
./test-opencode-connection.sh
```

---

## 📊 预期结果

启动成功后应该看到：

```
Backend 配置:
  ✅ 登录认证正常
  ✅ 实例列表 API 正常
  ✅ OpenCode 连接: healthy
  ✅ 可以获取会话列表
  ✅ 可以获取会话详情

OpenCode API 状态:
  ✅ 服务器运行中
  ✅ 提供 REST API
  ✅ 返回 JSON 数据
  ✅ 端口: 4096
```

---

## 🚀 下一步

1. **立即执行**:
   ```bash
   # 终端 1
   cd ~/workspace/ai/opencode && bun run dev
   
   # 终端 2
   cd ~/workspace/official-deploy/services/opencode-pocket/backend && ./start-dev.sh
   ```

2. **验证连接**:
   ```bash
   cd ~/workspace/official-deploy/services/opencode-pocket
   ./test-opencode-connection.sh
   ```

3. **构建前端 APK**:
   ```bash
   cd frontend
   npm run build
   npx cap sync android
   cd ../android
   ./gradlew assembleDebug
   ```

4. **在模拟器测试**:
   - 安装 APK
   - 登录 (admin/admin)
   - 查看实例列表（应该显示 local-opencode）
   - 查看会话列表（应该显示真实数据）

---

**创建时间**: 2026-07-06  
**状态**: 准备就绪，等待启动 OpenCode API  
**下一步**: 在两个终端分别启动 OpenCode 和 Backend
