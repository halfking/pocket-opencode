# OpenPocket 实时转写系统全局优化方案

> **编制时间**: 2026-11-14  
> **当前版本**: v1.2.0  
> **目标**: 提升实时转写速度与准确度，优化交互体验

---

## 📋 执行摘要

### 当前问题诊断

| 维度 | 现状 | 痛点 |
|------|------|------|
| **延迟** | 首字≥8秒 | 固定切片，无实时VAD |
| **准确度** | 本地85%，云端不稳定 | zipformer字准低，网关易故障 |
| **鲁棒性** | 云端单点故障 | 未配GROQ key时全链路失败 |
| **说话人** | 未实现 | ECAPA-TDNN未接入 |
| **交互** | 滚动字幕 | 无编辑、无置信度标注、无PiP |

### 优化目标

| 指标 | 当前 | 目标 | 改善幅度 |
|------|------|------|----------|
| 首字延迟 | 8-10秒 | **<1秒** | **8-10倍** |
| 中文字准率 | 85% (本地) | **95%+** | **+10%** |
| 端点响应 | 8秒固定 | **200-500ms** | **16-40倍** |
| 云端可用率 | 95% | **99%+** (本地兜底) | **+4%** |
| 说话人识别 | 0% | **90%+** | **从无到有** |

---

## 🎯 三期优化方案

### Phase 1: 延迟优化 (2-3周)

**核心目标**: 首字延迟从≥8秒降至<1秒

#### 1.1 流式VAD改造 ⭐最高优先级

**技术方案**:
- 集成 Silero VAD (sherpa-onnx内置)
- 动态端点检测替代固定8秒切片
- 分片策略: 500ms-3秒 (典型) / 10秒兜底

**实施位置**:
```java
// MeetingRecordService.java
SileroVad vad = SherpaPlugin.createVad();
while (running) {
  VadResult result = vad.process(audioBuffer);
  if (result.isSpeechEnd()) {
    emitPart(...); // 立即发送
  }
}
```

**预期收益**:
- 首字延迟: 8秒 → 0.5-1秒 (8-10倍)
- 端点响应: 8秒 → 200-500ms (16-40倍)
- CPU开销: +3% (可接受)

#### 1.2 流式识别管道

```
AudioRecord → 100ms块 → VAD → 语音缓冲 → 端点切句 → sherpa流式
                              ↓
                         静音跳过 (节省算力)
```

**关键改动**:
| 文件 | 改动 |
|------|------|
| `SherpaPlugin.java` | 新增 `createVad()` |
| `MeetingRecordService.java` | VAD动态切片 |
| `ingest-speech.ts` | 接收1-3秒小粒度分片 |

**风险兜底**:
- VAD误判 → 保留10秒最大片长
- 小片准确率下降 → <500ms不送转写
- 新增配置开关: `POCKET_VAD_ENABLED=true`

**参考实现**:
- [RealtimeSTT](https://github.com/KoljaB/RealtimeSTT) (5.2k⭐): webrtcvad + 流式Whisper
- [whisper_streaming](https://github.com/ufal/whisper_streaming) (2.3k⭐): buffer-based chunking

---

### Phase 2: 准确度提升 (3-4周)

**核心目标**: 字准率85%→95%，说话人识别从无到有

#### 2.1 本地引擎升级

**方案A: Paraformer替换zipformer** (推荐)

| 模型 | 字准率 | 标点 | RTF | 体量 | 用途 |
|------|--------|------|-----|------|------|
| zipformer-small (当前) | 85% | ❌ | 0.12 | 50MB | 流式 |
| **paraformer-zh** | **95%** | ✅ | **0.15** | **120MB** | **替代** |
| sensevoice-int8 | 97% | ✅ | 0.03 | 227MB | 保留精转 |

**收益**:
- 字准率: +10% (85%→95%)
- 自动标点: 从无到有
- RTF增加: 仅+3% (0.12→0.15)

**实现**:
```java
// SherpaPlugin.java
OfflineParaformerModelConfig config = 
  OfflineParaformerModelConfig.builder()
    .model("paraformer-zh-2023-09-14/model.int8.onnx")
    .build();
```

#### 2.2 说话人分离

**方案: 本地ECAPA-TDNN** (192维向量)

```java
// extractEmbedding 实现
SpeakerEmbeddingExtractor extractor = 
  new SpeakerEmbeddingExtractor(config);
float[] embedding = extractor.compute(audioPath);
```

**聚类逻辑**:
```typescript
// speaker-diarizer.ts
identify(embedding: Float32Array): { profileId, label } {
  for (const profile of this.profiles) {
    if (cosineSimilarity(embedding, profile.embedding) > 0.85) {
      return profile; // 已知说话人
    }
  }
  return this.createNewSpeaker(embedding); // 新说话人
}
```

**用户体验**:
- 自动标注: "说话人1" / "说话人2"
- 点击重命名: "张三" / "李四"
- 颜色区分: 每人独立配色

#### 2.3 云端质量优化

**当前问题**: glm-asr易挂，网关探测复杂

**方案**: 固定优先级 + 快速失败

```go
// backend/internal/stt/target.go
var chineseASRPriority = []Provider{
  {ID: "mimo-v2.5-asr", Score: 100},      // 最稳定
  {ID: "groq/whisper-large-v3", Score: 90},
  {ID: "minimax-asr-1.0", Score: 80},
  {ID: "glm-asr", Score: 50},             // 降级
}
```

#### 2.4 本地兜底增强 ✅ (已完成)

**2026-11-14 层3修复**:
```typescript
// stt.ts:167
catch (error) {
  if (localBest && !isAbortError(error)) {
    return localBest; // engine: 'local'
  }
  throw error;
}
```

**补充**: UI置信度提示
```vue
<div v-if="seg.confidence < 0.7" class="warning">
  本地转写 (置信度{{ (seg.confidence*100).toFixed(0) }}%)
</div>
```

**学习高星项目**:

| 项目 | Stars | 借鉴点 |
|------|-------|--------|
| [Faster-Whisper](https://github.com/SYSTRAN/faster-whisper) | 10k | CTranslate2加速4倍，INT8量化 |
| [WhisperX](https://github.com/m-bain/whisperX) | 10k | VAD预处理、强制对齐、Pyannote说话人分离 |
| [sherpa-onnx](https://github.com/k2-fsa/sherpa-onnx) | 2.7k | 运行时下载、多线程优化 |

---

### Phase 3: 交互体验 (4-5周)

**核心目标**: 对标讯飞听见，提升用户体验

#### 3.1 竞品对比分析

| 维度 | 讯飞听见 | OpenPocket | 差距等级 |
|------|----------|------------|----------|
| 实时反馈 | 即说即显，灰字→黑字 | 8秒延迟 | ⭐⭐⭐ |
| 置信度可视化 | 低置信词虚线标注 | 无 | ⭐⭐ |
| 编辑能力 | 点击即改，联想补全 | 只读 | ⭐⭐⭐ |
| 说话人标识 | 自动区分+自定义名称 | 缺失 | ⭐⭐⭐ |
| 多语言支持 | 实时中英混合 | 分段检测 | ⭐⭐ |
| 导出格式 | Word/SRT/TXT/双语 | TXT/SRT | ⭐ |

#### 3.2 三层渲染架构

**Layer 1: 实时流 (Interim)**
- 灰色文字，500ms刷新
- 本地引擎实时出字
- 单行滚动，自动跟随

**Layer 2: 分段确认 (Partial)**
- 黑色文字，VAD切句后固化
- 可点击编辑
- 多行堆叠，按时间排序

**Layer 3: 全量精校 (Final)**
- 黑色加粗，带标点说话人
- 录音结束后云端全转
- 完整文档模式

**实现**:
```vue
<template>
  <div class="transcript-container">
    <!-- Layer 1 -->
    <div v-if="interimText" class="interim-line">
      <span class="interim-text">{{ interimText }}</span>
      <span class="cursor">|</span>
    </div>
    
    <!-- Layer 2 -->
    <div v-for="seg in segments" class="segment" @click="editSegment">
      <speaker-header :speaker="seg.speakerLabel" />
      <div class="text" :class="{ 'low-conf': seg.confidence < 0.7 }">
        <span 
          v-for="word in seg.words"
          :class="{ 'uncertain': word.confidence < 0.6 }"
        >
          {{ word.text }}
        </span>
      </div>
    </div>
    
    <!-- Layer 3 -->
    <banner v-if="finalAvailable" @click="upgradeFinal">
      高精度全文已生成
    </banner>
  </div>
</template>
```

#### 3.3 智能滚动策略

**问题**: 长时录音字幕超屏

**方案**:
```typescript
class TranscriptScroller {
  onSegmentAdded() {
    if (this.userScrolled && !this.isNearBottom()) {
      this.showNewContentBadge(true); // 显示"新消息"
      return;
    }
    this.scrollToBottom('smooth'); // 自动跟随
  }
  
  onUserScroll() {
    if (this.isNearBottom()) {
      this.userScrolled = false; // 恢复自动跟随
    }
  }
}
```

参考: YouTube直播聊天室、Slack

#### 3.4 说话人管理UI

**功能**:
- 自动分配: "说话人1" / "说话人2"
- 点击重命名: 输入真实姓名
- 合并/分离: 纠正误识别
- 颜色编码: 每人独立配色

```vue
<div class="speaker-management">
  <draggable v-model="speakers">
    <div v-for="sp in speakers" class="speaker-card">
      <avatar :color="sp.color" />
      <input v-model="sp.customName" placeholder="输入姓名" />
      <span>{{ sp.segmentCount }}段</span>
      <button @click="merge(sp)">合并</button>
    </div>
  </draggable>
</div>
```

#### 3.5 点击即改编辑

```typescript
function makeSegmentEditable(segmentId, onSave) {
  const el = document.querySelector(`[data-segment-id="${segmentId}"]`);
  el.contentEditable = 'true';
  el.focus();
  
  el.addEventListener('blur', () => {
    el.contentEditable = 'false';
    onSave(el.textContent);
  });
}
```

**增强**: 智能纠错
```typescript
// 集成 LanguageTool
async function suggestCorrections(text: string) {
  const lt = new LanguageTool({ language: 'zh-CN' });
  return await lt.check(text);
}
```

#### 3.6 多格式导出

**新增格式**:
- **Word文档**: 带说话人、时间戳
- **HTML富文本**: 可分享在线预览
- **双语对照**: 中英文并排

```typescript
class TranscriptExporter {
  async exportDocx(meeting): Blob { ... }
  async exportHTML(meeting): string { ... }
  async exportBilingual(meeting): string { ... }
}
```

#### 3.7 PiP悬浮窗 (Android)

**功能**: 切换应用时保持字幕可见

```java
@Override
public void onUserLeaveHint() {
  if (isRecording) {
    enterPictureInPictureMode(
      new PictureInPictureParams.Builder()
        .setAspectRatio(new Rational(16, 9))
        .build()
    );
  }
}
```

---

## 📊 整体性能预期

| 指标 | 当前 | Phase 1后 | Phase 2后 | Phase 3后 | 总改善 |
|------|------|-----------|-----------|-----------|--------|
| 首字延迟 | 8-10秒 | **0.5-1秒** | 0.5-1秒 | 0.5-1秒 | **8-10倍** |
| 端点响应 | 8秒 | **200-500ms** | 200-500ms | 200-500ms | **16-40倍** |
| 字准率(本地) | 85% | 85% | **95%** | 95% | **+10%** |
| 字准率(云端) | 95% | 95% | **98%** | 98% | **+3%** |
| 说话人识别 | 0% | 0% | **90%** | 90% | **从无到有** |
| 可用率 | 95% | 95% | **99%+** | 99%+ | **+4%** |
| 编辑能力 | 0% | 0% | 0% | **100%** | **从无到有** |
| 导出格式 | 2种 | 2种 | 2种 | **5种** | **+150%** |

---

## 🗓️ 实施路线图

### 时间线 (总计10-12周)

```
Week 1-3:  Phase 1 延迟优化
  ├─ Week 1: Silero VAD集成
  ├─ Week 2: MeetingRecordService改造
  └─ Week 3: 真机验证 + A/B测试

Week 4-7:  Phase 2 准确度提升
  ├─ Week 4-5: Paraformer替换zipformer
  ├─ Week 6: ECAPA-TDNN说话人分离
  └─ Week 7: 云端优化 + 综合测试

Week 8-12: Phase 3 交互优化
  ├─ Week 8-9: 三层UI重构 + 智能滚动
  ├─ Week 10: 说话人管理 + 编辑功能
  └─ Week 11-12: 导出分享 + PiP + 收尾
```

### 里程碑

**M1 (Week 3)**: 延迟降至<1秒
- ✅ Silero VAD集成
- ✅ 动态切片实现
- ✅ 真机延迟测试<1秒

**M2 (Week 7)**: 准确率达95%
- ✅ Paraformer上线
- ✅ 说话人识别可用
- ✅ 本地兜底稳定

**M3 (Week 12)**: 体验对标讯飞
- ✅ 三层UI完整
- ✅ 编辑流程闭环
- ✅ 导出格式齐全

---

## 🎯 关键成功指标 (KPI)

### 技术指标

| 指标 | 基线 | 目标 | 验收标准 |
|------|------|------|----------|
| 首字延迟 P95 | 10秒 | **<1秒** | 真机10次测试平均<1秒 |
| 端点响应 P95 | 8秒 | **<500ms** | VAD切句90%在500ms内 |
| 字准率(中文) | 85% | **>95%** | 100条测试集平均>95% |
| 说话人准确率 | - | **>90%** | 5人会议10分钟误判<10% |
| 云端可用率 | 95% | **>99%** | 本地兜底成功率>99% |

### 用户体验指标

| 指标 | 基线 | 目标 | 测量方式 |
|------|------|------|----------|
| 用户满意度 (NPS) | - | **>50** | 每月用户调研 |
| 录音时长中位数 | 3分钟 | **>10分钟** | 埋点统计 |
| 编辑使用率 | 0% | **>30%** | 编辑功能使用人数/总用户 |
| 导出使用率 | 20% | **>50%** | 导出次数/会议总数 |

### 性能指标

| 指标 | 基线 | 目标 | 验收标准 |
|------|------|------|----------|
| CPU占用 | 5% | **<10%** | 录音期间平均CPU<10% |
| 内存占用 | 300MB | **<500MB** | 1小时录音峰值<500MB |
| 电池温度 | 31°C | **<35°C** | 30分钟录音升温<4°C |
| 崩溃率 | <0.1% | **<0.05%** | 月活崩溃率 |

---

## ⚠️ 风险与缓解

### 技术风险

| 风险 | 可能性 | 影响 | 缓解措施 |
|------|--------|------|----------|
| VAD误判率高 | 中 | 高 | 保留10秒最大片长兜底 |
| Paraformer体积过大 | 低 | 中 | 首次Wi-Fi下载，可配置关闭 |
| 说话人聚类准确率低 | 中 | 中 | 提供手动合并/分离UI |
| 本地引擎内存溢出 | 低 | 高 | 限制最大并发识别数为2 |
| 云端网关仍不稳定 | 中 | 中 | 本地兜底已实现(2026-11-14) |

### 项目风险

| 风险 | 可能性 | 影响 | 缓解措施 |
|------|--------|------|----------|
| 工期延误 | 中 | 中 | 每周sprint review，及时调整 |
| 真机适配问题 | 高 | 中 | Week 3/7/12三次真机验证 |
| 用户接受度低 | 低 | 高 | 灰度发布，A/B测试，收集反馈 |
| 模型许可证问题 | 低 | 高 | FunASR/SenseVoice法务确认 |

---

## 📚 参考资料

### 高星开源项目

| 项目 | Stars | 链接 | 借鉴点 |
|------|-------|------|--------|
| Faster-Whisper | 10k | [GitHub](https://github.com/SYSTRAN/faster-whisper) | CTranslate2加速、INT8量化 |
| WhisperX | 10k | [GitHub](https://github.com/m-bain/whisperX) | VAD预处理、强制对齐、说话人分离 |
| RealtimeSTT | 5.2k | [GitHub](https://github.com/KoljaB/RealtimeSTT) | 流式架构、webrtcvad |
| sherpa-onnx | 2.7k | [GitHub](https://github.com/k2-fsa/sherpa-onnx) | 多端部署、运行时下载 |
| whisper_streaming | 2.3k | [GitHub](https://github.com/ufal/whisper_streaming) | Buffer-based chunking |
| insanely-fast-whisper | 6.8k | [GitHub](https://github.com/Vaibhavs10/insanely-fast-whisper) | Batch优化 |

### 竞品分析

- **讯飞听见**: 实时转写、说话人识别、编辑能力
- **腾讯会议字幕**: 实时流畅度、多语言混合
- **钉钉闪记**: 会议纪要生成、智能摘要

### 技术文档

- `docs/2026-10-06-local-asr-sherpa-integration.md`: sherpa-onnx集成
- `docs/2026-10-06-ondevice-realtime-feasibility.md`: 端侧实时可行性
- `docs/handoff/2026-11-14-cicada-phase-a-live-test-report.md`: 真机测试报告
- `backend/internal/config/config.go`: 后端配置
- `frontend/src/api/stt.ts`: 转写API
- `frontend/src/features/meetings/ingest-speech.ts`: 音频处理

---

## ✅ 下一步行动

### 立即开始 (Week 1)

1. **环境准备**
   - [ ] 克隆 [sherpa-onnx](https://github.com/k2-fsa/sherpa-onnx) 官方示例
   - [ ] 下载 Silero VAD模型 (~1MB)
   - [ ] 搭建真机测试环境 (CDP自动化)

2. **代码改造**
   - [ ] `SherpaPlugin.java` 新增 `createVad()` 方法
   - [ ] `MeetingRecordService.java` 集成VAD检测循环
   - [ ] `sherpa.ts` 暴露VAD接口

3. **测试验证**
   - [ ] 单元测试: VAD端点检测准确性
   - [ ] 真机测试: 首字延迟测量
   - [ ] 对比实验: 固定切片 vs VAD动态

### 需要决策

1. **Paraformer vs zipformer**: 建议Phase 2切换，120MB体积可接受
2. **说话人分离方案**: 本地ECAPA优先，云端Pyannote作为可选精校
3. **灰度策略**: 建议10%用户先行，观察1周后全量

### 资源需求

- **研发**: 1名Android + 1名前端 + 0.5名后端
- **测试**: 1名QA + 5台真机设备池
- **设计**: 0.5名UI设计 (Phase 3)

---

## 📞 联系与反馈

如有问题或建议，请通过以下方式反馈：
- 项目仓库 Issue
- 技术群讨论
- 每周 Sprint Review

**编制人**: ZCode AI Assistant  
**审阅人**: [待填写]  
**批准人**: [待填写]
