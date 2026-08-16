<!-- S2.2 会议详情：转写、纪要，以及沉淀到 PKM/Task。 -->
<template>
      <div v-if="loading" class="state" role="status">加载中…</div>
    <ErrorState
      v-else-if="loadError"
      title="会议详情加载失败"
      :message="loadError"
      @retry="load"
    />
    <div v-else-if="!meeting" class="state">会议不存在或已删除。</div>
    <div v-else class="detail-page">
      <header class="header">
        <div>
          <h1>{{ meeting.title || '未命名会议' }}</h1>
          <p>{{ formatDate(meeting.startedAt) }} · {{ duration(meeting.durationMs) }}</p>
        </div>
        <button type="button" class="ghost" @click="router.push('/meetings')">返回</button>
      </header>

      <!-- E5-S2 恢复入口：录音中断但分片已落盘 → 可继续转写或丢弃 -->
      <section v-if="recoverable" class="card recovery-card">
        <h2>录音未完成处理</h2>
        <p class="muted">上次录音中断，已保留约 {{ duration(meeting.durationMs) }} 的音频。</p>
        <div class="actions" style="margin: 10px 0 0">
          <button class="primary" :disabled="recovering" @click="recoverTranscribe">
            {{ recovering ? '转写中…' : '继续转写' }}
          </button>
          <button class="secondary" :disabled="recovering" @click="discardRecording">丢弃录音</button>
        </div>
      </section>

      <section class="actions">
        <button class="primary" :disabled="summarizing || !meeting.transcript" @click="makeSummary">
          {{ summarizing ? '生成中…' : meeting.summary ? '重新生成纪要' : '生成会议纪要' }}
        </button>
        <button class="secondary" :disabled="!meeting.transcript" @click="saveAsNote">保存为笔记</button>
        <button class="secondary" :disabled="!meeting.summary || creatingTask" @click="createTask">
          {{ creatingTask ? '创建中…' : '纪要转任务' }}
        </button>
      </section>

      <section class="card">
        <h2>会议转写</h2>
        <p v-if="meeting.transcript" class="transcript">{{ meeting.transcript }}</p>
        <p v-else class="muted">暂无转写内容</p>
      </section>

      <section class="card" v-if="meeting.summary">
        <h2>AI 会议纪要</h2>
        <div class="summary">{{ meeting.summary }}</div>
      </section>

      <section class="card" v-if="segments.length">
        <h2>说话分段</h2>
        <div v-for="segment in segments" :key="segment.id" class="segment">
          <span class="speaker">{{ segment.speakerLabel || '说话人' }}</span>
          <span>{{ formatSegmentTime(segment.startMs) }}</span>
          <p>{{ segment.text }}</p>
        </div>
      </section>

      <p v-if="message" class="message" role="status" aria-live="polite">{{ message }}</p>
    </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../../api/client'
import { useToast } from '../../composables/useToast'
import { ErrorState } from '../../components'
import { saveNote } from '../pkm/pkm-store'
import { renderMarkdown, renderPlainText, sanitizeHtml } from '../../utils/markdown'
import { summarizeMeeting } from './meetings-ai'
import { sttApi } from '../../api/stt'
import {
  countAudioParts, deleteAudioParts, discardMeeting, getMeetingWithSegments,
  listAudioParts, updateSummary, updateTranscript,
  type LocalMeeting, type MeetingSegment,
} from './meetings-store'
import { classifyRecovery, partsToBlob } from './recorderTiming'

const route = useRoute()
const router = useRouter()
const toast = useToast()
const loading = ref(true)
const meeting = ref<LocalMeeting | null>(null)
const segments = ref<MeetingSegment[]>([])
const summarizing = ref(false)
const creatingTask = ref(false)
const message = ref('')
const loadError = ref('')
const recoverable = ref(false)
const recovering = ref(false)

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const id = route.params.id as string
    const result = await getMeetingWithSegments(id)
    meeting.value = result?.meeting || null
    segments.value = result?.segments || []
    recoverable.value = false
    if (meeting.value) {
      const partCount = await countAudioParts(id)
      recoverable.value =
        classifyRecovery({
          deletedAt: meeting.value.deletedAt,
          transcript: meeting.value.transcript,
          partCount,
        }) === 'recoverable'
    }
  } catch (e: any) {
    loadError.value = e?.message || '加载会议详情失败，请稍后重试。'
  } finally {
    loading.value = false
  }
}

/** 恢复中断的录音：分片 → Blob → 云转写 → 回填转写并清理分片。 */
async function recoverTranscribe() {
  if (!meeting.value || recovering.value) return
  recovering.value = true
  message.value = ''
  try {
    const parts = await listAudioParts(meeting.value.id)
    const blob = partsToBlob(parts.map((p) => ({ seq: p.seq, mimeType: p.mimeType, dataBase64: p.dataBase64 })))
    if (!blob) throw new Error('没有可恢复的音频分片。')
    const result = await sttApi.transcribe({ audioBlob: blob, forceEngine: 'cloud' })
    await updateTranscript(meeting.value.id, result.text)
    // durationMs 已随分片落盘时推进，无需再写
    await deleteAudioParts(meeting.value.id)
    recoverable.value = false
    await load()
    message.value = '已从中断处恢复转写。'
  } catch (error: any) {
    toast.error(error?.message || '恢复转写失败，音频分片已保留。')
  } finally {
    recovering.value = false
  }
}

/** 丢弃中断的录音：会议行 + 分片一起删除，回到列表。 */
async function discardRecording() {
  if (!meeting.value || recovering.value) return
  try {
    await discardMeeting(meeting.value.id)
    toast.success('已丢弃未完成的录音')
    router.replace('/meetings')
  } catch (error: any) {
    toast.error(error?.message || '丢弃失败')
  }
}

async function makeSummary() {
  if (!meeting.value?.transcript || summarizing.value) return
  summarizing.value = true
  message.value = ''
  try {
    const summary = await summarizeMeeting(meeting.value.transcript)
    await updateSummary(meeting.value.id, summary)
    meeting.value.summary = summary
    message.value = '纪要已保存到本地。'
  } catch (error: any) {
    message.value = error?.message || '纪要生成失败。'
  } finally {
    summarizing.value = false
  }
}

async function saveAsNote() {
  if (!meeting.value?.transcript) return
  try {
    const summary = meeting.value.summary || meeting.value.transcript.slice(0, 500)
    // AI 纪要按 Markdown 渲染，转写按纯文本保留换行；外层静态结构
    // 通过共享的 DOMPurify 配置做防御性净化（PKM 笔记 TipTap 接受 HTML）。
    const note = await saveNote({
      title: `会议：${meeting.value.title || '未命名会议'}`,
      html: buildMeetingNoteHtml(summary, meeting.value.transcript),
      workspaceId: undefined,
    })
    toast.success('已保存到 PKM 笔记')
    router.push(`/pkm/n/${note.id}`)
  } catch (error: any) {
    toast.error(error?.message || '保存笔记失败')
  }
}

/**
 * Assemble the meeting note HTML and run the result through DOMPurify.
 * Replaces the earlier local escapeHtml() helper — see docs/ui-ux-overhaul
 * remaining work item #4. The PKM note renderer now receives sanitized
 * HTML/Markdown via renderMarkdown / renderPlainText + sanitizeHtml.
 */
function buildMeetingNoteHtml(summary: string, transcript: string): string {
  const summaryHtml = summary ? renderMarkdown(summary) : '<p><em>暂无纪要</em></p>'
  const transcriptHtml = renderPlainText(transcript)
  const raw = `<h2>会议纪要</h2>${summaryHtml}<h2>完整转写</h2>${transcriptHtml}`
  return sanitizeHtml(raw)
}

async function createTask() {
  if (!meeting.value?.summary || creatingTask.value) return
  creatingTask.value = true
  try {
    const task = await api.createTask({
      title: `跟进会议：${meeting.value.title || '未命名会议'}`,
      description: meeting.value.summary,
      source: 'local',
      status: 'active',
      priority: 'medium',
    })
    toast.success('已从纪要创建任务')
    router.push(`/tasks/${task.id}`)
  } catch (error: any) {
    toast.error(error?.message || '创建任务失败')
  } finally {
    creatingTask.value = false
  }
}

// TODO: replaced by renderMarkdown / renderPlainText + sanitizeHtml in
// utils/markdown.ts. The local escapeHtml() is no longer needed.
function formatDate(ts: number) {
  const d = new Date(ts)
  return `${d.getFullYear()}/${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}
function duration(ms: number) {
  const seconds = Math.floor(ms / 1000)
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`
}
function formatSegmentTime(ms: number) {
  const seconds = Math.floor(ms / 1000)
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`
}

onMounted(load)
</script>

<style scoped>
.detail-page { padding: 18px 16px 100px; }
.header { display: flex; justify-content: space-between; gap: 12px; align-items: flex-start; }
h1 { margin: 0; font-size: 23px; }
.header p { margin: 5px 0 0; color: var(--text-secondary); font-size: 12px; }
.ghost, .secondary, .primary { border-radius: var(--radius-md); padding: 9px 11px; cursor: pointer; font-size: 12px; }
.ghost, .secondary { border: 1px solid var(--border); background: var(--bg-card); color: var(--text-primary); }
.primary { border: 0; background: var(--brand-primary); color: var(--text-inverse); }
button:disabled { opacity: .55; cursor: not-allowed; }
.actions { display: flex; flex-wrap: wrap; gap: 8px; margin: 18px 0; }
.card { margin-top: 12px; padding: 14px; background: var(--bg-card); border-radius: var(--radius-lg); box-shadow: var(--shadow-sm); }
.recovery-card { border: 1px solid rgba(234, 88, 12, 0.4); }
h2 { margin: 0 0 9px; font-size: 16px; }
.transcript, .summary { white-space: pre-wrap; line-height: 1.65; font-size: 13px; margin: 0; }
.muted, .state { color: var(--text-secondary); }
.state { padding: 48px 16px; text-align: center; }
.segment { border-top: 1px solid var(--border); padding: 10px 0; display: grid; grid-template-columns: auto auto 1fr; gap: 8px; align-items: baseline; font-size: 12px; }
.segment p { grid-column: 1 / -1; margin: 0; line-height: 1.5; }
.speaker { color: var(--brand-primary); font-weight: 600; }
.message { color: var(--success); font-size: 12px; margin-top: 12px; }
</style>
