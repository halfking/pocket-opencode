# 第七轮深度审计 - 新会话提示词

**使用说明**: 将此提示词复制到新的 ZCode 会话中，开始第七轮审计。

---

## 审计提示词

```
你是 OpenCode Pocket 项目的第七轮深度审计专家。这是一个 Capacitor + Vue + Go 混合架构的 AI 助手应用。

## 项目背景

**仓库**: halfking/pocket-opencode (main 分支)
**当前提交**: 7f7f350
**工作目录**: /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket
**已完成**: 前 6 轮审计，修复 47 项问题（所有 BLOCKER/CRITICAL/HIGH 已修复）

## 前置知识（必读）

已完成的 6 轮审计：
1. 第一轮：3 路 agent 并行扫描，修复 2 BLOCKER + 5 HIGH + 3 MEDIUM
2. 第二轮：tsconfig 引入，修复 33 个类型错误 + 3 个安全 gate
3. 第三轮：审计自查，修复遗漏文件 + M5/M7 + 文档
4. 第四轮：4 agent 并行，M6 认证 + 性能优化 + 代码质量
5. 第五轮：4 agent 深度审计，2 回归 + 4 CRITICAL + 5 BLOCKER/HIGH
6. 第六轮：静态 salt 修复 + 错误处理优化

**关键修复**:
- XSS 防御（DOMPurify）
- 并发安全（WS Hub、MCP、SessionCache）
- 加密强化（随机 salt、lockLobster 真正清密钥）
- 事务保护（vault import）
- 认证统一（client.ts authFetch）
- HTTP 超时、飞书时间窗、email ON CONFLICT 回归修复

**剩余已知风险（Phase 0 架构限制，有意接受）**:
- 全 API 无认证（userIDFromRequest 硬编码 "local"）
- 登录密码复用为主密钥（Phase 1 改为独立主密钥）
- M8 死代码（meetings/chat store，Phase 6 会启用）

详细参考: `AUDIT_HANDOFF_SUMMARY.md`

## 第七轮审计目标

### 聚焦方向（按优先级）

**HIGH 优先级 - 回归验证**:
1. **验证前 6 轮修复的副作用**
   - crypto.ts 随机 salt 是否破坏现有用户数据（localStorage 兼容性）
   - DOMPurify 是否过滤了合法的 markdown 语法（代码块、表格等）
   - authFetch 统一错误处理是否破坏调用方逻辑
   - WS Hub toRemove 延迟删除是否导致消息发送到已断开客户端
   - MCP sync.Once 是否阻塞后续重连（初始化失败场景）

2. **0c658d7 提交的不完整代码影响范围**
   - server.New 参数不匹配（缺 5 个参数）
   - handleEmailOAuthAuthorize 未定义
   - 评估是否影响核心功能（/api/tasks、/api/notes、/api/vault 等）

**MEDIUM 优先级 - 端到端安全审计**:
3. **完整数据流追踪**
   - 用户输入（笔记内容）→ 加密存储 → 解密展示 → v-html 渲染
   - 邮件内容 → IMAP 抓取 → 加密凭证存储 → 同步展示
   - vault 密码 → 加密 entry → export/import → 解密查看
   - 检查每个环节是否有明文泄漏、日志泄漏、内存泄漏

4. **API 接口权限矩阵**
   - 列出所有 HTTP 端点（/api/notes/*、/api/vault/*、/api/tasks/* 等）
   - 标注哪些需要认证（当前都无认证，但记录预期行为）
   - 检查是否有敏感端点未来会暴露（如 /api/config/models PUT）

**LOW 优先级 - 性能与文档**:
5. **性能基准测试**（如有时间）
   - vector search 实测（第四轮只做理论分析 O(n·k)）
   - email/notes fetch 并发压测（多账户同时同步）
   - WebSocket 消息吞吐量（100 并发客户端）

6. **文档完整性检查**（如有时间）
   - API 文档是否完整（缺 swagger/openapi spec）
   - 部署运维手册是否存在
   - 安全配置检查清单是否覆盖所有环境变量

### 不要重复的工作

❌ **不要再次审计**:
- 已修复的 BLOCKER/CRITICAL/HIGH（除非验证副作用）
- 命名规范、注释风格等 LOW 级别非功能性问题
- Phase 0 架构限制（全 API 无认证、登录密码复用）

## 审计方法

建议使用 **4 个并行审计 agent**（参考第四/五轮模式）:

**Agent 1: 回归验证专家**
- 任务: 验证前 6 轮修复是否引入新问题
- 重点: crypto.ts、DOMPurify、authFetch、WS Hub、MCP

**Agent 2: 数据流安全专家**
- 任务: 端到端数据流追踪（笔记/邮件/vault）
- 重点: 加密链路、明文泄漏、日志脱敏

**Agent 3: 0c658d7 提交影响分析**
- 任务: 评估不完整代码的影响范围
- 重点: server.New 缺参数、handleEmailOAuthAuthorize 未定义

**Agent 4: API 权限矩阵 + 文档检查**
- 任务: 列出所有端点权限 + 文档完整性
- 重点: 路由表分析、文档覆盖率

## 输出要求

对每个发现，提供：
1. **文件:行号** - 精确位置
2. **严重度** - BLOCKER/CRITICAL/HIGH/MEDIUM/LOW
3. **问题描述** - 清晰简洁
4. **影响范围** - 哪些功能受影响
5. **修复建议** - 具体可执行的修复方案
6. **是否修复** - 本轮修复 or 记录为后续

最后生成：
- 审计报告（markdown 格式）
- 需要修复的问题清单（按优先级排序）
- 验证通过的项（证明前 6 轮修复无副作用）

## 开始审计

请执行以下步骤：

1. 读取 `AUDIT_HANDOFF_SUMMARY.md` 了解前 6 轮成果
2. 检查当前 git 状态和最新提交
3. 启动 4 个并行审计 agent（使用 Agent tool）
4. 汇总结果并生成报告
5. 对需要修复的问题，提供具体修复方案

开始工作。
```

---

## 配套文档

审计新会话时，需要读取的关键文档：
1. **AUDIT_HANDOFF_SUMMARY.md** - 本次交接总结（已创建）
2. **AUDIT_REPORT_R1.md** - 第一轮详细报告（如存在）
3. **ARCHITECTURE.md** - 系统架构
4. **README.md** - 项目概览

## 使用方式

1. 打开新的 ZCode 会话
2. 复制上述"审计提示词"部分
3. 发送给 AI
4. AI 将自动启动 4 个并行 agent 开始审计

---

**注**: 此提示词已包含前 6 轮的上下文，新会话的 AI 可以直接基于此继续工作。
