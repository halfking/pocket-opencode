<template>
  <Sheet v-model="isOpen" title="导出转写记录">
    <div class="export-sheet">
      <!-- Format selection -->
      <div class="section">
        <h3>导出格式</h3>
        <div class="format-grid">
          <button
            v-for="fmt in formats"
            :key="fmt.value"
            class="format-option"
            :class="{ active: selectedFormat === fmt.value }"
            @click="selectedFormat = fmt.value as typeof selectedFormat"
          >
            <span class="format-icon">{{ fmt.icon }}</span>
            <div class="format-info">
              <div class="format-name">{{ fmt.name }}</div>
              <div class="format-desc">{{ fmt.description }}</div>
            </div>
          </button>
        </div>
      </div>

      <!-- Export options -->
      <div class="section">
        <h3>导出选项</h3>
        <label class="checkbox-label">
          <input type="checkbox" v-model="options.includeTimestamps" />
          <span>包含时间戳</span>
        </label>
        <label class="checkbox-label">
          <input type="checkbox" v-model="options.includeSpeakers" />
          <span>包含说话人</span>
        </label>
        <label class="checkbox-label" v-if="hasTranslations">
          <input type="checkbox" v-model="options.includeTranslation" />
          <span>包含翻译</span>
        </label>
      </div>

      <!-- File name -->
      <div class="section">
        <h3>文件名</h3>
        <input
          v-model="fileName"
          type="text"
          class="file-name-input"
          placeholder="输入文件名..."
        />
      </div>

      <!-- Actions -->
      <div class="actions">
        <button type="button" class="btn-secondary" @click="close">
          取消
        </button>
        <button 
          type="button" 
          class="btn-primary" 
          @click="exportFile"
          :disabled="exporting"
        >
          {{ exporting ? '导出中...' : '导出' }}
        </button>
      </div>

      <!-- Export status -->
      <div v-if="exportStatus" class="status" :class="exportStatus.type">
        {{ exportStatus.message }}
      </div>
    </div>
  </Sheet>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import Sheet from '@/components/interactive/Sheet.vue'
import {
  exportToText,
  exportToHtml,
  exportToSrt,
  exportToBilingual,
  exportToDocx,
  type ExportOptions
} from './export-formats'
import type { MeetingSegment } from './meetings-store'

interface Props {
  modelValue: boolean
  segments: MeetingSegment[]
  title?: string
}

const props = withDefaults(defineProps<Props>(), {
  title: '会议记录'
})

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
}>()

const isOpen = computed({
  get: () => props.modelValue,
  set: (val) => emit('update:modelValue', val)
})

// Export state
const selectedFormat = ref<'txt' | 'html' | 'srt' | 'bilingual' | 'docx'>('txt')
const fileName = ref('')

// Initialize fileName when props change
watch(() => props.title, (newTitle: string | undefined) => {
  if (!fileName.value) {
    fileName.value = newTitle || '会议记录'
  }
}, { immediate: true })
const exporting = ref(false)
const exportStatus = ref<{ type: 'success' | 'error', message: string } | null>(null)

// Options
const options = ref<ExportOptions>({
  format: 'txt',
  includeTimestamps: true,
  includeSpeakers: true,
  includeTranslation: false,
  title: props.title
})

// Format definitions
const formats = [
  {
    value: 'txt',
    name: 'TXT',
    description: '纯文本，兼容性最好',
    icon: '📄',
    ext: 'txt'
  },
  {
    value: 'html',
    name: 'HTML',
    description: '网页格式，可在浏览器查看',
    icon: '🌐',
    ext: 'html'
  },
  {
    value: 'srt',
    name: 'SRT',
    description: '字幕文件，可导入视频编辑软件',
    icon: '🎬',
    ext: 'srt'
  },
  {
    value: 'bilingual',
    name: '双语对照',
    description: '原文与翻译对照',
    icon: '🌍',
    ext: 'txt'
  },
  {
    value: 'docx',
    name: 'Word',
    description: 'Microsoft Word 文档',
    icon: '📝',
    ext: 'docx'
  }
]

const hasTranslations = computed(() => 
  props.segments.some(s => s.translation)
)

function close() {
  isOpen.value = false
  exportStatus.value = null
}

async function exportFile() {
  if (exporting.value) return
  
  exporting.value = true
  exportStatus.value = null

  try {
    options.value.format = selectedFormat.value
    options.value.title = fileName.value

    let content: string | Blob
    let mimeType: string
    const format = formats.find(f => f.value === selectedFormat.value)!

    switch (selectedFormat.value) {
      case 'txt':
        content = exportToText(props.segments, options.value)
        mimeType = 'text/plain'
        break
      case 'html':
        content = exportToHtml(props.segments, options.value)
        mimeType = 'text/html'
        break
      case 'srt':
        content = exportToSrt(props.segments)
        mimeType = 'text/srt'
        break
      case 'bilingual':
        content = exportToBilingual(props.segments)
        mimeType = 'text/plain'
        break
      case 'docx':
        content = await exportToDocx(props.segments, options.value)
        mimeType = 'application/vnd.openxmlformats-officedocument.wordprocessingml.document'
        break
      default:
        throw new Error('Unsupported format')
    }

    // Create download
    const blob = content instanceof Blob ? content : new Blob([content], { type: mimeType })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${fileName.value}.${format.ext}`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)

    exportStatus.value = {
      type: 'success',
      message: '✓ 导出成功！'
    }

    setTimeout(() => {
      close()
    }, 1500)

  } catch (error) {
    console.error('Export failed:', error)
    exportStatus.value = {
      type: 'error',
      message: '✗ 导出失败，请重试'
    }
  } finally {
    exporting.value = false
  }
}
</script>

<style scoped>
.export-sheet {
  padding: 16px;
}

.section {
  margin-bottom: 24px;
}

.section h3 {
  font-size: 16px;
  font-weight: 600;
  color: #1f2937;
  margin-bottom: 12px;
}

.format-grid {
  display: grid;
  gap: 12px;
}

.format-option {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px;
  border: 2px solid #e5e7eb;
  border-radius: 8px;
  background: white;
  cursor: pointer;
  transition: all 0.2s;
  text-align: left;
}

.format-option:hover {
  border-color: #3b82f6;
  background: #eff6ff;
}

.format-option.active {
  border-color: #3b82f6;
  background: #dbeafe;
}

.format-icon {
  font-size: 32px;
}

.format-info {
  flex: 1;
}

.format-name {
  font-weight: 600;
  color: #1f2937;
  margin-bottom: 4px;
}

.format-desc {
  font-size: 13px;
  color: #6b7280;
}

.checkbox-label {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 0;
  cursor: pointer;
}

.checkbox-label input[type="checkbox"] {
  width: 18px;
  height: 18px;
  cursor: pointer;
}

.file-name-input {
  width: 100%;
  padding: 10px 12px;
  border: 2px solid #e5e7eb;
  border-radius: 8px;
  font-size: 15px;
  transition: border-color 0.2s;
}

.file-name-input:focus {
  outline: none;
  border-color: #3b82f6;
}

.actions {
  display: flex;
  gap: 12px;
  margin-top: 24px;
}

.btn-primary,
.btn-secondary {
  flex: 1;
  padding: 12px;
  border: none;
  border-radius: 8px;
  font-size: 15px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.2s;
}

.btn-primary {
  background: #3b82f6;
  color: white;
}

.btn-primary:hover:not(:disabled) {
  background: #2563eb;
}

.btn-primary:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn-secondary {
  background: #f3f4f6;
  color: #374151;
}

.btn-secondary:hover {
  background: #e5e7eb;
}

.status {
  margin-top: 16px;
  padding: 12px;
  border-radius: 8px;
  text-align: center;
  font-weight: 500;
}

.status.success {
  background: #d1fae5;
  color: #065f46;
}

.status.error {
  background: #fee2e2;
  color: #991b1b;
}
</style>
