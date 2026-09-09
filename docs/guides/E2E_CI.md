# E2E CI 接入指南（web）

> 套件本体：[`e2e/web/`](../../e2e/web/README 已并入上层) —— 见 [`e2e/README.md`](../../e2e/README.md)。
> Workflow：[`.github/workflows/e2e-web.yml`](../../.github/workflows/e2e-web.yml)。

## 1. 本地运行（可复制粘贴）

```bash
cd /path/to/openpocket

# ① 后端（本地存储模式，不依赖 PG）
(cd backend && go build -o pocketd ./cmd/pocketd)
mkdir -p /tmp/pocketd-e2e
POCKET_HTTP_PORT=8090 POCKET_AUTH_USER=admin POCKET_AUTH_PASS=admin-e2e-pass \
POCKET_JWT_SECRET=e2e-only-jwt-secret-0123456789abcdef \
POCKET_DB_PATH=/tmp/pocketd-e2e/pocket.sqlite \
POCKET_AUTH_LEGACY_ONLY=true POCKET_DEV_AUTH=true \
./backend/pocketd > /tmp/pocketd.log 2>&1 &

# ② 前端 dev server（4174，同源代理 /api、/ws → 8090）
(cd frontend && env -u VITE_API_BASE VITE_API_BASE= npx vite --port 4174 --strictPort > /tmp/vite.log 2>&1 &)

# ③ 等就绪
curl -s http://127.0.0.1:8090/healthz   # → ok
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:4174/   # → 200

# ④ 跑套件
cd e2e/web && npm install && npx playwright install chromium && npx playwright test
```

## 2. 环境变量

| 变量 | 默认（本地） | CI 值 | 说明 |
|------|--------------|-------|------|
| `E2E_BASE_URL` | `http://127.0.0.1:4174` | 同左 | 前端地址 |
| `E2E_USERNAME` | `admin` | `admin` | 登录用户 |
| `E2E_PASSWORD` | dev 实例密码 | secret `E2E_POCKET_AUTH_PASS` | 登录密码 |
| `E2E_MASTER_PASSWORD` | `e2e-master-pass-123` | 同左 | 首次登录「创建主密码」分支 |
| `POCKET_*` | 见 workflow env 块 | secret 注入密码/JWT | 后端启动配置 |

⚠️ **`VITE_API_BASE` 陷阱（2026-09-10 实测）**：`resolveRuntimeApiBase` 优先级是
localStorage 覆盖 > `VITE_API_BASE` > 同源。若启动 vite 的环境里残留了指向其它实例的
`VITE_API_BASE`（本机曾指向 8088 的另一实例），全套件会静默打错后端。因此 CI 与本地
统一用 `env -u VITE_API_BASE VITE_API_BASE=` 显式清空，走 vite 同源代理。

## 3. CI 设计说明

| 决策 | 理由 |
|------|------|
| **dev server 模式（4174）而非 `vite preview`** | preview 只伺服 `dist/` 静态资源、没有 `/api` `/ws` 代理，后端链路需要额外反代才能通；dev server 天然同源代理，少一个活动部件 |
| **后端用本地存储模式（不连 PG）** | 免去 CI 起数据库；pocketd 以 `POCKET_DB_PATH` 指向 sqlite/文件存储即可拉起；PG 相关链路已有带 DSN guard 的集成测试单独覆盖 |
| **AI 流式用例容忍 error frame** | 断言的是「流式机制工作」（runtime activeCount / typing / retry chip / 终态），不断言上游回答成功——上游（llm 网关 auto 链）经常 502，90s 后下发 `context deadline exceeded` 也是合法终态 |
| **无数据自动 skip** | 审批完整流程 / RSS 数据用例在探测到无种子数据时 `test.skip`，能力缺口以 skip 原因显式暴露而不是让 CI 红 |
| **`npx playwright install --with-deps chromium`** | ubuntu runner 需要系统依赖；只装 chromium 控制时长 |

## 4. 排查

- 报告与 trace 在 artifacts（`playwright-report/`、`test-results/`），本地用
  `npx playwright show-trace test-results/artifacts/<case>/trace.zip` 看逐步快照。
- 后端起不来：看 `/tmp/pocketd.log`（CI 看 step log）；常见是端口占用或 `POCKET_DB_PATH` 目录未 mkdir。
- 前端 404/打到错误实例：确认 dev server 是以「清空 VITE_API_BASE」方式启动（见 §2 陷阱）。
- 流式用例整批 skip：说明 `/api/llm/models` 未就绪——后端没配 LLM 网关密钥，属环境问题非回归。

## 5. 失败用例处置

按 issue #20 约定：确认是回归（本地可复现）→ 修复 PR；确认是环境/数据缺失 → 调整 skip 门控并在
issue 里记录所需种子；新增覆盖点 → 直接补 spec 并在本文件登记。
