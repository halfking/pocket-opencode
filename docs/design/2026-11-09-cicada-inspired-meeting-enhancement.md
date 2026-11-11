# Cicada 启发的会议增强方案

> **状态**: 📋 分析与设计阶段  
> **日期**: 2026-11-09  
> **目标**: 学习 cicada 的交互模式与功能特性，整合到 OpenPocket 现有能力，支持 Android/iOS/macOS 三平台

---

## 1. Cicada 核心能力分析

### 1.1 Cicada 的关键特性

| 特性 | 描述 | 技术实现 |
|------|------|----------|
| **实时转写与翻译** | 会议/流媒体的实时双语字幕 | 云端 ASR + 翻译 API；macOS 支持本地 SpeechAnalyzer |
| **AI 会议笔记** | 结构化总结（要点、结论、行动项、鱼骨图） | LLM 实时流式生成 |
| **同声传译** | 按键说话→即时翻译→虚拟麦克风输出 | 按键触发 + TTS + 虚拟音频设备 |
| **说话人识别** | 本地声纹模型自动区分说话人 | WeSpeaker ResNet34-LM 模型 |
| **系统音频捕获** | 捕获会议软件音频（不只是麦克风） | macOS 屏幕录制权限；Windows/Linux 虚拟设备 |
| **虚拟麦克风输出** | 翻译后的语音输出到会议软件 | BlackHole 2ch (macOS)；VB-Cable (Windows) |
| **隐私优先** | 音频不离设备（Apple Silicon 本地转写） | 本地 ASR；只发送文本到翻译 API |
| **纠错机制** | AI 修正同音字和术语错误 | LLM 后处理 |

### 1.2 Cicada 的交互模式

1. **实时字幕悬浮窗**: 始终置顶，显示双语字幕流
2. **按键触发翻译**: 右 ⌥ (Mac) / 右 Ctrl (Windows) 按住说话
3. **一键混合会议**: 切换同时捕获麦克风+系统音频
4. **会后整理**: 自动生成可搜索转写本、导出 Markdown/Word/SRT
5. **说话人编辑**: 手动修改说话人名称、合并说话人
6. **跳转播放**: 点击任意句子跳转到该时间点播放

---

## 2. OpenPocket 现有能力盘点

### 2.1 已有能力（✅ 可直接复用）

| 能力域 | 现有实现 | 文件位置 |
|--------|----------|----------|
| **录音引擎** | MediaRecorder + 后台服务 | `frontend/src/native/recordingRuntime.ts` |
| **音频转码** | webm→wav 16kHz 单声道 | `frontend/src/utils/wav-encode.ts` |
| **逐簇解码** | webm 增量解析（降内存峰值） | `frontend/src/utils/webm-incremental.ts` |
| **云端转写** | STT 网关（Groq/智谱/MiniMax） | `backend/internal/stt/transcribe.go` |
| **说话人分离** | 声纹 embedding + 聚类 | `frontend/src/native/speaker-diarization.ts` |
| **会议管理** | 创建/停止/转写/总结 | `backend/internal/meeting/` |
| **AI 总结** | LLM 流式生成会议总结 | `backend/internal/meeting/` (已有) |
| **跨平台抽象** | Android/iOS/Web 统一接口 | `frontend/src/native/pocket-native.ts` |
| **后台保活** | Android FGS + 通知 | `aiStreamRuntime.ts` (已验证 30min+) |
| **文件系统** | 跨平台文件读写 | `PocketFilesystem` (Phase 9.1 已落地) |
| **分享功能** | 系统分享面板 | `PocketShare` (Phase 9.3 已落地) |

### 2.2 能力缺口（❌ 需要新增）

| 缺口 | Cicada 实现 | OpenPocket 需求 |
|------|-------------|------------------|
| **实时字幕悬浮窗** | 原生窗口始终置顶 | Android/iOS 悬浮窗权限 + 原生 UI |
| **系统音频捕获** | 屏幕录制权限 (macOS) | Android 内录 API 29+；iOS 需 ReplayKit |
| **按键触发录音** | 硬件键监听 (⌥/Ctrl) | Android 音量键长按；iOS 快捷方式 |
| **实时翻译流** | 边转写边翻译 | LLM 网关增加翻译端点 |
| **虚拟麦克风输出** | 虚拟音频驱动 | Android 不支持；iOS 限制；仅 macOS 可行 |
| **本地 ASR** | Apple SpeechAnalyzer (macOS) | Android sherpa-onnx；iOS Speech Framework |
| **语音克隆 TTS** | 云端 API | 可选特性（成本高） |
| **点击跳转播放** | 音频播放器 + 时间戳 | 前端播放器组件 |

### 2.3 架构优势

OpenPocket 相比 Cicada 的**既有优势**：

1. **已有完整后端**: Go 后端 + PostgreSQL，Cicada 是纯客户端
2. **多租户隔离**: 企业级权限管理，Cicada 是单用户
3. **跨端统一**: Vue 3 前端跨 Android/iOS/Web，Cicada 各平台独立实现
4. **AI 编排能力**: 已有 AI 流式生成 + 多模型切换，Cicada 依赖外部 Ling API
5. **移动优先**: Android 已验证真机 30min 后台保活，Cicada 移动端是次要平台

---

## 3. 整合优化方案

### 3.1 Phase A：实时字幕与翻译（高优先级）

**目标**: 实现会议实时字幕 + 可选双语翻译

#### 3.1.1 前端改造

**新增组件**: `frontend/src/features/meetings/LiveSubtitleOverlay.vue`

```vue
<!-- 悬浮字幕窗口：始终置顶，半透明背景，自动滚动 -->
<template>
  <div v-if="visible" class="subtitle-overlay" :style="overlayStyle">
    <div class="subtitle-content">
      <div v-for="seg in recentSegments" :key="seg.id" class="subtitle-line">
        <span class="speaker">{{ seg.speaker }}</span>
        <span class="text-primary">{{ seg.text }}</span>
        <span v-if="seg.translation" class="text-secondary">{{ seg.translation }}</span>
      </div>
    </div>
    <div class="subtitle-controls">
      <button @click="toggleTranslation">{{ translationEnabled ? '关闭翻译' : '开启翻译' }}</button>
      <button @click="togglePin">{{ pinned ? '取消置顶' : '置顶' }}</button>
    </div>
  </div>
</template>
```

**状态管理**: `frontend/src/stores/liveSubtitle.ts`

```typescript
export const useLiveSubtitleStore = defineStore('liveSubtitle', () => {
  const segments = ref<SubtitleSegment[]>([])
  const translationEnabled = ref(false)
  const targetLang = ref('zh') // 目标语言
  const overlayVisible = ref(false)
  const overlayPosition = ref({ x: 20, y: 100 })
  
  // 实时接收转写片段
  function addSegment(seg: SubtitleSegment) {
    segments.value.push(seg)
    if (translationEnabled.value && !seg.translation) {
      requestTranslation(seg.id, seg.text)
    }
  }
  
  // 请求翻译（防抖）
  const requestTranslation = useDebounceFn(async (id: string, text: string) => {
    const result = await api.translateText({ text, targetLang: targetLang.value })
    const seg = segments.value.find(s => s.id === id)
    if (seg) seg.translation = result.translation
  }, 500)
  
  return { segments, translationEnabled, addSegment, ... }
})
```

#### 3.1.2 后端改造

**新增端点**: `backend/internal/server/meeting_translate.go`

```go
// POST /api/meetings/{id}/translate
// 实时翻译片段（流式返回）
func (s *Server) handleMeetingTranslate(c echo.Context) error {
    type Req struct {
        Text       string `json:"text"`
        SourceLang string `json:"sourceLang"` // auto-detect
        TargetLang string `json:"targetLang"` // zh/en/ja/ko
    }
    var req Req
    if err := c.Bind(&req); err != nil {
        return echo.NewHTTPError(http.StatusBadRequest, err.Error())
    }
    
    // 调用 LLM 网关翻译端点
    result, err := s.llmGateway.Translate(c.Request().Context(), req.Text, req.TargetLang)
    if err != nil {
        return echo.NewHTTPError(http.StatusBadGateway, err.Error())
    }
    
    return c.JSON(http.StatusOK, result)
}
```

**集成点**: 复用现有 `llmgateway` 模块，增加翻译方法

#### 3.1.3 Android 原生增强

**悬浮窗权限**: `android/app/src/main/java/com/kaixuan/opencode/pocket/OverlayWindowPlugin.java`

```java
@CapacitorPlugin(name = "OverlayWindow")
public class OverlayWindowPlugin extends Plugin {
    @PluginMethod
    public void show(PluginCall call) {
        if (!Settings.canDrawOverlays(getContext())) {
            call.reject("OVERLAY_PERMISSION_DENIED");
            return;
        }
        // 创建悬浮窗 WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY
        // 加载 WebView 渲染 Vue 组件
    }
}
```

**iOS 限制**: iOS 不支持系统级悬浮窗；改用应用内画中画（PiP）模式

---

### 3.2 Phase B：系统音频捕获（中优先级）

**目标**: 捕获会议软件音频（不只是麦克风）

#### 3.2.1 Android 实现 (API 29+)

**新增 Plugin**: `android/app/src/main/java/com/kaixuan/opencode/pocket/SystemAudioPlugin.java`

```java
@RequiresApi(29)
@CapacitorPlugin(name = "SystemAudio")
public class SystemAudioPlugin extends Plugin {
    private MediaProjection projection;
    private AudioRecord recorder;
    
    @PluginMethod
    public void startCapture(PluginCall call) {
        // 1. 请求 MediaProjection 权限
        Intent intent = mediaProjectionManager.createScreenCaptureIntent();
        startActivityForResult(call, intent, "captureResult");
    }
    
    @ActivityCallback
    private void captureResult(PluginCall call, ActivityResult result) {
        projection = mediaProjectionManager.getMediaProjection(result);
        // 2. 创建 AudioPlaybackCaptureConfiguration
        AudioPlaybackCaptureConfiguration config = 
            new AudioPlaybackCaptureConfiguration.Builder(projection)
                .addMatchingUsage(AudioAttributes.USAGE_MEDIA)
                .addMatchingUsage(AudioAttributes.USAGE_VOICE_COMMUNICATION)
                .build();
        // 3. 启动 AudioRecord
        recorder = new AudioRecord.Builder()
            .setAudioPlaybackCaptureConfig(config)
            .setAudioFormat(new AudioFormat.Builder()
                .setEncoding(AudioFormat.ENCODING_PCM_16BIT)
                .setSampleRate(16000)
                .setChannelMask(AudioFormat.CHANNEL_IN_MONO)
                .build())
            .build();
        recorder.startRecording();
    }
}
```

#### 3.2.2 iOS 实现

**使用 ReplayKit**: `ios/App/App/SystemAudioPlugin.swift`

```swift
import ReplayKit

@objc(SystemAudioPlugin)
public class SystemAudioPlugin: CAPPlugin {
    private let recorder = RPScreenRecorder.shared()
    
    @objc func startCapture(_ call: CAPPluginCall) {
        recorder.isMicrophoneEnabled = true // 同时捕获麦克风
        recorder.startCapture(handler: { (sampleBuffer, bufferType, error) in
            if bufferType == .audioApp {
                // 处理系统音频
                self.processSampleBuffer(sampleBuffer)
            }
        }) { error in
            if let error = error {
                call.reject(error.localizedDescription)
            } else {
                call.resolve()
            }
        }
    }
}
```

#### 3.2.3 混合模式

**前端接口**: `frontend/src/native/pocket-native.ts` 扩展

```typescript
export interface PocketRecorder {
  start(opts?: { 
    sampleRate?: number
    format?: 'aac' | 'wav'
    captureMode?: 'microphone' | 'system' | 'hybrid' // ← 新增
  }): Promise<{ sessionId: string }>
  // ...
}
```

**逻辑**: 
- `microphone`: 只录麦克风（当前行为）
- `system`: 只录系统音频（会议软件输出）
- `hybrid`: 混合录制（麦克风+系统音频）

---

### 3.3 Phase C：按键触发与快捷操作（中优先级）

**目标**: 实现 Cicada 的 "按住说话" 快捷翻译

#### 3.3.1 Android 音量键监听

**原生拦截**: `android/app/src/main/java/com/kaixuan/opencode/pocket/HardwareKeyPlugin.java`

```java
@Override
public boolean dispatchKeyEvent(KeyEvent event) {
    if (event.getKeyCode() == KeyEvent.KEYCODE_VOLUME_DOWN) {
        if (event.getAction() == KeyEvent.ACTION_DOWN) {
            // 长按开始录音
            if (event.getRepeatCount() == 0) {
                startQuickTranslate();
            }
        } else if (event.getAction() == KeyEvent.ACTION_UP) {
            // 松开停止并翻译
            stopAndTranslate();
        }
        return true; // 拦截系统音量调节
    }
    return super.dispatchKeyEvent(event);
}
```

#### 3.3.2 iOS 快捷方式

**Siri Shortcuts Integration**: 创建 `QuickTranslate` intent

```swift
class QuickTranslateIntent: INIntent {
    // 用户可在设置中绑定到硬件按钮（如 Action Button）
}
```

#### 3.3.3 前端快捷翻译流程

```typescript
// frontend/src/features/meetings/useQuickTranslate.ts
export function useQuickTranslate() {
  const { recorder } = getPocketNative()
  const llmGateway = useLLMGateway()
  
  async function startQuickTranslate() {
    // 1. 震动反馈
    await Haptics.impact({ style: 'medium' })
    // 2. 开始录音
    const { sessionId } = await recorder.start({ format: 'wav' })
    quickSessionId.value = sessionId
    // 3. 显示录音指示
    showRecordingIndicator()
  }
  
  async function stopAndTranslate() {
    // 1. 停止录音
    const { uri } = await recorder.stop(quickSessionId.value)
    // 2. 转写
    const transcript = await api.transcribe({ audioUri: uri })
    // 3. 翻译
    const translation = await api.translate({ 
      text: transcript.text, 
      targetLang: 'en' 
    })
    // 4. 朗读翻译结果（TTS）
    await speak(translation.text)
    // 5. 复制到剪贴板
    await Clipboard.write({ string: translation.text })
    // 6. Toast 提示
    showToast('已翻译并复制到剪贴板')
  }
}
```

---

### 3.4 Phase D：说话人识别增强（低优先级）

**目标**: 优化现有声纹识别，增加手动编辑

**现状**: 已有 `speaker-diarization.ts` + `speaker-embedding.ts`

**增强点**:

1. **声纹库持久化**: 将用户常见说话人声纹存储到本地 SQLite
2. **手动标注界面**: 会议详情页增加说话人编辑功能
3. **声纹匹配**: 新会议自动匹配声纹库中的已知说话人

**实现**:

```typescript
// frontend/src/stores/speakerLibrary.ts
export const useSpeakerLibraryStore = defineStore('speakerLibrary', () => {
  const knownSpeakers = ref<SpeakerProfile[]>([])
  
  async function addSpeaker(profile: SpeakerProfile) {
    await db.speakers.add(profile)
    knownSpeakers.value.push(profile)
  }
  
  async function matchSpeaker(embedding: Float32Array): Promise<SpeakerProfile | null> {
    // 余弦相似度匹配
    let bestMatch: SpeakerProfile | null = null
    let bestScore = 0.75 // 阈值
    
    for (const speaker of knownSpeakers.value) {
      const score = cosineSimilarity(embedding, speaker.embedding)
      if (score > bestScore) {
        bestScore = score
        bestMatch = speaker
      }
    }
    return bestMatch
  }
})
```

---

### 3.5 Phase E：macOS 专属特性（可选）

**目标**: 利用 macOS 优势实现高级特性

1. **本地 ASR**: 使用 Apple Speech Framework（隐私友好）
2. **虚拟麦克风**: 使用 BlackHole 2ch 输出翻译音频
3. **屏幕录制**: 捕获任意应用的音频输出

**实现**: 创建 macOS Catalyst 版本或独立 Swift 应用

**优先级**: 低（当前 OpenPocket 主要面向移动端）

---

## 4. 实施计划

### 4.1 优先级排序

| Phase | 特性 | 工作量 | 价值 | 优先级 |
|-------|------|--------|------|--------|
| A | 实时字幕 + 翻译 | 5d | 高 | P0 |
| B | 系统音频捕获 | 8d | 中 | P1 |
| C | 按键触发快捷翻译 | 3d | 中 | P1 |
| D | 说话人识别增强 | 4d | 低 | P2 |
| E | macOS 专属特性 | 15d | 低 | P3 |

### 4.2 技术风险

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| Android 系统音频捕获需 API 29+ | 老设备不支持 | 降级到仅麦克风模式 |
| iOS 悬浮窗限制 | 体验受限 | 改用画中画模式 |
| 翻译 API 成本 | 运营成本高 | 仅会员功能；支持本地模型 |
| 实时性能要求 | 设备发热/耗电 | 优化采样率；可选开关 |

### 4.3 兼容性矩阵

| 特性 | Android | iOS | macOS | Web |
|------|---------|-----|-------|-----|
| 实时字幕 | ✅ 悬浮窗 | ✅ PiP | ✅ 原生窗口 | ✅ 页面内 |
| 实时翻译 | ✅ | ✅ | ✅ | ✅ |
| 系统音频捕获 | ✅ API29+ | ⚠️ ReplayKit | ✅ 屏幕录制 | ❌ |
| 按键触发 | ✅ 音量键 | ⚠️ Shortcuts | ✅ 全局快捷键 | ❌ |
| 虚拟麦克风输出 | ❌ | ❌ | ✅ BlackHole | ❌ |
| 本地 ASR | ✅ sherpa | ✅ Speech | ✅ Speech | ❌ |

---

## 5. 不采用的 Cicada 特性

| 特性 | 原因 |
|------|------|
| 独立桌面应用 | OpenPocket 是移动优先的 Web 应用 |
| Ling API 集成 | 已有自己的 LLM 网关，支持多家提供商 |
| 语音克隆 TTS | 成本高，用户需求不明确 |
| 本地 voiceprint 模型下载 | 已有轻量级方案（speaker-diarization.ts） |
| 多窗口管理 | 移动端不适用 |
| SRT 字幕导出 | 可后续按需添加 |

---

## 6. 成功指标

### 6.1 功能指标

- [ ] 实时字幕延迟 < 2 秒
- [ ] 翻译准确率 > 85%（人工抽样 100 句）
- [ ] 系统音频捕获成功率 > 95%（Android 10+）
- [ ] 说话人识别准确率 > 80%（2-5 人会议）

### 6.2 性能指标

- [ ] 内存占用 < 300MB（含音频缓冲）
- [ ] CPU 占用 < 20%（非转写时）
- [ ] 电池消耗 < 15%/小时（典型会议场景）
- [ ] 冷启动时间 < 3 秒

### 6.3 兼容性指标

- [ ] Android 10+ 设备覆盖率 > 90%
- [ ] iOS 15+ 设备覆盖率 > 95%
- [ ] 5 款主流会议软件测试通过（Zoom/Teams/Meet/飞书/腾讯会议）

---

## 7. 后续迭代方向

1. **离线模式**: 本地 LLM 支持离线转写和总结
2. **多语言混合**: 自动检测语言切换（中英混杂会议）
3. **情绪分析**: 识别说话人情绪状态
4. **会议搜索**: 全文检索历史会议内容
5. **协作功能**: 多人同时标注和编辑转写本

---

**文档版本**: v1.0  
**最后更新**: 2026-11-09  
**作者**: Mavis (ZCode Session)
