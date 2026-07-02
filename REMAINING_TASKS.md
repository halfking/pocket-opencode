# 剩余任务清单（第四轮并行处理）

**日期**: 2026-07-02  
**状态**: 三轮审计完成（14/29 项修复），剩余 15 项待处理  

---

## 任务分组（4 个独立并行任务）

### 任务 A: M6 认证逻辑修复（MEDIUM）
**优先级**: 中  
**文件**: `frontend/src/stores/auth.ts`  
**问题**: `isAuthenticated` computed 可仅由 `user.value` 满足（无 token 验证）  
**修复**: 改为 `computed(() => !!token.value && !!user.value)`  
**验证**: 检查 LoginView / router guard 是否依赖此逻辑

---

### 任务 B: 后端代码质量提升（LOW）
**优先级**: 低  
**范围**: 
1. `internal/tasksync/scheduler.go`: 错误处理已改进（第二轮完成），检查是否还有其他静默错误
2. `internal/email/scheduler.go`: 已加注释标明未启动（第三轮完成），检查是否需要补充启动示例
3. 环境变量示例文件：创建 `backend/.env.example` 列出所有 POCKET_* 变量

**产出**: 
- `.env.example` 文件（参考 config.go）
- 后端代码 minor 注释优化（如有）

---

### 任务 C: 前端代码质量提升（LOW）
**优先级**: 低  
**范围**:
1. `native/vector.ts`: 
   - 已加 MAX_VECTORS 上限（第二轮完成）
   - 检查 search() 是否可以优化（当前全量排序，可改为 top-k 部分排序）
2. `features/voice/VoiceRecorderWidget.vue`:
   - 已修 blob URL 泄漏（第一轮完成）
   - 检查 audioPath 管理是否仍有改进空间
3. `native/lobster-init.ts`:
   - lockLobster 互斥锁注释是否清晰

**产出**: 
- vector.ts 优化建议（或小幅优化代码）
- 注释补充（如需要）

---

### 任务 D: 文档一致性检查（LOW）
**优先级**: 低  
**范围**:
1. `docs/architecture-blueprint.md`: 
   - 检查模块计数是否与实际代码一致
   - Phase 描述是否与当前状态匹配
2. `docs/phase-5-email-integration-*.md`:
   - 检查 MCP/kxmemory 主机名是否统一（mcp.kxpms.cn vs 其他变体）
3. `docs/backend-schema.md`:
   - 检查表数量是否与 schema.sql 一致
   - 字段描述是否与代码匹配

**产出**: 
- 文档勘误 patch（如发现不一致）
- 文档验证报告（确认一致性）

---

## 并行执行策略

使用 4 个独立 Agent 并行处理：
- **Agent-A**: M6 认证修复（预计 10 分钟）
- **Agent-B**: 后端代码质量（预计 15 分钟）
- **Agent-C**: 前端代码质量（预计 15 分钟）
- **Agent-D**: 文档一致性（预计 20 分钟）

**预期产出**: 
- 4-6 个代码/文档修复
- 验证报告
- 统一提交到 main

---

## 不处理的项（有意接受）

- **M8**: meetings-store / chat-store 死代码 → Phase 6A/6B 会启用，保留
- **其他 LOW**: 命名规范、注释风格等非功能性优化 → 后续 code review 时处理
