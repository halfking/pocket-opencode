# OpenCode Pocket 本地统一方案

本目录是 OpenCode Pocket 本地部署、数据库复用和整体测试的唯一操作入口。

## 当前本地拓扑

```text
浏览器 / Android
       │
       ▼
frontend :4175 (nginx)
       │ /api、/ws、/plugin/ws
       ▼
pocketd :8088
       │
       ├── PostgreSQL: r112_postgres / 127.0.0.1:15432
       │       └── 独立数据库: pocket_local
       └── OpenCode（可选）: host.docker.internal:4096
```

Pocket 复用现有 `llm-gateway-go` Compose 创建的 `r112_postgres` 和 `r112_net`，不重复创建第二个 PostgreSQL，不接管共享容器生命周期。

## 端口

| 服务 | 地址 | 说明 |
| --- | --- | --- |
| Pocket API | `http://127.0.0.1:8088` | Go backend、REST、WebSocket |
| Pocket frontend | `http://127.0.0.1:4175` | Nginx 静态前端和 API 反代 |
| PostgreSQL | `127.0.0.1:15432` | 现有 `r112_postgres` 宿主映射 |
| OpenCode | `127.0.0.1:4096` | 可选，当前测试未要求运行 |

`4174` 已被本机已有 Node/Vite 进程占用，因此 Pocket 容器前端固定使用 `4175`。离线运行默认只使用已存在的本地镜像，不执行 `docker pull` 或 Docker build。

## 快速开始

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket

# 检查/创建隔离数据库 pocket_local，并生成 .env.local
./deploy/本地方案/local-db-init.sh

# 构建 ARM64 二进制、构建镜像、启动 Pocket API + frontend
./deploy/本地方案/local-up.sh

# 执行完整本地测试
./deploy/本地方案/local-test.sh

# 查看数据库表
./deploy/本地方案/local-db-check.sh

# 查看日志
./deploy/本地方案/local-logs.sh pocketd

# 停止 Pocket 自有容器（不删除共享 PG，不删除卷）
./deploy/本地方案/local-down.sh
```

## 数据安全边界

- 默认数据库为 `pocket_local`，不使用共享 PG 的 `postgres` 默认库。
- `local-db-init.sh` 只执行 `CREATE DATABASE IF NOT EXISTS` 等价的存在性检查；不会 reset、drop 或清理数据库。
- `local-down.sh` 不带 `--volumes`，不会删除 Pocket 数据卷。
- 任何 reset/drop/删除容器操作必须单独确认，不属于默认测试流程。

## 测试分层

`local-test.sh` 按以下顺序执行：

1. backend `go test ./... -count=1`
2. backend `go vet ./...`
3. backend `go build ./cmd/pocketd`
4. frontend `npm run typecheck`
5. frontend `npm run build`
6. API `/healthz`、frontend `/healthz`
7. `admin/admin` 登录和 token 校验
8. notes、notifications、workspaces 受保护 API
9. identity、agentbridge、lobster、notifycenter PostgreSQL 集成测试

每次结果写入 `artifacts/<timestamp>/`。token 不写入测试报告，只保存脱敏的登录结果和执行日志。

## 外部依赖边界

默认不阻塞于以下外部服务：

- OpenCode `4096`
- kxmemory
- Groq/STT
- 邮件 IMAP/OAuth
- ACC MCP
- Android emulator

如需验证这些链路，应在服务可用后单独运行对应集成脚本，并在报告中区分 `passed`、`skipped` 和 `blocked`。
