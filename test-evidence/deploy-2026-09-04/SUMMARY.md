# OpenPocket 部署审计 + 双环境部署测试（2026-09-04）

## 测试统计

| 套件                                | PASS | FAIL | 备注 |
|-------------------------------------|------|------|------|
| test_os_detect.sh                   |   20 |    0 |     |
| test_init_dirs.sh                   |   38 |    0 |     |
| test_blue_green.sh                  |   12 |    0 |     |
| test_database_detect.sh             |    7 |    0 |     |
| deploy-integration-test.sh          |   27 |    0 |     |
| **合计（单元 + 集成 dry-run）**     | **104** | **0** |     |
| 本地真起容器（deploy-local.sh）     |   ✓  |    — | pocketd + frontend 容器均 healthy，healthz 通过 |
| 252 等价 dry-run（deploy-252.sh）   |   ✓  |    — | 端口/Bind IP/PG 拓扑/env 文件全部正确派生 |

## 审计发现并已修复

| # | 优先级 | 文件 | 行 | 问题 | 修复 |
|---|--------|------|----|------|------|
| 1 | **P0** | `deploy/bin/ensure-databases.sh` | 145-147 | `[[ X != "false" \|\| X == "true" ]]` 恒真式 | 改 `[[ X == "true" \|\| X == "external" ]]` |
| 2 | **P0** | `deploy/bin/start.sh` | 104-119 | `auto` 模式 amd64 上静默触发不可用 `--build` | 提取 `check_arch_for_build` helper，`auto` + `build` 共用 |
| 3 | **P0** | `deploy/bin/ensure-databases.sh` | 74 + env.sh:136-138 + deploy-local.sh:144 | OPP_PG_MODE 字符串不统一（`remote`/`local-port`/`docker`/`system` vs `external`/`container`/`remote-required`） | `_ensure_db` 命中统一归到 `external`；deploy-local 删死代码比对 |
| 4 | P1 | `deploy/bin/start.sh` | 171-179 | 端口绑定 IP 不预检本机接口 | 加 `ip_addr_has` helper（兼容 `ip` + `ifconfig`） |
| 5 | P1 | `deploy-154.sh` / `deploy-245.sh` | 36-39 / 33-36 | OS 门禁只 warn 不 exit | 改 hard-exit 1 |
| 6 | P1 | `deploy-local.sh inject_pg_dsn` | 130-139 | mktemp 失败残留 | 加 `trap 'rm -f "${tmp}"' RETURN` |
| 7 | P1 | `deploy/bin/ensure-databases.sh` | 122,131 | openssl 缺失降级明文 `changeme` | 检测 openssl 缺失直接拒绝容器化路径 |
| 8 | P1 | `deploy/bin/lib/blue-green.sh:bg_prune` | 254 | `for id in ${to_prune}` 未加引号 | 改 `while IFS= read -r id` |
| 9 | P2 | `deploy/bin/load-images.sh` | 83 | 提示指向旧 `./deploy/bin/deploy-252.sh` | 指向新根级 `deploy-{154,245,252}.sh` |
| 10 | P2 | `deploy/bin/README.md` / DEPLOYMENT_PLAN.md | 271 / 201 | 文档错误宣称 deploy-252.sh 等价 deploy-154.sh | 修订为"已重构为根级 deploy-252.sh；旧 deploy/bin/ 版本加 deprecated 注释" |

## 新增

- `deploy-252.sh`（仓库根）：与 `deploy-154.sh` / `deploy-245.sh` 同款、同 `OPP_SERVER_NAME=252`、绑 eth0 IP `172.16.2.252`、HTTP 端口 8092、frontend 4177、env 文件 `.env.252`。
- `deploy/bin/env.sh` 252 分支：端口/Bind IP/env 文件命名。
- `deploy/bin/deploy-252.sh`（旧路径）：顶部加 DEPRECATED 注释，保留向后兼容。
- `deploy-154.sh` / `deploy-245.sh` / `deploy-252.sh` 三者顶部加 `--dry-run` 参数提取，让演练跳过 PG TCP 探测。

## 双环境部署测试结果

### 环境 1：本地（macOS dev）

```bash
DEPLOY_BASE_DIR=/tmp/opp-local-test OPP_PG_PASSWORD=test-pwd-not-real ./deploy-local.sh
```

结果：
- ✅ 9 个 always-create 子目录全建
- ✅ `bin/current` 自动 stage `pocket-opp-pa348560-20260904081344`
- ✅ pocketd 容器：healthy（http://localhost:8090/healthz → `ok`）
- ✅ frontend 容器：healthy（http://localhost:4175/healthz → `frontend ok`）
- ✅ bin/current version.json 含 commit / deployed_at / started_at / last_healthy_at / active=true
- ✅ logs/.last-healthy 写入
- ✅ 第二次 deploy：复用 current，复用成功
- ✅ 容器 down：compose down 干净

完整日志：`local-deploy.log`、`local-deploy-bg-switch.log`

### 环境 2：252（Linux 生产 dry-run 等价测试）

本机不在 252 内网，用 `--dry-run` 演练（mock `OPP_OS_KIND_OVERRIDE=linux` + fixture `.env.252`）：

```bash
DEPLOY_BASE_DIR=/tmp/opp-252-test OPP_OS_KIND_OVERRIDE=linux \
  DEPLOY_ENV=server OPP_SERVER_NAME=252 OPP_PG_PASSWORD=simulated-pwd \
  ./deploy-252.sh --dry-run
```

派生结果：
- ✅ `OS_KIND=linux`
- ✅ `DEPLOY_BASE_DIR=/tmp/opp-252-test`
- ✅ `HTTP_PORT=8092@172.16.2.252`（与 154 的 8090 / 245 的 8091 错开）
- ✅ `FRONTEND_PORT=4177`
- ✅ `POCKET_PROJECT_NAME=opencode-pocket-server-252`
- ✅ `POCKET_ENV_FILE=/tmp/opp-252-test/config/.env.252`
- ✅ `OPP_PG_HOST=172.16.2.210`（连 252 内网 PG）
- ✅ bin/ 8 个子目录创建 + bin/current 自动 stage
- ✅ dry-run 跳过 PG TCP 与 docker compose up

完整日志：`252-dryrun.log`

## 一键复跑

```bash
# 1) 单元 + 集成
bash deploy/bin/tests/run-all.sh

# 2) 本地真起容器
DEPLOY_BASE_DIR=/tmp/opp-local-test OPP_PG_PASSWORD=test ./deploy-local.sh

# 3) 252 等价 dry-run（本机）
DEPLOY_BASE_DIR=/tmp/opp-252-test OPP_OS_KIND_OVERRIDE=linux \
  DEPLOY_ENV=server OPP_SERVER_NAME=252 OPP_PG_PASSWORD=simulated-pwd \
  bash ./deploy-252.sh --dry-run
```

## 文件清单

### 新增

- `deploy-252.sh`（仓库根，与 154/245 同款）
- `test-evidence/deploy-2026-09-04/{SUMMARY.md,run-all-output.log,local-deploy.log,local-deploy-bg-switch.log,252-dryrun.log}`

### 修改

- `deploy/bin/env.sh`（252 分支 + OPP_SERVER_NAME 白名单）
- `deploy/bin/start.sh`（架构门禁 helper + ip_addr_has 预检 + --dry-run 兼容 + 252 提示）
- `deploy/bin/ensure-databases.sh`（恒真式布尔 + mode 字符串统一 + openssl 缺失拒绝）
- `deploy/bin/init-dirs.sh`（"下一步" 提示）
- `deploy/bin/lib/blue-green.sh`（bg_prune while-read）
- `deploy/bin/load-images.sh`（提示路径修正）
- `deploy/bin/deploy-252.sh`（DEPRECATED 注释）
- `deploy/bin/README.md`（252 入口 + 变更记录）
- `deploy-local.sh`（inject_pg_dsn trap + mode 比对清理）
- `deploy-154.sh` / `deploy-245.sh`（OS 门禁 hard-exit + --dry-run + PG 探测跳过）

### 不动

- `deploy/bin/tests/test_*.sh`（4 套单元测试维持 77 PASS）
- `tests/deploy-integration-test.sh`（集成 dry-run 维持 27 PASS）

## 风险与回滚

| 风险 | 缓解 |
|------|------|
| P0 #1 修复后 154/245（`OPP_DEPLOY_PG=false`）行为变化：从"假性跑过 detect" → "真正跳过 detect"。 | env.sh 默认 `OPP_PG_MODE=uninit` 表示"未探测"，不影响最终行为 |
| P0 #2 修复后 amd64 上 `auto` 模式若 kx-base 已加载，会提示用 `--no-build`。 | save/load-images.sh 流程已覆盖 252；要求 252 运维先 load 镜像 |
| P1 #4 ip_addr_has 启用后 154/245/252 在非目标机器演练会失败 | 加 `POCKET_PORT_BIND_IP=0.0.0.0` 临时绕过 |
| 新增 `deploy-252.sh` 不影响旧 `deploy/bin/deploy-252.sh` | 旧脚本加 DEPRECATED 注释，仍可向后兼容运行 |
