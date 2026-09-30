# 数据库预检前置（check-databases.py）

> 2026-10-01 审计轮新增。`deploy/bin/lib/check-databases.py` 是登录级预检：
> TCP 端口通 ≠ 能登录 ≠ schema/权限对。`start.sh` 在真实部署前强制跑它，
> **没装下列依赖的机器会被拦下**——本文说明要装什么、拦在哪、哪里可以降级。

## 一、依赖清单

| 依赖 | 用在哪 | 缺了会怎样 |
|---|---|---|
| `python3` | 预检本体；`ensure-databases.sh` 生成 DSN 时的密码 URL 编码 | 预检无法执行 → 部署中止（fail-closed） |
| `psql` | PostgreSQL 登录级预检：CONNECT / schema USAGE+CREATE / 表与序列权限 | `psql client required for preflight` → 中止 |
| `redis-cli` | Redis `PING`（配置了 `POCKET_REDIS_URL` 才检查） | 配了 URL 却没装 client → 中止；没配 Redis 则不需要 |
| `docker` + compose v2 | 渲染 compose 环境做预检（与运行时同一解析器）；`--can-create` 的容器清单 | compose 环境渲染失败 → 中止 |
| `ps -axo` | 仅 `--inventory` / `--can-create` 的原生进程清单（防重复置备） | MSYS 等受限环境可降级，见下节 |
| `openssl` | `deploy-local.sh` 首次生成 JWT 密钥 / 数据库随机密码 | 拒绝生成（不可降级为可预测字符串） |

**MySQL 不需要客户端**：MySQL 只有 compose 服务和防重复置备检查（进程/端口/容器清单），
没有登录级预检，`mysql` CLI 不装也不影响预检通过。

### 按 OS 安装

| OS | 命令 |
|---|---|
| Debian / Ubuntu | `apt install python3 postgresql-client redis-tools` |
| RHEL / CentOS | `dnf install python3 postgresql redis`（redis-cli 在 redis 包里） |
| macOS | `brew install python libpq redis`（libpq 是 keg-only，脚本已特判 `/opt/homebrew/opt/libpq/bin/psql` 与 `/usr/local/opt/...` 两个路径） |
| Windows | 需 **Windows 原生** python3（python.org）与 psql/redis-cli 并进 PATH；Git Bash 自带的工具链不够 |

## 二、fail-closed 行为

三个脚本都是 `set -euo pipefail`，预检非零退出即中止整条链：

1. `start.sh`（非 dry-run、非 frontend-only）：
   `python3 check-databases.py --env-file … --compose-file …` 失败 → **不切换 release**。
   PG 缺 CONNECT、schema 权限不足、Redis PING 失败都算失败。
2. `ensure-databases.sh`：起容器前过 `--can-create` 门禁——本机端口被占、
   已有原生进程/Docker 候选、非空数据目录、已有 DSN → **拒绝置备**
   （堵「孤儿卷 + 未知随机密码」的数据丢失路径）。凭据先于卷初始化持久化。
3. 脱敏承诺：预检所有错误信息是固定文案，**从不打印 DSN、密码或客户端原始错误**；
   PG 密码走 `PG*` 环境变量不进 argv。

**唯一的降级点**：`deploy-local.sh --dry-run`（见下节）。真实预检不降级。

## 三、MSYS（Git Bash on Windows）降级路径

Windows MSYS 的 `ps` 不支持 `-axo`，进程清单拿不到。行为分两条：

```
# dry-run 里只有清单是信息性的（deploy-local.sh）：
python3 check-databases.py --inventory \
  || echo "  ⚠️  数据库清单在本机不可用（ps/docker 受限）；计划仅供参考"
```

- `--dry-run` 的 `--inventory` 失败 → **只打 ⚠️ 告警，不判死 dry-run**（清单只服务计划展示）。
- `--env-file` 预检（dry-run 与真实部署同一命令）和 `--can-create` 门禁 → **保持 fail-closed**：
  缺 python3/psql/redis-cli 或凭据不对，一样中止。
  在 MSYS 上部署，必须先装 Windows 原生依赖并确认 `python3 -V`、`psql -V` 在 Git Bash 里可用。

## 四、部署前自查

```bash
# 1) 依赖是否齐（四条都应有输出）
python3 -V; psql -V; redis-cli -v; docker compose version

# 2) 单跑预检（不部署）：通过 → 逐行打印 PG/Redis 验证结果
python3 deploy/bin/lib/check-databases.py --env-file <你的.env> --compose-file deploy/bin/docker-compose.db.yml

# 3) 看本机已有数据库候选（复用/防重判断依据）
python3 deploy/bin/lib/check-databases.py --inventory
```

单测：`bash deploy/bin/tests/test_database_preflight.sh`（24 条）。
