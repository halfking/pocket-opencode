<!--
  NoteListView — voice-first notes list. Skeleton page; wires up to
  notesApi and a VoiceRecorderWidget FAB. Full editor/graph come later.
-->
<template>
  <div class="notes-view">
    <!-- App.vue 已全局包 AppLayout（壳层=唯一顶栏/底导航），此处不再嵌套，
         否则出现双顶栏 + 两个 #app-header-actions（HeaderActionsPortal 只会命中文档序第一个）。 -->
    <HeaderActionsPortal>
      <button
        class="notes-action"
        type="button"
        :aria-label="'搜索'"
        :aria-pressed="showSearch"
        @click="showSearch = !showSearch"
      >
        <span class="material-symbols-outlined" aria-hidden="true">{{ showSearch ? 'search_off' : 'search' }}</span>
      </button>
      <button class="notes-action" type="button" aria-label="新建笔记" @click="goCreate">
        <span class="material-symbols-outlined" aria-hidden="true">add</span>
      </button>
    </HeaderActionsPortal>

    <DbLockedState
      v-if="dbNotReady"
      hint="笔记功能需要本地加密数据库"
      @relogin="goToLogin"
    />

    <template v-else>
      <!-- "第二层"：领域筛选（与对话页 context-row 等价；DB 未解锁时不渲染） -->
      <div class="context-row">
        <button
          v-for="d in DOMAINS"
          :key="d.value"
          class="chip"
          :class="[`domain-${d.value}`, { active: domain === d.value }]"
          type="button"
          @click="setDomain(d.value)"
        >
          <span class="chip-icon" aria-hidden="true">{{ d.emoji }}</span>
          <span class="chip-label">{{ d.label }}</span>
        </button>
      </div>

      <div v-if="showSearch" class="search-bar">
        <input v-model="query" placeholder="搜索笔记…" @keyup.enter="onSearch" />
        <select v-model="searchMode" @change="onSearch" class="search-mode">
          <option value="list">全部</option>
          <option value="fts">全文</option>
          <option value="semantic">语义</option>
          <option value="hybrid">混合</option>
        </select>
      </div>

      <div v-if="loading" class="state"><Skeleton :count="3" /></div>
      <EmptyState
        v-else-if="filteredNotes.length === 0"
        icon="📝"
        :title="domain === 'all' ? '还没有笔记' : '该分类暂无笔记'"
        :hint="domain === 'all' ? '长按右下角麦克风开始语音录入' : '试试切换到其他分类'"
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
    </template>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'
import VoiceRecorderWidget from './VoiceRecorderWidget.vue'
import { Skeleton, EmptyState, DbLockedState } from '../../components'
import * as notesStore from './notes-store'
import type { LocalNote, SearchResult } from './notes-store'

const router = useRouter()
const notes = ref<LocalNote[]>([])
const loading = ref(true)
const query = ref('')
const searchMode = ref<'list' | 'fts' | 'semantic' | 'hybrid'>('list')
const dbNotReady = ref(false)
const showSearch = ref(false)
type Domain = 'all' | 'work' | 'study' | 'life' | 'idea'
const domain = ref<Domain>('all')

interface DomainMeta { value: Domain; label: string; emoji: string }
const DOMAINS: DomainMeta[] = [
  { value: 'all',   label: '全部', emoji: '🗂' },
  { value: 'work',  label: '工作', emoji: '💼' },
  { value: 'study', label: '学习', emoji: '📚' },
  { value: 'life',  label: '生活', emoji: '🌱' },
  { value: 'idea',  label: '想法', emoji: '💡' },
]

function goToLogin() {
  router.push('/login')
}

function goCreate() {
  router.push('/notes/new')
}

function setDomain(d: Domain) {
  domain.value = d
}

async function load() {
  loading.value = true
  dbNotReady.value = false
  try {
    const results = await notesStore.listNotes({ limit: 100 })
    notes.value = results
  } catch (e: any) {
    // 本地数据库未初始化时，显示友好提示而非崩溃
    if (e?.message?.includes('LocalDB 未初始化')) {
      dbNotReady.value = true
      console.warn('[notes] 本地数据库未初始化，显示降级界面')
    } else {
      console.error('[notes] 加载失败:', e)
    }
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

/** 当前领域过滤后的笔记列表（"全部"不过滤） */
const filteredNotes = computed(() => {
  if (domain.value === 'all') return notes.value
  return notes.value.filter((n) => (n.domain || 'work') === domain.value)
})

onMounted(load)
</script>

<style scoped>
/* 顶栏"搜索 / 新建"按钮（注入 AppLayout header-actions） */
:deep(.notes-action) {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 40px;
  height: 40px;
  border-radius: 999px;
  background: transparent;
  border: 1px solid var(--border);
  color: var(--text-primary);
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-out);
}
:deep(.notes-action[aria-pressed='true']) {
  background: var(--brand-bg);
  color: var(--brand-primary);
  border-color: var(--brand-primary);
}
:deep(.notes-action:active) { background: var(--bg-subtle); }
:deep(.notes-action .material-symbols-outlined) { font-size: 20px; }

/* "第二层"领域筛选 chip 行（与对话页 context-row 等价） */
.context-row {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px var(--space-3);
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
  overflow-x: auto;
  scrollbar-width: none;
}
.context-row::-webkit-scrollbar { display: none; }

.chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 5px 10px;
  border-radius: 999px;
  border: 1px solid var(--border);
  background: var(--bg-base);
  color: var(--text-secondary);
  font-size: 12px;
  font-weight: var(--font-weight-medium);
  cursor: pointer;
  flex-shrink: 0;
  white-space: nowrap;
  transition: background var(--duration-fast) var(--ease-out);
}
.chip:active { background: var(--bg-subtle); }
.chip.active {
  color: var(--text-primary);
  background: var(--brand-bg);
  border-color: var(--brand-primary);
}
.chip-icon { font-size: 14px; line-height: 1; }
.chip-label { white-space: nowrap; }

.search-bar input {
  width: 100%;
  padding: var(--space-2-5) var(--space-3); /* 修改：10px 12px（原 12px 16px） */
  border-radius: var(--radius-full);
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text-primary);
  font-size: 14px;
  margin-bottom: var(--space-2-5);          /* 修改：10px（原 12px） */
}
.state { text-align: center; color: var(--text-secondary); padding: var(--space-6); }
.state .hint { font-size: 12px; color: var(--text-muted); margin-top: var(--space-2); }
.note-list { display: flex; flex-direction: column; gap: var(--space-2-5); } /* 修改：10px（原 12px） */
.note-card {
  background: var(--bg-card);
  border-radius: var(--radius-md);          /* 修改：8px（原 12px） */
  padding: var(--space-2-5) var(--space-3); /* 修改：10px 12px（原 12px 16px） */
  border: 1px solid var(--border);          /* 新增：边框 */
  cursor: pointer;
  border-left: 3px solid var(--cat-work);
}
.note-card.domain-study { border-left-color: var(--cat-study); }
.note-card.domain-life { border-left-color: var(--cat-life); }
.note-card.domain-idea { border-left-color: var(--cat-idea); }
.note-title {
  font-weight: 600;
  font-size: 14px;                          /* 修改：14px（原 15px） */
  margin-bottom: var(--space-1);
  color: var(--text-primary);               /* 新增：高对比度 */
}
.note-snippet {
  color: var(--text-secondary);
  font-size: 12px;                          /* 修改：12px（原 13px） */
  line-height: 1.4;                         /* 新增：更紧凑行高 */
  display: -webkit-box;
  -webkit-line-clamp: 3;                    /* 修改：3 行（原 2 行） */
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.note-meta {
  display: flex;
  gap: var(--space-2);
  align-items: center;
  margin-top: var(--space-2);
  font-size: 10px;                          /* 修改：10px（原 11px） */
  color: var(--text-muted);
}
.badge.voice { font-size: 11px; }          /* 修改：11px（原 12px） */
.domain-tag {
  background: var(--bg-subtle);
  padding: 2px 6px;                         /* 修改：2px 垂直（原 1px） */
  border-radius: var(--radius-sm);
  font-size: 10px;                          /* 新增：明确字号 */
  font-weight: 500;                         /* 新增：加粗 */
}
.time { margin-left: auto; }
</style>
