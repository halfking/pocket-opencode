/**
 * liveSubtitle Store 单元测试
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useLiveSubtitleStore } from '../liveSubtitle'

describe('useLiveSubtitleStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    // Mock fetch
    global.fetch = vi.fn()
  })

  describe('基础状态', () => {
    it('初始状态正确', () => {
      const store = useLiveSubtitleStore()
      expect(store.segments).toEqual([])
      expect(store.visible).toBe(false)
      expect(store.pinned).toBe(false)
      expect(store.translationEnabled).toBe(false)
      expect(store.targetLang).toBe('zh')
      expect(store.activeMeetingId).toBeNull()
    })

    it('segmentCount 计算属性正确', () => {
      const store = useLiveSubtitleStore()
      expect(store.segmentCount).toBe(0)
      
      store.addSegment({ meetingId: 'm1', speaker: 'A', text: 'Hello', timestamp: 0 })
      expect(store.segmentCount).toBe(1)
    })

    it('hasSegments 计算属性正确', () => {
      const store = useLiveSubtitleStore()
      expect(store.hasSegments).toBe(false)
      
      store.addSegment({ meetingId: 'm1', speaker: 'A', text: 'Hello', timestamp: 0 })
      expect(store.hasSegments).toBe(true)
    })
  })

  describe('会话管理', () => {
    it('startSession 正确初始化', () => {
      const store = useLiveSubtitleStore()
      store.segments.push({ id: 's1', meetingId: 'm1', speaker: 'A', text: 'old', timestamp: 0 })
      
      store.startSession('m2')
      
      expect(store.activeMeetingId).toBe('m2')
      expect(store.segments).toEqual([])
      expect(store.visible).toBe(true)
    })

    it('endSession 隐藏窗口但保留片段', () => {
      const store = useLiveSubtitleStore()
      store.startSession('m1')
      store.addSegment({ meetingId: 'm1', speaker: 'A', text: 'Hello', timestamp: 0 })
      
      store.endSession()
      
      expect(store.activeMeetingId).toBeNull()
      expect(store.visible).toBe(false)
      expect(store.segments.length).toBe(1) // 保留片段
    })
  })

  describe('片段管理', () => {
    it('addSegment 添加片段并生成ID', () => {
      const store = useLiveSubtitleStore()
      store.addSegment({ meetingId: 'm1', speaker: 'A', text: 'Hello', timestamp: 1000 })
      
      expect(store.segments).toHaveLength(1)
      expect(store.segments[0]).toMatchObject({
        meetingId: 'm1',
        speaker: 'A',
        text: 'Hello',
        timestamp: 1000
      })
      expect(store.segments[0].id).toMatch(/^seg-/)
    })

    it('addSegment 超过1000条自动裁剪', () => {
      const store = useLiveSubtitleStore()
      // 添加 1050 条
      for (let i = 0; i < 1050; i++) {
        store.addSegment({ meetingId: 'm1', speaker: 'A', text: `text${i}`, timestamp: i })
      }
      
      expect(store.segments.length).toBe(550) // 裁剪到 550 条
    })

    it('updateSegment 更新指定片段', () => {
      const store = useLiveSubtitleStore()
      store.addSegment({ meetingId: 'm1', speaker: 'A', text: 'Hello', timestamp: 0 })
      const id = store.segments[0].id
      
      store.updateSegment(id, { text: 'Hi', speaker: 'B' })
      
      expect(store.segments[0].text).toBe('Hi')
      expect(store.segments[0].speaker).toBe('B')
    })

    it('clearSegments 清空所有片段', () => {
      const store = useLiveSubtitleStore()
      store.addSegment({ meetingId: 'm1', speaker: 'A', text: 'Hello', timestamp: 0 })
      store.addSegment({ meetingId: 'm1', speaker: 'B', text: 'Hi', timestamp: 1000 })
      
      store.clearSegments()
      
      expect(store.segments).toEqual([])
    })
  })

  describe('翻译功能', () => {
    it('toggleTranslation 切换翻译状态', () => {
      const store = useLiveSubtitleStore()
      expect(store.translationEnabled).toBe(false)
      
      store.toggleTranslation()
      expect(store.translationEnabled).toBe(true)
      
      store.toggleTranslation()
      expect(store.translationEnabled).toBe(false)
    })

    it('setTargetLang 切换目标语言并清除翻译', () => {
      const store = useLiveSubtitleStore()
      store.addSegment({ meetingId: 'm1', speaker: 'A', text: 'Hello', timestamp: 0 })
      store.segments[0].translation = '你好'
      
      store.setTargetLang('ja')
      
      expect(store.targetLang).toBe('ja')
      expect(store.segments[0].translation).toBeUndefined()
    })

    it('setTargetLang 相同语言不清除翻译', () => {
      const store = useLiveSubtitleStore()
      store.addSegment({ meetingId: 'm1', speaker: 'A', text: 'Hello', timestamp: 0 })
      store.segments[0].translation = '你好'
      
      store.setTargetLang('zh')
      
      expect(store.segments[0].translation).toBe('你好')
    })
  })

  describe('导出功能', () => {
    it('exportSRT 生成正确的SRT格式', () => {
      const store = useLiveSubtitleStore()
      store.addSegment({ meetingId: 'm1', speaker: 'A', text: 'Hello', timestamp: 0 })
      store.addSegment({ meetingId: 'm1', speaker: 'B', text: 'Hi', timestamp: 3000 })
      store.segments[0].translation = '你好'
      
      const srt = store.exportSRT()
      
      expect(srt).toContain('1\n')
      expect(srt).toContain('00:00:00,000 --> 00:00:03,000')
      expect(srt).toContain('[A] Hello')
      expect(srt).toContain('你好')
      expect(srt).toContain('2\n')
      expect(srt).toContain('[B] Hi')
    })

    it('exportText 生成纯文本格式', () => {
      const store = useLiveSubtitleStore()
      store.addSegment({ meetingId: 'm1', speaker: 'A', text: 'Hello', timestamp: 0 })
      store.addSegment({ meetingId: 'm1', speaker: 'B', text: 'Hi', timestamp: 1000 })
      store.segments[0].translation = '你好'
      
      const text = store.exportText()
      
      expect(text).toContain('[A] Hello')
      expect(text).toContain('→ 你好')
      expect(text).toContain('[B] Hi')
    })

    it('exportText 无说话人时不显示标签', () => {
      const store = useLiveSubtitleStore()
      store.addSegment({ meetingId: 'm1', speaker: null, text: 'Hello', timestamp: 0 })
      
      const text = store.exportText()
      
      expect(text).toBe('Hello')
      expect(text).not.toContain('[')
    })
  })

  describe('位置持久化', () => {
    it('savePosition 保存到 localStorage', () => {
      const store = useLiveSubtitleStore()
      store.position = { x: 100, y: 200 }
      
      store.savePosition()
      
      const saved = localStorage.getItem('liveSubtitle.position')
      expect(saved).toBe(JSON.stringify({ x: 100, y: 200 }))
    })

    it('loadPosition 从 localStorage 加载', () => {
      localStorage.setItem('liveSubtitle.position', JSON.stringify({ x: 150, y: 250 }))
      
      const store = useLiveSubtitleStore()
      store.loadPosition()
      
      expect(store.position).toEqual({ x: 150, y: 250 })
    })

    it('loadPosition 无保存数据时使用默认值', () => {
      localStorage.removeItem('liveSubtitle.position')
      
      const store = useLiveSubtitleStore()
      store.loadPosition()
      
      expect(store.position).toEqual({ x: 20, y: 100 })
    })
  })
})
