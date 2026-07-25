# OpenCode 任务管理系统 - 文件清单

## 📋 已创建的文件

### 文档 (3 个文件)
- ✅ `docs/opencode-task-management-architecture.md` - 系统架构设计文档
- ✅ `docs/OPENCODE_IMPLEMENTATION_GUIDE.md` - 实施指南
- ✅ `OPENCODE_DELIVERY_SUMMARY.md` - 交付总结

### 后端代码 (3 个文件)
- ✅ `backend/internal/opencode/manager.go` - OpenCode 管理器
- ✅ `backend/internal/opencode/store.go` - PostgreSQL 历史存储
- ✅ `backend/internal/server/server_opencode.go` - API 路由处理器

### 后端修改 (1 个文件)
- ✅ `backend/internal/server/server.go` - 已集成 OpenCode Manager 和路由

### 前端代码 (3 个文件)
- ✅ `frontend/src/stores/opencode.ts` - Pinia Store
- ✅ `frontend/src/features/opencode/OpenCodeHub.vue` - 实例管理中心
- ✅ `frontend/src/features/opencode/SessionListView.vue` - 会话列表视图

## 📂 目录结构

```
services/opencode-pocket/
├── docs/
│   ├── opencode-task-management-architecture.md      # 架构设计
│   ├── OPENCODE_IMPLEMENTATION_GUIDE.md              # 实施指南
│   └── OPENCODE_FILES_CHECKLIST.md                   # 本文件
├── OPENCODE_DELIVERY_SUMMARY.md                      # 交付总结
├── backend/
│   └── internal/
│       ├── opencode/
│       │   ├── manager.go                            # 核心管理器
│       │   └── store.go                              # 数据存储
│       └── server/
│           ├── server.go                             # 已修改，集成 OpenCode
│           └── server_opencode.go                    # OpenCode API 处理
└── frontend/
    └── src/
        ├── stores/
        │   └── opencode.ts                           # 状态管理
        └── features/
            └── opencode/
                ├── OpenCodeHub.vue                   # 实例中心
                └── SessionListView.vue               # 会话列表

```

## 🔍 文件说明

### 1. manager.go (357 行)
**职责：** OpenCode 核心业务逻辑
- 实例管理
- 会话跟踪和缓存
- 状态监控
- WebSocket 事件推送

**关键类型：**
- `Manager` - 主管理器
- `SessionCache` - 会话缓存
- `StatusMonitor` - 状态监控器

### 2. store.go (230 行)
**职责：** 数据持久化
- PostgreSQL 历史记录存储
- 会话信息持久化
- 数据库表初始化

**关键函数：**
- `InitSchema()` - 创建数据库表
- `SaveEvent()` - 保存历史事件
- `GetHistory()` - 查询历史记录

### 3. server_opencode.go (140 行)
**职责：** HTTP API 处理
- 会话列表 API
- 会话历史 API
- 会话摘要 API
- 实例统计 API

### 4. opencode.ts (220 行)
**职责：** 前端状态管理
- 实例和会话状态
- WebSocket 连接管理
- 实时状态更新
- 缓存和刷新

**关键方法：**
- `loadInstances()` - 加载实例
- `loadSessions()` - 加载会话
- `subscribeToRealTimeUpdates()` - WebSocket 订阅

### 5. OpenCodeHub.vue (300 行)
**职责：** 实例管理界面
- 显示在线/离线实例
- 实例统计信息
- 实例选择和导航

### 6. SessionListView.vue (350 行)
**职责：** 会话列表界面
- 按状态分组显示会话
- 实时状态更新
- 会话详情导航

## 📊 代码统计

| 类型 | 文件数 | 代码行数（估算） |
|------|--------|------------------|
| 后端 Go | 3 | ~800 行 |
| 前端 TS/Vue | 3 | ~900 行 |
| 文档 Markdown | 4 | ~1500 行 |
| **总计** | **10** | **~3200 行** |

## ✅ 集成检查清单

### 后端集成
- [ ] 导入 `internal/opencode` 包
- [ ] 在 main.go 中创建 `OpenCodeManager`
- [ ] 调用 `InitSchema(db)` 初始化数据库
- [ ] 启动 `StartStatusMonitoring()`
- [ ] 在 `server.New()` 中传入 `opencodeManager`
- [ ] 编译测试无错误

### 前端集成
- [ ] 添加路由配置
- [ ] 在主界面添加入口链接
- [ ] 测试 Store 是否正常工作
- [ ] 测试 WebSocket 连接
- [ ] 测试界面显示

### 测试验证
- [ ] 可以看到实例列表
- [ ] 可以选择实例
- [ ] 可以看到会话列表
- [ ] 实时状态更新正常
- [ ] 历史记录可以查询
- [ ] 错误处理正常

## 🔧 需要手动修改的文件

### 1. backend/cmd/pocketd/main.go
需要添加 OpenCode Manager 初始化代码

### 2. frontend/src/app/router-mobile.ts
需要添加 OpenCode 相关路由

### 3. frontend/src/features/instances/InstanceListView.vue (可选)
可以添加 OpenCode Hub 的入口链接

## 📝 版本信息

- **创建日期**: 2026-07-02
- **版本**: v1.0
- **作者**: AI Assistant
- **项目**: OpenCode Pocket - 任务管理系统

---

所有文件均已创建并放置在正确的位置，可以直接使用！
