#!/bin/bash
# Phase 1 VAD延迟测试脚本
# 用途：真机测试VAD动态切片的首字延迟

set -e

echo "=== Phase 1: VAD延迟测试 ==="
echo ""

# 检查ADB连接
if ! adb devices | grep -q "device$"; then
  echo "错误: 未检测到Android设备"
  echo "请确保："
  echo "  1. 设备已通过USB连接"
  echo "  2. 已启用USB调试"
  echo "  3. 运行 'adb devices' 验证连接"
  exit 1
fi

DEVICE=$(adb devices | grep "device$" | head -1 | awk '{print $1}')
echo "✓ 已连接设备: $DEVICE"
echo ""

# 检查应用是否已安装
PACKAGE="com.kaixuan.opencode.pocket"
if ! adb -s "$DEVICE" shell pm list packages | grep -q "^package:$PACKAGE$"; then
  echo "错误: 应用未安装"
  echo "请先运行: cd frontend/android && ./gradlew assembleDebug && adb install -r app/build/outputs/apk/debug/app-debug.apk"
  exit 1
fi
echo "✓ 应用已安装: $PACKAGE"
echo ""

# 清除logcat缓冲区
adb -s "$DEVICE" logcat -c
echo "✓ 已清除logcat缓冲区"
echo ""

# 启动应用
echo "正在启动应用..."
adb -s "$DEVICE" shell am start -n "$PACKAGE/.MainActivity"
sleep 3
echo "✓ 应用已启动"
echo ""

# 开始监控logcat（VAD相关日志）
echo "=== 开始监控VAD日志 ==="
echo "提示: 在应用中开始录音并说话，观察首字延迟"
echo "      按 Ctrl+C 停止监控"
echo ""

adb -s "$DEVICE" logcat -s SherpaPlugin:I MeetingRecordService:I BackgroundMicPlugin:I | \
  grep -E "(VAD|isSpeechEnd|emitPart)" --line-buffered | \
  awk '{
    if ($0 ~ /emitPart/) {
      split($0, a, "emitPart");
      split(a[2], b, ",");
      seq = b[1];
      gsub(/[^0-9]/, "", seq);
      print "[" strftime("%H:%M:%S") "] 分片发送 #" seq;
    } else if ($0 ~ /isSpeechEnd.*true/) {
      print "[" strftime("%H:%M:%S") "] VAD检测到语音结束";
    } else if ($0 ~ /VAD初始化/) {
      print "[" strftime("%H:%M:%S") "] " $0;
    }
  }'
