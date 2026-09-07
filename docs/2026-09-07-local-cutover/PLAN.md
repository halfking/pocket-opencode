# OpenPocket 本机切流与真机验收（2026-09-07）

**相对** `docs/2026-09-07-deployment-and-test-plan/PLAN.md`：那份是 252/loopback 演练。本文是 **公网域名 + NetBird + 本机 Docker + Android 真机本地包**。

**拍板**：`https://pocket.itestu.cn` 全量打到本机 pocketd；252 只做 TLS 入口；本轮一台本机 companion；不迁 252 历史库；不做 iOS。

---

## 1. 拓扑

```
Android Capacitor (local dist)
  → https://pocket.itestu.cn
    → 252 nginx :9443 (专用证书, PROXY protocol)
      → NetBird mesh → 本机 :8090 pocketd
pocketd → PG(llm-gateway-pg/pocket) + ACC MCP + Memora + RedClaw + LLM :8782
       → OpenCode :4096 (写) / companion :28080 (只读观测)
companion → ACC :4100 注册 + :4101 Runtime；Memora HTTP v2
```

权威栈：`acc-blue` / `agent-companion-local:28080` / `llm-gateway-pg` / `llm-gateway-local-8782` / RedClaw `27001`。旁路 `ai-native-*` 不接线。

---

## 2. 现状（实施前实测）

| 项 | 事实 |
|---|---|
| DNS `pocket.itestu.cn` | A → `115.29.212.252` |
| HTTPS | 落到默认 `CN=kxpms.cn`，无独立 vhost |
| 252 openpocket | 内网 `172.16.2.210:8092` / `:4177`，公网未放行 |
| 本机 NetBird `nb-mac-01` | Management Disconnected，无 mesh IP |
| `acc-nginx` | Exited；`acc-go-local` 不存在；宿主 4100/4101 未听 |
| Memora | 未跑；Qdrant/MinIO 在 |
| pocketd | 宿主 `pocketd-firstinstall :8090` health=ok（上 Docker 前须停） |
| companion | `:28080` healthz ok；env 指向不存在的 acc-nginx / acc-go-local / kxmemory-go-local |
| OpenCode | `:4096` 未听 |
| 真机 | adb `V2436A` 已连接 |

---

## 3. 实施顺序

1. 修本机 NetBird，拿到 `100.106.0.0/16` IP；ACL：252 → 本机 `:8090`（可选 `:4175`）。
2. 起 `acc-nginx`；确认 MCP/Runtime 实际端口；`memora/deploy-local.sh`；本机 OpenCode `:4096`。
3. 停宿主 pocketd；`openpocket/deploy-local.sh` 起 Docker（8090+4175），接线 ACC/Memora/RedClaw/PG/gateway/CORS。
4. companion 挂 `shared-infra`（已挂），改 env 指向真实 ACC/Memora DNS，确认注册心跳与本机 agent 发现。
5. 252：ACME vhost + 签发 `pocket.itestu.cn` + `9443` 反代本机 NetBird IP:8090（SSE/WS）。
6. `VITE_API_BASE=https://pocket.itestu.cn` 打 Android prod 包，装 vivo，跑 UI。

回滚：禁用 252 pocket vhost。不重建 PG/Redis。不改 NetBird 控制面本体。

---

## 4. 环境变量（值从 envs/loader 取，不入仓）

pocketd：`POCKET_POSTGRES_DSN`（db `pocket`）、`POCKET_MCP_BASE_URL`、`POCKET_MCP_API_KEY`、`POCKET_MCP_TENANT_ID`、`POCKET_KXMEMORY_BASE_URL`、`POCKET_REDCLAW_BASE_URL`、`POCKET_LLM_GATEWAY_URL=http://llm-gateway-local-8782:8782`、`POCKET_OPENCODE_INSTANCES`（`host.docker.internal:4096`）、`POCKET_ALLOWED_ORIGINS` 含 `https://pocket.itestu.cn,https://localhost,capacitor://localhost,http://localhost:4175`。

companion：`ACC_URL`（acc-nginx 或 acc-blue:4100）、`ACC_BASE_URL`（acc-go 或实测 Runtime）、`MEMORA_BASE_URL`、`LLM_GATEWAY_URL`、`AC_RUNTIME_SCAN_ENABLED=true`。

前端 Docker：`VITE_API_BASE=` 空（同源反代）。真机：`VITE_API_BASE=https://pocket.itestu.cn`。

---

## 5. 252 nginx

抄 `netbird.itestu.cn`：`:80` ACME webroot `/var/www/certbot`；`:9443 ssl http2 proxy_protocol`；证书 `/etc/letsencrypt/live/pocket.itestu.cn/`。签发用 `myvpn/deploy/netbird/issue-ssl-netbird-252.sh` 同款 CONNECT 隧道，域名改 `pocket.itestu.cn`。

upstream = 本机 NetBird IP `:8090`。必须：`Upgrade`/`Connection`、SSE `proxy_buffering off`、长超时。`/healthz` `/api/` `/ws` `/plugin/ws` → pocketd；可选 `/` → `:4175`。

验收：`openssl s_client -servername pocket.itestu.cn` 的 CN/SAN 必须是 `pocket.itestu.cn`。

---

## 6. 验收闸门

1. `https://pocket.itestu.cn/healthz` → 本机 pocketd。
2. 登录 JWT；`GET /api/instances` ≥ 1。
3. companion `/healthz`；ACC 可见本机 host 心跳。
4. pocketd → ACC MCP / Memora / RedClaw 通。
5. Flow A 经 **域名** 完成。
6. Android：本地壳可出登录页；联网后走域名完成对话。证据：`test-evidence/2026-09-07-local-android/`。
7. 252:8092 不被该域名命中。

清单见同目录 `CHECKLIST.md`。
