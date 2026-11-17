# Phase 1 VAD 测试快速启动卡

## ✅ 已完成的自动化验证

- [x] 设备连接确认 (4c308e2e)
- [x] 应用安装验证 (v1.2.0-openpocket)
- [x] VAD 模型文件确认 (silero_vad.onnx 2.2MB)
- [x] 代码实现审计通过
- [x] 测试框架部署完成

## 🎯 现在需要你做什么

### 准备工作（2分钟）

**终端1 - 启动日志监控：**
```bash
adb -s 4c308e2e logcat -c && \
adb -s 4c308e2e logcat -v time | grep --color=always -iE "(VAD|MeetingRecord|sherpa)"
```

保持这个窗口开着，它会实时显示 VAD 日志。

### 核心测试（5分钟）

**在你的小米设备 (4c308e2e) 上操作：**

#### Test 1: 验证 VAD 初始化 ✅/❌
1. 打开 OpenPocket 应用
2. 进入【会议】功能  
3. 点击【开始录音】
4. **看终端1** → 应该出现绿色的 `VAD enabled: dynamic endpoint detection active`
   - ✅ 看到 = Phase 1 成功！
   - ❌ 看到 `VAD initialization failed` = 需要调试

#### Test 2: 首字延迟测试 ⏱️
1. 录音开始后，**立即说**："你好，这是测试"
2. **用手机秒表计时**：从你说话到屏幕显示第一个字
3. **记录延迟**：_____ 秒
4. **目标**：< 1秒 ✅  (旧版本 ≥8秒 ❌)

#### Test 3: 动态分段测试 📝
1. 慢慢说3句话，**每句后停顿1秒**：
   - "第一句话" [停顿]
   - "第二句话" [停顿]  
   - "第三句话"
2. **观察屏幕**：应该看到3个独立的文本段
3. **看终端1**：应该看到3次 `VAD detected speech end`

#### Test 4: 10秒兜底测试 ⏰
1. **连续说话10秒以上**，中间不停顿
2. **看终端1**：约10秒时应该出现 `VAD max duration reached, forced emit`

### 收尾（30秒）

**终端2 - 保存完整日志：**
```bash
adb -s 4c308e2e logcat -d > /tmp/vad-test-$(date +%Y%m%d-%H%M%S).log && \
echo "日志已保存到: /tmp/vad-test-$(date +%Y%m%d-%H%M%S).log"
```

---

## 📊 结果判定

### ✅ Phase 1 成功标准
- [ ] Test 1: 看到 `VAD enabled` 日志
- [ ] Test 2: 首字延迟 < 1秒
- [ ] Test 3: 3句话独立分段
- [ ] Test 4: 10秒强制切片
- [ ] 无崩溃，无卡顿

### ❌ 如果失败
**场景1：看到 `VAD initialization failed`**
```bash
# 检查模型文件
adb shell run-as com.kaixuan.opencode.pocket ls -lh files/

# 检查 APK 是否包含模型
adb shell pm path com.kaixuan.opencode.pocket
```

**场景2：延迟仍然≥8秒**
- 说明 VAD 已回退到固定切片模式
- 检查日志中的错误原因

---

## 🚨 重要提示

1. **必须在真机上测试** - 模拟器可能不支持
2. **需要安静环境** - 背景噪音会影响 VAD
3. **说话清晰** - 太轻的声音可能不触发
4. **确保网络通畅** - 转写需要后端服务

---

## 📞 测试完成后

将以下信息发给开发团队：

```
Phase 1 VAD 测试结果
==================
设备: Xiaomi 4c308e2e
版本: 1.2.0-openpocket
时间: [填写测试时间]

Test 1 (VAD初始化): ✅/❌
Test 2 (首字延迟): ____ 秒 (目标<1s)
Test 3 (动态分段): ✅/❌
Test 4 (10秒兜底): ✅/❌

日志文件: /tmp/vad-test-XXXXXX.log
```

---

**预计总耗时**: 10分钟  
**难度**: ⭐⭐☆☆☆ (简单，只需按步骤操作)
