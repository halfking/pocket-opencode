#!/bin/bash

echo "========================================"
echo "Phase 2 Paraformer 手动功能验证"
echo "========================================"
echo "开始时间: $(date)"
echo ""

# 1. 启动应用
echo "Step 1: 启动应用..."
adb shell am start -n com.kaixuan.opencode.pocket/.MainActivity
sleep 3

# 2. 清空日志
echo "Step 2: 清空日志..."
adb logcat -c
sleep 1

# 3. 点击笔记标签
echo "Step 3: 导航到笔记标签页..."
adb shell input tap 360 1200
sleep 2

# 4. 点击添加按钮
echo "Step 4: 点击创建新会议按钮..."
adb shell input tap 700 1200
sleep 2

# 5. 输入会议名称
echo "Step 5: 输入会议名称..."
adb shell input text "Phase2_Paraformer_$(date +%s)"
sleep 1

# 6. 点击确认
echo "Step 6: 点击确认按钮..."
adb shell input tap 600 900
sleep 3

# 7. 开始录音
echo "Step 7: 开始录音..."
adb shell input tap 400 600
sleep 2

echo ""
echo "========================================"
echo "现在请对着手机清晰说话，时长3分钟"
echo "说一些完整的句子，包含标点符号"
echo "========================================"
echo ""

# 8. 等待3分钟（180秒）用户进行语音输入
echo "等待时间开始..."
for i in {1..6}; do
  echo "已等待 $((i * 30)) 秒..."
  sleep 30
done

# 9. 停止录音
echo "Step 8: 停止录音..."
adb shell input tap 400 600
sleep 3

# 10. 收集日志
echo "Step 9: 收集应用日志..."
adb logcat -d > logs/phase2_test_$(date +%Y%m%d_%H%M%S).log

echo ""
echo "========================================"
echo "测试完成时间: $(date)"
echo "========================================"
