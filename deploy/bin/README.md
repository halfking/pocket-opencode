# OpenPocket 部署体系（2026-09-03 重构版）

OpenPocket 的 docker 化部署；镜像、数据、日志、配置、blue-green 版本目录**全部落在 docker 外的指定根目录**，由环境变量驱动；可在 macOS / Linux / Windows 上跑通。

## 目录结构（按 OS 自动派生）

`deploy/bin/env.sh` 的 `os_detect_base_dir()` 按 OS 自动选根目录：

| OS          | 默认 DEPLOY_BASE_DIR                | 备注 |
|-------------|--------------------------------------|------|
| macOS       | `${HOME}/kaixuan/openpocket`         | 替代旧的 `~/Downloads/kaixuan/opp` |
| Linux       | `/opt/kaixuan/openpocket`            | 服务器默认 |
| Windows     | `D:/kaixuan/openpocket`（D 盘可写）  | 否则 `C:/kaixuan/openpocket` |

任何时候都可以用 `DEPLOY_BASE_DIR=/path` 显式覆盖。

### 子目录（始终创建）

```
${DEPLOY_BASE_DIR}/
├── attachments/      # 业务附件、导出文件（容器内 /app/data/attachments 经 bind）
├── bin/
│   ├── {version}.{build}/        # 每次发布的版本目录（blue-green）
│   ├── current → {version}.{build}    # 当前活跃版本的符号链接
│   └── .gitkeep
├── backups/          # 配置 / sqlite / 旧版本快照
├── logs/             # 容器日志拉取落盘（docker logs → *.log）
├── raw-logs/         # 未加工的原始日志（应用层 stderr 直写）
├── run/              # PID / 锁 / 状态文件
├── data/             # pocketd 业务数据
├── config/
│   ├── .env.local    # 本地开发配置
│   ├── .env.154      # 154 服务器配置
│   └── .env.245      # 245 服务器配置
└── images/           # 离线镜像 *.tar.gz
```

### 条件创建（DB 数据目录）

| 开关                          | 创建目录                | 默认 |
|-------------------------------|-------------------------|------|
| `OPP_DEPLOY_PG=true`           | `${BASE_DIR}/postgres/`  | deploy-local.sh 默认 true；154/245 默认 false（PG 在 252） |
| `OPP_DEPLOY_REDIS=true`        | `${BASE_DIR}/redis/`     | 全部默认 false（Redis 在 252） |
| `OPP_DEPLOY_MYSQL=true`        | `${BASE_DIR}/mysql/`     | 全部默认 false |

## 三个入口脚本

| 脚本                | 目标                   | DEPLOY_ENV | OPP_SERVER_NAME | 端口                            | 默认 PG |
|---------------------|------------------------|------------|------------------|----------------------------------|---------|
| `deploy-local.sh`   | macOS 开发 / Win        | local      | （空）           | 8090 / 4175                      | 容器化  |
| `deploy-154.sh`     | 154 Linux 生产          | server     | 154              | 8090 / 4175 @ 172.16.2.154       | 远端 252 |
| `deploy-245.sh`     | 245 Linux 生产          | server     | 245              | 8091 / 4176 @ 172.16.2.245       | 远端 252 |
| `deploy-252.sh`     | 252 Linux 生产          | server     | 252              | 8092 / 4177 @ 172.16.2.252       | 本机内网 |

每个入口都 source `deploy/bin/env.sh`（自动派生 base dir + 端口 + 项目名 + DB 拓扑），然后依次执行：

```
init-dirs.sh          # 建目录（含 blue-green 的 bin/）
ensure-databases.sh   # 复用检测 vs 容器化
start.sh              # 启动 docker compose（自动 blue-green stage + health check + 失败回滚）
```

## 数据库：复用 vs 容器化

`deploy/bin/ensure-databases.sh` 用 `lib/database-detect.sh` 探测本机已有的 PG / Redis / MySQL（无论 docker 还是 systemd），命中则直接复用，不新建；只有当 `OPP_DEPLOY_<DB>=true` 且未命中时才容器化起一个。

| 模式                   | 何时进入                                       | 副作用 |
|------------------------|-----------------------------------------------|--------|
| `external`             | detect 命中 docker / systemd / 本机端口          | 不创建容器，不创建 DB 子目录 |
| `container`            | `OPP_DEPLOY_<DB>=true` 且 detect 未命中          | `docker compose -f docker-compose.db.yml up`；写 DSN 到 .env |
| `remote-required`      | `OPP_DEPLOY_<DB>=false` 且 detect 未命中         | 不创建；DSN 必须由 .env 注入（如 154/245 连 252） |
| `external`（强制）     | `OPP_DEPLOY_<DB>=external`                      | 跳过探测；DSN 必须由 .env 提供 |

154 / 245 默认 `OPP_DEPLOY_PG=false`，detect 命中 252 内网（172.16.2.210:5432）后走 `remote` 模式，本机不起 PG。

## Blue-Green 切换

每次 `deploy-*.sh` 都会：

1. 计算 `OPP_VERSION_BUILD`（默认 `{image_tag}-p{git_rev}-{ts}`，或显式 `OPP_DEPLOY_VERSION` + `OPP_DEPLOY_BUILD`）
2. 在 `bin/${OPP_VERSION_BUILD}/` 落一份完整的发布目录：
   - `version.json`：含 id / version / build / commit / deployed_at / **previous**（上一个 verified 版本，用于 rollback）
   - `pocketd-compose-snippet.yml`：docker compose 拼接片段
   - `migration-pre.d/`、`migration-post.d/`：用户可挂自定义钩子
3. 启动容器后 healthcheck 通过 → `ln -sfn ${OPP_VERSION_BUILD} bin/current`（原子切换）
4. healthcheck 失败（60s 内未通）→ 自动 `bg_rollback`：把 `bin/current` 指回上一个 verified 版本，并把失败的版本移到 `bin/${id}.failed/`

```
bin/
├── 1.0.42.001/                  # 历史版本
├── 1.0.43.002/                  # 历史版本
├── pocket-opp-pa348560-20260903/   # ← 当前活跃版本
├── current → pocket-opp-pa348560-20260903
└── 1.0.43.003.failed/           # 启动失败被回滚的版本（保留供事后排查）
```

### 回滚

```bash
./deploy-local.sh --rollback          # local
./deploy-154.sh   --rollback          # 154
./deploy-245.sh   --rollback          # 245
./deploy-252.sh   --rollback          # 252
```

或手动：

```bash
DEPLOY_BASE_DIR=/opt/kaixuan/openpocket \
  bash -c 'source deploy/bin/env.sh; source "${LIB_DIR}/blue-green.sh"; bg_rollback'
```

### 清理旧版本

```bash
DEPLOY_BASE_DIR=/opt/kaixuan/openpocket \
  bash -c 'source deploy/bin/env.sh; source "${LIB_DIR}/blue-green.sh"; bg_prune 5'
# 默认保留最近 5 个版本，其余移到 backups/bin-pruned-<ts>/
```

## 快速开始

### 本地（macOS / Windows）

```bash
# 0) 可选：复用本机已有的 PG（Homebrew 或 docker postgres）；否则 deploy-local 默认起容器化 PG
#    （容器化路径要求 docker daemon 可用）

# 1) 一键部署（默认 DEPLOY_BASE_DIR=${HOME}/kaixuan/openpocket）
OPP_PG_PASSWORD=<pwd> ./deploy-local.sh

# 2) 验证
./deploy/bin/status.sh                # 容器状态 + /healthz
./deploy/bin/logs.sh                  # 拉取日志落盘
./deploy/bin/stop.sh                  # 停止（保留数据）

# 3) 回滚到上一个 verified 版本
./deploy-local.sh --rollback
```

### 154 Linux 生产

```bash
# 0) 本机构建并导出镜像（或从已有 CI 工件拉取）
./deploy/bin/build-images.sh --arch amd64
./deploy/bin/save-images.sh

# 1) 传输
scp images/*amd64*.tar.gz root@154:/opt/kaixuan/openpocket/images/
scp -r deploy/bin deploy/docker root@154:/opt/kaixuan/openpocket/deploy/

# 2) 在 154 上部署
sudo DEPLOY_BASE_DIR=/opt/kaixuan/openpocket ./deploy/bin/init-dirs.sh
sudo ./deploy/bin/load-images.sh
sudo vi /opt/kaixuan/openpocket/config/.env.154   # 填生产密钥/DSN
sudo ./deploy-154.sh
```

`.env.154` 必填项：

```
POCKET_ENV=production
POCKET_DEV_AUTH=false
POCKET_JWT_SECRET=<≥32字节随机>
POCKET_ALLOWED_ORIGINS=https://<前端域名>
POCKET_POSTGRES_DSN=postgresql://llm_gateway:<密码>@172.16.2.210:5432/pocket?sslmode=disable
POCKET_PG_SCHEMA=opencode_pocket
```

### 245 Linux 生产

与 154 完全对称，仅：

- 绑 eth0 IP `172.16.2.245`（避免与 154 冲突）
- HTTP 端口 `8091`，前端端口 `4176`（错开 154 的 8090/4175）
- `.env.245` 而非 `.env.154`

```bash
sudo ./deploy-245.sh
```

## 脚本清单

### 入口（4 个）
| 脚本 | 适用 |
|---|---|
| `deploy-local.sh` | macOS / Windows 本地开发 |
| `deploy-154.sh`   | 154 Linux 生产 |
| `deploy-245.sh`   | 245 Linux 生产 |
| `deploy-252.sh`   | 252 Linux 生产 |

### 复用库（`deploy/bin/lib/`，source 用，不直接执行）
| 库 | 作用 |
|---|---|
| `os-detect.sh`       | OS 检测（macOS / Linux / WSL / Windows-MSYS）+ base dir 派生 |
| `database-detect.sh` | PG / Redis / MySQL 复用检测（docker / systemd / port） |
| `blue-green.sh`      | `bg_init` / `bg_compute_id` / `bg_stage` / `bg_switch` / `bg_rollback` / `bg_prune` / `bg_current` / `bg_compose_snippet` / `bg_mark_healthy` |

### 核心脚本（`deploy/bin/`）
| 脚本 | 作用 |
|---|---|
| `env.sh`               | 环境变量中心（OS 派生 base dir + 端口 + DB 拓扑 + 项目名） |
| `init-dirs.sh`         | 幂等创建子目录（含条件 DB 目录 + bin/ blue-green 初始化） |
| `ensure-databases.sh`  | 复用 vs 容器化决策 |
| `start.sh`             | 启动 docker compose（集成 blue-green + healthcheck + 自动回滚） |
| `stop.sh`              | 停止（保留数据） |
| `status.sh`            | 容器状态 + healthz |
| `logs.sh`              | 日志拉取 / 跟随 / 轮转 |
| `save-images.sh`       | 镜像导出 |
| `load-images.sh`       | 镜像加载 |
| `build-images.sh`      | 宿主机交叉编译镜像 |
| `tunnel-252.sh`        | 本机 → 252 PG 的 SSH tunnel |
| `rebuild-db-local.sh`  | 本地数据库整库重建 |
| `docker-compose.opp.yml` | 本地/服务器共用的 compose |
| `docker-compose.db.yml`  | 容器化 PG/Redis/MySQL 的 compose |

### 测试（`deploy/bin/tests/` + `tests/`）
| 测试 | 覆盖 |
|---|---|
| `test_os_detect.sh`       | 20 个 case：macOS / Linux / WSL / Windows / 未知 OS 的 base dir 派生 |
| `test_init_dirs.sh`       | 38 个 case：9 个 always-create 目录 + 3 个条件 DB 目录 + .gitkeep/.gitignore + 154 server 模式 + 幂等性 |
| `test_blue_green.sh`      | 12 个 case：bg_init / compute_id / stage / switch / current / rollback / prune / 重复 stage 拒绝 |
| `test_database_detect.sh` | 7 个 case：docker / systemd / port-reachable / not-found / PG/Redis/MySQL 形态 |
| `deploy-integration-test.sh` | 27 个 case：deploy-local dry-run + 154/245 派生 + OPP_DEPLOY_PG=true |
| `run-all.sh`              | 一键跑全部测试 |

## 环境变量

### 输入（执行前设置即生效）

| 变量 | 默认 | 说明 |
|---|---|---|
| `DEPLOY_ENV` | `local` | `local` / `server` |
| `DEPLOY_BASE_DIR` | OS 派生 | 顶层根目录，显式覆盖 |
| `OPP_SERVER_NAME` | （空） | server 模式下填 `154` / `245` |
| `OPP_DEPLOY_PG` | local=true / 154|245=false | 本机容器化 PG 的开关 |
| `OPP_DEPLOY_REDIS` | false | 同上 |
| `OPP_DEPLOY_MYSQL` | false | 同上 |
| `OPP_DEPLOY_VERSION` | （空） | 显式版本号（如 `1.2.3`） |
| `OPP_DEPLOY_BUILD` | （空） | 显式 build 号（如 `005`） |
| `POCKET_HTTP_PORT` | local=8090 / 154=8090 / 245=8091 | 后端宿主端口 |
| `POCKET_FRONTEND_PORT` | local=4175 / 154=4175 / 245=4176 | 前端宿主端口 |
| `POCKET_PORT_BIND_IP` | local=0.0.0.0 / 154=172.16.2.154 / 245=172.16.2.245 | 宿主端口绑定 IP |
| `OPP_IMAGE_TAG` | `pocket-opp` | 镜像 tag |
| `OPP_PG_HOST` / `OPP_PG_PORT` | local=host.docker.internal:15432 / server=172.16.2.210:5432 | PG 目标 |
| `OPP_PG_PASSWORD` | （空） | 生成 `.env.local` 时注入 DSN 密码（不入库） |
| `POCKET_ENV_DEBUG` / `OPP_DEBUG` | `0` | `1` 时 env.sh 打印全部生效路径 |

### 派生输出（env.sh 自动导出）

`POCKET_BASE_DIR` / `POCKET_DATA_DIR` / `POCKET_LOG_DIR` / `POCKET_RAW_LOG_DIR` /
`POCKET_CONFIG_DIR` / `POCKET_IMAGE_DIR` / `POCKET_BACKUP_DIR` /
`POCKET_ATTACHMENTS_DIR` / `POCKET_BIN_DIR` / `POCKET_RUN_DIR` /
`POCKET_PG_DATA_DIR` / `POCKET_REDIS_DATA_DIR` / `POCKET_MYSQL_DATA_DIR` /
`POCKET_HTTP_PORT` / `POCKET_FRONTEND_PORT` / `POCKET_PORT_BIND_IP` /
`POCKET_PROJECT_NAME` / `POCKET_ENV_FILE` / `POCKET_COMPOSE_FILE` /
`OPP_OS_KIND` / `OPP_VERSION_BUILD` / `OPP_PREVIOUS_BUILD` / `OPP_PG_MODE` / ...

## 测试

```bash
# 单元 + 集成（77 unit + 27 integration = 104 tests）
bash deploy/bin/tests/run-all.sh

# 仅单元
for t in deploy/bin/tests/test_*.sh; do bash "${t}"; done

# 仅集成
bash tests/deploy-integration-test.sh
```

## 与旧版（deploy-252.sh）的兼容

- 仓库根已新增 `./deploy-252.sh`（与 `deploy-154.sh` / `deploy-245.sh` 同款、同 `OPP_SERVER_NAME=252` 风格），绑定 eth0 IP `172.16.2.252`、端口 8092 / 4177、env 文件 `.env.252`。
- `deploy/bin/deploy-252.sh`（旧路径，文件未删）已加 deprecated 注释，仍可向后兼容运行（不设置 OPP_SERVER_NAME，env.sh 走 172.16.2.210 默认分支）。
- 生产请优先用根级 `./deploy-{154,245,252}.sh`。
- `~/Downloads/kaixuan/opp` 与 `/opt/kaixuan/opp` 旧路径仍可通过 `DEPLOY_BASE_DIR` 显式覆盖使用。

## 变更记录

- **2026-09-04（审计 + 修复 + 252 入口）**：
  - 新增仓库根 `deploy-252.sh`（与 154 / 245 同款、同 `OPP_SERVER_NAME=252`、绑 eth0 IP `172.16.2.252`、端口 `8092 / 4177`、env 文件 `.env.252`）
  - `env.sh` 增加 `252` 端口/Bind IP/env 文件分支
  - 旧 `deploy/bin/deploy-252.sh` 加 deprecated 注释，仍保留向后兼容
  - 修 P0：`ensure-databases.sh` 三处恒真式 `[[ X != "false" || X == "true" ]]` 改为 `[[ X == "true" || X == "external" ]]`
  - 修 P0：`_ensure_db` 命中外部后统一 `OPP_*_MODE=external`（去掉 deploy-local 死代码比对 `remote` / `local-port`）
  - 修 P0：`start.sh` 提取 `check_arch_for_build` helper，`auto` / `build` 双分支共用架构门禁
  - 修 P1：`start.sh` 加 `ip_addr_has` 预检 `POCKET_PORT_BIND_IP` 是否落在本机接口上
  - 修 P1：`deploy-154.sh` / `deploy-245.sh` OS 门禁从 warn 改 hard-exit
  - 修 P1：`deploy-local.sh inject_pg_dsn` 加 `trap cleanup` 防 mktemp 残留
  - 修 P1：`ensure-databases.sh` openssl 缺失时直接拒绝容器化（不再降级到明文 `changeme`）
  - 修 P1：`blue-green.sh:bg_prune` `for id in ${to_prune}` 改 `while read` 防词拆分
  - 修 P2：`load-images.sh` 提示更新到根级 `deploy-252.sh`
- **2026-09-03（重构版）**：
  - 新增 `deploy-{local,154,245}.sh` 三个入口（macOS / 154 / 245）
  - 新增 `deploy/bin/lib/{os-detect,database-detect,blue-green}.sh` 复用库
  - 新增 `deploy/bin/ensure-databases.sh` + `docker-compose.db.yml`（DB 复用 vs 容器化）
  - 新增 blue-green 切换：`bin/{version}.{build}/` + `bin/current` 符号链接 + 自动回滚 + prune
  - 新增子目录：`attachments/`, `bin/`, `backups/`, `raw-logs/`, `run/`（加上既有 data/ logs/ config/ images/）
  - 新增条件 DB 目录：`postgres/`, `redis/`, `mysql/`（按 OPP_DEPLOY_* 创建）
  - OS 感知 base dir：macOS=`~/kaixuan/openpocket` / Linux=`/opt/kaixuan/openpocket` / Windows=`D:/kaixuan/openpocket`（D 可写）或 `C:/`
  - 154/245 绑各自 eth0 IP（154=172.16.2.154 / 245=172.16.2.245），错开端口（154=8090/4175 / 245=8091/4176）
  - 新增 4 套单元测试 + 1 套集成测试（104 cases，全部 PASS）
