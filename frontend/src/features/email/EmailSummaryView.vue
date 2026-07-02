<!--
  EmailSummaryView — daily summary browser.
  Two routes use this view:
    /email/summary          → list of summaries (latest first)
    /email/summary/:date    → specific date detail with Markdown rendering
  Markdown via marked (already installed in prompt 1).
-->
<template>
  <AppLayout>
    <template v-if="mode === 'list'">
      <div class="header-row">
        <h2 class="page-title">邮件摘要</h2>
        <span v-if="!loading" class="count">{{ summaries.length }} 天</span>
      </div>

      <div v-if="loading" class="state">加载中…</div>
      <div v-else-if="summaries.length === 0" class="state">
        <p>暂无摘要。</p>
        <p class="hint">每日 21:00 自动生成；缺数据时先确认 pockend 调度器在线。</p>
      </div>

      <div v-else class="summary-list">
        <div
          v-for="s in summaries"
          :key="s.id"
          class="summary-card"
          @click="open(s.summaryDate)"
        >
          <div class="card-top">
            <span class="date">{{ s.summaryDate }}</span>
            <span class="badge" :class="{ important: s.importantCount > 0 }">
              {{ s.importantCount }} 封重要
            </span>
          </div>
          <div class="total">共 {{ s.totalCount }} 封</div>
          <div class="preview">{{ preview(s.content) }}</div>
        </div>
      </div>
    </template>

    <template v-else-if="mode === 'detail'">
      <div v-if="loading" class="state">加载中…</div>
      <div v-else-if="!summary" class="state">
        <p>{{ date }} 当日的摘要暂未生成。</p>
        <p class="hint">每日 21:00 cron 自动生成，或手动触发抓取后会触发总结。</p>
        <button class="back-link" @click="goList">← 返回摘要列表</button>
      </div>

      <article v-else class="detail">
        <header class="detail-header">
          <h2 class="detail-title">📬 {{ summary.summaryDate }} 邮件摘要</h2>
          <div class="meta">
            <span>共 {{ summary.totalCount }} 封</span>
            <span class="dot">·</span>
            <span :class="{ emph: summary.importantCount > 0 }">
              {{ summary.importantCount }} 封重要
            </span>
          </div>
        </header>

        <div class="markdown" v-html="renderedMarkdown"></div>

        <div v-if="summary.actionItems && summary.actionItems.length" class="todos">
          <div class="todos-title">📌 待办</div>
          <ul class="todo-list">
            <li v-for="(item, i) in summary.actionItems" :key="i" :class="{ done: item.done }">
              <span class="check">{{ item.done ? '✓' : '○' }}</span>
              <span class="text">{{ item.text }}</span>
            </li>
          </ul>
        </div>

        <button class="back-link" @click="goList">← 返回摘要列表</button>
      </article>
    </template>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import AppLayout from '../../app/AppLayout.vue'
import { emailApi } from '../../api/email'
import type { DailySummary } from '../../api/email'
import { ApiError } from '../../api/http'

const route = useRoute()
const router = useRouter()

type ViewMode = 'list' | 'detail'

const mode = computed<ViewMode>(() => (route.params.date ? 'detail' : 'list'))
const date = computed(() => (route.params.date as string | undefined) || '')

const loading = ref(true)
const summaries = ref<DailySummary[]>([])
const summary = ref<DailySummary | null>(null)

async function loadList() {
  loading.value = true
  try {
    const res = await emailApi.listSummaries()
    summaries.value = res.summaries || []
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) {
      // 后端 endpoint 尚未实现 → 空列表
      summaries.value = []
    } else {
      console.warn('[email] 拉取摘要列表失败:', e)
      summaries.value = []
    }
  } finally {
    loading.value = false
  }
}

async function loadDetail(target: string) {
  loading.value = true
  summary.value = null
  try {
    summary.value = await emailApi.getSummary(target)
  } catch (e) {
    if (e instanceof ApiError && (e.status === 404 || e.status === 400)) {
      // 未生成 → 友好提示
      summary.value = null
    } else {
      console.warn('[email] 拉取摘要详情失败:', e)
      summary.value = null
    }
  } finally {
    loading.value = false
  }
}

function loadByMode() {
  if (mode.value === 'detail' && date.value) loadDetail(date.value)
  else loadList()
}

const renderedMarkdown = computed(() => {
  if (!summary.value) return ''
  // marked v18 默认不过滤 HTML，存在存储型 XSS 风险；
  // 强制 async:false 保证返回 string，再经 DOMPurify 清洗后渲染。
  const out = marked.parse(summary.value.content, { async: false })
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

function preview(s: string | undefined) {
  if (!s) return ''
  // 去掉 markdown 标记 → 取前 100 字
  const plain = s
    .replace(/^#+\s+/gm, '')
    .replace(/\*\*([^*]+)\*\*/g, '$1')
    .replace(/`([^`]+)`/g, '$1')
    .replace(/\n+/g, ' ')
    .trim()
  return plain.length > 100 ? `${plain.slice(0, 100)}…` : plain
}

function open(d: string) {
  router.push(`/email/summary/${d}`)
}

function goList() {
  router.push('/email/summary')
}

watch(() => route.fullPath, loadByMode)
onMounted(loadByMode)
</script>

<style scoped>
.header-row {
  display: flex; align-items: baseline; justify-content: space-between;
  margin-bottom: var(--space-3);
}
.page-title { font-size: 18px; font-weight: 600; margin: 0; color: var(--text-primary); }
.count { font-size: 12px; color: var(--text-muted); }
.state { text-align: center; color: var(--text-secondary); padding: var(--space-6); }
.hint { font-size: 12px; color: var(--text-muted); margin-top: var(--space-2); }

.summary-list { display: flex; flex-direction: column; gap: var(--space-2); }
.summary-card {
  background: var(--bg-card);
  border-radius: var(--radius-md);
  padding: var(--space-3) var(--space-4);
  box-shadow: var(--shadow-sm);
  cursor: pointer;
}
.summary-card:active { background: var(--bg-subtle); }
.card-top { display: flex; justify-content: space-between; align-items: center; }
.date { font-size: 14px; font-weight: 600; color: var(--text-primary); }
.badge {
  font-size: 11px;
  padding: 2px 8px;
  border-radius: var(--radius-full);
  background: var(--bg-subtle);
  color: var(--text-secondary);
}
.badge.important { background: rgba(239,68,68,0.12); color: var(--danger); }
.total { font-size: 12px; color: var(--text-muted); margin-top: 2px; }
.preview {
  margin-top: var(--space-2);
  font-size: 13px;
  color: var(--text-secondary);
  line-height: 1.5;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.detail { display: flex; flex-direction: column; gap: var(--space-4); padding-bottom: var(--space-6); }
.detail-header {
  background: var(--bg-card);
  border-radius: var(--radius-md);
  padding: var(--space-4);
  box-shadow: var(--shadow-sm);
}
.detail-title { font-size: 18px; font-weight: 700; margin: 0; color: var(--text-primary); }
.meta { font-size: 12px; color: var(--text-muted); margin-top: var(--space-1); display: flex; gap: var(--space-2); align-items: center; }
.meta .emph { color: var(--danger); font-weight: 600; }
.dot { color: var(--text-muted); }

.markdown {
  background: var(--bg-card);
  border-radius: var(--radius-md);
  padding: var(--space-4);
  box-shadow: var(--shadow-sm);
  font-size: 14px;
  line-height: 1.7;
  color: var(--text-primary);
}
.markdown :deep(h1) { font-size: 18px; font-weight: 700; margin: var(--space-3) 0 var(--space-2); }
.markdown :deep(h2) { font-size: 16px; font-weight: 600; margin: var(--space-3) 0 var(--space-2); }
.markdown :deep(h3) { font-size: 14px; font-weight: 600; margin: var(--space-2) 0; }
.markdown :deep(p) { margin: var(--space-2) 0; }
.markdown :deep(ul),
.markdown :deep(ol) { padding-left: var(--space-5); margin: var(--space-2) 0; }
.markdown :deep(li) { margin: 2px 0; }
.markdown :deep(code) {
  font-family: ui-monospace, SFMono-Regular, monospace;
  background: var(--bg-subtle);
  padding: 1px 4px;
  border-radius: 4px;
  font-size: 13px;
}
.markdown :deep(strong) { font-weight: 700; color: var(--text-primary); }

.todos {
  background: var(--bg-card);
  border-radius: var(--radius-md);
  padding: var(--space-3) var(--space-4);
  box-shadow: var(--shadow-sm);
}
.todos-title { font-size: 13px; font-weight: 600; color: var(--text-primary); margin-bottom: var(--space-2); }
.todo-list { list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; gap: var(--space-1); }
.todo-list li { display: flex; gap: var(--space-2); align-items: flex-start; font-size: 13px; color: var(--text-primary); }
.todo-list li .check { color: var(--text-muted); }
.todo-list li.done .text { text-decoration: line-through; color: var(--text-muted); }

.back-link {
  align-self: flex-start;
  border: none;
  background: transparent;
  color: var(--brand-primary);
  font-size: 14px;
  cursor: pointer;
  padding: var(--space-2) 0;
}
</style>
