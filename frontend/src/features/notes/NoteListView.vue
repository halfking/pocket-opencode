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
      <!--
        转写错误只在「录音结束后」才产生（runStop() 里的兜底转写失败才写
        recError），而 NoteRecordingStudio 只在 isRecording 为真时挂载。
        一点停止它就被卸载，错误随之从界面上消失 —— 用户只看到"点完没反应"，
        正是「语音没有转成文字」被报成卡死的原因。这里在停止后继续把错误
        单独显示出来，直到用户开始下一次录音（watch 里清空）。
      -->
      <p v-else-if="recError" class="studio-error" role="alert">{{ recordErrorText }}</p>
      <!--
        录音已停止、兜底转写仍在跑（最长 10 分钟）。这段窗口里 recording 已是
        false、error 还是空的，界面没有任何东西说明"正在做什么"，用户看到的就是
        「点了停止没反应」。见 note-recording.ts noteRecorderUiState 的注释。
      -->
      <p v-else-if="recorderUi.statusText" class="studio-busy" role="status" aria-live="polite">
        {{ recorderUi.statusText }}
        <!--
          兜底转写最长 10 分钟。此前这段窗口里全应用没有任何中止手段，
          用户只能干等或重启应用 —— 需求「后台执行的 api 可以强行终止」
          在这条链路上是空的。abort 会真传到服务端（ctx 派生自
          r.Context()），不是前端单方面撒手。
        -->
        <button
          v-if="recorderUi.canCancel"
          type="button"
          class="studio-busy-stop"
          @click="cancelTranscription"
        >停止转写</button>
      </p>
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

      <VoiceRecorderWidget :recording="isRecording" :busy="recorderUi.busy" @toggle="onMicToggle" />
      <NoteMetaSheet
        :open="metaOpen"
        :title="metaNote?.title"
        :content="metaNote?.content"
        :summary="metaNote?.summary"
        :summary-error="summarizeError"
        :summary-loading="summarizing"
        :domain="metaNote?.domain"
        :tags="metaNote?.tags"
        @close="onMetaClose"
        @save="onMetaSave"
        @delete="onMetaDelete"
      />
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'
import { Skeleton, EmptyState, DbLockedState, WaveformVisualizer } from '../../components'
import VoiceRecorderWidget from './VoiceRecorderWidget.vue'
import NoteRecordingStudio from './NoteRecordingStudio.vue'
import NoteMetaSheet from './NoteMetaSheet.vue'
import NoteSearchBrief from './NoteSearchBrief.vue'
import { notesApi } from '../../api/notes'
import { useNoteRecording } from './useNoteRecording'
import { noteRecorderUiState } from './note-recording'
import { searchNotesWithIntent, type NoteSearchBriefing } from './note-search'
import { useListSentinel } from '../../composables/use-list-sentinel'
import { DEFAULT_LIST_PAGE_SIZE, pageHasMore } from '../../native/list-sync/page'
import * as notesStore from './notes-store'
import type { LocalNote } from './notes-store'
import { useListScene } from '../../composables/use-list-scene'
import { useAuthStore } from '../../stores/auth'
import { useApiError } from '../../composables/useApiError'

defineOptions({ name: 'NoteListView' })

const router = useRouter()
const auth = useAuthStore()
const apiError = useApiError()
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
const summarizing = ref(false)
const summarizeError = ref('')
const {
  phase: recordPhase,
  recording: isRecording,
  transcript: liveTranscript,
  error: recError,
  toggle: toggleRecording,
  cancelTranscription,
  consumePendingResult,
} = useNoteRecording()

/** 收尾转写中的显式 UI 状态（见 note-recording.ts noteRecorderUiState）。 */
const recorderUi = computed(() => noteRecorderUiState(recordPhase.value))

/**
 * 录音停止后的转写错误文案。
 *
 * 2026-10-01 真机复现修正：这里**不能套 apiError**。
 *
 * recError 就是 runtime 的 `error`（见 useNoteRecording：`error: rt.error`），
 * runtime 在**写入时**已经调过 `sttFailureText()`，存进来的是面向用户的成品
 * 文案，`stt_unavailable:` 前缀已被剥掉。而 apiError 内部 `extractErrorCode()`
 * 取第一个冒号前的片段当错误码，前缀没了就取不到 → 落回 `errors.notConfigured`
 * 「该功能尚未完成配置」，把唯一可行动的信息整个盖掉。
 *
 * 真机证据（Redmi，笔记即时录音 23 秒后点停止）：
 *   录音中  显示「网关暂无可用的语音转写模型（…）；stt_unavailable: 外部语音
 *            转写服务未配置 API Key（设置 → 语音转写）」← 完整
 *   停止后  显示「该功能尚未完成配置」                        ← 信息被抹掉
 *
 * 同一个值、同一个页面，只是换了一条渲染路径就丢信息。NoteRecordingStudio
 * 早已按正确口径直出，这里是漏掉的姊妹路径。
 */
const recordErrorText = computed(() => recError.value || '')

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

/** 本地库按 workspace_id 分区。读写必须用当前登录 workspace，否则会写到/读到
 * 另一分区——表现为「保存成功但列表看不到」「删除没反应」。与 NoteEditView /
 * NoteDetailView 的同名函数保持一致。 */
function currentWorkspaceId(): string {
  return auth.workspaceId || 'default'
}

async function load() {
  loading.value = true
  dbNotReady.value = false
  try {
    const page = await notesStore.listNotes({
      limit: DEFAULT_LIST_PAGE_SIZE,
      offset: 0,
      domain: domain.value === 'all' ? undefined : domain.value,
      workspaceId: currentWorkspaceId(),
    })
    notes.value = page
    hasMore.value = pageHasMore(page.length)
    const drafts = await notesStore.listDraftNotes(currentWorkspaceId())
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
    const page = await notesStore.listNotes({
      limit: DEFAULT_LIST_PAGE_SIZE,
      offset: notes.value.length,
      domain: domain.value === 'all' ? undefined : domain.value,
      workspaceId: currentWorkspaceId(),
    })
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
    briefing.value = await searchNotesWithIntent(q, currentWorkspaceId())
    notes.value = briefing.value.results.map((r) => r.note)
  } finally {
    loading.value = false
  }
}

async function onMicToggle() {
  const stopped = await toggleRecording()
  if (!stopped) return
  await createVoiceDraft(stopped.text, stopped.audioBlob, stopped.durationMs)
}

/** 录音停止 → 语音草稿笔记 + 打开元信息编辑。页面在场与跨页停止(全局
 * 指示条/pendingResult 补建)共用同一条路径。
 * 即时总结:草稿落库后立即调 /api/notes/{id}/summarize,LLM 总结返回后
 * 写到 metaNote.summary,元信息编辑面板与列表预览同步生效。失败非阻塞,
 * 用户仍可正常进入元信息页(空 summary),只是顶部多出一行错误提示。 */
async function createVoiceDraft(text: string, audioBlob: Blob, durationMs: number) {
  metaNote.value = await notesStore.createNote({
    content: text || '（语音草稿）',
    contentType: 'voice',
    status: 'draft',
    createdByVoice: true,
    audioBlob,
    audioDurationMs: durationMs,
    workspaceId: currentWorkspaceId(),
  })
  metaOpen.value = true
  await load()
  if (!text || !text.trim()) return
  summarizing.value = true
  summarizeError.value = ''
  try {
    const { summary } = await notesApi.summarize(metaNote.value.id)
    if (summary && metaNote.value) {
      metaNote.value = { ...metaNote.value, summary }
      await notesStore.updateNote(metaNote.value.id, { summary }, currentWorkspaceId())
      await load()
    } else {
      // 第二种静默失败：api/notes.ts 的 summarize 注释写明「失败时返回空
      // summary，前端不阻塞流程」——也就是 200 + {summary:''}，**不抛异常**。
      // 原来的 `if (summary && ...)` 直接跳过，不设错误，于是用户看到的仍是
      // 「没有总结、也没有任何提示」。这里补上兜底文案。
      summarizeError.value = '未能生成 AI 总结（模型未返回内容），可稍后在笔记详情页重试'
    }
  } catch (e: unknown) {
    summarizeError.value = apiError(e, '总结失败，可稍后在笔记详情页重试')
  } finally {
    summarizing.value = false
  }
}

function resumeDraft() {
  if (!draftBanner.value) return
  metaNote.value = draftBanner.value
  metaOpen.value = true
}

async function onMetaSave(data: { title: string; domain: string; tags: string[] }) {
  if (!metaNote.value) return
  await notesStore.updateNote(metaNote.value.id, { ...data, status: 'saved' }, currentWorkspaceId())
  metaOpen.value = false
  metaNote.value = null
  await load()
}

async function onMetaDelete() {
  if (!metaNote.value) return
  await notesStore.deleteNote(metaNote.value.id, currentWorkspaceId())
  metaOpen.value = false
  metaNote.value = null
  await load()
}

function onMetaClose() {
  // 用户点关闭按钮但草稿仍存在:draft banner 不会再次展示(下次进页面才列草稿),
  // 这里清空 metaNote,让列表回到正常态。
  metaOpen.value = false
  // 不清掉 metaNote:metaNote 是当前临时语音笔记对象的引用,关闭 sheet 不应
  // 把它丢(后续用户点 banner 也能拿来当 resumeDraft 的种子)。
}

watch(domain, () => { void load() })
onMounted(async () => {
  await load()
  // 录音跨页存续(P0):录音在本页不在场时被停止(全局指示条),产物由
  // runtime 暂存;重进笔记页补建语音草稿,文本/音频不丢。
  const pending = consumePendingResult()
  if (pending && pending.text.trim()) {
    await createVoiceDraft(pending.text, pending.audioBlob, pending.durationMs)
  }
})
/* KeepAlive 现场保持：domain 筛选/搜索词保留；仅当笔记数据被详情页修改过才刷新 */
useListScene('notes', load)
</script>

<style scoped>
/* 录音停止后的转写失败提示。NoteRecordingStudio 里同名类是它 scoped 的，
   不会作用到本页，所以这里自带一份（保持视觉一致：danger 色 + 13px）。 */
.studio-error {
  margin: 0 0 var(--space-2);
  color: var(--danger);
  font-size: 13px;
}

/* 收尾转写中的状态行。刻意不用 danger 色：这不是错误，是进行中；
   也不该抢 NoteRecordingStudio 的位置（它此刻已被 v-if 卸载）。 */
.studio-busy {
  margin: 0 0 var(--space-2);
  color: var(--text-secondary);
  font-size: 13px;
  display: flex; align-items: center; gap: var(--space-2); flex-wrap: wrap;
}
/* 中止兜底转写。与状态同行而不是另起一行：这是一次性操作，
   用完就该消失，不该在界面上占一个常驻位置。 */
.studio-busy-stop {
  border: none; background: none; padding: 0;
  color: var(--brand-primary); font-size: var(--text-sm); font-weight: 600;
}
/* 这两个按钮经 HeaderActionsPortal teleport 到 AppLayout 的 .header-actions，
   scope 属性只挂在按钮自己身上，`:deep(.notes-action)` 编译成
   `[data-v-x] .notes-action`（要求祖先带 scope）→ 永不匹配，圆角描边一直没生效。
   必须写 scoped 自身选择器，编译成 `.notes-action[data-v-x]`。 */
.notes-action {
  display: inline-flex; align-items: center; justify-content: center;
  width: 40px; height: 40px; border-radius: 999px;
  background: transparent; border: 1px solid var(--border); color: var(--text-primary);
}
.context-row {
  display: flex; gap: 6px; padding: 8px var(--space-3);
  background: var(--bg-card); border-bottom: 1px solid var(--border); overflow-x: auto;
  /* 与 AIChatView 的同名容器保持一致：横向 chip 条不该常驻灰滚动条
     （真机 360dp 实测笔记页 chips 下方出现一条突兀灰线）。 */
  scrollbar-width: none;
  -ms-overflow-style: none;
}
.context-row::-webkit-scrollbar { display: none; }
.chip {
  padding: 5px 10px; border-radius: 999px; border: 1px solid var(--border);
  background: var(--bg-base); color: var(--text-secondary); font-size: var(--text-sm); flex-shrink: 0;
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
.note-title { font-weight: 600; font-size: var(--text-base); margin-bottom: 4px; }
.note-snippet {
  color: var(--text-secondary); font-size: var(--text-sm); line-height: 1.4;
  display: -webkit-box; -webkit-line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden;
}
.note-meta { display: flex; gap: 8px; margin-top: 8px; font-size: var(--text-xs); color: var(--text-muted); }
.time { margin-left: auto; }
.more { padding: 16px 0 24px; text-align: center; font-size: var(--text-sm); color: var(--text-muted); }
</style>
