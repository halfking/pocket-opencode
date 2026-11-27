#!/usr/bin/env bash
# ═══════════════════════════════════════════════════════════════════
# Maestro 测试快速验证脚本
# ═══════════════════════════════════════════════════════════════════
#
# 用途：快速验证 Maestro 测试环境和应用状态
#
# ═══════════════════════════════════════════════════════════════════

set -euo pipefail

# 颜色
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${BLUE}[INFO]${NC} $*"; }
log_success() { echo -e "${GREEN}[✓]${NC} $*"; }
log_error() { echo -e "${RED}[✗]${NC} $*"; }
log_warning() { echo -e "${YELLOW}[!]${NC} $*"; }

echo "═══════════════════════════════════════════════════════════════════"
echo "OpenPocket Maestro 环境检查"
echo "═══════════════════════════════════════════════════════════════════"
echo ""

# 1. 检查 Maestro
echo "1. 检查 Maestro CLI"
if command -v maestro &> /dev/null; then
    MAESTRO_VERSION=$(maestro --version 2>&1 || echo "unknown")
    log_success "Maestro 已安装: $MAESTRO_VERSION"
else
    log_error "Maestro 未安装"
    echo "   安装命令: curl -Ls \"https://get.maestro.mobile.dev\" | bash"
    exit 1
fi
echo ""

# 2. 检查 ADB
echo "2. 检查 ADB"
if command -v adb &> /dev/null; then
    ADB_VERSION=$(adb --version | head -n1)
    log_success "ADB 已安装: $ADB_VERSION"
else
    log_error "ADB 未安装"
    exit 1
fi
echo ""

# 3. 检查设备
echo "3. 检查 Android 设备"
DEVICE_COUNT=$(adb devices | grep -c "device$" || echo "0")
if [ "$DEVICE_COUNT" -gt 0 ]; then
    log_success "发现 $DEVICE_COUNT 个设备/模拟器"
    adb devices | grep "device$" | while read -r line; do
        echo "   - $line"
    done
else
    log_error "没有连接的设备"
    echo "   请启动模拟器或连接物理设备"
    exit 1
fi
echo ""

# 4. 检查应用
echo "4. 检查应用安装"
if adb shell pm list packages | grep -q "com.kaixuan.opencode.pocket"; then
    log_success "OpenPocket 已安装"
    
    # 获取版本信息
    VERSION_NAME=$(adb shell dumpsys package com.kaixuan.opencode.pocket | grep versionName | head -n1 | awk '{print $1}')
    if [ -n "$VERSION_NAME" ]; then
        echo "   $VERSION_NAME"
    fi
else
    log_warning "OpenPocket 未安装"
    echo "   请先安装 APK"
fi
echo ""

# 5. 检查后端连接
echo "5. 检查后端服务"
if adb reverse --list | grep -q "tcp:18099"; then
    log_success "端口转发已配置: tcp:18099"
else
    log_warning "端口转发未配置"
    echo "   执行: adb reverse tcp:18099 tcp:8088"
fi

# 尝试访问后端
if adb shell curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:18099/api/health 2>/dev/null | grep -q "200"; then
    log_success "后端服务可访问"
else
    log_warning "后端服务不可访问"
    echo "   请确认后端服务已启动"
fi
echo ""

# 6. 检查环境变量
echo "6. 检查环境变量"
if [ -n "${POCKET_MASTER:-}" ]; then
    log_success "POCKET_MASTER 已设置"
else
    log_warning "POCKET_MASTER 未设置"
    echo "   export POCKET_MASTER=\"12345678\""
fi

if [ -n "${POCKET_DEV_PASS:-}" ]; then
    log_success "POCKET_DEV_PASS 已设置"
else
    log_warning "POCKET_DEV_PASS 未设置"
    echo "   export POCKET_DEV_PASS=\"your_password\""
fi
echo ""

# 7. 检查测试文件
echo "7. 检查测试文件"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
MAESTRO_DIR="$PROJECT_ROOT/.maestro"

if [ -d "$MAESTRO_DIR" ]; then
    TEST_COUNT=$(find "$MAESTRO_DIR" -name "*.yaml" -type f | wc -l | tr -d ' ')
    log_success "找到 $TEST_COUNT 个测试文件"
    
    # 列出测试套件
    echo ""
    echo "   可用测试套件:"
    for suite in smoke-test-suite master-test-suite regression-test-suite; do
        if [ -f "$MAESTRO_DIR/${suite}.yaml" ]; then
            echo "   ✓ $suite"
        else
            echo "   ✗ $suite (缺失)"
        fi
    done
else
    log_error ".maestro 目录不存在"
    exit 1
fi
echo ""

# 8. 总结
echo "═══════════════════════════════════════════════════════════════════"
echo "环境检查完成"
echo "═══════════════════════════════════════════════════════════════════"
echo ""
echo "快速开始："
echo "  1. 运行冒烟测试:"
echo "     ./scripts/run-maestro-suite.sh smoke"
echo ""
echo "  2. 运行完整测试:"
echo "     ./scripts/run-maestro-suite.sh master"
echo ""
echo "  3. 运行回归测试:"
echo "     ./scripts/run-maestro-suite.sh regression"
echo ""
