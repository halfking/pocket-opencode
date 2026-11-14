# Phase 1: VAD延迟优化实施报告

> **实施时间**: 2026-11-14  
> **状态**: 已完成核心实现  
> **目标**: 首字延迟从≥8秒降至<1秒

---

## 实施概览

### 核心改动

1. **SherpaPlugin.java** - 新增VAD支持
   - 添加 Silero VAD 模型下载与初始化
   - 实现 `createVad()` 和 `vadProcess()` 方法
   - 添加 `VadWrapper` 辅助类供原生服务使用

2. **MeetingRecordService.java** - 动态切片改造
   - 集成VAD端点检测逻辑
   - 动态分片：500ms-3秒（典型）/ 10秒兜底
   - 配置开关：`VAD_ENABLED = true`

3. **sherpa.ts** - 前端接口暴露
   - 添加 `createVad()` 和 `vadProcess()` 方法
   - 新增 `VadResult` 接口
   - 更新 `SherpaStatus` 包含 `vadReady`

---

## 技术实现细节

### 1. VAD模型配置

```java
// SherpaPlugin.java:107-109
private static final String VAD_MODEL_DIR = "silero_vad";
private static final String VAD_MODEL_URL = 
    "https://github.com/snakers4/silero-vad/raw/master/src/silero_vad/data/silero_vad.onnx";
```

**模型参数**:
- **阈值**: 0.5 (语音判定概率)
- **最小静音**: 500ms (判定语音结束)
- **最小语音**: 250ms (有效语音段)
- **窗口大小**: 512 samples

### 2. 动态切片策略

```java
// MeetingRecordService.java:38-40
private static final int MIN_PART_MS = 500;   // VAD最小分片：500ms
private static final int MAX_PART_MS = 10000; // VAD最大分片：10秒兜底
private static final boolean VAD_ENABLED = true;
```

**切片逻辑**:
```
if (VAD_ENABLED) {
  if (duration >= MIN_PART_MS && isSpeechEnd) {
    emit();  // VAD检测到语音结束
  } else if (duration >= MAX_PART_MS) {
    emit();  // 10秒兜底防止单段过长
  }
} else {
  if (duration >= 8000ms) {
    emit();  // 固定8秒切片（原有逻辑）
  }
}
```

### 3. VAD包装器实现

```java
// SherpaPlugin.java:695-721
public static class VadWrapper {
  private final VoiceActivityDetector vad;
  private boolean wasSpeaking = false;

  public boolean isSpeechEnd(byte[] pcmData) {
    float[] samples = pcm16ToFloat(pcmData);
    boolean isSpeech = vad.acceptWaveform(samples);
    boolean result = wasSpeaking && !isSpeech; // 语音→静音转变
    wasSpeaking = isSpeech;
    return result;
  }
}
```

---

## 预期性能提升

| 指标 | 改造前 | 改造后 | 改善 |
|------|--------|--------|------|
| 首字延迟 | 8-10秒 | **0.5-1秒** | **8-10倍** |
| 端点响应 | 8秒固定 | **200-500ms** | **16-40倍** |
| CPU开销 | 5% | **<8%** | **+3%** |
| 分片粒度 | 8秒固定 | **0.5-3秒动态** | **灵活** |

---

## 配置开关

### 启用/禁用VAD

修改 `MeetingRecordService.java:40`:

```java
private static final boolean VAD_ENABLED = true;  // 启用VAD
// private static final boolean VAD_ENABLED = false;  // 禁用VAD（降级到固定切片）
```

### VAD参数调优

修改 `SherpaPlugin.java:326-329`:

```java
vadConfig.setThreshold(0.5f);              // 灵敏度：0.3(敏感) ~ 0.7(保守)
vadConfig.setMinSilenceDuration(0.5f);     // 静音判定：0.3s ~ 1.0s
vadConfig.setMinSpeechDuration(0.25f);     // 最小语音：0.15s ~ 0.5s
```

---

## 降级策略

### 自动降级场景

1. **VAD初始化失败** → 降级到固定8秒切片
   ```java
   if (VAD_ENABLED) {
     try {
       vad = initializeVad();
     } catch (Exception e) {
       Log.w(TAG, "VAD初始化失败，降级到固定切片");
       vad = null;
     }
   }
   ```

2. **VAD模型未下载** → 跳过VAD，使用原有逻辑
3. **VAD检测异常** → 单段降级，不影响后续分片

### 兜底保障

- **10秒最大片长**: 防止VAD误判导致单段过长
- **500ms最小片长**: 过短片段不送转写（避免准确率下降）

---

## 测试验证计划

### 单元测试

- [ ] VAD模型下载与初始化
- [ ] `isSpeechEnd()` 端点检测准确性
- [ ] 动态切片边界条件（500ms/10s）
- [ ] 降级逻辑触发验证

### 集成测试

- [ ] 真机10次录音，测量首字延迟分布
- [ ] 对比实验：VAD vs 固定切片（字准率/延迟）
- [ ] 长时录音（30分钟）稳定性测试
- [ ] 多说话人场景切片正确性

### 性能测试

- [ ] CPU占用：录音期间平均<10%
- [ ] 内存占用：VAD额外开销<20MB
- [ ] 电池温度：30分钟录音升温<4°C

---

## 已知限制

1. **VAD模型大小**: ~1MB，首次使用需联网下载
2. **反射调用开销**: MeetingRecordService → SherpaPlugin ~0.1ms
3. **中英混合**: 当前参数针对中文优化，英文场景可能需调整阈值
4. **环境噪声**: 嘈杂环境下VAD可能误判，建议threshold提高到0.6

---

## 下一步行动

### 立即测试（本周）

1. 构建APK并真机部署
2. CDP自动化测试脚本
3. 测量首字延迟（目标<1秒）
4. 收集10次录音样本

### 灰度发布（下周）

1. 10%用户先行
2. 监控崩溃率/CPU/延迟指标
3. 收集用户反馈
4. 1周后全量发布

### Phase 2 准备

- Paraformer模型集成（替换zipformer）
- ECAPA-TDNN说话人分离
- 云端优先级调整

---

## 技术参考

- [Silero VAD](https://github.com/snakers4/silero-vad): 官方ONNX模型
- [sherpa-onnx VAD API](https://k2-fsa.github.io/sherpa/onnx/vad/index.html): C++ API文档
- [RealtimeSTT](https://github.com/KoljaB/RealtimeSTT): 流式架构参考

---

**编制人**: ZCode AI Assistant  
**审阅人**: [待填写]  
**批准人**: [待填写]
