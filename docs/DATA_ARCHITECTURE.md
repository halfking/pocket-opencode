## 数据来源架构

### 双层任务系统

```
Pocket App
  │
  ├── ACC 任务 (系统级)
  │   └── acc.kxpms.cn/mcp
  │       └── acc_get_tasks → PostgreSQL
  │
  └── OpenCode 任务 (工作区级)
      ├── opencode-kaixuan1 → 实例自己的 MCP endpoint
      ├── opencode-kaixuan2 → 实例自己的 MCP endpoint  
      ├── opencode-kaixuan3 → 实例自己的 MCP endpoint
      └── opencode-local-test → localhost 实例
```

### 设计

- `Task` 结构增加 `source` 字段：`"acc"` | `"opencode"` | `"local"`
- ACC 任务通过 `acc_get_tasks` 获取（已实现）
- OpenCode 任务通过每个实例的 MCP endpoint 获取（实例配置中的 apiBaseURL）
- 前端按 `source` 和 `workstreamId` 过滤
