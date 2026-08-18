# PG / Docker 集成审计报告（2026-08-18，session 04）

## 审计结论

**评级：A-（本次目标路径通过）**

审计范围：

- `97527f7` PG 切换与 schema 隔离
- `0e84856` compose 凭据移除
- 当前 `main` 的 `backend/internal/db/pg.go`、配置、Docker 集成脚本和验证报告
- llm-gateway-pg 运行时联调

结果：

- 发现并修复 4 类问题：凭据残留、PG 未就绪时误启动、nginx 旧 upstream IP 502、schema 标识符/系统 schema 边界
- Backend 全量测试：通过
- `go vet ./...`：通过
- Frontend production build：通过
- ShellCheck + bash 语法检查：通过
- Docker 重建 + login + quota + nginx proxy：通过

## 已修复问题

### P1 — 验证报告泄露凭据

**问题**：历史集成报告曾包含真实 `llm-gateway-pg` 密码、旧 PG DSN、`admin1234` 开发密码和 `kxuser/kxpass`。

**修复**：

- 脱敏 `docker-acc-integration-2026-08-18.md`
- 脱敏 `docker-acc-integration-verify-2026-08-18.md`
- 脱敏 `docker-llm-gateway-pg-switch-2026-08-18.md`
- `.env.example` 改为 `<set-locally>` / `<SET_LOCALLY_MIN_8_CHARS>`
- `local-up.sh` 不再打印登录密码

### P1 — PG 缺失时可能误连宿主 5432

**问题**：`local-up.sh` 对 `llm-gateway-pg` 未就绪只打印 warning，但 compose 通过 `host.docker.internal:5432` 连接；宿主端口可能被其他 PostgreSQL 服务占用，导致 pocketd 连接到错误实例。

**修复**：

- 容器不存在：直接失败
- `pg_isready` 失败：直接失败
- 不再“继续启动并等待 pocketd 自己重试”

### P1 — pocketd 重建后 nginx upstream 502

**问题**：pocketd 容器 IP 变化后，旧 frontend nginx 仍缓存旧 `pocketd` IP，`/api/*` 返回 502。

**修复**：

- `local-up.sh` 使用 `--force-recreate pocketd frontend`
- 同时等待 backend 和 frontend `/healthz`
- 最终 Docker 验证中 nginx `/api/llm/quota` 已恢复通过

### P2 — schema 标识符安全与系统 schema 边界

**问题**：schema 名称虽然做了字符校验，但 SQL 标识符没有统一安全引用；`public` / `pg_catalog` 等保留 schema 也没有明确拒绝。

**修复**：

- 新增 `quoteIdent`
- `CREATE SCHEMA` 与 `SET search_path` 使用安全引用
- 拒绝 `public`、`pg_catalog`、`pg_toast`、`information_schema`
- 新增 `backend/internal/db/pg_test.go`
- 覆盖普通名称、非法字符、注入样式、过长名称、保留 schema、内嵌引号

## 验证结果

```text
Backend: go test ./...                 PASS
Backend: go vet ./...                 PASS
Frontend: vite build --mode ios-dev   PASS
ShellCheck + bash -n                  PASS
Docker: ./deploy/acc-integration/local-up.sh PASS
Login: local .env credentials          PASS
Backend quota API                     PASS
Nginx quota proxy                     PASS
Frontend health                       PASS
Startup migration errors              NONE
```

运行环境确认：

- `llm-gateway-pg` 继续作为唯一 PG 目标
- pocketd 表位于 `opencode_pocket` schema
- `public.tasks` 保持与其他模块隔离
- 真实 DSN 和密码只存在于本地 ignored `.env`

## 未在本轮扩大范围的历史风险

仓库仍有若干早期历史测试脚本和报告使用 `admin/admin` 或 `kxpass` 示例。这些不属于本次 PG 集成启动路径，但它们会误导开发者并与当前最小密码长度规则冲突。建议后续单独清理 legacy deployment docs/scripts，避免扩大本次变更范围。

## 剩余任务

1. 清理早期 legacy 脚本中的 `admin/admin` 示例，统一从本地 env 读取。
2. 为 `local-up.sh` 增加自动验证目标数据库身份（`current_database()` / `current_user`），避免仅依赖端口和 `pg_isready`。
3. 将本次 Docker smoke 纳入 CI 或 release gate。
4. RedClaw pocket endpoints 仍未实现，`POCKET_REDCLAW_BASE_URL` 保持空值。
5. AuditStore 仍是 in-memory，生产持久化审计仍未完成。
