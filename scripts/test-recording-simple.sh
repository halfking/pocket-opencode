#!/bin/bash
# test-recording-simple.sh
# 简化版录音测试 - 跳过 Maestro，直接使用 ADB 操作

set -e

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_ROOT"

APP_ID="${POCKET_APP_ID:-com.kaixuan.opencode.pocket}"
TEST_AUDIO_FILE="scripts/__tests__/test-audio-5s.wav"
LOG_FILE="/tmp/openpocket-recording-test-$(date +%Y%m%d-%H%M%S).log"
RESULT_FILE="/tmp/openpocket-test-result-$(date +%Y%m%d-%H%M%S).json"

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
echo -e "${BLUE}OpenPocket 简化版录音测试${NC}"
echo -e "${BLUE}========================================${NC}"

# 检查设备
print_step "检查设备连接"

if ! command -v adb &> /dev/null; then
    print_error "adb 未安装"
    exit 1
fi

DEVICE_COUNT=$(adb devices | grep -w "device" | wc -l)
if [ "$DEVICE_COUNT" -eq 0 ]; then
    print_error "未检测到连接的设备"
    exit 1
fi

DEVICE_ID=$(adb devices | grep -w "device" | head -1 | awk '{print $1}')
print_success "设备: $DEVICE_ID"

# 检查应用是否已安装
print_step "检查应用状态"

if adb -s "$DEVICE_ID" shell pm list packages | grep -q "$APP_ID"; then
    print_success "应用已安装: $APP_ID"
else
    print_error "应用未安装: $APP_ID"
    exit 1
fi

# 启动日志监控
print_step "启动日志监控"

adb -s "$DEVICE_ID" logcat -c
adb -s "$DEVICE_ID" logcat | grep -E "VAD|Paraformer|MeetingRecord|openpocket" > "$LOG_FILE" &
LOGCAT_PID=$!
echo "$LOGCAT_PID" > /tmp/openpocket-logcat.pid

print_success "日志监控已启动 (PID: $LOGCAT_PID)"

# 启动应用
print_step "启动应用"

adb -s "$DEVICE_ID" shell am start -n "$APP_ID/.MainActivity" 2>/dev/null
sleep 5
print_success "应用已启动"

# 使用 ADB 模拟点击
print_step "导航到录音页面（ADB 模拟）"

echo "  请在设备上手动导航到录音页面："
echo "  1. 点击底部「更多」tab"
echo "  2. 点击「会议」宫格"
echo "  3. 点击 🎙 FAB 进入录音页"
echo ""
read -p "  完成后按 Enter 继续..."

# 获取屏幕尺寸
SCREEN_SIZE=$(adb -s "$DEVICE_ID" shell wm size | grep -o '[0-9]*x[0-9]*' | head -1)
WIDTH=$(echo $SCREEN_SIZE | cut -d'x' -f1)
HEIGHT=$(echo $SCREEN_SIZE | cut -d'x' -f2)

# 计算中心点（假设录音按钮在屏幕下半部分中央）
CENTER_X=$((WIDTH / 2))
BUTTON_Y=$((HEIGHT * 3 / 4))

print_step "开始录音"

echo "  点击坐标: ($CENTER_X, $BUTTON_Y)"
adb -s "$DEVICE_ID" shell input tap $CENTER_X $BUTTON_Y
sleep 2

print_success "录音已启动（如果按钮位置正确）"

# 注入音频
print_step "注入测试音频"

if [ -f "$TEST_AUDIO_FILE" ]; then
    DEVICE_AUDIO_PATH="/sdcard/Download/openpocket-test-audio.wav"
    adb -s "$DEVICE_ID" push "$TEST_AUDIO_FILE" "$DEVICE_AUDIO_PATH" 2>&1 | grep -v "bytes"
    
    # 播放音频
    adb -s "$DEVICE_ID" shell am start -a android.intent.action.VIEW \
        -d "file://$DEVICE_AUDIO_PATH" \
        -t "audio/*" 2>/dev/null || true
    
    print_success "音频文件已推送并播放"
    
    echo -n "  等待音频处理"
    for i in {1..10}; do
        echo -n "."
        sleep 1
    done
    echo ""
else
    print_warning "测试音频不存在，请手动说话测试"
    sleep 10
fi

# 继续录音观察
print_step "观察 VAD 和转写"

echo "  录音中... (10秒)"
sleep 10

# 停止录音
print_step "停止录音"

echo "  请在设备上点击停止录音按钮"
read -p "  完成后按 Enter 继续..."

# 验证结果
print_step "验证测试结果"

# 停止 logcat
if [ -f /tmp/openpocket-logcat.pid ]; then
    LOGCAT_PID=$(cat /tmp/openpocket-logcat.pid)
    kill $LOGCAT_PID 2>/dev/null || true
    rm /tmp/openpocket-logcat.pid
fi

sleep 2

PASS_COUNT=0
FAIL_COUNT=0

# 测试 1: VAD 初始化
if grep -q "VAD enabled\|VAD.*init" "$LOG_FILE"; then
    print_success "Test 1: VAD 初始化成功"
    ((PASS_COUNT++))
else
    print_error "Test 1: VAD 初始化失败"
    ((FAIL_COUNT++))
fi

# 测试 2: VAD 端点检测
if grep -q "VAD detected\|speech.*end" "$LOG_FILE"; then
    print_success "Test 2: VAD 检测到语音端点"
    ((PASS_COUNT++))
else
    print_warning "Test 2: VAD 未检测到语音端点"
    ((FAIL_COUNT++))
fi

# 测试 3: Paraformer 转写
if grep -q "Paraformer\|transcription" "$LOG_FILE"; then
    print_success "Test 3: Paraformer 转写执行"
    ((PASS_COUNT++))
else
    print_warning "Test 3: Paraformer 转写未执行"
    ((FAIL_COUNT++))
fi

# 测试 4: 无崩溃
if ! grep -q "FATAL\|AndroidRuntime.*FATAL" "$LOG_FILE"; then
    print_success "Test 4: 无应用崩溃"
    ((PASS_COUNT++))
else
    print_error "Test 4: 检测到应用崩溃"
    ((FAIL_COUNT++))
fi

# 测试 5: 录音服务运行
if grep -q "MeetingRecord\|RecordService" "$LOG_FILE"; then
    print_success "Test 5: 录音服务正常运行"
    ((PASS_COUNT++))
else
    print_error "Test 5: 录音服务未运行"
    ((FAIL_COUNT++))
fi

# 生成报告
cat > "$RESULT_FILE" <<EOF
{
  "timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "device": "$DEVICE_ID",
  "app_id": "$APP_ID",
  "total_tests": $((PASS_COUNT + FAIL_COUNT)),
  "passed": $PASS_COUNT,
  "failed": $FAIL_COUNT,
  "log_file": "$LOG_FILE",
  "tests": {
    "vad_init": $(grep -q "VAD enabled\|VAD.*init" "$LOG_FILE" && echo true || echo false),
    "vad_detection": $(grep -q "VAD detected\|speech.*end" "$LOG_FILE" && echo true || echo false),
    "paraformer": $(grep -q "Paraformer\|transcription" "$LOG_FILE" && echo true || echo false),
    "no_crash": $(! grep -q "FATAL\|AndroidRuntime.*FATAL" "$LOG_FILE" && echo true || echo false),
    "service_running": $(grep -q "MeetingRecord\|RecordService" "$LOG_FILE" && echo true || echo false)
  }
}
EOF

echo ""
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo -e "${BLUE}测试结果总结${NC}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo "  通过: $PASS_COUNT"
echo "  失败: $FAIL_COUNT"
echo "  日志: $LOG_FILE"
echo "  报告: $RESULT_FILE"
echo ""

if [ $FAIL_COUNT -eq 0 ]; then
    echo -e "${GREEN}✓ 所有测试通过${NC}"
    EXIT_CODE=0
else
    echo -e "${YELLOW}⚠ 部分测试失败${NC}"
    EXIT_CODE=1
fi

echo ""
echo "查看详细日志:"
echo "  cat $LOG_FILE"
echo ""
echo "查看关键日志行:"
echo "  grep -E 'VAD|Paraformer' $LOG_FILE"
echo ""

exit $EXIT_CODE
