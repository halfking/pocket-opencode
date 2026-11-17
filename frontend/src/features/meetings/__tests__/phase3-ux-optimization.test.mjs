/**
 * Phase 3 UX Optimization Tests
 * Testing three-layer rendering, export, and editing features
 */

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { ThreeLayerRenderer, SmartScroller } from '../three-layer-renderer.ts'
import { exportToText, exportToHtml, exportToSrt, exportToBilingual } from '../export-formats.ts'

describe('ThreeLayerRenderer', () => {
  it('should add interim segments (Layer 1)', () => {
    const renderer = new ThreeLayerRenderer()
    
    renderer.addInterim({
      id: 'interim-1',
      text: '你好',
      confidence: 0.5,
      startMs: 1000,
      endMs: 1500,
    })

    const segments = renderer.render()
    assert.equal(segments.length, 1)
    assert.equal(segments[0].layer, 1)
    assert.equal(segments[0].text, '你好')
    assert.equal(segments[0].editable, false)
  })

  it('should confirm partial segment (Layer 2) and clear Layer 1', () => {
    const renderer = new ThreeLayerRenderer()
    
    renderer.addInterim({
      id: 'interim-1',
      text: '你好',
      confidence: 0.5,
      startMs: 1000,
      endMs: 1500,
    })

    renderer.confirmPartial({
      id: 'partial-1',
      text: '你好世界',
      confidence: 0.9,
      startMs: 1000,
      endMs: 2000,
    })

    const segments = renderer.render()
    assert.equal(segments.length, 1)
    assert.equal(segments[0].layer, 2)
    assert.equal(segments[0].text, '你好世界')
    assert.equal(segments[0].editable, true)
  })

  it('should get full text from all layers', () => {
    const renderer = new ThreeLayerRenderer()
    
    renderer.confirmPartial({
      id: 'partial-1',
      text: '第一段',
      confidence: 0.9,
      startMs: 1000,
      endMs: 2000,
    })

    renderer.confirmPartial({
      id: 'partial-2',
      text: '第二段',
      confidence: 0.9,
      startMs: 2000,
      endMs: 3000,
    })

    const fullText = renderer.getFullText()
    assert.equal(fullText, '第一段第二段')
  })
})

describe('Export Functions', () => {
  const mockSegments = [
    {
      id: '1',
      text: '你好世界',
      startMs: 1000,
      endMs: 2000,
      speaker: { profileId: 's1', label: '说话人A', color: '#ff0000' }
    }
  ]

  it('should export to text format with content', () => {
    const result = exportToText(mockSegments, {
      format: 'txt',
      includeTimestamps: false,
      includeSpeakers: false,
      title: '测试会议'
    })
    
    assert.ok(result.includes('你好世界'), 'Should include segment text')
  })

  it('should export to SRT format', () => {
    const result = exportToSrt(mockSegments)
    
    assert.ok(result.includes('1'), 'Should include sequence number')
    assert.ok(result.includes('你好世界'), 'Should include segment text')
  })
})
