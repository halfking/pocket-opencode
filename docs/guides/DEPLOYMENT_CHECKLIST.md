# 部署前检查清单

**日期**: 2026-07-05  
**版本**: Phase 6 (commit 7223368)  
**目标环境**: 生产服务器

## 前置条件

### 1. 服务器环境
- [ ] Ubuntu/Debian Linux
- [ ] Go 1.27+ 已安装
- [ ] PostgreSQL 14+ 已安装并运行
- [ ] Node.js 22+ 已安装（用于前端构建）
- [ ] Git 已安装

### 2. 数据库准备
- [ ] PostgreSQL 服务运行中
- [ ] 创建数据库用户 `pocket_user`
- [ ] 设置数据库密码（强密码）
- [ ] 创建数据库 `pocket_db`
- [ ] 授予用户权限

### 3. 网络配置
- [ ] 服务器可访问（SSH）
- [ ] 端口 8088 开放（backend API）
- [ ] 防火墙规则配置
- [ ] SSL 证书准备（可选，生产建议）

### 4. 环境变量
- [ ] POCKET_JWT_SECRET 已生成（强随机字符串）
- [ ] POCKET_POSTGRES_DSN 已设置（含库名、账号与密码）
- [ ] 所有必需的 .env 变量已准备

## 部署步骤

### Phase 1: 代码部署

```bash
# 1. SSH 到服务器
ssh user@your-server

# 2. 克隆或更新仓库
git clone git@github.com:halfking/pocket-opencode.git
cd pocket-opencode
git checkout main
git pull origin main

# 3. 验证代码版本
git log --oneline -1
# 应显示: 7223368 feat(phase6): UI z-index fix + task management backend verification
```

### Phase 2: 数据库初始化

```bash
# 1. 进入 deploy 目录
cd deploy

# 2. 运行数据库初始化
# ⚠️ 2026-10-31：deploy/deploy.sh 只接受 --env / --tag / --dry-run / --help，
#    没有 init-db 子命令（传 init-db 会报「未知参数: init-db」并退出 1）——
#    建库建角色请手工执行下面的 SQL。
psql -U postgres << SQL
CREATE USER pocket_user WITH PASSWORD 'your_secure_password';
CREATE DATABASE pocket_db OWNER pocket_user;
GRANT ALL PRIVILEGES ON DATABASE pocket_db TO pocket_user;
SQL

# 3. tasks 表不用手工建：后端启动时由 backend/internal/task/store.go 的 migrate()
#    自动 CREATE TABLE + ALTER 迁移（权威 DDL 就在那个文件里）。
#    ⚠️ 2026-10-31：不要手工 CREATE TABLE。实际 schema 的 INSERT 列有 28 列
#    （workspace_id / pending_approvals / session_count / accepted_at / type /
#    owner_id / due_at / remind_at / tags / visibility / acc_* 等），
#    且 created_at / updated_at 是 BIGINT（Unix 秒，代码写入 time.Now().Unix()）。
#    手工建出的 VARCHAR + TIMESTAMP 版本会被 migrate() 的
#    CREATE TABLE IF NOT EXISTS 跳过（它只 ADD COLUMN，不改类型），
#    之后建任务会直接报类型不匹配。

# 4. 验证表创建（后端启动过一次之后）
psql -U pocket_user -d pocket_db -c "\dt"
```

### Phase 3: 配置环境变量

```bash
# 1. 创建 .env 文件
cd /path/to/pocket-opencode
cp .env.example .env

# 2. 编辑配置
nano .env

# 必需变量:
# ⚠️ 2026-10-31：后端只读 POCKET_* 前缀的键（backend/internal/config/config.go）。
#    旧文档里的 JWT_SECRET / DB_HOST / DB_PORT / DB_NAME / DB_USER / DB_PASSWORD /
#    DB_SSLMODE 全仓无人读取，写进 .env 也不生效；生产启动时缺 POCKET_JWT_SECRET /
#    POCKET_POSTGRES_DSN 会直接报错退出。
POCKET_JWT_SECRET=your_generated_secret_key_here
POCKET_POSTGRES_DSN=postgres://pocket_user:your_secure_password@localhost:5432/pocket_db?sslmode=disable

# 3. 生成 POCKET_JWT_SECRET (推荐)
openssl rand -base64 32
```

### Phase 4: 构建后端

```bash
# 1. 进入 backend 目录
cd backend

# 2. 下载依赖
go mod download

# 3. 构建二进制
go build -o pocketd ./cmd/pocketd

# 4. 验证构建
ls -lh pocketd
# （pocketd 不解析命令行 flag：没有 --version / --help，直接跑会起服务并占用 8088）
```

### Phase 5: 构建前端

```bash
# 1. 进入 frontend 目录
cd ../frontend

# 2. 安装依赖
npm install

# 3. 构建生产版本
npm run build

# 4. 验证构建产物
ls -lh dist/
# 应包含: index.html, assets/
```

### Phase 6: 启动服务

```bash
# 方法 A: 直接运行（测试）
cd backend
./pocketd

# 方法 B: 使用 systemd（生产推荐）
sudo nano /etc/systemd/system/pocketd.service

# 添加内容:
[Unit]
Description=AI Work Mess Backend
After=network.target postgresql.service

[Service]
Type=simple
User=your_user
WorkingDirectory=/path/to/pocket-opencode/backend
EnvironmentFile=/path/to/pocket-opencode/.env
ExecStart=/path/to/pocket-opencode/backend/pocketd
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target

# 启用并启动服务
sudo systemctl daemon-reload
sudo systemctl enable pocketd
sudo systemctl start pocketd

# 查看状态
sudo systemctl status pocketd
```

### Phase 7: 验证部署

```bash
# 1. 运行自动化验证脚本
cd deploy
./verify.sh

# 2. 手动验证 API
# 健康检查
curl http://localhost:8088/healthz

# 登录测试
curl -X POST http://localhost:8088/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin"}'

# 任务列表
TOKEN="your_jwt_token_here"
curl http://localhost:8088/api/tasks \
  -H "Authorization: Bearer $TOKEN"

# 3. 检查日志
sudo journalctl -u pocketd -f
```

## 验证检查点

### Backend API
- [ ] `/healthz` 返回 200 OK
- [ ] `/api/auth/login` 可以登录获取 JWT
- [ ] `/api/tasks` (GET) 返回任务列表
- [ ] `/api/tasks` (POST) 可以创建任务
- [ ] `/api/instances` 返回实例列表

### 数据库
- [ ] PostgreSQL 连接正常
- [ ] `tasks` 表存在
- [ ] 可以插入/查询数据
- [ ] 索引已创建

### 性能
- [ ] API 响应时间 < 200ms
- [ ] 数据库查询优化
- [ ] 内存使用正常（< 500MB）

### 安全
- [ ] POCKET_JWT_SECRET 是强随机值
- [ ] 数据库密码是强密码
- [ ] 敏感信息不在日志中
- [ ] CORS 配置正确

## 故障排查

### Backend 启动失败
```bash
# 检查日志
sudo journalctl -u pocketd -n 50

# 常见问题:
# 1. 数据库连接失败 → 检查 .env 配置
# 2. 端口占用 → netstat -tulpn | grep 8088
# 3. 权限问题 → chown/chmod 调整
```

### 数据库连接失败
```bash
# 测试连接
psql -U pocket_user -d pocket_db -h localhost

# 检查 PostgreSQL 状态
sudo systemctl status postgresql

# 查看 PostgreSQL 日志
sudo tail -f /var/log/postgresql/postgresql-*.log
```

### API 返回 401/403
```bash
# 检查 POCKET_JWT_SECRET 是否一致
grep POCKET_JWT_SECRET .env

# 重新生成 token
curl -X POST http://localhost:8088/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin"}'
```

## 回滚计划

如果部署失败需要回滚：

```bash
# 1. 停止服务
sudo systemctl stop pocketd

# 2. 回滚代码
git checkout <previous_commit>

# 3. 重新构建
cd backend && go build -o pocketd ./cmd/pocketd

# 4. 恢复数据库（如果需要）
psql -U pocket_user -d pocket_db < backup.sql

# 5. 重启服务
sudo systemctl start pocketd
```

## 监控与维护

### 日志监控
```bash
# 实时查看日志
sudo journalctl -u pocketd -f

# 查看最近错误
sudo journalctl -u pocketd -p err -n 50
```

### 性能监控
```bash
# CPU/内存使用
top -p $(pgrep pocketd)

# 数据库连接数
psql -U pocket_user -d pocket_db -c "SELECT count(*) FROM pg_stat_activity;"
```

### 备份策略
```bash
# 数据库备份
pg_dump -U pocket_user pocket_db > backup_$(date +%Y%m%d_%H%M%S).sql

# 定期备份（crontab）
0 2 * * * /path/to/backup_script.sh
```

## 联系信息

- **技术支持**: [support email]
- **紧急联系**: [emergency contact]
- **文档**: /docs in repository
- **监控面板**: [monitoring URL]

---

**部署完成后请勾选所有检查项，并保留此清单用于审计。**
