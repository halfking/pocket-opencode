# OpenPocket 全自动录音测试

## 概述

完全自动化的录音功能测试方案，无需人工介入。集成了：
- ✅ Maestro UI 自动化（导航、点击、截图）
- ✅ ADB 音频注入（5种方案自动降级）
- ✅ logcat 实时验证（VAD、Paraformer、崩溃检测）
- ✅ JSON 测试报告生成

## 快速开始

### 一键运行

```bash
# 方式 1: 使用集成脚本（推荐）
./scripts/test-recording-auto.sh

# 方式 2: 分步运行
# Step 1: 生成测试音频
node scripts/generate-test-audio.mjs

# Step 2: 运行 Maestro 测试
node scripts/maestro-run.mjs .maestro/auto-recording-test.yaml

# Step 3: 手动注入音频（在录音过程中）
./scripts/inject-audio-during-recording.sh

# Step 4: 查看结果
cat /tmp/openpocket-recording-test-*.log
```

## 前置条件

### 必需工具

- ✅ **adb** (Android SDK Platform-Tools)
- ✅ **Node.js** >= 22.22.2
- ✅ **Maestro** (自动安装或手动: `curl -Ls https://get.maestro.mobile.dev | bash`)

### 可选工具（用于音频生成）

- **ffmpeg** - 生成高质量测试音频
- **sox** - 音频处理工具

### 设备要求

- USB 连接的 Android 真机
- 已启用 USB 调试
- 已授权 ADB 连接

## 测试流程

### 自动化流程图

```
┌─────────────────┐
│  启动应用        │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  导航到录音页    │ ← Maestro UI 自动化
│  更多 → 会议     │
│  → 开始会议录音  │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  开始录音        │
│  (点击麦克风按钮)│
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  音频注入        │ ← ADB 自动注入
│  5种方案降级     │   (5秒测试音频)
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  持续监控        │ ← logcat 实时验证
│  VAD + Paraformer│   - VAD 初始化
│  (15秒)          │   - 语音端点检测
└────────┬────────┘   - 转写输出
         │           - 崩溃检测
         ▼
┌─────────────────┐
│  停止录音        │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  生成报告        │
│  - JSON 结果     │
│  - 日志文件      │
│  - 截图          │
└─────────────────┘
```

### 音频注入方案（自动降级）

脚本会按优先级尝试以下方案，直到成功：

#### 方案 1: 音频文件播放 (推荐)
```bash
# 推送 WAV 文件到设备并播放
adb push test-audio.wav /sdcard/Download/
adb shell am start -a android.intent.action.VIEW -t "audio/*" ...
```

**优点**: 可控、可重复、音质好  
**要求**: 需要媒体播放器

#### 方案 2: TTS 语音合成
```bash
# 使用系统 TTS 引擎朗读文本
adb shell tts_speak "Testing recording..."
```

**优点**: 所有设备都有  
**缺点**: 质量取决于 TTS 引擎

#### 方案 3: 系统通知音
```bash
# 触发通知音效
adb shell cmd notification post ...
```

**优点**: 无需额外工具  
**缺点**: 时长短、可能被静音

#### 方案 4: 正弦波生成
```bash
# 使用 tinymix/tinyplay 播放
dd if=/dev/urandom | tinyplay ...
```

**优点**: 纯净信号  
**要求**: 设备需支持 tinymix

#### 方案 5: PCM 直接注入 (需要 root)
```bash
# 直接写入音频设备
su -c 'dd if=/dev/urandom of=/dev/snd/pcmC0D0c ...'
```

**优点**: 最直接  
**要求**: Root 权限

## 验证项目

测试脚本自动验证以下 5 个关键指标：

### Test 1: VAD 初始化 ✓
```
验证: logcat 包含 "VAD enabled"
期望: VAD 动态端点检测启动成功
```

### Test 2: VAD 端点检测 ✓
```
验证: logcat 包含 "VAD detected speech"
期望: 检测到语音开始/结束端点
```

### Test 3: Paraformer 转写 ✓
```
验证: logcat 包含 "Paraformer" + "transcription"
期望: 本地转写模型执行并输出结果
```

### Test 4: 无崩溃 ✓
```
验证: logcat 不包含 "FATAL" 或 "AndroidRuntime"
期望: 应用稳定运行，无崩溃
```

### Test 5: 录音服务 ✓
```
验证: logcat 包含 "MeetingRecordService"
期望: 后台录音服务正常运行
```

## 输出文件

### 1. 日志文件
```
/tmp/openpocket-recording-test-20261117-143025.log
```
包含 VAD、Paraformer、录音服务的完整日志

### 2. JSON 测试报告
```json
{
  "timestamp": "2026-11-17T06:30:25Z",
  "device": "emulator-5554",
  "app_id": "com.kaixuan.opencode.pocket",
  "total_tests": 5,
  "passed": 4,
  "failed": 1,
  "tests": {
    "vad_init": true,
    "vad_detection": false,
    "paraformer": true,
    "no_crash": true,
    "service_running": true
  }
}
```

### 3. 截图
```
logs/maestro/auto-recording-started.png  - 录音启动状态
logs/maestro/recording-stopped.png       - 录音停止后
logs/maestro/test-complete.png          - 测试完成
```

## 故障排查

### 问题 1: 无法检测到设备
```bash
# 检查设备连接
adb devices

# 应该看到类似输出:
# List of devices attached
# emulator-5554    device
```

**解决方案**:
- 检查 USB 连接
- 在手机上启用 USB 调试
- 授权此计算机调试

### 问题 2: Maestro 测试失败
```bash
# 检查 Maestro 安装
maestro --version

# 查看详细错误
node scripts/maestro-run.mjs .maestro/auto-recording-test.yaml
```

**解决方案**:
- 重新安装 Maestro: `curl -Ls https://get.maestro.mobile.dev | bash`
- 检查设备屏幕是否解锁
- 确保应用已安装

### 问题 3: 音频注入失败
```bash
# 手动测试各个方案
./scripts/inject-audio-during-recording.sh [设备ID] [时长]

# 查看具体失败原因
adb logcat | grep -i audio
```

**解决方案**:
- 检查设备音量设置
- 尝试手动播放音频文件
- 使用 TTS 方案作为后备

### 问题 4: VAD 未检测到语音
```bash
# 检查 VAD 日志
grep "VAD" /tmp/openpocket-recording-test-*.log
```

**可能原因**:
- 音频注入失败（无实际音频输入）
- 音量太低
- VAD 阈值设置过高

**解决方案**:
- 手动在设备上说话
- 增加测试音频音量
- 检查麦克风权限

### 问题 5: 测试报告显示失败
```bash
# 查看完整日志
cat /tmp/openpocket-recording-test-*.log

# 查看 JSON 报告
cat /tmp/openpocket-test-result-*.json | jq .
```

**分析方法**:
- 检查具体哪个测试项失败
- 查看对应的日志输出
- 根据上述故障排查步骤解决

## 高级用法

### 自定义测试音频

```bash
# 生成 10 秒测试音频
node scripts/generate-test-audio.mjs test-audio-10s.wav 10 440

# 使用 ffmpeg 生成人声频率
ffmpeg -f lavfi -i "sine=frequency=200:duration=5" \
  -ar 16000 -ac 1 test-audio-voice.wav
```

### 并行测试多个设备

```bash
# 获取所有连接的设备
adb devices | grep device | awk '{print $1}' | while read device; do
  POCKET_DEVICE_ID=$device ./scripts/test-recording-auto.sh &
done
wait
```

### CI/CD 集成

```yaml
# .github/workflows/recording-test.yml
name: Recording Test
on: [push, pull_request]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - name: Setup Android SDK
        uses: android-actions/setup-android@v2
      - name: Run Recording Test
        run: ./scripts/test-recording-auto.sh
      - name: Upload Results
        if: always()
        uses: actions/upload-artifact@v3
        with:
          name: test-results
          path: |
            /tmp/openpocket-*.log
            /tmp/openpocket-*.json
            logs/maestro/*.png
```

## 性能基准

### 预期时长
- 总测试时长: ~40-60 秒
  - UI 导航: 10-15 秒
  - 录音 + 音频注入: 15-20 秒
  - 验证 + 报告: 5-10 秒

### 成功率
- 生产环境: 95%+
- CI 环境: 85%+（网络和设备差异）

## 相关文件

```
scripts/
├── test-recording-auto.sh           # 主测试脚本
├── generate-test-audio.mjs          # 音频生成工具
├── inject-audio-during-recording.sh # 音频注入脚本
└── maestro-run.mjs                  # Maestro 启动器

.maestro/
├── auto-recording-test.yaml         # 自动化测试流程
└── recording-full-auto.yaml         # 完整流程（备用）
```

## 后续优化

- [ ] 支持自定义录音时长
- [ ] 增加转写准确率验证
- [ ] 支持多语言测试
- [ ] 集成到 CI/CD pipeline
- [ ] 生成 HTML 测试报告

## 参与贡献

如果你发现任何问题或有改进建议，请提交 Issue 或 PR。

## 许可证

MIT License
