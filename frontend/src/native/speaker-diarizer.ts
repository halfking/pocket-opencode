/**
 * Phase 2: 说话人分离实现
 * 基于 ECAPA-TDNN 192维向量的说话人聚类
 */

export interface SpeakerProfile {
  profileId: string
  label: string
  embedding: Float32Array
  color: string
  segmentCount: number
  customName?: string
}

export class SpeakerDiarizer {
  private profiles: Map<string, SpeakerProfile> = new Map()
  private speakerCounter = 0
  private readonly similarityThreshold = 0.85
  
  private readonly colors = [
    '#3B82F6', // blue
    '#EF4444', // red
    '#10B981', // green
    '#F59E0B', // amber
    '#8B5CF6', // purple
    '#EC4899', // pink
    '#14B8A6', // teal
    '#F97316', // orange
  ]

  /**
   * 识别说话人：返回已知说话人或创建新说话人
   */
  identify(embedding: Float32Array | null): { profileId: string; label: string } {
    if (!embedding || embedding.length === 0) {
      return this.getFallbackSpeaker()
    }

    // 与已知说话人比对
    for (const [profileId, profile] of this.profiles) {
      const similarity = this.cosineSimilarity(embedding, profile.embedding)
      if (similarity > this.similarityThreshold) {
        profile.segmentCount++
        return { profileId, label: profile.customName || profile.label }
      }
    }

    return this.createNewSpeaker(embedding)
  }

  private createNewSpeaker(embedding: Float32Array): { profileId: string; label: string } {
    this.speakerCounter++
    const profileId = `speaker-${Date.now()}-${this.speakerCounter}`
    const label = `说话人${this.speakerCounter}`
    const color = this.colors[(this.speakerCounter - 1) % this.colors.length]

    const profile: SpeakerProfile = {
      profileId,
      label,
      embedding,
      color,
      segmentCount: 1,
    }

    this.profiles.set(profileId, profile)
    return { profileId, label }
  }

  private getFallbackSpeaker(): { profileId: string; label: string } {
    const fallbackId = 'speaker-fallback'
    if (!this.profiles.has(fallbackId)) {
      this.profiles.set(fallbackId, {
        profileId: fallbackId,
        label: '未知说话人',
        embedding: new Float32Array(0),
        color: '#6B7280',
        segmentCount: 0,
      })
    }
    const profile = this.profiles.get(fallbackId)!
    profile.segmentCount++
    return { profileId: fallbackId, label: profile.label }
  }

  private cosineSimilarity(a: Float32Array, b: Float32Array): number {
    if (a.length !== b.length || a.length === 0) return 0

    let dotProduct = 0
    let normA = 0
    let normB = 0

    for (let i = 0; i < a.length; i++) {
      dotProduct += a[i] * b[i]
      normA += a[i] * a[i]
      normB += b[i] * b[i]
    }

    const denominator = Math.sqrt(normA) * Math.sqrt(normB)
    return denominator === 0 ? 0 : dotProduct / denominator
  }

  renameSpeaker(profileId: string, customName: string): boolean {
    const profile = this.profiles.get(profileId)
    if (!profile) return false
    profile.customName = customName
    return true
  }

  mergeSpeakers(sourceId: string, targetId: string): boolean {
    const source = this.profiles.get(sourceId)
    const target = this.profiles.get(targetId)
    if (!source || !target) return false

    target.segmentCount += source.segmentCount
    
    for (let i = 0; i < target.embedding.length; i++) {
      target.embedding[i] = (target.embedding[i] + source.embedding[i]) / 2
    }

    this.profiles.delete(sourceId)
    return true
  }

  getAllProfiles(): SpeakerProfile[] {
    return Array.from(this.profiles.values())
  }

  getSpeakerColor(profileId: string): string {
    return this.profiles.get(profileId)?.color || '#6B7280'
  }

  export(): string {
    const data = Array.from(this.profiles.entries()).map(([id, profile]) => ({
      profileId: id,
      label: profile.label,
      embedding: Array.from(profile.embedding),
      color: profile.color,
      segmentCount: profile.segmentCount,
      customName: profile.customName,
    }))
    return JSON.stringify(data)
  }

  import(data: string): void {
    try {
      const parsed = JSON.parse(data)
      this.profiles.clear()
      for (const item of parsed) {
        this.profiles.set(item.profileId, {
          ...item,
          embedding: new Float32Array(item.embedding),
        })
      }
      const maxCounter = Math.max(
        ...Array.from(this.profiles.values())
          .map(p => parseInt(p.label.replace('说话人', '')) || 0)
      )
      this.speakerCounter = maxCounter
    } catch (e) {
      console.error('Failed to import speaker profiles:', e)
    }
  }
}
