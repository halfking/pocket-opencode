<!--
  PkmTodayView.vue — PKM Today 入口页 /pkm/today。

  - 顶部：今日 Daily Note 卡片（不存在则一键创建；点开即编辑）
  - 中部：最近笔记列表（点击跳转 /pkm/n/:id）
  - 右下 FAB：新建笔记
  - 搜索框：复用 pkm-store.searchNotes
-->
<template>
  <AppLayout>
    <div class="pkm-today">
      <!-- 今日 Daily Note -->
      <section class="daily-card" @click="openDaily">
        <div class="daily-date">
          <span class="day">{{ dayNum }}</span>
          <span class="ym">{{ yearMonth }}</span>
        </div>
        <div class="daily-body">
          <h2 class="daily-title">{{ dailyExists ? '继续今日笔记' : '今日 Daily Note' }}</h2>
          <p class="daily-hint">
            {{ dailyExists ? '点击进入编辑' : '点击创建 · 用 [[日期]] 互链' }}
          </p>
        </div>
      </section>

      <!-- 搜索 -->
      <div class="search-bar">
        <input
          v-model="query"
          placeholder="搜索笔记…"
          @keyup.enter="onSearch"
        />
      </div>

      <!-- 最近 / 搜索结果 -->
      <div v-if="loading" class="state">加载中…</div>
      <div v-else-if="notes.length === 0" class="state">
        <p>{{ query ? '无匹配笔记' : '还没有笔记，点右下角 + 新建' }}</p>
      </div>
      <ul v-else class="note-list">
        <li
          v-for="n in notes"
          :key="n.id"
          class="note-item"
          @click="openNote(n.id)"
        >
          <span class="n-title">{{ n.title || '无标题' }}</span>
          <span class="n-snippet">{{ snippet(n.html) }}</span>
          <span class="n-date">{{ formatTime(n.updatedAt) }}</span>
        </li>
      </ul>

      <!-- 新建 FAB -->
      <button class="fab" @click="newNote">＋</button>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import AppLayout from '../../app/AppLayout.vue'
import {
  listNotes,
  searchNotes,
  getDailyNote,
  getOrCreateDailyNote,
  dailyKey,
  type PkmNote,
} from './pkm-store'
import { useAuthStore } from '../../stores/auth'

const router = useRouter()
const auth = useAuthStore()
const workspaceId = auth.workspaceId || 'default'

const query = ref('')
const notes = ref<PkmNote[]>([])
const loading = ref(false)
const dailyExists = ref(false)

const todayKey = dailyKey()
const now = new Date()
const dayNum = String(now.getDate())
const yearMonth = `${now.getFullYear()}/${now.getMonth() + 1}`

async function loadRecent() {
  loading.value = true
  query.value = ''
  notes.value = await listNotes({ workspaceId, limit: 50 })
  dailyExists.value = !!(await getDailyNote(todayKey, workspaceId))
  loading.value = false
}

async function onSearch() {
  if (!query.value.trim()) {
    await loadRecent()
    return
  }
  loading.value = true
  notes.value = await searchNotes(query.value, { workspaceId })
  loading.value = false
}

function openNote(id: string) {
  router.push(`/pkm/n/${id}`)
}

function newNote() {
  router.push('/pkm/n/new')
}

async function openDaily() {
  const daily = await getOrCreateDailyNote(todayKey, workspaceId)
  router.push(`/pkm/n/${daily.id}`)
}

function snippet(html: string): string {
  const text = html.replace(/<[^>]+>/g, ' ').replace(/\s+/g, ' ').trim()
  return text.length > 60 ? text.slice(0, 60) + '…' : text || '空笔记'
}

function formatTime(ts: number): string {
  const d = new Date(ts)
  const sameYear = d.getFullYear() === now.getFullYear()
  return sameYear
    ? `${d.getMonth() + 1}/${d.getDate()}`
    : `${d.getFullYear()}/${d.getMonth() + 1}/${d.getDate()}`
}

onMounted(loadRecent)
</script>

<style scoped>
.pkm-today {
  padding: 16px 16px 100px;
}
.daily-card {
  display: flex;
  align-items: center;
  gap: 16px;
  background: var(--accent-soft, #eef2ff);
  border-radius: 14px;
  padding: 16px;
  margin-bottom: 16px;
  cursor: pointer;
}
.daily-card:active {
  opacity: 0.85;
}
.daily-date {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  background: var(--accent, #2563eb);
  color: #fff;
  border-radius: 12px;
  width: 56px;
  height: 56px;
  flex-shrink: 0;
}
.daily-date .day {
  font-size: 24px;
  font-weight: 800;
  line-height: 1;
}
.daily-date .ym {
  font-size: 9px;
  opacity: 0.85;
}
.daily-title {
  font-size: 16px;
  font-weight: 700;
  margin: 0 0 4px;
}
.daily-hint {
  font-size: 12px;
  color: var(--text-secondary, #666);
  margin: 0;
}
.search-bar {
  margin-bottom: 12px;
}
.search-bar input {
  width: 100%;
  padding: 10px 14px;
  border: 1px solid var(--border, #e5e7eb);
  border-radius: 10px;
  font-size: 14px;
  background: var(--bg-input, #fff);
}
.state {
  text-align: center;
  color: var(--text-secondary, #999);
  padding: 40px 0;
  font-size: 14px;
}
.note-list {
  list-style: none;
  margin: 0;
  padding: 0;
}
.note-item {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 12px 14px;
  border-bottom: 1px solid var(--border, #f0f0f0);
  cursor: pointer;
}
.note-item:active {
  background: var(--bg-hover, #f7f7f9);
}
.n-title {
  font-size: 15px;
  font-weight: 600;
}
.n-snippet {
  font-size: 12px;
  color: var(--text-secondary, #888);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.n-date {
  font-size: 11px;
  color: var(--text-tertiary, #aaa);
}
.fab {
  position: fixed;
  right: 20px;
  bottom: 90px;
  width: 52px;
  height: 52px;
  border-radius: 50%;
  background: var(--accent, #2563eb);
  color: #fff;
  font-size: 26px;
  border: none;
  box-shadow: 0 4px 14px rgba(37, 99, 235, 0.4);
  cursor: pointer;
}
.fab:active {
  transform: scale(0.95);
}
</style>
