# Phase 3 前端集成实施指南

> **目标**: 将三层UI渲染和导出功能集成到现有会议系统  
> **预计时间**: 5-8小时  
> **前提条件**: 前端代码已就绪

---

## 一、现状分析

### 已有代码结构

**核心文件**:
- `ingest-speech.ts` - 语音摄入主逻辑
- `audio-parts.ts` - 音频分片存储
- `meetings-store.ts` - 会议状态管理

**Phase 3 新增文件**:
- `three-layer-renderer.ts` - 三层UI渲染器 ✅
- `export-formats.ts` - 多格式导出 ✅
- `speaker-diarizer.ts` - 说话人聚类 ✅

### 集成点

**当前渲染方式** (ingest-speech.ts:11):
```typescript
import { renderTranscript, type TranscriptRenderer } from './meeting-dedup.ts'
```

**需要替换为**:
```typescript
import { ThreeLayerRenderer } from './three-layer-renderer.ts'
```

---

## 二、实施步骤

### 步骤 1: 集成三层UI渲染器 (2-3小时)

#### 1.1 修改 ingest-speech.ts

**位置**: Line 11

**原代码**:
```typescript
import { renderTranscript, type TranscriptRenderer } from './meeting-dedup.ts'
```

**修改为**:
```typescript
import { renderTranscript, type TranscriptRenderer } from './meeting-dedup.ts'
import { ThreeLayerRenderer } from './three-layer-renderer.ts'
```

#### 1.2 初始化三层渲染器

**位置**: `ingestSpeechBlob` 函数内部

**添加代码**:
```typescript
// 在函数开始处初始化
const threeLayerRenderer = opts.transcriptRenderer instanceof ThreeLayerRenderer
  ? opts.transcriptRenderer
  : new ThreeLayerRenderer({
      streamThrottleMs: 500,
      segmentDebounceMs: 1000,
      fullRenderDebounceMs: 2000
    })
```

#### 1.3 更新渲染调用

**位置**: Line 98-100 (STT结果处理后)

**添加渲染逻辑**:
```typescript
// 在 STT 结果处理完成后
if (sttResult.text) {
  // Layer 1: 实时流式更新
  threeLayerRenderer.updateStreamingLayer(opts.seq, sttResult.text)
  
  // Layer 2: 分段确认
  threeLayerRenderer.updateSegmentLayer(opts.seq, {
    text: sttResult.text,
    speaker: embeddingResult.speaker || '未知',
    startMs: opts.startMs,
    endMs: opts.endMs,
    confidence: sttResult.confidence || 0.8
  })
}
```

#### 1.4 添加最终精校层触发

**位置**: 会议结束或用户请求时

**添加代码**:
```typescript
// 在会议结束时触发 Layer 3
export async function finalizeMeetingTranscript(meetingId: string) {
  const renderer = getRendererForMeeting(meetingId)
  if (renderer instanceof ThreeLayerRenderer) {
    await renderer.triggerFullRefinement()
  }
}
```

### 步骤 2: 添加导出功能 (1-2小时)

#### 2.1 创建导出服务

**新建文件**: `meeting-export-service.ts`

```typescript
import { exportToText, exportToHTML, exportToSRT, exportToBilingual, exportToDOCX } from './export-formats.ts'
import { getMeetingSegments } from './meetings-store.ts'

export type ExportFormat = 'text' | 'html' | 'srt' | 'bilingual' | 'docx'

export async function exportMeeting(
  meetingId: string,
  format: ExportFormat,
  options?: {
    includeTimestamps?: boolean
    includeSpeakers?: boolean
    targetLanguage?: string
  }
): Promise<string | Blob> {
  const segments = await getMeetingSegments(meetingId)
  
  switch (format) {
    case 'text':
      return exportToText(segments, options)
    case 'html':
      return exportToHTML(segments, options)
    case 'srt':
      return exportToSRT(segments)
    case 'bilingual':
      return exportToBilingual(segments, options?.targetLanguage)
    case 'docx':
      return exportToDOCX(segments, options)
    default:
      throw new Error(`Unsupported format: ${format}`)
  }
}
```

#### 2.2 添加 UI 按钮

**位置**: 会议详情页面组件

**添加导出按钮**:
```vue
<template>
  <div class="meeting-export">
    <button @click="showExportMenu = !showExportMenu">
      导出会议记录
    </button>
    
    <div v-if="showExportMenu" class="export-menu">
      <button @click="handleExport('text')">纯文本 (.txt)</button>
      <button @click="handleExport('html')">网页 (.html)</button>
      <button @click="handleExport('srt')">字幕 (.srt)</button>
      <button @click="handleExport('bilingual')">双语对照</button>
      <button @click="handleExport('docx')">Word文档 (.docx)</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { exportMeeting, type ExportFormat } from './meeting-export-service.ts'

const showExportMenu = ref(false)

async function handleExport(format: ExportFormat) {
  try {
    const result = await exportMeeting(meetingId, format, {
      includeTimestamps: true,
      includeSpeakers: true
    })
    
    // 下载文件
    if (result instanceof Blob) {
      const url = URL.createObjectURL(result)
      const a = document.createElement('a')
      a.href = url
      a.download = `meeting-${meetingId}.${format}`
      a.click()
      URL.revokeObjectURL(url)
    } else {
      // 文本结果
      downloadAsFile(result, `meeting-${meetingId}.${format}`)
    }
    
    showExportMenu.value = false
  } catch (error) {
    console.error('导出失败:', error)
    alert('导出失败，请重试')
  }
}

function downloadAsFile(content: string, filename: string) {
  const blob = new Blob([content], { type: 'text/plain' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}
</script>
```

### 步骤 3: 实现编辑功能 (1-2小时)

#### 3.1 添加编辑状态管理

**修改**: `meetings-store.ts`

```typescript
export interface EditableSegment extends MeetingSegment {
  isEditing?: boolean
  originalText?: string
}

export function enableSegmentEditing(segmentId: string) {
  const segment = segments.find(s => s.id === segmentId)
  if (segment) {
    segment.isEditing = true
    segment.originalText = segment.text
  }
}

export function saveSegmentEdit(segmentId: string, newText: string) {
  const segment = segments.find(s => s.id === segmentId)
  if (segment) {
    segment.text = newText
    segment.isEditing = false
    // 触发持久化
    updateTranscript(segment)
  }
}

export function cancelSegmentEdit(segmentId: string) {
  const segment = segments.find(s => s.id === segmentId)
  if (segment && segment.originalText) {
    segment.text = segment.originalText
    segment.isEditing = false
    delete segment.originalText
  }
}
```

#### 3.2 添加编辑 UI

**在三层渲染组件中添加**:

```vue
<template>
  <div class="segment-layer-2">
    <div
      v-for="segment in layer2Segments"
      :key="segment.id"
      class="segment"
      @dblclick="startEdit(segment.id)"
    >
      <div v-if="!segment.isEditing" class="segment-view">
        <span class="speaker">{{ segment.speaker }}:</span>
        <span class="text">{{ segment.text }}</span>
        <span class="timestamp">{{ formatTime(segment.startMs) }}</span>
      </div>
      
      <div v-else class="segment-edit">
        <textarea
          v-model="segment.text"
          @blur="saveEdit(segment.id)"
          @keydown.enter.ctrl="saveEdit(segment.id)"
          @keydown.esc="cancelEdit(segment.id)"
          autofocus
        />
        <div class="edit-actions">
          <button @click="saveEdit(segment.id)">保存</button>
          <button @click="cancelEdit(segment.id)">取消</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { enableSegmentEditing, saveSegmentEdit, cancelSegmentEdit } from './meetings-store.ts'

function startEdit(segmentId: string) {
  enableSegmentEditing(segmentId)
}

function saveEdit(segmentId: string) {
  const segment = segments.find(s => s.id === segmentId)
  if (segment) {
    saveSegmentEdit(segmentId, segment.text)
  }
}

function cancelEdit(segmentId: string) {
  cancelSegmentEdit(segmentId)
}

function formatTime(ms: number): string {
  const seconds = Math.floor(ms / 1000)
  const minutes = Math.floor(seconds / 60)
  const remainingSeconds = seconds % 60
  return `${minutes}:${remainingSeconds.toString().padStart(2, '0')}`
}
</script>

<style scoped>
.segment {
  padding: 8px;
  border-bottom: 1px solid #eee;
  cursor: pointer;
}

.segment:hover {
  background: #f5f5f5;
}

.segment-edit textarea {
  width: 100%;
  min-height: 60px;
  padding: 8px;
  border: 2px solid #4CAF50;
  border-radius: 4px;
}

.edit-actions {
  margin-top: 8px;
  display: flex;
  gap: 8px;
}
</style>
```

---

## 三、测试验证

### 3.1 单元测试

**创建**: `three-layer-renderer.test.ts`

```typescript
import { describe, it, expect } from 'vitest'
import { ThreeLayerRenderer } from './three-layer-renderer.ts'

describe('ThreeLayerRenderer', () => {
  it('应该正确初始化三层', () => {
    const renderer = new ThreeLayerRenderer({
      streamThrottleMs: 500,
      segmentDebounceMs: 1000,
      fullRenderDebounceMs: 2000
    })
    
    expect(renderer.getStreamingLayer()).toEqual([])
    expect(renderer.getSegmentLayer()).toEqual([])
    expect(renderer.getRefinedLayer()).toEqual([])
  })
  
  it('应该正确更新流式层', async () => {
    const renderer = new ThreeLayerRenderer()
    renderer.updateStreamingLayer(1, 'Hello world')
    
    // 等待节流
    await new Promise(resolve => setTimeout(resolve, 600))
    
    const layer1 = renderer.getStreamingLayer()
    expect(layer1[0].text).toBe('Hello world')
  })
  
  // ... 更多测试
})
```

### 3.2 集成测试

**测试场景**:
1. 开始录音 → 验证Layer 1实时更新
2. 分段结束 → 验证Layer 2显示完整段落
3. 编辑文本 → 验证保存和取消
4. 导出各格式 → 验证文件正确生成
5. 会议结束 → 验证Layer 3精校触发

### 3.3 手动测试清单

- [ ] Layer 1: 实时流式显示正常
- [ ] Layer 2: 分段确认显示正确
- [ ] Layer 3: 精校版本标点完整
- [ ] 双击进入编辑模式
- [ ] Ctrl+Enter保存编辑
- [ ] ESC取消编辑
- [ ] 导出Text格式
- [ ] 导出HTML格式
- [ ] 导出SRT字幕
- [ ] 导出双语对照
- [ ] 导出Word文档

---

## 四、性能优化

### 4.1 渲染优化

```typescript
// 使用虚拟滚动处理大量段落
import { useVirtualList } from '@vueuse/core'

const { list, containerProps, wrapperProps } = useVirtualList(
  segments,
  { itemHeight: 60 }
)
```

### 4.2 节流和防抖

```typescript
// Layer 1: 节流500ms
const updateStreamingThrottled = throttle(updateStreamingLayer, 500)

// Layer 2: 防抖1000ms
const updateSegmentDebounced = debounce(updateSegmentLayer, 1000)

// Layer 3: 防抖2000ms
const triggerFullRefinementDebounced = debounce(triggerFullRefinement, 2000)
```

---

## 五、故障排查

### 常见问题

**问题1**: Layer 1不显示
- 检查: `updateStreamingLayer`是否被调用
- 检查: 节流时间是否过长
- 检查: DOM更新是否正常

**问题2**: 导出失败
- 检查: `getMeetingSegments`返回数据
- 检查: 浏览器控制台错误
- 检查: Blob下载权限

**问题3**: 编辑不生效
- 检查: `isEditing`状态更新
- 检查: `saveSegmentEdit`调用
- 检查: 数据持久化

---

## 六、部署检查清单

### 代码审查
- [ ] 三层渲染器正确集成
- [ ] 导出功能完整实现
- [ ] 编辑功能正常工作
- [ ] 所有测试通过
- [ ] 性能优化到位

### 功能验证
- [ ] 真机测试通过
- [ ] 各导出格式正常
- [ ] 编辑保存可靠
- [ ] 性能指标达标

### 文档完善
- [ ] API文档更新
- [ ] 用户手册更新
- [ ] 变更日志记录

---

## 七、预期效果

### 用户体验改进

**Before**:
- 单层渲染，延迟感明显
- 无导出功能
- 无法编辑

**After**:
- 三层渲染，实时反馈
- 5种导出格式
- 可双击编辑

### 性能提升

| 指标 | 优化前 | 优化后 |
|------|--------|--------|
| 首字显示 | 2-3秒 | <500ms |
| 段落确认 | 5-8秒 | 1-2秒 |
| 导出时间 | N/A | <1秒 |

---

**实施人员**: 前端开发工程师  
**预计时间**: 5-8小时  
**优先级**: 高（快速见效）
