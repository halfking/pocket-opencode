#!/bin/bash
# Phase 1 VAD Maestro自动化测试包装脚本

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOG_FILE="/tmp/openpocket-vad-maestro-$(date +%Y%m%d-%H%M%S).log"

echo "========================================"
echo "OpenPocket Phase 1 VAD Maestro测试"
echo "测试时间: $(date '+%Y-%m-%d %H:%M:%S')"
echo "========================================"
echo ""

# 1. 检查设备
echo "1. 检查设备连接..."
DEVICE=$(adb devices | grep -v "List" | awk '{print $1}' | head -1)
if [ -z "$DEVICE" ]; then
    echo "❌ 未检测到设备"
    exit 1
fi
echo "✅ 设备已连接: $DEVICE"
echo ""

# 2. 清空日志并启动后台监控
echo "2. 启动logcat监控..."
adb logcat -c
adb logcat | grep -E "VAD|MeetingRecordService|sherpa" > "$LOG_FILE" &
LOGCAT_PID=$!
echo "✅ 监控已启动 (PID: $LOGCAT_PID)"
echo ""

# 3. 运行Maestro测试
echo "3. 运行Maestro自动化测试..."
echo "   - 应用将自动启动"
echo "   - 自动点击「开始录音」"
echo "   - 录音15秒"
echo "   - 自动停止录音"
echo ""
echo "⚠️  重要：在接下来的15秒内，请对手机说话："
echo "   '你好，这是VAD延迟测试' (停顿1秒) '第二句话'"
echo ""
echo "按Enter键继续..."
read

cd "$SCRIPT_DIR"
maestro test .maestro/vad-simple-test.yaml || {
    echo "❌ Maestro测试失败"
    kill $LOGCAT_PID 2>/dev/null
    exit 1
}

echo ""
echo "✅ Maestro测试完成"
echo ""

# 4. 停止监控
sleep 2
kill $LOGCAT_PID 2>/dev/null
echo "4. 停止日志监控"
echo ""

# 5. 分析结果
echo "5. 分析VAD测试结果..."
echo "========================================="
echo ""

if grep -q "VAD enabled" "$LOG_FILE"; then
    echo "✅ VAD初始化成功"
    grep "VAD enabled" "$LOG_FILE" | head -1
else
    if grep -q "VAD initialization failed" "$LOG_FILE"; then
        echo "❌ VAD初始化失败（已回退到8秒固定切片）"
        grep "VAD initialization failed" "$LOG_FILE" | head -1
    else
        echo "⚠️  未找到VAD初始化日志"
    fi
fi
echo ""

VAD_COUNT=$(grep -c "VAD detected speech end" "$LOG_FILE" 2>/dev/null || echo 0)
if [ "$VAD_COUNT" -gt 0 ]; then
    echo "✅ VAD端点检测: 检测到 $VAD_COUNT 次语音结束"
    echo ""
    echo "分片详情:"
    grep "VAD detected speech end" "$LOG_FILE" | head -5
else
    echo "⚠️  未检测到VAD端点"
    echo "   可能原因："
    echo "   - 无音频输入（未说话）"
    echo "   - 麦克风权限问题"
    echo "   - VAD使用固定切片模式"
fi
echo ""

# 6. 导出完整日志
echo "========================================="
echo "测试完成"
echo "========================================="
echo ""
echo "完整日志: $LOG_FILE"
echo ""
echo "查看完整VAD日志:"
echo "  cat $LOG_FILE | grep VAD"
echo ""
echo "查看Maestro截图:"
echo "  open ~/.maestro/tests/*/vad-test-complete.png"
echo ""

# 7. 生成验收报告
REPORT_FILE="$SCRIPT_DIR/vad-test-report-$(date +%Y%m%d-%H%M%S).txt"
cat > "$REPORT_FILE" << EOF
# Phase 1 VAD 测试报告

测试时间: $(date '+%Y-%m-%d %H:%M:%S')
测试设备: $DEVICE
测试方法: Maestro自动化

## 测试结果

EOF

if grep -q "VAD enabled" "$LOG_FILE"; then
    echo "✅ VAD初始化: 成功" >> "$REPORT_FILE"
else
    echo "❌ VAD初始化: 失败" >> "$REPORT_FILE"
fi

echo "📊 VAD端点检测次数: $VAD_COUNT" >> "$REPORT_FILE"
echo "" >> "$REPORT_FILE"

if [ "$VAD_COUNT" -gt 0 ]; then
    echo "✅ 动态端点检测: 工作正常" >> "$REPORT_FILE"
    echo "" >> "$REPORT_FILE"
    echo "## 分片详情" >> "$REPORT_FILE"
    grep "VAD detected speech end" "$LOG_FILE" | head -5 >> "$REPORT_FILE"
else
    echo "⚠️  动态端点检测: 未触发（可能无音频输入）" >> "$REPORT_FILE"
fi

echo "" >> "$REPORT_FILE"
echo "## 完整日志" >> "$REPORT_FILE"
echo "位置: $LOG_FILE" >> "$REPORT_FILE"

echo "测试报告已生成: $REPORT_FILE"
cat "$REPORT_FILE"
