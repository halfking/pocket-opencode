#!/bin/bash
# quick-test.sh
# 快速启动录音自动化测试

set -e

cd "$(dirname "$0")/.."

echo "🚀 OpenPocket 录音自动化测试"
echo ""

# 检查设备
if ! adb devices | grep -q "device$"; then
    echo "❌ 错误: 未检测到连接的设备"
    echo ""
    echo "请确保:"
    echo "  1. 真机已通过 USB 连接"
    echo "  2. 已启用 USB 调试"
    echo "  3. 已授权此计算机"
    echo ""
    exit 1
fi

DEVICE=$(adb devices | grep device | head -1 | awk '{print $1}')
echo "✓ 设备: $DEVICE"
echo ""

# 生成测试音频（如果不存在）
if [ ! -f "scripts/__tests__/test-audio-5s.wav" ]; then
    echo "📝 生成测试音频..."
    node scripts/generate-test-audio.mjs scripts/__tests__/test-audio-5s.wav 5 440 || {
        echo "⚠️  音频生成失败，将使用替代方案"
    }
    echo ""
fi

# 运行测试
echo "🎬 开始自动化测试..."
echo ""
./scripts/test-recording-auto.sh

echo ""
echo "✨ 测试完成！"
