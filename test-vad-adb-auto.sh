#!/bin/bash
# Phase 1 VAD 纯ADB自动化测试（无需Maestro）

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOG_FILE="/tmp/openpocket-vad-adb-$(date +%Y%m%d-%H%M%S).log"

echo "========================================"
echo "OpenPocket Phase 1 VAD 自动化测试"
echo "方法: ADB + UI Automator"
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

# 2. 清空日志并启动监控
echo "2. 启动logcat监控..."
adb logcat -c
adb logcat | grep -E "VAD|MeetingRecordService|sherpa" > "$LOG_FILE" &
LOGCAT_PID=$!
echo "✅ 监控已启动 (PID: $LOGCAT_PID)"
echo ""

# 3. 启动应用
echo "3. 启动OpenPocket应用..."
adb shell am start -n com.kaixuan.opencode.pocket/.MainActivity > /dev/null 2>&1
sleep 3
echo "✅ 应用已启动"
echo ""

# 4. 获取当前UI布局
echo "4. 分析UI布局..."
adb shell uiautomator dump /sdcard/ui.xml > /dev/null 2>&1
adb pull /sdcard/ui.xml /tmp/ui-before-record.xml > /dev/null 2>&1

if grep -q "开始录音" /tmp/ui-before-record.xml; then
    echo "✅ 找到「开始录音」按钮"
    
    # 提取按钮坐标
    BOUNDS=$(grep "开始录音" /tmp/ui-before-record.xml | grep -o 'bounds="\[[[0-9,]*\]"' | head -1 | sed 's/bounds="\[\([0-9]*\),\([0-9]*\)\]\[\([0-9]*\),\([0-9]*\)\]"/\1 \2 \3 \4/')
    if [ -n "$BOUNDS" ]; then
        read X1 Y1 X2 Y2 <<< "$BOUNDS"
        TAP_X=$(( (X1 + X2) / 2 ))
        TAP_Y=$(( (Y1 + Y2) / 2 ))
        echo "   按钮位置: ($TAP_X, $TAP_Y)"
    else
        echo "❌ 无法获取按钮坐标"
        kill $LOGCAT_PID 2>/dev/null
        exit 1
    fi
else
    echo "❌ 未找到「开始录音」按钮"
    echo "   当前UI:"
    grep -o 'text="[^"]*"' /tmp/ui-before-record.xml | head -20
    kill $LOGCAT_PID 2>/dev/null
    exit 1
fi
echo ""

# 5. 点击开始录音
echo "5. 点击「开始录音」..."
adb shell input tap $TAP_X $TAP_Y
sleep 2
echo "✅ 已点击录音按钮"
echo ""

# 6. 等待VAD初始化
echo "6. 等待VAD初始化（3秒）..."
sleep 3

if grep -q "VAD enabled" "$LOG_FILE"; then
    echo "✅ VAD初始化成功！"
    grep "VAD enabled" "$LOG_FILE" | head -1
elif grep -q "VAD initialization failed" "$LOG_FILE"; then
    echo "⚠️  VAD初始化失败，已回退到固定8秒切片"
    grep "VAD initialization failed" "$LOG_FILE" | head -1
else
    echo "⏳ VAD初始化日志尚未出现（可能仍在初始化）"
fi
echo ""

# 7. 录音测试
echo "7. 录音测试（15秒）..."
echo "   ⚠️  请对手机说话测试："
echo "   '你好，这是VAD延迟测试' (停顿1秒) '第二句话'"
echo ""

for i in {15..1}; do
    echo -ne "   倒计时: $i 秒\r"
    sleep 1
done
echo ""
echo "✅ 录音时间完成"
echo ""

# 8. 分析VAD端点检测
echo "8. 分析VAD端点检测..."
VAD_COUNT=$(grep -c "VAD detected speech end" "$LOG_FILE" 2>/dev/null || echo 0)

if [ "$VAD_COUNT" -gt 0 ]; then
    echo "✅ VAD动态端点检测: 检测到 $VAD_COUNT 次语音结束"
    echo ""
    echo "   分片详情:"
    grep "VAD detected speech end" "$LOG_FILE" | while read line; do
        echo "   $line"
    done
else
    echo "⚠️  未检测到VAD端点"
    echo "   可能原因："
    echo "   - 环境安静，无音频输入"
    echo "   - 麦克风权限未授予"
    echo "   - VAD使用固定切片模式"
fi
echo ""

# 9. 停止录音
echo "9. 停止录音..."
adb shell uiautomator dump /sdcard/ui.xml > /dev/null 2>&1
adb pull /sdcard/ui.xml /tmp/ui-during-record.xml > /dev/null 2>&1

# 尝试找到停止按钮（可能文字已变化）
if grep -q "停止" /tmp/ui-during-record.xml; then
    STOP_TEXT="停止"
elif grep -q "开始录音" /tmp/ui-during-record.xml; then
    STOP_TEXT="开始录音"
else
    # 使用相同坐标再次点击（Toggle按钮）
    STOP_TEXT="toggle"
fi

if [ "$STOP_TEXT" = "toggle" ]; then
    adb shell input tap $TAP_X $TAP_Y
else
    BOUNDS=$(grep "$STOP_TEXT" /tmp/ui-during-record.xml | grep -o 'bounds="\[[[0-9,]*\]"' | head -1 | sed 's/bounds="\[\([0-9]*\),\([0-9]*\)\]\[\([0-9]*\),\([0-9]*\)\]"/\1 \2 \3 \4/')
    read X1 Y1 X2 Y2 <<< "$BOUNDS"
    STOP_X=$(( (X1 + X2) / 2 ))
    STOP_Y=$(( (Y1 + Y2) / 2 ))
    adb shell input tap $STOP_X $STOP_Y
fi

sleep 2
echo "✅ 录音已停止"
echo ""

# 10. 停止监控
kill $LOGCAT_PID 2>/dev/null
echo "10. 停止日志监控"
echo ""

# 11. 截图保存
echo "11. 保存截图..."
adb shell screencap -p /sdcard/vad-test-result.png > /dev/null 2>&1
adb pull /sdcard/vad-test-result.png /tmp/ > /dev/null 2>&1
echo "✅ 截图已保存: /tmp/vad-test-result.png"
echo ""

# 12. 生成测试报告
echo "========================================="
echo "测试完成 - 生成报告"
echo "========================================="
echo ""

REPORT_FILE="$SCRIPT_DIR/VAD_TEST_REPORT_$(date +%Y%m%d_%H%M%S).md"

cat > "$REPORT_FILE" << EOF
# Phase 1 VAD 自动化测试报告

**测试时间**: $(date '+%Y-%m-%d %H:%M:%S')  
**测试设备**: $DEVICE  
**测试方法**: ADB + UI Automator  
**APK版本**: 1.2.0-openpocket

---

## 测试结果

### 1. VAD初始化

EOF

if grep -q "VAD enabled" "$LOG_FILE"; then
    echo "✅ **成功** - VAD动态端点检测已启用" >> "$REPORT_FILE"
    echo "\`\`\`" >> "$REPORT_FILE"
    grep "VAD enabled" "$LOG_FILE" | head -1 >> "$REPORT_FILE"
    echo "\`\`\`" >> "$REPORT_FILE"
elif grep -q "VAD initialization failed" "$LOG_FILE"; then
    echo "❌ **失败** - VAD初始化失败，已回退到固定8秒切片" >> "$REPORT_FILE"
    echo "\`\`\`" >> "$REPORT_FILE"
    grep "VAD initialization failed" "$LOG_FILE" | head -1 >> "$REPORT_FILE"
    echo "\`\`\`" >> "$REPORT_FILE"
else
    echo "⚠️  **未知** - 未找到VAD初始化日志" >> "$REPORT_FILE"
fi

cat >> "$REPORT_FILE" << EOF

### 2. VAD端点检测

- **检测次数**: $VAD_COUNT 次

EOF

if [ "$VAD_COUNT" -gt 0 ]; then
    echo "✅ **工作正常** - VAD成功检测到语音端点" >> "$REPORT_FILE"
    echo "" >> "$REPORT_FILE"
    echo "**分片详情**:" >> "$REPORT_FILE"
    echo "\`\`\`" >> "$REPORT_FILE"
    grep "VAD detected speech end" "$LOG_FILE" >> "$REPORT_FILE"
    echo "\`\`\`" >> "$REPORT_FILE"
else
    echo "⚠️  **未触发** - 可能无音频输入或使用固定切片模式" >> "$REPORT_FILE"
fi

cat >> "$REPORT_FILE" << EOF

### 3. 验收标准检查

- [$([ "$VAD_COUNT" -gt 0 ] && echo "x" || echo " ")] VAD初始化成功
- [ ] 首字延迟 < 1秒（需人工观察）
- [$([ "$VAD_COUNT" -ge 2 ] && echo "x" || echo " ")] 动态端点检测（多个分片）
- [ ] 无崩溃、无ANR

### 4. 测试文件

- **完整日志**: \`$LOG_FILE\`
- **测试截图**: \`/tmp/vad-test-result.png\`
- **UI快照**: \`/tmp/ui-before-record.xml\`, \`/tmp/ui-during-record.xml\`

---

## 下一步

EOF

if grep -q "VAD enabled" "$LOG_FILE" && [ "$VAD_COUNT" -gt 0 ]; then
    cat >> "$REPORT_FILE" << EOF
✅ **Phase 1 自动化测试通过**

建议继续：
1. 人工验证首字延迟（观察屏幕）
2. 测试长时间录音稳定性
3. 启动 Phase 2 Paraformer集成
EOF
else
    cat >> "$REPORT_FILE" << EOF
⚠️  **需要进一步验证**

建议操作：
1. 检查麦克风权限：\`adb shell dumpsys package com.kaixuan.opencode.pocket | grep permission\`
2. 查看完整日志：\`cat $LOG_FILE\`
3. 手动测试录音功能
EOF
fi

cat >> "$REPORT_FILE" << EOF

---

**生成时间**: $(date '+%Y-%m-%d %H:%M:%S')  
**生成工具**: test-vad-adb-auto.sh
EOF

# 显示报告
cat "$REPORT_FILE"

echo ""
echo "========================================="
echo "📄 完整报告已保存"
echo "========================================="
echo ""
echo "报告位置: $REPORT_FILE"
echo "完整日志: $LOG_FILE"
echo "测试截图: /tmp/vad-test-result.png"
echo ""

# 返回成功/失败状态
if grep -q "VAD enabled" "$LOG_FILE"; then
    exit 0
else
    exit 1
fi
