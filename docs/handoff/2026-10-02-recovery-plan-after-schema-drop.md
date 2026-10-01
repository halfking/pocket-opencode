# opencode_pocket 被整体删除后的恢复方案（未执行，待授权）

日期：2026-10-02
触发：2026-10-01 22:48 整个 `opencode_pocket` schema 被 `DROP … CASCADE`

> 本文只是方案。**我没有执行任何一步**，也没有改动生产库。
> 每一步都标了「可逆性」与「需要什么」，等你点头再逐步做。

## 当前状态（22:55 实测）

- `opencode_pocket` schema **不存在**，`postgres` 库里只剩 `public` 与两个
  `meeting_test_*` 残留（各 2 张表）。
- 在跑的 pocketd（PID 33948 / 端口 18099）每 5 秒报错一次，**尚未重建 schema**
  ——它的迁移只在启动时跑。所以恢复窗口还在。
- 磁盘上**没有任何备份**：扫过 `C:\workspace` 的 `*.dump` / `*.backup`，
  以及 `rebuild-db-local.sh` 约定的 `~/Downloads/kaixuan/opp/backup`
  （该目录从未创建过——那个脚本针对的是 Docker 里的另一套 PG）。
- `pg_stat_statements` 未安装、`log_statement=none` ⇒ **DROP 的语句文本已无法
  从 PG 侧恢复**。
- **已定位到元凶代码的位置**：6 个 worktree 仍在用未修复的
  `server_auth_extended_test.go`（DSN 带 `search_path` 时 cleanup 会
  `DROP SCHEMA … CASCADE`）。逐个 worktree 的对照表见
  `2026-10-02-schema-drop-incident-2026-10-01-1947.md`。

> **恢复前请先做这件事**：把 6 个未修复的 worktree 同步到 main，否则一边恢复
> 一边可能再被删一次。


## 损失清单

| 数据 | 丢失 | 能否恢复 |
|---|---|---|
| 表结构 | 全部 | **能**，重启 pocketd 自动重建 |
| `chat_agents`（277 个内置角色） | 是 | **能**，仓库里有种子文件 |
| `users` / `workspaces`（admin） | 是 | **能**，重新 bootstrap |
| `llm_gateway_configs` + `user_settings` | 是 | **能**，可重新写入并实测 |
| `email_accounts`（5 个） | 是 | **能**，有 seed 脚本（需凭据） |
| `emails`（120 封） | 是 | **只能重新抓取**，需真实 IMAP 凭据 |
| 其它用户自建数据 | 假设有 | **不能** |

## 恢复步骤

### 0. 先止血（可选）
停掉 PID 33948，避免它在重建过程中持续刷错误日志、也避免它自动重抓邮件
与步骤 6 冲突。**不可逆性：无**（随时可重启）。

### 1. 重启 pocketd —— 表结构自动回来
`internal/db/pg.go:102` 在 `POCKET_PG_SCHEMA` 非空时会
`CREATE SCHEMA IF NOT EXISTS`，随后各 store 跑自己的迁移。

关键环境变量（缺一不可）：
```
POCKET_POSTGRES_DSN=postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable
POCKET_PG_SCHEMA=opencode_pocket
POCKET_DATA_DIR=<那把能解开 IMAP 凭据的 data 目录>
POCKET_SCHEDULER_ENABLED=false      # 重建阶段先别抓邮件
```
`POCKET_DATA_DIR` 必须是 `C:\workspace\openpocket\wt3\backend\data`——
实测 5 把 `email_master.key` 里只有它能解开那两个真实账户的凭据
（`C:\workspace\openpocket\data` 那把只能解网关）。

**不可逆性：无。** 重建后立刻做第 2 步。

### 2. 回填 277 个内置 agent
`deploy/sql/chat_agents_seed.sql`（2.0 MB，278 条 INSERT，已验证包含
`customer-success-manager`）。它自带 `CREATE TABLE IF NOT EXISTS` 与
`ON CONFLICT (id) DO NOTHING`，**幂等、可重复执行**。

但表名未限定 schema，依赖 `search_path`，且文件里**没有** `CREATE SCHEMA`：

```sql
SET search_path TO opencode_pocket, public;
\i deploy/sql/chat_agents_seed.sql
```

**不可逆性：无**（幂等）。

### 3. 重新 bootstrap admin
`users` 表为空时 pocketd 拒绝用内置缺省口令建号（`336c883` 之后的行为，
因为 6 字符内置口令撞 8 字符下限、那条路径注定失败）。必须显式给：

```
POCKET_AUTH_PASS=<≥8 字符>   # 仅本次启动需要
```
之后用户自己在设置页改密码。

**不可逆性：低**（会新建一行 users）。

### 4. 重新写入网关配置 —— 这一步我已经能独立完成
走应用自身的 `POST /api/llm-gateway/config`（不直接写库），只带
`baseURL` + `apiKey`，模型列表沿用内置 9 个默认项。

验收：`GET /v1/models` 200、`POST /v1/chat/completions` 200、
`GET /api/integration/status` 显示 `llm_gateway: enabled/configured`。

密钥**不在磁盘、不进命令行**：进程内解密 / 进程内 POST。
需要你把 key 再发一次（之前那条已随上一批上下文过去，我不愿把它写进任何文件）。

**不可逆性：低**（新增一条 active 行，旧行自动置 inactive）。

### 5. 重新导入 5 个邮箱账户
`scripts/seed_email_accounts.sh`，走 API 幂等 upsert，凭证从 envs loader 读。

**需要你提供**：envs loader 的位置 + admin 口令。
**不可逆性：低**。

### 6. 重新抓取邮件
只有 `wt3` 那把 master key 能解开凭据，所以抓取必须由使用该 data 目录的
实例执行，且 `POCKET_SCHEDULER_ENABLED=true`。

**需要你提供**：确认这 5 个账户的 IMAP 凭据仍然有效。
**不可逆性：中**（会往生产库写入邮件；建议先只放行 1 个账户试跑）。

## 我建议的顺序与检查点

1. 停 33948 → 2. 重启（`SCHEDULER=false`）→ **立刻核对表数量**
   → 3. 回填 agent（核对 277）→ 4. bootstrap admin（核对 users=1）
   → 5. 网关（我来做，带实测）→ 6. 邮箱账户 → 7. 邮件抓取

每一步做完先核对再进下一步。**任何一步出问题就停下，不要连着往下走。**

## 还没做、但强烈建议的两件事

1. **给本地 PG 打开 DDL 日志**（`log_statement = 'ddl'`，需 reload）。
   今天两次事件都因为它没开而查不清源头。这是唯一能防止第三次还查不出的措施。
   属部署配置变更，**等你同意**我再改。
2. **加一条自动备份**。`rebuild-db-local.sh` 里有现成的「重建前自动备份」
   逻辑，但它绑在 Docker 那套 PG 上，本机这套裸跑的 PG 完全没覆盖。
   今天这次若有任何备份，120 封邮件就不必重抓。
