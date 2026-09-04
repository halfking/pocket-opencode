# OpenPocket 252 实机部署（2026-09-05）

## 结果：✅ 部署成功

| 项 | 值 |
|---|---|
| 服务器 | 252（公网 115.29.212.252:25022，内网 eth0 **172.16.2.210**，x86_64） |
| 后端 | `opencode-pocket:pocket-opp` (amd64) → **http://172.16.2.210:8092** healthy |
| 前端 | `opencode-pocket-frontend:pocket-opp` (amd64) → **http://172.16.2.210:4177** healthy |
| PG | 252 本机 docker `pg-252-pg17`（172.16.2.210:5432/pocket，schema `opencode_pocket`） |
| 旧栈 | `server-opp`（8090/4175）未受扰动，仍 healthy |
| 蓝绿 | `bin/current → pocket-opp-plocal-20260905011136`，previous 链 + last_healthy_at 落盘 |

## 部署路径（kx-base 离线包仅 arm64，252 为 amd64 → 全程交叉构建）

```
Mac: build-images.sh --arch amd64     # 宿主交叉编译，不依赖 kx-base
    save-images.sh                    # 导出 tar.gz（22M/27M）
    scp → 252:/opt/kaixuan/openpocket/images/
252: load-images.sh                   # docker load
    .env.252（沿用旧 .env.server 的 DSN/JWT + POCKET_AUTH_LEGACY_ONLY=true）
    OPP_NET_NAME=opp-server-252-net ./deploy-252.sh
```

## 部署中发现并修复的问题（按发现顺序）

| # | 级别 | 位置 | 问题 | 修复 |
|---|------|------|------|------|
| 1 | **P0** | `deploy/bin/env.sh` 252 分支 | 绑 IP 误设 `172.16.2.252`（252 实际 eth0 是 **172.16.2.210**，"252"来自公网 IP） | 改 `172.16.2.210`；8090/4175 已被旧栈占用，端口 8092/4177 维持 |
| 2 | P1 | `deploy/bin/env.sh http_ok` | curl/wget 皆无时 `return 0` 误放行 | 改 `return 1` + 安装提示 |
| 3 | P1 | `deploy-252.sh` | PG 探测 `bash -c "</dev/tcp/${HOST}..."` 注入风险 | host/port 白名单正则校验 |
| 4 | P1 | `deploy-local.sh` | openssl 缺失时 JWT 兜底为可预测字符串 | 改硬失败 |
| 5 | P2 | `load-images.sh` / `start.sh` | 旧路径提示（`/opt/kaixuan/opp`、开发机 tar 路径）、"254" 笔误 | 修正 |
| 6 | **P0** | 252 运行时 | `data/` 等目录 root 属主 755，容器用户 `pocket`(uid 100) 写不了 master key | `chown 100:101 data logs raw-logs attachments run` |
| 7 | **P0** | `backend/internal/chatagent/store.go` | base schema 含 `idx_chat_agents_marketplace` 索引，旧库升级时 Init 先于补列迁移执行 → SQLSTATE 42703 → log.Fatalf 启动崩 | 索引移入 `RunMarketplaceMigration`（幂等），base schema 只留 base 结构 |
| 8 | **P0** | `deploy-local.sh` | `source env.sh` **之后**才 `export OPP_DEPLOY_PG=:-true` → 被 env.sh 预置的 false 盖死（端口同理 15432/5432）→ 检测跳过、DSN 不写 | DB 开关预设挪到 source env.sh 之前；模板加 `POCKET_AUTH_LEGACY_ONLY=true`（新二进制 dev 也强制要求） |
| 9 | P1 | `deploy/bin/ensure-databases.sh` | 容器化 DB 启动失败被吞（`mode=container; return 0`），无 DB 继续部署 | 失败传导 `return 1`；顶层 `|| true` 改 `|| exit 1` |
| 10 | P1 | `deploy/bin/lib/database-detect.sh` | 宿主侧探 `host.docker.internal` 在新版 Docker Desktop 不可解析 → 漏检本机实例 → 误容器化撞端口 | 加 loopback（127.0.0.1）兜底探测，PG/Redis/MySQL 对称 |
| 11 | **P1** | `deploy/bin/start.sh` | blue-green 只在 current 不存在时 stage+switch；重复部署不产生版本目录，current 永不前移，回滚链断（`last_healthy_at: null`） | 每次部署 stage 新版本；健康通过后切换；失败标 `.failed`、current 不动 |

## 验证

- **单元 + 集成**：104/104 PASS（test_database_detect 测试 5 更新为冷门端口，消除本机 5432 假命中）
- **本地两连部署**（/tmp/opp-bg-verify，连共享 llm-gateway-pg 的 pocket 库）：
  - #1 命中外部 PG → stage+bootstrap → healthy → `last_healthy_at` 落盘
  - #2 stage 新版本 → 健康通过 → `🔀 bin/current` 前移 → `previous` 链建立
- **252 重部署**（同步修复后脚本）：`🧩 stage 新版本` → `✅ pocketd/frontend` → `🔀 bin/current → ...11136`，`previous=...003438`
- **后端**：`go build ./...` + `go test ./internal/chatagent/` 通过

## 归档

- `252-final-deploy.log` — 252 最终部署完整日志
- `../../deploy-2026-09-04/` — 前序审计修复与双环境 dry-run 证据

## 遗留事项

- 252 上 `bin/current/version.json` 的 `commit: "unknown"`：`/opt/kaixuan/openpocket` 非 git 仓库，`bg_compute_id` 退化为 `plocal` 前缀（行为正常，仅溯源信息弱）。
- `POCKET_AUTH_LEGACY_ONLY=true` 是迁移文档定义的回退路径；待 RedClaw Admin 服务就绪后配置 `POCKET_REDCLAW_ADMIN_URL` + `POCKET_REDCLAW_ADMIN_SECRET` 再切回。
- 旧栈 `server-opp`（8090/4175）仍在运行；切换流量前不要下线。
