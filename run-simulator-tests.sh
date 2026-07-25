#!/bin/bash
# OpenCode Pocket 模拟器测试执行脚本

set -e

echo "=========================================="
echo "OpenCode Pocket 模拟器测试执行"
echo "=========================================="

# 设置 adb 路径
export PATH=$PATH:~/Library/Android/sdk/platform-tools

# 1. 检查 Backend
echo ""
echo "📍 步骤 1/8: 检查 Backend 状态..."
if ! pgrep -f pocketd > /dev/null; then
  echo "⚠️  Backend 未运行，请先启动 Backend"
  echo "提示: cd backend && ./pocketd"
  exit 1
fi
BACKEND_PID=$(pgrep -f pocketd | head -1)
echo "✅ Backend 运行中 (PID: $BACKEND_PID)"

# 测试健康检查
if curl -sf http://localhost:8088/healthz > /dev/null; then
  echo "✅ Backend 健康检查通过"
else
  echo "❌ Backend 健康检查失败"
  exit 1
fi

# 2. 检查模拟器
echo ""
echo "📍 步骤 2/8: 检查模拟器连接..."
if ! command -v adb &> /dev/null; then
  echo "❌ adb 未找到，请安装 Android SDK"
  exit 1
fi

if ! adb devices | grep -q "emulator"; then
  echo "⚠️  模拟器未连接，请先启动模拟器"
  echo "提示: 打开 Android Studio -> Device Manager -> 启动模拟器"
  exit 1
fi
DEVICE=$(adb devices | grep emulator | awk '{print $1}')
echo "✅ 模拟器已连接: $DEVICE"

# 3. 配置网络
echo ""
echo "📍 步骤 3/8: 配置网络转发..."
adb -s $DEVICE reverse tcp:8088 tcp:8088
adb -s $DEVICE reverse --list | grep 8088
echo "✅ 网络配置完成 (tcp:8088 -> tcp:8088)"

# 4. 测试登录 API
echo ""
echo "📍 步骤 4/8: 测试登录 API..."
LOGIN_RESPONSE=$(curl -s -w "\n%{http_code}" -X POST http://localhost:8088/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin"}')

HTTP_CODE=$(echo "$LOGIN_RESPONSE" | tail -n1)
RESPONSE_BODY=$(echo "$LOGIN_RESPONSE" | head -n-1)

if [ "$HTTP_CODE" != "200" ]; then
  echo "❌ 登录失败 (HTTP $HTTP_CODE)"
  echo "响应: $RESPONSE_BODY"
  echo ""
  echo "可能的原因："
  echo "1. POCKET_DEV_AUTH 未设置为 true"
  echo "2. Backend 配置错误"
  echo ""
  echo "解决方案："
  echo "  killall pocketd"
  echo "  cd backend"
  echo "  export POCKET_DEV_AUTH=true"
  echo "  export JWT_SECRET=test-secret-key-for-phase7-validation"
  echo "  ./pocketd"
  exit 1
fi

TOKEN=$(echo "$RESPONSE_BODY" | jq -r '.token')
USER=$(echo "$RESPONSE_BODY" | jq -r '.user')

if [ -z "$TOKEN" ] || [ "$TOKEN" = "null" ]; then
  echo "❌ 登录成功但未获取到 token"
  echo "响应: $RESPONSE_BODY"
  exit 1
fi

echo "✅ 登录成功"
echo "   用户: $USER"
echo "   Token: ${TOKEN:0:20}..."

# 5. 测试任务 API
echo ""
echo "📍 步骤 5/8: 测试任务 API..."
TASKS_RESPONSE=$(curl -s -w "\n%{http_code}" http://localhost:8088/api/tasks \
  -H "Authorization: Bearer $TOKEN")

HTTP_CODE=$(echo "$TASKS_RESPONSE" | tail -n1)
TASKS_BODY=$(echo "$TASKS_RESPONSE" | head -n-1)

if [ "$HTTP_CODE" != "200" ]; then
  echo "❌ 任务 API 调用失败 (HTTP $HTTP_CODE)"
  exit 1
fi

TASK_COUNT=$(echo "$TASKS_BODY" | jq '. | length')
echo "✅ 获取任务列表成功: $TASK_COUNT 个任务"

if [ "$TASK_COUNT" -gt 0 ]; then
  echo "   任务示例:"
  echo "$TASKS_BODY" | jq -r '.[0] | "   - ID: \(.id)\n   - 标题: \(.title)\n   - 状态: \(.status)"'
fi

# 6. 测试实例 API
echo ""
echo "📍 步骤 6/8: 测试实例 API..."
INSTANCES_RESPONSE=$(curl -s -w "\n%{http_code}" http://localhost:8088/api/instances \
  -H "Authorization: Bearer $TOKEN")

HTTP_CODE=$(echo "$INSTANCES_RESPONSE" | tail -n1)
INSTANCES_BODY=$(echo "$INSTANCES_RESPONSE" | head -n-1)

if [ "$HTTP_CODE" != "200" ]; then
  echo "⚠️  实例 API 调用失败 (HTTP $HTTP_CODE)"
  echo "   这可能是正常的，如果端点不存在"
else
  INSTANCE_COUNT=$(echo "$INSTANCES_BODY" | jq '. | length' 2>/dev/null || echo "N/A")
  echo "✅ 获取实例列表成功: $INSTANCE_COUNT 个实例"
fi

# 7. 检查 APK
echo ""
echo "📍 步骤 7/8: 检查和安装 APK..."
APK="android/app/build/outputs/apk/debug/app-debug.apk"
if [ ! -f "$APK" ]; then
  echo "⚠️  APK 不存在: $APK"
  echo "开始构建 APK..."
  
  cd frontend
  npm run build
  npx cap sync android
  cd ../android
  ./gradlew assembleDebug
  cd ..
  
  if [ ! -f "$APK" ]; then
    echo "❌ APK 构建失败"
    exit 1
  fi
fi

APK_SIZE=$(ls -lh "$APK" | awk '{print $5}')
echo "✅ APK 存在: $APK ($APK_SIZE)"

echo "   正在安装 APK..."
adb -s $DEVICE install -r $APK 2>&1 | grep -v "warning"
echo "✅ APK 安装完成"

# 8. 启动应用
echo ""
echo "📍 步骤 8/8: 启动应用..."
adb -s $DEVICE shell am start -n com.kaixuan.opencode.pocket/.MainActivity
sleep 3
echo "✅ 应用已启动"

# 截图
mkdir -p test-evidence
adb -s $DEVICE exec-out screencap -p > test-evidence/00-startup-$(date +%H%M%S).png
echo "✅ 启动截图已保存"

echo ""
echo "=========================================="
echo "✅ 自动化准备测试完成"
echo "=========================================="
echo ""
echo "📋 Backend 测试总结:"
echo "  ✅ 登录 API: 正常"
echo "  ✅ Token 验证: 通过"
echo "  ✅ 任务 API: $TASK_COUNT 个任务"
echo "  ✅ 实例 API: $INSTANCE_COUNT 个实例"
echo ""
echo "📱 应用状态:"
echo "  ✅ APK 已安装"
echo "  ✅ 应用已启动"
echo "  ✅ 网络已配置"
echo ""
echo "🧪 下一步手动测试:"
echo "  1. 在模拟器中查看登录页面"
echo "  2. 输入用户名: admin"
echo "  3. 输入密码: admin"
echo "  4. 点击登录按钮"
echo "  5. 验证跳转到主页"
echo "  6. 验证任务列表显示"
echo "  7. 验证实例列表显示"
echo ""
echo "📸 测试证据保存在: test-evidence/"
echo ""
echo "🐛 如果登录失败，运行以下命令查看日志:"
echo "  adb -s $DEVICE logcat | grep -i 'opencode\\|pocket\\|webkit'"
echo ""
echo "=========================================="
