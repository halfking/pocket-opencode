# 方案：从 Mock 数据切换到 ACC MCP 真实任务

## 现状
- 📛 当前任务从 SQLite mock 数据读取
- ✅ ACC MCP 已确认可用
- ✅ API Key 已配置

## 发现的 MCP 端点
正确的 MCP 端点是 `https://acc.kxpms.cn/mcp`（不是之前配置的 `https://mcp.kxpms.cn/acc/mcp`）

## 需要修改的文件

### 1. 修复 MCP 端点配置
- `/etc/systemd/system/opencode-pocket.service` → `POCKET_MCP_URL=https://acc.kxpms.cn/mcp`

### 2. `internal/mcp/client.go` - 添加工具调用方法
- 添加 `CallTool(name, args)` 方法（完整的 MCP 三次握手）
- 添加 `ListRemoteTasks(status, limit)` 方法

### 3. `internal/adapter/adapter.go` - 扩展接口
- 在 `OpenCodeAdapter` 接口中添加 `ListRemoteTasks(ctx, status, limit)` 方法

### 4. `internal/adapter/mcp_adapter.go` - 实现远程任务获取
- 实现 `ListRemoteTasks` 调用 `acc_get_tasks`

### 5. `internal/server/server.go` - 切换任务数据源
- `handleTasks` 改用 `s.opencode.ListRemoteTasks()` 替代 `s.taskStore.ListTasks()`

### 6. 前端 `client.ts` - 增加状态字段映射
- ACC 任务状态: `todo`, `in_progress`, `done`, `blocked`, `assigned`
- 前端显示: 进行中/已阻塞/已完成
