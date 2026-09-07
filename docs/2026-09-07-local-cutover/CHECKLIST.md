# 本机切流清单（companion to PLAN.md）

## Phase 0 — 文档与盘点

- [ ] PLAN.md 已落盘
- [ ] 本机 docker / 端口 / NetBird / 真机 adb 快照写入 `evidence/inventory.md`
- [ ] 两仓（pocket-opencode vs openpocket）deploy/frontend 差异已记

## Phase 1 — 本机依赖

- [ ] NetBird `nb-mac-01` Connected，有 mesh IP
- [ ] ACL：252 → 本机 `:8090`（可选 `:4175`）
- [ ] `acc-nginx` 健康；MCP/Runtime 地址已实测
- [ ] Memora `/healthz` 200
- [ ] OpenCode `:4096` 可达
- [ ] `llm-gateway-pg` / `llm-gateway-local-8782` / RedClaw `:27001` 保持 healthy（不重建）

## Phase 2 — OpenPocket Docker

- [ ] 宿主 `pocketd-firstinstall :8090` 已停
- [ ] `./deploy-local.sh` 起 pocketd `:8090` + frontend `:4175`
- [ ] `curl -fsS http://127.0.0.1:8090/healthz` → ok
- [ ] `curl -fsS http://127.0.0.1:4175/healthz` → frontend ok
- [ ] env：PG / MCP / KxMemory / RedClaw / LLM / OpenCode / CORS 已接线
- [ ] 容器内 DNS 不用 localhost 指跨容器服务

## Phase 3 — Companion

- [ ] `agent-companion-local:28080` `/healthz` ok
- [ ] 与 ACC 同网络；`ACC_URL` / `ACC_BASE_URL` / `MEMORA_BASE_URL` 可达
- [ ] ACC 可见 register/heartbeat
- [ ] 本机 agent 发现非空（或已记录 PATH 上无 CLI 的原因）
- [ ] 不把 `ai-native-agent-companion:28082` 当权威

## Phase 4 — 252 域名

- [ ] `/etc/nginx/conf.d/pocket.itestu.cn-80.conf` ACME
- [ ] LE 证书 `CN/SAN=pocket.itestu.cn`
- [ ] `/etc/nginx/conf.d/pocket.itestu.cn.conf` listen 9443 proxy_protocol
- [ ] upstream = 本机 NetBird IP:8090；WS/SSE 头已设
- [ ] `curl -fsS https://pocket.itestu.cn/healthz` → ok（本机）
- [ ] `openssl s_client` 不是 `CN=kxpms.cn`
- [ ] 回滚方法已记录（禁用 vhost）

## Phase 5 — API / Flow A（走域名）

- [ ] `POST /api/auth/login` 得 JWT
- [ ] 无 token 访问 `/api/instances` → 401
- [ ] `GET /api/instances` ≥ 1
- [ ] Flow A：create → SSE `server.connected` → prompt → messages
- [ ] ACC MCP / Memora / RedClaw 探针通过

## Phase 6 — Android 真机

- [ ] `VITE_API_BASE=https://pocket.itestu.cn` 构建 + `cap sync android`
- [ ] dist 含该域名（sanity grep）
- [ ] 安装到 `V2436A`；**不用** `adb reverse`
- [ ] 登录壳本地可渲染
- [ ] 联网后会话列表 / 对话 / 底栏 / 横屏
- [ ] 证据目录 `test-evidence/2026-09-07-local-android/`
- [ ] Web 断点补充（4175 或域名 H5）

## 回滚

- [ ] 需要时：252 禁用 pocket vhost，nginx reload
- [ ] 不删 `llm-gateway-pg` / `nbjl-redis`
