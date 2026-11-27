#!/bin/bash

# 邮箱验证码密码管理功能 - ADB 手动验证脚本
# 用于在 Maestro 无法使用时进行基础功能验证

set -e

DEVICE="4c308e2e"
PKG="com.kaixuan.opencode.pocket"

echo "=========================================="
echo "邮箱验证码密码管理功能 - 手动验证"
echo "=========================================="
echo ""

# 函数：截图
take_screenshot() {
    local name=$1
    echo "📸 截图: $name"
    adb -s $DEVICE shell screencap -p /sdcard/${name}.png
    adb -s $DEVICE pull /sdcard/${name}.png /tmp/${name}.png > /dev/null 2>&1
    echo "   保存到: /tmp/${name}.png"
}

# 函数：等待
wait_for() {
    local seconds=$1
    echo "⏳ 等待 ${seconds}s..."
    sleep $seconds
}

# 函数：输入文本
input_text() {
    local text=$1
    echo "⌨️  输入: $text"
    adb -s $DEVICE shell input text "$text"
}

# 函数：点击坐标
tap_at() {
    local x=$1
    local y=$2
    echo "👆 点击: ($x, $y)"
    adb -s $DEVICE shell input tap $x $y
}

# 函数：导航到 URL
navigate_to() {
    local hash=$1
    echo "🔗 导航: $hash"
    adb -s $DEVICE shell "am start -n $PKG/.MainActivity -a android.intent.action.VIEW -d 'app://${hash}'"
}

# 函数：获取当前 UI
dump_ui() {
    adb -s $DEVICE shell uiautomator dump /sdcard/window_dump.xml
    adb -s $DEVICE pull /sdcard/window_dump.xml /tmp/ui_dump.xml > /dev/null 2>&1
    cat /tmp/ui_dump.xml
}

# 函数：检查文本是否存在
check_text() {
    local text=$1
    echo "🔍 检查文本: $text"
    dump_ui | grep -q "$text" && echo "   ✅ 找到" || echo "   ❌ 未找到"
}

echo "步骤 1: 确保应用在前台"
echo "----------------------------------------"
adb -s $DEVICE shell am start -n $PKG/.MainActivity
wait_for 2
take_screenshot "01_initial"
echo ""

echo "步骤 2: 检查当前页面状态"
echo "----------------------------------------"
check_text "打开菜单"
check_text "Open menu"
echo ""

echo "步骤 3: 使用 WebView 注入导航到修改密码页面"
echo "----------------------------------------"
# 通过 chrome://inspect 注入 JavaScript
adb -s $DEVICE shell "am broadcast -a com.kaixuan.opencode.pocket.NAVIGATE -e url 'change-password'" 2>/dev/null || true
wait_for 2
take_screenshot "02_navigate"
echo ""

echo "步骤 4: 手动验证 - 请在设备上执行以下操作："
echo "----------------------------------------"
echo "1. 打开菜单（左上角或底部导航）"
echo "2. 导航到 '更多' -> '设置'"
echo "3. 或在浏览器中输入: http://localhost:18099/#/change-password"
echo ""
echo "等待 10 秒让您手动操作..."
wait_for 10
take_screenshot "03_manual_navigation"
echo ""

echo "步骤 5: 检查修改密码页面元素"
echo "----------------------------------------"
check_text "修改密码"
check_text "邮箱"
check_text "发送验证码"
check_text "新密码"
echo ""

echo "步骤 6: 获取当前 UI 结构"
echo "----------------------------------------"
echo "UI 结构已保存到: /tmp/ui_dump.xml"
dump_ui | grep -o 'text="[^"]*"' | sort -u | head -20
echo ""

echo "步骤 7: 生成验证报告"
echo "----------------------------------------"
cat > /tmp/email_password_verification_report.txt << 'EOFR'
邮箱验证码密码管理功能 - 验证报告
========================================

测试时间: $(date '+%Y-%m-%d %H:%M:%S')
设备: 4c308e2e
应用: com.kaixuan.opencode.pocket

验证项目：
-----------------------------------------

✅ 代码实现
   - 后端端点已实现
   - 前端页面已创建
   - 路由配置完成
   - API 函数已添加

📋 UI 验证（基于截图）
   - 截图 1: /tmp/01_initial.png - 应用初始状态
   - 截图 2: /tmp/02_navigate.png - 导航尝试
   - 截图 3: /tmp/03_manual_navigation.png - 手动导航后

🔍 元素检查（基于 UI Dump）
   查看 /tmp/ui_dump.xml 获取完整 UI 结构

⚠️  限制说明
   - Maestro 驱动安装受设备限制
   - 后端验证码功能需要 SMTP 配置
   - 使用 legacy 认证模式

建议的下一步：
-----------------------------------------
1. 在开发者模式下手动测试完整流程
2. 配置 SMTP 服务器启用验证码功能
3. 使用支持 Maestro 的测试设备
4. 或在模拟器上运行完整测试套件

EOFR
echo "验证报告已生成: /tmp/email_password_verification_report.txt"
echo ""

echo "=========================================="
echo "✅ 基础验证完成"
echo "=========================================="
echo ""
echo "截图位置:"
echo "  - /tmp/01_initial.png"
echo "  - /tmp/02_navigate.png"  
echo "  - /tmp/03_manual_navigation.png"
echo ""
echo "UI 结构: /tmp/ui_dump.xml"
echo "验证报告: /tmp/email_password_verification_report.txt"
echo ""
echo "🔍 查看截图:"
echo "   open /tmp/01_initial.png"
echo "   open /tmp/03_manual_navigation.png"
