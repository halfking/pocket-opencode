# E2E 测试（web / Playwright）

本目录是 openpocket web 端的 Playwright E2E 套件（issue #20 / handoff T5 的 web 部分）。

## 布局

```
e2e/
├── web/                    # Playwright 套件（本目录）
│   ├── playwright.config.ts
│   ├── helpers/            # 登录 / 会话复用
│   └── specs/              # auth · chat-stream · approvals · rss-smoke
└── android/                # Android CDP 驱动脚本（keepalive 全链路验证）
```

## 覆盖点

| spec | 覆盖 | 数据依赖 |
|------|------|----------|
| `auth.spec.ts` | 未登录守卫重定向 / 错误密码提示 / 正确登录进入 `/#/ai`（含首次「创建主密码」弹窗分支） | 无 |
| `chat-stream.spec.ts` | AI 对话发送 → 10s 内流式证据（`__openpocket_aiStreamRuntime__.activeCount≥1` / `.typing` / `.msg-retry` 三选一）→ 气泡终态（**容忍上游 error frame，不要求回答成功**） | 需后端配置 LLM 网关，未配置自动 skip |
| `approvals.spec.ts` | `/ai` 指挥中心骨架；审批空态；完整审批流程（处理一条待审批） | 完整流程需种子数据，实时探测缺失时自动 skip |
| `rss-smoke.spec.ts` | `/rss` 列表骨架 / 新增源入口与表单 / 信息流数据或空态 | 外部添加订阅源为 TODO（外部网络依赖） |

> 设计原则：**无数据 = skip，不是失败**。环境缺种子/网关时套件保持绿，能力缺口用 skip 原因显式暴露。

## 本地运行

```bash
# 1. 后端（本地存储模式，无 PG 依赖）
cd backend && go build -o pocketd ./cmd/pocketd
mkdir -p /tmp/pocketd-e2e
POCKET_HTTP_PORT=8090 POCKET_AUTH_USER=admin POCKET_AUTH_PASS=admin-e2e-pass \
POCKET_JWT_SECRET=e2e-only-jwt-secret-0123456789abcdef \
POCKET_DB_PATH=/tmp/pocketd-e2e/pocket.sqlite \
POCKET_AUTH_LEGACY_ONLY=true POCKET_DEV_AUTH=true \
./pocketd &

# 2. 前端（vite dev，同源代理 /api、/ws → 8090；注意清掉环境里的 VITE_API_BASE，
#    否则会打到别的实例——2026-09-10 踩坑：本机全局 VITE_API_BASE 指向 8088 导致全套件打错后端）
cd frontend && env -u VITE_API_BASE VITE_API_BASE= npx vite --port 4174

# 3. 套件
cd e2e/web && npm install && npx playwright install chromium && npx playwright test
```

## 环境变量

| 变量 | 默认 | 说明 |
|------|------|------|
| `E2E_BASE_URL` | `http://127.0.0.1:4174` | 前端地址 |
| `E2E_USERNAME` / `E2E_PASSWORD` | `admin` / `Veritrans&9527` | 登录凭据（CI 用 secret 覆盖） |
| `E2E_MASTER_PASSWORD` | `e2e-master-pass-123` | 首次登录创建主密码分支用 |

## CI

GitHub Actions：[`.github/workflows/e2e-web.yml`](../../.github/workflows/e2e-web.yml)（push 触发路径 `e2e/web/**`、`frontend/src/**`、`backend/**`；手跑 workflow_dispatch）。设计说明与排查指南见 [`docs/guides/E2E_CI.md`](../docs/guides/E2E_CI.md)。失败用例按 issue #20 约定转 issue。

## Android / iOS

- Android：`e2e/android/android-keepalive-cdp.py` —— CDP 注入 WebView 驱动 keepalive 全链路（spawnChat + 伪造 hidden → 断言 FGS 拉起），用法见文件头注释与 [T2 落地文档](../docs/design/2026-09-10-android-ai-stream-fgs.md)。
- iOS：TODO（依赖真机档验收，见 issue #15）。
