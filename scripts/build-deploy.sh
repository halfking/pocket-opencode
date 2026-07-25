#!/bin/bash
# OpenCode Pocket 完整构建和部署脚本
# 用途: 构建前端、Android APK并部署到模拟器

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "=========================================="
echo "OpenCode Pocket 完整构建部署"
echo "=========================================="
echo ""

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

# 1. 构建前端
echo "Step 1: 构建前端..."
cd "$PROJECT_ROOT/frontend"
npm run build
echo -e "${GREEN}✓${NC} 前端构建完成"
echo ""

# 2. 同步到Android
echo "Step 2: 同步到 Android..."
npx cap sync android
echo -e "${GREEN}✓${NC} Capacitor 同步完成"
echo ""

# 3. 构建APK
echo "Step 3: 构建 APK..."
cd "$PROJECT_ROOT/frontend/android"

# 确保使用正确的JDK
export JAVA_HOME="/Library/Java/JavaVirtualMachines/jdk-21.jdk/Contents/Home"

if [ ! -d "$JAVA_HOME" ]; then
    echo -e "${RED}✗${NC} JDK 21 未找到: $JAVA_HOME"
    echo "   请安装 Oracle JDK 21"
    exit 1
fi

./gradlew assembleDebug
APK_PATH="$PROJECT_ROOT/frontend/android/app/build/outputs/apk/debug/app-debug.apk"

if [ -f "$APK_PATH" ]; then
    APK_SIZE=$(du -h "$APK_PATH" | cut -f1)
    echo -e "${GREEN}✓${NC} APK 构建完成: $APK_SIZE"
else
    echo -e "${RED}✗${NC} APK 构建失败"
    exit 1
fi
echo ""

# 4. 检查模拟器
echo "Step 4: 检查模拟器..."
EMULATOR_DEVICES=$(adb devices | grep "emulator" | wc -l)

if [ "$EMULATOR_DEVICES" -eq 0 ]; then
    echo -e "${YELLOW}⚠${NC} 模拟器未运行"
    echo "   是否启动模拟器? (y/n)"
    read -r response
    if [[ "$response" =~ ^[Yy]$ ]]; then
        echo "   启动模拟器..."
        emulator -avd pocket_test -no-snapshot -no-audio &
        echo "   等待模拟器启动 (30秒)..."
        sleep 30
    else
        echo "   跳过部署"
        exit 0
    fi
fi

EMULATOR_ID=$(adb devices | grep "emulator" | head -1 | awk '{print $1}')
echo -e "${GREEN}✓${NC} 模拟器: $EMULATOR_ID"
echo ""

# 5. 安装APK
echo "Step 5: 安装 APK 到模拟器..."
adb -s "$EMULATOR_ID" install -r "$APK_PATH"
echo -e "${GREEN}✓${NC} APK 安装完成"
echo ""

# 6. 配置端口转发
echo "Step 6: 配置端口转发..."
adb -s "$EMULATOR_ID" reverse tcp:8088 tcp:8088
echo -e "${GREEN}✓${NC} 端口转发: tcp:8088"
echo ""

# 7. 启动应用
echo "Step 7: 启动应用..."
adb -s "$EMULATOR_ID" shell am force-stop com.kaixuan.opencode.pocket
sleep 1
adb -s "$EMULATOR_ID" shell am start -n com.kaixuan.opencode.pocket/.MainActivity
echo -e "${GREEN}✓${NC} 应用已启动"
echo ""

# 8. 显示结果
echo "=========================================="
echo "构建部署完成！"
echo "=========================================="
echo ""
echo "应用信息:"
echo "  - 包名: com.kaixuan.opencode.pocket"
echo "  - APK: $APK_PATH"
echo "  - 大小: $APK_SIZE"
echo "  - 模拟器: $EMULATOR_ID"
echo ""
echo "测试命令:"
echo "  - 查看日志: adb -s $EMULATOR_ID logcat | grep -E 'opencode|pocket|Capacitor'"
echo "  - 截图: adb -s $EMULATOR_ID exec-out screencap -p > screenshot.png"
echo "  - 重启应用: adb -s $EMULATOR_ID shell am start -n com.kaixuan.opencode.pocket/.MainActivity"
echo ""
