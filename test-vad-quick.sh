#!/bin/bash
# Phase 1 VAD 快速测试

LOG_FILE="/tmp/vad-quick-$(date +%s).log"

echo "=== OpenPocket Phase 1 VAD 快速测试 ==="
echo ""

# 清空并启动日志监控
adb logcat -c
adb logcat | grep -E "VAD|MeetingRecordService" > "$LOG_FILE" &
LOGCAT_PID=$!
echo "✅ 日志监控启动 (PID: $LOGCAT_PID)"

# 获取当前UI并找到录音按钮
adb shell uiautomator dump /sdcard/ui.xml > /dev/null 2>&1
adb pull /sdcard/ui.xml /tmp/ui.xml > /dev/null 2>&1

if grep -q "开始录音" /tmp/ui.xml; then
    BOUNDS=$(grep "开始录音" /tmp/ui.xml | grep -o 'bounds="\[[0-9,]*\]\[[0-9,]*\]"' | head -1)
    X1=$(echo "$BOUNDS" | sed 's/.*\[\([0-9]*\),.*/\1/')
    Y1=$(echo "$BOUNDS" | sed 's/.*,\([0-9]*\)\].*/\1/')
    X2=$(echo "$BOUNDS" | sed 's/.*\]\[\([0-9]*\),.*/\1/')
    Y2=$(echo "$BOUNDS" | sed 's/.*,\([0-9]*\)\]".*/\1/')
    TAP_X=$(( (X1 + X2) / 2 ))
    TAP_Y=$(( (Y1 + Y2) / 2 ))
    
    echo "✅ 找到录音按钮: ($TAP_X, $TAP_Y)"
    echo "⏱  点击开始录音..."
    adb shell input tap $TAP_X $TAP_Y
    sleep 3
    
    echo "⏱  录音15秒（请对手机说话）..."
    sleep 15
    
    echo "⏱  停止录音..."
    adb shell input tap $TAP_X $TAP_Y
    sleep 2
else
    echo "❌ 未找到开始录音按钮"
    echo "当前UI:"
    grep 'text=' /tmp/ui.xml | grep -v 'text=""' | head -10
fi

# 停止监控并分析
kill $LOGCAT_PID 2>/dev/null
sleep 1

echo ""
echo "=== 测试结果 ==="
echo ""

if grep -q "VAD enabled" "$LOG_FILE"; then
    echo "✅ VAD初始化成功"
    grep "VAD enabled" "$LOG_FILE" | head -1
else
    echo "❌ VAD初始化失败或未找到日志"
fi

VAD_COUNT=$(grep -c "VAD detected speech end" "$LOG_FILE" 2>/dev/null || echo 0)
echo ""
echo "VAD端点检测: $VAD_COUNT 次"

if [ "$VAD_COUNT" -gt 0 ]; then
    echo ""
    echo "分片详情:"
    grep "VAD detected speech end" "$LOG_FILE"
fi

echo ""
echo "完整日志: $LOG_FILE"
