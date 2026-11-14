# 录音转写技术选型报告

**日期**: 2026-11-14  
**基于**: GitHub高星项目调研（2024-2026活跃项目）

---

## 1. 技术选型总览

### 核心技术栈推荐

```
┌─────────────────────────────────────────────────────────┐
│                   录音转写技术架构                        │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  Android录音层                                           │
│  ├─ 录音: OmRecorder (Java) / Android-Wave-Recorder    │
│  ├─ 降噪: rnnoise-kmp (Kotlin Multiplatform)           │
│  └─ VAD: FireRedVAD / Silero VAD                       │
│                                                         │
│  ASR层（二选一）                                         │
│  ├─ 端侧: sherpa-onnx (ONNX Runtime, 跨平台)           │
│  └─ 云端: FunASR (比Whisper快170倍, 50+语言)           │
│                                                         │
│  说话人分离                                              │
│  ├─ 服务端: pyannote-audio + WhisperX                  │
│  └─ 客户端: 声纹库 (ECAPA-TDNN嵌入)                    │
│                                                         │
│  动态参谋                                                │
│  └─ kxmemory + 关键词提取 + 实时推送                    │
│                                                         │
│  多语言                                                  │
│  └─ 检测 + 并行转写 + 翻译pipeline                      │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

---

## 2. 分类技术选型

### 2.1 VAD（语音活动检测）

#### 🏆 推荐: **FireRedVAD**
- **仓库**: https://github.com/FireRedTeam/FireRedVAD
- **性能**: FLEURS-VAD-102上97.57% F1分数
- **优势**:
  - 误报率2.69%（Silero 9.41%，TEN-VAD 15.47%）
  - 支持100+语言语音/歌唱/音乐检测
  - 支持流式和非流式
- **集成方案**:
  ```python
  # 服务端VAD（Python）
  from fireredvad import FireRedVAD
  vad = FireRedVAD()
  segments = vad.detect(audio, sample_rate=16000)
  ```

#### 备选: **Silero VAD**
- **仓库**: https://github.com/snakers4/silero-vad
- **优势**: MIT许可，小巧快速，ONNX支持
- **适用场景**: 需要MIT许可或8kHz电话场景
- **集成方案**:
  ```kotlin
  // Android端（通过ONNX Runtime）
  val sileroVad = SileroVAD(context)
  val isSpeech = sileroVad.detect(audioBuffer)
  ```

#### 备选: **TEN-VAD**
- **仓库**: https://github.com/TEN-framework/ten-vad
- **优势**: 已集成到sherpa-onnx，低延迟
- **适用场景**: 使用sherpa-onnx时自动获得

**选型建议**: 
- 服务端优先FireRedVAD（精度最高）
- Android端优先Silero VAD（轻量）
- 使用sherpa-onnx时自动获得TEN-VAD

---

### 2.2 Android原生录音

#### 🏆 推荐: **OmRecorder**
- **仓库**: https://github.com/LouisP79/OmRecorder
- **优势**:
  - 支持PCM/WAV格式
  - 支持暂停/恢复
  - 支持静音检测和跳过
  - 可配置音频源
- **集成方案**:
  ```java
  Recorder recorder = OmRecorder.wav(
      new PullTransport.Default(mic(), new PullTransport.OnAudioChunkPulledListener() {
          @Override public void onAudioChunkPulled(AudioChunk audioChunk) {
              // 实时音频片段
              processChunk(audioChunk);
          }
      }), file());
  recorder.startRecording();
  ```

#### 备选: **Android-Wave-Recorder**
- **仓库**: https://github.com/squti/Android-Wave-Recorder
- **优势**: Kotlin原生，支持Float和32-bit编码
- **适用场景**: Kotlin项目或需要高精度编码

#### 🏆 降噪: **rnnoise-kmp**
- **仓库**: https://github.com/Enaium/rnnoise-kmp
- **优势**:
  - RNN音频降噪
  - Kotlin Multiplatform（跨平台复用）
  - 同时具备VAD功能
- **集成方案**:
  ```kotlin
  val rnnoise = RNNoise()
  val denoisedAudio = rnnoise.process(audioBuffer)
  val vadProbability = rnnoise.getVadProbability()
  ```

**选型建议**: OmRecorder（录音） + rnnoise-kmp（降噪+VAD）

---

### 2.3 实时语音转写

#### 🏆 推荐（服务端）: **FunASR**
- **仓库**: https://github.com/modelscope/funasr
- **性能**: 比Whisper快170倍
- **优势**:
  - 支持50+语言
  - 离线、流式、边缘部署全覆盖
  - 集成VAD、标点、说话人分离
  - **OpenAI兼容API**（无缝对接现有代码）
  - 训练于数千万小时真实语音
- **部署方案**:
  ```bash
  # Docker部署（推荐用于192.168.31.30）
  docker run -d -p 10095:10095 \
    registry.cn-hangzhou.aliyuncs.com/funasr_repo/funasr:funasr-runtime-sdk-online-cpu-0.1.10
  
  # 兼容OpenAI API
  curl http://192.168.31.30:10095/v1/audio/transcriptions \
    -H "Authorization: Bearer xxx" \
    -F file=@audio.wav \
    -F model=paraformer-zh
  ```
- **集成现有代码**: 无需改动，只需配置环境变量
  ```bash
  POCKET_STT_BASE_URL=http://192.168.31.30:10095/v1
  POCKET_STT_API_KEY=dummy  # FunASR不需要真实key
  POCKET_STT_MODEL=paraformer-zh
  ```

#### 🏆 推荐（端侧）: **sherpa-onnx**
- **仓库**: https://github.com/k2-fsa/sherpa-onnx
- **优势**:
  - 完全离线，无需互联网
  - 跨平台（Android/iOS/嵌入式）
  - 集成ASR、TTS、说话人分离、VAD
  - C++高性能，多语言绑定
- **Android集成**:
  ```kotlin
  // build.gradle
  implementation 'com.k2fsa.sherpa:sherpa-onnx:1.10.0'
  
  // 使用
  val config = OnlineRecognizerConfig(
      modelConfig = OnlineModelConfig(
          paraformer = OnlineParaformerModelConfig(
              encoder = "encoder.onnx",
              decoder = "decoder.onnx"
          )
      )
  )
  val recognizer = OnlineRecognizer(assetManager, config)
  recognizer.acceptWaveform(samples, sampleRate)
  val result = recognizer.getResult()
  ```

#### 备选（云端高精度）: **VibeVoice ASR Streaming**
- **仓库**: https://github.com/microsoft/VibeVoice
- **优势**: 微软最新开源，统一流式ASR，支持热词
- **适用场景**: 需要最高精度或热词定制

#### 备选（高性能）: **faster-whisper**
- **仓库**: https://github.com/SYSTRAN/faster-whisper
- **性能**: 比openai/whisper快4倍
- **适用场景**: 已有Whisper模型，需要提速

**选型建议**:
- **主力方案**: FunASR服务端 + sherpa-onnx端侧（离线备份）
- **原因**: FunASR性能卓越且OpenAI兼容，sherpa-onnx完全离线可用

---

### 2.4 说话人识别/分离

#### 🏆 推荐: **pyannote-audio + WhisperX**
- **pyannote-audio仓库**: https://github.com/pyannote/pyannote-audio
- **WhisperX仓库**: https://github.com/m-bain/whisperX
- **优势**:
  - pyannote是工业标准，SOTA性能
  - WhisperX结合ASR和说话人分离
  - 精确词级时间戳
  - large-v2模型70倍实时速度
- **部署方案**:
  ```python
  # 服务端API（部署在192.168.31.30）
  from pyannote.audio import Pipeline
  import whisperx
  
  # 说话人分离
  diarization = Pipeline.from_pretrained(
      "pyannote/speaker-diarization-3.1",
      use_auth_token="HF_TOKEN"
  )
  
  # ASR + 分离
  model = whisperx.load_model("large-v2", device="cuda")
  result = model.transcribe(audio)
  result = whisperx.align(result["segments"], model, audio)
  diarize_segments = diarization(audio)
  result = whisperx.assign_word_speakers(diarize_segments, result)
  
  # 返回格式
  # [{"speaker": "SPEAKER_00", "text": "...", "start": 0.5, "end": 3.2}, ...]
  ```

#### 备选: **WhoSpeaks**
- **仓库**: https://github.com/KoljaB/WhoSpeaks
- **优势**: 更轻量，速度更快
- **适用场景**: 资源受限环境

**选型建议**: pyannote-audio + WhisperX（精度和集成度最佳）

---

## 3. 推荐技术组合

### 方案A: 混合架构（推荐）

**适用场景**: 生产环境，平衡离线和在线能力

```
┌──────────────────────────────────────────────────┐
│                  Android客户端                    │
├──────────────────────────────────────────────────┤
│ 录音: OmRecorder                                  │
│ 降噪: rnnoise-kmp                                │
│ VAD: Silero VAD (ONNX)                          │
│ 离线ASR: sherpa-onnx (降级/无网)                 │
└──────────────────────────────────────────────────┘
                      ↓ WebSocket/HTTP
┌──────────────────────────────────────────────────┐
│              后端服务 (pocketd)                   │
├──────────────────────────────────────────────────┤
│ 流式转写: FunASR (192.168.31.30)                 │
│ 说话人分离: pyannote + WhisperX                   │
│ 动态参谋: kxmemory检索                            │
│ 精转总结: LLM Gateway                            │
└──────────────────────────────────────────────────┘
```

**成本**: 
- 开发成本: 中等（需要Android原生开发）
- 运行成本: 低（本地FunASR）
- 维护成本: 中等

**优势**:
- 离线可用（sherpa-onnx）
- 在线高精度（FunASR）
- 自动降级策略

---

### 方案B: 纯云端架构

**适用场景**: 快速迭代，不关心离线

```
┌──────────────────────────────────────────────────┐
│                  Android客户端                    │
├──────────────────────────────────────────────────┤
│ 录音: OmRecorder                                  │
│ 降噪: rnnoise-kmp                                │
│ VAD: 客户端简单能量检测                           │
└──────────────────────────────────────────────────┘
                      ↓ 音频流上传
┌──────────────────────────────────────────────────┐
│              后端服务 (pocketd)                   │
├──────────────────────────────────────────────────┤
│ VAD: FireRedVAD                                  │
│ ASR: FunASR                                      │
│ 说话人: pyannote + WhisperX                       │
│ 其他: 同方案A                                     │
└──────────────────────────────────────────────────┘
```

**成本**:
- 开发成本: 低（Android只做录音）
- 运行成本: 低（本地部署）
- 维护成本: 低

**优势**:
- 快速上线
- 统一服务端处理
- 易于优化和监控

---

### 方案C: 纯端侧架构

**适用场景**: 极致隐私，完全离线

```
┌──────────────────────────────────────────────────┐
│                  Android客户端                    │
├──────────────────────────────────────────────────┤
│ 录音: OmRecorder                                  │
│ 降噪: rnnoise-kmp                                │
│ VAD: Silero VAD                                  │
│ ASR: sherpa-onnx (Paraformer-zh)                │
│ 本地总结: TinyLLM (MobileVLM/Phi-3-mini)        │
└──────────────────────────────────────────────────┘
                      ↓ 仅元数据同步
┌──────────────────────────────────────────────────┐
│              后端服务 (pocketd)                   │
├──────────────────────────────────────────────────┤
│ 仅存储: 会议元数据、同步状态                      │
│ 不接触: 音频、转写内容                           │
└──────────────────────────────────────────────────┘
```

**成本**:
- 开发成本: 高（复杂Android集成）
- 运行成本: 极低（无云端依赖）
- 维护成本: 高（多模型管理）

**优势**:
- 完全隐私
- 完全离线
- 无运行成本

---

## 4. 最终推荐

### 🎯 第一阶段（2周，P0）: **方案B（纯云端）**

**理由**:
1. **快速验证**: Android只需实现录音，2-3天可完成
2. **低风险**: 服务端已有完整STT基础设施
3. **易调试**: 问题集中在服务端
4. **高精度**: FunASR性能卓越

**实施路径**:
```
Week 1:
- Day 1-2: Android OmRecorder + rnnoise-kmp集成
- Day 3-4: 后端FunASR部署到192.168.31.30
- Day 5: 前后端联调

Week 2:
- Day 1-2: pyannote说话人分离集成
- Day 3-4: 动态参谋（kxmemory检索）
- Day 5: 端到端测试
```

### 🚀 第二阶段（4周，P1）: **升级为方案A（混合架构）**

**理由**:
1. 方案B已验证可行
2. 用户需求离线能力
3. 降低长期云端成本

**实施路径**:
```
Week 3-4:
- sherpa-onnx Android集成
- 离线模型下载与管理
- 在线/离线自动切换

Week 5-6:
- 精转与总结pipeline
- 多语言支持
- 性能优化
```

---

## 5. 模型部署清单

### 192.168.31.30服务器部署

```yaml
服务清单:
  - FunASR Runtime:
      端口: 10095
      模型: paraformer-zh, whisper-large-v3
      内存: 4GB
      
  - pyannote-audio API:
      端口: 8002
      模型: speaker-diarization-3.1
      内存: 6GB
      GPU: 可选（提速10倍）
      
  - Silero VAD (可选):
      端口: 8003
      模型: silero_vad.onnx
      内存: 512MB

总资源需求:
  内存: 12GB (无GPU) / 16GB (有GPU)
  磁盘: 20GB (模型文件)
  带宽: 内网，无限制
```

### Android APK内置模型

```yaml
可选模型（按需打包）:
  - Silero VAD: 2MB (silero_vad.onnx)
  - sherpa-onnx Paraformer: 220MB (压缩后)
    - encoder.onnx: 110MB
    - decoder.onnx: 80MB
    - tokens.txt: 200KB
    
建议策略:
  - 默认不打包ASR模型（首次使用时下载）
  - 必打包VAD模型（2MB可接受）
  - 提供"离线包"APK变体（含全部模型）
```

---

## 6. 性能指标预期

### 延迟指标

| 场景 | 方案B（云端） | 方案A（混合） | 方案C（端侧） |
|------|--------------|--------------|--------------|
| 首字延迟 | 200-500ms | 150-300ms | 100-200ms |
| 实时率 | 10-50倍 | 5-20倍 | 2-5倍 |
| 说话人标注 | +500ms | +500ms | 不支持 |

### 精度指标

| 指标 | FunASR | sherpa-onnx | Whisper |
|------|--------|-------------|---------|
| 中文CER | 3-5% | 5-8% | 4-6% |
| 英文WER | 5-8% | 8-12% | 6-9% |
| 说话人错误率 | 8-12% | N/A | 8-12% |

### 资源占用

| 组件 | CPU | 内存 | 带宽 |
|------|-----|------|------|
| Android录音 | 5-10% | 50MB | - |
| sherpa-onnx | 30-60% | 300MB | - |
| FunASR(服务端) | 100-200% | 2GB | 50KB/s |
| pyannote(服务端) | 50-100% | 4GB | - |

---

## 7. 风险与缓解

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|----------|
| 192.168.31.30无GPU | 延迟2-3倍 | 中 | CPU版本仍可用，或租用GPU服务器 |
| FunASR部署失败 | P0阻塞 | 低 | Docker镜像成熟，回退faster-whisper |
| Android后台录音被杀 | 用户体验差 | 中 | 前台Service + 用户教育 |
| 说话人分离精度低 | 功能受限 | 中 | 降级为手动标注 |
| sherpa-onnx模型过大 | 安装包膨胀 | 高 | 首次使用时在线下载 |

---

## 8. 许可证合规性

| 项目 | 许可证 | 商用 | 注意事项 |
|------|--------|------|----------|
| FunASR | Apache 2.0 | ✅ | 无限制 |
| sherpa-onnx | Apache 2.0 | ✅ | 无限制 |
| pyannote-audio | MIT | ✅ | 无限制 |
| Silero VAD | MIT | ✅ | 无限制 |
| OmRecorder | Apache 2.0 | ✅ | 无限制 |
| rnnoise-kmp | BSD-3-Clause | ✅ | 需保留版权声明 |
| FireRedVAD | Apache 2.0 | ✅ | 无限制 |
| WhisperX | BSD-4-Clause | ✅ | 需保留版权声明 |
| TEN-VAD | Apache 2.0+ | ⚠️ | 有附加条款，需审查 |

**结论**: 推荐技术栈均可商用，无重大许可风险。

---

**更新记录**:
- 2026-11-14: 初版，基于GitHub最新调研
