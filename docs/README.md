# openpocket 文档索引

> 本目录为 openpocket（pocket-opencode）文档中心。现行文档在本文件与各子目录 README 中索引；历史过程文档统一归档于 `archive/`，只归档不删除。

## 入口

| 文档 | 说明 |
|---|---|
| [../README.md](../README.md) | 项目总览、构建与运行 |
| [AUTH_REDCLAW.md](AUTH_REDCLAW.md) / [AUTH_REDCLAW_CURRENT.md](AUTH_REDCLAW_CURRENT.md) | RedClaw 认证系统全量文档 / 当前实现基线 |
| [opencode-contract.md](opencode-contract.md) | OpenCode 适配契约（现行事实以此为准） |
| [v5-integration.md](v5-integration.md) | v5 平台整合对齐（最高层对齐依据） |
| [scheduled-task-system.md](scheduled-task-system.md) | 计划任务系统 |
| [MOBILE_ARCHITECTURE_V2.md](MOBILE_ARCHITECTURE_V2.md) | 移动端架构 v2 |
| [DESIGN.md](DESIGN.md) / [DATA_ARCHITECTURE.md](DATA_ARCHITECTURE.md) / [NAVIGATION_ARCHITECTURE.md](NAVIGATION_ARCHITECTURE.md) | 设计 / 数据架构 / 导航架构 |
| [2026-09-08-meetings-studio.md](2026-09-08-meetings-studio.md) | 会议工作台现行产品面（列表 / 详情 / ACC） |

## 指南（guides/）

[OPERATIONS_GUIDE.md](guides/OPERATIONS_GUIDE.md) · [DEPLOYMENT_GUIDE.md](guides/DEPLOYMENT_GUIDE.md) · [INSTALLATION_GUIDE.md](guides/INSTALLATION_GUIDE.md) · [DEPLOYMENT_CHECKLIST.md](guides/DEPLOYMENT_CHECKLIST.md)

## 专题子目录

| 目录 | 说明 |
|---|---|
| [新架构v1/](新架构v1/) | 新架构方案全集（含子目录索引 README） |
| [优化v4/](优化v4/) | 优化 v4 方案与报告 |
| [security/](security/) | 威胁模型与安全 ADR |
| [governance/](governance/) | 文档治理：[SUPERSEDED.md](governance/SUPERSEDED.md)（被取代文档索引）、[STATUS-MATRIX.md](governance/STATUS-MATRIX.md)、[EVIDENCE-LEDGER.md](governance/EVIDENCE-LEDGER.md) |
| [redclaw-mapping/](redclaw-mapping/) | RedClaw 映射分析 |
| [audits/](audits/) · [handoff/](handoff/) | 近期审计与交接 |
| `2026-MM-DD-*` 目录/文件 | 按日期的专题设计与执行记录（最新工作区） |
| [test-evidence/](../test-evidence/) | 按日期的测试证据（仓库根） |

## 归档（archive/）

一次性交付/测试/审计报告与被取代方案，按归档年份月份存放，只增不删：

- `archive/2026-07/` — 2026 年 6–7 月开发期报告（部署/交付/测试/OpenCode 适配系列）、I18N 实施报告
- `archive/2026-08/` — 8 月审计与交接（AUDIT_ROUND_7、SECURITY_AUDIT_R8、P0–P3 移动端验证）、`全面优化v1/`、`v4-融合对齐.md`、`AUTH_REDCLAW_MIGRATION.md`
- `archive/2026-09/` — 9 月一次性审计报告

> 归档约定：被 `governance/SUPERSEDED.md` 标记或使命完结的过程文档移入对应月份目录；现行契约/方案保持原位。
