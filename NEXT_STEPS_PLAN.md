# 🎯 OpenCode Pocket 下一步行动方案

**当前状态:** OpenCode API 已启动，但是 Web UI 而非 REST API  
**核心问题:** 需要决定如何获取和显示任务数据

---

## 📊 当前发现

### OpenCode API 服务器
- ✅ 已成功启动在 http://localhost:4096
- ✅ 提供 Web UI 界面
- ❌ 没有传统的 REST API 端点（/api/tasks 等）
- 💡 主要用于浏览器访问，不是后端集成

### Pocket Backend
- ✅ 运行正常
- ✅ 有自己的 SQLite 数据库
- ❌ 数据库是空的
- ❌ 没有连接到 OpenCode

---

## 🎯 三个方案选择

### 方案 A: Pocket 独立管理任务（推荐）⭐

**描述:**
- Pocket 有自己的任务管理系统
- 不依赖 OpenCode
- 功能完整且独立

**优点:**
- ✅ 立即可用
- ✅ 功能完整
- ✅ 移动端优化
- ✅ 离线可用

**实施步骤:**
1. 在 Pocket Backend 创建测试任务
2. 实现完整的 CRUD API
3. 手机上验证所有功能
4. 时间：30分钟

**适合:** 快速验证 Pocket 功能，独立使用

---

### 方案 B: Pocket 作为 OpenCode 快捷方式

**描述:**
- Pocket 显示实例列表
- 点击实例打开 OpenCode Web UI
- 不显示具体任务

**优点:**
- ✅ 简单实现
- ✅ 利用 OpenCode 现有功能
- ✅ 无需数据同步

**实施步骤:**
1. 更新实例卡片添加"打开 OpenCode"按钮
2. 点击后打开浏览器到 http://localhost:4096
3. 时间：15分钟

**适合:** Pocket 作为启动器，实际工作在 OpenCode

---

### 方案 C: 深度集成 OpenCode

**描述:**
- 研究 OpenCode 内部数据结构
- 读取 ZCode session 数据
- 实现双向同步

**优点:**
- ✅ 完全集成
- ✅ 数据一致性

**缺点:**
- ❌ 需要深入研究 OpenCode 源码
- ❌ 可能需要几天时间
- ❌ OpenCode 更新后可能失效

**实施步骤:**
1. 研究 ZCode session 存储格式
2. 实现数据读取和解析
3. 实现同步机制
4. 时间：2-3天

**适合:** 长期项目，需要完美集成

---

## 💡 我的建议

**立即采用方案 A（独立管理）**

原因：
1. **验证功能** - 快速验证 Pocket 的 UI 和交互
2. **完整体验** - 用户可以完整测试所有功能
3. **独立价值** - Pocket 本身就是有价值的工具
4. **可扩展** - 将来可以添加与 OpenCode 的集成

**实施计划:**
```
1. 创建真实的测试任务（10分钟）
2. 实现创建/编辑/删除任务（已有）
3. 手机上完整验证（20分钟）
4. 修复发现的 Bug
```

---

## 🚀 方案 A 详细实施

### Step 1: 创建丰富的测试数据

在 Backend 数据库中创建：
- 10个不同状态的任务
- 不同优先级
- 不同实例归属
- 模拟真实使用场景

### Step 2: 验证功能

- [x] 登录
- [x] 服务器选择
- [x] 实例列表
- [ ] 任务列表（按状态分组）
- [ ] 任务详情
- [ ] 创建任务
- [ ] 编辑任务
- [ ] 删除任务
- [ ] 附加会话
- [ ] 实时更新

### Step 3: 优化体验

- 添加下拉刷新
- 添加加载动画
- 优化错误提示
- 添加空状态提示

---

## 📱 方案 A - 立即执行

### 命令序列:

```bash
# 1. 连接到 Backend 服务器
ssh root@14.103.112.184

# 2. 进入 Backend 目录
cd /data/services/opencode-pocket/backend

# 3. 安装 sqlite3
apt-get update && apt-get install -y sqlite3

# 4. 创建测试任务
sqlite3 ./data/pocket.sqlite << 'SQL'
INSERT INTO tasks (id, title, description, status, priority, workstream_id, created_at, updated_at) VALUES
('task-001', '实现用户认证系统', '为移动端应用实现完整的用户登录和认证功能，支持 Token 管理', 'active', 'high', 'opencode-local-test', datetime('now'), datetime('now')),
('task-002', '优化数据库查询性能', '分析慢查询，添加必要的索引，优化 SQL 语句', 'active', 'medium', 'opencode-local-test', datetime('now'), datetime('now')),
('task-003', '修复任务列表刷新 Bug', '解决返回后任务列表不刷新的问题', 'completed', 'high', 'opencode-local-test', datetime('now'), datetime('now')),
('task-004', '编写 API 文档', '使用 Swagger 编写完整的 REST API 文档', 'active', 'low', 'opencode-kaixuan1', datetime('now'), datetime('now')),
('task-005', '实现 WebSocket 实时更新', '添加任务状态变更的实时推送功能', 'blocked', 'high', 'opencode-kaixuan1', datetime('now'), datetime('now')),
('task-006', '设计数据库迁移方案', '制定从旧版本到新版本的数据迁移策略', 'active', 'medium', 'opencode-kaixuan2', datetime('now'), datetime('now')),
('task-007', '添加单元测试', '为核心模块编写单元测试，覆盖率达到 80%', 'active', 'medium', 'opencode-kaixuan2', datetime('now'), datetime('now')),
('task-008', '优化移动端 UI', '根据用户反馈优化界面布局和交互', 'completed', 'low', 'opencode-kaixuan3', datetime('now'), datetime('now')),
('task-009', '实现离线模式', '支持无网络情况下的基本功能', 'blocked', 'medium', 'opencode-kaixuan3', datetime('now'), datetime('now')),
('task-010', '配置 CI/CD 流程', '使用 GitHub Actions 自动化构建和部署', 'active', 'high', 'opencode-kaixuan3', datetime('now'), datetime('now'));
SQL

# 5. 验证数据
sqlite3 ./data/pocket.sqlite "SELECT id, title, status FROM tasks;"

# 6. 重启服务
systemctl restart opencode-pocket
```

---

## ✅ 预期结果

完成后，在 Pocket App 中应该能看到：

```
📱 OpenCode 本地测试 (XUTAOdeMacBook-Pro)
   进行中 (2个):
   - 实现用户认证系统 [高]
   - 优化数据库查询性能 [中]
   
   已完成 (1个):
   - 修复任务列表刷新 Bug [高]

💻 OpenCode @ kaixuan-1
   进行中 (1个):
   - 编写 API 文档 [低]
   
   已阻塞 (1个):
   - 实现 WebSocket 实时更新 [高]

... 等等
```

---

## 🎯 下一步

**请确认：是否立即执行方案 A？**

如果是，我会：
1. 连接到 Backend 服务器
2. 安装 sqlite3
3. 创建 10 个真实的测试任务
4. 重启服务
5. 在手机上验证

预计时间：15 分钟

**请回复 "执行方案 A" 开始！** 🚀
