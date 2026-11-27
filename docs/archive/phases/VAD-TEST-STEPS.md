# VAD 真机测试快速步骤

## 准备工作（已完成）

✅ 1. 实现 VAD 工厂 (`vad-factory.ts`)
✅ 2. 实现 Silero VAD (`vad-web-segmenter.ts`)  
✅ 3. 创建 VAD 设置界面 (`VadSettingsSheet.vue`)
✅ 4. 集成到录音运行时 (`recordingRuntime.ts`)
✅ 5. 部署模型文件到 `public/vad/`
✅ 6. 前端构建完成
🔄 7. Android APK 构建中...

## 测试步骤

### 1. 安装和启动 (5分钟)

```bash
# 部署并监控日志
cd /Users/xutaohuang/workspace/ai-native-tools/client/openpocket
./deploy-and-test.sh
```

或手动：
```bash
# 安装 APK
adb -s 4c308e2e install -r frontend/android/app/build/outputs/apk/debug/app-debug.apk

# 启动应用
adb -s 4c308e2e shell am start -n com.kaixuan.opencode.pocket/.MainActivity

# 监控日志
adb -s 4c308e2e logcat | grep -E "vad-|recordingRuntime"
```

### 2. 测试能量检测 VAD (10分钟)

1. 打开应用 → 设置
2. 找到"语音检测方案"，选择"⚡ 能量检测"
3. 创建新会议，开始录音
4. **确认日志**：`[recordingRuntime] VAD created successfully: energy`
5. 执行3个测试用例：
   - 短句+停顿（说话→停2秒→说话）
   - 连续讲话60秒
   - 小声或嘈杂环境
6. 记录：延迟、分段准确性、转写质量

### 3. 测试 Silero VAD (15分钟)

1. 返回设置，切换到"🧠 Silero VAD"
2. **首次使用**：等待下载模型 (~16MB)
3. 创建新会议，开始录音
4. **确认日志**：`[recordingRuntime] VAD created successfully: silero`
5. 执行相同的3个测试用例
6. 记录：延迟、分段准确性、转写质量

### 4. 对比分析 (5分钟)

填写测试报告：`docs/2026-11-17-vad-live-test-report.md`

重点对比：
- 延迟：停顿后多久显示转写
- 准确性：分段是否在自然停顿处
- 体验：哪个更流畅

## 预期观察

### 能量检测 VAD
- ✅ 启动快速 (<10ms)
- ⚠️ 停顿后 ~1.5秒才切段
- ⚠️ 背景噪音可能影响检测

### Silero VAD  
- ⚠️ 首次启动慢 (~500ms，下载+加载模型)
- ✅ 停顿后 ~0.5秒就切段（**快1秒**）
- ✅ 自适应噪音，更稳定

## 关键日志检查点

### 初始化成功
```
[vad-factory] Creating VAD: <type>
[vad-factory] <type> VAD ready in <X>ms
[recordingRuntime] VAD created successfully: <type>
```

### 语音检测
```
# Silero:
[vad-web-segmenter] Speech started at <X>ms
[vad-web-segmenter] Speech ended at <X>ms

# 能量检测:
[vad-segmenter] (检测是静默的，只在问题时报告)
```

### 问题诊断
```
# 模型加载失败 → 自动回退
[vad-factory] Failed to create Silero VAD, falling back to energy detection

# 音频不完整警告
[vad-xxx-segmenter] 段音频不完整

# 强制切分（单段过长）
[vad-xxx-segmenter] 强制切分触发
```

## 测试完成后

1. 保存日志：
   ```bash
   adb logcat -d > vad-test-$(date +%Y%m%d-%H%M%S).log
   ```

2. 填写测试报告

3. 得出结论：
   - Silero VAD 是否达到预期改善？
   - 是否推荐作为默认方案？
   - 有哪些需要优化的地方？

---

**估计总时长**: 35-40 分钟
**关键指标**: 延迟降低 ~1000ms (从 1550ms 到 500ms)
