#!/bin/bash
# Phase 1 VAD 真机测试脚本

echo "========================================"
echo "OpenPocket Phase 1 VAD 真机测试"
echo "测试时间: $(date '+%Y-%m-%d %H:%M:%S')"
echo "========================================"
echo ""

# 1. 检查设备连接
echo "1. 检查设备连接..."
DEVICE=$(adb devices | grep -v "List" | awk '{print $1}' | head -1)
if [ -z "$DEVICE" ]; then
    echo "❌ 未检测到设备"
    exit 1
fi
echo "✅ 设备已连接: $DEVICE"
echo ""

# 2. 检查应用安装
echo "2. 检查应用安装..."
if adb shell pm list packages | grep -q "com.kaixuan.opencode.pocket$"; then
    VERSION=$(adb shell dumpsys package com.kaixuan.opencode.pocket | grep versionName | head -1 | awk '{print $1}')
    echo "✅ 应用已安装: $VERSION"
else
    echo "❌ 应用未安装"
    exit 1
fi
echo ""

# 3. 清空日志并启动监控
echo "3. 准备日志监控..."
adb logcat -c
echo "✅ 日志已清空"
echo ""

# 4. 启动应用
echo "4. 启动应用..."
adb shell am start -n com.kaixuan.opencode.pocket/.MainActivity > /dev/null 2>&1
sleep 3
echo "✅ 应用已启动"
echo ""

# 5. 监控日志（15秒）
echo "5. 监控VAD相关日志（持续15秒）..."
echo "   请在手机上执行以下操作："
echo "   - 打开「会议」功能"
echo "   - 点击「开始录音」"
echo "   - 说话测试: '你好，这是VAD延迟测试'"
echo "   - 停顿1秒后再说: '第二句话'"
echo ""
echo "----------------------------------------"

timeout 15 adb logcat | grep -E "VAD|MeetingRecordService|sherpa" --line-buffered | while read line; do
    echo "[$(date '+%H:%M:%S')] $line"
done

echo "----------------------------------------"
echo ""

# 6. 导出完整日志
LOG_FILE="/tmp/openpocket-vad-test-$(date +%Y%m%d-%H%M%S).log"
adb logcat -d > "$LOG_FILE"
echo "6. 完整日志已导出: $LOG_FILE"
echo ""

# 7. 分析关键指标
echo "7. 分析关键指标..."
echo ""

if grep -q "VAD enabled" "$LOG_FILE"; then
    echo "✅ VAD初始化成功"
else
    if grep -q "VAD initialization failed" "$LOG_FILE"; then
        echo "❌ VAD初始化失败（已回退到8秒固定切片）"
        echo "   失败原因:"
        grep "VAD initialization failed" "$LOG_FILE" | tail -1
    else
        echo "⚠️  未找到VAD初始化日志（可能未触发录音）"
    fi
fi
echo ""

VAD_DETECTIONS=$(grep -c "VAD detected speech end" "$LOG_FILE" 2>/dev/null || echo 0)
if [ "$VAD_DETECTIONS" -gt 0 ]; then
    echo "✅ VAD端点检测: 检测到 $VAD_DETECTIONS 次语音结束"
    echo "   分片时长:"
    grep "VAD detected speech end" "$LOG_FILE" | tail -3
else
    echo "⚠️  未检测到VAD端点（可能未录音或使用固定切片模式）"
fi
echo ""

# 8. 结论
echo "========================================"
echo "测试完成"
echo "========================================"
echo ""
echo "下一步："
echo "1. 查看完整日志: cat $LOG_FILE | grep -E 'VAD|MeetingRecord'"
echo "2. 手动验证首字延迟（目标<1秒）"
echo "3. 检查分片数量（目标20-30片/分钟）"
echo ""
