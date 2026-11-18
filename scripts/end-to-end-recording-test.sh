#!/bin/bash
# end-to-end-recording-test.sh
# 完整的端到端录音测试：启动应用 → 开始录音 → 注入音频 → 验证结果

set -e

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_ROOT"

APP_ID="com.kaixuan.opencode.pocket"
DEVICE_ID="${1:-$(adb devices | grep -w device | head -1 | awk '{print $1}')}"
TEST_AUDIO="scripts/__tests__/test-audio-5s.wav"
LOG_FILE="/tmp/openpocket-e2e-test-$(date +%Y%m%d-%H%M%S).log"
RESULT_FILE="/tmp/openpocket-e2e-result-$(date +%Y%m%d-%H%M%S).json"

print_step() {
    echo ""
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${BLUE}$1${NC}"
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
}

print_success() {
    echo -e "${GREEN}✓${NC} $1"
}

print_error() {
    echo -e "${RED}✗${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}⚠${NC} $1"
}

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}OpenPocket 端到端录音测试${NC}"
echo -e "${BLUE}========================================${NC}"

# 检查设备
if [ -z "$DEVICE_ID" ] || [ "$DEVICE_ID" = "List" ]; then
    print_error "未检测到设备"
    exit 1
fi

print_step "Step 1: 环境准备"
print_success "设备: $DEVICE_ID"
print_success "应用: $APP_ID"
print_success "测试音频: $TEST_AUDIO"

# 启动日志监控
print_step "Step 2: 启动日志监控"
adb -s "$DEVICE_ID" logcat -c
adb -s "$DEVICE_ID" logcat > "$LOG_FILE" &
LOGCAT_PID=$!
print_success "日志监控已启动 (PID: $LOGCAT_PID)"

# 确保应用在前台
print_step "Step 3: 启动应用"
adb -s "$DEVICE_ID" shell am start -n "$APP_ID/.MainActivity" > /dev/null 2>&1
sleep 3
CURRENT_APP=$(adb -s "$DEVICE_ID" shell dumpsys window | grep mCurrentFocus | grep -o "$APP_ID" || echo "")
if [ -n "$CURRENT_APP" ]; then
    print_success "应用已在前台"
else
    print_warning "应用可能不在前台"
fi

# 等待用户操作
print_step "Step 4: 手动操作录音"
echo ""
echo "  请在设备上完成以下操作："
echo "  ${YELLOW}1. 点击底部「更多」tab${NC}"
echo "  ${YELLOW}2. 点击「会议」宫格${NC}"
echo "  ${YELLOW}3. 点击 🎙 FAB (开始会议录音)${NC}"
echo "  ${YELLOW}4. 点击麦克风按钮开始录音${NC}"
echo ""
echo "  ⏳ 开始录音后，按 Enter 继续..."
read -r

# 注入音频
print_step "Step 5: 注入测试音频"
if [ -f "$TEST_AUDIO" ]; then
    DEVICE_PATH="/sdcard/Download/openpocket-test-audio.wav"
    adb -s "$DEVICE_ID" push "$TEST_AUDIO" "$DEVICE_PATH" 2>&1 | grep -v "bytes" || true
    print_success "音频已推送到设备"
    
    # 播放音频
    adb -s "$DEVICE_ID" shell am start -a android.intent.action.VIEW \
        -d "file://$DEVICE_PATH" -t "audio/*" > /dev/null 2>&1 || true
    print_success "音频播放已触发"
    
    echo -n "  等待音频处理 (10秒)"
    for _ in {1..10}; do
        echo -n "."
        sleep 1
    done
    echo ""
else
    print_warning "测试音频不存在"
    echo "  请手动说话 10 秒..."
    sleep 10
fi

# 继续录音
print_step "Step 6: 继续录音观察"
echo "  录音中... (10秒)"
sleep 10

# 提示停止
print_step "Step 7: 停止录音"
echo ""
echo "  请在设备上点击停止录音按钮"
echo "  ⏹ 停止后，按 Enter 继续..."
read -r

# 等待处理完成
print_success "等待转写处理完成 (5秒)"
sleep 5

# 停止日志
kill $LOGCAT_PID 2>/dev/null || true
sleep 2

# 验证结果
print_step "Step 8: 分析测试结果"

PASS_COUNT=0
FAIL_COUNT=0
FINDINGS=()

# 只分析录音相关日志
FILTERED_LOG="/tmp/openpocket-filtered-$(date +%Y%m%d-%H%M%S).log"
grep -E "VAD|Paraformer|MeetingRecord|RecordService|openpocket" "$LOG_FILE" > "$FILTERED_LOG" 2>/dev/null || true

LOG_LINES=$(wc -l < "$FILTERED_LOG" | tr -d ' ')
echo "  收集到 $LOG_LINES 行相关日志"

# Test 1: VAD 初始化
echo -n "  Test 1: VAD 初始化..."
if grep -q "VAD.*init\|VAD enabled\|VAD.*start" "$FILTERED_LOG"; then
    echo -e " ${GREEN}✓${NC}"
    ((PASS_COUNT++))
    VAD_INIT=true
    FINDINGS+=("✓ VAD 成功初始化")
else
    echo -e " ${RED}✗${NC}"
    ((FAIL_COUNT++))
    VAD_INIT=false
    FINDINGS+=("✗ 未检测到 VAD 初始化")
fi

# Test 2: VAD 端点检测
echo -n "  Test 2: VAD 端点检测..."
if grep -q "VAD detected\|speech.*end\|endpoint" "$FILTERED_LOG"; then
    echo -e " ${GREEN}✓${NC}"
    ((PASS_COUNT++))
    VAD_DETECT=true
    FINDINGS+=("✓ VAD 检测到语音端点")
else
    echo -e " ${YELLOW}⚠${NC}"
    ((FAIL_COUNT++))
    VAD_DETECT=false
    FINDINGS+=("⚠ VAD 未检测到语音端点")
fi

# Test 3: Paraformer 转写
echo -n "  Test 3: Paraformer 转写..."
if grep -q "Paraformer\|transcription\|recognized" "$FILTERED_LOG"; then
    echo -e " ${GREEN}✓${NC}"
    ((PASS_COUNT++))
    PARAFORMER=true
    FINDINGS+=("✓ Paraformer 转写已执行")
else
    echo -e " ${YELLOW}⚠${NC}"
    ((FAIL_COUNT++))
    PARAFORMER=false
    FINDINGS+=("⚠ Paraformer 转写未检测到")
fi

# Test 4: 无崩溃
echo -n "  Test 4: 应用稳定性..."
if grep -q "FATAL\|AndroidRuntime.*FATAL" "$FILTERED_LOG"; then
    echo -e " ${RED}✗${NC}"
    ((FAIL_COUNT++))
    NO_CRASH=false
    FINDINGS+=("✗ 检测到应用崩溃")
else
    echo -e " ${GREEN}✓${NC}"
    ((PASS_COUNT++))
    NO_CRASH=true
    FINDINGS+=("✓ 应用运行稳定")
fi

# Test 5: 录音服务
echo -n "  Test 5: 录音服务..."
if grep -q "MeetingRecord\|RecordService\|recording" "$FILTERED_LOG"; then
    echo -e " ${GREEN}✓${NC}"
    ((PASS_COUNT++))
    SERVICE_OK=true
    FINDINGS+=("✓ 录音服务正常运行")
else
    echo -e " ${RED}✗${NC}"
    ((FAIL_COUNT++))
    SERVICE_OK=false
    FINDINGS+=("✗ 录音服务未检测到")
fi

# 生成报告
cat > "$RESULT_FILE" <<EOF
{
  "timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "device": "$DEVICE_ID",
  "app_id": "$APP_ID",
  "test_type": "end-to-end",
  "log_lines": $LOG_LINES,
  "total_tests": $((PASS_COUNT + FAIL_COUNT)),
  "passed": $PASS_COUNT,
  "failed": $FAIL_COUNT,
  "log_file": "$FILTERED_LOG",
  "full_log": "$LOG_FILE",
  "tests": {
    "vad_init": $VAD_INIT,
    "vad_detection": $VAD_DETECT,
    "paraformer": $PARAFORMER,
    "no_crash": $NO_CRASH,
    "service_running": $SERVICE_OK
  }
}
EOF

# 显示结果
print_step "测试结果总结"

for finding in "${FINDINGS[@]}"; do
    echo "  $finding"
done

echo ""
echo "  通过: $PASS_COUNT / $((PASS_COUNT + FAIL_COUNT))"
echo "  失败: $FAIL_COUNT / $((PASS_COUNT + FAIL_COUNT))"
echo ""
echo "  过滤日志: $FILTERED_LOG"
echo "  完整日志: $LOG_FILE"
echo "  JSON 报告: $RESULT_FILE"

if [ $FAIL_COUNT -eq 0 ]; then
    echo ""
    echo -e "${GREEN}✓ 所有测试通过！${NC}"
    EXIT_CODE=0
elif [ $PASS_COUNT -ge 3 ]; then
    echo ""
    echo -e "${YELLOW}⚠ 大部分测试通过${NC}"
    EXIT_CODE=0
else
    echo ""
    echo -e "${RED}✗ 多个测试失败${NC}"
    EXIT_CODE=1
fi

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "查看详细信息:"
echo "  cat $FILTERED_LOG | grep -E 'VAD|Paraformer'"
echo "  cat $RESULT_FILE | jq ."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

exit $EXIT_CODE
