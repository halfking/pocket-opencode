# 录音与转写功能优化实施方案

**日期**: 2026-11-14  
**目标**: 实现原生录音、实时转写、说话人分离、动态参谋、多语言并行转写

---

## 1. 现状分析

### 1.1 已有能力

#### 后端 (Go)
- ✅ 完整的STT基础设施
  - `/api/stt/transcribe` - 单次转写（120秒超时）
  - `/api/stt/transcribe-full` - 全量转写（自动VAD分段，10分钟超时）
  - `/api/stt/transcribe-incremental` - 即时转写（会话态去重）
- ✅ 多provider支持：OpenAI兼容、MiniMax、智谱
- ✅ 自动目标解析（resolveSTTTarget）
- ✅ 会议元数据管理（PostgreSQL）
- ✅ kxmemory集成（知识检索）

#### 前端 (Vue3 + Capacitor)
- ✅ 会议工作台UI（录音、转写、总结、待办）
- ✅ VAD分段（约1.5秒静音边界）
- ✅ Web Speech API兼容
- ✅ 本地会议存储（local_meetings）

#### Android
- ✅ MainActivity注册了BackgroundMicPlugin
- ✅ 权限管理（RECORD_AUDIO）
- ⚠️ SherpaPlugin已注册但100%未实现（只reject）

### 1.2 核心痛点

1. **本地ASR完全不可用**
   - sherpa-onnx骨架存在但无实现
   - 无.onnx模型文件
   - 100%依赖云端，有网络/成本/隐私问题

2. **无说话人识别/分离**
   - 无声纹库
   - 无说话人标注UI
   - 转写结果无speaker标签

3. **无动态参谋**
   - 录音时无实时知识检索
   - 无上下文建议
   - 与kxmemory未联动

4. **无多语言并行**
   - 只支持单一语言转写
   - 无翻译pipeline
   - 无对照视图

5. **任务提取不完善**
   - 待办提取依赖后处理
   - 无实时任务识别
   - 智能体关联是手动的

---

## 2. 优化目标

### P0 - 核心功能（2周）

1. ✅ **原生录音模块**
   - 后台录音Service（Android前台服务）
   - 降噪（AudioRecord + AcousticEchoCanceler）
   - 实时VAD（webrtc-vad或silero-vad）
   - 音频缓冲与分片

2. ✅ **实时流式转写**
   - 利用现有的`/api/stt/transcribe-incremental`
   - 多模型候选（glm-asr、minimax-asr）
   - 断网降级策略

3. ✅ **模型配置与测试**
   - 验证llm.itestu.cn可用性
   - 配置192.168.31.30本地模型
   - 性能基准测试

### P1 - 高级功能（3周）

4. **说话人分离**
   - 集成pyannote.audio或类似方案
   - 本地声纹库（SQLite）
   - 说话人标注UI

5. **动态参谋**
   - 实时关键词提取
   - kxmemory检索集成
   - 上下文建议推送

6. **精转与总结**
   - 二次精校pipeline
   - 自动总结（按技能）
   - 多维度分析

### P2 - 扩展功能（4周）

7. **多语言并行**
   - 语言检测
   - 并行转写+翻译
   - 对照视图

8. **任务智能体联动**
   - 实时任务识别
   - 自动生成提示词
   - ACC任务关联与跟踪

---

## 3. 技术方案

### 3.1 原生录音模块

#### Android实现
```
目标：实现BackgroundMicPlugin和SherpaPlugin

组件：
1. ForegroundRecordingService
   - 前台通知（避免被杀）
   - AudioRecord配置（16kHz, MONO, 16bit）
   - 实时VAD检测
   - 音频分片（3秒/片）

2. AudioProcessor
   - AcousticEchoCanceler（回声消除）
   - NoiseSuppressor（降噪）
   - AutomaticGainControl（增益控制）

3. VadDetector
   - 优先webrtc-vad-android
   - 备选：自实现能量阈值VAD
```

#### Capacitor插件接口
```typescript
interface BackgroundMicPlugin {
  startRecording(options: RecordOptions): Promise<{sessionId: string}>
  stopRecording(options: {sessionId: string}): Promise<{audioPath: string}>
  pauseRecording(options: {sessionId: string}): Promise<void>
  resumeRecording(options: {sessionId: string}): Promise<void>
  addListener(event: 'audioChunk', callback: (chunk: AudioChunk) => void)
}

interface SherpaPlugin {
  initialize(model: ModelConfig): Promise<void>
  transcribeFile(path: string): Promise<TranscriptionResult>
  startStreaming(): Promise<void>
  stopStreaming(): Promise<TranscriptionResult>
  addListener(event: 'partialResult', callback: (text: string) => void)
}
```

### 3.2 实时流式转写

```typescript
// 改进后的录音器
class EnhancedMeetingRecorder {
  private backgroundSession?: string
  private streamingEnabled = false
  private audioChunks: AudioChunk[] = []
  
  async startRecording(options: {
    enableStreaming: boolean
    enableSpeakerDiarization: boolean
    enableDynamicAdvisor: boolean
  }) {
    // 1. 启动原生后台录音
    const { sessionId } = await BackgroundMic.startRecording({
      sampleRate: 16000,
      channels: 1,
      vadEnabled: true,
      chunkDurationMs: 3000
    })
    
    // 2. 监听音频片段
    BackgroundMic.addListener('audioChunk', async (chunk) => {
      // 即时转写
      if (this.streamingEnabled) {
        await this.transcribeChunk(chunk)
      }
      
      // 动态参谋
      if (options.enableDynamicAdvisor) {
        await this.advisorPipeline(chunk.text)
      }
    })
    
    // 3. 启动流式识别（如果支持）
    if (options.enableStreaming) {
      try {
        await Sherpa.startStreaming()
        this.streamingEnabled = true
      } catch {
        // 降级到云端分片转写
        this.streamingEnabled = false
      }
    }
  }
  
  private async transcribeChunk(chunk: AudioChunk) {
    const res = await sttApi.transcribeIncremental({
      sessionId: this.backgroundSession,
      audioBase64: chunk.audioBase64,
      module: 'meetings'
    })
    
    // 更新实时转写结果
    this.updateTranscript(res.incremental)
    
    // 触发动态参谋
    if (res.incremental.trim()) {
      this.triggerAdvisor(res.incremental)
    }
  }
}
```

### 3.3 说话人分离方案

#### 方案A：云端分离（推荐启动）
```
流程：
1. 录音完成后上传音频
2. 后端调用pyannote.audio API
3. 返回带时间戳的speaker segments
4. 前端渲染说话人标签

优点：精度高、无需本地模型
缺点：有网络依赖、延迟高
```

#### 方案B：本地声纹（长期）
```
流程：
1. 用户标注关键说话人（"张三"、"李四"）
2. 提取声纹特征存本地
3. 录音时实时匹配
4. 渐进式学习优化

优点：隐私、实时、离线
缺点：冷启动需标注、精度较低
```

### 3.4 动态参谋集成

```typescript
class DynamicAdvisor {
  private keywordBuffer: string[] = []
  private lastQueryTime = 0
  
  async onTranscriptUpdate(newText: string) {
    // 1. 关键词提取
    const keywords = this.extractKeywords(newText)
    this.keywordBuffer.push(...keywords)
    
    // 2. 防抖查询（5秒一次）
    const now = Date.now()
    if (now - this.lastQueryTime < 5000) return
    this.lastQueryTime = now
    
    // 3. 检索相关知识
    if (this.keywordBuffer.length > 0) {
      const query = this.keywordBuffer.join(' ')
      const suggestions = await this.queryKnowledge(query)
      
      // 4. 推送建议到右侧面板
      this.pushSuggestions(suggestions)
      
      this.keywordBuffer = []
    }
  }
  
  private async queryKnowledge(query: string) {
    // 并行查询多个源
    const [notes, memora, web] = await Promise.allSettled([
      notesApi.search(query, { limit: 3 }),
      memoraApi.search(query, { limit: 3 }),
      this.shouldQueryWeb() ? webSearch(query) : null
    ])
    
    return this.mergeResults(notes, memora, web)
  }
}
```

### 3.5 多语言并行转写

```typescript
interface MultilingualTranscription {
  async transcribeWithTranslation(audio: Blob, options: {
    sourceLanguage?: string  // 自动检测
    targetLanguages: string[]  // ['en', 'zh']
  }) {
    // 1. 主语言转写
    const primary = await sttApi.transcribe({
      audioBlob: audio,
      module: 'meetings'
    })
    
    // 2. 语言检测
    const detected = options.sourceLanguage || 
                     await this.detectLanguage(primary.text)
    
    // 3. 并行翻译
    const translations = await Promise.all(
      options.targetLanguages.map(lang => 
        this.translate(primary.text, detected, lang)
      )
    )
    
    return {
      primary: { lang: detected, text: primary.text },
      translations: translations.map((text, i) => ({
        lang: options.targetLanguages[i],
        text
      }))
    }
  }
}
```

---

## 4. 模型配置方案

### 4.1 云端模型配置

```bash
# llm.itestu.cn（已确认无ASR，仅LLM）
POCKET_LLM_GATEWAY_URL=https://llm.itestu.cn/v1
POCKET_LLM_GATEWAY_API_KEY=${POCKET_LLM_GATEWAY_API_KEY}

# 需要单独配置ASR端点（待确认）
# 选项1：使用智谱单独的ASR API
POCKET_STT_ZHIPU_API_KEY=xxx
POCKET_STT_ZHIPU_MODEL=glm-4-asr

# 选项2：使用MiniMax ASR
POCKET_STT_MINIMAX_API_KEY=xxx
POCKET_STT_MINIMAX_MODEL=speech-01-240228

# 选项3：配置支持ASR的OpenAI兼容端点
POCKET_STT_BASE_URL=https://api.another-gateway.cn/v1
POCKET_STT_API_KEY=xxx
POCKET_STT_MODEL=whisper-large-v3
```

### 4.2 本地模型配置（192.168.31.30）

```bash
# 需要验证该服务器上的实际可用模型
# 可能的选项：

# 1. 本地Whisper服务
POCKET_STT_LOCAL_URL=http://192.168.31.30:8000/v1
POCKET_STT_LOCAL_MODEL=whisper-large-v3-turbo

# 2. 本地Paraformer（中文优化）
POCKET_STT_LOCAL_PARAFORMER_URL=http://192.168.31.30:8001
POCKET_STT_LOCAL_PARAFORMER_MODEL=paraformer-zh

# 3. 说话人分离
POCKET_DIARIZATION_URL=http://192.168.31.30:8002
POCKET_DIARIZATION_MODEL=pyannote-audio-3.0
```

---

## 5. 实施步骤

### Week 1-2: P0核心功能

**Day 1-3: 原生录音模块**
- [ ] 实现ForegroundRecordingService
- [ ] 集成AudioRecord + 音频处理
- [ ] 实现VAD检测
- [ ] Capacitor插件封装

**Day 4-5: 实时转写对接**
- [ ] 改造useMeetingRecorder支持流式
- [ ] 对接后端incremental端点
- [ ] 实现断网降级

**Day 6-7: 模型配置与测试**
- [ ] 检查192.168.31.30可用模型
- [ ] 配置STT目标解析
- [ ] 性能基准测试

### Week 3-4: P1高级功能

**Day 8-10: 说话人分离**
- [ ] 后端pyannote API对接
- [ ] 前端说话人标注UI
- [ ] 本地声纹库（V2）

**Day 11-12: 动态参谋**
- [ ] 关键词提取
- [ ] kxmemory实时检索
- [ ] 建议推送UI

**Day 13-14: 精转总结**
- [ ] 二次精校pipeline
- [ ] 多技能总结
- [ ] 任务自动提取

### Week 5-8: P2扩展功能

**Week 5-6: 多语言支持**
- [ ] 语言检测
- [ ] 并行转写翻译
- [ ] 对照视图UI

**Week 7-8: 智能体联动**
- [ ] 实时任务识别
- [ ] 提示词生成
- [ ] ACC任务跟踪

---

## 6. 验收标准

### P0
- ✅ Android后台录音稳定运行30分钟以上
- ✅ 实时转写延迟<5秒
- ✅ 至少有2个可用的云端ASR模型
- ✅ 离线录音可正常保存并后处理

### P1
- ✅ 说话人分离精度>80%（2人场景）
- ✅ 动态参谋推荐相关度>70%
- ✅ 自动总结可读性满足用户需求

### P2
- ✅ 支持中英双语并行转写
- ✅ 任务自动提取准确率>60%
- ✅ 智能体任务执行成功率>80%

---

## 7. 风险与缓解

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| llm.itestu.cn无ASR | P0阻塞 | 配置192.168.31.30本地模型 |
| 192.168.31.30无模型 | P0阻塞 | 安装Whisper/Paraformer |
| Android后台限制 | 功能受限 | 前台Service + 用户引导 |
| 说话人分离精度低 | 用户体验差 | 降级为手动标注 |
| 实时转写成本高 | 运营压力 | 本地模型 + 用量限制 |

---

## 8. 后续优化方向

1. **端侧模型**
   - 集成Whisper.cpp到Android
   - ONNX Runtime优化
   - 量化模型（INT8）

2. **声纹技术**
   - ECAPA-TDNN声纹提取
   - 声纹注册与管理
   - 渐进式学习

3. **多模态**
   - 屏幕录制+OCR
   - 视频会议集成
   - 手写笔记融合

4. **协作功能**
   - 多设备同步录音
   - 会议角色标注
   - 实时协作标注

---

**负责人**: AI Development Team  
**审阅人**: 待定  
**版本**: v0.1.0
