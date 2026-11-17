#!/bin/bash
# test-recording-auto.sh
# OpenPocket 全自动录音测试脚本
# 集成 Maestro UI 操作 + ADB 音频注入 + logcat 验证

set -e

# 颜色定义
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_ROOT"

# ============================================================
# 配置
# ============================================================

APP_ID="${POCKET_APP_ID:-com.kaixuan.opencode.pocket}"
TEST_AUDIO_FILE="scripts/__tests__/test-audio-5s.wav"
LOG_FILE="/tmp/openpocket-recording-test-$(date +%Y%m%d-%H%M%S).log"
RESULT_FILE="/tmp/openpocket-test-result-$(date +%Y%m%d-%H%M%S).json"

# ============================================================
# 函数
# ============================================================

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

# 检查设备连接
check_device() {
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
}

# 生成测试音频（使用 TTS）
generate_test_audio() {
    print_step "生成测试音频"
    
    # 方案 1: 使用 ffmpeg 生成 5 秒测试音频（500Hz 正弦波）
    if command -v ffmpeg &> /dev/null; then
        ffmpeg -f lavfi -i "sine=frequency=500:duration=5" -ar 16000 -ac 1 "$TEST_AUDIO_FILE" -y 2>/dev/null || true
        if [ -f "$TEST_AUDIO_FILE" ]; then
            print_success "生成测试音频: $TEST_AUDIO_FILE"
            return 0
        fi
    fi
    
    # 方案 2: 使用 Node.js 生成
    if command -v node &> /dev/null && [ -f "scripts/make-test-tone.mjs" ]; then
        node scripts/make-test-tone.mjs "$TEST_AUDIO_FILE" 2>/dev/null || true
        if [ -f "$TEST_AUDIO_FILE" ]; then
            print_success "生成测试音频: $TEST_AUDIO_FILE"
            return 0
        fi
    fi
    
    print_warning "无法生成测试音频，将使用设备 TTS"
}

# 启动录音（通过 Maestro）
start_recording_maestro() {
    print_step "启动录音（Maestro）"
    
    # 清理之前的日志
    adb -s "$DEVICE_ID" logcat -c
    
    # 后台启动 logcat 监控
    adb -s "$DEVICE_ID" logcat | grep -E "VAD|Paraformer|MeetingRecord|openpocket" > "$LOG_FILE" &
    LOGCAT_PID=$!
    echo "$LOGCAT_PID" > /tmp/openpocket-logcat.pid
    
    print_success "日志监控已启动 (PID: $LOGCAT_PID)"
    
    # 运行 Maestro flow
    if [ -f "scripts/maestro-run.mjs" ]; then
        node scripts/maestro-run.mjs .maestro/auto-recording-test.yaml || {
            print_error "Maestro 测试失败"
            kill $LOGCAT_PID 2>/dev/null || true
            exit 1
        }
    else
        maestro test .maestro/auto-recording-test.yaml || {
            print_error "Maestro 测试失败"
            kill $LOGCAT_PID 2>/dev/null || true
            exit 1
        }
    fi
    
    print_success "录音已启动"
}

# 注入音频
inject_audio() {
    print_step "注入测试音频"
    
    if [ -f "$TEST_AUDIO_FILE" ]; then
        # 方案 1: 推送音频文件到设备并播放
        DEVICE_AUDIO_PATH="/sdcard/Download/test-audio.wav"
        adb -s "$DEVICE_ID" push "$TEST_AUDIO_FILE" "$DEVICE_AUDIO_PATH" 2>/dev/null || true
        
        # 使用 am 启动播放
        adb -s "$DEVICE_ID" shell am start -a android.intent.action.VIEW -d "file://$DEVICE_AUDIO_PATH" -t "audio/*" 2>/dev/null || true
        
        print_success "音频文件已推送并播放"
    else
        # 方案 2: 使用设备 TTS 说话
        print_warning "使用 TTS 方案"
        
        # 启用 TTS 并说一段话
        adb -s "$DEVICE_ID" shell "am start -a android.speech.tts.engine.CHECK_TTS_DATA" 2>/dev/null || true
        sleep 1
        
        # 通过 TTS 服务说话（需要设备支持）
        TEST_TEXT="Testing OpenPocket recording functionality with automatic audio injection"
        adb -s "$DEVICE_ID" shell "am broadcast -a com.android.tts.TTS_QUEUE_PROCESSING_COMPLETED --es text '$TEST_TEXT'" 2>/dev/null || true
        
        print_warning "TTS 可能不可用，将等待 10 秒后继续"
    fi
    
    # 等待音频播放和处理
    echo -n "  等待音频处理"
    for i in {1..10}; do
        echo -n "."
        sleep 1
    done
    echo ""
    print_success "音频处理时间完成"
}

# 停止录音
stop_recording() {
    print_step "停止录音"
    
    # 通过 UI 自动化点击停止按钮
    adb -s "$DEVICE_ID" shell input tap 540 1800 2>/dev/null || true
    
    # 或者通过 accessibility service
    adb -s "$DEVICE_ID" shell "am broadcast -a com.kaixuan.opencode.pocket.STOP_RECORDING" 2>/dev/null || true
    
    sleep 3
    print_success "录音已停止"
}

# 验证结果
verify_results() {
    print_step "验证测试结果"
    
    # 停止 logcat
    if [ -f /tmp/openpocket-logcat.pid ]; then
        LOGCAT_PID=$(cat /tmp/openpocket-logcat.pid)
        kill $LOGCAT_PID 2>/dev/null || true
        rm /tmp/openpocket-logcat.pid
    fi
    
    # 给日志一点时间落盘
    sleep 2
    
    PASS_COUNT=0
    FAIL_COUNT=0
    
    # 测试 1: VAD 初始化
    if grep -q "VAD enabled" "$LOG_FILE"; then
        print_success "Test 1: VAD 初始化成功"
        ((PASS_COUNT++))
    else
        print_error "Test 1: VAD 初始化失败"
        ((FAIL_COUNT++))
    fi
    
    # 测试 2: VAD 端点检测
    if grep -q "VAD detected speech" "$LOG_FILE"; then
        print_success "Test 2: VAD 检测到语音端点"
        ((PASS_COUNT++))
    else
        print_warning "Test 2: VAD 未检测到语音端点（可能无音频输入）"
        ((FAIL_COUNT++))
    fi
    
    # 测试 3: Paraformer 转写
    if grep -q "Paraformer" "$LOG_FILE" && grep -q "transcription" "$LOG_FILE"; then
        print_success "Test 3: Paraformer 转写执行"
        ((PASS_COUNT++))
    else
        print_warning "Test 3: Paraformer 转写未执行或无输出"
        ((FAIL_COUNT++))
    fi
    
    # 测试 4: 无崩溃
    if ! grep -q "FATAL\|AndroidRuntime" "$LOG_FILE"; then
        print_success "Test 4: 无应用崩溃"
        ((PASS_COUNT++))
    else
        print_error "Test 4: 检测到应用崩溃"
        ((FAIL_COUNT++))
    fi
    
    # 测试 5: 录音服务运行
    if grep -q "MeetingRecordService" "$LOG_FILE"; then
        print_success "Test 5: 录音服务正常运行"
        ((PASS_COUNT++))
    else
        print_error "Test 5: 录音服务未运行"
        ((FAIL_COUNT++))
    fi
    
    # 生成 JSON 报告
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
    "vad_init": $(grep -q "VAD enabled" "$LOG_FILE" && echo true || echo false),
    "vad_detection": $(grep -q "VAD detected speech" "$LOG_FILE" && echo true || echo false),
    "paraformer": $(grep -q "Paraformer.*transcription" "$LOG_FILE" && echo true || echo false),
    "no_crash": $(! grep -q "FATAL\|AndroidRuntime" "$LOG_FILE" && echo true || echo false),
    "service_running": $(grep -q "MeetingRecordService" "$LOG_FILE" && echo true || echo false)
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
    
    if [ $FAIL_COUNT -eq 0 ]; then
        echo -e "${GREEN}✓ 所有测试通过${NC}"
        return 0
    else
        echo -e "${YELLOW}⚠ 部分测试失败${NC}"
        return 1
    fi
}

# ============================================================
# 主流程
# ============================================================

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}OpenPocket 全自动录音测试${NC}"
echo -e "${BLUE}========================================${NC}"

check_device
generate_test_audio
start_recording_maestro

# 等待录音界面稳定
sleep 3

inject_audio

# 继续录音 5 秒（让 VAD 和转写有时间处理）
sleep 5

stop_recording

verify_results
EXIT_CODE=$?

echo ""
echo "查看详细日志: cat $LOG_FILE"
echo "查看 JSON 报告: cat $RESULT_FILE"

exit $EXIT_CODE
