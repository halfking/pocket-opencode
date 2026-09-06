# OpenPocket · v5 整合对齐文档

> 本文档取代 `v4-融合对齐.md` 成为最高层对齐依据；旧文冻结归档（原位保留）。
> 总方案：工作区 `docs/2026-09-06-multi-agent-platform-integration/00-总纲-多智能体工作平台整合方案v5.md`

## 本项目在 v5 中的定位
- 移动端（Android/iOS）+ 轻量 Web：任务/审批/告警/知识问答随身入口
- 模块设计：工作区 `docs/2026-09-06-multi-agent-platform-integration/modules/openpocket/README.md`

## 资源对齐（2026-09-06 核实）
- integration 模式 compose：`deploy/acc-integration/docker-compose.yml`（pocketd + frontend，acc-local-net + shared-infra）
- PG：共享 `llm-gateway-pg`（pocket 库 + role pocket_app）；Redis：DB8 前缀 `pocket:`（Phase 0 登记）
- **漂移 D2**：deploy-local.sh 默认 `OPP_DEPLOY_PG=true` 自建 PG，与 integration 模式冲突

## 本仓库待办（对应 v5 Phase）
- [ ] Phase 0-D2：deploy-local.sh 默认复用 llm-gateway-pg，自建改显式 opt-in
- [ ] Phase 2：GP2（创建任务→ACC 派发→执行→完成回显）通过
- [ ] Phase 3：APK 构建链确认 + 模拟器 UI 全遍历（或 H5 移动端替代并注明）

## 铁律
- r1.12/pocket-local 变体为 legacy-only，不与 canonical integration 栈并存。
