#!/bin/bash
# VAD 真机测试部署脚本
# 用法: ./deploy-and-test.sh

set -e

DEVICE="4c308e2e"
APK_PATH="frontend/android/app/build/outputs/apk/debug/app-debug.apk"
PACKAGE="com.kaixuan.opencode.pocket"

echo "=== OpenPocket VAD 真机测试部署 ==="
echo ""

# 1. 检查设备连接
echo "1. 检查设备连接..."
if ! adb devices | grep -q "$DEVICE"; then
    echo "❌ 设备 $DEVICE 未连接"
    exit 1
fi
echo "✓ 设备已连接"

# 2. 检查 APK 是否存在
echo ""
echo "2. 检查 APK..."
if [ ! -f "$APK_PATH" ]; then
    echo "❌ APK 不存在: $APK_PATH"
    echo "请先运行: cd frontend && node scripts/build-mobile.mjs android prod"
    exit 1
fi
APK_SIZE=$(ls -lh "$APK_PATH" | awk '{print $5}')
echo "✓ APK 已准备: $APK_SIZE"

# 3. 安装 APK
echo ""
echo "3. 安装 APK 到设备..."
adb -s "$DEVICE" install -r "$APK_PATH"
echo "✓ 安装完成"

# 4. 清空日志
echo ""
echo "4. 清空旧日志..."
adb -s "$DEVICE" logcat -c
echo "✓ 日志已清空"

# 5. 启动应用
echo ""
echo "5. 启动应用..."
adb -s "$DEVICE" shell am start -n "$PACKAGE/.MainActivity"
sleep 2
echo "✓ 应用已启动"

# 6. 开始监控日志
echo ""
echo "=== 开始监控 VAD 日志 ==="
echo "按 Ctrl+C 停止"
echo ""

# 实时显示 VAD 相关日志，并保存到文件
LOG_FILE="vad-test-$(date +%Y%m%d-%H%M%S).log"
adb -s "$DEVICE" logcat | tee "$LOG_FILE" | grep --color=auto -E "vad-factory|vad-web-segmenter|vad-segmenter|recordingRuntime|Creating VAD|VAD created|Speech started|Speech ended|Finalizing segment"
