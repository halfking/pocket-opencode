#!/bin/bash

# 邮箱验证码密码管理功能 Maestro 测试运行脚本
#
# 用法:
#   ./run-email-tests.sh [test-name]
#
# 示例:
#   ./run-email-tests.sh                    # 运行完整测试套件
#   ./run-email-tests.sh change-password    # 只运行密码修改测试
#   ./run-email-tests.sh errors             # 只运行错误场景测试
#   ./run-email-tests.sh login-setup        # 只运行登录后邮箱设置测试

set -euo pipefail

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 项目根目录
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
MAESTRO_DIR="$PROJECT_ROOT/.maestro"

# 检查 Maestro 是否安装
if ! command -v maestro &> /dev/null; then
    echo -e "${RED}错误: Maestro 未安装${NC}"
    echo "请访问 https://maestro.mobile.dev 安装 Maestro"
    exit 1
fi

# 检查设备是否连接
if ! adb devices | grep -q "device$"; then
    echo -e "${RED}错误: 没有检测到 Android 设备${NC}"
    echo "请确保设备已连接并启用 USB 调试"
    exit 1
fi

# 环境变量配置
# 从 .env 文件加载（如果存在）
if [ -f "$PROJECT_ROOT/.env.test" ]; then
    echo -e "${BLUE}加载测试环境变量...${NC}"
    export $(cat "$PROJECT_ROOT/.env.test" | grep -v '^#' | xargs)
fi

# 设置默认值
export POCKET_MASTER=${POCKET_MASTER:-"TestMaster123"}
export POCKET_DEV_PASS=${POCKET_DEV_PASS:-"admin"}
export POCKET_TEST_EMAIL=${POCKET_TEST_EMAIL:-"test@example.com"}
export POCKET_OLD_PASS=${POCKET_OLD_PASS:-"admin"}
export POCKET_NEW_PASS=${POCKET_NEW_PASS:-"NewPass123"}
export POCKET_NO_EMAIL_USER=${POCKET_NO_EMAIL_USER:-""}
export POCKET_NO_EMAIL_PASS=${POCKET_NO_EMAIL_PASS:-""}
export POCKET_TEST_EMAIL_NEW=${POCKET_TEST_EMAIL_NEW:-"newtest@example.com"}

# 显示配置
echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}邮箱验证码密码管理功能测试${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""
echo -e "${YELLOW}测试配置:${NC}"
echo "  测试邮箱: $POCKET_TEST_EMAIL"
echo "  主密码: [已设置]"
echo "  测试账号: admin"
echo ""

# 运行指定的测试
run_test() {
    local test_file=$1
    local test_name=$2
    
    echo -e "${BLUE}运行测试: $test_name${NC}"
    echo "----------------------------------------"
    
    if maestro test "$MAESTRO_DIR/$test_file"; then
        echo -e "${GREEN}✅ $test_name - 通过${NC}"
        return 0
    else
        echo -e "${RED}❌ $test_name - 失败${NC}"
        return 1
    fi
}

# 获取测试参数
TEST_NAME=${1:-"suite"}

case "$TEST_NAME" in
    "suite"|"all")
        echo -e "${YELLOW}运行完整测试套件...${NC}"
        run_test "email-password-suite.yaml" "完整测试套件"
        ;;
    "change-password"|"change")
        echo -e "${YELLOW}运行密码修改测试...${NC}"
        run_test "email-password-change.yaml" "密码修改功能"
        ;;
    "errors"|"error")
        echo -e "${YELLOW}运行错误场景测试...${NC}"
        run_test "email-password-errors.yaml" "错误处理"
        ;;
    "login-setup"|"setup")
        if [ -z "$POCKET_NO_EMAIL_USER" ]; then
            echo -e "${RED}错误: 需要设置 POCKET_NO_EMAIL_USER 环境变量${NC}"
            echo "这个测试需要一个没有邮箱的测试账号"
            exit 1
        fi
        echo -e "${YELLOW}运行登录后邮箱设置测试...${NC}"
        run_test "email-setup-on-login.yaml" "登录后邮箱设置"
        ;;
    "dialog")
        echo -e "${YELLOW}运行邮箱验证对话框测试...${NC}"
        run_test "email-verification-dialog.yaml" "邮箱验证对话框"
        ;;
    *)
        echo -e "${RED}未知的测试名称: $TEST_NAME${NC}"
        echo ""
        echo "可用的测试:"
        echo "  suite, all         - 运行完整测试套件"
        echo "  change-password    - 密码修改功能测试"
        echo "  errors             - 错误场景测试"
        echo "  login-setup        - 登录后邮箱设置测试"
        echo "  dialog             - 邮箱验证对话框测试"
        exit 1
        ;;
esac

# 测试结果汇总
echo ""
echo -e "${BLUE}========================================${NC}"
if [ $? -eq 0 ]; then
    echo -e "${GREEN}✅ 测试执行完成${NC}"
else
    echo -e "${RED}❌ 测试执行失败${NC}"
    exit 1
fi
echo -e "${BLUE}========================================${NC}"
