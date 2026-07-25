#!/bin/bash
# OpenCode Pocket 开发环境快速启动脚本
# 用途: 一键启动完整开发环境

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "=========================================="
echo "OpenCode Pocket 开发环境启动"
echo "=========================================="
echo ""

# 颜色定义
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

# 检查函数
check_command() {
    if command -v $1 &> /dev/null; then
        echo -e "${GREEN}✓${NC} $1 已安装"
        return 0
    else
        echo -e "${RED}✗${NC} $1 未安装"
        return 1
    fi
}

# 1. 检查依赖
echo "Step 1: 检查依赖..."
check_command "go" || { echo "请安装 Go 1.22+"; exit 1; }
check_command "node" || { echo "请安装 Node.js 18+"; exit 1; }
check_command "adb" || { echo "请安装 Android SDK"; exit 1; }
echo ""

# 2. 启动Backend
echo "Step 2: 启动 Backend 服务..."
cd "$PROJECT_ROOT/backend"

# 停止旧进程
killall pocketd 2>/dev/null || true
sleep 1

# 设置环境变量
export JWT_SECRET="test-secret-key-for-phase7-validation"
export POCKET_DB_PATH="./data/pocket.sqlite"
export POCKET_HTTP_PORT="8088"
export POCKET_DEV_AUTH="true"
export POCKET_AUTH_USER="admin"
export POCKET_AUTH_PASS="admin"
export POCKET_OPENCODE_INSTANCES='[{"id":"demo-main","displayName":"Demo OpenCode Instance","apiBaseURL":"http://demo-api","environment":"development","capabilities":["session","summary","pty"]}]'
export POCKET_OPENCODE_TIMEOUT_MS="10000"

# 启动Backend
nohup ./pocketd > "$PROJECT_ROOT/logs/pocketd-dev.log" 2>&1 &
BACKEND_PID=$!
echo -e "${GREEN}✓${NC} Backend 已启动 (PID: $BACKEND_PID)"

# 等待Backend启动
sleep 3
if curl -s http://localhost:8088/healthz > /dev/null; then
    echo -e "${GREEN}✓${NC} Backend 健康检查通过"
else
    echo -e "${RED}✗${NC} Backend 启动失败"
    exit 1
fi
echo ""

# 3. 检查模拟器
echo "Step 3: 检查 Android 模拟器..."
EMULATOR_DEVICES=$(adb devices | grep "emulator" | wc -l)
if [ "$EMULATOR_DEVICES" -gt 0 ]; then
    EMULATOR_ID=$(adb devices | grep "emulator" | head -1 | awk '{print $1}')
    echo -e "${GREEN}✓${NC} 模拟器已运行: $EMULATOR_ID"
    
    # 配置端口转发
    adb -s "$EMULATOR_ID" reverse tcp:8088 tcp:8088
    echo -e "${GREEN}✓${NC} 端口转发已配置: tcp:8088"
else
    echo -e "${YELLOW}⚠${NC} 模拟器未运行"
    echo "   启动命令: emulator -avd pocket_test -no-snapshot -no-audio &"
fi
echo ""

# 4. 显示状态
echo "=========================================="
echo "开发环境启动完成！"
echo "=========================================="
echo ""
echo "服务状态:"
echo "  - Backend: http://localhost:8088"
echo "  - 健康检查: http://localhost:8088/healthz"
echo "  - API文档: http://localhost:8088/api/"
echo ""
echo "测试命令:"
echo "  - 登录: curl -X POST http://localhost:8088/api/auth/login -H 'Content-Type: application/json' -d '{\"username\":\"admin\",\"password\":\"admin\"}'"
echo "  - 实例: curl http://localhost:8088/api/instances"
echo ""
echo "日志文件:"
echo "  - Backend: $PROJECT_ROOT/logs/pocketd-dev.log"
echo "  - 查看: tail -f $PROJECT_ROOT/logs/pocketd-dev.log"
echo ""
echo "停止服务:"
echo "  - killall pocketd"
echo ""
