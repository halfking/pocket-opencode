> **STATUS: superseded** (2026-08-27)
> Evidence level at supersede time: `claimed (unverified)`
> Superseded by: [`docs/新架构v1/03-roadmap/里程碑.md`](docs/新架构v1/03-roadmap/里程碑.md)
> Do NOT use this doc for current implementation decisions.
> This doc is a historical plan/analysis from a completed sprint; 规划以 v3 里程碑为准。

# OpenCode Pocket 部署架构规划

**规划时间**: 2026-07-04 17:22  
**目标**: 清晰、完整、可执行的部署方案

---

## 🏗️ 服务器架构

### 56服务器 (14.103.169.56)
**角色**: Nginx反向代理 + SSL终止  
**功能**: 
- 接收外部HTTPS请求
- SSL证书管理 (Let's Encrypt)
- 反向代理到184服务器
- 负载均衡（如需要）

**访问方式**: `ssh root@14.103.169.56 -p 25022`

### 184服务器 (172.31.0.4)
**角色**: 应用服务器 + 数据库服务器  
**功能**:
- OpenCode Pocket后端 (Go)
- 前端静态文件 (Vue3)
- PostgreSQL数据库

**访问方式**: 需要确认（从56跳板？K8s？）

---

## 📦 服务端口分配

### 184服务器端口规划

| 服务 | 端口 | 说明 | 状态 |
|------|------|------|------|
| **Pocket后端** | 9010 | Go HTTP服务 | 🎯 目标端口 |
| **前端页面** | 10026 | Nginx静态文件 | ✅ 现有 |
| **PostgreSQL** | 5432 | 数据库 | ✅ 现有 |
| **旧Pocket服务** | 9010 | 待替换 | ⚠️ 需停止 |

---

## 🔄 数据流

```
用户手机
    ↓ HTTPS
56服务器 (m.kxpms.cn:443)
    ↓ HTTP (内网)
184服务器:9010 (Pocket后端)
    ↓
184服务器:5432 (PostgreSQL)
```

**详细流程**:
1. 用户访问 `https://m.kxpms.cn`
2. DNS解析到 14.103.169.56
3. 56 Nginx SSL终止，转发到 172.31.0.4:10026 (前端页面)
4. 前端JS发起API请求到 `https://m.kxpms.cn/api/*`
5. 56 Nginx代理到 172.31.0.4:9010 (Pocket后端)
6. Pocket后端连接 localhost:5432 (PostgreSQL)

---

## 📋 部署步骤

### 阶段1: 准备工作 (10分钟)

#### 1.1 访问184服务器
```bash
# 从本地直接访问？还是通过56跳板？
# 需要确认访问方式
```

**TODO**: 确认如何SSH到184服务器

#### 1.2 停止旧服务
```bash
# 在184服务器上
# 找到9010端口的进程
netstat -tlnp | grep 9010
# 或
ss -tlnp | grep 9010

# 停止旧服务（Docker/K8s/进程）
# 具体命令取决于部署方式
```

### 阶段2: 部署后端 (15分钟)

#### 2.1 上传后端二进制
```bash
# 编译好的二进制文件
LOCAL: backend/pocketd-linux (已存在)

# 上传到184服务器
scp -P <port> backend/pocketd-linux root@<184-ip>:/opt/opencode-pocket/pocketd
```

#### 2.2 配置环境变量
```bash
# 在184服务器上创建 /opt/opencode-pocket/.env
cat > /opt/opencode-pocket/.env << 'EOF'
# 数据库配置
POCKET_POSTGRES_DSN=postgresql://postgres:<password>@localhost:5432/pocket?sslmode=disable

# 认证配置
POCKET_DEV_AUTH=true
POCKET_JWT_SECRET=pocket-production-secret-2026

# 服务配置
POCKET_HTTP_PORT=9010

# NPS配置（如需要）
# POCKET_NPS_BASE_URL=...
EOF
```

#### 2.3 初始化数据库
```bash
# 在184服务器上
psql -U postgres << 'SQL'
-- 创建数据库
CREATE DATABASE IF NOT EXISTS pocket;

\c pocket

-- 创建users表
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'user',
    created_at BIGINT NOT NULL
);

-- 创建notes表
CREATE TABLE IF NOT EXISTS notes (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    workspace_id TEXT DEFAULT 'default',
    title TEXT,
    content TEXT NOT NULL DEFAULT '',
    snippet TEXT,
    content_type TEXT DEFAULT 'voice',
    domain TEXT,
    tags JSONB DEFAULT '[]'::jsonb,
    audio_path TEXT,
    audio_duration INTEGER DEFAULT 0,
    created_by_voice BOOLEAN DEFAULT TRUE,
    ai_summary TEXT,
    confidence_score REAL,
    created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITHOUT TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITHOUT TIME ZONE
);

-- 其他必要的表...
SQL
```

#### 2.4 创建systemd服务
```bash
# 在184服务器上
cat > /etc/systemd/system/opencode-pocket.service << 'EOF'
[Unit]
Description=OpenCode Pocket Backend
After=network.target postgresql.service

[Service]
Type=simple
User=root
WorkingDirectory=/opt/opencode-pocket
EnvironmentFile=/opt/opencode-pocket/.env
ExecStart=/opt/opencode-pocket/pocketd
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

# 启动服务
systemctl daemon-reload
systemctl enable opencode-pocket
systemctl start opencode-pocket
systemctl status opencode-pocket
```

### 阶段3: 前端部署 (10分钟)

#### 3.1 构建前端
```bash
# 在本地
cd frontend
echo "VITE_API_BASE=https://m.kxpms.cn" > .env
npm run build
```

#### 3.2 上传前端
```bash
# 打包
tar -czf dist.tar.gz -C frontend/dist .

# 上传到184服务器
scp dist.tar.gz root@<184>:/var/www/m.kxpms.cn/

# 在184上解压
cd /var/www/m.kxpms.cn/
tar -xzf dist.tar.gz
```

#### 3.3 配置184的Nginx（前端文件服务）
```bash
# 在184服务器上
# /etc/nginx/sites-available/pocket-frontend

server {
    listen 10026;
    server_name _;
    
    root /var/www/m.kxpms.cn;
    index index.html;
    
    location / {
        try_files $uri $uri/ /index.html;
    }
}
```

### 阶段4: 56服务器Nginx配置 (5分钟)

#### 4.1 确认upstream配置
```nginx
# 在56: /etc/nginx/sites-available/m.kxpms.cn

upstream mobile_h5_backend {
    server 172.31.0.4:10026 max_fails=3 fail_timeout=30s;
    keepalive 32;
}

upstream pocket_backend {
    server 172.31.0.4:9010 max_fails=2 fail_timeout=10s;
    keepalive 16;
}
```

#### 4.2 确认location配置
```nginx
server {
    listen 443 ssl http2;
    server_name m.kxpms.cn;
    
    # 前端页面
    location / {
        proxy_pass http://mobile_h5_backend;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        # ... 其他配置
    }
    
    # API代理
    location /api/ {
        proxy_pass http://pocket_backend;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        # ... 其他配置
    }
}
```

#### 4.3 重新加载Nginx
```bash
# 在56服务器
nginx -t
nginx -s reload
```

---

## ✅ 验证步骤

### 验证1: 后端健康检查
```bash
# 从56服务器测试内网
curl http://172.31.0.4:9010/api/instances

# 从外网测试
curl https://m.kxpms.cn/api/instances
```

### 验证2: 登录功能
```bash
curl -X POST https://m.kxpms.cn/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin"}'

# 应该返回: {"token":"...","user":"admin"}
```

### 验证3: 前端页面
```bash
# 浏览器访问
https://m.kxpms.cn

# 应该显示OpenCode Pocket登录页
```

### 验证4: 手机测试
1. 打开手机上的OpenCode Pocket应用
2. 登录 admin/admin
3. 测试底部导航
4. 创建笔记/任务

---

## 🚧 待确认项

### 关键问题（需要您回答）

1. **如何访问184服务器？**
   - [ ] 直接SSH：`ssh root@172.31.0.4 -p ???`
   - [ ] 从56跳板：`ssh -J root@56 root@184`
   - [ ] K8s部署：`kubectl apply -f ...`
   - [ ] Docker部署：`docker run ...`
   - [ ] 其他方式

2. **PostgreSQL密码是什么？**
   - 需要用于连接字符串

3. **184上是否已有Nginx？**
   - 用于服务前端10026端口

4. **旧服务如何停止？**
   - systemd service？
   - docker container？
   - k8s pod？
   - 直接进程？

---

## 📝 部署清单

在开始部署前，请准备：

- [ ] 184服务器访问方式
- [ ] PostgreSQL访问密码
- [ ] 确认旧服务停止方式
- [ ] 确认184上Nginx配置
- [ ] 备份现有服务（如需要）

---

## ⏱️ 预计时间

- 准备工作：10分钟
- 后端部署：15分钟
- 前端部署：10分钟
- Nginx配置：5分钟
- 测试验证：10分钟

**总计**: ~50分钟

---

## 💡 建议

鉴于：
1. 需要确认多个访问细节
2. 涉及生产环境操作
3. 已持续工作5+小时

**建议**: 明天清醒状态下，用1小时完成部署

**优势**:
- 避免疲劳操作错误
- 有充足时间确认细节
- 可以完整测试验证

---

**请您决定**: 
- A: 现在继续（需要先回答上述4个关键问题）
- B: 明天继续（我已准备好完整方案）
