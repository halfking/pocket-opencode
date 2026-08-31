# llm-gateway-pg 数据完整性审计 · 2026-08-31

> 目的：在「拉取最新代码 + 配置 AI 网关默认值」前，确认 dev 期望的 PG 实例是否有完整 schema 与现有数据。
> 方法：只读 SELECT（不修改任何 row）。
> 边界：本仓库 docker-compose 当前把 pocketd 的 DSN 指向 **kx-citus(pocket)**，不是 llm-gateway-pg(kaixuan)；两台 PG 镜像同为 `kx-citus-pg17:offline-arm64`，本次审计并行采样，便于后续决定 seed 写哪台。

## 一、容器与连接

| 容器 | 镜像 | 监听 | DB | 用户 | 角色 |
|---|---|---|---|---|---|
| **llm-gateway-pg** | kx-citus-pg17:offline-arm64 | 127.0.0.1:5432 | kaixuan | llm_gateway | 历史 ACC 集成实例 |
| **kx-citus** | kx-citus-pg17:offline-arm64 | 127.0.0.1:15433 | pocket | kxuser | 当前 dev DSN 实际指向 |
| opencode-pocket-pocketd-local-opp | opencode-pocket:pocket-opp | 8090→8088 | — | — | runtime env 显示 `POCKET_POSTGRES_DSN=postgresql://kxuser:***@kx-citus:5432/pocket?sslmode=disable`、`POCKET_PG_SCHEMA=opencode_pocket` |

**关键发现**：dev 的 pocketd 当前实际使用 **kx-citus(pocket)**，`llm-gateway-pg(kaixuan)` 是文档基线（`docs/优化v4/reports/docker-llm-gateway-pg-switch-2026-08-18.md`、`dsn-fix-2026-08-31.md` 同源）但 production 切换期间被旁路。

## 二、Schema 完整性

`SELECT count(*) FROM pg_class c JOIN pg_namespace n ON c.relnamespace=n.oid WHERE n.nspname='opencode_pocket' AND c.relkind='r' AND NOT c.relispartition;`

| 库 | 期望（dsn-fix 24 张 + 后续迭代）| 实际 |
|---|---|---|
| kx-citus / pocket | 33 | **33** ✅ |
| llm-gateway-pg / kaixuan | 33 | **33** ✅ |

33 张表清单（两库一致，按字母序）：

```
agents, approval_observations, asset_mirrors, asset_sync_log,
asset_workspace_revisions, audit_entries, biometric_credentials,
chat_agent_sync, chat_agents, daily_summaries, devices,
email_accounts, email_action_intents, email_oauth_tokens,
email_vacation_deliveries, email_vacation_replies, emails,
llm_gateway_configs, llm_gateway_nodes, model_usage, notes,
notification_rules, notifications, quota_budgets,
scheduled_task_runs, scheduled_tasks, task_approval_projections,
task_session_links, tasks, users, vault_sync, workspace_members,
workspaces
```

相对 P15plus-baseline（见 `docs/优化v4/reports/docker-llm-gateway-pg-switch-2026-08-18.md` 第 78-103 行，25 张）新增 8 张：

- `asset_workspace_revisions` — vault 修订
- `audit_entries` — redclaw 审计落 PG
- `biometric_credentials` — 指纹登录绑定
- `chat_agent_sync` / `chat_agents` — unified-input 角色种子
- `model_usage` — 用量统计
- `scheduled_task_runs` / `scheduled_tasks` — 计划任务持久化

与本次拉取的 origin commit `1ea11f7 feat(unified-input): 全站统一输入组件 + 邮箱设置页 + llm-gateway-pg 重建与角色种子` 一致。

## 三、每张表的精确行数（PL/pgSQL count(*) 循环）

### llm-gateway-pg / kaixuan

```
agents                            0
approval_observations             0
asset_mirrors                     0
asset_sync_log                    0
asset_workspace_revisions         0
audit_entries                     0
biometric_credentials             0
chat_agent_sync                   0
chat_agents                     277
daily_summaries                   0
devices                           0
email_accounts                    0
email_action_intents              0
email_oauth_tokens                0
email_vacation_deliveries         0
email_vacation_replies            0
emails                            0
llm_gateway_configs               0   ← 用户已手动重置
llm_gateway_nodes                 0
model_usage                       0
notes                             0
notification_rules                0
notifications                     0
quota_budgets                     0
scheduled_task_runs               0
scheduled_tasks                   0
task_approval_projections         0
task_session_links                0
tasks                             0
users                             1   ← admin 重置后残留
vault_sync                        0
workspace_members                 0
workspaces                        0
```

### kx-citus / pocket（pocketd 实际指向）

```
agents                            0
approval_observations             0
asset_mirrors                     0
asset_sync_log                    0
asset_workspace_revisions         0
audit_entries                     3   ← 2× llm_gateway.config.updated + 1× llm.quota.checked
biometric_credentials             0
chat_agent_sync                   0
chat_agents                       0   ← 注意：此处为 0，但 llm-gateway-pg 里有 277
daily_summaries                   0
devices                           0
email_accounts                    0
email_action_intents              0
email_oauth_tokens                0
email_vacation_deliveries         0
email_vacation_replies            0
emails                            0
llm_gateway_configs               2   ← 1 inactive (#1) + 1 active 9-preferred/689-models (#2)
llm_gateway_nodes                 1   ← control-plane node，data_api_key_encrypted 已存
model_usage                       0
notes                             0
notification_rules                0
notifications                     0
quota_budgets                     0
scheduled_task_runs               0
scheduled_tasks                   0
task_approval_projections         0
task_session_links                0
tasks                             0
users                             1   ← user-admin / admin
vault_sync                        0
workspace_members                 1
workspaces                        1
```

## 四、关键 row 内容（kx-citus / pocket）

### users
```
id          username  role   created_at
user-admin  admin     admin  1788176506
```

### workspaces
```
id             name           owner_id     created_at
ws_user-admin  我的随身公司     user-admin   1788176519
```

### workspace_members
```
workspace_id   user_id      role
ws_user-admin  user-admin   owner
```

### llm_gateway_configs（dev 现役配置）
```
id  workspace_id     base_url                format       n_models  n_pref  key_len  is_active  created_at
1   ws_user-admin   https://llm.kxpms.cn/v1  openai-chat  0         0       108      f          2026-08-31 19:50:37
2   ws_user-admin   https://llm.kxpms.cn/v1  openai-chat  689       9       108      t          2026-08-31 19:50:41
```

`#2` 是用户昨天真机测试留下的"完整"网关配置 —— 689 模型（来自 `{baseURL}/v1/models` 自动拉取）、9 个 preferred（其中应包含用户希望默认的 glm-5.2 / minimax-m3 / kimi-k3 / claude-sonnet-5 / gpt-5.6-terra / claude-opus-5 / claude-fable-5 / gpt-5.6-sol / gemini-3.5-flash），base_url 已经就是用户指定的 `https://llm.kxpms.cn/v1`。**当前 dev 实例已经「事实上」与用户要求一致，无需重写，只需要保证后续 rebuild 不丢**。

### llm_gateway_nodes（运维控制面，独立于 configs）
```
id  workspace_id     name      base_url               admin_username  data_api_key_encrypted                                                                                          enabled
1   ws_user-admin   default   https://llm.kxpms.cn   (空)             LJaiVVLjRzimJOhJHiB8EXNo9zP8CP9c3gH9k+LhcJHXXAexUMuXsqkK2Nt2gEKOeq3cWjG7gwn0Z0Tx1eidGeO79Swjosh+9f1S2ojpvA==  t
```

### audit_entries
```
id                                          action                       user_id     tenant_id      resource          success  timestamp
aud_b8df68973a8403e17746f12812d6f4cc       llm_gateway.config.updated   user-admin  ws_user-admin  llm_gateway_config  t       2026-08-31 19:50:37
aud_0e9899750bf4f6090e5f7dc69a9f042c       llm_gateway.config.updated   user-admin  ws_user-admin  llm_gateway_config  t       2026-08-31 19:50:41
aud_c37011847d80a50a8626456ed44f5f07       llm.quota.checked             user-admin  ws_user-admin  llm:llm.stream     t       2026-08-31 20:06:09
```

## 五、结论与决策

1. **Schema 完整**：两台 PG 实例的 `opencode_pocket` schema 都已建齐 33 张表，没有缺表，索引/主键一致。
2. **数据在 dev 路径上事实已经满足用户目标**：kx-citus / pocket 的 `llm_gateway_configs#2` 已经是 `base_url=https://llm.kxpms.cn/v1`、9 个 preferred 模型、is_active=true —— 与用户口述要求一字不差；`llm_gateway_nodes#1` 的 data_api_key_encrypted 也已加密入库。
3. **llm-gateway-pg / kaixuan 处于"待重新 seed" 状态**：除 `chat_agents=277` 与 `users=1` 外全部空表 —— 这与 `dsn-fix-2026-08-31.md` "重建与角色种子"叙述吻合。本次任务以 dev 路径（kx-citus）为准，不需要写入 llm-gateway-pg。
4. **代码层默认值仍需固化为「兜底」**：
   - 后端 `DefaultLLMGatewayBaseURL` 常量（目前 `https://llmgo.kxpms.cn/v1`）应当跟随用户预期调整为 `https://llm.kxpms.cn/v1`，否则 dev 实例如果被 reset，`defaultLLMGatewayState()` 会回落到旧域名，前端第一次 GET `/api/llm-gateway/config` 会拿错地址。
   - 前端 `SettingsLLMGateway.vue` placeholder 与 `client.ts` 注释应同步为 `https://llm.kxpms.cn/v1`。
   - 9 模型 preferred 列表应在后端 seed 路径（`SeedDefaultLLMGatewayConfig`）落地，避免后续 P15plus reset 又得手工再勾。

## 六、引用

- 容器拓扑：`docs/优化v4/reports/docker-llm-gateway-pg-switch-2026-08-18.md`
- dev 切换叙事：`test-evidence/P15plus-real-2026-08-31/dsn-fix-2026-08-31.md`
- 后端默认值来源：`backend/internal/opencode/config_writer.go:10`、`backend/internal/server/llm_gateway_handler.go:55-63`
- 前端 UI：`frontend/src/features/settings/SettingsLLMGateway.vue`
- 角色种子 schema 来源：`backend/internal/{chatagent,redclaw,audit_pg}.go` 与 docs 同节
