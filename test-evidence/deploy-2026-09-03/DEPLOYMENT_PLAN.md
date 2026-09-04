# OpenPocket 部署方案（2026-09-03 重构版）

## 目标

按部署环境的操作系统（macOS / Linux / Windows）和角色（开发 / 154 生产 / 245 生产），把 openpocket 的 docker 化部署收敛到 **3 个入口脚本** + **1 套复用库**，并把 **blue-green 切换**、**DB 复用/容器化**、**OS 感知目录**三件事做正。

## 一、根目录规则（OS 自动派生）

| OS          | 默认 DEPLOY_BASE_DIR                  | 备注 |
|-------------|----------------------------------------|------|
| macOS       | `${HOME}/kaixuan/openpocket`           | 替代旧的 `~/Downloads/kaixuan/opp` |
| Linux       | `/opt/kaixuan/openpocket`              | 服务器默认 |
| Windows     | `D:/kaixuan/openpocket`（D 盘可写）    | 否则 `C:/kaixuan/openpocket` |

任何时候都可以 `DEPLOY_BASE_DIR=/path` 显式覆盖。

实现：`deploy/bin/lib/os-detect.sh:os_detect_base_dir()`。

## 二、子目录结构

### 始终创建（9 个）

```
${DEPLOY_BASE_DIR}/
├── attachments/      # 业务附件（bind → 容器 /app/data/attachments）
├── bin/              # blue-green 版本目录 + current 符号链接
├── backups/          # 配置 / sqlite / 旧版本快照
├── logs/             # docker logs 拉取落盘
├── raw-logs/         # 应用层 stderr 直写（bind → /var/log/pocketd-raw）
├── run/              # PID / 锁 / state
├── data/             # pocketd 业务数据（bind → /app/data）
├── config/           # .env.local / .env.154 / .env.245
└── images/           # 离线镜像 *.tar.gz
```

### 条件创建（按 OPP_DEPLOY_<DB>）

| 开关               | 创建目录           | deploy-local | deploy-154 | deploy-245 |
|--------------------|--------------------|--------------|------------|------------|
| `OPP_DEPLOY_PG`    | `postgres/`         | true（默认） | false       | false       |
| `OPP_DEPLOY_REDIS` | `redis/`            | false        | false       | false       |
| `OPP_DEPLOY_MYSQL` | `mysql/`            | false        | false       | false       |

154 / 245 的 PG / Redis 都在 252 内网（172.16.2.210），不创建本机 DB。

## 三、三个入口脚本

| 脚本                | 目标                   | DEPLOY_ENV | OPP_SERVER_NAME | 端口 / Bind IP                     | 镜像来源 |
|---------------------|------------------------|------------|------------------|------------------------------------|----------|
| `deploy-local.sh`   | macOS 开发 / Win        | local      | （空）           | 8090/4175 @ 0.0.0.0                | kx-base 现场构建 或 已加载镜像 |
| `deploy-154.sh`     | 154 Linux 生产          | server     | 154              | 8090/4175 @ 172.16.2.154           | 离线镜像 load-images.sh |
| `deploy-245.sh`     | 245 Linux 生产          | server     | 245              | 8091/4176 @ 172.16.2.245           | 离线镜像 load-images.sh |

入口流程：

```
1. source deploy/bin/env.sh        # OS 派生 base dir / 端口 / 项目名
2. deploy/bin/init-dirs.sh         # 建子目录（条件 DB + blue-green）
3. deploy/bin/ensure-databases.sh  # 复用检测 vs 容器化
4. exec deploy/bin/start.sh "$@"   # 启动（blue-green stage + healthcheck + 自动回滚）
```

## 四、数据库复用 vs 容器化

`deploy/bin/ensure-databases.sh` + `lib/database-detect.sh` 实现：

```
detect 命中（docker / systemd / 端口可达）  →  mode=external/remote/local-port  → 不创建容器
detect 未命中 + OPP_DEPLOY_<DB>=true       →  mode=container                   → 容器化起一个 + 写 DSN 到 .env
detect 未命中 + OPP_DEPLOY_<DB>=false      →  mode=remote-required             → 警告，DSN 由 .env 注入远端（如 252）
OPP_DEPLOY_<DB>=external                   →  mode=external（强制）             → 跳过探测，DSN 必须由 .env 提供
```

154 / 245 默认 `OPP_DEPLOY_PG=false`，detect 命中 252 内网 → `remote` 模式，本机不起 PG。

容器化 PG / Redis / MySQL 用新增的 `deploy/bin/docker-compose.db.yml`：
- bind-mount `${DEPLOY_BASE_DIR}/<db>/` → 容器内数据目录
- 单端口绑 127.0.0.1（不对外暴露）
- 自动生成密码 → 写入 `.env`（不覆盖已有值）

## 五、Blue-Green 切换

### 目录布局

```
bin/
├── pocket-opp-pa348560-20260903/   # 历史版本
├── 1.0.43.002/                     # 历史版本
├── 1.0.43.003/                     # ← 当前活跃版本
├── current → 1.0.43.003
└── 1.0.43.004.failed/              # 启动失败被回滚的版本（保留供排查）
```

### `bin/<id>/` 内容

- `version.json`：`{ id, version, build, image_tag, commit, deployed_at, previous, host, deploy_env }`
- `pocketd-compose-snippet.yml`：docker compose 拼接片段
- `migration-pre.d/`、`migration-post.d/`：用户可挂自定义钩子

### 切换流程

1. 计算 `OPP_VERSION_BUILD`（默认 `{image_tag}-p{git_rev}-{ts}`，或显式 `OPP_DEPLOY_VERSION` + `OPP_DEPLOY_BUILD`）
2. `bg_stage`：落一份完整的发布目录到 `bin/${OPP_VERSION_BUILD}/`
3. 启动容器 → healthcheck 60s 通过
4. `bg_switch`：原子 `ln -sfn ${OPP_VERSION_BUILD} bin/current`；previous 写到 `version.json`
5. healthcheck 失败 → `bg_rollback`：把 `bin/current` 指回上一个 verified 版本，失败的版本移到 `.failed/`

### 工具

```bash
./deploy-local.sh --rollback    # 一键回滚
./deploy-local.sh --version 1.2.3 --build 005   # 显式指定版本
./deploy-local.sh --dry-run     # 不真起容器，只跑探测 + stage
```

清理：`bg_prune 5` 保留最近 5 个版本，其余移到 `backups/bin-pruned-<ts>/`。

## 六、测试方案与结果

### 单元测试（4 套，77 cases）

| 测试 | 覆盖 | 结果 |
|------|------|------|
| `test_os_detect.sh`       | OS 派生（macOS / Linux / WSL / Windows-MSYS）+ base dir | 20 / 20 PASS |
| `test_init_dirs.sh`       | 9 个 always-create 目录 + 3 个条件 DB 目录 + .gitkeep/.gitignore + 154 模式 + 幂等 | 38 / 38 PASS |
| `test_blue_green.sh`      | bg_init / compute_id / stage / switch / current / rollback / prune / 重复 stage 拒绝 | 12 / 12 PASS |
| `test_database_detect.sh` | PG / Redis / MySQL 三种 DB 的 docker / systemd / port-reachable / not-found 检测 | 7 / 7 PASS |

### 集成 dry-run 测试（1 套，27 cases）

| 场景 | 覆盖 | 结果 |
|------|------|------|
| `deploy-local.sh --dry-run`            | 9 目录创建 + bin/ + bin/current 自动 stage + .env.local 生成 | 17 / 17 PASS |
| `deploy-154.sh` 派生（dry-run）         | POCKET_ENV_FILE=.env.154 + POCKET_PORT_BIND_IP=172.16.2.154 | 2 / 2 PASS |
| `deploy-245.sh` 派生（dry-run）         | POCKET_ENV_FILE=.env.245 + POCKET_PORT_BIND_IP=172.16.2.245 + 端口 8091/4176 | 4 / 4 PASS |
| `OPP_DEPLOY_PG=true` → postgres/ 创建 | 条件 DB 目录 | 3 / 3 PASS |

### 总计

```
单元：  77 PASS  /  0 FAIL
集成：  27 PASS  /  0 FAIL
合计： 104 PASS  /  0 FAIL
```

完整测试日志：
- `test-evidence/deploy-2026-09-03/run-all-output.log`
- `test-evidence/deploy-2026-09-03/test_{os_detect,init_dirs,blue_green,database_detect}.log`
- `test-evidence/deploy-2026-09-03/deploy-integration-test.log`

### 真实 end-to-end dry-run（macOS 本机）

```
DEPLOY_BASE_DIR=/tmp/opp-debug-test OPP_PG_PASSWORD=test ./deploy-local.sh --dry-run
→ init-dirs.sh 创建 10 个 always-create 目录
→ ensure-databases.sh 检测 PG/Redis/MySQL（外部无 → remote-required）
→ 写 config/.env.local（含 JWT_SECRET 与 DSN 占位）
→ start.sh 自动 stage bin/pocket-opp-pa348560-20260903231302/ + bin/current 符号链接
→ --dry-run 短路 docker compose up（打印预期命令而非真起容器）
```

blue-green 切换 end-to-end：

```
bin/pocket-opp-pa348560-20260903231302  ← 初始 active
→ bg_stage 1.0.0.002 + bg_switch        ← 切到 1.0.0.002，previous=pocket-opp-...
→ bg_rollback                            ← 切回 pocket-opp-...，失败的 1.0.0.002 移到 .failed/
```

## 七、文件清单

### 新增

- `deploy-local.sh`（macOS / Win 本地）
- `deploy-154.sh`（154 Linux 生产）
- `deploy-245.sh`（245 Linux 生产）
- `deploy/bin/lib/os-detect.sh`
- `deploy/bin/lib/database-detect.sh`
- `deploy/bin/lib/blue-green.sh`
- `deploy/bin/ensure-databases.sh`
- `deploy/bin/docker-compose.db.yml`
- `deploy/bin/tests/test_os_detect.sh`
- `deploy/bin/tests/test_init_dirs.sh`
- `deploy/bin/tests/test_blue_green.sh`
- `deploy/bin/tests/test_database_detect.sh`
- `deploy/bin/tests/run-all.sh`
- `tests/deploy-integration-test.sh`

### 修改

- `deploy/bin/env.sh`（OS 派生、新增 OPP_* exports、154/245 server 端口派生）
- `deploy/bin/init-dirs.sh`（新增 5 个子目录 + 条件 DB 目录 + .gitignore 模板）
- `deploy/bin/start.sh`（blue-green stage / switch / rollback 集成 + --dry-run 模式）
- `deploy/bin/docker-compose.opp.yml`（新增 raw-logs / attachments / run bind mounts）
- `deploy/bin/README.md`（重写章节反映新约定）

### 不动

- `stop.sh` / `status.sh` / `logs.sh` / `save-images.sh` / `load-images.sh` / `build-images.sh` / `tunnel-252.sh` / `rebuild-db-local.sh` / `legacy-env.sh`
- `deploy/deploy.sh` / `deploy/deploy_test.sh` / `deploy/acc-integration/*`
- `deploy-252.sh`（保留为旧 deploy-154 / deploy-245 的兼容入口，行为等价）

## 八、风险与回滚

| 风险 | 缓解 |
|------|------|
| 旧路径 `~/Downloads/kaixuan/opp` 不再用 | `DEPLOY_BASE_DIR` 可显式覆盖；env.sh 启动时给一次性迁移提示 |
| blue-green 引入新目录 | `bin/current` 不存在时 start.sh 自动 stage（首次升级兼容） |
| DB 检测假阳性（端口被无关服务占用） | detect 命中后给 warn 提示，让用户确认 DSN |
| Windows 支持有限 | docker daemon 必须装；脚本对 Windows 给出清晰错误而非兜底 |
| 154 / 245 服务器上以非 root 跑 | deploy-154.sh / deploy-245.sh 显式检查 root 权限并退出 |

## 九、变更记录

- **2026-09-03（本次重构）**：3 个入口 + 4 套库（OS detect / DB detect / blue-green / ensure-databases）+ 5 套测试（4 单元 + 1 集成）全部落地；104 tests 全部 PASS。
