> **STATUS: superseded** (2026-08-27)
> Evidence level at supersede time: `claimed (unverified)`
> Superseded by: [`docs/新架构v1/03-roadmap/里程碑.md`](docs/新架构v1/03-roadmap/里程碑.md)
> Do NOT use this doc for current implementation decisions.
> This doc is a historical plan/analysis from a completed sprint; 规划以 v3 里程碑为准。

# OpenCode Pocket 模拟器完整测试计划

**日期**: 2026-07-06  
**目标**: 在模拟器上完成端到端功能测试，重点解决登录问题  
**范围**: 登录流程、OpenCode 实例获取、任务会话详情

---

## 📋 测试目标

### 核心测试点
1. **登录流程** - 验证用户认证、JWT token 获取和存储
2. **OpenCode 实例获取** - 验证实例列表 API 和数据展示
3. **任务会话详情** - 验证任务详情 API 和数据流转

### 测试优先级
- 🔴 **P0 - 阻塞性**: 登录流程（无法登录则无法测试其他功能）
- 🟡 **P1 - 关键**: OpenCode 实例获取
- 🟢 **P2 - 重要**: 任务会话详情

---

## 🔧 测试环境准备

### 1. Backend 服务
```bash
# 检查 Backend 运行状态
ps aux | grep pocketd

# 如未运行，启动 Backend
cd backend
export JWT_SECRET=test-secret-key-for-phase7-validation
export POCKET_DEV_AUTH=true
export POCKET_HTTP_PORT=8088
export POCKET_DB_PATH=./data/pocket.sqlite
./pocketd

# 验证健康检查
curl http://localhost:8088/healthz
```

**预期结果**: 
- ✅ Backend 进程正常运行
- ✅ 健康检查返回 "ok"
- ✅ 监听端口 8088

### 2. 数据库准备
```bash
# 检查数据库文件
ls -lh backend/data/pocket.sqlite

# 测试数据库连接
sqlite3 backend/data/pocket.sqlite "SELECT COUNT(*) FROM tasks;"
```

**预期结果**:
- ✅ 数据库文件存在
- ✅ 可以查询表数据

### 3. 模拟器配置
```bash
# 设置 adb 路径
export PATH=$PATH:~/Library/Android/sdk/platform-tools

# 检查设备连接
adb devices -l

# 配置端口转发
adb reverse tcp:8088 tcp:8088

# 验证转发
adb reverse --list
```

**预期结果**:
- ✅ 模拟器在线 (emulator-5554 或类似)
- ✅ 端口转发配置成功
- ✅ 模拟器可访问 localhost:8088

### 4. APK 安装
```bash
# 检查 APK 是否存在
ls -lh android/app/build/outputs/apk/debug/app-debug.apk

# 如不存在，重新构建
cd frontend
npm run build
npx cap sync android
cd ../android
./gradlew assembleDebug

# 安装到模拟器
adb -s emulator-5554 uninstall com.kaixuan.opencode.pocket
adb -s emulator-5554 install android/app/build/outputs/apk/debug/app-debug.apk

# 启动应用
adb -s emulator-5554 shell am start -n com.kaixuan.opencode.pocket/.MainActivity
```

**预期结果**:
- ✅ APK 安装成功
- ✅ 应用启动无崩溃
- ✅ 显示登录页面

---

## 🧪 测试用例 1: 登录流程

### 1.1 Backend API 验证

#### Test 1.1.1: 登录 API 测试
```bash
# 测试登录 API
curl -X POST http://localhost:8088/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin"}' \
  -v

# 预期响应
# HTTP 200 OK
# {"token":"eyJhbG...", "user":"admin"}
```

**验证点**:
- ✅ HTTP 状态码 200
- ✅ 返回 token 字段（JWT 格式）
- ✅ 返回 user 字段
- ❌ 如果返回 401/403，检查 POCKET_DEV_AUTH 环境变量

#### Test 1.1.2: 认证 Token 验证
```bash
# 使用获取的 token 访问受保护的 API
TOKEN="<从上面获取的token>"

curl http://localhost:8088/api/tasks \
  -H "Authorization: Bearer $TOKEN" \
  -v

# 预期响应
# HTTP 200 OK
# [{"id":"task-1", "title":"...", ...}]
```

**验证点**:
- ✅ HTTP 状态码 200
- ✅ 返回任务列表
- ❌ 如果返回 401，token 可能无效或已过期

### 1.2 Frontend 登录流程

#### Test 1.2.1: 登录页面渲染
**操作步骤**:
1. 启动应用
2. 观察启动画面过渡到登录页
3. 检查登录表单元素

**验证点**:
- ✅ 显示用户名输入框
- ✅ 显示密码输入框
- ✅ 显示登录按钮
- ✅ 登录按钮初始状态为禁用（未输入时）

**截图**: 
```bash
adb exec-out screencap -p > test-evidence/01-login-page.png
```

#### Test 1.2.2: 手动登录测试
**操作步骤**:
1. 在用户名框输入: `admin`
2. 在密码框输入: `admin`
3. 点击"登录"按钮
4. 观察页面跳转

**验证点**:
- ✅ 输入时登录按钮变为可用
- ✅ 点击后显示加载状态
- ✅ 成功后跳转到主页（/ai 或 /tasks）
- ✅ localStorage 中保存 pocket_token 和 pocket_user
- ❌ 如果显示错误提示，记录错误信息

**调试命令**:
```bash
# 启用 WebView 调试
adb shell settings put global webview_multiprocess 0

# 查看应用日志
adb logcat | grep -i "opencode\|pocket\|webkit"

# 检查网络请求
adb logcat | grep -i "http\|curl"
```

#### Test 1.2.3: 登录状态持久化
**操作步骤**:
1. 成功登录后
2. 关闭应用（不是退出登录）
3. 重新启动应用
4. 观察是否需要重新登录

**验证点**:
- ✅ 重启后直接进入主页（保持登录状态）
- ✅ authStore.isAuthenticated 为 true
- ❌ 如果重新显示登录页，检查 localStorage 持久化

### 1.3 自动化登录脚本

如果手动登录成功，可尝试自动化：

```bash
#!/bin/bash
# test-login-automation.sh

# 1. 启动应用
adb shell am start -n com.kaixuan.opencode.pocket/.MainActivity
sleep 3

# 2. 输入用户名 (使用 ADB 输入)
adb shell input tap 540 800  # 点击用户名框（坐标需调整）
sleep 0.5
adb shell input text "admin"
sleep 0.5

# 3. 输入密码
adb shell input tap 540 950  # 点击密码框
sleep 0.5
adb shell input text "admin"
sleep 0.5

# 4. 点击登录按钮
adb shell input tap 540 1100  # 点击登录按钮
sleep 2

# 5. 截图验证
adb exec-out screencap -p > test-evidence/02-after-login.png

# 6. 检查是否在主页
adb shell "dumpsys activity activities | grep mResumedActivity"
```

**注意**: 坐标值需要根据实际屏幕尺寸调整

---

## 🧪 测试用例 2: OpenCode 实例获取

**前置条件**: 已成功登录

### 2.1 Backend API 验证

#### Test 2.1.1: 实例列表 API
```bash
# 获取 token（从登录响应或 localStorage）
TOKEN="<your-token>"

# 请求实例列表
curl http://localhost:8088/api/instances \
  -H "Authorization: Bearer $TOKEN" \
  -v

# 预期响应示例
# [
#   {
#     "id": "instance-1",
#     "name": "OpenCode Instance 1",
#     "url": "https://opencode.example.com",
#     "status": "active"
#   }
# ]
```

**验证点**:
- ✅ HTTP 状态码 200
- ✅ 返回数组（即使为空）
- ✅ 实例对象包含 id, name, url 等字段
- ⚠️ 如果返回空数组，需要先创建测试数据

#### Test 2.1.2: 创建测试实例
```bash
# 如果没有实例数据，创建一个
curl -X POST http://localhost:8088/api/instances \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "id": "test-instance-1",
    "name": "测试实例 1",
    "url": "https://opencode-test.example.com",
    "status": "active"
  }'

# 验证创建成功
curl http://localhost:8088/api/instances \
  -H "Authorization: Bearer $TOKEN"
```

### 2.2 Frontend 实例列表页面

#### Test 2.2.1: 导航到实例页
**操作步骤**:
1. 在主页中
2. 点击底部导航栏的"实例"标签
3. 观察实例列表页面加载

**验证点**:
- ✅ 页面切换成功
- ✅ 显示实例列表（或空状态提示）
- ✅ 每个实例显示名称、URL、状态
- ✅ 可以点击实例查看详情

**截图**:
```bash
adb exec-out screencap -p > test-evidence/03-instances-list.png
```

#### Test 2.2.2: 实例详情查看
**操作步骤**:
1. 点击某个实例
2. 查看实例详情页面

**验证点**:
- ✅ 显示实例完整信息
- ✅ 显示相关任务列表
- ✅ 可以返回实例列表

### 2.3 实例管理流程

#### Test 2.3.1: 创建实例（如果有 UI）
**操作步骤**:
1. 点击"添加实例"按钮
2. 填写实例信息
3. 保存

**验证点**:
- ✅ 创建成功并显示在列表中
- ✅ Backend 数据已保存

#### Test 2.3.2: 刷新实例列表
**操作步骤**:
1. 下拉刷新实例列表
2. 观察加载状态

**验证点**:
- ✅ 显示加载动画
- ✅ 数据更新成功

---

## 🧪 测试用例 3: 任务会话详情获取

**前置条件**: 已成功登录

### 3.1 Backend API 验证

#### Test 3.1.1: 任务列表 API
```bash
TOKEN="<your-token>"

# 获取任务列表
curl http://localhost:8088/api/tasks \
  -H "Authorization: Bearer $TOKEN" \
  -v

# 预期响应
# [
#   {
#     "id": "task-1",
#     "title": "测试任务",
#     "status": "pending",
#     "priority": "high",
#     "sessions": [...],
#     "created_at": "2026-07-06T12:00:00Z"
#   }
# ]
```

**验证点**:
- ✅ HTTP 状态码 200
- ✅ 返回任务数组
- ✅ 任务包含 sessions 字段

#### Test 3.1.2: 任务详情 API
```bash
TASK_ID="task-phase6-final"  # 使用实际存在的任务 ID

# 获取任务详情
curl http://localhost:8088/api/tasks/$TASK_ID \
  -H "Authorization: Bearer $TOKEN" \
  -v

# 预期响应
# {
#   "id": "task-phase6-final",
#   "title": "Phase 6 最终验证",
#   "description": "...",
#   "sessions": [
#     {
#       "id": "session-1",
#       "messages": [...],
#       "created_at": "..."
#     }
#   ]
# }
```

**验证点**:
- ✅ HTTP 状态码 200
- ✅ 返回完整任务对象
- ✅ 包含 sessions 数组
- ✅ sessions 包含 messages 数组

#### Test 3.1.3: 会话详情 API
```bash
SESSION_ID="<从任务详情获取>"

# 获取会话详情
curl http://localhost:8088/api/sessions/$SESSION_ID \
  -H "Authorization: Bearer $TOKEN" \
  -v

# 预期响应
# {
#   "id": "session-1",
#   "task_id": "task-phase6-final",
#   "messages": [
#     {
#       "id": "msg-1",
#       "role": "user",
#       "content": "...",
#       "timestamp": "..."
#     }
#   ]
# }
```

**验证点**:
- ✅ HTTP 状态码 200
- ✅ 返回会话对象
- ✅ 包含消息列表

### 3.2 Frontend 任务会话流程

#### Test 3.2.1: 任务列表页面
**操作步骤**:
1. 点击底部导航栏的"任务"标签
2. 查看任务列表

**验证点**:
- ✅ 显示任务列表
- ✅ 每个任务显示标题、状态、优先级
- ✅ 可以点击查看详情

**截图**:
```bash
adb exec-out screencap -p > test-evidence/04-tasks-list.png
```

#### Test 3.2.2: 任务详情页面
**操作步骤**:
1. 点击某个任务
2. 查看任务详情页面

**验证点**:
- ✅ 显示任务完整信息
- ✅ 显示会话列表
- ✅ 可以点击会话查看消息

**截图**:
```bash
adb exec-out screencap -p > test-evidence/05-task-detail.png
```

#### Test 3.2.3: 会话详情页面
**操作步骤**:
1. 在任务详情中点击某个会话
2. 查看会话消息列表

**验证点**:
- ✅ 显示消息列表
- ✅ 消息按时间排序
- ✅ 区分用户和 AI 消息
- ✅ 可以滚动查看历史消息

**截图**:
```bash
adb exec-out screencap -p > test-evidence/06-session-detail.png
```

#### Test 3.2.4: 创建新任务
**操作步骤**:
1. 点击 FAB (+) 按钮
2. 填写任务标题和描述
3. 保存

**验证点**:
- ✅ 创建成功
- ✅ 新任务显示在列表中
- ✅ Backend 数据已保存（ID 自动生成）

---

## 🐛 问题诊断指南

### 登录失败诊断

#### 症状 1: 点击登录无响应
**可能原因**:
1. Backend 未启动
2. 网络配置错误（adb reverse 未设置）
3. API 端点配置错误

**诊断步骤**:
```bash
# 1. 检查 Backend
ps aux | grep pocketd
curl http://localhost:8088/healthz

# 2. 检查网络配置
adb reverse --list
adb shell "curl http://localhost:8088/healthz"

# 3. 检查前端配置
grep VITE_API_BASE frontend/.env
cat android/app/src/main/assets/capacitor.config.json
```

#### 症状 2: 返回 401/403 错误
**可能原因**:
1. POCKET_DEV_AUTH 未设置
2. 用户名/密码错误
3. JWT_SECRET 配置不一致

**诊断步骤**:
```bash
# 检查环境变量
ps aux | grep pocketd | grep DEV_AUTH

# 重启 Backend 并设置正确的环境变量
killall pocketd
cd backend
export POCKET_DEV_AUTH=true
export JWT_SECRET=test-secret-key-for-phase7-validation
./pocketd
```

#### 症状 3: 登录成功但立即退出
**可能原因**:
1. token 未正确存储到 localStorage
2. authStore 状态同步问题
3. router guard 逻辑错误

**诊断步骤**:
```bash
# 启用 WebView 调试
adb shell settings put global webview_multiprocess 0

# 查看 WebView 控制台日志
# Chrome 打开: chrome://inspect
# 选择应用并查看 Console

# 手动检查 localStorage
# 在 Console 中执行:
localStorage.getItem('pocket_token')
localStorage.getItem('pocket_user')
```

### 实例/会话数据获取失败

#### 症状: API 返回空数组
**可能原因**:
1. 数据库中无测试数据
2. 查询过滤条件过严

**解决方案**:
```bash
# 创建测试数据
./quick-test-opencode.sh

# 或手动创建
sqlite3 backend/data/pocket.sqlite <<EOF
INSERT INTO tasks (id, title, status, priority) 
VALUES ('test-task-1', '测试任务', 'pending', 'high');
EOF
```

---

## 📊 测试报告模板

### 执行记录

| 测试用例 | 状态 | 执行时间 | 备注 |
|---------|------|---------|------|
| 1.1.1 登录 API | ⏳ | - | - |
| 1.1.2 Token 验证 | ⏳ | - | - |
| 1.2.1 登录页面渲染 | ⏳ | - | - |
| 1.2.2 手动登录 | ⏳ | - | - |
| 1.2.3 状态持久化 | ⏳ | - | - |
| 2.1.1 实例列表 API | ⏳ | - | - |
| 2.2.1 实例列表页 | ⏳ | - | - |
| 3.1.1 任务列表 API | ⏳ | - | - |
| 3.2.1 任务列表页 | ⏳ | - | - |
| 3.2.3 会话详情页 | ⏳ | - | - |

### 缺陷记录

| ID | 优先级 | 描述 | 复现步骤 | 状态 |
|----|-------|------|---------|------|
| - | - | - | - | - |

### 测试总结

**通过率**: _/%  
**关键问题**: 
- 

**建议**: 
- 

---

## 🚀 快速执行脚本

创建自动化测试脚本：

```bash
#!/bin/bash
# run-simulator-tests.sh

set -e

echo "=========================================="
echo "OpenCode Pocket 模拟器测试执行"
echo "=========================================="

# 设置 adb 路径
export PATH=$PATH:~/Library/Android/sdk/platform-tools

# 1. 检查 Backend
echo "1. 检查 Backend 状态..."
if ! pgrep -f pocketd > /dev/null; then
  echo "⚠️  Backend 未运行，请先启动 Backend"
  exit 1
fi
echo "✅ Backend 运行中"

# 2. 检查模拟器
echo "2. 检查模拟器连接..."
if ! adb devices | grep -q "emulator"; then
  echo "⚠️  模拟器未连接，请先启动模拟器"
  exit 1
fi
DEVICE=$(adb devices | grep emulator | awk '{print $1}')
echo "✅ 模拟器已连接: $DEVICE"

# 3. 配置网络
echo "3. 配置网络转发..."
adb -s $DEVICE reverse tcp:8088 tcp:8088
echo "✅ 网络配置完成"

# 4. 测试登录 API
echo "4. 测试登录 API..."
TOKEN=$(curl -s -X POST http://localhost:8088/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin"}' \
  | jq -r '.token')

if [ -z "$TOKEN" ] || [ "$TOKEN" = "null" ]; then
  echo "❌ 登录失败"
  exit 1
fi
echo "✅ 登录成功，获取 token"

# 5. 测试任务 API
echo "5. 测试任务 API..."
TASK_COUNT=$(curl -s http://localhost:8088/api/tasks \
  -H "Authorization: Bearer $TOKEN" \
  | jq '. | length')
echo "✅ 获取任务列表: $TASK_COUNT 个任务"

# 6. 安装 APK
echo "6. 安装 APK..."
APK="android/app/build/outputs/apk/debug/app-debug.apk"
if [ ! -f "$APK" ]; then
  echo "⚠️  APK 不存在，请先构建"
  exit 1
fi
adb -s $DEVICE install -r $APK
echo "✅ APK 安装完成"

# 7. 启动应用
echo "7. 启动应用..."
adb -s $DEVICE shell am start -n com.kaixuan.opencode.pocket/.MainActivity
sleep 3
echo "✅ 应用已启动"

# 8. 截图
echo "8. 捕获截图..."
mkdir -p test-evidence
adb -s $DEVICE exec-out screencap -p > test-evidence/current-screen.png
echo "✅ 截图已保存: test-evidence/current-screen.png"

echo ""
echo "=========================================="
echo "自动化测试完成"
echo "=========================================="
echo "请手动完成以下测试："
echo "1. 在模拟器中输入 admin/admin 并登录"
echo "2. 验证任务列表显示"
echo "3. 验证实例列表显示"
echo "4. 验证会话详情显示"
echo ""
echo "测试证据保存在: test-evidence/"
echo "=========================================="
```

---

**制定人**: Kiro AI  
**日期**: 2026-07-06  
**版本**: v1.0
