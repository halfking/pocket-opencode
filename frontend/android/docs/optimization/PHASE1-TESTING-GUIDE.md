# Phase 1 VAD 真机测试指南

## 测试目标
验证动态VAD端点检测实际延迟改善效果，目标：首字延迟从8-10秒降至<1秒

## 测试环境

### 硬件
- **设备**: vivo V2436A (序列号: 待填写)
- **Android版本**: Android 14+
- **APK**: `app/build/outputs/apk/debug/app-debug.apk`
- **编译时间**: 2026-11-14
- **版本**: v1.2.0-openpocket + Phase 1 VAD

### 后端
- **生产环境**: https://pocket.itestu.cn
- **本地测试**: 可选

---

## 测试步骤

### Step 1: 安装APK

```bash
# 1. 连接设备
adb devices

# 2. 安装APK（覆盖安装）
adb install -r frontend/android/app/build/outputs/apk/debug/app-debug.apk

# 3. 验证安装
adb shell pm list packages | grep pocket
```

**预期输出**: `package:com.kaixuan.opencode.pocket`

### Step 2: 启动日志监控

```bash
# 开启logcat监控（新终端窗口）
adb logcat -c  # 清空旧日志
adb logcat | grep -E "VAD|MeetingRecord|sherpa" | tee /tmp/vad-test-$(date +%Y%m%d-%H%M%S).log
```

**关键日志**:
- `VAD enabled: dynamic endpoint detection active` - VAD初始化成功
- `VAD detected speech end: X.XXs` - 检测到语音结束
- `VAD initialization failed, falling back to fixed 8s chunks` - 回退固定切片

### Step 3: 执行测试用例

#### 用例1: 首字延迟测试

**操作**:
1. 打开应用 → 登录 → 进入会议
2. 点击录音按钮
3. 立即说话："你好，这是测试"
4. 记录从说话到字幕出现的时间

**测量方法**:
```bash
# 在另一终端记录时间戳
echo "开始说话: $(date +%H:%M:%S.%3N)"
# 观察logcat和屏幕，当字幕出现时
echo "字幕出现: $(date +%H:%M:%S.%3N)"
```

**验收标准**:
- ✅ 首字延迟 < 1秒
- ✅ logcat显示 "VAD enabled"
- ✅ 无崩溃错误

#### 用例2: 端点响应测试

**操作**:
1. 录音中说一句短话："测试"
2. 停顿2秒
3. 再说一句："结束"
4. 观察logcat中的VAD检测日志

**验收标准**:
- ✅ 停顿500ms后检测到语音结束
- ✅ logcat显示 "VAD detected speech end"
- ✅ 两句话分为两个片段

#### 用例3: 连续说话测试

**操作**:
1. 连续说话1分钟，不停顿
2. 观察分片情况

**验收标准**:
- ✅ 未超过10秒强制切片
- ✅ 分片数量约20-30片/分钟
- ✅ 无音频丢失

#### 用例4: 兜底机制测试

**操作**:
1. 在飞行模式下录音（模拟VAD失败）
2. 或删除 silero_vad.onnx 触发初始化失败

**验收标准**:
- ✅ 自动回退到固定8秒切片
- ✅ logcat显示 "falling back to fixed 8s chunks"
- ✅ 录音功能正常

---

## 数据收集

### 延迟指标

| 测试轮次 | 首字延迟 | 端点响应 | 分片数/分钟 | CPU占用 | 内存占用 |
|---------|---------|---------|------------|---------|---------|
| 1 | | | | | |
| 2 | | | | | |
| 3 | | | | | |
| **平均** | | | | | |

### 系统资源监控

```bash
# CPU占用
adb shell top -n 1 | grep pocket

# 内存占用
adb shell dumpsys meminfo com.kaixuan.opencode.pocket | grep TOTAL

# 电池温度
adb shell dumpsys battery | grep temperature
```

---

## 问题排查

### 问题1: VAD初始化失败

**症状**: logcat显示 "VAD initialization failed"

**排查**:
```bash
# 检查silero_vad.onnx是否存在
adb shell ls -lh /data/data/com.kaixuan.opencode.pocket/files/silero_vad.onnx

# 检查sherpa-onnx版本
adb shell dumpsys package com.kaixuan.opencode.pocket | grep versionName
```

**解决**: 重新安装APK，确保模型文件打包

### 问题2: 延迟未改善

**症状**: 首字延迟仍≥8秒

**排查**:
```bash
# 检查VAD是否启用
adb logcat | grep "VAD enabled"

# 检查是否回退到固定切片
adb logcat | grep "falling back"
```

**解决**: 检查logcat错误信息，可能是VAD未正确初始化

### 问题3: 应用崩溃

**症状**: 录音时应用闪退

**排查**:
```bash
# 查看崩溃日志
adb logcat | grep -E "FATAL|AndroidRuntime"

# 检查内存泄漏
adb shell dumpsys meminfo com.kaixuan.opencode.pocket
```

**解决**: 检查VadManager.release()是否被调用

---

## 测试报告模板

```markdown
# Phase 1 VAD 真机测试报告

**测试时间**: YYYY-MM-DD HH:MM
**测试设备**: vivo V2436A
**测试人员**: [姓名]

## 测试结果

### 延迟改善
- 首字延迟: XX秒 (目标<1秒) - [✅/❌]
- 端点响应: XXXms (目标200-500ms) - [✅/❌]
- 分片数量: XX片/分钟 (目标20-30) - [✅/❌]

### 系统资源
- CPU占用: XX% (目标<10%) - [✅/❌]
- 内存占用: XXX MB (目标<500MB) - [✅/❌]
- 电池温度: XX°C (目标升温<4°C) - [✅/❌]

### 功能验证
- [✅/❌] VAD初始化成功
- [✅/❌] 端点检测正常
- [✅/❌] 兜底机制工作
- [✅/❌] 无崩溃无内存泄漏

## 问题记录
1. [问题描述]
   - 复现步骤: ...
   - 日志输出: ...
   - 解决方案: ...

## 结论
- Phase 1目标达成度: XX%
- 是否可进入Phase 2: [是/否]
- 后续改进建议: ...
```

---

## 快速测试命令集

```bash
# 一键测试脚本
cat > /tmp/vad-test.sh << 'EOF'
#!/bin/bash
echo "=== Phase 1 VAD 真机测试 ==="
echo "1. 检查设备连接..."
adb devices

echo "2. 安装APK..."
adb install -r frontend/android/app/build/outputs/apk/debug/app-debug.apk

echo "3. 启动应用..."
adb shell am start -n com.kaixuan.opencode.pocket/.MainActivity

echo "4. 监控日志（按Ctrl+C停止）..."
adb logcat -c
adb logcat | grep -E "VAD|MeetingRecord" --color=always
EOF

chmod +x /tmp/vad-test.sh
/tmp/vad-test.sh
```

---

## 参考

- [[openpocket-device-rig]] - 真机测试装置配置
- [[cicada-phase-a-live-test-2026-11-14]] - 往期真机测试经验
- `docs/optimization/PHASE1-FINAL-SUMMARY.md` - Phase 1完成总结
