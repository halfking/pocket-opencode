<!--
  NoteDetailView — note 详情展示页。

  - 顶部：标题 + 返回按钮（由 AppLayout 提供 canGoBack）
  - 中部：Markdown 渲染的 content
  - 元信息条：域色标 + 创建时间 + tags + 创建方式
  - 关联推荐区：基于 content 语义搜索（排除自身 ID）
  - 操作按钮：编辑 / 删除 / 重新分类
-->
<template>
  <div class="note-detail-view">
          <div v-if="loading" class="state" role="status">加载中…</div>

      <ErrorState
        v-else-if="loadError"
        title="笔记加载失败"
        :message="loadError"
        @retry="load"
      />

      <div v-else-if="!note" class="state">
        <p>笔记不存在或已被删除</p>
        <button class="action-btn ghost" @click="goBack">返回</button>
      </div>

      <div v-else class="note-detail">
        <!-- 标题区 -->
        <header class="note-header" :class="`domain-${note.domain || 'work'}`">
          <h1 class="note-title">{{ displayTitle }}</h1>
          <!-- P2：读到有用的笔记当场进记忆回路 / 或变成一条工作项 -->
          <div class="note-actions">
            <AddToLearningButton source-kind="note" :source-id="note.id" />
            <AddToLearningButton source-kind="note" :source-id="note.id" as-task task-type="study" />
          </div>
        </header>

        <!-- 元信息条 -->
        <div class="note-meta">
          <span class="domain-tag" :class="`domain-${note.domain || 'work'}`">
            {{ domainText(note.domain) }}
          </span>
          <span class="meta-text">{{ formatTime(note.createdAt) }}</span>
          <span v-if="note.createdByVoice" class="meta-tag voice">🎙 语音</span>
          <span v-else class="meta-tag text">✍ 文字</span>
          <span v-if="note.tags && note.tags.length" class="tags">
            <span v-for="t in note.tags" :key="t" class="tag-chip">#{{ t }}</span>
          </span>
        </div>

        <!-- Markdown 正文 -->
        <article class="markdown-body" v-html="renderedMarkdown" />

        <!-- AI 总结 + 自动记账 -->
        <section class="summary-section">
          <div class="summary-head">
            <h2 class="section-title">AI 总结</h2>
            <button class="action-btn ghost" :disabled="summarizing" @click="summarize">
              {{ summarizing ? '生成中…' : (summary ? '🔄 重新总结' : '✨ 生成总结') }}
            </button>
          </div>
          <p v-if="summaryError" class="form-error" role="alert">{{ summaryError }}</p>
          <!--
            §125：与列表页 NoteListView 同名的待办落库提示。
            位置在错误提示**之后**、总结卡片**之前** —— 行动项与摘要是同一次
            响应里的两个独立字段，模型可能给出行动项却没给出 summary，
            所以提示不能塞进 v-if="summary" 的卡片里，否则那一次会被整块挡掉。
          -->
          <p v-if="todoNotice" class="todo-notice" role="status">✅ {{ todoNotice }}</p>
          <div v-if="summary" class="summary-card">
            <p class="summary-text">{{ summary }}</p>
            <div v-if="summaryTxs.length > 0" class="summary-txs">
              <div class="txs-title">{{ bookkeeping === 'existing' ? '已自动入账（此前已记，未重复）' : '已自动入账' }}</div>
              <p v-if="bookkeepingMismatch" class="txs-mismatch" role="note">
                ⚠ 笔记内容与已入账记录不一致（金额或收支方向），请到记账页核对调整
              </p>
              <div v-for="tx in summaryTxs" :key="tx.id" class="tx-line">
                <span>{{ tx.type === 'income' ? '收入' : '支出' }} · {{ tx.category }}</span>
                <span class="tx-amt" :class="tx.type">
                  {{ tx.type === 'income' ? '+' : '-' }}¥{{ tx.amount.toFixed(2) }}
                </span>
              </div>
              <router-link to="/finance" class="txs-link">查看记账 →</router-link>
            </div>
          </div>
          <p v-else-if="!summarizing && !summaryError" class="summary-hint">
            由网关模型生成 3-5 句摘要；内容含金额时会自动记账（可在记账页核对删除）。
          </p>
        </section>

        <!-- 关联推荐 -->
        <section v-if="related.length > 0" class="related-section">
          <h2 class="section-title">相关笔记</h2>
          <div class="related-list">
            <div
              v-for="r in related"
              :key="r.id"
              class="related-card"
              :class="`domain-${r.domain || 'work'}`"
              tabindex="0"
              role="link"
              :aria-label="`打开笔记：${r.title || (r.content || '').slice(0, 28)}`"
              @click="openNote(r.id)"
              @keydown.enter.prevent="openNote(r.id)"
              @keydown.space.prevent="openNote(r.id)"
            >
              <div class="related-title">{{ r.title || r.content.slice(0, 28) }}</div>
              <div class="related-snippet">{{ r.content }}</div>
            </div>
          </div>
        </section>

        <!-- 操作按钮 -->
        <div class="actions">
          <button class="action-btn primary" @click="goEdit">✎ 编辑</button>
          <button class="action-btn ghost" :disabled="reclassifying" @click="reclassify">
            {{ reclassifying ? '分类中…' : '🏷 重新分类' }}
          </button>
          <button class="action-btn danger" @click="onDelete">🗑 删除</button>
        </div>
        <p v-if="reclassifyError" class="form-error" role="alert">{{ reclassifyError }}</p>
      </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import * as notesStore from './notes-store'
import type { LocalNote } from './notes-store'
import AddToLearningButton from '../study/AddToLearningButton.vue'
import { http } from '../../api/http'
import { ErrorState } from '../../components'
import { useAuthStore } from '../../stores/auth'
import { useConfirm } from '../../composables/useConfirm'
import { markListDirty } from '../../composables/list-scene-store'
import { useApiError } from '../../composables/useApiError'
import { createNoteTodos } from './note-todo-persist'
import { buildNoteTodoNotice } from './note-todo-notice.ts'
import { awaitNoteMirror } from './notes-persist'
import type { NoteActionItem } from '../../api/notes'

const apiError = useApiError()
const route = useRoute()
const { confirm } = useConfirm()
const router = useRouter()
const auth = useAuthStore()

const note = ref<LocalNote | null>(null)
const loading = ref(true)
const related = ref<LocalNote[]>([])
const reclassifying = ref(false)
const reclassifyError = ref('')
const loadError = ref('')

const displayTitle = computed(() => {
  if (!note.value) return ''
  return note.value.title || note.value.content.slice(0, 40)
})

const renderedMarkdown = computed(() => {
  if (!note.value) return ''
  // marked v18 默认同步返回 string；强制 async:false 保证类型为 string
  // marked 默认不过滤 HTML，存在存储型 XSS 风险，输出必须经 DOMPurify 消毒
  const out = marked.parse(note.value.content, { async: false })
  const html = typeof out === 'string' ? out : ''
  
  // Markdown 安全白名单配置（第七轮审计修复）
  // 允许合法的 Markdown 渲染标签（表格、代码块、链接等），禁止危险标签和属性
  return DOMPurify.sanitize(html, {
    ALLOWED_TAGS: [
      'h1', 'h2', 'h3', 'h4', 'h5', 'h6',
      'p', 'br', 'strong', 'em', 'u', 'del', 's',
      'a', 'img',
      'ul', 'ol', 'li',
      'blockquote', 'pre', 'code',
      'table', 'thead', 'tbody', 'tr', 'th', 'td',  // 允许 Markdown 表格
      'hr', 'div', 'span'
    ],
    ALLOWED_ATTR: ['href', 'src', 'alt', 'title', 'class', 'id'],
    ALLOW_DATA_ATTR: false,  // 禁止 data-* 属性（防止 XSS）
    FORBID_TAGS: ['style', 'script'],  // 明确禁止危险标签
    FORBID_ATTR: ['onerror', 'onload', 'onclick', 'onmouseover']  // 禁止事件处理器
  })
})

onMounted(() => load())

async function load() {
  const id = route.params.id as string
  loading.value = true
  loadError.value = ''
  try {
    const fetched = await notesStore.getNote(id, false, currentWorkspaceId())
    if (fetched) fetched.content = await notesStore.loadFullContent(fetched)
    note.value = fetched
    if (fetched) await loadRelated(fetched)
  } catch (e: any) {
    loadError.value = apiError(e, 'errors.loadNotesFailed')
  } finally {
    loading.value = false
  }
}

async function loadRelated(target: LocalNote) {
  try {
    // 取 5 条以便排除自身后还能剩 4 条
    const results = await notesStore.searchSemantic(target.content, 5, currentWorkspaceId())
    related.value = results
      .map((r) => r.note)
      .filter((n) => n.id !== target.id)
      .slice(0, 4)
  } catch {
    related.value = []
  }
}

function currentWorkspaceId(): string {
  return auth.workspaceId || 'default'
}

function goBack() {
  if (window.history.length > 1) router.back()
  else router.push('/notes')
}

function goEdit() {
  if (!note.value) return
  router.push(`/notes/${note.value.id}/edit`)
}

function openNote(id: string) {
  router.push(`/notes/${id}`)
}

async function onDelete() {
  if (!note.value) return
  const ok = await confirm({ title: '删除笔记', message: '确认删除这条笔记？此操作不可撤销。', confirmText: '删除', danger: true })
  if (!ok) return
  await notesStore.deleteNote(note.value.id, currentWorkspaceId())
  markListDirty('notes')
  router.back()
}

async function reclassify() {
  if (!note.value || reclassifying.value) return
  reclassifying.value = true
  reclassifyError.value = ''
  try {
    await http(`/api/notes/${note.value.id}/classify`, { method: 'POST' })
    // 后端当前 stub；刷新本地数据以拿最新分类
    const refreshed = await notesStore.getNote(note.value.id, false, currentWorkspaceId())
    if (refreshed) note.value = refreshed
    markListDirty('notes')
    reclassifyError.value = ''
  } catch (e: any) {
    console.warn('[note] 重新分类失败:', e)
    reclassifyError.value = apiError(e, 'errors.operateFailed')
  } finally {
    reclassifying.value = false
  }
}

// ---- AI 总结 + 自动记账 ----
const summarizing = ref(false)
const summary = ref('')
const summaryTxs = ref<Array<{ id: string; type: string; amount: number; category: string }>>([])
// created=本次新入账；existing=该笔记此前已入账，本次返回既有记录（按笔记幂等）
const bookkeeping = ref<'' | 'created' | 'existing'>('')
// existing 且本次解析的金额/方向与已入账记录不一致（笔记内容改过）时提示核对
const bookkeepingMismatch = ref(false)
const summaryError = ref('')
// 行动项落库提示。与列表页 NoteListView 的同名 ref 同义。
const todoNotice = ref('')

interface NoteSummarizeResp {
  summary?: string
  transactions?: Array<{ id: string; type: string; amount: number; category: string }>
  bookkeeping?: 'created' | 'existing'
  bookkeeping_mismatch?: boolean
  /**
   * §125：本页此前**只声明了上面四个记账键**，于是服务端回的 action_items
   * 在这一侧被直接丢弃 —— 从详情页点「生成总结」，行动项一条都不会变成待办、
   * 也不会进日程，而界面不报错、不提示，少了东西这件事完全不可见。
   *
   * ⚠ 与列表页的分叉（§117.9）：`api/notes.ts` 只声明 summary/model/action_items，
   * 本地这份只声明记账四键，**互不为超集**。本页走的是裸 `http`，不走
   * notesApi.summarize，所以两份声明必须各自补齐自己用到的键。
   */
  action_items?: NoteActionItem[]
  /**
   * §222：true = 服务端给的 `summary` 是**模型原文**，不是它解析出来的摘要。
   *
   * ⚠ 与上面那份分叉同一类问题（两份声明互不为超集）：本键此前**两份都没有**，
   *   于是回落形态（模型没按 JSON 契约返回）在两侧都不可见。
   *   本页不落库，但会把原文当摘要**显示**在总结区，同样是说错话。
   */
  summary_fallback?: boolean
}

async function summarize() {
  if (!note.value || summarizing.value) return
  summarizing.value = true
  summaryError.value = ''
  // 上一次的提示必须清掉：否则第二次总结（没有行动项）时，屏幕上还挂着
  // 上一条的「已加入 3 条待办」，读起来像是这一次的。
  todoNotice.value = ''
  try {
    // 先把 id / 标题取出来：summarize 是一段最长 90 秒的 await，期间用户
    // 可能关掉/切走这条笔记（note.value 置 null），届时再读 note.value.id
    // 就会在 null 上取字段而抛 TypeError —— 被下面的 catch 归类成
    // 「总结失败」，实际总结已经拿到了。
    const noteId = note.value.id
    const noteTitle = note.value.title || ''
    // §125.8：必须先等笔记在后端落行，否则 summarize 拿到的是 404。
    // createNote 刻意不等云端（notes-persist.ts:122 只把镜像 promise 登记进
    // noteMirrorReady），而本页 load() 读的是**本地** store，所以刚录完的笔记
    // 在镜像落地前就能被打开 —— 这段时间里按「生成总结」必然 404。
    // 列表页那边早有同样的 await（见 NoteListView 的 presentVoiceDraft），
    // 本页此前漏了。awaitNoteMirror 永不抛异常，且没有在途镜像时立即返回，
    // 所以这条对「从云端进来的老笔记」零成本、行为不变。
    await awaitNoteMirror(noteId)
    const res = await http<NoteSummarizeResp>(`/api/notes/${noteId}/summarize`, { method: 'POST' })
    summary.value = res.summary || ''
    summaryTxs.value = res.transactions ?? []
    bookkeeping.value = res.bookkeeping ?? ''
    bookkeepingMismatch.value = res.bookkeeping_mismatch ?? false
    if (res.summary_fallback) {
      // ⚠ §222：这段是**模型原文**，不是解析出来的摘要。必须说出来，
      //   否则用户会把「抱歉，无法生成」当成自己笔记的总结读下去。
      summaryError.value = '模型没有按约定返回结构化摘要，下面显示的是它的原文'
    } else if (!summary.value) {
      summaryError.value = '模型未返回内容，请稍后重试'
    }
    // §125：需求「把一些时间点自动加入计划日程」在详情页的落点。
    // 刻意放在 if 之外 —— 行动项与摘要是同一次响应里的两个独立字段，
    // 模型可能给出行动项却没给出 summary（那正是上面设错误提示的场景），
    // 塞进 if 里会把那时的行动项一起丢掉。
    // createNoteTodos 自身永不抛异常，所以这里不需要额外 try。
    const { created, reminders, reminderPlanned, unresolved } = await createNoteTodos(
      noteId,
      res.action_items,
      noteTitle,
    )
    // §127：记账三键也传进去。`created === 0` 时早退只看「什么都没做」，
    // 而本页的记账卡片挂在 `v-if="summary"` **里面** —— 模型给出记账却没给出
    // summary 时那张卡片整块不显示，两者叠加就是纯记账场景全静默。
    // 提示在卡片之外，才不依赖 summary 是否存在。
    todoNotice.value = buildNoteTodoNotice(
      { created, reminders, reminderPlanned, unresolved },
      { transactions: summaryTxs.value, bookkeeping: bookkeeping.value, bookkeepingMismatch: bookkeepingMismatch.value },
    )
  } catch (e: any) {
    summaryError.value = apiError(e, 'errors.summaryFailed')
  } finally {
    summarizing.value = false
  }
}

const domainText = (d?: string | null) =>
  ({ work: '工作', study: '学习', life: '生活', idea: '想法' }[d || 'work'] || '工作')

function formatTime(ms: number) {
  const d = new Date(ms)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}
</script>

<style scoped>
.note-detail-view { min-height: 100%; background: var(--bg-base); }
.state { text-align: center; color: var(--text-secondary); padding: var(--space-6); }
.note-detail { display: flex; flex-direction: column; gap: var(--space-4); padding-bottom: var(--space-6); }
.note-header {
  background: var(--bg-card);
  border-radius: var(--radius-md);
  padding: var(--space-4) var(--space-5);
  border-left: 4px solid var(--cat-work);
  box-shadow: var(--shadow-sm);
}
.note-header.domain-study { border-left-color: var(--cat-study); }
.note-header.domain-life { border-left-color: var(--cat-life); }
.note-header.domain-idea { border-left-color: var(--cat-idea); }
.note-title { font-size: 20px; font-weight: 700; margin: 0; color: var(--text-primary); line-height: 1.4; }

.note-meta {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  align-items: center;
  padding: var(--space-3) var(--space-4);
  background: var(--bg-card);
  border-radius: var(--radius-md);
  font-size: var(--text-sm);
  color: var(--text-secondary);
  box-shadow: var(--shadow-sm);
}
.domain-tag {
  padding: 2px 8px;
  border-radius: var(--radius-sm);
  font-weight: 600;
  background: var(--bg-subtle);
  color: var(--text-primary);
}
.domain-tag.domain-work { background: var(--cat-work-bg); color: var(--cat-work); }
.domain-tag.domain-study { background: var(--cat-study-bg); color: var(--cat-study); }
.domain-tag.domain-life { background: var(--cat-life-bg); color: var(--cat-life); }
.domain-tag.domain-idea { background: var(--cat-idea-bg); color: var(--cat-idea); }
.meta-text { font-size: var(--text-sm); }
.meta-tag.voice, .meta-tag.text {
  padding: 2px 6px;
  background: var(--bg-subtle);
  border-radius: var(--radius-sm);
  font-size: var(--text-2xs);
}
.tags { display: inline-flex; gap: var(--space-1); flex-wrap: wrap; }
.tag-chip {
  background: var(--bg-subtle);
  color: var(--text-secondary);
  padding: 2px 8px;
  border-radius: var(--radius-full);
  font-size: var(--text-2xs);
}

.markdown-body {
  background: var(--bg-card);
  border-radius: var(--radius-md);
  padding: var(--space-5);
  box-shadow: var(--shadow-sm);
  color: var(--text-primary);
  font-size: var(--text-md);
  line-height: 1.7;
}
.markdown-body :deep(h1),
.markdown-body :deep(h2),
.markdown-body :deep(h3) { margin: var(--space-4) 0 var(--space-2); font-weight: 700; }
.markdown-body :deep(h1) { font-size: 20px; }
.markdown-body :deep(h2) { font-size: var(--text-xl); }
.markdown-body :deep(h3) { font-size: var(--text-lg); }
.markdown-body :deep(p) { margin: var(--space-2) 0; }
.markdown-body :deep(code) {
  background: var(--bg-subtle);
  padding: 1px 6px;
  border-radius: var(--radius-sm);
  font-family: var(--font-mono);
  font-size: var(--text-smd);
}
.markdown-body :deep(pre) {
  background: var(--bg-subtle);
  padding: var(--space-3);
  border-radius: var(--radius-sm);
  overflow-x: auto;
}
.markdown-body :deep(pre code) { background: transparent; padding: 0; }
.markdown-body :deep(ul),
.markdown-body :deep(ol) { padding-left: var(--space-5); }
.markdown-body :deep(blockquote) {
  border-left: 3px solid var(--border-strong);
  padding-left: var(--space-3);
  color: var(--text-secondary);
  margin: var(--space-3) 0;
}
.markdown-body :deep(a) { color: var(--brand-primary); text-decoration: none; }

.related-section {
  background: var(--bg-card);
  border-radius: var(--radius-md);
  padding: var(--space-4);
  box-shadow: var(--shadow-sm);
}
.section-title {
  font-size: var(--text-base);
  font-weight: 600;
  color: var(--text-secondary);
  margin: 0 0 var(--space-3) 0;
}
.related-list { display: flex; flex-direction: column; gap: var(--space-2); }
.related-card {
  padding: var(--space-3);
  background: var(--bg-subtle);
  border-radius: var(--radius-sm);
  border-left: 3px solid var(--cat-work);
  cursor: pointer;
}
.related-card.domain-study { border-left-color: var(--cat-study); }
.related-card.domain-life { border-left-color: var(--cat-life); }
.related-card.domain-idea { border-left-color: var(--cat-idea); }
.related-title { font-weight: 600; font-size: var(--text-smd); margin-bottom: 2px; }
.related-snippet {
  font-size: var(--text-sm);
  color: var(--text-secondary);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.actions {
  display: flex;
  gap: var(--space-3);
  padding-top: var(--space-2);
}
.action-btn {
  flex: 1;
  padding: var(--space-3) var(--space-2);
  border-radius: var(--radius-md);
  border: 1px solid var(--border);
  background: var(--bg-card);
  font-size: var(--text-base);
  font-weight: 500;
  cursor: pointer;
  color: var(--text-primary);
  transition: opacity var(--duration-fast);
}
.action-btn:active { opacity: 0.7; }
.action-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.action-btn.primary {
  background: var(--brand-gradient);
  color: var(--text-inverse);
  border: none;
}
.action-btn.ghost { background: var(--bg-subtle); }
.action-btn.danger { color: var(--danger); border-color: var(--danger); }

.form-error {
  margin: 0;
  padding: var(--space-3) var(--space-4);
  border-radius: var(--radius-md);
  background: var(--danger-bg);
  color: var(--danger);
  font-size: var(--text-smd);
  font-weight: 500;
  line-height: 1.5;
}

/* AI 总结 + 自动记账 */
.summary-section { padding: 0 var(--space-5); }
.summary-head {
  display: flex; align-items: center; justify-content: space-between; gap: 10px;
  margin-bottom: var(--space-2);
}
.summary-head .section-title { margin: 0; }
.summary-card {
  background: var(--bg-card); border: 1px solid var(--border);
  border-radius: var(--radius-md); padding: var(--space-3) var(--space-4);
}
.summary-text { margin: 0; font-size: var(--text-smd); line-height: 1.7; color: var(--text-primary); white-space: pre-wrap; }
.summary-txs {
  margin-top: var(--space-3); padding-top: var(--space-3);
  border-top: 1px dashed var(--border);
}
.txs-title { font-size: var(--text-sm); font-weight: 600; color: var(--text-secondary); margin-bottom: 6px; }
.txs-mismatch {
  margin: 0 0 6px; font-size: var(--text-sm); line-height: 1.6;
  color: var(--warning, #d97706);
}
.tx-line {
  display: flex; align-items: center; justify-content: space-between;
  font-size: var(--text-sm); color: var(--text-secondary); padding: 3px 0;
}
.tx-amt.income { color: var(--success, #10b981); font-weight: 600; }
.tx-amt.expense { color: var(--danger); font-weight: 600; }
.txs-link { display: inline-block; margin-top: 8px; font-size: var(--text-sm); color: var(--brand-primary); text-decoration: none; }
.summary-hint { margin: 0; font-size: var(--text-sm); color: var(--text-muted); line-height: 1.6; }

/* §125 行动项落库提示。与 NoteListView 的同名类保持同一视觉语言：
   success 色 + --text-smd，语义是「已办成的事」而不是错误；
   role="status" 让读屏器在它出现时播报（待办是用户看不见但确实发生的副作用）。 */
.todo-notice {
  margin: 0;
  color: var(--success, var(--text-secondary));
  font-size: var(--text-smd);
}
</style>
