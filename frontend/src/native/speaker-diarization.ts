/**
 * speaker-diarization.ts — 增量说话人聚类
 *
 * 新 embedding 与已有 profile 比对（余弦 > threshold 视为同一人）。
 * 冷启动时显示「说话人 N」，用户标注后写入 voiceprints 库。
 */
import { cosineSimilarity } from './speaker-embedding'

export interface SpeakerProfile {
  id: string
  label: string
  embedding: Float32Array
  sampleCount: number
}

export class SpeakerDiarizer {
  private profiles: SpeakerProfile[] = []
  private nextId = 1
  private threshold: number

  constructor(threshold = 0.72) {
    this.threshold = threshold
  }

  loadProfiles(profiles: SpeakerProfile[]) {
    this.profiles = profiles.map((p) => ({ ...p }))
    const nums = profiles
      .map((p) => parseInt(p.label.replace(/\D/g, ''), 10))
      .filter((n) => !isNaN(n))
    this.nextId = nums.length > 0 ? Math.max(...nums) + 1 : 1
  }

  /** 识别说话人，返回 label 和 profile id */
  identify(embedding: Float32Array): { profileId: string; label: string; isNew: boolean } {
    let bestSim = -1
    let best: SpeakerProfile | null = null

    for (const p of this.profiles) {
      const sim = cosineSimilarity(embedding, p.embedding)
      if (sim > bestSim) {
        bestSim = sim
        best = p
      }
    }

    if (best && bestSim >= this.threshold) {
      // 增量更新 embedding（滑动平均）
      const alpha = 0.3
      for (let i = 0; i < best.embedding.length; i++) {
        best.embedding[i] = best.embedding[i] * (1 - alpha) + embedding[i] * alpha
      }
      best.sampleCount++
      return { profileId: best.id, label: best.label, isNew: false }
    }

    const id = `sp-${Date.now()}-${this.nextId}`
    const label = `说话人 ${this.nextId}`
    this.nextId++
    this.profiles.push({ id, label, embedding: new Float32Array(embedding), sampleCount: 1 })
    return { profileId: id, label, isNew: true }
  }

  /** 用户标注：将 profile 绑定到显示名 */
  labelProfile(profileId: string, displayName: string) {
    const p = this.profiles.find((x) => x.id === profileId)
    if (p) p.label = displayName
  }

  getProfiles(): SpeakerProfile[] {
    return [...this.profiles]
  }

  getProfile(profileId: string): SpeakerProfile | undefined {
    return this.profiles.find((p) => p.id === profileId)
  }
}
