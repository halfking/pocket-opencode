# 切流验收闸门 2026-09-07

## 入口

- DNS `pocket.itestu.cn` → 115.29.212.252
- LE cert `CN=SAN=pocket.itestu.cn` (expires 2026-12-06)
- 252 nginx: `pocket.itestu.cn-80.conf` (ACME) + `pocket.itestu.cn.conf` (9443 PROXY)
- Upstream: **NetBird mesh**（2026-09-08）nginx `pocket_mac_api` → `100.106.126.138:8090`，`pocket_mac_web` → `100.106.126.138:4175`（Mac `nb-mac-01`）。252 host `nb-252-host` = `100.106.190.139`。SSH reverse `252:18090/14175` 仍监听作回滚。
- `curl -fsS https://pocket.itestu.cn/healthz` → `ok` (本机 pocketd)
- `openssl s_client -servername pocket.itestu.cn` → `CN=pocket.itestu.cn` (not kxpms.cn)

## 本机栈

- pocketd Docker `:8090` on `shared-infra`; binary replaced with current amd64 build (workspaceId 生效)
- frontend `:4175` healthy
- Memora `:8091` healthz ok
- OpenCode `:4096` `/global/health` healthy
- RedClaw gateway `:27081` `{status:alive}`；pocketd `POCKET_REDCLAW_BASE_URL=http://host.docker.internal:27081`
- RedClaw facade `:27001` `{status:alive}`（无 `/api/v1/pocket/*`）
- Gateway companion passthrough → `host.docker.internal:28080`：`GET /api/v2/orchestration/agents/{id}/logs` **200**；`GET /api/v2/orchestration/agents` **200**（companion 回落，`host_id=host-local`）
- Gateway ACC `/api/v2/canonical/tasks` **200**（acc-go `:4101` + `acc_db` GRANT）
- `/api/v1/pocket/llm/chat` **200**；knowledge search **200 有命中**（Memora `:8091` + project `agent-companion`）
- pocketd RedClaw：gateway JWT + `/healthz` → `connected:true`；H5/真机 `POST /api/redclaw/chat` **200** `pong`
- llm-gateway `:8782` ok
- PG `llm-gateway-pg` db `pocket` reused (not rebuilt)

## Companion

- `agent-companion-local:28080` healthz ok; network `shared-infra`
- ACC registry: `acc registry: registered` agent_id=`local-runtime-dev`
- Discovered agents: claude-code, opencode
- Runtime Control SSE `/api/v2/orchestration/runs/...` 404 (no acc-go-local:4101) — PARTIAL
- Scanner `unsupported platform` inside Linux container; PATH discovery still found 2 CLIs

## ACC MCP

- pocketd → `http://host.docker.internal:4101/api/v2/mcp`（本机 acc-go，tenant `default`）
- `POST /api/tasks/delegate` 200 `source=acc`；H5/真机「委托 ACC」已点通
- Node ACC `/mcp`（`acc-nginx`）仍可作为回滚，不再是 pocketd 主路径
- Runtime Control 走 acc-go `:4101`（employees 在线）

## Flow A (domain)

See `flow-a-domain.md`. Create/SSE/messages/interrupt/idempotency green; prompt HTTP 502 (upstream deadline) but messages n=2.

## 回滚

```
ssh 252 'mv /etc/nginx/conf.d/pocket.itestu.cn.conf /etc/nginx/conf.d/pocket.itestu.cn.conf.disabled && nginx -t && nginx -s reload'
```

Do not drop `llm-gateway-pg` / `nbjl-redis`. Re-enable: move the file back and reload.
