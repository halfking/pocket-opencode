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
      <button class="notes-action" type="button" :aria-pressed="showSearch" :aria-label="showSearch ? '关闭搜索' : '搜索笔记'" @click="showSearch = !showSearch">
        <!-- 原来这个图标 span 没有 aria-hidden：图标连字（"search"/"search_off"）
             就成了这个 ToggleButton 的可访问名，读屏会念英文图标名。
             aria-label 又会因为 aria-pressed 被 Android 桥接丢掉（同 TasksView
             「快速提问」，2026-10-03 vivo 实测），所以必须同时给文字子节点。 -->
        <span class="material-symbols-outlined" aria-hidden="true">{{ showSearch ? 'search_off' : 'search' }}</span>
        <span class="sr-only">{{ showSearch ? '关闭搜索' : '搜索笔记' }}</span>
      </button>
      <button class="notes-action" type="button" aria-label="新建笔记" @click="goCreate">
        <span class="material-symbols-outlined" aria-hidden="true">add</span>
        <span class="sr-only">新建笔记</span>
      </button>
    </HeaderActionsPortal>

    <DbLockedState v-if="dbNotReady" hint="笔记功能需要本地加密数据库" @relogin="router.push('/login')" />

    <template v-else>
      <button v-if="draftBanner" class="draft-banner" type="button" @click="resumeDraft">
        未完成草稿：{{ draftBanner.title || draftBanner.content.slice(0, 24) }}
      </button>

      <!--
        ⚠§832：建草稿失败必须有自己的错误行，且**不能**挂在下面那条 v-else-if 链上 ——
        失败发生时录音已经停止、recError 为空，挂在链上会被下一条分支顶掉。
        （下面 recError 那行正是这个坑：它只覆盖「转写错误」，不覆盖「存不下来」。）
      -->
      <p v-if="draftSaveError" class="studio-error" role="alert">{{ draftSaveError }}</p>

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
        <!--
          §840：与 MeetingListView 同形同源。首屏读失败时 `loading` 为 false、
          `filteredNotes` 为 0 ⇒ 正好落进下面的「还没有笔记」，而那颗
          「加载失败，点击重试」在 `notes.length > 0` 分支里永不渲染。
          ⚠ 搜索态**不进**这个分支（`readError` 里已排除）：搜索 0 结果是
            合法结论，不能被一次无关的历史失败改写成「读取失败」。
          ⚠ `v-else-if` 落在空态组件自己身上 + 自带 `.length` 守卫，
            理由同会议侧（门禁从空态标签那一行向下读条件）。
        -->
        <EmptyState
          v-else-if="readError && filteredNotes.length === 0"
          icon="⚠️"
          :title="readFailure.title"
          :hint="readFailure.hint"
          :action-label="readFailure.action"
          size="sm"
          variant="inline"
          @action="retry"
        />
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
          <div v-if="notes.length > 0" ref="sentinelRef" class="more">
            <span v-if="loadingMore">加载中…</span>
            <button v-else-if="listStatus === 'failed'" type="button" class="more-retry" @click="retry">
              加载失败，点击重试
            </button>
            <button v-else-if="hasMore" type="button" class="more-retry" @click="loadMore">
              {{ needsManualLoad ? '继续加载' : '加载更多' }}
            </button>
            <span v-else>没有更多了</span>
          </div>
        </div>
      </template>

      <VoiceRecorderWidget :recording="isRecording" :busy="recorderUi.busy" @toggle="onMicToggle" />
      <!--
        待办落库提示。位置在录音控件**下方**而不是塞进 NoteMetaSheet：
        行动项是录音停止那一刻就落库的（不等用户打开元信息面板），
        提示要跟录音控件在同一个视觉区里，否则用户点开面板才看到，
        会以为这是面板自己生成的。
      -->
      <p v-if="todoNotice" class="todo-notice" role="status">✅ {{ todoNotice }}</p>
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
import { computed, onActivated, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'
import { Skeleton, EmptyState, DbLockedState, WaveformVisualizer } from '../../components'
import VoiceRecorderWidget from './VoiceRecorderWidget.vue'
import NoteRecordingStudio from './NoteRecordingStudio.vue'
import NoteMetaSheet from './NoteMetaSheet.vue'
import NoteSearchBrief from './NoteSearchBrief.vue'
import { notesApi } from '../../api/notes'
import { createNoteTodos } from './note-todo-persist'
import { buildNoteTodoNotice } from './note-todo-notice.ts'
import { describeVoiceDraftSaveFailure } from './voice-draft-save-notice.ts'
import { awaitNoteMirror } from './notes-persist'
import { useNoteRecording } from './useNoteRecording'
import { noteRecorderUiState } from './note-recording'
import { searchNotesWithIntent, type NoteSearchBriefing } from './note-search'
import { useContinuousList } from '../../composables/useContinuousList'
import {
  isListReadError,
  isListSkeleton,
  listReadFailureNotice,
  LIST_READ_FAIL_TAG,
} from '@/utils/list-load-state'
import { DEFAULT_LIST_PAGE_SIZE, pageHasMore } from '../../native/list-sync/page'
import * as notesStore from './notes-store'
import type { LocalNote } from './notes-store'
import { useListScene } from '../../composables/use-list-scene'
import { useAuthStore } from '../../stores/auth'
import { useApiError } from '../../composables/useApiError'
import { useToast } from '../../composables/useToast'
import {
  describeRecordActionFailure,
  RECORD_ACTION_FAIL_TAG,
} from '../../utils/action-failure-notice'

defineOptions({ name: 'NoteListView' })

const router = useRouter()
const auth = useAuthStore()
const apiError = useApiError()
// notes / loading / loadingMore / hasMore 已由下方 useContinuousList 接管
// （分页数据 + 状态），此处不再重复声明。
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
/**
 * 待办落库结果提示。
 *
 * 为什么必须如实说「已加入 N 条」而不是静默建完：待办和日程提醒是**用户能
 * 看见后果**的动作（待办列表里会多出条目，到点会有提醒）。静默创建等于让
 * 用户在别处凭空多出东西；反过来，期限没解出来时也要说，否则用户会以为
 * 「说了明天下午三点就一定会有提醒」。
 */
const todoNotice = ref('')
// §838：对已有记录的动作失败需要一句人话。
const toast = useToast()
/** §832：建草稿失败的用户可见出口（此前完全没有）。 */
const draftSaveError = ref('')
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

/**
 * 列表数据源：2026-10-04 从 `useListSentinel` 迁到 Hyper 的
 * `useContinuousList`（第二个迁移页，样板见 MeetingListView）。
 *
 * 换来的：代次（切 domain 后旧响应不回写）、失败态（保留已加载行 + 显式重试）、
 * 耗尽态、KeepAlive pause/resume、按 id 去重与同页原位替换。
 *
 * ⚠️ 搜索态与分页态是**两条独立路径**，不能混：
 *   - 分页结果落在 `pagedNotes`（控制器持有）；
 *   - 搜索结果落在 `searchResults`（页面持有）。
 * 早先把搜索结果直接写进 `notes.value`，那在迁移后会立刻被控制器的下一次
 * emit 覆盖掉——搜索结果会「闪一下就没」。所以用 `notes` computed 合并。
 */
const {
  rows: pagedNotes,
  status: listStatus,
  hasMore,
  needsManualLoad,
  sentinelRef,
  resetQuery,
  retry,
  loadMore,
} = useContinuousList<LocalNote>({
  pageSize: DEFAULT_LIST_PAGE_SIZE,
  fetchPage: async ({ page }) => {
    try {
      const res = await notesStore.listNotes({
        limit: DEFAULT_LIST_PAGE_SIZE,
        // 后端是 offset 型，控制器是 page 型（从 1 起）
        offset: (page - 1) * DEFAULT_LIST_PAGE_SIZE,
        domain: domain.value === 'all' ? undefined : domain.value,
        workspaceId: currentWorkspaceId(),
      })
      return {
        rows: res,
        total: Number.MAX_SAFE_INTEGER,
        hasMore: pageHasMore(res.length),
      }
    } catch (e: unknown) {
      // §840：控制器链自带 `.catch` 吞掉 fetch 错误、`resetQuery()` 也不抛，
      // 所以**只有这里**看得见 e.message（能区分「本地库未就绪 → 重新登录」）。
      const msg = e instanceof Error ? e.message : String(e)
      if (msg.includes('LocalDB 未初始化')) dbNotReady.value = true
      else console.error(LIST_READ_FAIL_TAG, '笔记列表', msg)
      // ⚠ 必须 rethrow，否则控制器当成成功（'exhausted'）⇒ 失败态永不显示。
      throw e
    }
  },
})

/** 搜索结果；null = 不在搜索态，展示分页数据。 */
const searchResults = ref<LocalNote[] | null>(null)

/** 模板统一读它：搜索态优先，否则读分页数据。 */
const notes = computed<LocalNote[]>(() => searchResults.value ?? pagedNotes.value)

const readFailure = listReadFailureNotice('笔记')
/**
 * §840：无行 + 读失败 ⇒ 整页失败态。
 * 搜索态排除在外：那时空列表是「没匹配到」，不是读取失败。
 */
const readError = computed(
  () => searchResults.value === null && isListReadError(listStatus.value, notes.value.length),
)
const loading = computed(
  () => isListSkeleton(listStatus.value, notes.value.length, dbNotReady.value),
)
const loadingMore = computed(() => listStatus.value === 'loadingNext' || listStatus.value === 'refreshing')

async function load() {
  dbNotReady.value = false
  // 离开搜索态，回到分页
  searchResults.value = null
  try {
    resetQuery()
  } catch (e: unknown) {
    // §840：只兜 resetQuery 自身的同步抛出；读库失败在 fetchPage 内捕获。
    const msg = e instanceof Error ? e.message : String(e)
    if (msg.includes('LocalDB 未初始化')) dbNotReady.value = true
  }
  // 草稿横幅与分页无关，单独取
  try {
    const drafts = await notesStore.listDraftNotes(currentWorkspaceId())
    draftBanner.value = drafts[0] ?? null
  } catch {
    draftBanner.value = null
  }
}

async function onSearch() {
  const q = query.value.trim()
  if (!q) { briefing.value = null; await load(); return }
  try {
    briefing.value = await searchNotesWithIntent(q, currentWorkspaceId())
    searchResults.value = briefing.value.results.map((r) => r.note)
  } catch {
    searchResults.value = null
  }
}

async function onMicToggle() {
  const stopped = await toggleRecording()
  if (!stopped) return
  // ⚠⚠ §832：此前这里**没有** try/catch，而前端也没有 unhandledrejection 兜底
  //   ⇒ 存储写失败时整条链一路抛出去，用户只看到「点完没反应」，
  //   整段录音白录却**完全不知道**。失败必须变成一句人话。
  //   ⚠ 存下来的失败**不阻断**后续动作（这是一次收尾，不是一次交互）。
  draftSaveError.value = ''
  try {
    // draftId 非空 = 运行时已经落过库了，再建一次就会留下两条笔记。
    if (stopped.draftId) await adoptVoiceDraft(stopped.draftId, stopped.text)
    else await createVoiceDraft(stopped.text, stopped.audioBlob, stopped.durationMs)
  } catch (e) {
    draftSaveError.value = describeVoiceDraftSaveFailure({ error: e, via: 'stop-button' })
    console.error('[notes] 语音草稿保存失败：', e)
  }
}

/** 录音停止 → 语音草稿笔记 + 打开元信息编辑。页面在场与跨页停止(全局
 * 指示条/pendingResult 补建)共用同一条路径。
 * 即时总结:草稿落库后立即调 /api/notes/{id}/summarize,LLM 总结返回后
 * 写到 metaNote.summary,元信息编辑面板与列表预览同步生效。失败非阻塞,
 * 用户仍可正常进入元信息页(空 summary),只是顶部多出一行错误提示。 */
/** 草稿就绪之后的共同尾巴：打开元信息面板 + 刷新列表 + 触发 AI 总结。
 * 「自己建的」和「运行时提前落库的」两条路径都必须走它，否则总结、元信息面板
 * 这套后续动作会在早落库那条路径上整个消失。 */
async function presentVoiceDraft(note: LocalNote, text: string) {
  metaNote.value = note
  metaOpen.value = true
  await load()
  if (!text || !text.trim()) return
  summarizing.value = true
  summarizeError.value = ''
  // 上一条录音的待办提示必须清掉：否则第二次录音（没有行动项）时，
  // 屏幕上还挂着上一条的「已加入 3 条待办」，读起来像是这一次的。
  todoNotice.value = ''
  try {
    // 先把 id / 标题取出来：summarize 是一段最长 90 秒的 await，期间用户
    // 可能关掉元信息面板（onMetaClose 置空 metaNote），届时再读
    // metaNote.value.id 就会在 null 上取字段而抛 TypeError —— 被下面的
    // catch 归类成「总结失败」，实际总结已经拿到了。
    const noteId = metaNote.value.id
    const noteTitle = metaNote.value.title || ''
    // 必须先等笔记在后端落行，否则 /summarize 拿到的是 404 ——
    // 这是「录音后即时总结从来没成功过」的直接成因（见 notes-persist 的
    // mirrorNoteToBackend 注释）。createNote 刻意不等云端，所以等在这里。
    await awaitNoteMirror(noteId)
    const { summary, action_items: actionItems, transactions, bookkeeping, bookkeeping_mismatch: bkMismatch, summary_fallback: summaryFellBack } =
      await notesApi.summarize(noteId)
    if (summary && metaNote.value) {
      metaNote.value = { ...metaNote.value, summary }
      // ⚠ §222：summaryFellBack 时这段是**模型原文**，不是解析出来的摘要。
      //   形态上分不出来（非空、读起来像一段话），此前照常 updateNote 落库
      //   ⇒ 「抱歉，无法生成摘要」这类散文会长期占着笔记的 AI 总结字段。
      //   ⇒ 显示但不落库，并把「这不是摘要」说出来。
      //   ⚠ 外层 if 条件刻意保持 `if (summary && metaNote.value)` 逐字不变：
      //   判据 note-recording-error-visibility 锚在这行字面量上（它自己注释里
      //   记过一次「判据窗口被中文注释撑爆」的教训），改它会红。
      if (!summaryFellBack) {
        await notesStore.updateNote(noteId, { summary }, currentWorkspaceId())
      } else {
        summarizeError.value = '模型没有按约定返回结构化摘要，上面显示的是它的原文（未保存），可在笔记详情页重试'
      }
      await load()
    } else {
      // 第二种静默失败：api/notes.ts 的 summarize 注释写明「失败时返回空
      // summary，前端不阻塞流程」——也就是 200 + {summary:''}，**不抛异常**。
      // 原来的 `if (summary && ...)` 直接跳过，不设错误，于是用户看到的仍是
      // 「没有总结、也没有任何提示」。这里补上兜底文案。
      summarizeError.value = '未能生成 AI 总结（模型未返回内容），可稍后在笔记详情页重试'
    }
    // 需求「把一些时间点自动加入计划日程」在随手记侧的落点。
    //
    // 刻意放在 if/else **之外**：行动项与摘要是同一次响应里的两个独立字段，
    // 模型可能给出行动项却没给出 summary（那正是 else 分支的场景）。
    // 塞进 if 里有两层代价：① 那种情况下的行动项会被一起丢掉；② 它把
    // 现有门禁 note-recording-error-visibility 的判据窗口撑爆 —— 那道门
    // 锚在「if 分支到 else 之间」，而它的注释里已经记过一次「被自己那段
    // 中文注释撑爆而误报」的教训。
    //
    // createNoteTodos 自身永不抛异常，所以这里不需要额外 try。
    const { created, reminders, reminderPlanned, unresolved } = await createNoteTodos(
      noteId,
      actionItems,
      noteTitle,
    )
    // 文案本身在纯模块里（note-todo-notice.ts）：这道门要能直接断言
    // 「用户实际看到的那句话」，写在组件里就只能源码扫描，而源码扫描
    // 分不清代码和注释里引述的同一句话（本会话已被骗三次）。
    //
    // ★ §127：记账三键也必须传进去。服务端**每次** summarize 都会自动记账
    //   （server_assistant.go:704-747，没有调用方开关），而纯记账笔记
    //   一条行动项都没有 ⇒ `created === 0` ⇒ 旧的首行早退直接 return ''，
    //   用户账上凭空多一笔、屏幕上一个字都不说。
    //   ⚠ 提示在 `if (summary)` 之外是必须的：模型可能给出记账却没给出
    //   summary，那正是详情页记账卡片（挂在 v-if="summary" 里）整块消失的场景。
    // ⚠ 这一行**必须保持单行**：既有门 reminder-outcome-honesty.test.ts 的
    //   `matchOutsideComment` 是**逐行**匹配的，调用一拆成多行那条「值流」判据
    //   永远看不到实参（同 §127.4 的教训：判据的匹配边界必须与真实写法对齐）。
    todoNotice.value = buildNoteTodoNotice({ created, reminders, reminderPlanned, unresolved }, { transactions, bookkeeping, bookkeepingMismatch: bkMismatch })
  } catch (e: unknown) {
    summarizeError.value = apiError(e, '总结失败，可稍后在笔记详情页重试')
  } finally {
    summarizing.value = false
  }
}

/**
 * 自己建草稿：只在**运行时没有提前落库**（draftId 为空）时走。
 * 形态与 useNoteRecording.ts 里注册的 sink.create 完全一致，所以两条路径产出的
 * 草稿长得一样（正文占位、createdByVoice、附件）。
 */
async function createVoiceDraft(text: string, audioBlob: Blob, durationMs: number) {
  const note = await notesStore.createNote({
    content: text || '（语音草稿）',
    contentType: 'voice',
    status: 'draft',
    createdByVoice: true,
    audioBlob,
    audioDurationMs: durationMs,
    workspaceId: currentWorkspaceId(),
  })
  await presentVoiceDraft(note, text)
}

/**
 * 接管「运行时已提前落库」的草稿。
 *
 * 2026-10-06 起 stop() 会在兜底转写（最长 10 分钟）**之前**先把音频落库，
 * 好让录音扛得住进程死亡。代价是「建草稿」这件事有两个候选点 ——
 * 若这里再建一次，一次录音就会留下两条笔记。
 * 所以判据只有一条：**draftId 非空 ⇒ 草稿已经存在，只接管，不重建。**
 */
async function adoptVoiceDraft(draftId: string, text: string) {
  const note = await notesStore.getNote(draftId, false, currentWorkspaceId())
  // 落库之后又被删掉（用户手动删 / 工作区切换）⇒ 不重建、不抛错，走完即止。
  if (!note) return
  await presentVoiceDraft(note, text)
}

function resumeDraft() {
  if (!draftBanner.value) return
  metaNote.value = draftBanner.value
  metaOpen.value = true
}

// ⚠⚠ §838：这两处此前没有 try/catch。面板关闭排在 await **之后**（这是对的，
//   不能假装保存成功），但写失败时**零反馈** —— 面板还开着，用户不知道该重试。
async function onMetaSave(data: { title: string; domain: string; tags: string[] }) {
  if (!metaNote.value) return
  try {
    await notesStore.updateNote(metaNote.value.id, { ...data, status: 'saved' }, currentWorkspaceId())
  } catch (e) {
    console.error(RECORD_ACTION_FAIL_TAG, 'meta-save', e)
    toast.error(describeRecordActionFailure('meta-save', { error: e }))
    return
  }
  metaOpen.value = false
  metaNote.value = null
  await load()
}

async function onMetaDelete() {
  if (!metaNote.value) return
  try {
    await notesStore.deleteNote(metaNote.value.id, currentWorkspaceId())
  } catch (e) {
    console.error(RECORD_ACTION_FAIL_TAG, 'meta-delete', e)
    toast.error(describeRecordActionFailure('meta-delete', { error: e }))
    return
  }
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

/**
 * 拾取「页面不在场时停止」的录音产物。
 *
 * 2026-10-06 第一次修（onMounted → onActivated）：这段逻辑原先**只**挂在
 * onMounted 上，但 NoteListView 在 LIST_CACHE_NAMES（use-list-scene.ts:52）
 * 里、defineOptions name 也对得上，所以被 `<KeepAlive :include>` 缓存 ——
 * 从笔记页切走再回来跑的是 onActivated，onMounted 不会重跑。后果：全局停止键
 * 结束后，转写文本与音频都进了 pendingResult，却永远不会被建出来。
 *
 * 2026-10-06 第二次修（去掉 text.trim() 门槛）：上一版把条件写成
 * `if (pending && pending.text.trim())`，而 consumePendingResult() 是
 * **取过即清** —— 条件不成立时不是「留着以后用」，是当场销毁。
 * 跨页停止 + 转写无字（用户中止、网络失败、静音录音）这两个条件同时成立，
 * 录音就被静默丢弃。实测：跨页 + 真后端 17→18 有笔记；跨页 + 兜底返空 18→18，
 * 一条都没有。createVoiceDraft 内部本来就写 `text || '（语音草稿）'`，
 * 能力是有的，门槛把它挡在门外了。
 */
async function pickupPendingRecording(): Promise<void> {
  const pending = consumePendingResult()
  if (!pending) return
  // ⚠ §832：跨页补建是**第二条**建草稿路径，同样必须有 try/catch ——
  //   它跑在 `onMounted` 里，抛错连界面提示都没有，只剩一个未处理的 rejection。
  try {
    if (pending.draftId) await adoptVoiceDraft(pending.draftId, pending.text)
    else await createVoiceDraft(pending.text, pending.audioBlob, pending.durationMs)
  } catch (e) {
    draftSaveError.value = describeVoiceDraftSaveFailure({ error: e, via: 'pending-pickup' })
    console.error('[notes] 跨页补建语音草稿失败：', e)
  }
}

onMounted(async () => {
  await load()
  await pickupPendingRecording()
})
// KeepAlive 命中：切离再回来走的是这条，不是上面的 onMounted
onActivated(async () => {
  await pickupPendingRecording()
})
/* KeepAlive 现场保持：domain 筛选/搜索词保留；仅当笔记数据被详情页修改过才刷新 */
useListScene('notes', load)
</script>

<style scoped>
/* 行动项落库提示。语义是「已办成的事」而不是错误，所以用 success 色；
   role="status" 让读屏器在它出现时播报（待办是用户看不见但确实发生的副作用）。 */
.todo-notice {
  margin: var(--space-2) 0 0;
  color: var(--success, var(--text-secondary));
  font-size: var(--text-smd);
}

/* 录音停止后的转写失败提示。NoteRecordingStudio 里同名类是它 scoped 的，
   不会作用到本页，所以这里自带一份（保持视觉一致：danger 色 + 13px）。 */
.studio-error {
  margin: 0 0 var(--space-2);
  color: var(--danger);
  font-size: var(--text-smd);
}

/* 收尾转写中的状态行。刻意不用 danger 色：这不是错误，是进行中；
   也不该抢 NoteRecordingStudio 的位置（它此刻已被 v-if 卸载）。 */
.studio-busy {
  margin: 0 0 var(--space-2);
  color: var(--text-secondary);
  font-size: var(--text-smd);
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
  background: var(--warning-bg, #fff6e5); color: var(--text-primary); font-size: var(--text-smd);
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
.more-retry {
  display: block;
  width: 100%;
  padding: 10px 12px;
  background: transparent;
  color: var(--text-secondary);
  border: none;
  /* 触控目标下限：compact 档主操作 ≥44px 是红线 */
  min-height: 44px;
  font-size: var(--text-smd);
  cursor: pointer;
}
.more-retry:active { opacity: 0.6; }
.more { padding: 16px 0 24px; text-align: center; font-size: var(--text-sm); color: var(--text-muted); }
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}
</style>
