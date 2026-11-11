/**
 * Meeting Recording Integration with Live Subtitle (Cicada-inspired)
 * 
 * 职责：
 * - 将会议录音 runtime 的转写片段实时推送到字幕 store
 * - 处理说话人标签同步
 * - 管理字幕会话生命周期
 */
import { watch } from 'vue'
import { useLiveSubtitleStore } from '../../stores/liveSubtitle'
import { meetingRecorderRuntime } from '../../native/recordingRuntime'

/**
 * 初始化会议录音与实时字幕的集成
 * 
 * 在 App.vue 或 main.ts 中调用一次
 */
export function initLiveSubtitleIntegration() {
  const subtitleStore = useLiveSubtitleStore()

  // 监听会议录音开始
  watch(() => meetingRecorderRuntime.isRecording.value, (isRecording) => {
    if (isRecording) {
      const meetingId = meetingRecorderRuntime.activeMeetingId.value
      if (meetingId) {
        subtitleStore.startSession(meetingId)
      }
    } else {
      subtitleStore.endSession()
    }
  })

  // 监听转写片段更新
  watch(() => meetingRecorderRuntime.segments.value.length, () => {
    const segments = meetingRecorderRuntime.segments.value
    if (segments.length === 0) return

    // 找到最新的片段
    const latestSeg = segments[segments.length - 1]
    
    // 检查是否已添加到字幕（避免重复）
    const alreadyAdded = subtitleStore.segments.some((s: any) => 
      s.timestamp === latestSeg.startMs && s.text === latestSeg.text
    )
    
    if (!alreadyAdded && latestSeg.text.trim()) {
      subtitleStore.addSegment({
        meetingId: meetingRecorderRuntime.activeMeetingId.value || '',
        speaker: latestSeg.speakerLabel || null,
        text: latestSeg.text,
        timestamp: latestSeg.startMs,
        confidence: latestSeg.confidence,
        lang: latestSeg.lang,
      })
    }
  })

  // 监听说话人标签更新（用户手动编辑后同步到字幕）
  watch(() => meetingRecorderRuntime.speakers.value, (speakers) => {
    // 更新字幕中的说话人标签
    for (const seg of subtitleStore.segments) {
      const speaker = speakers.find((s: any) => s.profileId === seg.speaker)
      if (speaker) {
        subtitleStore.updateSegment(seg.id, { speaker: speaker.label })
      }
    }
  }, { deep: true })
}

/**
 * 手动显示/隐藏实时字幕
 */
export function toggleLiveSubtitle() {
  const store = useLiveSubtitleStore()
  store.visible = !store.visible
}

/**
 * 导出当前会议字幕
 */
export async function exportMeetingSubtitle(format: 'srt' | 'txt' = 'srt') {
  const store = useLiveSubtitleStore()
  const content = format === 'srt' ? store.exportSRT() : store.exportText()
  
  // 使用 pocket-native 分享功能
  const { getPocketNative } = await import('../../native/pocket-native')
  const native = getPocketNative()
  
  // 写入临时文件
  const filename = `subtitle-${Date.now()}.${format}`
  const blob = new Blob([content], { type: format === 'srt' ? 'text/plain' : 'text/plain' })
  const base64 = await blobToBase64(blob)
  
  await native.filesystem.writeFile(filename, base64, 'cache')
  const { uri } = await native.filesystem.getUri(filename, 'cache')
  
  // 分享
  await native.share.share({
    title: '会议字幕',
    text: `共 ${store.segments.length} 条字幕`,
    url: uri,
    dialogTitle: '导出字幕',
  })
}

async function blobToBase64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onloadend = () => {
      const result = reader.result as string
      // 移除 data:*;base64, 前缀
      const base64 = result.split(',')[1]
      resolve(base64)
    }
    reader.onerror = reject
    reader.readAsDataURL(blob)
  })
}
