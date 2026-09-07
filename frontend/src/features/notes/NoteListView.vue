<template>
  <div class="notes-view">
    <HeaderActionsPortal>
      <WaveformVisualizer
        v-if="isRecording"
        :is-recording="true"
        :width="120"
        :height="28"
        color="var(--brand-primary)"
        :show-time="false"
        :show-progress="false"
      />
      <button class="notes-action" type="button" :aria-pressed="showSearch" @click="showSearch = !showSearch">
        <span class="material-symbols-outlined">{{ showSearch ? 'search_off' : 'search' }}</span>
      </button>
      <button class="notes-action" type="button" aria-label="新建笔记" @click="goCreate">
        <span class="material-symbols-outlined">add</span>
      </button>
    </HeaderActionsPortal>

    <DbLockedState v-if="dbNotReady" hint="笔记功能需要本地加密数据库" @relogin="router.push('/login')" />

    <template v-else>
      <button v-if="draftBanner" class="draft-banner" type="button" @click="resumeDraft">
        未完成草稿：{{ draftBanner.title || draftBanner.content.slice(0, 24) }}
      </button>

      <template v-if="isRecording">
        <NoteRecordingStudio v-model="liveTranscript" :error="recError" />
      </template>
      <template v-else>
        <div class="context-row">
          <button
            v-for="d in DOMAINS"
            :key="d.value"
            class="chip"
            :class="{ active: domain === d.value }"
            type="button"
            @click="domain = d.value"
          >{{ d.emoji }} {{ d.label }}</button>
        </div>
        <div v-if="showSearch" class="search-bar">
          <input v-model="query" placeholder="问笔记… 或搜关键词" @keyup.enter="onSearch" />
        </div>
        <NoteSearchBrief v-if="briefing" :summary="briefing.summary" :offline="briefing.offline" />
        <div v-if="loading" class="state"><Skeleton :count="3" /></div>
        <EmptyState
          v-else-if="filteredNotes.length === 0"
          icon="📝"
          :title="domain === 'all' ? '还没有笔记' : '该分类暂无笔记'"
          hint="点右下角麦克风开始语音录入"
          size="sm"
          variant="inline"
        />
        <div v-else class="note-list">
          <div
            v-for="n in filteredNotes"
            :key="n.id"
            class="note-card"
            :class="`domain-${n.domain || 'work'}`"
            @click="open(n.id)"
          >
            <div class="note-title">{{ n.title || (n.summary || n.content).slice(0, 24) }}</div>
            <div class="note-snippet">{{ n.summary || n.content }}</div>
            <div class="note-meta">
              <span v-if="n.createdByVoice" class="badge">🎙</span>
              <span>{{ domainText(n.domain) }}</span>
              <span class="time">{{ relTime(n.updatedAt) }}</span>
            </div>
          </div>
          <div v-if="notes.length > 0" ref="moreEl" class="more">
            <span v-if="loadingMore">加载中…</span>
            <span v-else-if="hasMore">上拉加载更多</span>
            <span v-else>没有更多了</span>
          </div>
        </div>
      </template>

      <VoiceRecorderWidget :recording="isRecording" @toggle="onMicToggle" />
      <NoteMetaSheet
        :open="metaOpen"
        :title="metaNote?.title"
        :content="metaNote?.content"
        :domain="metaNote?.domain"
        :tags="metaNote?.tags"
        @close="metaOpen = false"
        @save="onMetaSave"
        @delete="onMetaDelete"
      />
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'
import { Skeleton, EmptyState, DbLockedState, WaveformVisualizer } from '../../components'
import VoiceRecorderWidget from './VoiceRecorderWidget.vue'
import NoteRecordingStudio from './NoteRecordingStudio.vue'
import NoteMetaSheet from './NoteMetaSheet.vue'
import NoteSearchBrief from './NoteSearchBrief.vue'
import { useNoteRecording } from './useNoteRecording'
import { searchNotesWithIntent, type NoteSearchBriefing } from './note-search'
import { useListSentinel } from '../../composables/use-list-sentinel'
import { DEFAULT_LIST_PAGE_SIZE, pageHasMore } from '../../native/list-sync/page'
import * as notesStore from './notes-store'
import type { LocalNote } from './notes-store'

const router = useRouter()
const notes = ref<LocalNote[]>([])
const loading = ref(true)
const loadingMore = ref(false)
const hasMore = ref(false)
const query = ref('')
const dbNotReady = ref(false)
const showSearch = ref(false)
const briefing = ref<NoteSearchBriefing | null>(null)
const domain = ref<'all' | 'work' | 'study' | 'life' | 'idea'>('all')
const draftBanner = ref<LocalNote | null>(null)
const metaOpen = ref(false)
const metaNote = ref<LocalNote | null>(null)
const {
  recording: isRecording,
  transcript: liveTranscript,
  error: recError,
  toggle: toggleRecording,
} = useNoteRecording()

const DOMAINS = [
  { value: 'all', label: '全部', emoji: '🗂' },
  { value: 'work', label: '工作', emoji: '💼' },
  { value: 'study', label: '学习', emoji: '📚' },
  { value: 'life', label: '生活', emoji: '🌱' },
  { value: 'idea', label: '想法', emoji: '💡' },
] as const

const filteredNotes = computed(() =>
  domain.value === 'all' ? notes.value : notes.value.filter((n) => (n.domain || 'work') === domain.value),
)

function goCreate() { router.push('/notes/new') }
function open(id: string) { router.push(`/notes/${id}`) }
function domainText(d?: string | null) {
  return ({ work: '工作', study: '学习', life: '生活', idea: '想法' }[d || 'work'] || '工作')
}
function relTime(ms: number) {
  const min = Math.floor((Date.now() - ms) / 60000)
  if (min < 60) return `${min}分钟前`
  const hr = Math.floor(min / 60)
  if (hr < 24) return `${hr}小时前`
  return `${Math.floor(hr / 24)}天前`
}

async function load() {
  loading.value = true
  dbNotReady.value = false
  try {
    const page = await notesStore.listNotes({ limit: DEFAULT_LIST_PAGE_SIZE, offset: 0 })
    notes.value = page
    hasMore.value = pageHasMore(page.length)
    const drafts = await notesStore.listDraftNotes()
    draftBanner.value = drafts[0] ?? null
  } catch (e: unknown) {
    if (e instanceof Error && e.message.includes('LocalDB 未初始化')) dbNotReady.value = true
  } finally {
    loading.value = false
  }
}

async function loadMore() {
  if (loading.value || loadingMore.value || !hasMore.value || query.value.trim()) return
  loadingMore.value = true
  try {
    const page = await notesStore.listNotes({ limit: DEFAULT_LIST_PAGE_SIZE, offset: notes.value.length })
    const seen = new Set(notes.value.map((n) => n.id))
    notes.value = [...notes.value, ...page.filter((n) => !seen.has(n.id))]
    hasMore.value = pageHasMore(page.length)
  } finally {
    loadingMore.value = false
  }
}

const { moreEl } = useListSentinel(loadMore)

async function onSearch() {
  const q = query.value.trim()
  if (!q) { briefing.value = null; await load(); return }
  loading.value = true
  try {
    briefing.value = await searchNotesWithIntent(q)
    notes.value = briefing.value.results.map((r) => r.note)
  } finally {
    loading.value = false
  }
}

async function onMicToggle() {
  const stopped = await toggleRecording()
  if (!stopped) return
  metaNote.value = await notesStore.createNote({
    content: stopped.text || '（语音草稿）',
    contentType: 'voice',
    status: 'draft',
    createdByVoice: true,
    audioBlob: stopped.audioBlob,
    audioDurationMs: stopped.durationMs,
  })
  metaOpen.value = true
  await load()
}

function resumeDraft() {
  if (!draftBanner.value) return
  metaNote.value = draftBanner.value
  metaOpen.value = true
}

async function onMetaSave(data: { title: string; domain: string; tags: string[] }) {
  if (!metaNote.value) return
  await notesStore.updateNote(metaNote.value.id, { ...data, status: 'saved' })
  metaOpen.value = false
  metaNote.value = null
  await load()
}

async function onMetaDelete() {
  if (!metaNote.value) return
  await notesStore.deleteNote(metaNote.value.id)
  metaOpen.value = false
  metaNote.value = null
  await load()
}

onMounted(load)
</script>

<style scoped>
:deep(.notes-action) {
  display: inline-flex; align-items: center; justify-content: center;
  width: 40px; height: 40px; border-radius: 999px;
  background: transparent; border: 1px solid var(--border); color: var(--text-primary);
}
.context-row {
  display: flex; gap: 6px; padding: 8px var(--space-3);
  background: var(--bg-card); border-bottom: 1px solid var(--border); overflow-x: auto;
}
.chip {
  padding: 5px 10px; border-radius: 999px; border: 1px solid var(--border);
  background: var(--bg-base); color: var(--text-secondary); font-size: 12px; flex-shrink: 0;
}
.chip.active { background: var(--brand-bg); color: var(--text-primary); border-color: var(--brand-primary); }
.search-bar { padding: 8px var(--space-3); }
.search-bar input {
  width: 100%; padding: 10px 12px; border-radius: var(--radius-full);
  border: 1px solid var(--border); background: var(--bg-card); color: var(--text-primary);
}
.draft-banner {
  width: 100%; border: none; text-align: left; padding: 10px var(--space-3);
  background: var(--warning-bg, #fff6e5); color: var(--text-primary); font-size: 13px;
}
.state { text-align: center; color: var(--text-secondary); padding: var(--space-6); }
.note-list { display: flex; flex-direction: column; gap: 10px; padding: 0 var(--space-3) 96px; }
.note-card {
  background: var(--bg-card); border-radius: 8px; padding: 10px 12px;
  border: 1px solid var(--border); border-left: 3px solid var(--cat-work);
}
.note-card.domain-study { border-left-color: var(--cat-study); }
.note-card.domain-life { border-left-color: var(--cat-life); }
.note-card.domain-idea { border-left-color: var(--cat-idea); }
.note-title { font-weight: 600; font-size: 14px; margin-bottom: 4px; }
.note-snippet {
  color: var(--text-secondary); font-size: 12px; line-height: 1.4;
  display: -webkit-box; -webkit-line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden;
}
.note-meta { display: flex; gap: 8px; margin-top: 8px; font-size: 10px; color: var(--text-muted); }
.time { margin-left: auto; }
.more { padding: 16px 0 24px; text-align: center; font-size: 12px; color: var(--text-muted); }
</style>
