/**
 * vad-factory.ts — VAD 方案工厂
 * 
 * 提供统一接口来创建和切换不同的 VAD 实现：
 * 1. 能量检测 VAD (vad-segmenter.ts)
 * 2. Silero VAD (vad-web-segmenter.ts)
 * 
 * 用于真机 A/B 测试和性能对比
 */

import { VadSegmenter, type VadSegmenterOptions } from './vad-segmenter'
import { VadWebSegmenter, type VadWebSegmenterOptions } from './vad-web-segmenter'

export type VadType = 'energy' | 'silero'

export interface VadConfig {
  type: VadType
  minSpeechMs?: number
  maxSpeechMs?: number
  sliceMs?: number
  // 能量检测特定配置
  silenceMs?: number
  energyThreshold?: number
  // Silero VAD 特定配置
  preSpeechPadMs?: number
  postSpeechPadMs?: number
}

export interface VadInstance {
  start(stream: MediaStream): Promise<void>
  stop(): Promise<Blob | null>
  getStream(): MediaStream | null
  type: VadType
}

/**
 * 创建 VAD 实例
 */
export async function createVad(
  config: VadConfig,
  onSegment: (seg: { blob: Blob; startMs: number; endMs: number }) => void
): Promise<VadInstance> {
  const startTime = performance.now()
  
  console.log(`[vad-factory] Creating VAD: ${config.type}`)
  
  if (config.type === 'silero') {
    // 尝试创建 Silero VAD
    try {
      const options: VadWebSegmenterOptions = {
        minSpeechMs: config.minSpeechMs,
        maxSpeechMs: config.maxSpeechMs,
        sliceMs: config.sliceMs,
        preSpeechPadMs: config.preSpeechPadMs,
        postSpeechPadMs: config.postSpeechPadMs,
        onSegment,
        onVadReady: () => {
          const elapsed = performance.now() - startTime
          console.log(`[vad-factory] Silero VAD ready in ${elapsed.toFixed(0)}ms`)
        },
        onVadError: (error) => {
          console.error('[vad-factory] Silero VAD error:', error)
        }
      }
      
      const vad = new VadWebSegmenter(options)
      
      return {
        async start(stream: MediaStream) {
          await vad.start(stream)
        },
        async stop() {
          return await vad.stop()
        },
        getStream() {
          return vad.getStream()
        },
        type: 'silero'
      }
    } catch (error) {
      console.error('[vad-factory] Failed to create Silero VAD, falling back to energy detection:', error)
      // 回退到能量检测
      config.type = 'energy'
    }
  }
  
  // 创建能量检测 VAD
  const options: VadSegmenterOptions = {
    minSpeechMs: config.minSpeechMs,
    maxSpeechMs: config.maxSpeechMs,
    sliceMs: config.sliceMs,
    silenceMs: config.silenceMs,
    energyThreshold: config.energyThreshold,
    onSegment
  }
  
  const vad = new VadSegmenter(options)
  const elapsed = performance.now() - startTime
  console.log(`[vad-factory] Energy VAD ready in ${elapsed.toFixed(0)}ms`)
  
  return {
    async start(stream: MediaStream) {
      await vad.start(stream)
    },
    async stop() {
      return await vad.stop()
    },
    getStream() {
      return vad.getStream()
    },
    type: 'energy'
  }
}

/**
 * 从 localStorage 读取用户偏好的 VAD 方案
 */
export function getPreferredVadType(): VadType {
  try {
    const stored = localStorage.getItem('vad-type')
    if (stored === 'silero' || stored === 'energy') {
      return stored
    }
  } catch (err) {
    console.warn('[vad-factory] Failed to read vad-type from localStorage:', err)
  }
  // 默认使用能量检测（稳定方案）
  return 'energy'
}

/**
 * 保存用户偏好的 VAD 方案
 */
export function setPreferredVadType(type: VadType): void {
  try {
    localStorage.setItem('vad-type', type)
    console.log(`[vad-factory] VAD preference saved: ${type}`)
  } catch (err) {
    console.warn('[vad-factory] Failed to save vad-type to localStorage:', err)
  }
}

/**
 * 获取默认 VAD 配置
 */
export function getDefaultVadConfig(type?: VadType): VadConfig {
  const preferredType = type || getPreferredVadType()
  
  const baseConfig = {
    minSpeechMs: 400,
    maxSpeechMs: 20_000,
    sliceMs: 250,
  }
  
  if (preferredType === 'silero') {
    return {
      ...baseConfig,
      type: 'silero',
      preSpeechPadMs: 300,
      postSpeechPadMs: 300,
    }
  }
  
  return {
    ...baseConfig,
    type: 'energy',
    silenceMs: 1500,
    energyThreshold: 0.015,
  }
}
