# Phase 2: 准确度提升方案

## 1. 目标

- **中文字准率**：从85% (zipformer) 提升至95%+
- **标点还原**：从无标点到完整标点
- **说话人识别**：从缺失到多人会议可用
- **鲁棒性**：云端故障时本地结果可用

## 2. 本地引擎升级

### 2.1 替换为 Paraformer 大模型

**当前问题**：
- zipformer: 85%字准，无标点，同音字多
- sensevoice: 97%字准但体量大 (227MB)，不适合流式

**方案**：引入 Paraformer-large (FunASR)

| 模型 | 字准率 | 标点 | RTF (移动端) | 体量 | 适用场景 |
|------|--------|------|--------------|------|----------|
| zipformer-small | 85% | ❌ | 0.12 | 50MB | 当前流式 |
| **paraformer-zh** | **95%** | ✅ | **0.15** | **120MB** | **替代流式** |
| sensevoice-int8 | 97% | ✅ | 0.03 | 227MB | 保留整段精转 |

**实现**：
```java
// SherpaPlugin.java
private void initParaformer() {
  OfflineParaformerModelConfig paraformer = 
    OfflineParaformerModelConfig.builder()
      .model(modelsDir + "/paraformer-zh-2023-09-14/model.int8.onnx")
      .build();
      
  OfflineModelConfig modelConfig = 
    OfflineModelConfig.builder()
      .paraformer(paraformer)
      .tokens(modelsDir + "/paraformer-zh-2023-09-14/tokens.txt")
      .numThreads(2)
      .build();
}
```

**收益**：
- 字准率 +10% (85%→95%)
- 自动标点恢复
- RTF增加仅3% (0.12→0.15)，可接受
- 体量+70MB，首次下载可接受

### 2.2 说话人分离集成

**当前问题**：
- `extractEmbedding` 未接入 (sherpa.ts:62 reject)
- 多人会议无法区分说话人
- diarization service 配置了但未联调

**方案A：本地 ECAPA-TDNN (推荐)**

```typescript
// speaker-embedding.ts 真实实现
export async function extractEmbeddingResult(
  blob: Blob
): Promise<{ ok: true; embedding: Float32Array } | { ok: false }> {
  try {
    // 当前：直接reject
    // const result = await sherpa.extractEmbedding({ audioPath })
    
    // 改为：先落盘再提取
    const audioPath = await saveTempAudioForEmbedding(blob);
    const result = await sherpa.extractEmbedding({ audioPath });
    return { ok: true, embedding: result.embedding };
  } catch {
    return { ok: false };
  }
}
```

```java
// SherpaPlugin.java 实现extractEmbedding
@PluginMethod
public void extractEmbedding(PluginCall call) {
  String path = call.getString("audioPath");
  
  // 3D-Speaker ECAPA-TDNN 192维向量
  SpeakerEmbeddingExtractorConfig config = 
    SpeakerEmbeddingExtractorConfig.builder()
      .model(modelsDir + "/3dspeaker_speech_eres2net_base_200k_sv_zh-cn_16k-common.onnx")
      .numThreads(1)
      .build();
      
  SpeakerEmbeddingExtractor extractor = 
    new SpeakerEmbeddingExtractor(config);
    
  float[] embedding = extractor.compute(readAudio(path));
  
  JSObject ret = new JSObject();
  ret.put("embedding", embedding);
  ret.put("dim", 192);
  call.resolve(ret);
}
```

**方案B：后端 Diarization Service**

已配置但未联调 (config.go:225)：
```go
DiarizationBaseURL string // POCKET_DIARIZATION_BASE_URL
DiarizationAPIKey  string 
DiarizationTimeout time.Duration // 300s
```

实现调用：
```typescript
// ingest-speech.ts
const [sttResult, embeddingResult, speakerLabel] = await Promise.all([
  sttApi.transcribe(...),
  extractEmbeddingResult(opts.blob),
  // 新增：后端说话人分离
  opts.diarizer.identifyRemote(opts.blob, opts.startMs, opts.endMs)
]);
```

**推荐**：方案A (本地) + 方案B (云端精校)
- 本地ECAPA快速分类 (50ms)
- 云端Pyannote精确聚类 (可选)

### 2.3 标点后处理

**问题**：zipformer无标点，影响可读性

**方案**：轻量标点恢复模型

```typescript
// 新增 punctuation-restore.ts
import ort from 'onnxruntime-node'; // 服务端
// 或使用云端API
async function restorePunctuation(text: string): Promise<string> {
  // 选项1：本地 BERT-wwm 标点模型 (20MB)
  // 选项2：调用 LLM网关 few-shot prompt
  // 选项3：规则+统计混合 (最轻量)
  
  // 规则示例
  return text
    .replace(/(\d+年\d+月\d+日)/g, '$1。')
    .replace(/(好的|明白了|收到)/g, '$1。')
    .replace(/([？！])/g, '$1 ')
    .trim();
}
```

**推荐**：Phase 2.1完成后此项可选 (paraformer自带标点)

## 3. 云端引擎优化

### 3.1 网关候选质量排序

**当前问题**：
- 网关探测逻辑复杂 (stt/target.go)
- glm-asr "易挂" (llm-itestu-gateway-profile.md)
- 故障时全链路失败

**方案**：固定优先级 + 快速失败

```go
// backend/internal/stt/target.go
var chineseASRPriority = []Provider{
  {ID: "mimo-v2.5-asr", Score: 100},      // 最稳定
  {ID: "groq/whisper-large-v3", Score: 90},
  {ID: "minimax-asr-1.0", Score: 80},
  {ID: "glm-asr", Score: 50},             // 易挂，降级
}

func (t *Target) SelectBestProvider(ctx context.Context) Provider {
  // 快速失败：每个候选最多尝试3s
  ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
  defer cancel()
  
  for _, p := range chineseASRPriority {
    if t.probe(ctx, p) {
      return p
    }
  }
  return fallbackProvider // groq whisper
}
```

### 3.2 本地兜底机制增强

**当前问题**：
- 云端失败时返回空 (2026-11-14真机实测)
- 低置信本地结果被丢弃

**方案**：已在 stt.ts 实现 (2026-11-14层3修复)

```typescript
// stt.ts:167 已有
catch (error) {
  // 云端失败且本地有可用文本 → 降级返回本地结果
  if (localBest && !isAbortError(error)) {
    console.warn('[stt] 云端转写失败，降级返回本地低置信结果：', error);
    return localBest; // engine: 'local'
  }
  throw error;
}
```

**补充**：UI提示优化
```vue
<!-- MeetingDetail.vue -->
<template>
  <div v-if="segment.engine === 'local' && segment.confidence < 0.7" 
       class="warning-badge">
    本地转写 (置信度{{ (segment.confidence * 100).toFixed(0) }}%)
  </div>
</template>
```

## 4. 多模型ensemble (可选)

**高星项目实践**：
- [insanely-fast-whisper](https://github.com/Vaibhavs10/insanely-fast-whisper): 多Whisper版本投票
- [whisperX](https://github.com/m-bain/whisperX): 对齐+说话人融合

**方案**：本地+云端结果融合

```typescript
async function ensembleTranscription(
  localResult: SttResult,
  cloudResult: SttResult
): Promise<SttResult> {
  // 字符级编辑距离
  const similarity = levenshteinSimilarity(localResult.text, cloudResult.text);
  
  if (similarity > 0.9 && localResult.confidence > 0.8) {
    // 高一致性：直接用本地 (省成本)
    return localResult;
  } else if (cloudResult.confidence > 0.95) {
    // 云端高置信：用云端
    return cloudResult;
  } else {
    // 混合：云端文本 + 本地时间戳
    return {
      text: cloudResult.text,
      confidence: (localResult.confidence + cloudResult.confidence) / 2,
      engine: 'ensemble'
    };
  }
}
```

## 5. 性能预期

| 指标 | 当前 | Phase 2后 | 改善 |
|------|------|-----------|------|
| 中文字准率 | 85% | 95% | +10% |
| 标点还原 | 无 | 完整 | 质的飞跃 |
| 说话人识别 | 缺失 | 可用 | 从无到有 |
| 云端可靠性 | 单点故障 | 本地兜底 | 可用率99%+ |

## 6. 实施优先级

**P0 (必做)**：
- ✅ 本地兜底机制 (已完成 2026-11-14)
- 🔲 Paraformer替换zipformer
- 🔲 说话人分离 (ECAPA-TDNN)

**P1 (重要)**：
- 🔲 云端候选优先级调整
- 🔲 UI置信度提示

**P2 (可选)**：
- 🔲 Ensemble融合
- 🔲 标点后处理 (如paraformer标点已足够则跳过)

## 7. 学习高星项目的经验

### 7.1 [Faster-Whisper](https://github.com/SYSTRAN/faster-whisper) (10k⭐)

**关键优化**：
- CTranslate2加速：比原版快4倍
- 动态量化：INT8推理
- Batch处理：多段并行

**借鉴**：
```python
# 服务端可选集成
from faster_whisper import WhisperModel
model = WhisperModel("large-v3", device="cpu", compute_type="int8")

segments, info = model.transcribe(
  audio, 
  beam_size=5,
  language="zh",
  vad_filter=True,  # 内置VAD
  vad_parameters=dict(min_silence_duration_ms=500)
)
```

### 7.2 [WhisperX](https://github.com/m-bain/whisperX) (10k⭐)

**关键创新**：
- VAD预处理：跳过静音段
- 强制对齐：word-level时间戳
- 说话人分离：Pyannote集成

**借鉴**：后端全量转写可用
```python
import whisperx
result = whisperx.transcribe(audio, model, language="zh")
result = whisperx.align(result, align_model, audio)
diarize_segments = whisperx.assign_word_speakers(result, diarize_model)
```

### 7.3 [sherpa-onnx](https://github.com/k2-fsa/sherpa-onnx) (2.7k⭐)

**已集成**，学习其最佳实践：
- 模型运行时下载 (已有)
- 多线程配置：CPU档2线程，GPU档4线程
- 内存池复用：减少分配开销

**配置优化**：
```java
OfflineRecognizerConfig config = 
  OfflineRecognizerConfig.builder()
    .numThreads(2)  // CPU友好
    .maxActivePaths(4)  // beam width
    .enableEndpoint(true)  // 端点检测
    .build();
```
