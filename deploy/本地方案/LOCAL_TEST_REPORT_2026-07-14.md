# 本地整体测试报告

- 日期：2026-07-14
- Docker context：`desktop-linux`
- PostgreSQL：`r112_postgres`，PG 17，宿主 `127.0.0.1:15432`
- Pocket 数据库：独立 `pocket_local`
- Pocket API：`8088`
- Pocket frontend：`4175`

## 结果

| 检查项 | 结果 |
| --- | --- |
| PostgreSQL 健康检查 | PASS |
| `pocket_local` 创建/连接 | PASS |
| Pocket schema 表初始化 | PASS，17 张 public 表 |
| backend `go test ./...` | PASS |
| backend `go vet ./...` | PASS |
| backend `go build ./cmd/pocketd` | PASS |
| frontend `npm run typecheck` | PASS |
| frontend `npm run build` | PASS |
| Pocket `/healthz` | PASS |
| frontend `/healthz` | PASS |
| `admin/admin` 登录 | PASS |
| notes / notifications / workspaces API | PASS |
| identity PG 集成测试 | PASS |
| agentbridge PG 集成测试 | PASS |
| lobster PG 集成测试 | PASS |
| notifycenter PG 集成测试 | PASS |
| OpenCode `4096` 集成 | SKIPPED，未运行 OpenCode |
| kxmemory/邮件/ACC/Android | SKIPPED，非核心本地闭环 |

## 测试产物

完整日志位于：

`deploy/本地方案/artifacts/20260714-170320/`

其中包含 backend/frontend 日志、API 响应、PG 集成测试日志和 `summary.txt`。JWT token 未写入报告。

## 已知提示

- Docker 构建阶段 npm 报告 4 个依赖漏洞（3 moderate、1 high）；本轮未执行自动升级，避免引入无关依赖变更。
- Vite 报告 Capacitor 动态/静态导入 chunk 提示，不影响构建结果。
- 本地 OpenCode 实例目录显示 offline，这是预期的可选外部依赖，不影响核心 Pocket + PostgreSQL 测试。
- 旧的根 `deploy/build-image.sh` 仍依赖联网多阶段 Dockerfile；本地方案使用 `Dockerfile.runtime` 离线路径。
