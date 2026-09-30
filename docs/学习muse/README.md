# 学习 / Muse 架构方案（2026-09-30）

> 目标：借鉴 **Meta Muse** 的产品架构，把项目里的**任务、笔记、邮件、RSS** 重组成
> **一个统一的「工作」实体 + 一个「学习」模块**，并让学习**符合记忆法则地定时提醒**，
> 协作任务与通知闭环。最新方案文档全部沉淀在本目录。

## 文档索引

| 文档 | 内容 |
|---|---|
| **[交付状态与未验证清单.md](交付状态与未验证清单.md)** | ⭐ **先看这份**。已验证 / 未验证的权威清单，每项附「怎么关掉」的命令 |
| [01-muse-调研.md](01-muse-调研.md) | Meta Muse 六个核心机制 → 对本项目的可借鉴点 / 明确不借鉴的部分 |
| [02-现状盘点.md](02-现状盘点.md) | 源码级现状与缺口盘点（带 `文件:行号` 证据） |
| [03-架构方案.md](03-架构方案.md) | 目标架构：统一 Work Item、Learning Core、记忆法则、协作、通知 |
| [04-数据模型与API契约.md](04-数据模型与API契约.md) | DDL、枚举、HTTP 契约（标 ✅ 已实现 / 📐 规划） |
| [05-实施路线图.md](05-实施路线图.md) | P0–P4 分阶段、验收判据、依赖与边界 |
| [06-ADR.md](06-ADR.md) | 七条架构决策与后果（ADR-001..007） |
| [evidence/2026-09-30-p0-verification.md](evidence/2026-09-30-p0-verification.md) | P0 实测证据：命令、输出、基线对照、**未验证项** |
| [evidence/2026-09-30-p1-verification.md](evidence/2026-09-30-p1-verification.md) | P1 实测证据：前端全门禁、语言包、图标子集、**未验证项** |
| [evidence/2026-09-30-p2-verification.md](evidence/2026-09-30-p2-verification.md) | P2 实测证据：材料管线端到端契约、字体子集漏字根因修复、**未验证项** |
| [evidence/2026-09-30-p3-verification.md](evidence/2026-09-30-p3-verification.md) | P3 实测证据：协作端点、事件→通知映射、路径冲突改名、**未验证项** |
| [evidence/2026-09-30-p3b-verification.md](evidence/2026-09-30-p3b-verification.md) | P3 剩余项：审批只读投影、子任务层级与派生进度、环检测 |
| [evidence/2026-09-30-p4-verification.md](evidence/2026-09-30-p4-verification.md) | P4：工作项提醒生产者、免打扰顺延、ADR-002 契约登记、**部署前置与未做项** |
| [evidence/2026-09-30-p4c-verification.md](evidence/2026-09-30-p4c-verification.md) | P4c：连续学习天数语义、里程碑 exactly-once、补上 digest 执行器的零覆盖 |
| [evidence/2026-09-30-icon-subset-verification.md](evidence/2026-09-30-icon-subset-verification.md) | 图标子集缺字根因收口：20 处正在发生的缺字 + 机器强制的不变量 |
| [evidence/2026-09-30-icon-registry-and-font-gate.md](evidence/2026-09-30-icon-registry-and-font-gate.md) | 图标集中映射表 `constants/icons.ts` + 字体级实测门禁：三个扫描盲区、**当前无活着缺字**的原因、注入实测 |
| [evidence/2026-09-30-runtime-and-build-verification.md](evidence/2026-09-30-runtime-and-build-verification.md) | 运行时与构建产物验证：dev server 启动、生产构建、**动态图标名入产物**、字体 SHA256 一致 |
| [evidence/2026-09-30-server-binary-route-verification.md](evidence/2026-09-30-server-binary-route-verification.md) | 真实服务端二进制验证：14 条路由**确实注册**、通配注册探测陷阱、**登录墙成因定位到 PG** |
| [如何验证真实数据库.md](如何验证真实数据库.md) | **怎么跑**真实 Postgres 集成测试（门控套件已就绪，**尚未执行过**） |

## 三十秒版本

1. **工作 = 任务**，同一实体（`tasks` 表），差别在 `type` 分类（工作/生活/学习/其他四组细类）。
2. **学习是一个模块**：`learning_items` 把笔记、邮件、RSS、会议、聊天统一收口为"学习条目"，
   `stage` 驱动 inbox→learning→review→mastered。
3. **定时提醒符合记忆法则**：间隔重复调度下沉到服务端（FSRS-5 形态纯函数），
   `learning_reminders` 管触发，`scheduledtask` 跑执行器，`notifycenter` 管推送与免打扰。
4. **协作任务**：参与者（owner/assignee/watcher）+ 活动流 `work_item_events` + 六类协作通知。
5. **P0 已落地**：后端地基（枚举、DDL、调度器、存储、服务、路由、执行器、装配），
   编译与单测通过。
6. **P1 已落地**：前端两个入口 —— `/study` 学习中心（今日回顾 / 四项明细 / 每日提醒 /
   学习收件箱）与 `/tasks` 分类视图（工作·生活·学习·其他分组 + 逾期/今天/本周筛选 +
   类型/到期/协作标签），9 语言齐备，全门禁通过。

## 相关既有文档

- `docs/flashcards-contract.md` — 闪卡契约（FSRS 客户端真相，P4 将按 ADR-002 收口）
- `docs/scheduled-task-system.md` — 调度系统
- `docs/2026-09-08-feature-inventory.md` — 全量功能盘点
- `docs/design/2026-09-23-hybrid-tabbar-and-anki-integration.md` — 闪卡/Anki 融合设计
