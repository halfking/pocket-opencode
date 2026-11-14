# Phase 3: 交互体验优化 (学习讯飞听见)

## 1. 竞品分析：讯飞听见

### 1.1 核心体验特点

| 维度 | 讯飞听见 | OpenPocket当前 | 差距 |
|------|----------|----------------|------|
| **实时反馈** | 即说即显，灰字→黑字 | 8秒延迟 | ⭐⭐⭐ |
| **置信度可视化** | 低置信词虚线标注 | 无 | ⭐⭐ |
| **编辑能力** | 点击即改，联想补全 | 只读 | ⭐⭐⭐ |
| **说话人标识** | 自动区分+自定义名称 | 缺失 | ⭐⭐⭐ |
| **多语言支持** | 实时中英混合 | 分段检测 | ⭐⭐ |
| **导出格式** | Word/SRT/TXT/双语对照 | TXT/SRT | ⭐ |
| **协同编辑** | 多人同步查看/标注 | 无 | ⭐⭐ |

### 1.2 交互细节拆解

**实时转写显示**：
```
[灰色] 今天我们讨论一下...
      ↓ 500ms后
[黑色] 今天我们讨论一下项目进度
      ↓ 云端精校
[黑色+高亮] 今天我们讨论一下项目进度。
```

**低置信标注**：
```
今天[⚠我们]讨论项目进度
     ↑ 虚线标注，点击可选替代词
```

**说话人切换**：
```
[张三 10:23] 今天的议题是...
[李四 10:24] 我补充一点，关于...
[张三 10:25] 好的，那我们...
```

## 2. 实时字幕UI重构

### 2.1 三层渲染架构

**Layer 1: 实时流 (Interim)**
- 本地zipformer/paraformer实时出字
- 灰色显示，500ms刷新
- 单行滚动，自动跟随

**Layer 2: 分段确认 (Partial)**
- VAD端点切句后固化
- 黑色显示，可点击编辑
- 多行堆叠，按时间排序

**Layer 3: 全量精校 (Final)**
- 录音结束后云端全转
- 黑色加粗，带标点说话人
- 完整文档模式

**实现**：
```vue
<!-- MeetingTranscript.vue 重构 -->
<template>
  <div class="transcript-container">
    <!-- Layer 1: 实时流 -->
    <div v-if="interimText" class="interim-line">
      <span class="interim-text">{{ interimText }}</span>
      <span class="cursor-blink">|</span>
    </div>
    
    <!-- Layer 2: 已确认段落 -->
    <div v-for="seg in segments" :key="seg.id" class="segment">
      <div class="speaker-header">
        <avatar :speaker="seg.speakerLabel" />
        <span class="speaker-name">{{ speakerName(seg) }}</span>
        <span class="timestamp">{{ formatTime(seg.startMs) }}</span>
        <confidence-badge 
          v-if="seg.confidence < 0.8" 
          :value="seg.confidence" 
        />
      </div>
      <div 
        class="segment-text" 
        :class="{ 'low-confidence': seg.confidence < 0.7 }"
        @click="editSegment(seg)"
      >
        <span 
          v-for="(word, i) in seg.words" 
          :key="i"
          :class="{ 'uncertain-word': word.confidence < 0.6 }"
        >
          {{ word.text }}
        </span>
      </div>
    </div>
    
    <!-- Layer 3: 全量精校提示 -->
    <div v-if="finalTranscriptAvailable" class="final-upgrade-banner">
      <icon name="sparkles" />
      <span>高精度全文已生成</span>
      <button @click="upgradeFinal">查看</button>
    </div>
  </div>
</template>

<style scoped>
.interim-text {
  color: #999;
  font-style: italic;
  animation: fadeIn 0.3s;
}

.uncertain-word {
  border-bottom: 1px dashed #f59e0b;
  cursor: help;
}

.low-confidence {
  background: #fef3c7;
  border-left: 3px solid #f59e0b;
  padding-left: 8px;
}
</style>
```

### 2.2 实时跟随与锚定

**问题**：长时录音字幕超屏，用户看不到最新内容

**方案**：智能滚动策略

```typescript
// transcript-auto-scroll.ts
export class TranscriptScroller {
  private userScrolled = false;
  private lastSegmentId = '';
  
  onSegmentAdded(segmentId: string) {
    // 用户主动滚动过 → 不自动跟随
    if (this.userScrolled && !this.isNearBottom()) {
      this.showNewContentBadge(true);
      return;
    }
    
    // 自动滚到底部
    this.scrollToBottom('smooth');
    this.lastSegmentId = segmentId;
  }
  
  onUserScroll() {
    this.userScrolled = true;
    // 滚到底部 → 恢复自动跟随
    if (this.isNearBottom()) {
      this.userScrolled = false;
      this.showNewContentBadge(false);
    }
  }
  
  private isNearBottom(): boolean {
    const el = this.scrollContainer;
    return el.scrollHeight - el.scrollTop - el.clientHeight < 100;
  }
}
```

**参考**：
- YouTube直播聊天室：用户滚动后显示"新消息"badge
- Slack：滚动到底部后自动恢复跟随

### 2.3 字幕悬浮窗 (PiP模式)

**需求**：切换应用时保持字幕可见

**方案**：Android PiP API

```java
// MeetingDetailActivity.java
@Override
public void onUserLeaveHint() {
  if (isRecording) {
    enterPictureInPictureMode(
      new PictureInPictureParams.Builder()
        .setAspectRatio(new Rational(16, 9))
        .setActions(Arrays.asList(
          createAction("pause", R.drawable.ic_pause),
          createAction("stop", R.drawable.ic_stop)
        ))
        .build()
    );
  }
}

@Override
public void onPictureInPictureModeChanged(boolean isInPip) {
  if (isInPip) {
    // 隐藏非必要UI，只保留字幕滚动
    binding.transcriptOnly.setVisibility(View.VISIBLE);
    binding.controls.setVisibility(View.GONE);
  }
}
```

## 3. 说话人管理

### 3.1 自动识别 + 自定义名称

**UI流程**：
```
录音中自动分配 → "说话人1" / "说话人2"
              ↓
用户点击头像 → 输入真实姓名
              ↓
后续段落自动应用 → "张三" / "李四"
```

**实现**：
```typescript
// speaker-profile.ts
interface SpeakerProfile {
  id: string;              // embedding聚类ID
  label: string;           // 自动标签 "说话人1"
  customName?: string;     // 用户自定义 "张三"
  embedding: Float32Array; // ECAPA向量
  segmentCount: number;    // 该说话人段落数
  color: string;           // UI配色
}

export class SpeakerManager {
  private profiles = new Map<string, SpeakerProfile>();
  
  identify(embedding: Float32Array): SpeakerProfile {
    // 余弦相似度聚类
    for (const [id, profile] of this.profiles) {
      if (cosineSimilarity(embedding, profile.embedding) > 0.85) {
        return profile;
      }
    }
    
    // 新说话人
    const newProfile: SpeakerProfile = {
      id: generateId(),
      label: `说话人${this.profiles.size + 1}`,
      embedding,
      segmentCount: 0,
      color: SPEAKER_COLORS[this.profiles.size % SPEAKER_COLORS.length]
    };
    
    this.profiles.set(newProfile.id, newProfile);
    return newProfile;
  }
  
  rename(profileId: string, customName: string) {
    const profile = this.profiles.get(profileId);
    if (profile) {
      profile.customName = customName;
      this.saveToDb(profile);
      this.refreshUI(); // 刷新所有该说话人的段落
    }
  }
}
```

### 3.2 说话人合并与分离

**场景**：
- 误识别为两个人 → 合并
- 一个人被当作多人 → 重新聚类

**UI**：
```vue
<template>
  <div class="speaker-management">
    <h3>说话人管理 ({{ speakers.length }}人)</h3>
    <draggable v-model="speakers" @end="onReorder">
      <div v-for="sp in speakers" :key="sp.id" class="speaker-card">
        <avatar :color="sp.color" />
        <input v-model="sp.customName" placeholder="点击输入姓名" />
        <span class="segment-count">{{ sp.segmentCount }}段</span>
        <button @click="mergeSpeaker(sp)">合并</button>
        <button @click="deleteSpeaker(sp)">删除</button>
      </div>
    </draggable>
  </div>
</template>
```

## 4. 编辑与校对

### 4.1 点击即改

**实现**：
```typescript
// editable-segment.ts
export function makeSegmentEditable(
  segmentId: string,
  onSave: (newText: string) => void
) {
  const el = document.querySelector(`[data-segment-id="${segmentId}"]`);
  
  el.contentEditable = 'true';
  el.focus();
  
  // 选中全文
  const range = document.createRange();
  range.selectNodeContents(el);
  window.getSelection()?.removeAllRanges();
  window.getSelection()?.addRange(range);
  
  el.addEventListener('blur', () => {
    el.contentEditable = 'false';
    onSave(el.textContent || '');
  });
  
  el.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      el.blur();
    }
  });
}
```

### 4.2 智能纠错建议

**集成拼写检查**：
```typescript
// spell-check.ts
import { LanguageTool } from 'languagetool-api';

async function suggestCorrections(text: string): Promise<Correction[]> {
  const lt = new LanguageTool({ language: 'zh-CN' });
  const result = await lt.check(text);
  
  return result.matches.map(m => ({
    offset: m.offset,
    length: m.length,
    original: text.substring(m.offset, m.offset + m.length),
    suggestions: m.replacements.map(r => r.value),
    message: m.message
  }));
}
```

**UI提示**：
```vue
<template>
  <div class="segment-editor">
    <div 
      v-for="word in annotatedWords" 
      :key="word.offset"
      :class="{ 'has-suggestion': word.corrections.length }"
    >
      <span>{{ word.text }}</span>
      <div v-if="word.corrections.length" class="correction-popup">
        <div v-for="sug in word.corrections" @click="applySuggestion(sug)">
          {{ sug }}
        </div>
      </div>
    </div>
  </div>
</template>
```

## 5. 导出与分享

### 5.1 多格式导出

**新增格式**：
```typescript
// export-formats.ts
export class TranscriptExporter {
  // 已有：TXT, SRT
  
  // 新增：Word文档
  async exportDocx(meeting: Meeting): Promise<Blob> {
    const doc = new Document({
      sections: [{
        children: [
          new Paragraph({
            text: meeting.title,
            heading: HeadingLevel.HEADING_1
          }),
          ...meeting.segments.map(seg => 
            new Paragraph({
              children: [
                new TextRun({
                  text: `[${seg.speakerLabel}] `,
                  bold: true
                }),
                new TextRun({
                  text: seg.text
                })
              ]
            })
          )
        ]
      }]
    });
    
    return await Packer.toBlob(doc);
  }
  
  // 新增：双语对照
  async exportBilingual(meeting: Meeting): Promise<string> {
    return meeting.segments
      .map(seg => `
        ${seg.text}
        ${seg.translation || ''}
        
      `.trim())
      .join('\n\n');
  }
  
  // 新增：HTML富文本
  async exportHTML(meeting: Meeting): Promise<string> {
    return `
      <!DOCTYPE html>
      <html>
      <head>
        <meta charset="utf-8">
        <title>${meeting.title}</title>
        <style>
          .speaker { font-weight: bold; color: #2563eb; }
          .timestamp { color: #6b7280; font-size: 0.875em; }
          .low-confidence { background: #fef3c7; }
        </style>
      </head>
      <body>
        <h1>${meeting.title}</h1>
        ${meeting.segments.map(seg => `
          <div class="segment ${seg.confidence < 0.7 ? 'low-confidence' : ''}">
            <span class="speaker">${seg.speakerLabel}</span>
            <span class="timestamp">${formatTime(seg.startMs)}</span>
            <p>${seg.text}</p>
          </div>
        `).join('\n')}
      </body>
      </html>
    `;
  }
}
```

### 5.2 分享链接

**功能**：生成可分享的在线预览链接

```typescript
// share-link.ts
export async function createShareLink(
  meetingId: string,
  options: { password?: string; expiresIn?: number }
): Promise<string> {
  const shareToken = await http.post('/api/meetings/share', {
    meetingId,
    password: options.password,
    expiresIn: options.expiresIn || 7 * 24 * 3600 // 7天
  });
  
  return `https://pocket.itestu.cn/share/${shareToken.token}`;
}
```

**预览页面**：
```vue
<!-- ShareView.vue -->
<template>
  <div class="share-view">
    <div class="meeting-header">
      <h1>{{ meeting.title }}</h1>
      <p>{{ meeting.createdAt }} · {{ meeting.duration }}分钟</p>
    </div>
    
    <div class="transcript-readonly">
      <div v-for="seg in segments" class="segment">
        <span class="speaker">{{ seg.speakerLabel }}</span>
        <span class="text">{{ seg.text }}</span>
      </div>
    </div>
    
    <div class="actions">
      <button @click="downloadSRT">下载SRT</button>
      <button @click="downloadTXT">下载TXT</button>
    </div>
  </div>
</template>
```

## 6. 性能与体验指标

| 指标 | 当前 | Phase 3后 | 目标 |
|------|------|-----------|------|
| 首屏渲染 | 未优化 | <100ms | ✅ |
| 实时刷新 | 8秒 | 500ms | ✅ |
| 编辑响应 | 不支持 | <50ms | ✅ |
| 导出速度 | TXT 500ms | Word 2s | ✅ |
| 滚动流畅度 | 60fps | 60fps | ✅ |

## 7. 实施路线图

**Week 1-2: UI重构**
- 三层渲染架构
- 实时跟随滚动
- 置信度可视化

**Week 3-4: 说话人管理**
- 自动识别+聚类
- 自定义名称
- 合并/分离

**Week 5-6: 编辑功能**
- 点击即改
- 智能纠错
- 版本历史

**Week 7-8: 导出分享**
- Word/HTML导出
- 双语对照
- 分享链接

## 8. A/B测试计划

**对照组**：当前UI (固定8秒更新)
**实验组**：新UI (实时流式)

**关键指标**：
- 用户停留时长
- 编辑使用率
- 导出格式分布
- NPS评分
