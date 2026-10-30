# AI Work Mess 部署指南

**版本**: Phase 6 (commit 7223368)  
**日期**: 2026-07-05  
**状态**: ✅ 代码已合并到 main 分支并推送

## 部署概览

本指南用于将 AI Work Mess 部署到生产服务器。系统包含：
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
- Go: 1.27+
- PostgreSQL: 14+
- Node.js: 22+ (仅构建时需要)

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
go version      # 应显示 >= 1.27
node --version  # 应显示 >= 22
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

# tasks 表不用手工建：后端启动时由 backend/internal/task/store.go 的 migrate()
# 自动 CREATE TABLE + ALTER 迁移。权威 DDL 就在那个文件里。
# ⚠️ 2026-10-31：不要手工 CREATE TABLE。实际 schema 的 INSERT 列有 28 列
#    （workspace_id / pending_approvals / session_count / accepted_at / type /
#    owner_id / due_at / remind_at / tags / visibility / acc_* 等），
#    且 created_at / updated_at 是 BIGINT（Unix 秒，代码写入 time.Now().Unix()）。
#    手工建出的 VARCHAR + TIMESTAMP 版本会被 migrate() 的
#    CREATE TABLE IF NOT EXISTS 跳过（它只 ADD COLUMN，不改类型），
#    之后建任务会直接报类型不匹配。

# 验证表创建（后端启动过一次之后）
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
POCKET_JWT_SECRET=$(openssl rand -base64 32)

# 编辑配置
nano .env

# 设置以下变量:
# ⚠️ 2026-10-31：后端只读 POCKET_* 前缀的键（backend/internal/config/config.go）。
#    旧文档里的 JWT_SECRET / DB_HOST / DB_PORT / DB_NAME / DB_USER / DB_PASSWORD /
#    DB_SSLMODE 全仓无人读取，写进 .env 也不生效；生产启动时缺 POCKET_JWT_SECRET /
#    POCKET_POSTGRES_DSN 会直接报错退出。
POCKET_JWT_SECRET=$POCKET_JWT_SECRET  # 上面生成的值
POCKET_POSTGRES_DSN=postgres://pocket_user:your_secure_password_here@localhost:5432/pocket_db?sslmode=disable

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
ls -lh pocketd
# （pocketd 不解析命令行 flag：没有 --version / --help，直接跑会起服务并占用 8088）
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
Description=AI Work Mess Backend API
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
curl http://localhost:8088/healthz

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
cat /opt/opencode-pocket/.env | grep POCKET_JWT_SECRET

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
curl http://localhost:8088/healthz
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
-- ⚠️ 2026-10-17：下面这条**曾经是查不通的**。默认部署里
--    log_min_duration_statement = -1（不落慢 SQL 日志）且 pg_stat_statements
--    没安装 ⇒ 这条语句必然报 relation "pg_stat_statements" does not exist。
--    docker-compose.db.yml 现已默认开启两者（阈值 OPP_PG_SLOW_QUERY_MS，默认 500ms）。
--
--    优先用随初始化建好的视图（按**总**耗时排序 —— 跑得多的才是真正吃时间的）：
SELECT * FROM pocket_slow_statements LIMIT 20;

--    需要更细的口径时再直接查扩展表（calls 用于区分「跑得多」与「单次慢」）：
SELECT calls, round(total_exec_time::numeric, 1) AS total_ms,
       round(mean_exec_time::numeric, 2)  AS mean_ms, left(query, 160)
FROM pg_stat_statements
ORDER BY total_exec_time DESC LIMIT 20;

-- 实时看慢 SQL 日志（compose 已开 logging_collector，落在数据目录的 log/ 下）
--   docker exec opp-db-postgres sh -c 'tail -f /var/lib/postgresql/data/log/postgresql-$(date +%F).log'
```

> ⚠️ **存量实例必须补一次**（2026-10-17 加脚本）。
> `pg-init-slow-query.sql` 挂在 `/docker-entrypoint-initdb.d/`，而镜像**只在数据目录
> 为空的那一次**执行该目录 ⇒ 已经跑过初始化的实例**永远不会**再装它。
> ⇒ 上面两条查询在存量实例上必然报 `relation "pg_stat_statements" does not exist`。
>
> ```bash
> deploy/bin/pg-backfill-slow-query.sh            # 先查后做，幂等，完成后复核
> deploy/bin/pg-backfill-slow-query.sh --dry-run  # 只看会做什么
> ```
>
> 该脚本只做**运行期**能做的部分（建扩展 + 建视图），
> 并**报告** `shared_preload_libraries` 的状态而不擅自重启容器——
> `shared_preload_libraries` 只能走启动参数（`ALTER SYSTEM` 补不回来，要重启才生效），
> 它写在 `docker-compose.db.yml` 的 `command:` 里，所以存量实例重建一次容器即可带上：
>
> ```bash
> docker compose -p opp-db-postgres -f deploy/bin/docker-compose.db.yml up -d postgres
> ```

---

## PGDATA 在哪：命名卷 vs 宿主目录（2026-10-17 定案）

**默认是 Docker 命名卷**（`opp-pgdata`），**不再**把 PGDATA 绑到宿主目录。

| | 命名卷（默认） | 宿主 bind mount |
|---|---|---|
| 首次启动建库 | ✅ Docker 按镜像 uid 建目录，initdb 必然能跑 | ⚠️ macOS 上起不来，见下 |
| 数据在宿主可见 | ❌ 需 `docker cp` / `pg_dump` | ✅ 直接可翻 |
| 换一台机器 | `pg_dump` / `pg_dumpall` | 拷目录（**不跨 uid 场景**） |

**为什么从 bind mount 退回来**（不是偏好，三条实测/事实）：

1. **uid 70 是镜像实现细节**，是 Debian 系 postgres 镜像里 postgres 用户的 uid。
   把它固化进宿主目录属主 = 把部署焊死在某个镜像版本的用户表上。
2. **macOS 上「修」不了。** Docker Desktop 的 bind mount 过文件共享层，
   容器内 `chown` 不生效；宿主 `sudo chown 70:70` 也会被 virtiofs 重新呈现成 uid 501。
   ⇒ 属主不对 ⇒ initdb 的临时 `pg_ctl` 以 postgres 身份起不来 ⇒
   镜像入口看到 `PG_VERSION` 就「目录已有数据库，**跳过初始化**」⇒
   **`POSTGRES_DB` 根本没被创建**。（2026-10-17 在 macOS + Docker Desktop 上实机复现。）
3. 官方 postgres 镜像的入口脚本本就围绕「一个它自己拥有的目录」设计。

**确实需要宿主可见时**（备份工具、想直接翻文件）显式退回 bind mount：

```bash
export OPP_PG_DATA_MODE=bind          # PGDATA 走 ${POCKET_PG_DATA_DIR}
deploy/bin/ensure-databases.sh
```

⚠ **切换模式 = 换数据位置**（同名卷与宿主目录是两个不同的库）。
来回切会得到一个空库。切之前先 `pg_dump`。
在 macOS 上退回 bind mount 会让 `doctor.sh` 直接报 **fail**（不是 warn）——
因为那个环境下它一定会踩到上面那条故障链。

**怎么知道自己有没有踩过**：旧故障的症状是「容器 healthy，但 `pocket` 库不存在」。

```bash
docker exec opp-db-postgres psql -U llm_gateway -d pocket -tAc 'SELECT 1'   # 报错就是踩了
```

healthcheck 现在会真的把连接建起来（库/角色/密码任一不对即转红），
所以这类故障不会再以 healthy 的形态潜伏；但注意它有 **12 次 × 10s ≈ 2 分钟**
的判定延迟。

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
