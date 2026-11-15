/**
 * liveSubtitle Store 单元测试
 */
import { strict as assert } from 'node:assert'
import { describe, it, beforeEach, afterEach, after } from 'node:test'
import { setActivePinia, createPinia } from 'pinia'
import { loadSubtitleModules } from './subtitle-fixture.mjs'
const modules = await loadSubtitleModules()
const { installLiveSubtitleStorage, useLiveSubtitleStore, useAuthStore } = modules
after(() => modules.close())

function memoryStorage() {
  const map = new Map<string, string>()
  return {
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => { map.set(key, value) },
    removeItem: (key: string) => { map.delete(key) },
    dump: () => map,
  }
}

let storage = memoryStorage()

describe('useLiveSubtitleStore', () => {
  beforeEach(() => {
    storage = memoryStorage()
    installLiveSubtitleStorage(storage)
    setActivePinia(createPinia())
    useAuthStore().$patch({ token: 'fixture', userId: 'alice', workspaceId: 'one', activationStatus: 'ready' })
  })
  afterEach(() => { useLiveSubtitleStore().$dispose(); useAuthStore().$dispose() })

  describe('基础状态', () => {
    it('初始状态正确', () => {
      const store = useLiveSubtitleStore()
      assert.deepEqual(store.segments, [])
      assert.equal(store.visible, false)
      assert.equal(store.pinned, false)
      assert.equal(store.translationEnabled, false)
      assert.equal(store.targetLang, 'zh')
      assert.equal(store.activeMeetingId, null)
    })

    it('segmentCount 计算属性正确', () => {
      const store = useLiveSubtitleStore()
      assert.equal(store.segmentCount, 0)
      
      store.addSegment({ meetingId: 'm1', speaker: 'A', text: 'Hello', timestamp: 0 })
      assert.equal(store.segmentCount, 1)
    })

    it('hasSegments 计算属性正确', () => {
      const store = useLiveSubtitleStore()
      assert.equal(store.hasSegments, false)
      
      store.addSegment({ meetingId: 'm1', speaker: 'A', text: 'Hello', timestamp: 0 })
      assert.equal(store.hasSegments, true)
    })
  })

  describe('会话管理', () => {
    it('startSession 正确初始化', () => {
      const store = useLiveSubtitleStore()
      store.segments.push({ id: 's1', meetingId: 'm1', speaker: 'A', text: 'old', timestamp: 0 })
      
      store.startSession('m2')
      
      assert.equal(store.activeMeetingId, 'm2')
      assert.deepEqual(store.segments, [])
      assert.equal(store.visible, true)
    })

    it('endSession 隐藏窗口但保留片段', () => {
      const store = useLiveSubtitleStore()
      store.startSession('m1')
      store.addSegment({ meetingId: 'm1', speaker: 'A', text: 'Hello', timestamp: 0 })
      
      store.endSession()
      
      assert.equal(store.activeMeetingId, null)
      assert.equal(store.visible, false)
      assert.equal(store.segments.length, 1) // 保留片段
    })
  })

  describe('片段管理', () => {
    it('addSegment 添加片段并生成ID', () => {
      const store = useLiveSubtitleStore()
      store.addSegment({ meetingId: 'm1', speaker: 'A', text: 'Hello', timestamp: 1000 })
      
      assert.equal(store.segments.length, 1)
      assert.deepEqual({
        meetingId: 'm1',
        speaker: 'A',
        text: 'Hello',
        timestamp: 1000
      }, {
        meetingId: store.segments[0].meetingId,
        speaker: store.segments[0].speaker,
        text: store.segments[0].text,
        timestamp: store.segments[0].timestamp,
      })
      assert.match(store.segments[0].id, /^seg-/)
    })

    it('addSegment 超过1000条自动裁剪', () => {
      const store = useLiveSubtitleStore()
      // 添加 1050 条
      for (let i = 0; i < 1050; i++) {
        store.addSegment({ meetingId: 'm1', speaker: 'A', text: `text${i}`, timestamp: i })
      }
      
      assert.equal(store.segments.length, 550) // 裁剪到 550 条
    })

    it('updateSegment 更新指定片段', () => {
      const store = useLiveSubtitleStore()
      store.addSegment({ meetingId: 'm1', speaker: 'A', text: 'Hello', timestamp: 0 })
      const id = store.segments[0].id
      
      store.updateSegment(id, { text: 'Hi', speaker: 'B' })
      
      assert.equal(store.segments[0].text, 'Hi')
      assert.equal(store.segments[0].speaker, 'B')
    })

    it('clearSegments 清空所有片段', () => {
      const store = useLiveSubtitleStore()
      store.addSegment({ meetingId: 'm1', speaker: 'A', text: 'Hello', timestamp: 0 })
      store.addSegment({ meetingId: 'm1', speaker: 'B', text: 'Hi', timestamp: 1000 })
      
      store.clearSegments()
      
      assert.deepEqual(store.segments, [])
    })
  })

  describe('翻译功能', () => {
    it('toggleTranslation 切换翻译状态', () => {
      const store = useLiveSubtitleStore()
      assert.equal(store.translationEnabled, false)
      
      store.toggleTranslation()
      assert.equal(store.translationEnabled, true)
      
      store.toggleTranslation()
      assert.equal(store.translationEnabled, false)
    })

    it('setTargetLang 切换目标语言并清除翻译', () => {
      const store = useLiveSubtitleStore()
      store.addSegment({ meetingId: 'm1', speaker: 'A', text: 'Hello', timestamp: 0 })
      store.segments[0].translation = '你好'
      
      store.setTargetLang('ja')
      
      assert.equal(store.targetLang, 'ja')
      assert.equal(store.segments[0].translation, undefined)
    })

    it('setTargetLang 相同语言不清除翻译', () => {
      const store = useLiveSubtitleStore()
      store.addSegment({ meetingId: 'm1', speaker: 'A', text: 'Hello', timestamp: 0 })
      store.segments[0].translation = '你好'
      
      store.setTargetLang('zh')
      
      assert.equal(store.segments[0].translation, '你好')
    })
  })

  describe('导出功能', () => {
    it('exportSRT 生成正确的SRT格式', () => {
      const store = useLiveSubtitleStore()
      store.addSegment({ meetingId: 'm1', speaker: 'A', text: 'Hello', timestamp: 0 })
      store.addSegment({ meetingId: 'm1', speaker: 'B', text: 'Hi', timestamp: 3000 })
      store.segments[0].translation = '你好'
      
      const srt = store.exportSRT()
      
      assert.ok(srt.includes('1\n'))
      assert.ok(srt.includes('00:00:00,000 --> 00:00:03,000'))
      assert.ok(srt.includes('[A] Hello'))
      assert.ok(srt.includes('你好'))
      assert.ok(srt.includes('2\n'))
      assert.ok(srt.includes('[B] Hi'))
    })

    it('exportText 生成纯文本格式', () => {
      const store = useLiveSubtitleStore()
      store.addSegment({ meetingId: 'm1', speaker: 'A', text: 'Hello', timestamp: 0 })
      store.addSegment({ meetingId: 'm1', speaker: 'B', text: 'Hi', timestamp: 1000 })
      store.segments[0].translation = '你好'
      
      const text = store.exportText()
      
      assert.ok(text.includes('[A] Hello'))
      assert.ok(text.includes('→ 你好'))
      assert.ok(text.includes('[B] Hi'))
    })

    it('exportText 无说话人时不显示标签', () => {
      const store = useLiveSubtitleStore()
      store.addSegment({ meetingId: 'm1', speaker: null, text: 'Hello', timestamp: 0 })
      
      const text = store.exportText()
      
      assert.equal(text, 'Hello')
      assert.ok(!text.includes('['))
    })
  })

  describe('位置持久化', () => {
    it('savePosition 保存到 localStorage', () => {
      const store = useLiveSubtitleStore()
      store.position = { x: 100, y: 200 }
      
      store.savePosition()
      
      const saved = storage.getItem('liveSubtitle.position')
      assert.equal(saved, JSON.stringify({ x: 100, y: 200 }))
    })

    it('loadPosition 从 localStorage 加载', () => {
      storage.setItem('liveSubtitle.position', JSON.stringify({ x: 150, y: 250 }))
      
      const store = useLiveSubtitleStore()
      store.loadPosition()
      
      assert.deepEqual(store.position, { x: 150, y: 250 })
    })

    it('loadPosition 无保存数据时使用默认值', () => {
      storage.removeItem('liveSubtitle.position')
      
      const store = useLiveSubtitleStore()
      store.loadPosition()
      
      assert.deepEqual(store.position, { x: 20, y: 100 })
    })
  })
})
