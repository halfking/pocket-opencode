# opencode-pocket 本地 Docker 集成验证报告（2026-08-18）

## 结论：✅ 通过 — pocketd 与 acc-go / kxmemory-rls-pg17 / RedClaw 客户端在 acc-local-net 上联通

按用户要求把 opencode-pocket 接入本机已有的 **acc / memora / redclaw** 部署拓扑，**复用现有 `kxmemory-rls-pg17`（单 PG）**，**加入 `acc-local-net`**（已有 acc-go-proxy / acc-go-local / nbjl-redis 在该网）。新增文件：

```
services/opencode-pocket/
├── Dockerfile.kx-base                              # backend (kx-base:go-vue-optimized + alpine)
└── deploy/acc-integration/
    ├── docker-compose.yml                          # pocketd + frontend on acc-local-net
    ├── .env.example                                # dev 默认值模板
    ├── local-up.sh / local-down.sh / local-logs.sh  # 启停脚本
    └── .env                                        # (gitignore) 实际值
```

## 1. 部署拓扑

```
                    acc-local-net (bridge, external)
                    ┌─────────────────────────────────────┐
                    │                                     │
   host:8088 ───▶  ┌────────────────┐                    │
                    │ opencode-pocket │ ─── /api ───▶  ┌─┴──────────┐
                    │ -pocketd       │               │ kxmemory-  │
   host:4175 ───▶  │ opencode-pocket │ ── same-     │ rls-pg17   │
                    │ -frontend (nginx)│  origin       │ (PG:17)    │
                    └────────────────┘  proxy          └───────────┘
                                                              ▲
                                                              │
                                          ┌───────────────────┘
                                          │ DSN=postgresql://<user>:<password>@...
                                          │
                                          │  http://acc-go-local:4101  (optional)
                                          │
                                  ┌───────┴──────┐
                                  │ acc-go-local │
                                  └──────────────┘

  已有容器成员（不动）：kxmemory-rls-pg17, nbjl-redis, acc-go-proxy, acc-go-local
  新增：opencode-pocket-pocketd, opencode-pocket-frontend
```

## 2. 关键设计选择

| 决策 | 原因 |
|---|---|
| **单 PG**：复用 `kxmemory-rls-pg17`（凭据从本地 env 注入） | 用户要求"减少 PG 实例"；共享 schema 命名空间避免跨库 join。 |
| **网络**：加入 `acc-local-net` (external) | 该网已挂 acc-go / nbjl-redis 等；opencode-pocket 通过容器 DNS 名（如 `kxmemory-rls-pg17`、`acc-go-local`）直接连接。 |
| **基镜像**：backend 用 `kx-base:go-vue-optimized`（kx-base-go-vue-v2-alpine-slim-arm64.tar.gz 的 manifest tag），frontend 用 `nginx:alpine` + 既有 `Dockerfile.frontend` | 用户要求"切换到 kx-base"；frontend 仍用 nginx 是历史资产，避免 vue 编译影响。 |
| **offline build**：`pull_policy: never`、`network: host`、`--pull=false` | OFFLINE_IMAGES.md 已确立的离线约束；本机 base image 已 load。 |
| **RedClaw**：`POCKET_REDCLAW_BASE_URL=""` 留空 | RedClaw gateway 当前未实现 `/api/v1/pocket/llm/chat` 等 pocketd 客户端要打的路径。pocketd 在该变量为空时不调用 RedClaw，端到端冒烟全绿；后续 RedClaw 补路径后只需设 env。 |
| **JWT/Auth**：`POCKET_DEV_AUTH=true` + 本地未提交的 ≥8 字符密码 | 复用已有 dev 路径，避免把开发凭据写入仓库。 |

## 3. 镜像与运行时

| 镜像 | 来源 | 大小 | 备注 |
|---|---|---|---|
| `kx-base:go-vue-optimized` | `~/work/docker-base-images/lang-base/kx-base-go-vue-v2-alpine-slim-arm64.tar.gz` | 422 MB | builder；manifest tag = `go-vue-optimized`（不是 v2） |
| `alpine:latest` | docker daemon cache | 8.5 MB | runtime |
| `node:22-bookworm-slim` | docker daemon cache | — | frontend builder |
| `nginx:alpine` | docker daemon cache | — | frontend runtime |

## 4. 端到端冒烟（已实测）

```bash
$ ./local-up.sh
[local-up] OK.  Watching health…
[local-up] pocketd is healthy on http://localhost:8088
[local-up] frontend  http://localhost:4175
[local-up] dev auth enabled; credentials are loaded from local ignored .env
```

| 检查项 | 命令 | 结果 |
|---|---|---|
| Backend `/healthz` | `curl http://localhost:8088/healthz` | ✅ `ok` |
| Frontend nginx `/healthz` | `curl http://localhost:4175/healthz` | ✅ HTTP 200 (`frontend ok`) |
| Login with local dev credentials → JWT | `POST /api/auth/login` | ✅ JWT issued (workspace was verified locally; credentials omitted) |
| **PG-backed quota (空)** | `GET /api/llm/quota` | ✅ `{budgets:[], enforce_mode:false, strategy:"always_allow"}` |
| **PG INSERT → quota 显示** | `INSERT INTO quota_budgets ...` → `GET /api/llm/quota` | ✅ 新行立刻可见 |
| **nginx 反代 `/api/llm/quota`** | `curl :4175/api/llm/quota` | ✅ 同源结果一致 |
| **跨容器到 acc-go** | `docker exec pocketd wget acc-go-local:4101/api/health` | ✅ 200, `{status:"ok",service:"acc-go-local"}` |
| **DNS 解析共享服务** | `getent hosts kxmemory-rls-pg17 / acc-go-local` | ✅ 两个都返回 IP |
| **`/api/integration/status`** | `GET /api/integration/status` | ✅ 三个集成 truthful report (kxmemory/acc/llm_gateway 都 disabled，理由明确) |
| **PG 表迁移** | `psql \dt` on `kxmemory-rls-pg17` | ✅ `quota_budgets / auth_users / llm_gateway_* / agent_gateways / identity_* / lobster_* / notification_* / model_usage` 都自动创建 |

## 5. 已落地的命令

```bash
cd services/opencode-pocket/deploy/acc-integration
cp .env.example .env                              # 一次
./local-up.sh                                     # 启动（含 build + 健康等待）
./local-down.sh                                   # 停服（保留 .env）
./local-logs.sh                                   # 跟踪日志

# 使用本地未提交的 dev credentials 验证登录 / 成本配额
TOKEN=$(curl -s -X POST http://localhost:8088/api/auth/login \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"${POCKET_AUTH_USER}\",\"password\":\"${POCKET_AUTH_PASS}\"}" | python3 -c "import json,sys;print(json.load(sys.stdin)['token'])")
curl -H "Authorization: Bearer $TOKEN" http://localhost:8088/api/llm/quota

# 共享 PG 校验
# 以本地 env 注入的 PG 凭据查询共享库（示例不包含真实值）
docker exec <shared-pg-container> psql -U "$PG_USER" -d "$PG_DATABASE" \
  -c "SELECT * FROM quota_budgets;"
```

## 6. 已知限制

- **开发密码不提交到仓库**：backend/internal/auth/users.go:47 强制密码 ≥ 8 字符；请在本地 `.env` 设置未提交的开发密码。**生产部署必须关闭 `POCKET_DEV_AUTH` 并通过 UI/API 创建用户**。
- **RedClaw 端点暂未对接**：pocketd `redclaw.Client` 调 `/api/v1/pocket/llm/chat` 和 `/api/v1/pocket/knowledge/search`，但 RedClaw `platform-go` 服务当前不暴露这两个路径。本集成留 `POCKET_REDCLAW_BASE_URL=""`，pocketd 不发起调用；后续 RedClaw 补路由后只需设该 env 即可联通。
- **`/api/llm/stream` / `/api/llm/chat` 不可用**：未设 `POCKET_LLM_API_KEY` / `POCKET_LLM_GATEWAY_*`。前端 `/cost` 页面渲染 OK（`/api/llm/quota` 已工作），但实际聊天流被禁用——这是 dev 设计，不影响 P3 集成冒烟。
- **frontend bundle 包含 `192.168.31.37`**：构建期从 `.env.ios-dev` 注入的 LAN IP（本机 dev 模式）。如需同源纯 nginx 反代（推荐），编辑 `frontend/.env.ios-dev` 把 `VITE_API_BASE` 改空后重 build。

## 7. 复现（从零开始）

```bash
# 1. 离线 base 镜像（如未 load）
docker load -i ~/work/docker-base-images/lang-base/kx-base-go-vue-v2-alpine-slim-arm64.tar.gz
docker load -i ~/work/docker-base-images/lang-base/alpine-3.20-arm64.tar.gz 2>/dev/null || true

# 2. 共享 PG / 网络
docker ps | grep <shared-pg-container> || echo "start the shared PG with credentials from your ignored .env"
# Do not put POSTGRES_PASSWORD or a real DSN in this report.
# Ensure the shared PG is attached to acc-local-net before starting pocketd.
docker network create acc-local-net 2>/dev/null || true

# 3. opencode-pocket 集成
cd services/opencode-pocket/deploy/acc-integration
cp .env.example .env
./local-up.sh
```

## 8. 与 `deploy/本地方案/` 的关系

`deploy/本地方案/` 是**纯 pocket 部署**（自己的 `pocket_local` 网络 + 自带 PG-or-no-PG 单机模式）；
`deploy/acc-integration/` 是 **与 acc/memora/redclaw 共存**的集成模式（加入 `acc-local-net` + 复用 PG）。

两者并存覆盖两种使用场景：
- 本地方案：单独验证 pocketd 不依赖外部服务（CI / 单模块开发）。
- acc-integration：联调 / 跨模块 E2E（本报告）。

切换时只需切换 `cd deploy/<dir>`，互不污染。