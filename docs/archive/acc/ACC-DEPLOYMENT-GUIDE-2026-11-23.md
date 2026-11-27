# ACC Multi-Agent Task Management - 部署与验证指南

**日期**: 2026-11-23  
**当前状态**: 95%完成，代码实现100%，等待211网络恢复

## 系统概览

ACC（Agent Companion Cluster）多智能体任务管理系统已完成全部代码实现，支持以下核心功能：

1. ✅ **感知智能体**：通过ACC注册表发现所有agent-companion实例
2. ✅ **查看任务详情**：三层会话分类、transcript加载、标题提取、摘要生成
3. ✅ **控制任务**：取消、暂停、恢复任务
4. ✅ **任务移效**：跨runtime转派任务给不同agent
5. ✅ **总结功能**：自动提取标题和生成摘要
6. ✅ **分配任务**：创建新任务并派发到指定agent

## 功能实现清单

### 1. 感知智能体（Agent Discovery）

**后端实现**：
- 文件：`backend/internal/server/acc_ecosystem.go`
- 端点：`GET /api/acc/agents`
- 特性：
  - 8秒超时保护（避免30s网关超时）
  - 降级到注册表（ListAgents）
  - 60秒进程内缓存
  - 单飞行模式（single-flight）

**前端实现**：
- 文件：`frontend/src/features/tasks/TasksView.vue`
- 组件：ACC生态卡（展示agents列表）

**测试覆盖**：
- 5个测试用例（acc_ecosystem_test.go）
- 211生产验证：30s超时 → 8.1s（缓存：0.08s）

---

### 2. 查看任务详情（Task Detail）

**后端实现**：
- 文件：`backend/internal/server/session_bundle.go`
- 端点：`GET /api/tasks/{id}/session-bundle`
- 特性：
  - 三层会话分类：
    - `current`：可被companion打开的活跃会话
    - `historical`：已结束的网关会话
    - `localOnly`：仅本地链接的会话
  - 多companion解析（POCKET_COMPANION_URLS按runtime路由）
  - dispatch会话支持（ACC任务ID前缀处理）
  - companion探测（最多20行，避免大任务变慢）

**前端实现**：
- 主视图：`frontend/src/features/tasks/TaskDetailView.vue`
- 会话面板：`frontend/src/features/tasks/TaskSessionPanel.vue`
- 会话详情：`frontend/src/features/tasks/TaskSessionSheet.vue`
- Transcript：`frontend/src/features/tasks/TaskSessionTranscript.vue`

**功能细节**：
- 加载会话transcript（完整对话历史）
- 四路过滤：user / assistant / tool / thinking
- Token统计（input/output/cache）
- 标题提取和摘要生成

**测试覆盖**：
- 12个测试用例（session_bundle_test.go）
- 211生产验证：dispatch会话70b86237成功显示在current段

---

### 3. 控制任务（Task Control）

**后端实现**：
- 取消：`backend/internal/server/task_cancel.go`
- 状态更新：`backend/internal/server/tasks.go` (handleUpdateTask)

**前端实现**：
- 文件：`frontend/src/features/tasks/TaskDetailView.vue`
- 按钮：
  - ▶ 恢复（status !== 'active'时显示）
  - ⏸ 暂停（status === 'active'时显示）
  - ✅ 完成（status !== 'completed'时显示）

**特性**：
- Fail-closed错误处理
- 终态任务跳过取消（没有副作用需要停止）
- 活跃任务需要holder才能取消

---

### 4. 任务移效（Task Transfer）

**后端实现**：
- 文件：`backend/internal/server/task_transfer.go` (242行)
- 端点：`POST /api/tasks/{id}/transfer`
- 请求体：
  ```json
  {
    "runtime_id": "acs-local",
    "agent_id": "agent-companion",
    "reason": "转移到本地执行"
  }
  ```

**组合事务流程**：
```
① 解析旧dispatch
   - 优先从绑定表读取
   - 兜底从run events解析
   
② 取消旧dispatch
   - 终态跳过（无副作用）
   - 活跃任务需要holder
   - 取消失败 → 502 fail-closed（不重派）
   
③ 重新派发
   - 复用同一ACC任务ID
   - correlation_id: "transfer-from-{taskID}"
   - IdempotencyKey绑定(task, 旧dispatch)对
   
④ 换绑
   - SetACCBinding更新run/dispatch
   - acc_task_id保持不变
   
⑤ 记录activity
   - 事件类型：transferred
   - 包含旧/新runtime信息
```

**前端实现**：
- 文件：`frontend/src/features/tasks/TasksView.vue`
- 触发方式：长按任务卡片 → "🔀 移交到其他智能体"
- BottomSheet收集目标runtime/agent

**与其他操作的区别**：
- vs Delegate：delegation建新任务，transfer复用任务
- vs Cancel：本地任务保持active，只换执行者

**测试覆盖**：
- 4个测试用例（task_transfer_test.go，317行）
- 211生产E2E验证：runtime-211-docker → acs-local成功

---

### 5. 总结功能（Summarization）

**后端实现**：
- 集成在session-bundle端点
- 支持标题提取和摘要生成
- 多companion路由（按runtime查找正确的companion）

**前端实现**：
- 文件：`frontend/src/features/tasks/TaskSessionSheet.vue`
- 显示：会话标题、摘要、完整transcript

**测试覆盖**：
- 211生产验证：标题提取和摘要生成正常工作

---

### 6. 分配任务（Task Delegation）

**后端实现**：
- 端点：`POST /api/tasks`
- ACC绑定：通过dispatch到指定runtime/agent

**前端实现**：
- 文件：`frontend/src/features/tasks/TaskCollaborationPanel.vue`
- 功能：委派面板，选择目标agent并创建新任务

---

## 部署前置检查

### 验证211网络恢复

```bash
# 1. 网络层连通性
ping -c 3 10.22.133.211
# 期望：0% packet loss

# 2. ACC服务可用性
curl -m 5 http://10.22.133.211:4101/health
# 期望：HTTP 200 {"status":"ok"}

# 3. SSH访问
ssh root@10.22.133.211 "echo OK"
# 期望：输出 OK
```

如果以上三项全部通过，可以继续部署。

---

## 部署步骤

### Step 1: 验证本地Companion状态（5分钟）

```bash
# 检查agent-companion进程
cd ~/workspace/ai-native-tools/ai-platform/agent-companion
./deploy-local.sh --status

# 期望输出：
# ✓ agent-companion 运行中 (PID: 4737)
# ✓ 健康检查通过
# ✓ 检测到 260 个agents
# ✓ 缓存 800 条会话
# ✓ acs-local 在 ACC 智能体列表
```

**如果"acs-local 在 ACC 智能体列表"显示为 ⚠**：

```bash
# 重新部署companion注册
./deploy-local.sh

# 等待10秒后再次检查
sleep 10
./deploy-local.sh --status
```

---

### Step 2: 配置211 POCKET_COMPANION_URL（10分钟）

```bash
# 1. SSH到211服务器
ssh root@10.22.133.211

# 2. 检查现有配置
docker exec pocketd-1 env | grep COMPANION

# 期望输出（如果已配置）：
# POCKET_COMPANION_URLS=runtime-211-docker:http://10.22.133.1:12110
# POCKET_COMPANION_SECRETS=runtime-211-docker:<secret>
```

**如果配置缺失**：

```bash
# 3. 编辑docker-compose配置
cd /opt/openpocket-211  # 或实际部署目录
nano docker-compose.platform.yml

# 在pocketd服务的environment部分添加：
services:
  pocketd:
    environment:
      POCKET_COMPANION_URLS: "runtime-211-docker:http://10.22.133.1:12110"
      POCKET_COMPANION_SECRETS: "runtime-211-docker:${AC_API_SECRET}"
      # 其他现有环境变量...

# 4. 重启pocketd服务
docker-compose -f docker-compose.platform.yml restart pocketd

# 5. 验证配置生效
docker logs pocketd-1 | grep "Companion runtime map"
# 期望：Companion runtime map: 1 entr(y/ies)

# 6. 再次验证环境变量
docker exec pocketd-1 env | grep COMPANION
# 应该显示两个变量
```

---

### Step 3: 验证Session-Bundle数据（5分钟）

```bash
# 1. 获取认证token
TOKEN=$(curl -s -X POST https://openpocket.itestu.cn/api/auth/sso/direct-login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"<DEV-ADMIN-PASS>…<masked>"}' | jq -r .token)

# 2. 创建测试任务（如果需要）
TASK_ID=$(curl -s -X POST https://openpocket.itestu.cn/api/tasks \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "ACC部署验证测试",
    "description": "验证session-bundle是否包含dispatch会话",
    "priority": "medium"
  }' | jq -r .id)

echo "测试任务ID: $TASK_ID"

# 3. 派发任务到ACC
curl -s -X POST https://openpocket.itestu.cn/api/tasks/$TASK_ID/dispatch \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "runtime_id": "runtime-211-docker",
    "agent_id": "agent-companion",
    "prompt": "列出当前目录的文件"
  }' | jq .

# 4. 等待任务执行（30秒）
sleep 30

# 5. 验证session-bundle包含dispatch会话
curl -s -H "Authorization: Bearer $TOKEN" \
  "https://openpocket.itestu.cn/api/tasks/$TASK_ID/session-bundle" \
  | jq '{current: (.current | length), historical: (.historical | length)}'

# 期望输出：
# {
#   "current": 1,     // ← 应该 > 0（dispatch会话在current段）
#   "historical": 0
# }
```

---

### Step 4: 验证Agents端点（5分钟）

```bash
# 使用上一步的TOKEN
curl -s -H "Authorization: Bearer $TOKEN" \
  "https://openpocket.itestu.cn/api/acc/agents" \
  | jq '[.[] | {id, name, status}]'

# 期望输出：包含runtime-211-docker和acs-local等agents
# [
#   {
#     "id": "runtime-211-docker",
#     "name": "211 Docker Runtime",
#     "status": "active"
#   },
#   {
#     "id": "acs-local",
#     "name": "Local Agent Companion",
#     "status": "active"
#   }
# ]

# 性能验证（应该在8秒内返回，缓存命中<0.1秒）
time curl -s -H "Authorization: Bearer $TOKEN" \
  "https://openpocket.itestu.cn/api/acc/agents" > /dev/null
```

---

### Step 5: 验证Transfer功能（10分钟）

```bash
# 1. 创建测试任务并派发到runtime-211-docker
TASK_ID=$(curl -s -X POST https://openpocket.itestu.cn/api/tasks \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Transfer功能验证",
    "description": "测试跨runtime转派",
    "priority": "high"
  }' | jq -r .id)

curl -s -X POST https://openpocket.itestu.cn/api/tasks/$TASK_ID/dispatch \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "runtime_id": "runtime-211-docker",
    "agent_id": "agent-companion",
    "prompt": "echo Phase1: runtime-211-docker && sleep 5"
  }' | jq .

# 2. 等待任务开始执行
sleep 10

# 3. 转派到acs-local
curl -s -X POST https://openpocket.itestu.cn/api/tasks/$TASK_ID/transfer \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "runtime_id": "acs-local",
    "agent_id": "agent-companion",
    "reason": "转移到本地Mac执行"
  }' | jq .

# 期望输出：
# {
#   "status": "transferred",
#   "new_dispatch_id": "dispatch_<hash>",
#   "message": "Task transferred successfully"
# }

# 4. 验证绑定更新
curl -s -H "Authorization: Bearer $TOKEN" \
  "https://openpocket.itestu.cn/api/tasks/$TASK_ID" \
  | jq '{accTaskId, accDispatchId, accRuntimeId}'

# 期望：
# - accTaskId 不变
# - accDispatchId 更新为新dispatch
# - accRuntimeId 更新为 acs-local

# 5. 验证activity日志
curl -s -H "Authorization: Bearer $TOKEN" \
  "https://openpocket.itestu.cn/api/tasks/$TASK_ID/activities" \
  | jq '.[] | select(.event_type == "transferred")'

# 期望：显示transferred事件，包含old/new runtime信息
```

---

## Phase D: 端到端真机验证（2-4小时）

### 测试设备准备

```bash
# 设备：vivo V2436A
# 序列号：c8bdee87

# 1. 连接设备
adb devices
# 应该显示：c8bdee87	device

# 2. 安装最新APK
cd frontend
npm run build
# APK路径：android/app/build/outputs/apk/release/app-release.apk

adb install -r android/app/build/outputs/apk/release/app-release.apk

# 3. 启动应用
adb shell am start -n cn.itestu.openpocket/.MainActivity
```

### E2E测试清单

#### 测试1: 任务看板 → ACC生态卡显示agents

**步骤**：
1. 打开OpenPocket应用
2. 进入「任务」页面
3. 滚动到「ACC生态」卡片

**验证点**：
- [ ] 卡片显示「ACC 智能体 (N个)」
- [ ] 列表显示至少2个agents（runtime-211-docker, acs-local）
- [ ] 每个agent显示ID、名称、状态

**adb命令验证**：
```bash
# 截图
adb exec-out screencap -p > e2e-test1-acc-ecosystem.png

# UI自动化验证
adb shell uiautomator dump
adb shell cat /sdcard/window_dump.xml | grep "ACC 智能体"
```

---

#### 测试2: 任务列表 → dispatch任务可见

**步骤**：
1. 在任务列表中找到之前创建的测试任务
2. 任务卡片应该显示ACC图标或标记

**验证点**：
- [ ] 任务标题正确显示
- [ ] 任务状态显示（active/completed）
- [ ] 显示ACC绑定标识

---

#### 测试3: 会话详情 → transcript加载

**步骤**：
1. 点击任务进入详情页
2. 滚动到「会话」面板
3. 点击current段的dispatch会话

**验证点**：
- [ ] 会话列表显示三个分类：当前/历史/本地
- [ ] dispatch会话出现在「当前」段
- [ ] 点击会话弹出详情sheet
- [ ] transcript完整加载显示

**adb命令验证**：
```bash
adb shell uiautomator dump
adb shell cat /sdcard/window_dump.xml | grep "当前"
```

---

#### 测试4: 四路过滤 → user/assistant/tool/thinking

**步骤**：
1. 在会话详情sheet中
2. 点击过滤按钮（如果有）
3. 切换不同消息类型

**验证点**：
- [ ] user消息正确显示
- [ ] assistant消息正确显示
- [ ] tool调用正确显示
- [ ] thinking内容（如果有）正确显示

---

#### 测试5: 总结功能 → 标题提取 + 摘要生成

**步骤**：
1. 在会话详情中查看摘要区域

**验证点**：
- [ ] 会话标题自动提取
- [ ] 摘要文本生成并显示
- [ ] 摘要内容与transcript一致

---

#### 测试6: 任务委派 → 新任务派发

**步骤**：
1. 在任务列表点击「+」或委派按钮
2. 选择「委派到ACC」
3. 填写任务信息
4. 选择目标agent
5. 提交

**验证点**：
- [ ] 弹窗显示可用agents列表
- [ ] 可以选择runtime和agent
- [ ] 提交成功并创建新任务
- [ ] 新任务出现在列表中

---

#### 测试7: 任务取消 → 停止活跃任务

**步骤**：
1. 创建一个长时间运行的任务
2. 在任务详情页点击「取消」按钮
3. 确认取消

**验证点**：
- [ ] 取消按钮可点击
- [ ] 弹出确认对话框
- [ ] 确认后任务状态变为cancelled
- [ ] 后端dispatch停止执行

---

#### 测试8: 任务移交 → 转移到不同agent

**步骤**：
1. 长按任务卡片
2. 选择「🔀 移交到其他智能体」
3. 选择目标runtime和agent
4. 填写原因（可选）
5. 提交

**验证点**：
- [ ] 长按菜单显示移交选项
- [ ] BottomSheet显示可用agents
- [ ] 提交后显示成功提示
- [ ] 任务详情显示新的runtime
- [ ] activity日志记录transferred事件

**adb命令验证**：
```bash
# 模拟长按
adb shell input swipe 500 800 500 800 1000

# 验证菜单出现
adb shell uiautomator dump
adb shell cat /sdcard/window_dump.xml | grep "移交"
```

---

#### 测试9: 审批流程 → 权限请求处理

**步骤**：
1. 创建一个需要权限的任务（如文件操作）
2. 等待审批卡片出现在「ACC待批」区域
3. 点击「✓ 允许一次」或「✕ 拒绝」

**验证点**：
- [ ] 审批卡片正确显示
- [ ] 显示权限请求摘要
- [ ] 批准按钮可点击
- [ ] 点击后卡片消失
- [ ] 任务继续执行（如果批准）
- [ ] 任务失败（如果拒绝）

---

## 测试结果记录模板

```markdown
# ACC Multi-Agent E2E Test Report

**日期**: 2026-11-23
**测试人员**: [姓名]
**设备**: vivo V2436A (c8bdee87)
**APK版本**: [commit hash]
**后端版本**: [commit hash]

## 测试结果汇总

| 测试项 | 状态 | 备注 |
|--------|------|------|
| 1. ACC生态卡显示agents | ⏳ | |
| 2. dispatch任务可见 | ⏳ | |
| 3. Transcript加载 | ⏳ | |
| 4. 四路过滤 | ⏳ | |
| 5. 总结功能 | ⏳ | |
| 6. 任务委派 | ⏳ | |
| 7. 任务取消 | ⏳ | |
| 8. 任务移交 | ⏳ | |
| 9. 审批流程 | ⏳ | |

**总体通过率**: 0/9 (0%)

## 详细测试记录

### 测试1: ACC生态卡

**状态**: ⏳ 未开始
**截图**: [路径]
**日志**: [相关日志]
**问题**: [如果有]

[重复每个测试项...]

## 发现的问题

1. [问题描述]
   - 严重程度: P0/P1/P2
   - 复现步骤: [...]
   - 期望行为: [...]
   - 实际行为: [...]

## 总结

[测试总结和建议]
```

---

## 性能基准

基于211生产验证的性能数据：

| 端点 | 首次调用 | 缓存命中 | 超时阈值 |
|------|----------|----------|----------|
| /api/acc/agents | ~8.1s | ~0.08s | 8s |
| /api/tasks/{id}/session-bundle | ~1-2s | N/A | 30s |
| /api/tasks/{id}/transfer | ~2-3s | N/A | 30s |

---

## 故障排查

### 问题1: agents端点返回空列表

**症状**: GET /api/acc/agents 返回 `[]`

**排查步骤**：
```bash
# 1. 检查ACC服务
curl http://10.22.133.211:4101/health

# 2. 检查companion注册
curl http://10.22.133.211:4101/api/v2/agents

# 3. 检查本地companion
cd ~/workspace/ai-native-tools/ai-platform/agent-companion
./deploy-local.sh --status

# 4. 查看pocketd日志
ssh root@10.22.133.211
docker logs pocketd-1 | grep -i "acc\|agent"
```

**解决方案**：
- 如果ACC返回空：重启ACC服务
- 如果companion未注册：执行Step 1重新部署
- 如果超时：检查网络和ACC响应时间

---

### 问题2: dispatch会话不显示在current段

**症状**: session-bundle的current数组为空

**排查步骤**：
```bash
# 1. 检查POCKET_COMPANION_URLS配置
ssh root@10.22.133.211
docker exec pocketd-1 env | grep COMPANION

# 2. 检查companion可访问性
curl http://10.22.133.1:12110/health

# 3. 查看pocketd日志
docker logs pocketd-1 | grep "Companion runtime map"

# 4. 测试companion端点
TOKEN=[companion-token]
curl -H "Authorization: Bearer $TOKEN" \
  http://10.22.133.1:12110/api/sessions/[agent-kind]/[session-id]
```

**解决方案**：
- 如果COMPANION_URLS缺失：执行Step 2配置
- 如果companion不可访问：检查防火墙和端口
- 如果companion返回404：检查session_path是否正确

---

### 问题3: transfer失败返回502

**症状**: POST /api/tasks/{id}/transfer 返回502错误

**排查步骤**：
```bash
# 1. 检查任务状态
curl -H "Authorization: Bearer $TOKEN" \
  https://openpocket.itestu.cn/api/tasks/$TASK_ID \
  | jq '{status, accTaskId, accDispatchId}'

# 2. 查看后端日志
ssh root@10.22.133.211
docker logs pocketd-1 | tail -100 | grep transfer

# 3. 检查ACC命令状态
# [查看MCP调用日志]
```

**可能原因**：
- 旧dispatch取消失败（fail-closed保护）
- 缺少holder ID
- 目标runtime不可达

**解决方案**：
- 如果取消失败：等待任务完成后再transfer
- 如果缺holder：检查ACC命令状态
- 如果runtime不可达：验证目标agent在agents列表中

---

## 完成标准

部署和验证被视为完成需要满足：

### 代码层面
- [x] 所有代码实现完成（95%已完成）
- [x] 所有单元测试通过（21+测试）
- [x] 代码审查通过

### 部署层面
- [ ] 211网络连通（当前阻塞）
- [ ] agent-companion注册到ACC（Step 1）
- [ ] POCKET_COMPANION_URL配置正确（Step 2）
- [ ] session-bundle包含dispatch会话（Step 3）
- [ ] agents端点返回正确数据（Step 4）
- [ ] transfer功能E2E验证（Step 5）

### 用户体验层面
- [ ] 9项E2E测试全部通过（Phase D）
- [ ] 性能符合基准（agents <8s，缓存<0.1s）
- [ ] 无重大bug（P0/P1问题=0）

---

## 预计完成时间

基于211网络恢复：

- **Step 1-2**: 配置部署（15-20分钟）
- **Step 3-5**: 功能验证（20-30分钟）
- **Phase D**: E2E测试（2-4小时）

**总计**: 3-5小时

---

## 联系信息

如有问题，请联系：
- 技术负责人：[联系方式]
- 项目文档：`/Users/xutaohuang/workspace/ai-native-tools/client/openpocket/`

---

## 附录：架构图

```
┌─────────────────────────────────────────────────────────────┐
│                     OpenPocket Frontend                       │
│  (TasksView.vue, TaskDetailView.vue, TaskSessionPanel.vue)  │
└───────────────────────┬─────────────────────────────────────┘
                        │ HTTPS
                        ↓
┌─────────────────────────────────────────────────────────────┐
│                     pocketd (Backend)                         │
│  • GET /api/acc/agents          (acc_ecosystem.go)          │
│  • GET /api/tasks/{id}/session-bundle (session_bundle.go)   │
│  • POST /api/tasks/{id}/transfer      (task_transfer.go)    │
└─────┬────────────────────────────────┬──────────────────────┘
      │                                │
      │ ACC MCP Client                 │ Multi-Companion URLs
      ↓                                ↓
┌──────────────────┐          ┌────────────────────────────┐
│   ACC (4101)     │          │  agent-companion instances │
│  • ListAgents    │          │  • runtime-211-docker      │
│  • ListRuntimes  │          │  • acs-local (Mac)         │
│  • acc_list_     │          │  • [其他runtimes]          │
│    sessions      │          └────────────────────────────┘
└──────────────────┘

Legend:
→ : HTTP/MCP calls
─ : Configuration dependency
```

---

**文档版本**: 1.0  
**最后更新**: 2026-11-23  
**状态**: 等待211网络恢复
