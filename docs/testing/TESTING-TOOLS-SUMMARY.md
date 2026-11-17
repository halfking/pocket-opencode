# OpenPocket 录音自动化测试工具集

## 📦 已创建的工具

我为你创建了完整的录音自动化测试工具集，包含以下组件：

### 1. 核心测试脚本

| 文件 | 功能 | 使用场景 |
|------|------|---------|
| `scripts/test-recording-auto.sh` | **全自动测试** | 完整的自动化测试流程 |
| `scripts/test-recording-simple.sh` | **简化版测试** | 需要少量人工交互 |
| `scripts/analyze-recording-logs.sh` | **日志分析** | 分析已有的录音日志 |
| `scripts/quick-test.sh` | **快速启动** | 一键启动测试 |

### 2. 辅助工具

| 文件 | 功能 |
|------|------|
| `scripts/generate-test-audio.mjs` | 生成 WAV 格式测试音频 |
| `scripts/inject-audio-during-recording.sh` | 5种音频注入方案 |
| `scripts/__tests__/test-audio-5s.wav` | 预生成的测试音频 (156KB) |

### 3. Maestro 自动化流程

| 文件 | 功能 |
|------|------|
| `.maestro/auto-recording-test.yaml` | UI 自动化：导航 + 开始录音 |
| `.maestro/recording-full-auto.yaml` | 完整流程（备用） |

### 4. 文档

| 文件 | 内容 |
|------|------|
| `docs/testing/auto-recording-test-guide.md` | 完整使用指南 |
| `README-AUTO-TEST.md` | 快速上手说明 |

## 🚀 使用方法

### 方案 1: 日志分析（最简单，当前可用）

```bash
# 1. 在设备上手动进行一次录音操作
#    更多 → 会议 → 🎙 开始录音 → 说几句话 → 停止

# 2. 运行日志分析
./scripts/analyze-recording-logs.sh

# 输出示例：
# ✓ VAD 成功初始化
# ✓ VAD 检测到语音端点
# ✓ Paraformer 转写已执行
# ✓ 应用运行稳定，无崩溃
# ✓ 录音服务正常运行
```

**优点**: 
- ✅ 无需任何额外配置
- ✅ 立即可用
- ✅ 分析已有日志

### 方案 2: 简化版测试（需少量人工）

```bash
# 运行测试（会提示你手动操作）
./scripts/test-recording-simple.sh

# 按提示操作：
# 1. 导航到录音页面
# 2. 等待脚本注入音频
# 3. 停止录音
```

**优点**:
- ✅ 自动注入音频
- ✅ 自动验证结果
- ✅ 生成 JSON 报告

### 方案 3: 完全自动化（需配置认证）

```bash
# 设置认证密码（一次性）
export POCKET_DEV_PASS="your-dev-password"
export POCKET_AUTH_PASS="your-dev-password"

# 一键运行
./scripts/quick-test.sh
```

**优点**:
- ✅ 完全无人工介入
- ✅ 可集成 CI/CD
- ✅ 可批量运行

## 📊 测试验证项

所有方案都会验证以下 5 个关键指标：

1. **VAD 初始化** - VAD 动态端点检测启动
2. **VAD 端点检测** - 检测到语音开始/结束
3. **Paraformer 转写** - 本地转写模型执行
4. **应用稳定性** - 无崩溃异常
5. **录音服务** - 后台服务正常运行

## 🎯 输出示例

### 终端输出
```
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
测试结果总结
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  ✓ VAD 成功初始化
  ✓ VAD 检测到语音端点
  ✓ Paraformer 转写已执行
  ✓ 应用运行稳定，无崩溃
  ✓ 录音服务正常运行

  通过: 5 / 5
  失败: 0 / 5

  日志文件: /tmp/openpocket-log-analysis-20261117-153045.log
  JSON 报告: /tmp/openpocket-log-result-20261117-153045.json

✓ 所有测试通过
```

### JSON 报告
```json
{
  "timestamp": "2026-11-17T07:30:45Z",
  "device": "4c308e2e",
  "total_tests": 5,
  "passed": 5,
  "failed": 0,
  "tests": {
    "vad_init": true,
    "vad_detection": true,
    "paraformer": true,
    "no_crash": true,
    "service_running": true
  }
}
```

## 🔧 音频注入技术

脚本支持 5 种音频注入方案（自动降级）：

1. **音频文件播放** ⭐ 推荐
   - 推送 WAV 文件到设备
   - 使用媒体播放器播放
   - 最可靠和可重复

2. **TTS 语音合成**
   - 使用系统 TTS 引擎
   - 所有设备都支持
   - 质量取决于 TTS

3. **系统通知音**
   - 触发系统音效
   - 无需额外工具
   - 时长较短

4. **正弦波生成**
   - 使用 tinymix/tinyplay
   - 纯净测试信号
   - 部分设备支持

5. **PCM 直接注入** (需要 root)
   - 直接写入音频设备
   - 最直接的方式
   - 需要 root 权限

## 📝 当前建议

由于你的设备上还没有录音日志，建议按以下步骤操作：

### Step 1: 手动测试一次
```bash
# 1. 在设备上打开 OpenPocket
# 2. 更多 → 会议 → 🎙 开始会议录音
# 3. 说几句话（"这是一个测试"）
# 4. 点击停止录音
```

### Step 2: 运行日志分析
```bash
./scripts/analyze-recording-logs.sh
```

### Step 3: 查看结果
```bash
# 查看完整日志
cat /tmp/openpocket-log-analysis-*.log

# 查看 JSON 报告
cat /tmp/openpocket-log-result-*.json | jq .

# 查看 VAD 相关日志
grep -C 2 'VAD' /tmp/openpocket-log-analysis-*.log

# 查看 Paraformer 日志
grep -C 2 'Paraformer' /tmp/openpocket-log-analysis-*.log
```

## 🛠️ 故障排查

### 问题 1: 未检测到设备
```bash
# 检查连接
adb devices

# 应该看到：
# 4c308e2e    device
```

### 问题 2: 未找到日志
```bash
# 确保应用在前台运行
adb shell dumpsys window | grep mCurrentFocus

# 手动查看 logcat
adb logcat | grep -E "VAD|Paraformer"
```

### 问题 3: Maestro 认证失败
```bash
# 设置开发密码
export POCKET_DEV_PASS="your-password"
export POCKET_AUTH_PASS="your-password"

# 或者使用简化版测试（无需认证）
./scripts/test-recording-simple.sh
```

## 📚 相关文档

- **完整指南**: `docs/testing/auto-recording-test-guide.md`
- **快速上手**: `README-AUTO-TEST.md`
- **Maestro 流程**: `.maestro/auto-recording-test.yaml`

## 🎉 下一步

1. **立即可用**: 运行 `./scripts/analyze-recording-logs.sh` 分析现有日志
2. **完整测试**: 手动录音一次后，运行日志分析验证
3. **CI/CD 集成**: 配置认证后，可集成到持续集成流程

## 📞 需要帮助？

如果遇到问题，请：
1. 查看 `docs/testing/auto-recording-test-guide.md` 的故障排查章节
2. 运行 `adb logcat | grep -E "VAD|Paraformer"` 查看实时日志
3. 检查 `/tmp/openpocket-*.log` 文件中的详细信息

---

**创建时间**: 2026-11-17  
**测试设备**: 4c308e2e (已连接)  
**状态**: ✅ 工具已就绪，等待首次录音测试
