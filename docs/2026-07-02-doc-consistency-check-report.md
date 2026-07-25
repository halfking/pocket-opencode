# 📋 OpenCode Pocket 文档一致性检查报告

**检查日期**: 2026-07-02
**工作目录**: `/Users/xutaohuang/workspace/official-deploy/services/opencode-pocket`

---

## 1. 架构蓝图模块计数检查

### 文档声明
- **文件**: `docs/2026-07-02-frontend-app-blueprint-v1.md`
- **声明**: "5 个主功能模块 + 3 个原生能力 = 8 个子系统"

### 实际代码结构
- **Backend 模块数**: 21 个 (backend/internal/)
  - aigate, websocket, kxmemory, config, feishu, notification, server
  - mcp, adapter, updates, notes, task, model, registry, db
  - email, opencode, tasksync, vault, llmgateway, stt

### 分析结果
✅ **一致** - 文档描述的是**前端功能模块**（5个：AI工具、笔记、会议、邮件、密码箱），而非后端代码模块。
- 前端5大功能模块对应文档描述
- 后端21个模块是技术实现层的划分（支撑前端功能）

---

## 2. Phase 描述一致性检查

### 文档中的 Phase 命名
```
Phase 0~5: 数字编号（整体项目阶段）
Phase A: 笔记模块已就绪
Phase D: 邮件/密码箱基础完成
Phase 2: 邮件完整功能
Phase 4: 密码箱详细页
Phase 5: MCP 任务推送
Phase 6A: 会议功能
Phase 6B: 聊天聚合
Phase E: 云同步
```

### 分析结果
⚠️ **混合命名** - 存在数字(0-6)和字母(A,D,E)两种命名体系，但有清晰上下文：
- **数字 Phase**: 项目整体迭代阶段
- **字母 Phase**: 特定模块的开发阶段
- 文档中有明确说明："基于 Phase 0~5 + 端到端部署验证"

✅ **建议保持现状** - 两种命名体系服务于不同目的，文档已清晰说明。

---

## 3. 主机名统一性检查

### 检查范围
- `docs/2026-07-02-email-assistant-design.md`
- `docs/2026-07-02-backend-tasks-kxmemory-llmgateway.md`
- `docs/2026-07-02-phase5-acc-task-integration.md`

### 发现的主机名

#### kxmemory 相关文档
```
docs/2026-07-02-backend-tasks-kxmemory-llmgateway.md:217
  POCKET_KXMEMORY_BASE_URL=http://kxmemory.kxpms.cn
  
docs/2026-07-02-backend-tasks-kxmemory-llmgateway.md:234
  LLM_GATEWAY_URL=http://llm-gateway.kxpms.cn
```

#### Phase 5 ACC 集成文档
```
docs/2026-07-02-phase5-acc-task-integration.md:16
  远程（acc.kxpms.cn/mcp）

docs/2026-07-02-phase5-acc-task-integration.md:68
  POCKET_MCP_BASE_URL（如 https://mcp.kxpms.cn/acc/mcp）

docs/2026-07-02-phase5-acc-task-integration.md:136
  WebSocket endpoint: wss://acc.kxpms.cn/events
```

### 问题分析

❌ **不一致问题**:

1. **协议不统一**:
   - kxmemory: `http://` (217, 234行)
   - acc (phase5): `https://` 和 `wss://` (68, 136行)
   - acc (phase5 表格): 无协议 `acc.kxpms.cn/mcp` (16行)

2. **子域名规范**:
   - ✅ 规范: `kxmemory.kxpms.cn`, `llm-gateway.kxpms.cn`
   - ⚠️ 混合: `acc.kxpms.cn` vs `mcp.kxpms.cn`

### 建议修正方案

#### 统一协议规范
```bash
# 生产环境应统一使用 HTTPS/WSS
kxmemory.kxpms.cn          → https://kxmemory.kxpms.cn
llm-gateway.kxpms.cn       → https://llm-gateway.kxpms.cn
acc.kxpms.cn               → https://acc.kxpms.cn
mcp.kxpms.cn               → https://mcp.kxpms.cn (如果是独立服务)
```

#### 子域名命名统一
需要明确：
- `acc.kxpms.cn/mcp` - ACC服务下的MCP端点？
- `mcp.kxpms.cn/acc/mcp` - 独立MCP网关下的ACC路径？

建议选其一并在所有文档中统一。

---

## 4. Backend Schema 检查

### SQL 文件分析

#### appendix-a-pg-migration.sql
- **表数量**: 8 张表 (PostgreSQL)
- **Schema**: `voice_notion` (独立schema)
- **表清单**: notes, workspaces, knowledge_blocks, todos, smart_links, ssot_conflicts, classification_history, user_preferences

#### schema-optimization.sql  
- **类型**: SQLite 性能优化脚本
- **内容**: 索引、触发器、视图、统计表
- **目标表**: local_notes, local_emails, local_todos, local_vault_entries 等

### 文档对比

❌ **未找到独立的 backend-schema.md 文档**

现有schema文档分散在：
1. `appendix-a-pg-migration.sql` - PostgreSQL voice_notion schema
2. `frontend/src/native/schema-optimization.sql` - 前端SQLite schema优化
3. `docs/2026-07-02-email-assistant-design.md` - 包含email相关表DDL

### 建议
📝 **需要创建统一的 backend-schema.md**，整合：
- PostgreSQL 表结构 (pocketd 后端)
- SQLite 表结构 (前端本地存储)
- 表之间的关系说明
- 数据同步策略

---

## 5. 总结与行动建议

### ✅ 已验证通过
1. 前端架构蓝图中的"5+3模块"描述准确
2. Phase命名体系虽混合但有明确语境
3. SQL文件结构清晰，表设计合理

### ⚠️ 需要修正
1. **主机名协议统一** (优先级: 高)
   - 统一使用 https:// 和 wss://
   - 明确 acc.kxpms.cn vs mcp.kxpms.cn 的架构选择
   
2. **创建统一 backend-schema.md** (优先级: 中)
   - 整合分散的schema文档
   - 说明PG和SQLite的分工

### 📋 修正清单
- [ ] 修正 `2026-07-02-backend-tasks-kxmemory-llmgateway.md` 中的协议 (http→https)
- [ ] 统一 Phase 5 文档中的 MCP 端点命名
- [ ] 创建 `docs/backend-schema.md` 整合schema文档
- [ ] 在 email-assistant-design.md 中补充实际部署的主机名

---

**报告生成时间**: 2026-07-02
**检查工具**: grep, find, wc, manual review
