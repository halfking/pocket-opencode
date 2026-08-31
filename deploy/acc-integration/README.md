# ACC Integration 部署方案

本部署方案用于将 OpenCode Pocket 集成到 ACC（AI Coding Center）本地开发环境中，与其他服务共享基础设施。

## ⚠️ 本地 PG 共享策略（强约束）

> **本工作区统一规定：本地只有一套共享 PG 实例。acc-integration 模式下唯一允许接入的是 `llm-gateway-pg`（宿主端口 `5432`，由 ACC / llm-gateway 启动），其中包含真实业务库 `kaixuan`（用户名 `llm_gateway`）。本仓库及本目录任何脚本/Compose 一律不得再启动任何 PG 容器（`postgres:*` / `*citus*` / 自定义 PG），且不得删除/重建 `llm-gateway-pg`。**

具体规则：

1. **禁止新启 PG 实例**：本目录的 `docker-compose.*.yml` 与所有 `*.sh` 脚本中禁止出现 `image: postgres*`、`image: *citus*`、任何带 `postgres`/`pg`/`citus` 字样的 `container_name`、或独立 `pg-data` 卷。
2. **禁止删除共享 PG**：禁止对 `llm-gateway-pg` 执行 `docker stop / rm / down / volume rm / docker system prune` 等销毁性操作；本目录脚本不得调用任何 `docker rm` / `docker volume rm` / `docker compose down --volumes` 命令。
3. **禁止 reset / drop 数据库**：禁止 `DROP DATABASE`、`TRUNCATE`、`pg_resetwal`；如需清空业务数据，仅限 `kaixuan` 下的 Pocket 业务 schema，禁止触碰 `postgres` / `llm_gateway` / `kxmemory_rls` 等共享库。
4. **冲突时优先保留共享 PG**：若发现本地有同名/同端口的孤儿 PG 容器，先停掉应用再由维护者确认后再清理，**绝不允许直接 `docker rm -f llm-gateway-pg`**。

违反以上任一条会导致其他依赖 `llm-gateway-pg` 的服务（llm-gateway-go、ACC、memora、kxmemory、RedClaw、openpocket 自身）出现级联数据丢失。

## 🎯 方案定位

**ACC Integration** 是为本地开发和集成测试设计的部署模式，与 **本地方案**（`deploy/本地方案/`）的主要区别：

| 特性 | ACC Integration | 本地方案 |
|------|----------------|----------|
| **PostgreSQL** | 复用共享的 `llm-gateway-pg` 容器 | 独立的 PostgreSQL 容器 |
| **Docker 网络** | 加入 `acc-local-net`、`shared-infra` | 独立的 `r112_net` |
| **基础镜像** | 使用离线的 `kx-base:go-vue-optimized` | 使用公共 `golang:alpine` |
| **适用场景** | 与 ACC、LLM Gateway 等服务集成开发 | 完全独立的本地开发 |
| **依赖复杂度** | 需要预加载镜像和共享服务 | 自包含，依赖少 |

---

## 📋 前置条件

### 1. 共享 PostgreSQL 容器

必须先启动 `llm-gateway-pg` 容器，该容器：
- 容器名：`llm-gateway-pg`
- 宿主端口：`5432`
- 网络：`acc-local-net`、`shared-infra`
- 数据库：包含 `kaixuan` 数据库
- 用户：`llm_gateway`

**启动方式**：
```bash
# 通常由 llm-gateway 或 ACC 环境的启动脚本自动创建
# 如需手动启动，参考 llm-gateway 项目的文档
```

### 2. Docker 基础镜像（离线）

需要预先加载 `kx-base:go-vue-optimized` 镜像：

```bash
# 加载离线镜像包
docker load -i ~/work/docker-base-images/lang-base/kx-base-go-vue-v2-alpine-slim-arm64.tar.gz

# 验证镜像已加载
docker images | grep kx-base
# 应显示: kx-base    go-vue-optimized    ...
```

**镜像说明**：
- 基于 Alpine Linux
- 预装 Go 1.26 + Node.js + pnpm
- 优化的构建层缓存
- 仅 ACC Integration 模式需要

### 3. Docker 网络

以下网络会由启动脚本自动创建（无需手动操作）：
- `acc-local-net` - ACC 本地服务通信网络
- `shared-infra` - 共享基础设施网络

---

## 🚀 快速启动

### 1. 配置环境变量

```bash
cd deploy/acc-integration

# 复制环境配置模板
cp .env.example .env

# 编辑 .env 文件，关键配置：
# - POCKET_HTTP_PORT=8088
# - POCKET_FRONTEND_HOST_PORT=4175
# - POCKET_POSTGRES_DSN=postgres://llm_gateway:xxx@llm-gateway-pg:5432/kaixuan?sslmode=disable
```

### 2. 启动服务

```bash
# 一键启动（自动检查依赖、构建镜像、启动服务）
./local-up.sh
```

**启动脚本会自动执行**：
1. ✅ 检查并创建所需的 Docker 网络
2. ✅ 验证 `kx-base` 镜像已加载
3. ✅ 检查 `llm-gateway-pg` 容器可用性
4. ✅ 验证数据库身份（防止误连错误实例）
5. ✅ 构建并启动 `pocketd` 和 `frontend` 服务
6. ✅ 等待健康检查通过

### 3. 验证部署

```bash
# 检查服务状态
docker compose ps

# 测试 Backend API
curl http://localhost:8088/healthz

# 测试 Frontend
curl http://localhost:4175/healthz

# 查看日志
docker compose logs -f pocketd
```

---

## 📁 目录结构

```
deploy/acc-integration/
├── README.md                 # 本文档
├── docker-compose.yml        # Docker Compose 配置
├── .env.example              # 环境变量模板
├── .env                      # 实际环境变量（需创建）
├── local-up.sh               # 启动脚本
├── local-down.sh             # 停止脚本
└── OFFLINE_IMAGES.md         # 离线镜像说明
```

---

## 🔧 常用操作

### 停止服务

```bash
./local-down.sh
# 或
docker compose down
```

### 重启服务

```bash
# 完全重建
docker compose up -d --build --force-recreate

# 仅重启
docker compose restart
```

### 查看日志

```bash
# 实时日志
docker compose logs -f

# 最近 100 行
docker compose logs --tail=100 pocketd
```

### 进入容器调试

```bash
# 进入 pocketd 容器
docker compose exec pocketd sh

# 进入 PostgreSQL
docker exec -it llm-gateway-pg psql -U llm_gateway -d kaixuan
```

---

## 🐛 常见问题

### 1. 网络不存在错误

**错误信息**：
```
acc-local-net does not exist
```

**解决方法**：
启动脚本已自动创建网络，如仍报错，手动执行：
```bash
docker network create acc-local-net
docker network create shared-infra
```

### 2. kx-base 镜像未找到

**错误信息**：
```
kx-base:go-vue-optimized not loaded
```

**解决方法**：
```bash
docker load -i ~/work/docker-base-images/lang-base/kx-base-go-vue-v2-alpine-slim-arm64.tar.gz
```

### 3. PostgreSQL 连接失败

**错误信息**：
```
llm-gateway-pg container not found
```

**解决方法**：
确保 llm-gateway 或 ACC 环境已启动，`llm-gateway-pg` 容器正在运行：
```bash
docker ps | grep llm-gateway-pg
```

### 4. 端口冲突

**错误信息**：
```
Bind for 0.0.0.0:8088 failed: port is already allocated
```

**解决方法**：
修改 `.env` 中的端口配置：
```bash
POCKET_HTTP_PORT=8089
POCKET_FRONTEND_HOST_PORT=4176
```

---

## 🔄 与本地方案的切换

如果需要切换到完全独立的本地部署：

```bash
# 1. 停止 ACC Integration
cd deploy/acc-integration
./local-down.sh

# 2. 启动本地方案
cd ../本地方案
./local-up.sh
```

本地方案会启动独立的 PostgreSQL 容器，无需依赖共享服务。

---

## 📊 服务拓扑

```
┌─────────────────────────────────────────────────────┐
│           ACC Local Development Network             │
│                  (acc-local-net)                    │
│                                                     │
│  ┌──────────────┐      ┌──────────────┐           │
│  │  acc-go      │      │  pocketd     │           │
│  │  (可选)      │◄────►│  :8088       │           │
│  └──────────────┘      └──────┬───────┘           │
│                               │                    │
│  ┌──────────────────────────┐ │                    │
│  │   llm-gateway-pg         │◄┘                    │
│  │   :5432                  │                      │
│  │   database: kaixuan      │                      │
│  └──────────────────────────┘                      │
│                                                     │
└─────────────────────────────────────────────────────┘

                        │
                        ▼
              ┌──────────────────┐
              │   frontend       │
              │   :4175          │
              │   (nginx)        │
              └──────────────────┘
                        │
                        ▼ (宿主机端口映射)
                   用户浏览器
```

---

## 📝 相关文档

- [OpenCode Pocket 主文档](../../README.md)
- [运维指南](../../OPERATIONS_GUIDE.md)
- [API 文档](../../docs/API.md)
- [本地方案文档](../本地方案/README.md)（待创建）

---

## 🤝 贡献

如发现文档错误或有改进建议，请：
1. 在项目仓库提交 Issue
2. 或直接提交 Pull Request

---

**最后更新**: 2026-08-30
