<!--
  NoteListView — voice-first notes list. Skeleton page; wires up to
  notesApi and a VoiceRecorderWidget FAB. Full editor/graph come later.
-->
<template>
  <div class="notes-view">
    <AppLayout>
      <div class="search-bar">
        <input v-model="query" placeholder="搜索笔记…" @keyup.enter="onSearch" />
        <select v-model="searchMode" @change="onSearch" class="search-mode">
          <option value="list">全部</option>
          <option value="fts">全文</option>
          <option value="semantic">语义</option>
          <option value="hybrid">混合</option>
        </select>
      </div>

      <div v-if="loading" class="state">加载中…</div>
      <div v-else-if="notes.length === 0" class="state">
        <p>还没有笔记</p>
        <p class="hint">长按右下角麦克风开始语音录入</p>
      </div>

      <div v-else class="note-list">
        <div
          v-for="n in notes"
          :key="n.id"
          class="note-card"
          :class="`domain-${n.domain || 'work'}`"
          @click="open(n.id)"
        >
          <div class="note-title">{{ n.title || n.content.slice(0, 24) }}</div>
          <div class="note-snippet">{{ n.content }}</div>
          <div class="note-meta">
            <span v-if="n.createdByVoice" class="badge voice">🎙</span>
            <span class="domain-tag">{{ domainText(n.domain) }}</span>
            <span class="time">{{ relTime(n.updatedAt) }}</span>
          </div>
        </div>
      </div>

      <VoiceRecorderWidget @transcribed="onTranscribed" />
    </AppLayout>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import AppLayout from '../../app/AppLayout.vue'
import VoiceRecorderWidget from './VoiceRecorderWidget.vue'
import * as notesStore from './notes-store'
import type { LocalNote, SearchResult } from './notes-store'

const router = useRouter()
const notes = ref<LocalNote[]>([])
const loading = ref(true)
const query = ref('')
const searchMode = ref<'list' | 'fts' | 'semantic' | 'hybrid'>('list')

async function load() {
  loading.value = true
  try {
    const results = await notesStore.listNotes({ limit: 100 })
    notes.value = results
  } finally {
    loading.value = false
  }
}

async function onSearch() {
  if (!query.value.trim()) {
    searchMode.value = 'list'
    await load()
    return
  }
  loading.value = true
  try {
    let results: SearchResult[]
    switch (searchMode.value) {
      case 'semantic':
        results = await notesStore.searchSemantic(query.value, 20)
        break
      case 'hybrid':
        results = await notesStore.searchHybrid(query.value, 20)
        break
      default:
        results = await notesStore.searchFullText(query.value, 20)
    }
    notes.value = results.map((r) => r.note)
  } finally {
    loading.value = false
  }
}

function open(id: string) { router.push(`/notes/${id}`) }

async function onTranscribed(result: { text: string; audioPath: string; durationSec: number }) {
  // 创建本地笔记；嵌入异步发 pocketd /api/embed
  await notesStore.createNote({
    content: result.text,
    contentType: 'voice',
    audioPath: result.audioPath,
    audioDurationMs: Math.round(result.durationSec * 1000),
  })
  await load()
}

const domainText = (d?: string | null) =>
  ({ work: '工作', study: '学习', life: '生活', idea: '想法' }[d || 'work'] || '工作')

function relTime(ms: number) {
  const diff = Date.now() - ms
  const min = Math.floor(diff / 60000)
  if (min < 60) return `${min}分钟前`
  const hr = Math.floor(min / 60)
  if (hr < 24) return `${hr}小时前`
  return `${Math.floor(hr / 24)}天前`
}

const searchLabel = computed(() =>
  ({ list: '全部', fts: '全文', semantic: '语义', hybrid: '混合' }[searchMode.value]),
)

import { computed } from 'vue'
onMounted(load)
</script>

<style scoped>
.search-bar input {
  width: 100%;
  padding: var(--space-3) var(--space-4);
  border-radius: var(--radius-full);
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text-primary);
  font-size: 14px;
  margin-bottom: var(--space-3);
}
.state { text-align: center; color: var(--text-secondary); padding: var(--space-6); }
.state .hint { font-size: 12px; color: var(--text-muted); margin-top: var(--space-2); }
.note-list { display: flex; flex-direction: column; gap: var(--space-3); }
.note-card {
  background: var(--bg-card);
  border-radius: var(--radius-md);
  padding: var(--space-3) var(--space-4);
  box-shadow: var(--shadow-sm);
  cursor: pointer;
  border-left: 3px solid var(--cat-work);
}
.note-card.domain-study { border-left-color: var(--cat-study); }
.note-card.domain-life { border-left-color: var(--cat-life); }
.note-card.domain-idea { border-left-color: var(--cat-idea); }
.note-title { font-weight: 600; font-size: 15px; margin-bottom: var(--space-1); }
.note-snippet {
  color: var(--text-secondary);
  font-size: 13px;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.note-meta { display: flex; gap: var(--space-2); align-items: center; margin-top: var(--space-2); font-size: 11px; color: var(--text-muted); }
.badge.voice { font-size: 12px; }
.domain-tag { background: var(--bg-subtle); padding: 1px 6px; border-radius: var(--radius-sm); }
.time { margin-left: auto; }
</style>
