/**
 * liveSubtitle Store —— 实时字幕状态管理（Cicada 启发）
 * 
 * 职责：
 * - 管理实时字幕片段列表
 * - 控制翻译开关和目标语言
 * - 悬浮窗位置和可见性
 * - 与会议录音 runtime 联动
 */
import { defineStore } from 'pinia'
import { ref, computed } from 'vue'

export interface SubtitleSegment {
  id: string
  meetingId: string
  speaker: string | null
  text: string
  translation?: string
  translating?: boolean
  timestamp: number // 相对会议开始的毫秒数
  confidence?: number
  lang?: string // 检测到的源语言
}

export type FontSize = 'small' | 'medium' | 'large'

// 简单的防抖函数实现
function debounce<T extends (...args: any[]) => any>(fn: T, delay: number): T {
  let timeoutId: ReturnType<typeof setTimeout> | null = null
  return ((...args: Parameters<T>) => {
    if (timeoutId) clearTimeout(timeoutId)
    timeoutId = setTimeout(() => fn(...args), delay)
  }) as T
}

export const useLiveSubtitleStore = defineStore('liveSubtitle', () => {

  // ===== 状态 =====
  const segments = ref<SubtitleSegment[]>([])
  const visible = ref(false)
  const pinned = ref(false)
  const translationEnabled = ref(false)
  const targetLang = ref<string>('zh') // zh/en/ja/ko/es/fr/de
  const fontSize = ref<FontSize>('medium')
  const position = ref({ x: 20, y: 100 })
  
  // 当前活跃的会议 ID
  const activeMeetingId = ref<string | null>(null)

  // ===== 计算属性 =====
  const segmentCount = computed(() => segments.value.length)
  const hasSegments = computed(() => segmentCount.value > 0)

  // ===== 方法 =====

  /**
   * 开始一个新会议的字幕会话
   */
  function startSession(meetingId: string) {
    activeMeetingId.value = meetingId
    segments.value = []
    visible.value = true
    loadPosition()
  }

  /**
   * 结束当前字幕会话
   */
  function endSession() {
    activeMeetingId.value = null
    visible.value = false
    // 保留 segments 供回看，不清空
  }

  /**
   * 添加一条新字幕片段
   */
  function addSegment(seg: Omit<SubtitleSegment, 'id'>) {
    const id = `seg-${Date.now()}-${Math.random().toString(36).slice(2)}`
    const newSeg: SubtitleSegment = { ...seg, id }
    segments.value.push(newSeg)

    // 如果翻译已开启，立即请求翻译
    if (translationEnabled.value && !newSeg.translation) {
      requestTranslation(id, newSeg.text, newSeg.lang)
    }

    // 限制内存：超过 1000 条后裁剪前 500 条
    if (segments.value.length > 1000) {
      segments.value = segments.value.slice(500)
    }
  }

  /**
   * 更新指定片段（用于修正、合并等）
   */
  function updateSegment(id: string, updates: Partial<SubtitleSegment>) {
    const seg = segments.value.find(s => s.id === id)
    if (seg) {
      Object.assign(seg, updates)
      // 如果文本变了且翻译开启，重新翻译
      if (updates.text && translationEnabled.value && !updates.translation) {
        requestTranslation(id, updates.text, seg.lang)
      }
    }
  }

  /**
   * 清空所有片段
   */
  function clearSegments() {
    segments.value = []
  }

  /**
   * 请求翻译（防抖 500ms，合并连续请求）
   */
  const requestTranslation = debounce(async (
    id: string, 
    text: string, 
    sourceLang?: string
  ) => {
    const seg = segments.value.find(s => s.id === id)
    if (!seg) return

    seg.translating = true
    try {
      const response = await fetch('/api/meetings/translate', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          text,
          sourceLang: sourceLang || 'auto',
          targetLang: targetLang.value,
        }),
      })
      if (!response.ok) throw new Error(`HTTP ${response.status}`)
      
      const result = await response.json() as { translation: string; detectedLang?: string }
      seg.translation = result.translation
      if (result.detectedLang) seg.lang = result.detectedLang
    } catch (error) {
      console.warn('[liveSubtitle] Translation failed:', error)
      seg.translation = undefined // 翻译失败，不显示
    } finally {
      seg.translating = false
    }
  }, 500)

  /**
   * 为所有未翻译的片段请求翻译（用于开启翻译时批量处理）
   */
  async function translateAllPending() {
    const pending = segments.value.filter(s => !s.translation && !s.translating)
    for (const seg of pending) {
      requestTranslation(seg.id, seg.text, seg.lang)
    }
  }

  /**
   * 切换翻译开关
   */
  function toggleTranslation() {
    translationEnabled.value = !translationEnabled.value
    if (translationEnabled.value) {
      translateAllPending()
    }
  }

  /**
   * 设置目标语言
   */
  function setTargetLang(lang: string) {
    if (targetLang.value === lang) return
    targetLang.value = lang
    // 清除所有已有翻译，重新翻译
    for (const seg of segments.value) {
      seg.translation = undefined
      seg.translating = false
    }
    if (translationEnabled.value) {
      translateAllPending()
    }
  }

  /**
   * 保存悬浮窗位置到 localStorage
   */
  function savePosition() {
    try {
      localStorage.setItem('liveSubtitle.position', JSON.stringify(position.value))
    } catch (e) {
      console.warn('[liveSubtitle] Failed to save position:', e)
    }
  }

  /**
   * 从 localStorage 加载悬浮窗位置
   */
  function loadPosition() {
    try {
      const saved = localStorage.getItem('liveSubtitle.position')
      if (saved) {
        const parsed = JSON.parse(saved)
        position.value = { x: parsed.x || 20, y: parsed.y || 100 }
      }
    } catch (e) {
      console.warn('[liveSubtitle] Failed to load position:', e)
    }
  }

  /**
   * 导出字幕为 SRT 格式
   */
  function exportSRT(): string {
    let srt = ''
    for (let i = 0; i < segments.value.length; i++) {
      const seg = segments.value[i]
      const startMs = seg.timestamp
      const endMs = i < segments.value.length - 1 
        ? segments.value[i + 1].timestamp 
        : startMs + 3000 // 最后一条默认 3 秒

      const startTime = formatSRTTime(startMs)
      const endTime = formatSRTTime(endMs)

      srt += `${i + 1}\n`
      srt += `${startTime} --> ${endTime}\n`
      srt += `${seg.speaker ? `[${seg.speaker}] ` : ''}${seg.text}\n`
      if (seg.translation) {
        srt += `${seg.translation}\n`
      }
      srt += '\n'
    }
    return srt
  }

  /**
   * 格式化 SRT 时间戳 (00:00:00,000)
   */
  function formatSRTTime(ms: number): string {
    const hours = Math.floor(ms / 3600000)
    const minutes = Math.floor((ms % 3600000) / 60000)
    const seconds = Math.floor((ms % 60000) / 1000)
    const millis = ms % 1000
    return `${String(hours).padStart(2, '0')}:${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')},${String(millis).padStart(3, '0')}`
  }

  /**
   * 导出字幕为纯文本
   */
  function exportText(): string {
    return segments.value
      .map(seg => {
        let line = seg.speaker ? `[${seg.speaker}] ${seg.text}` : seg.text
        if (seg.translation) line += `\n  → ${seg.translation}`
        return line
      })
      .join('\n\n')
  }

  return {
    // 状态
    segments,
    visible,
    pinned,
    translationEnabled,
    targetLang,
    fontSize,
    position,
    activeMeetingId,

    // 计算属性
    segmentCount,
    hasSegments,

    // 方法
    startSession,
    endSession,
    addSegment,
    updateSegment,
    clearSegments,
    toggleTranslation,
    setTargetLang,
    translateAllPending,
    savePosition,
    loadPosition,
    exportSRT,
    exportText,
  }
})
