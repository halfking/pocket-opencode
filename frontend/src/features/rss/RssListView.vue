<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { rssApi, type RSSSource, type RSSItem } from '../../api/rss'
import { toUserMessage } from '../../api/error-message'
import { useConfirm } from '../../composables/useConfirm'

const router = useRouter()
const { t } = useI18n()
const { confirm } = useConfirm()
const tab = ref<'sources' | 'items'>('items')
const sources = ref<RSSSource[]>([])
const items = ref<RSSItem[]>([])
const loading = ref(false)
const errorMsg = ref<string>('')
const statusFilter = ref<'unread' | 'read' | 'starred' | 'archived' | ''>('unread')
const search = ref('')
const importing = ref(false)
const importMsg = ref('')

/** 一键把内置推荐源（IT / 财经 / 时事）加入订阅列表。幂等：已订阅的会跳过。 */
async function importStarter() {
  importing.value = true
  importMsg.value = ''
  errorMsg.value = ''
  try {
    const r = await rssApi.importStarter({})
    importMsg.value = r.created > 0
      ? `已导入 ${r.created} 个推荐源${r.skipped > 0 ? `（${r.skipped} 个已存在，跳过）` : ''}`
      : `推荐源都已经在订阅列表里了（共 ${r.total} 个）`
    tab.value = 'sources'
    await refresh()
  } catch (e: any) {
    errorMsg.value = toUserMessage(e, t, t('errors.operateFailed'))
  } finally {
    importing.value = false
  }
}

function openDigest() {
  router.push({ name: 'rss-digest' })
}

async function refresh() {
  loading.value = true
  errorMsg.value = ''
  try {
    sources.value = await rssApi.listSources()
    items.value = await rssApi.listItems({
      status: statusFilter.value || undefined,
      q: search.value || undefined,
      limit: 50,
    })
  } catch (e: any) {
    // 真机实测：原先直接渲染 e.message，屏幕上出现
    // 「rss_unavailable: store not configured」——英文技术标识且未本地化。
    errorMsg.value = toUserMessage(e, t, t('errors.loadRssFailed'))
  } finally {
    loading.value = false
  }
}

onMounted(refresh)

function openItem(id: string) {
  router.push({ name: 'rss-item', params: { id } })
}
function openAdd() {
  router.push({ name: 'rss-add' })
}
async function refreshSource(src: RSSSource) {
  try {
    await rssApi.refreshSource(src.id)
    await refresh()
  } catch (e: any) {
    errorMsg.value = `刷新 ${src.title} 失败：${e?.message ?? e}`
  }
}
async function toggleSource(src: RSSSource) {
  try {
    await rssApi.patchSource(src.id, { enabled: !src.enabled })
    await refresh()
  } catch (e: any) {
    errorMsg.value = toUserMessage(e, t, t('errors.operateFailed'))
  }
}
async function deleteSource(src: RSSSource) {
  // BUG-AQ：原为裸 confirm()（同步阻塞），在 Android WebView 里会卡死渲染进程。
  const ok = await confirm({ title: '删除订阅源', message: `确定删除订阅源「${src.title}」？所有 item 也会被删除。`, confirmText: '删除', danger: true })
  if (!ok) return
  try {
    await rssApi.deleteSource(src.id)
    await refresh()
  } catch (e: any) {
    errorMsg.value = toUserMessage(e, t, t('errors.operateFailed'))
  }
}
const totalUnread = computed(() => sources.value.reduce((acc, s) => acc + (s.unreadCount || 0), 0))
</script>

<template>
  <div class="rss-page">
    <header class="rss-header">
      <h2>RSS 订阅</h2>
      <span v-if="totalUnread > 0" class="badge">{{ totalUnread }} 未读</span>
      <div class="actions">
        <button class="btn-secondary" type="button" @click="openDigest">
          <!--
            用 subject 而不是 summarize：summarize 不在 material-symbols 子集字体里
            （check-icon-font.mjs 会判红，真机上会显示 "summarize" 字面文本）。
            重建字体要提交 3.4 MB 二进制，不值当；subject 已在子集内且语义接近。
          -->
          <span class="material-symbols-outlined">subject</span>
          今日摘要
        </button>
        <button class="btn-secondary" type="button" @click="refresh" :disabled="loading">
          <span class="material-symbols-outlined">refresh</span>
          刷新
        </button>
        <button class="btn-primary" type="button" @click="openAdd">
          <span class="material-symbols-outlined">add</span>
          新增源
        </button>
      </div>
    </header>

    <div v-if="sources.length === 0" class="starter-hint">
      <div class="hint-text">还没有订阅。可以一键导入内置推荐源（IT / 财经 / 时事，全部经过真实抓取验证），也可以自己填地址。</div>
      <button class="btn-primary" type="button" :disabled="importing" @click="importStarter">
        <span class="material-symbols-outlined">auto_awesome</span>
        {{ importing ? '导入中…' : '一键导入推荐源' }}
      </button>
    </div>
    <div v-else-if="importMsg" class="notice">{{ importMsg }}</div>

    <nav class="tabs">
      <button :class="{ active: tab === 'items' }" @click="tab = 'items'">信息流</button>
      <button :class="{ active: tab === 'sources' }" @click="tab = 'sources'">源 ({{ sources.length }})</button>
    </nav>

    <div v-if="errorMsg" class="error">{{ errorMsg }}</div>

    <!-- 信息流 -->
    <section v-if="tab === 'items'" class="items">
      <div class="filters">
        <select v-model="statusFilter">
          <option value="">全部</option>
          <option value="unread">未读</option>
          <option value="read">已读</option>
          <option value="starred">收藏</option>
          <option value="archived">已归档</option>
        </select>
        <input v-model="search" placeholder="搜索标题/摘要" @keydown.enter="refresh" />
      </div>
      <div v-if="loading" class="empty">加载中…</div>
      <div v-else-if="items.length === 0" class="empty">
        暂无信息。<button class="link" @click="openAdd">添加第一个订阅源</button>。
      </div>
      <ul v-else class="item-list">
        <li v-for="it in items" :key="it.id" :class="{ read: it.status !== 'unread' }" @click="openItem(it.id)">
          <div class="title">{{ it.title || '(无标题)' }}</div>
          <div class="meta">
            <span v-if="it.author">{{ it.author }}</span>
            <span v-if="it.publishedAt">{{ new Date(it.publishedAt).toLocaleDateString() }}</span>
            <span v-if="it.status === 'starred'" class="star">★</span>
          </div>
          <div v-if="it.summary" class="summary">{{ it.summary.slice(0, 160) }}</div>
        </li>
      </ul>
    </section>

    <!-- 源 -->
    <section v-if="tab === 'sources'" class="sources">
      <div v-if="sources.length > 0" class="starter-row">
        <button class="btn-secondary" type="button" :disabled="importing" @click="importStarter">
          <span class="material-symbols-outlined">auto_awesome</span>
          {{ importing ? '导入中…' : '再导入推荐源' }}
        </button>
      </div>
      <div v-if="sources.length === 0" class="empty">
        还没有订阅源。<button class="link" @click="openAdd">添加一个</button>，或在上面一键导入推荐源。
      </div>
      <ul v-else class="source-list">
        <li v-for="src in sources" :key="src.id">
          <div class="src-info">
            <div class="src-title">{{ src.title || src.url }}</div>
            <div class="src-url">{{ src.url }}</div>
            <div class="src-meta">
              <span v-if="src.lastFetchedAt">最近拉取：{{ new Date(src.lastFetchedAt).toLocaleString() }}</span>
              <span v-if="src.error" class="err">{{ src.error }}</span>
              <span v-if="src.unreadCount > 0" class="badge">{{ src.unreadCount }} 未读</span>
              <span v-if="!src.enabled" class="dim">已停用</span>
            </div>
          </div>
          <div class="src-actions">
            <button class="icon" @click="refreshSource(src)" title="立即拉取">
              <span class="material-symbols-outlined">refresh</span>
            </button>
            <button class="icon" @click="toggleSource(src)" :title="src.enabled ? '停用' : '启用'">
              <span class="material-symbols-outlined">{{ src.enabled ? 'pause' : 'play_arrow' }}</span>
            </button>
            <button class="icon" @click="deleteSource(src)" title="删除">
              <span class="material-symbols-outlined">delete</span>
            </button>
          </div>
        </li>
      </ul>
    </section>
  </div>
</template>

<style scoped>
.rss-page { padding: 16px; max-width: 720px; margin: 0 auto; }
.rss-header { display: flex; align-items: center; gap: 12px; margin-bottom: 12px; }
.rss-header h2 { flex: 1; margin: 0; font-size: 20px; }
.actions { display: flex; gap: 8px; }
.btn-primary, .btn-secondary { display: inline-flex; align-items: center; gap: 4px; padding: 6px 12px; border-radius: 6px; border: 1px solid var(--border); cursor: pointer; background: var(--bg-elevated); }
.btn-primary { background: var(--brand-primary); color: white; border-color: var(--brand-primary); }
.badge { background: var(--brand-primary); color: white; padding: 2px 8px; border-radius: 10px; font-size: var(--text-sm); }
.tabs { display: flex; gap: 16px; border-bottom: 1px solid var(--border); margin-bottom: 12px; }
.tabs button { padding: 8px 4px; border: none; background: transparent; cursor: pointer; border-bottom: 2px solid transparent; }
.tabs button.active { border-bottom-color: var(--brand-primary); color: var(--brand-primary); }
.filters { display: flex; gap: 8px; margin-bottom: 12px; }
.filters select, .filters input { padding: 6px 10px; border: 1px solid var(--border); border-radius: 6px; }
.filters input { flex: 1; }
.empty { padding: 32px; text-align: center; color: var(--text-muted); }
.error { padding: 8px 12px; background: var(--err-bg); color: var(--err-fg); border-radius: 6px; margin-bottom: 8px; }
.starter-hint { border: 1px solid var(--border); border-radius: 10px; padding: 12px; margin-bottom: 12px; background: var(--bg-elevated); }
.hint-text { font-size: var(--text-smd); color: var(--text-secondary); margin-bottom: 8px; }
.starter-row { margin-bottom: 8px; }
.notice { padding: 8px 12px; background: var(--bg-hover); border-radius: 6px; margin-bottom: 8px; font-size: var(--text-sm); }
.item-list { list-style: none; padding: 0; margin: 0; }
.item-list li { padding: 12px 8px; border-bottom: 1px solid var(--border); cursor: pointer; }
.item-list li:hover { background: var(--bg-hover); }
.item-list li.read .title { color: var(--text-muted); font-weight: normal; }
.title { font-weight: 600; margin-bottom: 4px; }
.meta { font-size: var(--text-sm); color: var(--text-muted); display: flex; gap: 8px; }
.star { color: var(--warn); }
.summary { font-size: var(--text-smd); color: var(--text-secondary); margin-top: 4px; }
.source-list { list-style: none; padding: 0; }
.source-list li { display: flex; gap: 12px; align-items: flex-start; padding: 12px 0; border-bottom: 1px solid var(--border); }
.src-info { flex: 1; }
.src-title { font-weight: 600; }
.src-url { font-size: var(--text-sm); color: var(--text-muted); word-break: break-all; }
.src-meta { font-size: var(--text-sm); color: var(--text-muted); margin-top: 4px; display: flex; gap: 8px; }
.src-meta .err { color: var(--err-fg); }
.src-meta .dim { color: var(--text-faint); }
.src-actions { display: flex; gap: 4px; }
.icon { padding: 4px; border: none; background: transparent; cursor: pointer; border-radius: 4px; }
.icon:hover { background: var(--bg-hover); }
.link { background: none; border: none; color: var(--brand-primary); cursor: pointer; text-decoration: underline; padding: 0; }
</style>