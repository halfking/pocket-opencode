<!--
  NoteListView — voice-first notes list. Skeleton page; wires up to
  notesApi and a VoiceRecorderWidget FAB. Full editor/graph come later.
-->
<template>
  <div class="notes-view">
          <!-- 本地数据库未初始化提示 -->
      <div v-if="dbNotReady" class="state" style="padding: 40px 20px;">
        <p style="font-size: 48px; margin-bottom: 16px;">🔒</p>
        <p style="font-size: 16px; font-weight: 600; margin-bottom: 8px;">本地数据未解锁</p>
        <p style="font-size: 13px; color: var(--text-secondary); margin-bottom: 16px;">
          输入主密码以解锁本地加密存储
        </p>
        <input v-model="unlockPassword" type="password" placeholder="主密码" @keyup.enter="unlock" class="unlock-input" />
        <button class="btn-primary" :disabled="unlocking || !unlockPassword" @click="unlock">
          {{ unlocking ? '解锁中…' : '解锁' }}
        </button>
      </div>

      <template v-else>
        <div class="search-bar">
          <input v-model="query" placeholder="搜索笔记…" @keyup.enter="onSearch" />
          <select v-model="searchMode" @change="onSearch" class="search-mode">
            <option value="list">全部</option>
            <option value="fts">全文</option>
            <option value="semantic">语义</option>
            <option value="hybrid">混合</option>
          </select>
          <button class="btn-ghost import-button" :disabled="importing" @click="openImporter">
            {{ importing ? '导入中…' : '导入 ENEX' }}
          </button>
          <input ref="importInput" class="sr-only" type="file" accept=".enex,.xml,text/xml" @change="onImportFile" />
        </div>
        <p v-if="importMessage" class="import-message">{{ importMessage }}</p>

        <Loading v-if="loading" text="加载笔记中…" />
        <ErrorState
          v-else-if="loadError !== ''"
          title="笔记加载失败"
          :message="loadError"
          @retry="load"
        />
        <EmptyState
          v-else-if="notes.length === 0"
          icon="📝"
          title="还没有笔记"
          message="创建的第一条笔记会保存在本地加密存储中"
          hint="长按右下角麦克风开始语音录入，或在上方搜索已有内容"
        />

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
      </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '../../stores/auth'
import { EmptyState, ErrorState, Loading } from '../../components'
import VoiceRecorderWidget from './VoiceRecorderWidget.vue'
import * as notesStore from './notes-store'
import { importEnex, readImportFile } from '../imports/import-service'
import { initLobster } from '../../native/lobster-init'
import type { LocalNote, SearchResult } from './notes-store'

const router = useRouter()
const auth = useAuthStore()
const notes = ref<LocalNote[]>([])
const loading = ref(true)
const loadError = ref('')
const query = ref('')
const searchMode = ref<'list' | 'fts' | 'semantic' | 'hybrid'>('list')
const dbNotReady = ref(false)
const unlockPassword = ref('')
const unlocking = ref(false)
const importInput = ref<HTMLInputElement | null>(null)
const importing = ref(false)
const importMessage = ref('')

async function unlock() {
  if (!unlockPassword.value) return
  unlocking.value = true
  try {
    await initLobster(unlockPassword.value)
    unlockPassword.value = ''
    await load()
  } catch (e: any) {
    console.warn('[notes] 解锁失败:', e)
  } finally {
    unlocking.value = false
  }
}

function openImporter() {
  importInput.value?.click()
}

async function onImportFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  importing.value = true
  importMessage.value = ''
  try {
    const summary = await importEnex(await readImportFile(file), currentWorkspaceId())
    importMessage.value = `导入完成：${summary.imported} 条笔记，跳过 ${summary.skipped} 条重复记录，附件 ${summary.attachments} 个`
    await load()
  } catch (e: any) {
    importMessage.value = `导入失败：${e?.message || '文件格式不正确'}`
  } finally {
    importing.value = false
    input.value = ''
  }
}

async function load() {
  loading.value = true
  dbNotReady.value = false
  loadError.value = ''
  try {
    const results = await notesStore.listNotes({ limit: 100, workspaceId: currentWorkspaceId() })
    notes.value = results
  } catch (e: any) {
    // 本地数据库未初始化时，显示友好提示而非崩溃
    if (e?.message?.includes('LocalDB 未初始化')) {
      dbNotReady.value = true
      console.warn('[notes] 本地数据库未初始化，显示降级界面')
    } else {
      // 页面内错误 + 重试（08 §6），保留已有列表数据
      loadError.value = e?.message || '读取本地数据库失败，请重试'
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
        results = await notesStore.searchSemantic(query.value, 20, currentWorkspaceId())
        break
      case 'hybrid':
        results = await notesStore.searchHybrid(query.value, 20, currentWorkspaceId())
        break
      default:
        results = await notesStore.searchFullText(query.value, 20, currentWorkspaceId())
    }
    notes.value = results.map((r) => r.note)
  } finally {
    loading.value = false
  }
}

function currentWorkspaceId(): string {
  return auth.workspaceId || 'default'
}

function open(id: string) { router.push(`/notes/${id}`) }

async function onTranscribed(result: { text: string; audioPath: string; durationSec: number }) {
  // 创建本地笔记；嵌入异步发 pocketd /api/embed
  await notesStore.createNote({
    content: result.text,
    contentType: 'voice',
    audioPath: result.audioPath,
    audioDurationMs: Math.round(result.durationSec * 1000),
    workspaceId: currentWorkspaceId(),
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

onMounted(load)
</script>

<style scoped>
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
.import-button { flex: 0 0 auto; white-space: nowrap; }
.import-message { margin: 0 0 var(--space-2); color: var(--text-secondary); font-size: 12px; }
.sr-only { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border: 0; }
.unlock-input {
  display: block;
  width: min(100%, 280px);
  margin: 0 auto var(--space-2);
  padding: 9px 10px;
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-card);
  color: var(--text-primary);
}
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
