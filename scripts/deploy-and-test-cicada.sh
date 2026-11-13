#!/bin/bash
# deploy-and-test-cicada.sh
# Cicada Phase A 真机部署和测试自动化脚本

set -e

# 颜色定义
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 工作目录
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_ROOT"

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}Cicada Phase A 真机部署测试脚本${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""

# 步骤计数
STEP=1

# ============================================================
# 函数定义
# ============================================================

print_step() {
    echo ""
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    echo -e "${BLUE}步骤 $STEP: $1${NC}"
    echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
    ((STEP++))
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

check_command() {
    if command -v "$1" &> /dev/null; then
        print_success "$1 已安装"
        return 0
    else
        print_error "$1 未安装"
        return 1
    fi
}

# ============================================================
# 步骤 1: 环境检查
# ============================================================

print_step "环境检查"

# 检查必要工具
MISSING_TOOLS=0

if ! check_command "adb"; then
    echo "  请安装 Android SDK Platform-Tools"
    ((MISSING_TOOLS++))
fi

if ! check_command "node"; then
    echo "  请安装 Node.js >= 22.22.2"
    ((MISSING_TOOLS++))
fi

if ! check_command "npm"; then
    echo "  请安装 npm"
    ((MISSING_TOOLS++))
fi

if ! check_command "curl"; then
    echo "  curl 未找到（通常系统自带）"
    ((MISSING_TOOLS++))
fi

if [ $MISSING_TOOLS -gt 0 ]; then
    print_error "缺少 $MISSING_TOOLS 个必要工具，请先安装"
    exit 1
fi

print_success "所有必要工具已就绪"

# ============================================================
# 步骤 2: 检查设备连接
# ============================================================

print_step "检查设备连接"

DEVICE_COUNT=$(adb devices | grep -w "device" | wc -l)

if [ "$DEVICE_COUNT" -eq 0 ]; then
    print_error "未检测到连接的设备"
    echo ""
    echo "请执行以下操作："
    echo "  1. 用 USB 连接真机"
    echo "  2. 在手机上启用 USB 调试"
    echo "  3. 确认授权此计算机调试"
    echo "  4. 运行 'adb devices' 确认连接"
    echo ""
    exit 1
elif [ "$DEVICE_COUNT" -eq 1 ]; then
    DEVICE_ID=$(adb devices | grep -w "device" | awk '{print $1}')
    print_success "检测到设备: $DEVICE_ID"
else
    print_warning "检测到多个设备，将使用第一个"
    DEVICE_ID=$(adb devices | grep -w "device" | head -1 | awk '{print $1}')
    echo "  使用设备: $DEVICE_ID"
fi

# 获取设备信息
DEVICE_MODEL=$(adb -s "$DEVICE_ID" shell getprop ro.product.model | tr -d '\r')
ANDROID_VERSION=$(adb -s "$DEVICE_ID" shell getprop ro.build.version.release | tr -d '\r')
API_LEVEL=$(adb -s "$DEVICE_ID" shell getprop ro.build.version.sdk | tr -d '\r')

echo "  型号: $DEVICE_MODEL"
echo "  Android 版本: $ANDROID_VERSION (API $API_LEVEL)"

# ============================================================
# 步骤 3: 检查后端服务
# ============================================================

print_step "检查后端服务"

if curl -s -f http://localhost:8080/health > /dev/null 2>&1; then
    print_success "后端服务运行正常 (localhost:8080)"
else
    print_warning "后端服务未响应"
    echo ""
    echo "请确保 memora/pocketd 服务正在运行："
    echo "  cd ../ai-platform/memora"
    echo "  ./pocketd"
    echo ""
    read -p "是否继续？(y/n) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        exit 1
    fi
fi

# ============================================================
# 步骤 4: 检查 APK
# ============================================================

print_step "检查 APK 文件"

APK_PATH="frontend/android/app/build/outputs/apk/debug/app-debug.apk"

if [ -f "$APK_PATH" ]; then
    APK_SIZE=$(du -h "$APK_PATH" | cut -f1)
    APK_DATE=$(stat -f "%Sm" -t "%Y-%m-%d %H:%M" "$APK_PATH" 2>/dev/null || stat -c "%y" "$APK_PATH" 2>/dev/null | cut -d'.' -f1)
    print_success "找到 APK: $APK_SIZE"
    echo "  路径: $APK_PATH"
    echo "  构建时间: $APK_DATE"
    
    read -p "是否使用现有 APK？(y/n) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        REBUILD=true
    else
        REBUILD=false
    fi
else
    print_warning "未找到 APK 文件"
    REBUILD=true
fi

# ============================================================
# 步骤 5: 构建 APK（如果需要）
# ============================================================

if [ "$REBUILD" = true ]; then
    print_step "构建 APK"
    
    cd frontend
    
    echo "  → 安装依赖..."
    npm install --silent || {
        print_error "依赖安装失败"
        exit 1
    }
    
    echo "  → 构建前端..."
    npm run build:fast || {
        print_error "前端构建失败"
        exit 1
    }
    
    echo "  → 同步到 Capacitor..."
    npx cap sync android || {
        print_error "Capacitor 同步失败"
        exit 1
    }
    
    echo "  → 构建 Android APK..."
    cd android
    ./gradlew assembleDebug || {
        print_error "Android 构建失败"
        exit 1
    }
    
    cd "$PROJECT_ROOT"
    print_success "APK 构建完成"
fi

# ============================================================
# 步骤 6: 安装 APK
# ============================================================

print_step "安装 APK 到设备"

adb -s "$DEVICE_ID" install -r "$APK_PATH" || {
    print_error "APK 安装失败"
    exit 1
}

print_success "APK 安装成功"

# ============================================================
# 步骤 7: 启动应用
# ============================================================

print_step "启动应用"

adb -s "$DEVICE_ID" shell am start -n com.kaixuan.opencode.pocket/.MainActivity || {
    print_error "应用启动失败"
    exit 1
}

print_success "应用已启动"
sleep 3

# ============================================================
# 步骤 8: 开始日志监控（后台）
# ============================================================

print_step "启动日志监控"

LOG_FILE="/tmp/openpocket-cicada-test-$(date +%Y%m%d-%H%M%S).log"
adb -s "$DEVICE_ID" logcat -c
adb -s "$DEVICE_ID" logcat | grep -E "SystemAudio|LiveSubtitle|Cicada|openpocket" > "$LOG_FILE" &
LOGCAT_PID=$!

print_success "日志保存到: $LOG_FILE"
echo "  停止监控: kill $LOGCAT_PID"

# ============================================================
# 步骤 9: 运行 Maestro 测试（可选）
# ============================================================

print_step "Maestro 自动化测试"

if check_command "maestro" > /dev/null 2>&1; then
    echo ""
    read -p "是否运行 Maestro 自动化测试？(y/n) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        if [ -f "scripts/maestro-run.mjs" ]; then
            node scripts/maestro-run.mjs .maestro/cicada-phase-a-live-subtitle.yaml || {
                print_warning "Maestro 测试失败或未完成"
            }
        else
            maestro test .maestro/cicada-phase-a-live-subtitle.yaml || {
                print_warning "Maestro 测试失败或未完成"
            }
        fi
    fi
else
    print_warning "Maestro 未安装，跳过自动化测试"
    echo "  安装方式: curl -Ls https://get.maestro.mobile.dev | bash"
fi

# ============================================================
# 完成
# ============================================================

echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}部署完成！${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo "下一步："
echo "  1. 在设备上手动测试 Cicada Phase A 功能"
echo "  2. 参考测试指南: docs/testing/cicada-phase-a-manual-test-guide.md"
echo "  3. 查看实时日志: tail -f $LOG_FILE"
echo "  4. 填写测试报告: docs/handoff/2026-11-09-cicada-device-testing-report.md"
echo ""
echo "停止日志监控: kill $LOGCAT_PID"
echo ""
