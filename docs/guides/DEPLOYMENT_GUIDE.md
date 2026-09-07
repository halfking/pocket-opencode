# OpenCode Pocket 部署指南

**版本**: Phase 6 (commit 7223368)  
**日期**: 2026-07-05  
**状态**: ✅ 代码已合并到 main 分支并推送

## 部署概览

本指南用于将 OpenCode Pocket 部署到生产服务器。系统包含：
- **Backend**: Go 服务器 (端口 8088)
- **Database**: PostgreSQL 14+
- **Frontend**: Vue 3 SPA (构建到 backend 静态资源)

## 当前验证状态

### 本地环境验证 ✅
```
✅ Backend API 运行正常 (http://localhost:8088)
✅ JWT 认证功能正常
✅ Tasks API 完全可用
✅ 数据库连接正常 (2 条测试任务)
✅ 代码已推送到 main 分支
```

### API 功能验证
```bash
# 登录测试
curl -X POST http://localhost:8088/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin"}'
# ✅ 返回 JWT token

# 任务列表
curl http://localhost:8088/api/tasks -H "Authorization: Bearer $TOKEN"
# ✅ 返回 2 个任务
```

## 服务器要求

### 硬件要求
- CPU: 2 核心+
- 内存: 4GB+
- 磁盘: 20GB+
- 网络: 公网 IP 或域名

### 软件要求
- OS: Ubuntu 20.04+ / Debian 11+
- Go: 1.21+
- PostgreSQL: 14+
- Node.js: 18+ (仅构建时需要)

## 部署步骤

### 1. 准备服务器

```bash
# SSH 到服务器
ssh user@your-server

# 更新系统
sudo apt update && sudo apt upgrade -y

# 安装依赖
sudo apt install -y git postgresql postgresql-contrib golang-go nodejs npm

# 验证安装
go version      # 应显示 >= 1.21
node --version  # 应显示 >= 18
psql --version  # 应显示 >= 14
```

### 2. 配置 PostgreSQL

```bash
# 启动 PostgreSQL
sudo systemctl start postgresql
sudo systemctl enable postgresql

# 创建数据库和用户
sudo -u postgres psql << EOF
CREATE USER pocket_user WITH PASSWORD 'your_secure_password_here';
CREATE DATABASE pocket_db OWNER pocket_user;
GRANT ALL PRIVILEGES ON DATABASE pocket_db TO pocket_user;
\q
EOF

# 创建 tasks 表
sudo -u postgres psql -U pocket_user -d pocket_db << 'EOF'
CREATE TABLE IF NOT EXISTS tasks (
  id VARCHAR(255) PRIMARY KEY,
  title VARCHAR(500) NOT NULL,
  description TEXT,
  status VARCHAR(50) DEFAULT 'active',
  priority VARCHAR(50) DEFAULT 'normal',
  workstream_id VARCHAR(255),
  source VARCHAR(50) DEFAULT 'local',
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_tasks_status ON tasks(status);
CREATE INDEX idx_tasks_source ON tasks(source);
CREATE INDEX idx_tasks_workstream ON tasks(workstream_id);
EOF

# 验证表创建
sudo -u postgres psql -U pocket_user -d pocket_db -c "\dt"
```

### 3. 克隆代码

```bash
# 创建应用目录
sudo mkdir -p /opt/opencode-pocket
sudo chown $USER:$USER /opt/opencode-pocket

# 克隆仓库
cd /opt
git clone https://github.com/halfking/pocket-opencode.git opencode-pocket
cd opencode-pocket

# 切换到 main 分支
git checkout main
git pull origin main

# 验证版本
git log --oneline -1
# 应显示: 7223368 feat(phase6): UI z-index fix + task management backend verification
```

### 4. 配置环境变量

```bash
# 复制模板
cp .env.example .env

# 生成 JWT_SECRET
JWT_SECRET=$(openssl rand -base64 32)

# 编辑配置
nano .env

# 设置以下变量:
JWT_SECRET=$JWT_SECRET  # 上面生成的值
DB_HOST=localhost
DB_PORT=5432
DB_NAME=pocket_db
DB_USER=pocket_user
DB_PASSWORD=your_secure_password_here
DB_SSLMODE=disable

# 验证配置
cat .env | grep -v PASSWORD
```

### 5. 构建 Backend

```bash
# 进入 backend 目录
cd /opt/opencode-pocket/backend

# 下载依赖
go mod download

# 构建二进制
go build -o pocketd ./cmd/pocketd

# 验证构建
./pocketd --help
ls -lh pocketd
```

### 6. 构建 Frontend (可选)

```bash
# 如果需要更新前端资源
cd /opt/opencode-pocket/frontend

# 安装依赖
npm install

# 构建生产版本
npm run build

# 验证构建产物
ls -lh dist/
```

### 7. 配置 Systemd 服务

```bash
# 创建服务文件
sudo nano /etc/systemd/system/pocketd.service

# 添加以下内容:
[Unit]
Description=OpenCode Pocket Backend API
After=network.target postgresql.service
Wants=postgresql.service

[Service]
Type=simple
User=your_user
WorkingDirectory=/opt/opencode-pocket/backend
EnvironmentFile=/opt/opencode-pocket/.env
ExecStart=/opt/opencode-pocket/backend/pocketd
Restart=on-failure
RestartSec=5s
StandardOutput=journal
StandardError=journal

# 安全加固
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target

# 重载 systemd
sudo systemctl daemon-reload

# 启用并启动服务
sudo systemctl enable pocketd
sudo systemctl start pocketd

# 检查状态
sudo systemctl status pocketd
```

### 8. 验证部署

```bash
# 运行自动验证脚本
cd /opt/opencode-pocket/deploy
chmod +x verify.sh
./verify.sh

# 或手动验证:

# 1. 检查服务状态
sudo systemctl status pocketd

# 2. 检查日志
sudo journalctl -u pocketd -n 50

# 3. 测试 API
# 健康检查
curl http://localhost:8088/api/health

# 登录测试
curl -X POST http://localhost:8088/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin"}'

# 获取 token 并测试 tasks API
TOKEN=$(curl -s -X POST http://localhost:8088/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin"}' | jq -r .token)

curl http://localhost:8088/api/tasks \
  -H "Authorization: Bearer $TOKEN"
```

## 配置反向代理 (推荐)

### 使用 Nginx

```bash
# 安装 Nginx
sudo apt install -y nginx

# 创建站点配置
sudo nano /etc/nginx/sites-available/opencode-pocket

# 添加配置:
server {
    listen 80;
    server_name your-domain.com;

    # API 代理
    location /api/ {
        proxy_pass http://localhost:8088;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection 'upgrade';
        proxy_set_header Host $host;
        proxy_cache_bypass $http_upgrade;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    # WebSocket 支持
    location /ws/ {
        proxy_pass http://localhost:8088;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }

    # 前端静态文件 (如果 backend 不提供)
    location / {
        root /opt/opencode-pocket/frontend/dist;
        try_files $uri $uri/ /index.html;
    }
}

# 启用站点
sudo ln -s /etc/nginx/sites-available/opencode-pocket /etc/nginx/sites-enabled/
sudo nginx -t
sudo systemctl reload nginx
```

### 配置 SSL (Let's Encrypt)

```bash
# 安装 Certbot
sudo apt install -y certbot python3-certbot-nginx

# 获取证书
sudo certbot --nginx -d your-domain.com

# 自动续期
sudo systemctl enable certbot.timer
```

## 监控和维护

### 日志管理

```bash
# 查看实时日志
sudo journalctl -u pocketd -f

# 查看最近错误
sudo journalctl -u pocketd -p err -n 100

# 按时间范围查看
sudo journalctl -u pocketd --since "1 hour ago"
```

### 性能监控

```bash
# 检查进程资源
top -p $(pgrep pocketd)

# 检查数据库连接
sudo -u postgres psql -d pocket_db -c "SELECT count(*) FROM pg_stat_activity WHERE datname='pocket_db';"

# 检查磁盘使用
df -h /opt/opencode-pocket
```

### 数据库备份

```bash
# 创建备份脚本
sudo nano /opt/opencode-pocket/backup.sh

#!/bin/bash
BACKUP_DIR="/var/backups/pocket-db"
DATE=$(date +%Y%m%d_%H%M%S)
mkdir -p $BACKUP_DIR
pg_dump -U pocket_user -d pocket_db -h localhost > $BACKUP_DIR/pocket_db_$DATE.sql
# 保留最近 7 天的备份
find $BACKUP_DIR -name "*.sql" -mtime +7 -delete

# 设置权限
sudo chmod +x /opt/opencode-pocket/backup.sh

# 添加到 crontab (每天凌晨 2 点)
sudo crontab -e
0 2 * * * /opt/opencode-pocket/backup.sh
```

## 故障排查

### Backend 无法启动

```bash
# 检查日志
sudo journalctl -u pocketd -n 100

# 常见问题:
# 1. 数据库连接失败
#    → 检查 .env 中的 DB_* 变量
#    → 确认 PostgreSQL 运行: sudo systemctl status postgresql

# 2. 端口被占用
#    → 检查: sudo lsof -i :8088
#    → 停止冲突进程或修改端口

# 3. 权限问题
#    → 检查文件所有者: ls -la /opt/opencode-pocket/backend/pocketd
#    → 修正: sudo chown your_user:your_user pocketd
```

### API 返回 401/403

```bash
# 1. 检查 JWT_SECRET
cat /opt/opencode-pocket/.env | grep JWT_SECRET

# 2. 重新获取 token
curl -X POST http://localhost:8088/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin"}'

# 3. 检查 token 是否过期
# JWT token 默认有效期检查
```

### 数据库连接失败

```bash
# 测试连接
PGPASSWORD=your_password psql -h localhost -U pocket_user -d pocket_db -c "SELECT 1;"

# 检查 PostgreSQL 状态
sudo systemctl status postgresql

# 查看 PostgreSQL 日志
sudo tail -f /var/log/postgresql/postgresql-*.log

# 检查防火墙
sudo ufw status
```

## 更新部署

```bash
# 1. 拉取最新代码
cd /opt/opencode-pocket
git pull origin main

# 2. 重新构建
cd backend
go build -o pocketd ./cmd/pocketd

# 3. 重启服务
sudo systemctl restart pocketd

# 4. 验证
sudo systemctl status pocketd
curl http://localhost:8088/api/health
```

## 回滚操作

```bash
# 1. 停止服务
sudo systemctl stop pocketd

# 2. 回滚代码
cd /opt/opencode-pocket
git log --oneline -5  # 找到之前的 commit
git checkout <previous_commit_hash>

# 3. 重新构建
cd backend
go build -o pocketd ./cmd/pocketd

# 4. 恢复数据库 (如果需要)
PGPASSWORD=your_password psql -h localhost -U pocket_user -d pocket_db < /var/backups/pocket-db/backup.sql

# 5. 重启服务
sudo systemctl start pocketd

# 6. 验证
sudo systemctl status pocketd
```

## 安全加固

### 1. 防火墙配置

```bash
# 启用 UFW
sudo ufw enable

# 允许必要端口
sudo ufw allow 22/tcp    # SSH
sudo ufw allow 80/tcp    # HTTP
sudo ufw allow 443/tcp   # HTTPS

# 如果直接暴露 backend (不推荐)
sudo ufw allow 8088/tcp

# 查看规则
sudo ufw status
```

### 2. PostgreSQL 安全

```bash
# 修改 PostgreSQL 配置
sudo nano /etc/postgresql/*/main/pg_hba.conf

# 确保只允许本地连接
# local   all             all                                     peer
# host    all             all             127.0.0.1/32            md5

# 重启 PostgreSQL
sudo systemctl restart postgresql
```

### 3. 定期更新

```bash
# 系统更新
sudo apt update && sudo apt upgrade -y

# Go 依赖更新
cd /opt/opencode-pocket/backend
go get -u ./...
go mod tidy
```

## 性能优化

### Database 优化

```sql
-- 添加索引
CREATE INDEX idx_tasks_created_at ON tasks(created_at DESC);

-- 分析表
ANALYZE tasks;

-- 查看慢查询
SELECT * FROM pg_stat_statements ORDER BY mean_exec_time DESC LIMIT 10;
```

### Backend 优化

```bash
# 调整环境变量
# 在 .env 中添加:
GOMAXPROCS=4              # 根据 CPU 核心数
DB_MAX_OPEN_CONNS=25      # 数据库连接池大小
DB_MAX_IDLE_CONNS=5
```

## 技术支持

- **GitHub**: https://github.com/halfking/pocket-opencode
- **文档**: /docs 目录
- **问题反馈**: GitHub Issues

---

**部署完成后请进行完整的功能测试，确认所有 API 端点正常工作。**
