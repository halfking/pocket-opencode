<!-- S2.2 会议列表：最近会议 + 新建录音入口。 -->
<template>
      <div class="meetings-page">
      <header class="page-header">
        <div>
          <h1>会议记录</h1>
          <p>录音、转写和纪要都保存在本地</p>
        </div>
        <button class="primary" @click="router.push('/meetings/new')">开始会议</button>
      </header>

      <Loading v-if="loading" text="加载中…" />
      <ErrorState
        v-else-if="error && meetings.length === 0"
        title="会议记录加载失败"
        :message="error"
        @retry="load"
      />
      <EmptyState
        v-else-if="meetings.length === 0"
        icon="🎙️"
        title="还没有会议记录"
        message="录音、转写和纪要都保存在本地"
        hint="点击下方按钮开始第一场会议"
        action-label="开始第一场会议"
        @action="router.push('/meetings/new')"
      />
      <ul v-else class="meeting-list">
        <li
          v-for="meeting in meetings"
          :key="meeting.id"
          tabindex="0"
          role="link"
          :aria-label="`打开会议：${meeting.title || '未命名会议'}`"
          @click="open(meeting.id)"
          @keydown.enter.prevent="open(meeting.id)"
          @keydown.space.prevent="open(meeting.id)"
        >
          <div class="meeting-icon">🎙️</div>
          <div class="meeting-main">
            <strong>
              {{ meeting.title || '未命名会议' }}
              <span v-if="unfinishedIds.has(meeting.id)" class="badge-unfinished">未完成</span>
            </strong>
            <span>{{ formatDate(meeting.startedAt) }} · {{ duration(meeting.durationMs) }}</span>
            <small>{{ meeting.summary || meeting.transcript || '尚未生成纪要' }}</small>
          </div>
          <span class="arrow">›</span>
        </li>
      </ul>
    </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { findUnfinishedMeetings, listMeetings, type LocalMeeting } from './meetings-store'
import { EmptyState, ErrorState, Loading } from '../../components'

const router = useRouter()
const meetings = ref<LocalMeeting[]>([])
const unfinishedIds = ref(new Set<string>())
const loading = ref(true)
const error = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    meetings.value = await listMeetings()
    // 未完成（录音中断待恢复）的会议标徽标，进详情可继续转写或丢弃
    const unfinished = await findUnfinishedMeetings()
    unfinishedIds.value = new Set(unfinished.map((m) => m.id))
  } catch (e: any) {
    error.value = e?.message || '加载会议记录失败，请稍后重试。'
  } finally {
    loading.value = false
  }
}

function open(id: string) { router.push(`/meetings/${id}`) }
function formatDate(ts: number) {
  const d = new Date(ts)
  return `${d.getFullYear()}/${d.getMonth() + 1}/${d.getDate()} ${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
}
function duration(ms: number) {
  const seconds = Math.floor(ms / 1000)
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`
}

onMounted(load)
</script>

<style scoped>
.meetings-page { padding: 16px; padding-bottom: 96px; }
.page-header { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; margin-bottom: 18px; }
h1 { margin: 0; font-size: 24px; }
.page-header p { margin: 5px 0 0; color: var(--text-secondary); font-size: 12px; }
.primary { border: 0; border-radius: var(--radius-md); background: var(--brand-primary); color: var(--text-inverse); padding: 9px 12px; font-size: 13px; cursor: pointer; }
.meeting-list li:focus-visible { outline: none; box-shadow: 0 0 0 2px var(--brand-primary); }
.meeting-main small { display: -webkit-box; -webkit-line-clamp: 1; -webkit-box-orient: vertical; overflow: hidden; white-space: normal; }
.meeting-list { list-style: none; padding: 0; margin: 0; display: grid; gap: 8px; }
.meeting-list li { display: flex; align-items: center; gap: 11px; padding: 13px; background: var(--bg-card); border-radius: 12px; cursor: pointer; box-shadow: var(--shadow-sm); }
.meeting-icon { font-size: 25px; }
.meeting-main { min-width: 0; flex: 1; display: grid; gap: 3px; }
.meeting-main strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.badge-unfinished { display: inline-block; margin-left: 6px; padding: 1px 7px; border-radius: 999px; background: rgba(234, 88, 12, 0.12); color: #ea580c; font-size: 10px; font-weight: 600; vertical-align: middle; }
.meeting-main span { color: var(--text-secondary); font-size: 11px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.arrow { color: var(--text-muted); font-size: 24px; }
</style>
