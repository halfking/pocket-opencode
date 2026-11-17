# OpenPocket Phase 1 VAD 真机测试指南

**测试日期**: 2026-11-15  
**测试设备**: Xiaomi 2411DRN47C (4c308e2e)  
**APK版本**: 1.2.0-openpocket (158MB)  
**目标**: 验证VAD延迟优化 - 首字延迟从≥8秒降至<1秒

---

## 📋 测试前准备

### 1. 确认安装
```bash
adb devices
# 应显示: 4c308e2e device

adb shell pm list packages | grep pocket
# 应显示: package:com.kaixuan.opencode.pocket
```

### 2. 启动日志监控
```bash
# 清空旧日志
adb logcat -c

# 开始监控（新终端窗口）
adb logcat | grep -E "VAD|MeetingRecord|sherpa"
```

---

## 🧪 测试步骤

### Test 1: VAD初始化验证

**操作**:
1. 打开OpenPocket应用
2. 进入「会议」功能
3. 点击「开始录音」按钮

**预期日志**:
```
I/MeetingRecordService: VAD enabled: dynamic endpoint detection active
I/VadManager: VAD initialized successfully with Silero model
```

**失败标志**:
```
W/MeetingRecordService: VAD initialization failed, falling back to fixed 8s chunks
```

---

### Test 2: 首字延迟测试

**操作**:
1. 开始录音后，立即对手机说话: "你好，这是测试"
2. 观察屏幕上首个字出现的时间
3. 记录从说话到显示的延迟

**预期结果**:
- ✅ 延迟 < 1秒（Phase 1目标）
- ❌ 延迟 ≥ 8秒（旧版固定切片）

**验证日志**:
```
D/MeetingRecordService: VAD detected speech end: 0.65s
```

---

### Test 3: 动态端点检测

**操作**:
1. 连续说3句话，每句间隔1秒:
   - "第一句话"（停顿1秒）
   - "第二句话"（停顿1秒）  
   - "第三句话"
2. 观察每句话是否单独成段

**预期结果**:
- ✅ 每句话单独显示（3个分片）
- ✅ 分片间隔约1秒
- ❌ 所有话合并成一个分片（旧版）

**验证日志**:
```
D/MeetingRecordService: VAD detected speech end: 0.85s
D/MeetingRecordService: VAD detected speech end: 0.72s
D/MeetingRecordService: VAD detected speech end: 0.91s
```

---

### Test 4: 兜底机制测试

**操作**:
1. 开始录音
2. 连续说话超过10秒不停顿

**预期结果**:
- ✅ 约10秒时强制切片
- ✅ 日志显示 "VAD max duration reached"

**验证日志**:
```
D/MeetingRecordService: VAD max duration reached, forced emit
```

---

### Test 5: 回退模式测试

**操作**:
1. 检查是否有VAD初始化失败的情况
2. 如果失败，验证是否正确回退到8秒固定切片

**预期日志（失败时）**:
```
W/MeetingRecordService: VAD initialization failed, falling back to fixed 8s chunks: [错误原因]
```

---

## 📊 验收标准

### 必须满足 (Phase 1 核心目标)

- [ ] VAD初始化成功（无 fallback 警告）
- [ ] 首字延迟 < 1秒（测量3次，取平均值）
- [ ] 动态端点检测工作（句子自动分段）
- [ ] 兜底机制正常（10秒强制切片）
- [ ] 无崩溃、无ANR

### 性能指标

| 指标 | 目标 | 实测 | 状态 |
|------|------|------|------|
| 首字延迟 | < 1秒 | ___ 秒 | ⬜ |
| 分片数量/分钟 | 20-30 | ___ | ⬜ |
| CPU占用 | +3% | ___% | ⬜ |
| 内存占用 | < 500MB | ___ MB | ⬜ |
| 端点响应 | 200-500ms | ___ ms | ⬜ |

---

## 🔍 调试命令

### 检查sherpa-onnx版本
```bash
adb shell unzip -l /data/app/*/com.kaixuan.opencode.pocket*/base.apk | grep sherpa
```

### 检查VAD模型文件
```bash
adb shell run-as com.kaixuan.opencode.pocket ls -lh files/silero_vad.onnx
```

### 实时性能监控
```bash
# CPU占用
adb shell top -n 1 | grep pocket

# 内存占用
adb shell dumpsys meminfo com.kaixuan.opencode.pocket | grep TOTAL
```

### 导出完整日志
```bash
adb logcat -d > /tmp/openpocket-vad-test-$(date +%Y%m%d-%H%M%S).log
```

---

## 📝 测试报告模板

**测试人员**: ___________  
**测试时间**: ___________  
**设备型号**: Xiaomi 2411DRN47C  

### 测试结果

1. VAD初始化: ⬜ 成功 / ⬜ 失败  
2. 首字延迟: ___ 秒 (⬜ <1s / ⬜ ≥8s)  
3. 动态端点: ⬜ 正常 / ⬜ 异常  
4. 兜底机制: ⬜ 正常 / ⬜ 异常  
5. 稳定性: ⬜ 无问题 / ⬜ 有问题  

### 发现的问题

1. ___________
2. ___________

### 日志片段

```
[粘贴关键日志]
```

---

## 🎯 下一步

Phase 1通过后，继续Phase 2:
- [ ] Paraformer-zh 模型集成（准确度85%→95%）
- [ ] ECAPA-TDNN 说话人分离
- [ ] 云端服务优先级调整
