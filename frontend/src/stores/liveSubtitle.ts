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
import { ref, computed, watch, onScopeDispose } from 'vue'
import { captureAuthAttempt, captureAuthSession, isCurrentAuthSession, useAuthStore, type AuthSession } from './auth.ts'
import { fetchWithTimeout } from '../api/fetch-timeout.ts'

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

type SubtitleStorageLike = {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
  removeItem(key: string): void
}

let injectedStorage: SubtitleStorageLike | null = null

/** 测试注入内存存储；传 null 恢复默认真实 localStorage。 */
export function installLiveSubtitleStorage(storage: SubtitleStorageLike | null): void {
  injectedStorage = storage
}

function subtitleStorage(): SubtitleStorageLike | null {
  if (injectedStorage) return injectedStorage
  if (typeof window === 'undefined') return null
  return window.localStorage
}

export const useLiveSubtitleStore = defineStore('liveSubtitle', () => {
  const auth = useAuthStore()
  let ownerGeneration = captureAuthAttempt(auth).generation

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
    invalidateTranslations()
    activeMeetingId.value = meetingId
    segments.value = []
    visible.value = true
    loadPosition()
  }

  /**
   * 结束当前字幕会话
   */
  function endSession() {
    invalidateTranslations()
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
      if (updates.text !== undefined && translationEnabled.value && !updates.translation) {
        seg.translation = undefined
        requestTranslation(id, updates.text, seg.lang)
      }
    }
  }

  /**
   * 清空所有片段
   */
  function clearSegments() {
    invalidateTranslations()
    segments.value = []
  }

  interface Translation {
    id: string; text: string; sourceLang?: string; lang: string
    meeting: string; epoch: object; session: AuthSession; version: object
  }
  let translationEpoch = {}
  let disposed = false
  const timers = new Map<string, ReturnType<typeof setTimeout>>()
  const versions = new Map<string, object>()
  const ready = new Map<string, Translation>()
  const active = new Map<Translation, AbortController>()
  const currentTranslation = (job: Translation) => !disposed && translationEnabled.value &&
    job.epoch === translationEpoch && job.lang === targetLang.value &&
    job.meeting === activeMeetingId.value && versions.get(job.id) === job.version &&
    isCurrentAuthSession(auth, job.session) && segments.value.some(seg =>
      seg.id === job.id && seg.meetingId === job.meeting && seg.text === job.text)

  function invalidateTranslations() {
    translationEpoch = {}
    for (const timer of timers.values()) clearTimeout(timer)
    timers.clear(); ready.clear(); versions.clear()
    for (const controller of active.values()) controller.abort()
    for (const seg of segments.value) seg.translating = false
  }

  // Per-segment debounce retains every segment; at most three HTTP flights.
  function requestTranslation(id: string, text: string, sourceLang?: string) {
    const meeting = activeMeetingId.value
    const seg = segments.value.find(s => s.id === id)
    if (disposed || !meeting || !seg || seg.meetingId !== meeting || !text || auth.activationStatus !== 'ready') return
    const session = captureAuthSession(auth), version = {}
    if (!auth.userId || !auth.workspaceId || !auth.isAuthenticated) return
    clearTimeout(timers.get(id)); ready.delete(id)
    for (const [job, controller] of active) if (job.id === id) controller.abort()
    versions.set(id, version)
    const job: Translation = { id, text, sourceLang, lang: targetLang.value, meeting, epoch: translationEpoch, session, version }
    timers.set(id, setTimeout(() => { timers.delete(id); ready.set(id, job); drainTranslations() }, 500))
  }

  function drainTranslations() {
    while (active.size < 3 && ready.size) {
      const [id, job] = ready.entries().next().value!
      ready.delete(id)
      if (!currentTranslation(job)) {
        if (versions.get(id) === job.version) versions.delete(id)
        continue
      }
      const controller = new AbortController()
      active.set(job, controller)
      void translate(job, controller)
    }
  }

  async function translate(job: Translation, controller: AbortController) {
    const seg = segments.value.find(s => s.id === job.id)!
    seg.translating = true
    try {
      const response = await fetchWithTimeout(`/api/meetings/${encodeURIComponent(job.meeting)}/translate`, {
        method: 'POST',
        signal: controller.signal,
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${auth.token}` },
        body: JSON.stringify({
          text: job.text,
          sourceLang: job.sourceLang || 'auto',
          targetLang: job.lang,
        }),
      }, { timeoutMs: 30_000, label: 'meeting-subtitle-translation' })
      if (!currentTranslation(job)) return
      if (!response.ok) throw new Error(`HTTP ${response.status}`)
      
      const result = await response.json() as { translation: string; detectedLang?: string }
      if (!currentTranslation(job)) return
      seg.translation = result.translation
      if (result.detectedLang) seg.lang = result.detectedLang
    } catch (error) {
      if (!currentTranslation(job)) return
      console.warn('[liveSubtitle] Translation failed:', error)
      seg.translation = undefined // 翻译失败，不显示
    } finally {
      if (currentTranslation(job)) seg.translating = false
      if (versions.get(job.id) === job.version) versions.delete(job.id)
      active.delete(job)
      drainTranslations()
    }
  }

  watch(() => [auth.token, auth.userId, auth.workspaceId, auth.activationStatus, auth.activationVersion], () => {
    const generation = captureAuthAttempt(auth).generation
    if (generation === ownerGeneration && auth.activationStatus === 'ready') return
    ownerGeneration = generation
    invalidateTranslations()
    segments.value = []; activeMeetingId.value = null; visible.value = false
  }, { flush: 'sync' })
  onScopeDispose(() => { disposed = true; invalidateTranslations() })

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
    } else invalidateTranslations()
  }

  /**
   * 设置目标语言
   */
  function setTargetLang(lang: string) {
    if (targetLang.value === lang) return
    invalidateTranslations()
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
      subtitleStorage()?.setItem('liveSubtitle.position', JSON.stringify(position.value))
    } catch (e) {
      console.warn('[liveSubtitle] Failed to save position:', e)
    }
  }

  /**
   * 从 localStorage 加载悬浮窗位置
   */
  function loadPosition() {
    try {
      const saved = subtitleStorage()?.getItem('liveSubtitle.position')
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
