# 切流验收闸门 2026-09-07

## 入口

- DNS `pocket.itestu.cn` → 115.29.212.252
- LE cert `CN=SAN=pocket.itestu.cn` (expires 2026-12-06)
- 252 nginx: `pocket.itestu.cn-80.conf` (ACME) + `pocket.itestu.cn.conf` (9443 PROXY)
- Upstream: SSH reverse tunnel `252:18090 → Mac:8090`, `252:14175 → Mac:4175`
  (NetBird mesh blocked: Aliyun SG drops :10000/:33080)
- `curl -fsS https://pocket.itestu.cn/healthz` → `ok` (本机 pocketd)
- `openssl s_client -servername pocket.itestu.cn` → `CN=pocket.itestu.cn` (not kxpms.cn)

## 本机栈

- pocketd Docker `:8090` on `shared-infra`; binary replaced with current amd64 build (workspaceId 生效)
- frontend `:4175` healthy
- Memora `:8091` healthz ok
- OpenCode `:4096` `/global/health` healthy
- RedClaw facade `:27001` `{status:alive}` (client init skipped: empty POCKET_REDCLAW_SECRET)
- llm-gateway `:8782` ok
- PG `llm-gateway-pg` db `pocket` reused (not rebuilt)

## Companion

- `agent-companion-local:28080` healthz ok; network `shared-infra`
- ACC registry: `acc registry: registered` agent_id=`local-runtime-dev`
- Discovered agents: claude-code, opencode
- Runtime Control SSE `/api/v2/orchestration/runs/...` 404 (no acc-go-local:4101) — PARTIAL
- Scanner `unsupported platform` inside Linux container; PATH discovery still found 2 CLIs

## ACC MCP

- pocketd → `http://acc-nginx/mcp` configured
- Node ACC `/mcp` authenticates `api_keys` table Bearer (≤256 chars)
- pocketd client sends HS256 JWT → HTTP 401 E_AUTH_INVALID — PARTIAL until acc-go MCP or API-key client

## Flow A (domain)

See `flow-a-domain.md`. Create/SSE/messages/interrupt/idempotency green; prompt HTTP 502 (upstream deadline) but messages n=2.

## 回滚

```
ssh 252 'mv /etc/nginx/conf.d/pocket.itestu.cn.conf /etc/nginx/conf.d/pocket.itestu.cn.conf.disabled && nginx -t && nginx -s reload'
```

Do not drop `llm-gateway-pg` / `nbjl-redis`. Re-enable: move the file back and reload.
