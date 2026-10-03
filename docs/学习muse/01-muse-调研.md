# 01 · Meta Muse 调研：可借鉴的产品与架构决策

**日期**：2026-09-30
**目的**：把 Meta Muse（2026-09-08 上线的个人 AI 智能体）拆成「能力 → 机制 → 对 OpenPocket 的可借鉴点」，
作为 `03-架构方案.md` 的设计输入。本文只写有来源支撑的事实，来源逐条列在文末。

---

## 1. Muse 是什么（一句话）

Meta 把它定位成 **个人 AI 代理**：用户给出目标，Muse 自行拆解步骤、调用外部应用执行，
**关闭 App 后仍在云端继续跑**，只有遇到敏感操作才回来找用户确认。

来源：
- [Meta AI 官网 · Introducing Muse](https://www.meta.com/ai/)（2026-09-08 发布，Muse Spark 1.3 驱动）
- [Economic Times · Meta's free Muse AI app](https://economictimes.indiatimes.com/news/international/global-trends/metas-free-muse-ai-app-can-do-more-than-answer-questions-and-you-can-use-it-through-whatsapp-too/articleshow/133966509.cms)
- [百度百科 · Muse](https://baike.baidu.com/item/Muse/68951307)

---

## 2. 六个核心机制

### M1 · 目标 → 计划 → 持续追踪（Goal → Plan → Tracking）

Muse 对「大目标」（如"四周内把旧车卖出更高价"）会先生成**个性化行动计划**，再在时间轴上追踪进度，
**情况变化时自动调整计划**，而不是一次性回答。
来源：[Zee Business](https://www.zeebiz.com/technology/news-meta-launches-muse-personal-ai-agent-check-features-price-and-availability-401897/amp)、
[百度百科](https://baike.baidu.com/item/Muse/68951307)（"先制订计划、再持续追踪进度"）。

> **可借鉴**：我们的「工作/任务」不该只有扁平列表，需要 **目标 → 任务 → 进度** 的三层，
> 且计划要能被 agent/定时任务持续推进，而不是靠人手动勾。

### M2 · 长期记忆 + 主动建议（Cross-app memory）

Muse 会记住用户此前给过的信息（例：Instagram 收藏的食谱 → 生成购物清单 → 结合**宾客饮食禁忌**生成菜单），
并据此在**未被提问时**主动建议。用户可查看、删除、断开被记住的内容。
来源：[Economic Times](https://economictimes.indiatimes.com/news/international/global-trends/metas-free-muse-ai-app-can-do-more-than-answer-questions-and-you-can-use-it-through-whatsapp-too/articleshow/133966509.cms)、
[ABC News](https://abcnews.com/Business/metas-muse-ai-agent/story?id=136680507)。

> **可借鉴**：这正是「学习模块」的核心 —— 把**读过的东西**（邮件 / RSS / 笔记）沉淀成**会回来找你**的记忆。

### M3 · 后台持续执行（Background execution）

Muse 跑在云端虚拟机里，用户关掉 App 后继续开浏览器、填表单；**只有需要授权或环境变化时才回来**。
来源：[Zee Business](https://www.zeebiz.com/technology/news-meta-launches-muse-personal-ai-agent-check-features-price-and-availability-401897/amp)。

> **可借鉴**：我们已有 `scheduledtask` 调度器（cron/interval/at）与 `scheduled_tasks` 表，
> 缺的只是"到期提醒/每日回顾"这类**学习域**调度器与统一事件出口。

### M4 · 敏感操作必须人工确认（Sentinel 审批）

同机独立安全代理 **Sentinel** 审核 Muse 的每一次对外动作；**未经批准不能访问互联网**；
发邮件、下单等敏感动作强制二次确认；凭证存放在 Muse **读不到**的安全区。
来源：[Economic Times](https://economictimes.indiatimes.com/news/international/global-trends/metas-free-muse-ai-app-can-do-more-than-answer-questions-and-you-can-use-it-through-whatsapp-too/articleshow/133966509.cms)、
[ABC News](https://abcnews.com/Business/metas-muse-ai-agent/story?id=136680507)。

> **可借鉴**：我们的 `task_approval_projections` / `approval_observations` 已经是同一套骨架。
> 缺的是**协作域里的审批**：把任务委派给他人后，对方/AI 的动作也要走同一条审批投影。

### M5 · 读写分离的细粒度权限 + 审计轨迹

用户可逐服务授权（例：允许 AI **读**邮件但不允许**发**邮件），并可查看"AI 做了什么 / 打算做什么"的完整记录，
随时断连服务或让 AI 忘掉某条信息。
来源：[Economic Times](https://economictimes.indiatimes.com/news/international/global-trends/metas-free-muse-ai-app-can-do-more-than-answer-questions-and-you-can-use-it-through-whatsapp-too/articleshow/133966509.cms)、
[百度百科](https://baike.baidu.com/item/Muse/68951307)。

> **可借鉴**：我们的 `notification_rules`（event_source/event_type → 渠道 + 免打扰）
> 已经等价于"哪些事件推给我"；缺的是**动作侧**的授权面（AI 能不能替我发这条通知/建这张卡）。

### M6 · 多入口 + 自定义连接器

App / 网页 / WhatsApp 多入口同源；服务有 API 时用户可让 Muse **自己写连接器**；
后续接入 Instagram / Facebook / 智能眼镜。
来源：[百度百科](https://baike.baidu.com/item/Muse/68951307)、
[Firstpost](https://www.firstpost.com/tech/meta-unveils-muse-ai-for-image-generation-across-instagram-whatsapp-and-meta-ai-app-14029694.html/amp)。

> **可借鉴**：我们已有多端（Web / Android / 飞书 webhook / MCP），但**学习/任务域没有统一入口**。

---

## 3. 能力 → OpenPocket 映射表

| Muse 机制 | 我们的现状（证据见 `02-现状盘点.md`） | 缺口 | 方案落点 |
|---|---|---|---|
| M1 目标→计划 | `tasks` 表扁平，无层级、无目标实体 | 无目标/父子结构 | `work_items.parent_id` + 目标（Phase 3） |
| M2 长期记忆+主动 | 闪卡 due 只在前端算（`cards.go:17-20`） | 服务端无调度真相、无主动回顾 | **Learning Core**（服务端调度 + 每日回顾推送） |
| M3 后台执行 | `scheduledtask` 三种 ScheduleKind 已具备 | 没有学习域调度器 | `learning_digest` / `learning_review` executor |
| M4 审批 | 任务审批投影已具备 | 协作域无审批 | Phase 3 复用 `approval_projections` |
| M5 权限+审计 | `notification_rules` + 审计写入器 | 动作侧授权缺失 | Phase 4 授权面 |
| M6 多入口 | 多端已通 | 学习/工作域无统一入口 | 统一「工作」+「学习」两个一级入口 |

---

## 4. 明确**不**借鉴的部分

- **不做云端虚拟机隔离**：我们是移动端 + 自有后端，凭据已有 `vault` + keystore 加密（见 `02-现状盘点.md` §4），
  引入 VM 隔离属于架构层面重写，成本与收益不匹配。
- **不做交易/代付代理**：无业务场景。
- **不追模型能力**：Muse Spark 是 Meta 自研模型；我们的 agent 能力已由 `opencode` / `llmgateway` 承担，
  本方案聚焦**领域建模与提醒闭环**。

---

## 5. 一句话结论

> Muse 值得学的不是模型，而是**「目标驱动 + 长期记忆 + 后台执行 + 审批审计」四件套**。
> 对 OpenPocket 而言，这四件事恰好映射到：**统一工作实体（目标/任务合一）**、
> **学习模块（把日常输入变成会回访的记忆）**、**服务端调度 + 提醒中枢**、**协作审批与活动流**。
