<!-- S2.2 会议详情：转写、纪要，以及沉淀到 PKM/Task。 -->
<template>
  <AppLayout>
    <div v-if="loading" class="state">加载中…</div>
    <div v-else-if="!meeting" class="state">会议不存在或已删除。</div>
    <div v-else class="detail-page">
      <header class="header">
        <div>
          <h1>{{ meeting.title || '未命名会议' }}</h1>
          <p>{{ formatDate(meeting.startedAt) }} · {{ duration(meeting.durationMs) }}</p>
        </div>
        <button class="ghost" @click="router.push('/meetings')">返回</button>
      </header>

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

      <p v-if="message" class="message">{{ message }}</p>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '../../app/AppLayout.vue'
import { api } from '../../api/client'
import { useToast } from '../../composables/useToast'
import { saveNote } from '../pkm/pkm-store'
import { summarizeMeeting } from './meetings-ai'
import { getMeetingWithSegments, updateSummary, type LocalMeeting, type MeetingSegment } from './meetings-store'

const route = useRoute()
const router = useRouter()
const toast = useToast()
const loading = ref(true)
const meeting = ref<LocalMeeting | null>(null)
const segments = ref<MeetingSegment[]>([])
const summarizing = ref(false)
const creatingTask = ref(false)
const message = ref('')

async function load() {
  loading.value = true
  try {
    const result = await getMeetingWithSegments(route.params.id as string)
    meeting.value = result?.meeting || null
    segments.value = result?.segments || []
  } finally {
    loading.value = false
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
    const note = await saveNote({
      title: `会议：${meeting.value.title || '未命名会议'}`,
      html: `<h2>会议纪要</h2><p>${escapeHtml(summary)}</p><h2>完整转写</h2><p>${escapeHtml(meeting.value.transcript)}</p>`,
      workspaceId: undefined,
    })
    toast.success('已保存到 PKM 笔记')
    router.push(`/pkm/n/${note.id}`)
  } catch (error: any) {
    toast.error(error?.message || '保存笔记失败')
  }
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

function escapeHtml(value: string) {
  return value.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}
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
.ghost, .secondary, .primary { border-radius: 8px; padding: 9px 11px; cursor: pointer; font-size: 12px; }
.ghost, .secondary { border: 1px solid var(--border); background: var(--bg-card); color: var(--text-primary); }
.primary { border: 0; background: var(--brand-primary); color: white; }
button:disabled { opacity: .55; cursor: not-allowed; }
.actions { display: flex; flex-wrap: wrap; gap: 8px; margin: 18px 0; }
.card { margin-top: 12px; padding: 14px; background: var(--bg-card); border-radius: 11px; box-shadow: var(--shadow-sm); }
h2 { margin: 0 0 9px; font-size: 16px; }
.transcript, .summary { white-space: pre-wrap; line-height: 1.65; font-size: 13px; margin: 0; }
.muted, .state { color: var(--text-secondary); }
.state { padding: 48px 16px; text-align: center; }
.segment { border-top: 1px solid var(--border); padding: 10px 0; display: grid; grid-template-columns: auto auto 1fr; gap: 8px; align-items: baseline; font-size: 12px; }
.segment p { grid-column: 1 / -1; margin: 0; line-height: 1.5; }
.speaker { color: var(--brand-primary); font-weight: 600; }
.message { color: var(--success); font-size: 12px; margin-top: 12px; }
</style>
