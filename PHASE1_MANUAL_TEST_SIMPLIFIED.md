# Phase 1 VAD 手动测试 - 简化版

**测试日期**: 2026-11-15  
**预计时间**: 5分钟  
**设备**: Xiaomi 2411DRN47C (4c308e2e)

---

## 🎯 测试目标

验证VAD（语音活动检测）功能是否正常工作，首字延迟是否<1秒。

---

## 📱 第一步：准备监控（电脑操作）

在终端运行以下命令启动实时日志监控：

```bash
cd /Users/xutaohuang/workspace/ai-native-tools/client/openpocket
adb logcat -c
adb logcat | grep --line-buffered -E "VAD|MeetingRecord" | tee /tmp/vad-test-$(date +%Y%m%d-%H%M%S).log
```

保持这个终端窗口打开，你会实时看到VAD相关日志。

---

## 📱 第二步：手机操作

### 2.1 导航到会议功能
1. 打开手机上的 OpenPocket 应用
2. 点击底部导航栏的 **「笔记」** tab
3. 如果看到会议列表，选择任意一个现有会议；如果没有，点击 **「+」** 创建新会议

### 2.2 进入会议详情
1. 进入会议详情页面
2. 找到 **「开始录音」** 按钮（可能在底部浮动栏或页面中间）

### 2.3 录音测试
1. 点击 **「开始录音」** 按钮
2. 等待1秒
3. 清晰地说：**"你好，这是VAD延迟测试"**
4. 停顿 **2秒**（保持安静）
5. 再说：**"第二句话"**
6. 再停顿 **2秒**
7. 点击 **「停止录音」** 按钮

---

## ✅ 第三步：验证结果（查看电脑终端）

在电脑终端中查找以下关键日志：

### ✅ 成功标志 1：VAD初始化
应该看到：
```
MeetingRecordService: VAD enabled with sample rate: 16000
```

### ✅ 成功标志 2：语音端点检测
应该看到多次：
```
MeetingRecordService: VAD detected speech end, duration: XXX ms
```

### ✅ 成功标志 3：首字延迟
- 在手机屏幕上观察：说话后 **不到1秒** 就应该看到转写文字开始出现
- 当前固定8秒切片模式下，需要等8秒才能看到文字

### ✅ 成功标志 4：动态分段
- 两句话应该被分成 **2个独立片段**（不是合并成1个长片段）
- 日志中应该有至少 **2次** "VAD detected speech end"

---

## ❌ 如果没有看到VAD日志

### 可能原因1：麦克风权限未授予
解决方法：
```bash
adb shell pm grant com.kaixuan.opencode.pocket android.permission.RECORD_AUDIO
```

### 可能原因2：VAD初始化失败
查找日志中是否有：
```
VAD initialization failed
```
这是预期的回退行为，系统会使用固定8秒切片。

### 可能原因3：录音功能未触发
- 确认确实点击了「开始录音」按钮
- 检查应用是否要求登录或配置后端服务

---

## 📊 测试报告模板

完成测试后填写：

```
Phase 1 VAD测试结果
==================
测试时间: _______________
测试人员: _______________

1. VAD初始化: ⬜ 成功 / ⬜ 失败 / ⬜ 未触发
2. 首字延迟: _____ 秒 (目标 <1秒)
3. 检测到的端点数量: _____ 次 (预期 2-3次)
4. 分段效果: ⬜ 正常 / ⬜ 异常
5. 应用稳定性: ⬜ 无崩溃 / ⬜ 有崩溃

备注:
_______________________________
_______________________________
```

---

## 🔧 调试命令速查

### 重启应用
```bash
adb shell am force-stop com.kaixuan.opencode.pocket
adb shell am start -n com.kaixuan.opencode.pocket/.MainActivity
```

### 导出完整日志
```bash
adb logcat -d > /tmp/openpocket-full-$(date +%Y%m%d-%H%M%S).log
```

### 检查应用状态
```bash
adb shell ps | grep pocket
```

### 检查内存使用
```bash
adb shell dumpsys meminfo com.kaixuan.opencode.pocket | grep TOTAL
```

---

**下一步**: 完成Phase 1验证后，立即启动Phase 2 Paraformer集成（模型已在下载中）
