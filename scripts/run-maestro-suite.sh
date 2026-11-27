#!/usr/bin/env bash
# ═══════════════════════════════════════════════════════════════════
# Maestro 测试套件执行脚本
# ═══════════════════════════════════════════════════════════════════
#
# 用途：批量运行 Maestro 测试并生成报告
#
# 用法：
#   ./scripts/run-maestro-suite.sh [suite]
#
# 参数：
#   suite - 测试套件名称（可选）
#     - smoke: 冒烟测试（默认）
#     - master: 完整测试
#     - regression: 回归测试
#     - all: 运行所有测试
#
# 示例：
#   ./scripts/run-maestro-suite.sh smoke
#   ./scripts/run-maestro-suite.sh master
#   ./scripts/run-maestro-suite.sh all
#
# ═══════════════════════════════════════════════════════════════════

set -euo pipefail

# ═══════════════════════════════════════════════════════════════════
# 配置
# ═══════════════════════════════════════════════════════════════════

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
MAESTRO_DIR="$PROJECT_ROOT/.maestro"
LOGS_DIR="$PROJECT_ROOT/logs/maestro"
REPORT_DIR="$PROJECT_ROOT/test-reports"

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 测试套件定义
SUITE="${1:-smoke}"

# ═══════════════════════════════════════════════════════════════════
# 函数定义
# ═══════════════════════════════════════════════════════════════════

log_info() {
    echo -e "${BLUE}[INFO]${NC} $*"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $*"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $*"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $*"
}

# 创建必要的目录
setup_directories() {
    log_info "创建测试目录..."
    mkdir -p "$LOGS_DIR"
    mkdir -p "$REPORT_DIR"
}

# 检查前置条件
check_prerequisites() {
    log_info "检查前置条件..."
    
    # 检查 Maestro CLI
    if ! command -v maestro &> /dev/null; then
        log_error "Maestro CLI 未安装"
        log_info "请访问: https://maestro.mobile.dev/getting-started/installing-maestro"
        exit 1
    fi
    
    # 检查 ADB
    if ! command -v adb &> /dev/null; then
        log_error "ADB 未安装"
        exit 1
    fi
    
    # 检查设备连接
    local device_count
    device_count=$(adb devices | grep -c "device$" || true)
    if [ "$device_count" -eq 0 ]; then
        log_error "没有连接的 Android 设备或模拟器"
        log_info "请启动模拟器或连接设备"
        exit 1
    fi
    
    log_success "前置条件检查通过"
}

# 运行单个测试
run_test() {
    local test_file="$1"
    local test_name
    test_name=$(basename "$test_file" .yaml)
    
    log_info "运行测试: $test_name"
    
    local start_time
    start_time=$(date +%s)
    
    local result=0
    if maestro test "$test_file" \
        --format junit \
        --output "$REPORT_DIR/${test_name}-report.xml" \
        > "$LOGS_DIR/${test_name}.log" 2>&1; then
        local end_time
        end_time=$(date +%s)
        local duration=$((end_time - start_time))
        log_success "✓ $test_name 通过 (${duration}s)"
    else
        result=$?
        local end_time
        end_time=$(date +%s)
        local duration=$((end_time - start_time))
        log_error "✗ $test_name 失败 (${duration}s)"
        log_info "查看日志: $LOGS_DIR/${test_name}.log"
    fi
    
    return $result
}

# 运行冒烟测试
run_smoke_tests() {
    log_info "开始运行冒烟测试套件..."
    
    local tests=(
        "$MAESTRO_DIR/smoke-test-suite.yaml"
    )
    
    local passed=0
    local failed=0
    
    for test in "${tests[@]}"; do
        if [ -f "$test" ]; then
            if run_test "$test"; then
                ((passed++))
            else
                ((failed++))
            fi
        else
            log_warning "测试文件不存在: $test"
        fi
    done
    
    echo ""
    log_info "冒烟测试完成: $passed 通过, $failed 失败"
    
    return "$failed"
}

# 运行完整测试
run_master_tests() {
    log_info "开始运行完整测试套件..."
    
    local tests=(
        "$MAESTRO_DIR/master-test-suite.yaml"
    )
    
    local passed=0
    local failed=0
    
    for test in "${tests[@]}"; do
        if [ -f "$test" ]; then
            if run_test "$test"; then
                ((passed++))
            else
                ((failed++))
            fi
        else
            log_warning "测试文件不存在: $test"
        fi
    done
    
    echo ""
    log_info "完整测试完成: $passed 通过, $failed 失败"
    
    return "$failed"
}

# 运行回归测试
run_regression_tests() {
    log_info "开始运行回归测试套件..."
    
    local tests=(
        "$MAESTRO_DIR/notes-crud.yaml"
        "$MAESTRO_DIR/tasks-crud.yaml"
        "$MAESTRO_DIR/email-browse.yaml"
        "$MAESTRO_DIR/flashcards-write.yaml"
    )
    
    local passed=0
    local failed=0
    
    for test in "${tests[@]}"; do
        if [ -f "$test" ]; then
            if run_test "$test"; then
                ((passed++))
            else
                ((failed++))
            fi
        else
            log_warning "测试文件不存在: $test"
        fi
    done
    
    echo ""
    log_info "回归测试完成: $passed 通过, $failed 失败"
    
    return "$failed"
}

# 运行所有测试
run_all_tests() {
    log_info "开始运行所有测试..."
    
    local total_failed=0
    
    run_smoke_tests || total_failed=$((total_failed + $?))
    echo ""
    
    run_master_tests || total_failed=$((total_failed + $?))
    echo ""
    
    run_regression_tests || total_failed=$((total_failed + $?))
    
    return "$total_failed"
}

# 生成测试报告
generate_report() {
    log_info "生成测试报告..."
    
    local report_file="$REPORT_DIR/test-summary-$(date +%Y%m%d-%H%M%S).txt"
    
    {
        echo "═══════════════════════════════════════════════════════════════════"
        echo "OpenPocket Maestro 测试报告"
        echo "═══════════════════════════════════════════════════════════════════"
        echo ""
        echo "日期: $(date '+%Y-%m-%d %H:%M:%S')"
        echo "套件: $SUITE"
        echo ""
        echo "测试结果文件:"
        ls -1 "$REPORT_DIR"/*.xml 2>/dev/null || echo "  (无)"
        echo ""
        echo "日志文件:"
        ls -1 "$LOGS_DIR"/*.log 2>/dev/null || echo "  (无)"
        echo ""
        echo "═══════════════════════════════════════════════════════════════════"
    } > "$report_file"
    
    cat "$report_file"
    log_success "报告已保存: $report_file"
}

# ═══════════════════════════════════════════════════════════════════
# 主流程
# ═══════════════════════════════════════════════════════════════════

main() {
    echo ""
    echo "═══════════════════════════════════════════════════════════════════"
    echo "OpenPocket Maestro 测试套件"
    echo "═══════════════════════════════════════════════════════════════════"
    echo ""
    
    setup_directories
    check_prerequisites
    
    echo ""
    log_info "执行测试套件: $SUITE"
    echo ""
    
    local exit_code=0
    
    case "$SUITE" in
        smoke)
            run_smoke_tests || exit_code=$?
            ;;
        master)
            run_master_tests || exit_code=$?
            ;;
        regression)
            run_regression_tests || exit_code=$?
            ;;
        all)
            run_all_tests || exit_code=$?
            ;;
        *)
            log_error "未知的测试套件: $SUITE"
            log_info "可用套件: smoke, master, regression, all"
            exit 1
            ;;
    esac
    
    echo ""
    generate_report
    
    echo ""
    if [ $exit_code -eq 0 ]; then
        log_success "所有测试通过 ✓"
    else
        log_error "部分测试失败 (失败数: $exit_code)"
    fi
    
    echo ""
    
    exit "$exit_code"
}

main "$@"
