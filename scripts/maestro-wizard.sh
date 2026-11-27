#!/usr/bin/env bash
# ═══════════════════════════════════════════════════════════════════
# Maestro 一键启动脚本
# ═══════════════════════════════════════════════════════════════════
#
# 用途：一键完成环境配置和测试执行
#
# ═══════════════════════════════════════════════════════════════════

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# 颜色
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

log_info() { echo -e "${BLUE}[INFO]${NC} $*"; }
log_success() { echo -e "${GREEN}[✓]${NC} $*"; }
log_warning() { echo -e "${YELLOW}[!]${NC} $*"; }
log_error() { echo -e "${RED}[✗]${NC} $*"; }
log_step() { echo -e "${CYAN}[STEP]${NC} $*"; }

clear

cat << "EOF"
╔═══════════════════════════════════════════════════════════════════╗
║                                                                   ║
║            OpenPocket Maestro 一键启动向导                        ║
║                                                                   ║
║            快速配置环境并开始测试                                  ║
║                                                                   ║
╚═══════════════════════════════════════════════════════════════════╝

EOF

echo ""

# ═══════════════════════════════════════════════════════════════════
# 步骤 1: 环境检查
# ═══════════════════════════════════════════════════════════════════

log_step "步骤 1/5: 环境检查"
echo ""

# 检查 Maestro
if ! command -v maestro &> /dev/null; then
    log_error "Maestro 未安装"
    echo ""
    echo "安装方法："
    echo "  curl -Ls \"https://get.maestro.mobile.dev\" | bash"
    echo ""
    exit 1
fi
log_success "Maestro 已安装"

# 检查 ADB
if ! command -v adb &> /dev/null; then
    log_error "ADB 未安装"
    exit 1
fi
log_success "ADB 已安装"

# 检查设备
DEVICE_COUNT=$(adb devices | grep -c "device$" || echo "0")
if [ "$DEVICE_COUNT" -eq 0 ]; then
    log_error "没有连接的设备"
    echo ""
    echo "请确保："
    echo "  1. 设备已连接或模拟器已启动"
    echo "  2. USB 调试已开启"
    echo "  3. 运行 'adb devices' 可以看到设备"
    echo ""
    exit 1
fi
log_success "找到 $DEVICE_COUNT 个设备"

echo ""

# ═══════════════════════════════════════════════════════════════════
# 步骤 2: 配置环境变量
# ═══════════════════════════════════════════════════════════════════

log_step "步骤 2/5: 配置环境变量"
echo ""

if [ -z "${POCKET_MASTER:-}" ]; then
    log_warning "POCKET_MASTER 未设置"
    read -p "请输入主密码 (默认: 12345678): " master_pass
    master_pass=${master_pass:-12345678}
    export POCKET_MASTER="$master_pass"
    log_success "已设置 POCKET_MASTER"
else
    log_success "POCKET_MASTER 已设置"
fi

if [ -z "${POCKET_DEV_PASS:-}" ]; then
    log_warning "POCKET_DEV_PASS 未设置"
    read -p "请输入测试账号密码 (默认: Veritrans&9527): " dev_pass
    dev_pass=${dev_pass:-"Veritrans&9527"}
    export POCKET_DEV_PASS="$dev_pass"
    log_success "已设置 POCKET_DEV_PASS"
else
    log_success "POCKET_DEV_PASS 已设置"
fi

echo ""

# ═══════════════════════════════════════════════════════════════════
# 步骤 3: 配置端口转发
# ═══════════════════════════════════════════════════════════════════

log_step "步骤 3/5: 配置端口转发"
echo ""

log_info "配置端口转发 tcp:18099 -> tcp:8088..."
adb reverse tcp:18099 tcp:8088

if adb reverse --list | grep -q "tcp:18099"; then
    log_success "端口转发配置成功"
else
    log_warning "端口转发可能未成功，但继续执行"
fi

echo ""

# ═══════════════════════════════════════════════════════════════════
# 步骤 4: 检查应用
# ═══════════════════════════════════════════════════════════════════

log_step "步骤 4/5: 检查应用安装"
echo ""

if adb shell pm list packages | grep -q "com.kaixuan.opencode.pocket"; then
    log_success "OpenPocket 已安装"
    
    # 获取版本
    VERSION=$(adb shell dumpsys package com.kaixuan.opencode.pocket | grep versionName | head -n1 | awk '{print $1}' || echo "")
    if [ -n "$VERSION" ]; then
        echo "   $VERSION"
    fi
else
    log_warning "OpenPocket 未安装"
    echo ""
    read -p "是否需要安装 APK? (y/N): " install_apk
    if [[ "$install_apk" =~ ^[Yy]$ ]]; then
        APK_PATH="$PROJECT_ROOT/frontend/android/app/build/outputs/apk/debug/app-debug.apk"
        if [ -f "$APK_PATH" ]; then
            log_info "安装 APK..."
            adb install -r "$APK_PATH"
            log_success "APK 安装完成"
        else
            log_error "APK 文件不存在: $APK_PATH"
            log_info "请先构建 APK: cd frontend/android && ./gradlew assembleDebug"
            exit 1
        fi
    fi
fi

echo ""

# ═══════════════════════════════════════════════════════════════════
# 步骤 5: 选择测试套件
# ═══════════════════════════════════════════════════════════════════

log_step "步骤 5/5: 选择测试套件"
echo ""

echo "请选择要运行的测试："
echo ""
echo "  1) 冒烟测试 (Smoke)      - 快速验证核心功能 (3-5分钟)"
echo "  2) 回归测试 (Regression)  - PR验证 (10-12分钟)"
echo "  3) 完整测试 (Master)      - 发布前验证 (15-20分钟)"
echo "  4) 所有测试 (All)         - 完整验证 (30-40分钟)"
echo "  5) 性能测试 (Performance) - 性能基准 (2-3分钟)"
echo "  6) 自定义测试"
echo ""

read -p "请选择 [1-6] (默认: 1): " choice
choice=${choice:-1}

SUITE=""
case $choice in
    1)
        SUITE="smoke"
        ;;
    2)
        SUITE="regression"
        ;;
    3)
        SUITE="master"
        ;;
    4)
        SUITE="all"
        ;;
    5)
        log_info "运行性能测试..."
        "$SCRIPT_DIR/run-performance-test.sh"
        exit 0
        ;;
    6)
        echo ""
        echo "可用的测试文件："
        ls -1 "$PROJECT_ROOT/.maestro/"*-test*.yaml | xargs -n1 basename
        echo ""
        read -p "请输入测试文件名: " custom_test
        if [ -f "$PROJECT_ROOT/.maestro/$custom_test" ]; then
            log_info "运行自定义测试: $custom_test"
            maestro test "$PROJECT_ROOT/.maestro/$custom_test"
        else
            log_error "测试文件不存在: $custom_test"
            exit 1
        fi
        exit 0
        ;;
    *)
        log_error "无效的选择"
        exit 1
        ;;
esac

echo ""

# ═══════════════════════════════════════════════════════════════════
# 执行测试
# ═══════════════════════════════════════════════════════════════════

cat << EOF
╔═══════════════════════════════════════════════════════════════════╗
║                                                                   ║
║                      开始执行测试                                  ║
║                                                                   ║
╚═══════════════════════════════════════════════════════════════════╝

EOF

log_info "测试套件: $SUITE"
log_info "开始时间: $(date '+%Y-%m-%d %H:%M:%S')"
echo ""

START_TIME=$(date +%s)

# 运行测试
"$SCRIPT_DIR/run-maestro-suite.sh" "$SUITE"
EXIT_CODE=$?

END_TIME=$(date +%s)
DURATION=$((END_TIME - START_TIME))
MINUTES=$((DURATION / 60))
SECONDS=$((DURATION % 60))

echo ""

# ═══════════════════════════════════════════════════════════════════
# 测试完成
# ═══════════════════════════════════════════════════════════════════

cat << EOF
╔═══════════════════════════════════════════════════════════════════╗
║                                                                   ║
║                      测试执行完成                                  ║
║                                                                   ║
╚═══════════════════════════════════════════════════════════════════╝

EOF

log_info "结束时间: $(date '+%Y-%m-%d %H:%M:%S')"
log_info "执行时长: ${MINUTES}分${SECONDS}秒"
echo ""

if [ $EXIT_CODE -eq 0 ]; then
    log_success "所有测试通过 ✓"
    echo ""
    echo "📊 查看结果："
    echo "  - 测试报告: cat test-reports/test-summary-*.txt"
    echo "  - 测试截图: open logs/maestro/"
    echo "  - JUnit XML: ls test-reports/*.xml"
else
    log_error "部分测试失败"
    echo ""
    echo "🔍 排查建议："
    echo "  1. 查看日志: cat logs/maestro/*.log"
    echo "  2. 查看截图: open logs/maestro/"
    echo "  3. 重新运行: $0"
fi

echo ""

# 询问是否生成 HTML 报告
read -p "是否生成 HTML 报告? (Y/n): " gen_report
gen_report=${gen_report:-Y}

if [[ "$gen_report" =~ ^[Yy]$ ]]; then
    echo ""
    log_info "生成 HTML 报告..."
    "$SCRIPT_DIR/generate-maestro-report.sh"
fi

echo ""
echo "感谢使用 OpenPocket Maestro 测试框架！"
echo ""

exit $EXIT_CODE
