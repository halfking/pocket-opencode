#!/bin/bash
# analyze-recording-logs.sh
# 分析现有的录音日志，无需运行应用

set -e

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

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
echo -e "${BLUE}OpenPocket 录音日志分析工具${NC}"
echo -e "${BLUE}========================================${NC}"

# 检查设备
DEVICE_ID="${1:-$(adb devices | grep -w device | head -1 | awk '{print $1}')}"

if [ -z "$DEVICE_ID" ] || [ "$DEVICE_ID" = "List" ]; then
    print_error "未检测到设备"
    echo ""
    echo "用法: $0 [设备ID]"
    echo "  或直接连接一个设备"
    exit 1
fi

print_step "连接设备: $DEVICE_ID"
print_success "设备已连接"

# 收集最近的日志（最近 5 分钟）
print_step "收集录音相关日志"

LOG_FILE="/tmp/openpocket-log-analysis-$(date +%Y%m%d-%H%M%S).log"
RESULT_FILE="/tmp/openpocket-log-result-$(date +%Y%m%d-%H%M%S).json"

echo "  提取最近 5 分钟的日志..."
adb -s "$DEVICE_ID" logcat -t 500 | grep -E "VAD|Paraformer|MeetingRecord|RecordService|openpocket" > "$LOG_FILE" 2>/dev/null || true

LOG_LINES=$(wc -l < "$LOG_FILE" | tr -d ' ')
print_success "日志已收集: $LOG_LINES 行"

if [ "$LOG_LINES" -eq 0 ]; then
    print_warning "未找到相关日志"
    echo ""
    echo "建议："
    echo "  1. 确保应用正在运行"
    echo "  2. 尝试进行一次录音操作"
    echo "  3. 重新运行此脚本"
    exit 0
fi

# 分析日志
print_step "分析日志内容"

PASS_COUNT=0
FAIL_COUNT=0
FINDINGS=()

# Test 1: VAD 初始化
echo -n "  Test 1: VAD 初始化..."
if grep -q "VAD.*init\|VAD enabled\|VAD.*start" "$LOG_FILE"; then
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
if grep -q "VAD detected\|speech.*end\|endpoint detected" "$LOG_FILE"; then
    echo -e " ${GREEN}✓${NC}"
    ((PASS_COUNT++))
    VAD_DETECT=true
    FINDINGS+=("✓ VAD 检测到语音端点")
else
    echo -e " ${YELLOW}⚠${NC}"
    ((FAIL_COUNT++))
    VAD_DETECT=false
    FINDINGS+=("⚠ VAD 未检测到语音端点（可能无音频输入）")
fi

# Test 3: Paraformer 转写
echo -n "  Test 3: Paraformer 转写..."
if grep -q "Paraformer\|transcription\|recognized" "$LOG_FILE"; then
    echo -e " ${GREEN}✓${NC}"
    ((PASS_COUNT++))
    PARAFORMER=true
    FINDINGS+=("✓ Paraformer 转写已执行")
    
    # 提取转写文本示例
    TRANSCRIPTION=$(grep -i "transcription\|recognized" "$LOG_FILE" | head -1 | sed 's/.*: //')
    if [ -n "$TRANSCRIPTION" ]; then
        FINDINGS+=("  → 示例: $TRANSCRIPTION")
    fi
else
    echo -e " ${YELLOW}⚠${NC}"
    ((FAIL_COUNT++))
    PARAFORMER=false
    FINDINGS+=("⚠ Paraformer 转写未检测到")
fi

# Test 4: 无崩溃
echo -n "  Test 4: 应用稳定性..."
if grep -q "FATAL\|AndroidRuntime.*FATAL\|CRASH" "$LOG_FILE"; then
    echo -e " ${RED}✗${NC}"
    ((FAIL_COUNT++))
    NO_CRASH=false
    FINDINGS+=("✗ 检测到应用崩溃")
    
    # 提取崩溃信息
    CRASH_INFO=$(grep -A 2 "FATAL\|CRASH" "$LOG_FILE" | head -3)
    FINDINGS+=("  → $CRASH_INFO")
else
    echo -e " ${GREEN}✓${NC}"
    ((PASS_COUNT++))
    NO_CRASH=true
    FINDINGS+=("✓ 应用运行稳定，无崩溃")
fi

# Test 5: 录音服务
echo -n "  Test 5: 录音服务状态..."
if grep -q "MeetingRecord\|RecordService\|recording.*start" "$LOG_FILE"; then
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

# 额外分析：性能指标
print_step "性能指标分析"

# VAD 延迟
if grep -q "VAD.*latency\|VAD.*delay" "$LOG_FILE"; then
    VAD_LATENCY=$(grep "VAD.*latency\|VAD.*delay" "$LOG_FILE" | head -1)
    echo "  首字延迟: $VAD_LATENCY"
else
    echo "  首字延迟: 未找到相关数据"
fi

# 内存使用
if grep -q "memory\|heap" "$LOG_FILE"; then
    MEMORY=$(grep "memory\|heap" "$LOG_FILE" | tail -1)
    echo "  内存状态: $MEMORY"
else
    echo "  内存状态: 未找到相关数据"
fi

# 生成 JSON 报告
cat > "$RESULT_FILE" <<EOF
{
  "timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "device": "$DEVICE_ID",
  "log_lines": $LOG_LINES,
  "total_tests": $((PASS_COUNT + FAIL_COUNT)),
  "passed": $PASS_COUNT,
  "failed": $FAIL_COUNT,
  "log_file": "$LOG_FILE",
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
echo "  日志文件: $LOG_FILE"
echo "  JSON 报告: $RESULT_FILE"

if [ $FAIL_COUNT -eq 0 ]; then
    echo ""
    echo -e "${GREEN}✓ 所有测试通过${NC}"
    EXIT_CODE=0
elif [ $PASS_COUNT -gt $FAIL_COUNT ]; then
    echo ""
    echo -e "${YELLOW}⚠ 大部分测试通过，但有失败项${NC}"
    EXIT_CODE=0
else
    echo ""
    echo -e "${RED}✗ 多个测试失败${NC}"
    EXIT_CODE=1
fi

echo ""
echo "查看详细日志:"
echo "  cat $LOG_FILE"
echo ""
echo "查看 JSON 报告:"
echo "  cat $RESULT_FILE | jq ."
echo ""
echo "查看关键日志行:"
echo "  grep -C 2 'VAD' $LOG_FILE"
echo "  grep -C 2 'Paraformer' $LOG_FILE"
echo ""

exit $EXIT_CODE
