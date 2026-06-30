# 🔬 方案 C 研究结果：OpenCode 深度集成方案

**研究完成时间:** 2026-06-29 16:55  
**状态:** ✅ 完成  
**关键发现:** OpenCode 使用 MCP 协议管理会话数据

---

## 🎯 核心发现

### OpenCode 的数据架构

```
┌─────────────────────────────────────────┐
│  OpenCode 应用 (localhost:4096)         │
│  - Web UI (Electron)                   │
│  - 不提供传统 REST API                   │
└──────────────┬──────────────────────────┘
               │
               ↓ MCP 协议
┌─────────────────────────────────────────┐
│  MCP ACC 服务器                          │
│  https://acc.kxpms.cn:9002/mcp         │
│  - session.create/search/append        │
│  - session.handoff/pin/archive         │
│  - 存储所有会话数据和历史                 │
└─────────────────────────────────────────┘
```

### 关键发现

1. **OpenCode 本地文件只存储元数据**
   - `~/Library/Application Support/ai.opencode.desktop/`
   - `opencode.global.dat` - 全局设置
   - `opencode.workspace.*.dat` - 工作区设置
   - **不包含会话内容和历史**

2. **真实会话数据存储在 MCP 服务器**
   - `acc` MCP 服务器：`https://acc.kxpms.cn:9002/mcp`
   - 提供完整的会话管理 API
   - 这是 OpenCode 自己使用的接口

3. **OpenCode HTTP API 不可用**
   - `/api/sessions` 等端点返回 HTML（Web UI）
   - 不是传统的 REST API

---

## 🎯 推荐方案：MCP 集成

### 方案优势

✅ **官方接口** - 使用 OpenCode 自己的数据源  
✅ **完整数据** - 可以获取会话内容和历史  
✅ **实时同步** - 与 OpenCode 保持一致  
✅ **稳定可靠** - 不依赖内部实现细节  

### MCP ACC 服务器 API

```
会话管理:
- session.create    创建新会话
- session.search    搜索会话
- session.append    添加消息到会话
- session.handoff   移交会话
- session.pin       固定会话
- session.archive   归档会话
```

---

## 🚀 实施计划

### 阶段 1: MCP 客户端实现（2-3天）

**任务:**
1. 在 Pocket Backend 实现 MCP 客户端
2. 连接到 `https://acc.kxpms.cn:9002/mcp`
3. 实现 `session.search` 查询会话
4. 实现 `session.append` 添加任务上下文

**技术栈:**
- Go MCP 客户端库
- WebSocket 或 HTTP 连接
- JSON-RPC 协议

### 阶段 2: 会话-任务映射（1天）

**任务:**
1. 在 Pocket 数据库创建映射表
2. 关联 MCP 会话 ID 和 Pocket 任务 ID
3. 实现双向查询

**数据库结构:**
```sql
CREATE TABLE session_task_mapping (
    session_id TEXT,
    task_id TEXT,
    created_at INTEGER,
    PRIMARY KEY (session_id, task_id)
);
```

### 阶段 3: UI 集成（1天）

**任务:**
1. 在任务详情页显示关联会话
2. 点击会话打开 OpenCode
3. 从 OpenCode 创建任务

---

## 📊 三种集成方案对比

| 方案 | 可行性 | 完整性 | 开发时间 | 维护成本 |
|------|--------|--------|----------|----------|
| **MCP 集成** | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | 2-3天 | 低 |
| 文件系统读取 | ⭐⭐⭐ | ⭐⭐ | 1天 | 中 |
| CLI 包装 | ⭐⭐⭐⭐ | ⭐⭐⭐ | 1-2天 | 中 |

---

## 🎯 近期行动（基于当前进展）

### 当前状态
- ✅ 方案 A 已完成：Pocket 独立管理 10 个测试任务
- ✅ 手机 App 可以完整测试所有功能
- 🔬 方案 C 研究已完成：找到 MCP 集成路径

### 建议时间线

**本周（验证阶段）:**
- 完成方案 A 的手机验证（今天）
- 修复发现的 Bug
- 优化用户体验

**下周（集成阶段）:**
- 实现 MCP 客户端
- 连接 ACC 服务器
- 实现会话查询功能

**第三周（完善阶段）:**
- 会话-任务关联
- UI 优化
- 测试和调试

---

## 🔍 技术细节

### MCP 协议示例

**查询会话:**
```json
{
  "jsonrpc": "2.0",
  "method": "session.search",
  "params": {
    "query": "",
    "limit": 10,
    "offset": 0
  },
  "id": 1
}
```

**响应:**
```json
{
  "jsonrpc": "2.0",
  "result": {
    "sessions": [
      {
        "id": "sess_xxx",
        "title": "实现用户认证",
        "created_at": 1719648000,
        "messages": [...]
      }
    ]
  },
  "id": 1
}
```

### OpenCode 配置文件位置

```bash
# 主配置
~/.config/opencode/opencode.json

# 应用数据
~/Library/Application Support/ai.opencode.desktop/
  - opencode.global.dat
  - opencode.workspace.*.dat

# CLI 工具
~/.opencode/bin/opencode
```

---

## 💡 关键洞察

1. **OpenCode 不是传统的客户端-服务器架构**
   - 它是基于 MCP 协议的分布式系统
   - 会话数据存储在云端 (ACC 服务器)
   - 本地只缓存元数据

2. **Pocket 应该成为 MCP 客户端**
   - 使用与 OpenCode 相同的数据源
   - 提供移动端优化的界面
   - 支持任务管理功能

3. **集成的价值**
   - 统一的会话视图
   - 任务驱动的工作流
   - 跨设备同步

---

## 🎊 结论

**方案 C（MCP 集成）是可行的，并且是最佳长期方案。**

**建议行动:**
1. **本周:** 完成方案 A 验证，确保基础功能完善
2. **下周:** 开始 MCP 客户端实现
3. **两周后:** 完成深度集成，实现统一的任务-会话管理

---

**文档创建时间:** 2026-06-29 16:55  
**研究人员:** AI Agent (Explore)  
**审核人员:** Kiro
