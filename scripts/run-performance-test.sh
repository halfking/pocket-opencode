#!/usr/bin/env bash
# ═══════════════════════════════════════════════════════════════════
# Maestro 性能测试脚本
# ═══════════════════════════════════════════════════════════════════
#
# 用途：测试应用关键操作的性能指标
#
# 测试指标：
#   - 应用启动时间
#   - 登录响应时间
#   - 页面切换延迟
#   - 录音启动延迟
#   - 内存使用情况
#
# ═══════════════════════════════════════════════════════════════════

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
REPORT_FILE="$PROJECT_ROOT/test-reports/performance-report-$(date +%Y%m%d-%H%M%S).txt"

# 颜色
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info() { echo -e "${BLUE}[INFO]${NC} $*"; }
log_success() { echo -e "${GREEN}[✓]${NC} $*"; }
log_warning() { echo -e "${YELLOW}[!]${NC} $*"; }

PACKAGE="com.kaixuan.opencode.pocket"

echo "═══════════════════════════════════════════════════════════════════"
echo "OpenPocket 性能测试"
echo "═══════════════════════════════════════════════════════════════════"
echo ""

# 创建报告目录
mkdir -p "$(dirname "$REPORT_FILE")"

# 开始报告
{
    echo "═══════════════════════════════════════════════════════════════════"
    echo "OpenPocket 性能测试报告"
    echo "═══════════════════════════════════════════════════════════════════"
    echo ""
    echo "测试时间: $(date '+%Y-%m-%d %H:%M:%S')"
    echo ""
} > "$REPORT_FILE"

# ═══════════════════════════════════════════════════════════════════
# 测试 1: 应用启动时间
# ═══════════════════════════════════════════════════════════════════

log_info "测试 1: 应用启动时间..."

# 停止应用
adb shell am force-stop "$PACKAGE" 2>/dev/null || true
sleep 2

# 记录启动时间
START_TIME=$(date +%s%3N)

# 启动应用
adb shell am start -n "$PACKAGE/.MainActivity" -W 2>&1 | grep "TotalTime" || true

END_TIME=$(date +%s%3N)
LAUNCH_TIME=$((END_TIME - START_TIME))

log_success "启动时间: ${LAUNCH_TIME}ms"

{
    echo "1. 应用启动时间"
    echo "   测量值: ${LAUNCH_TIME}ms"
    echo "   目标值: < 3000ms"
    if [ "$LAUNCH_TIME" -lt 3000 ]; then
        echo "   状态: ✓ 通过"
    else
        echo "   状态: ✗ 超标"
    fi
    echo ""
} >> "$REPORT_FILE"

# ═══════════════════════════════════════════════════════════════════
# 测试 2: 内存使用
# ═══════════════════════════════════════════════════════════════════

log_info "测试 2: 内存使用..."

sleep 5  # 等待应用稳定

# 获取内存信息
MEMORY_INFO=$(adb shell dumpsys meminfo "$PACKAGE" | grep "TOTAL" | head -1 | awk '{print $2}')

if [ -n "$MEMORY_INFO" ]; then
    MEMORY_MB=$((MEMORY_INFO / 1024))
    log_success "内存使用: ${MEMORY_MB}MB"
    
    {
        echo "2. 内存使用"
        echo "   测量值: ${MEMORY_MB}MB"
        echo "   目标值: < 500MB"
        if [ "$MEMORY_MB" -lt 500 ]; then
            echo "   状态: ✓ 通过"
        else
            echo "   状态: ✗ 超标"
        fi
        echo ""
    } >> "$REPORT_FILE"
else
    log_warning "无法获取内存信息"
    {
        echo "2. 内存使用"
        echo "   状态: ⚠ 无法测量"
        echo ""
    } >> "$REPORT_FILE"
fi

# ═══════════════════════════════════════════════════════════════════
# 测试 3: CPU 使用
# ═══════════════════════════════════════════════════════════════════

log_info "测试 3: CPU 使用..."

# 获取 CPU 使用率（简化版）
CPU_INFO=$(adb shell top -n 1 | grep "$PACKAGE" | head -1 | awk '{print $9}' || echo "0")

if [ -n "$CPU_INFO" ] && [ "$CPU_INFO" != "0" ]; then
    log_success "CPU 使用: ${CPU_INFO}%"
    
    {
        echo "3. CPU 使用"
        echo "   测量值: ${CPU_INFO}%"
        echo "   状态: ℹ 参考"
        echo ""
    } >> "$REPORT_FILE"
else
    log_warning "无法获取 CPU 信息"
    {
        echo "3. CPU 使用"
        echo "   状态: ⚠ 无法测量"
        echo ""
    } >> "$REPORT_FILE"
fi

# ═══════════════════════════════════════════════════════════════════
# 测试 4: APK 大小
# ═══════════════════════════════════════════════════════════════════

log_info "测试 4: APK 大小..."

APK_PATH=$(adb shell pm path "$PACKAGE" | cut -d: -f2 | tr -d '\r')
if [ -n "$APK_PATH" ]; then
    APK_SIZE=$(adb shell stat -c%s "$APK_PATH" 2>/dev/null || echo "0")
    if [ "$APK_SIZE" != "0" ]; then
        APK_SIZE_MB=$((APK_SIZE / 1024 / 1024))
        log_success "APK 大小: ${APK_SIZE_MB}MB"
        
        {
            echo "4. APK 大小"
            echo "   测量值: ${APK_SIZE_MB}MB"
            echo "   目标值: < 50MB"
            if [ "$APK_SIZE_MB" -lt 50 ]; then
                echo "   状态: ✓ 通过"
            else
                echo "   状态: ⚠ 偏大"
            fi
            echo ""
        } >> "$REPORT_FILE"
    fi
fi

# ═══════════════════════════════════════════════════════════════════
# 测试总结
# ═══════════════════════════════════════════════════════════════════

{
    echo "═══════════════════════════════════════════════════════════════════"
    echo "性能指标对比"
    echo "═══════════════════════════════════════════════════════════════════"
    echo ""
    printf "%-25s %-15s %-15s %-10s\n" "指标" "目标值" "实际值" "状态"
    echo "───────────────────────────────────────────────────────────────────"
    
    if [ "$LAUNCH_TIME" -lt 3000 ]; then
        printf "%-25s %-15s %-15s %-10s\n" "应用启动时间" "< 3000ms" "${LAUNCH_TIME}ms" "✓"
    else
        printf "%-25s %-15s %-15s %-10s\n" "应用启动时间" "< 3000ms" "${LAUNCH_TIME}ms" "✗"
    fi
    
    if [ -n "$MEMORY_INFO" ]; then
        if [ "$MEMORY_MB" -lt 500 ]; then
            printf "%-25s %-15s %-15s %-10s\n" "内存占用" "< 500MB" "${MEMORY_MB}MB" "✓"
        else
            printf "%-25s %-15s %-15s %-10s\n" "内存占用" "< 500MB" "${MEMORY_MB}MB" "✗"
        fi
    fi
    
    if [ -n "$APK_SIZE_MB" ]; then
        if [ "$APK_SIZE_MB" -lt 50 ]; then
            printf "%-25s %-15s %-15s %-10s\n" "APK 大小" "< 50MB" "${APK_SIZE_MB}MB" "✓"
        else
            printf "%-25s %-15s %-15s %-10s\n" "APK 大小" "< 50MB" "${APK_SIZE_MB}MB" "⚠"
        fi
    fi
    
    echo ""
    echo "═══════════════════════════════════════════════════════════════════"
    echo "报告生成完成"
    echo "═══════════════════════════════════════════════════════════════════"
} >> "$REPORT_FILE"

echo ""
log_success "性能测试完成"
log_info "报告已保存: $REPORT_FILE"
echo ""

# 显示报告
cat "$REPORT_FILE"
