# 2026-11-15 · Agent Manager 审计与 OpenPocket 回归

基线：Codeup main `5805020b0a893efb137bfbeaa875114868a4200e`。
原 GitHub/source 工作树的其他任务修改保留；候选在隔离 clone 中修复和验证。

## 修改范围

- 消息页在固定底栏之上独立滚动，邮箱入口可达。
- 笔记页加载阶段仍可切换入口；本地库锁定时继续阻止业务访问。
- ACC 日志进程运行时负责连接、重放、退避、终止与身份隔离；视图只订阅。
- 看板快照只在当前进程同一 authority 内复用，废弃无身份归属的旧缓存。
- 字幕翻译按片段排队，绑定语言、会议、文本版本和会话代次。
- CDP 验证测试包名并及时结束；PKM 清场绑定当前 auth 租约和 workspace，精确匹配两个测试标题。
- Codeup 基线已有的 OIDC 有界读取、字幕 helper、样式 token、键盘布局和邮件测试夹具问题得到修复。

## 审查与证据

- 原工作树绿灯和早期候选的历史绿灯不冒充最终发布验证。
- 独立代理审查记录见 `docs/audit/`；Sonnet 外部审查指出的 PKM 租约问题新增两项 SQLite 先红后绿回归，另两条身份竞态误报经实际代码/执行语义核对撤回。
- 既有静默 catch、流卸载、样式和有界读取门禁未增加通用豁免；精确 census 随真实调用点更新，原因逐项记录。
- 最终前端全量 438/438 文件实际执行，6104 执行事件不作案例数；Go 全量 70 个有测试包、CLI 10 项、类型检查及独立 fetch/Maestro 门禁通过。
- 原生日志不可达有两项 Chromium RED→GREEN；现有 owner/SSE 浏览器组合 20/20。独立只读复核无新增 P1/P2。
- 最终 Android dev 并存包 165698657 字节，SHA-256 为 `c71146fd765ea08bcb4fc981fa0dee751c7dcca50de251f14ff53b6aedffb188`；正式包数据保留。
- 共享 Docker PG 超时后，另建本轮独占 PG17（15439）与候选后端（18099），实际 health、login 和成员绑定签发均返回 200。未重启或改动共享服务。
- 最终原生批次详情见根仓 `docs/agent-manager-plan/results/validation-2026-11-15.json`；新增看板 Mock 已通过，真实 ACC/SOP 不由此代替。

## 未验收

真实 ACC SOP 后端、跨主机移交/续跑、外部 LLM/STT、真实邮件收发与完整离线恢复仍需独立验收。
连接真机使用并存测试包 `com.kaixuan.opencode.pocket.auditnov15`，不清除原正式包数据。
原 GitHub 与 Codeup 历史分叉未强推或覆盖。
GitHub 知识库等 25 个领先提交仍未集成到 Codeup 候选，不能将此版称为两远端全能力已对齐。
旧 STT 错误夹具会覆盖配置后删除整行，另有跨 owner 删除；旧闪卡清场固定正式包名，本轮均未在共享数据或正式包上执行。
闪卡空态改用独占后端空表核实和并存包内保留测试数据清场；邮件仅五个 pocket.test 账户与固定 snippet fallback，不证明真实 MIME 缓存或邮件收发。
