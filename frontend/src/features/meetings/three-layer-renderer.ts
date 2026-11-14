/**
 * Phase 3: 三层UI架构实现
 * 
 * Layer 1: 实时流（Interim）- 灰字，500ms刷新
 * Layer 2: 分段确认（Partial）- 黑字，可编辑
 * Layer 3: 全量精校（Final）- 黑色加粗，带标点
 */

export interface TranscriptLayer {
  layer: 1 | 2 | 3
  segments: TranscriptSegment[]
  lastUpdate: number
}

export interface TranscriptSegment {
  id: string
  text: string
  confidence: number
  speaker?: {
    profileId: string
    label: string
    color: string
  }
  startMs: number
  endMs: number
  layer: 1 | 2 | 3
  editable: boolean
}

/**
 * 三层渲染管理器
 */
export class ThreeLayerRenderer {
  private layer1: TranscriptSegment[] = []  // 实时流
  private layer2: TranscriptSegment[] = []  // 分段确认
  private layer3: TranscriptSegment[] = []  // 全量精校
  
  private readonly LAYER1_THROTTLE = 500  // 500ms刷新一次
  private layer1LastUpdate = 0

  /**
   * 添加实时流片段（Layer 1）
   */
  addInterim(segment: Omit<TranscriptSegment, 'layer' | 'editable'>): void {
    const now = Date.now()
    if (now - this.layer1LastUpdate < this.LAYER1_THROTTLE) {
      return  // 节流
    }

    this.layer1 = [{
      ...segment,
      layer: 1,
      editable: false,
    }]
    this.layer1LastUpdate = now
  }

  /**
   * 确认为Layer 2（分段确认）
   */
  confirmPartial(segment: Omit<TranscriptSegment, 'layer' | 'editable'>): void {
    // 清空Layer 1
    this.layer1 = []
    
    // 添加到Layer 2
    this.layer2.push({
      ...segment,
      layer: 2,
      editable: true,
    })
  }

  /**
   * 升级到Layer 3（全量精校）
   */
  upgradeToFinal(segments: Omit<TranscriptSegment, 'layer' | 'editable'>[]): void {
    // 清空Layer 1和2
    this.layer1 = []
    this.layer2 = []
    
    // 设置Layer 3
    this.layer3 = segments.map(seg => ({
      ...seg,
      layer: 3,
      editable: true,
    }))
  }

  /**
   * 获取当前渲染内容（合并三层）
   */
  render(): TranscriptSegment[] {
    return [...this.layer3, ...this.layer2, ...this.layer1]
  }

  /**
   * 编辑某个段落
   */
  editSegment(segmentId: string, newText: string): boolean {
    // 只能编辑Layer 2和3
    const segment = [...this.layer2, ...this.layer3].find(s => s.id === segmentId)
    if (!segment || !segment.editable) return false
    
    segment.text = newText
    return true
  }

  /**
   * 获取完整文本
   */
  getFullText(): string {
    return this.render()
      .map(seg => seg.text)
      .join('')
  }

  /**
   * 清空所有层
   */
  clear(): void {
    this.layer1 = []
    this.layer2 = []
    this.layer3 = []
  }
}

/**
 * 智能滚动策略
 */
export class SmartScroller {
  private userScrolling = false
  private lastScrollTop = 0
  private scrollThreshold = 100  // 用户滚动超过100px视为主动滚动

  /**
   * 检测用户是否主动滚动
   */
  checkUserScroll(scrollTop: number, maxScroll: number): void {
    const delta = Math.abs(scrollTop - this.lastScrollTop)
    const nearBottom = maxScroll - scrollTop < this.scrollThreshold

    if (delta > 10 && !nearBottom) {
      this.userScrolling = true
    } else if (nearBottom) {
      this.userScrolling = false
    }

    this.lastScrollTop = scrollTop
  }

  /**
   * 是否应该自动滚动
   */
  shouldAutoScroll(): boolean {
    return !this.userScrolling
  }

  /**
   * 重置为自动跟随
   */
  resetToAuto(): void {
    this.userScrolling = false
  }
}
