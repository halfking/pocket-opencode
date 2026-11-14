# Phase 1: 流式VAD改造设计 (实时转写延迟优化)

## 1. 目标

- **首字延迟**：从 ≥8秒 降至 <1秒
- **端点响应**：从固定切片改为动态端点检测
- **向后兼容**：保留现有fallback机制

## 2. 技术方案

### 2.1 集成 Silero VAD

**选择理由**：
- sherpa-onnx 已内置 silero-vad-v4.onnx (~1MB)
- 检测延迟 <100ms，准确率 >95%
- CPU友好，移动端 RTF 0.01-0.02

**实现位置**：`MeetingRecordService.java` 的音频循环

```java
// 当前：固定8秒切片
if (nowRel - partStartRel >= PART_MS && pcm.size() > 0) {
  emitPart(...);
}

// 改为：VAD动态端点检测
SileroVad vad = SherpaPlugin.createVad();
while (running) {
  int n = rec.read(buf, 0, buf.length);
  VadResult result = vad.process(buf, n);
  
  if (result.isSpeechEnd()) {
    // 检测到语音结束，立即发送
    emitPart(...);
    partStartRel = nowRel;
  } else if (nowRel - partStartRel >= MAX_PART_MS) {
    // 兜底：最长不超过10秒
    emitPart(...);
  }
}
```

### 2.2 流式识别管道

**架构改造**：

```
AudioRecord → 100ms块 → VAD检测 → 语音活动缓冲 
                                      ↓
                            端点检测 → 切句 → sherpa流式识别
                                      ↓
                            非语音段 → 静音跳过
```

**核心改动点**：

| 文件 | 改动 |
|------|------|
| `SherpaPlugin.java` | 新增 `createVad()` / `VadConfig` |
| `MeetingRecordService.java` | 音频循环接入VAD，动态切片 |
| `sherpa.ts` | 新增 `createVad()` 接口暴露 |
| `ingest-speech.ts` | 接收更小粒度分片 (1-3秒) |

### 2.3 分片策略优化

**当前**：固定8秒 → 延迟基线8秒

**优化后**：
- **最小片长**：500ms (避免过碎)
- **典型片长**：1-3秒 (句子级)
- **最大片长**：10秒 (兜底，防止长句无端点)

**配置项**：
```typescript
interface VadConfig {
  minSilenceDurationMs: number;  // 最短静音判定端点：500ms
  speechPadMs: number;            // 语音前后padding：100ms
  minSpeechDurationMs: number;    // 最短语音片段：500ms
  maxSpeechDurationMs: number;    // 最长语音片段：10000ms
}
```

## 3. 性能预期

| 指标 | 当前 | 优化后 | 改善 |
|------|------|--------|------|
| 首字延迟 | 8-10秒 | 0.5-1秒 | **8-10倍** |
| 端点响应 | 固定8秒 | 200-500ms | **16-40倍** |
| 分片数量 | 7.5片/分钟 | 20-30片/分钟 | 内存开销+50%可控 |
| CPU开销 | ~5% | ~8% (+VAD 3%) | 移动端可接受 |

## 4. 风险与兜底

| 风险 | 兜底措施 |
|------|----------|
| VAD误判导致切句错误 | 保留10秒最大片长兜底 |
| 小片转写准确率下降 | <500ms片段不送转写，合并到下一片 |
| CPU开销上升 | 新增 `POCKET_VAD_ENABLED` 开关，默认true |
| 分片过多内存压力 | 渲染节流保持 (§925已有) |

## 5. 实施步骤

1. **Week 1**：Silero VAD 集成到 SherpaPlugin
2. **Week 2**：MeetingRecordService 改造 + 单元测试
3. **Week 3**：真机验证 (延迟测量 + 稳定性)
4. **Week 4**：灰度发布 + A/B测试

## 6. 参考实现

**高星项目**：
- [RealtimeSTT](https://github.com/KoljaB/RealtimeSTT) (5.2k⭐)：webrtcvad + 流式Whisper
- [whisper_streaming](https://github.com/ufal/whisper_streaming) (2.3k⭐)：buffer-based chunking
- [sherpa-onnx examples](https://github.com/k2-fsa/sherpa-onnx/tree/master/android)：官方VAD示例

**关键代码**：
```kotlin
// sherpa-onnx官方示例
val config = VadModelConfig(
  sileroVad = SileroVadModelConfig(
    model = "$modelDir/silero_vad.onnx",
  ),
  sampleRate = 16000,
  minSilenceDuration = 0.5f,
  minSpeechDuration = 0.5f,
)
val vad = Vad(config)
```
