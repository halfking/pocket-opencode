> **STATUS: superseded** (2026-08-27)
> Evidence level at supersede time: `claimed (unverified)`
> Superseded by: [`docs/新架构v1/02-modules/redclaw-integration.md`](docs/新架构v1/02-modules/redclaw-integration.md)
> Do NOT use this doc for current implementation decisions.
> （补横幅：SUPERSEDED.md Group 1 已登记）

# OpenCode 实例发现配置方案

**问题**: Backend 如何感知本地运行的 OpenCode 实例？

**当前状态**: Backend 使用静态 NPS 适配器（demo 模式），返回硬编码的 `demo-main` 实例

---

## 📋 三种方案对比

### 方案 1: JSON 配置文件（推荐 - 最简单）✅

**适用场景**: 
- 开发/测试环境
- OpenCode 实例数量较少且固定
- 不需要动态发现

**优点**:
- ✅ 配置简单，无需额外服务
- ✅ 立即可用，不依赖 NPS
- ✅ 适合本地开发

**缺点**:
- ⚠️ 需要手动配置每个实例
- ⚠️ 无法自动发现新实例

**配置方法**:

1. **创建 OpenCode 实例配置文件**:
```json
// opencode-instances.json
[
  {
    "id": "local-dev",
    "displayName": "本地开发环境",
    "baseURL": "http://localhost:4096",
    "environment": "development",
    "capabilities": ["session", "summary", "pty"]
  },
  {
    "id": "local-prod",
    "displayName": "本地生产环境",
    "baseURL": "http://localhost:5096",
    "environment": "production",
    "capabilities": ["session", "summary"]
  }
]
```

2. **设置环境变量**:
```bash
# 方式 1: 使用 JSON 字符串
export POCKET_OPENCODE_INSTANCES='[{"id":"local-dev","displayName":"本地开发","baseURL":"http://localhost:4096","environment":"development","capabilities":["session","summary","pty"]}]'

# 方式 2: 使用文件路径（如果 Backend 支持）
export POCKET_OPENCODE_INSTANCES_FILE="./config/opencode-instances.json"
```

3. **启动 Backend**:
```bash
cd backend
export POCKET_DEV_AUTH=true
export POCKET_JWT_SECRET=test-secret-key
export POCKET_OPENCODE_INSTANCES='[{"id":"local-dev","displayName":"本地开发","baseURL":"http://localhost:4096","environment":"development"}]'
./pocketd
```

4. **验证**:
```bash
# 检查实例列表
curl -s http://localhost:8088/api/instances \
  -H "Authorization: Bearer $TOKEN" | jq .

# 应该看到 local-dev 实例
```

---

### 方案 2: NPS 服务发现（生产环境推荐）

**适用场景**:
- 生产环境
- 多个 OpenCode 实例需要统一管理
- 需要动态发现和心跳检测

**优点**:
- ✅ 自动发现新实例
- ✅ 健康检查和心跳
- ✅ 适合分布式部署

**缺点**:
- ⚠️ 需要部署 NPS 服务
- ⚠️ 配置相对复杂

**配置方法**:

1. **部署 NPS 服务** (如果还没有):
```bash
# 下载 NPS
wget https://github.com/ehang-io/nps/releases/latest/download/linux_amd64_server.tar.gz
tar -xzf linux_amd64_server.tar.gz
cd nps

# 配置
vi conf/nps.conf
# 设置 web_username, web_password

# 启动
./nps start
```

2. **配置 OpenCode 实例连接到 NPS**:
```bash
# 在每个 OpenCode 机器上
./npc -server=<nps-server>:8024 -vkey=<your-vkey>
```

3. **配置 Backend 连接 NPS**:
```bash
export POCKET_NPS_BASE_URL="http://nps-server:8080"
export POCKET_NPS_AUTH_KEY="your-nps-auth-key"
export POCKET_NPS_AUTH_CRYPT_KEY="your-crypt-key"
```

4. **启动 Backend**:
```bash
cd backend
./pocketd
```

---

### 方案 3: 在 Backend 服务器安装 OpenCode CLI（混合方案）

**适用场景**:
- Backend 和 OpenCode 在同一台机器
- 需要本地快速访问
- 测试环境

**优点**:
- ✅ 零网络延迟
- ✅ 简化部署架构
- ✅ 适合单机部署

**缺点**:
- ⚠️ 只能访问本地实例
- ⚠️ 无法管理远程实例

**配置方法**:

1. **在 Backend 服务器安装 OpenCode**:
```bash
# 安装 Node.js (如果没有)
curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash -
sudo apt-get install -y nodejs

# 克隆 OpenCode
git clone https://github.com/your-org/opencode.git
cd opencode
npm install
npm run build

# 启动 OpenCode
npm run start
```

2. **配置 Backend 指向本地 OpenCode**:
```bash
export POCKET_OPENCODE_INSTANCES='[{"id":"localhost","displayName":"本地实例","baseURL":"http://localhost:4096","environment":"production"}]'
```

3. **启动 Backend**:
```bash
cd ../backend
./pocketd
```

---

## 🚀 推荐实施步骤

### 阶段 1: 开发环境（立即实施）

**使用方案 1 - JSON 配置**

1. **修改 backend/start-dev.sh**:
```bash
#!/bin/bash
set -e

echo "启动 OpenCode Pocket Backend (开发模式)"

# 停止现有进程
if pgrep -f pocketd > /dev/null; then
    killall pocketd 2>/dev/null || true
    sleep 1
fi

# 设置环境变量
export POCKET_DEV_AUTH=true
export POCKET_JWT_SECRET=test-secret-key-for-phase7-validation
export POCKET_HTTP_PORT=8088
export POCKET_DB_PATH=./data/pocket.sqlite

# ✨ 新增：配置本地 OpenCode 实例
export POCKET_OPENCODE_INSTANCES='[
  {
    "id": "local-dev",
    "displayName": "本地开发环境",
    "baseURL": "http://localhost:4096",
    "environment": "development",
    "capabilities": ["session", "summary", "pty"]
  }
]'

echo "配置的 OpenCode 实例:"
echo "$POCKET_OPENCODE_INSTANCES" | jq .

# 启动 backend
nohup ./pocketd > ../logs/backend-dev.log 2>&1 &
sleep 2

# 验证
if pgrep -f pocketd > /dev/null; then
    PID=$(pgrep -f pocketd | head -1)
    echo "✅ Backend 启动成功 (PID: $PID)"
    
    # 健康检查
    if curl -sf http://localhost:8088/healthz > /dev/null; then
        echo "✅ 健康检查通过"
    fi
    
    # 测试登录
    TOKEN=$(curl -s -X POST http://localhost:8088/api/auth/login \
        -H "Content-Type: application/json" \
        -d '{"username":"admin","password":"admin"}' \
        | jq -r '.token')
    
    if [ -n "$TOKEN" ] && [ "$TOKEN" != "null" ]; then
        echo "✅ 登录测试通过"
        
        # 检查实例列表
        INSTANCES=$(curl -s http://localhost:8088/api/instances \
            -H "Authorization: Bearer $TOKEN")
        
        INSTANCE_COUNT=$(echo "$INSTANCES" | jq '.instances | length')
        echo "✅ 实例列表: $INSTANCE_COUNT 个实例"
        echo "$INSTANCES" | jq -r '.instances[] | "  - \(.id): \(.displayName) (\(.baseURL))"'
    fi
fi
```

2. **创建配置文件** (可选):
```bash
# backend/config/opencode-instances.json
cat > backend/config/opencode-instances.json << 'EOF'
[
  {
    "id": "local-dev",
    "displayName": "本地开发环境",
    "baseURL": "http://localhost:4096",
    "environment": "development",
    "capabilities": ["session", "summary", "pty"]
  }
]
EOF
```

3. **测试**:
```bash
cd backend
./start-dev.sh

# 验证实例
TOKEN=$(curl -s -X POST http://localhost:8088/api/auth/login \
    -H "Content-Type: application/json" \
    -d '{"username":"admin","password":"admin"}' | jq -r '.token')

curl -s http://localhost:8088/api/instances \
    -H "Authorization: Bearer $TOKEN" | jq .
```

### 阶段 2: 测试环境

**方案 1 + 方案 3**

1. 在 Backend 服务器安装 OpenCode
2. 使用 JSON 配置指向 localhost:4096
3. 配置 systemd 服务自动启动

### 阶段 3: 生产环境

**方案 2 - NPS 服务发现**

1. 部署 NPS 服务
2. 配置所有 OpenCode 实例连接 NPS
3. Backend 通过 NPS API 发现实例

---

## 🔍 检查当前 OpenCode 实例

### 检查本地是否有 OpenCode 运行

```bash
# 检查 4096 端口
lsof -i :4096

# 或者
netstat -tuln | grep 4096

# 测试 OpenCode API
curl http://localhost:4096/api/health

# 如果返回健康状态，说明 OpenCode 正在运行
```

### 如果没有 OpenCode 实例

**选项 1: 启动本地 OpenCode**
```bash
cd ~/workspace/ai/opencode
npm install
npm run dev
```

**选项 2: 使用 Docker 运行 OpenCode**
```bash
docker run -d -p 4096:4096 \
  --name opencode \
  your-org/opencode:latest
```

**选项 3: 指向远程 OpenCode 实例**
```bash
export POCKET_OPENCODE_INSTANCES='[
  {
    "id": "remote-prod",
    "displayName": "远程生产环境",
    "baseURL": "https://opencode.example.com",
    "environment": "production"
  }
]'
```

---

## 📊 方案对比总结

| 特性 | 方案 1: JSON 配置 | 方案 2: NPS 发现 | 方案 3: 本地 CLI |
|------|------------------|-----------------|----------------|
| 配置难度 | ⭐ 简单 | ⭐⭐⭐ 复杂 | ⭐⭐ 中等 |
| 自动发现 | ❌ 否 | ✅ 是 | ❌ 否 |
| 健康检查 | ⚠️ 手动 | ✅ 自动 | ⚠️ 手动 |
| 适用场景 | 开发/测试 | 生产环境 | 单机部署 |
| 网络延迟 | 取决于配置 | 低 | 极低 |
| 扩展性 | ⭐⭐ 中 | ⭐⭐⭐ 高 | ⭐ 低 |

---

## 🎯 立即行动建议

### 1. 检查本地 OpenCode 状态
```bash
curl http://localhost:4096/api/health
```

### 2. 如果有 OpenCode，使用方案 1
```bash
# 修改 backend/start-dev.sh
# 添加 POCKET_OPENCODE_INSTANCES 配置
# 重启 Backend
```

### 3. 如果没有 OpenCode
- 选项 A: 启动本地 OpenCode 实例
- 选项 B: 配置远程 OpenCode 实例
- 选项 C: 暂时使用 demo 模式（当前状态）

---

## 📝 下一步

想要我帮你：
1. ✅ 检查本地是否有 OpenCode 运行？
2. ✅ 修改 `backend/start-dev.sh` 添加实例配置？
3. ✅ 创建测试脚本验证实例连接？
4. ✅ 部署 NPS 服务（如果需要）？

---

**创建时间**: 2026-07-06  
**作者**: Kiro AI  
**文档版本**: v1.0
