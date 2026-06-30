# 📡 OpenCode 实例自动注册方案

**目标:** 让池前主机上的 OpenCode 实例自动注册到 NPS，供 Pocket 发现和管理

---

## 🎯 架构设计

### 当前架构
```
[池前主机 A] OpenCode 实例 (未连接)
[池前主机 B] OpenCode 实例 (未连接)
[池前主机 C] OpenCode 实例 (未连接)
       ↓
    ❌ 无法被发现
       ↓
[Pocket App] → 看不到任何实例
```

### 目标架构
```
[池前主机 A] OpenCode + NPC 客户端
       ↓ (tunnel)
[NPS 服务器 56/252]
       ↓ (API)
[Pocket Backend 184] → 发现实例
       ↓
[Pocket App] → 显示实例列表
```

---

## 🔧 实施方案

### 方案 1: NPC 客户端 + 自动注册脚本 (推荐)

#### 1.1 在池前主机安装 NPC 客户端

**下载 NPC:**
```bash
# Linux/Mac
wget https://github.com/ehang-io/nps/releases/download/v0.26.10/linux_amd64_client.tar.gz
tar -xzvf linux_amd64_client.tar.gz

# 或者从 NPS 服务器下载
scp root@14.103.169.56:/usr/local/bin/npc ./
```

**配置 NPC:**
```bash
# 创建配置文件
cat > npc.conf << EOF
[common]
server_addr=14.103.169.56:8024
vkey=your_verify_key
auto_reconnection=true
max_conn=100
EOF
```

**启动 NPC:**
```bash
# 前台运行（测试）
./npc -config=npc.conf

# 后台运行
nohup ./npc -config=npc.conf > npc.log 2>&1 &

# 或者使用 systemd
sudo systemctl start npc
```

#### 1.2 自动注册 OpenCode 实例

**创建注册脚本:**
```bash
#!/bin/bash
# register-opencode.sh

POCKET_API="http://14.103.169.56:8088/api"
INSTANCE_ID="opencode-$(hostname)"
DISPLAY_NAME="OpenCode on $(hostname)"
NPS_CLIENT_ID="自动分配或手动指定"

# 获取 OpenCode 端口
OPENCODE_PORT=$(lsof -i -P -n | grep LISTEN | grep "node" | awk '{print $9}' | cut -d: -f2 | head -1)

if [ -z "$OPENCODE_PORT" ]; then
    echo "❌ OpenCode 未运行"
    exit 1
fi

echo "✅ 发现 OpenCode 在端口 $OPENCODE_PORT"

# 注册到 Pocket
curl -X POST "$POCKET_API/instances/register" \
  -H "Content-Type: application/json" \
  -d "{
    \"id\": \"$INSTANCE_ID\",
    \"displayName\": \"$DISPLAY_NAME\",
    \"npsClientId\": $NPS_CLIENT_ID,
    \"localPort\": $OPENCODE_PORT,
    \"environment\": \"development\"
  }"

echo "✅ OpenCode 实例已注册"
```

#### 1.3 配置 NPS 隧道

**在 NPS Web 管理界面 (http://14.103.169.56:8080):**

1. **创建客户端**
   - 客户端备注: `opencode-[主机名]`
   - 验证密钥: 自动生成
   - 记录客户端 ID

2. **创建隧道**
   - 类型: HTTP
   - 客户端: 选择上面创建的客户端
   - 服务端端口: 自动分配或指定
   - 目标 (target): `127.0.0.1:[OpenCode端口]`
   - 域名: `opencode-[主机名].kxpms.cn`

3. **获取访问地址**
   ```
   内网: http://127.0.0.1:[OpenCode端口]
   外网: http://opencode-[主机名].kxpms.cn
   ```

---

### 方案 2: OpenCode 内置 NPS 注册 (未来增强)

修改 OpenCode 代码，启动时自动注册到 NPS：

```python
# opencode/nps_registration.py
import requests
import socket

def register_to_nps():
    """启动时自动注册到 NPS"""
    hostname = socket.gethostname()
    instance_id = f"opencode-{hostname}"
    
    # 注册到 Pocket API
    response = requests.post(
        "http://14.103.169.56:8088/api/instances/register",
        json={
            "id": instance_id,
            "displayName": f"OpenCode on {hostname}",
            "localPort": 3000,  # OpenCode 默认端口
            "environment": "development"
        }
    )
    
    if response.status_code == 200:
        print(f"✅ 已注册到 Pocket: {instance_id}")
    else:
        print(f"❌ 注册失败: {response.text}")

# 在 OpenCode 启动时调用
if __name__ == "__main__":
    register_to_nps()
    # ... OpenCode 启动代码
```

---

## 🔌 Pocket Backend 需要的 API

### 新增实例注册 API

**文件:** `backend/internal/server/server.go`

```go
// handleRegisterInstance 注册新的 OpenCode 实例
func (s *Server) handleRegisterInstance(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }

    type RegisterRequest struct {
        ID            string `json:"id"`
        DisplayName   string `json:"displayName"`
        NPSClientID   int    `json:"npsClientId"`
        LocalPort     int    `json:"localPort"`
        Environment   string `json:"environment"`
    }

    var req RegisterRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "invalid request", http.StatusBadRequest)
        return
    }

    // 验证必填字段
    if req.ID == "" || req.DisplayName == "" {
        http.Error(w, "missing required fields", http.StatusBadRequest)
        return
    }

    // 注册实例到 registry
    instance := &model.Instance{
        ID:            req.ID,
        DisplayName:   req.DisplayName,
        NPSClientID:   req.NPSClientID,
        Environment:   req.Environment,
        Capabilities:  []string{"session", "summary"},
    }

    if err := s.registry.RegisterInstance(instance); err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]interface{}{
        "success": true,
        "instance": instance,
    })
}

// 添加到路由
func (s *Server) Handler() http.Handler {
    mux := http.NewServeMux()
    // ... 其他路由
    mux.HandleFunc("/api/instances/register", s.handleRegisterInstance)
    return mux
}
```

---

## 📋 实施步骤

### Step 1: 在一台池前主机上测试

1. **安装 NPC 客户端**
```bash
# 下载
wget https://github.com/ehang-io/nps/releases/download/v0.26.10/linux_amd64_client.tar.gz
tar -xzvf linux_amd64_client.tar.gz
```

2. **在 NPS Web 管理界面创建客户端**
```
访问: http://14.103.169.56:8080
用户名/密码: (需要提供)
客户端 → 新增 → 记录 vkey 和 client_id
```

3. **配置并启动 NPC**
```bash
./npc -server=14.103.169.56:8024 -vkey=[你的vkey] -type=tcp
```

4. **在 NPS 创建 HTTP 隧道**
```
隧道 → 新增
类型: HTTP
目标: 127.0.0.1:3000 (OpenCode 端口)
域名: opencode-test.kxpms.cn
```

5. **测试访问**
```bash
curl http://opencode-test.kxpms.cn/healthz
```

### Step 2: 更新 Pocket Backend

1. **添加实例注册 API**
2. **更新 registry 支持动态注册**
3. **重启 Backend 服务**

### Step 3: 注册实例

```bash
curl -X POST http://14.103.169.56:8088/api/instances/register \
  -H "Content-Type: application/json" \
  -d '{
    "id": "opencode-test",
    "displayName": "OpenCode Test",
    "npsClientId": 123,
    "localPort": 3000,
    "environment": "development"
  }'
```

### Step 4: 在 Pocket App 中验证

```
1. 打开 Pocket App
2. 登录
3. 选择服务器 (56)
4. 查看实例列表
5. 应该能看到 "OpenCode Test"
```

---

## 🔍 故障排查

### NPC 无法连接
```bash
# 检查网络
ping 14.103.169.56

# 检查端口
telnet 14.103.169.56 8024

# 查看 NPC 日志
cat npc.log
```

### 隧道不工作
```bash
# 检查 OpenCode 是否运行
curl http://localhost:3000/healthz

# 检查 NPS 隧道状态
# 在 NPS Web 界面查看隧道是否在线
```

### Pocket 看不到实例
```bash
# 检查实例是否注册
curl http://14.103.169.56:8088/api/instances

# 检查 Backend 日志
tail -f /data/services/opencode-pocket/logs/pocket.log
```

---

## 📊 预期结果

完成配置后，Pocket App 中应该显示：

```
服务器选择: NPS 56
  ↓
实例列表:
  📱 OpenCode Test
     - 环境: development
     - 状态: 在线
     - 功能: 2 个
  ↓
任务列表:
  (显示该实例的任务)
```

---

## 🚀 下一步

1. **提供 NPS 管理界面的登录信息**
   - URL: http://14.103.169.56:8080
   - 用户名/密码: ?

2. **确认一台池前主机用于测试**
   - 主机名: ?
   - OpenCode 端口: ?
   - 可以安装 NPC: ?

3. **开始实施**
   - 创建 NPC 客户端
   - 配置隧道
   - 注册到 Pocket

---

**需要你提供 NPS 管理界面的登录信息，我们就可以开始配置了！** 🔧
