<!--
  KnowledgeHubPage — 知识中心主页
  
  功能：
  - 三栏响应式布局（左：笔记本树+标签云，中：笔记列表，右：笔记链接）
  - 全局知识搜索
  - 笔记本管理
  - 标签过滤
  - 笔记创建和选择
  
  布局：
  - 移动端：单栏，通过导航切换
  - 平板：双栏（侧边栏+主内容）
  - 桌面：三栏（侧边栏+列表+详情）
-->
<template>
  <div class="knowledge-hub-page">
    <!-- 顶部搜索栏（全局） -->
    <div class="hub-header">
      <SearchBar
        v-model="searchQuery"
        :filters="searchFilters"
        :notebooks="notebooksStore.notebooks"
        :loading="searchStore.loading"
        @update:filters="handleFiltersUpdate"
        @search="handleSearch"
      />
    </div>

    <!-- 错误出口（2026-11-14 审计补：notebooks/search/links 读失败必须可见，
         error-outlet-audit 门禁登记见 stores/__tests__/error-outlet-audit.test.ts） -->
    <div
      v-if="notebooksStore.error || searchStore.error || noteLinksStore.error"
      class="hub-errors"
      role="alert"
    >
      <p v-if="notebooksStore.error">{{ notebooksStore.error }}</p>
      <p v-if="searchStore.error">{{ searchStore.error }}</p>
      <p v-if="noteLinksStore.error">{{ noteLinksStore.error }}</p>
    </div>

    <!-- 主内容区域 - 三栏布局 -->
    <div class="hub-content">
      <!-- 左侧栏：笔记本树 + 标签云 -->
      <aside class="hub-sidebar" :class="{ 'sidebar-visible': sidebarVisible }">
        <div class="sidebar-content">
          <!-- 笔记本树 -->
          <div class="sidebar-section notebooks-section">
            <NotebookTree
              :notebooks="notebooksStore.notebooks"
              :selected-id="selectedNotebookId"
              :loading="notebooksStore.loading"
              @select="handleNotebookSelect"
              @create="handleNotebookCreate"
              @update="handleNotebookUpdate"
              @delete="handleNotebookDelete"
            />
          </div>

          <!-- 标签云 -->
          <div class="sidebar-section tags-section">
            <TagCloud
              :tags="availableTags"
              :selected-tags="selectedTags"
              :loading="tagsLoading"
              @select="handleTagsSelect"
            />
          </div>
        </div>

        <!-- 移动端侧边栏遮罩 -->
        <div 
          v-if="sidebarVisible" 
          class="sidebar-overlay"
          @click="sidebarVisible = false"
        ></div>
      </aside>

      <!-- 中间栏：笔记列表 -->
      <main class="hub-main">
        <NoteList
          :notes="displayNotes"
          :loading="notesLoading"
          :selected-id="selectedNoteId"
          :title="listTitle"
          @select="handleNoteSelect"
          @create="handleNoteCreate"
        />
      </main>

      <!-- 右侧栏：笔记链接面板（暂时隐藏，未来实现） -->
      <aside class="hub-sidebar-right" v-if="showRightPanel">
        <div class="sidebar-right-content">
          <div class="panel-header">
            <h3 class="panel-title">{{ t('knowledge.relatedNotes') }}</h3>
            <button class="btn-close" @click="showRightPanel = false">
              <span class="material-symbols-outlined">close</span>
            </button>
          </div>
          
          <div class="panel-content">
            <div class="panel-empty">
              <span class="material-symbols-outlined empty-icon">link_off</span>
              <p>{{ t('knowledge.selectNoteToSeeLinks') }}</p>
            </div>
          </div>
        </div>
      </aside>
    </div>

    <!-- 移动端底部导航 -->
    <nav class="hub-bottom-nav" v-if="isMobile">
      <button
        class="nav-item"
        :class="{ active: activeTab === 'notebooks' }"
        @click="handleMobileTabChange('notebooks')"
      >
        <span class="material-symbols-outlined">folder</span>
        <span class="nav-label">{{ t('knowledge.notebooks') }}</span>
      </button>
      <button
        class="nav-item"
        :class="{ active: activeTab === 'notes' }"
        @click="handleMobileTabChange('notes')"
      >
        <span class="material-symbols-outlined">edit_note</span>
        <span class="nav-label">{{ t('knowledge.notes') }}</span>
      </button>
      <button
        class="nav-item"
        :class="{ active: activeTab === 'tags' }"
        @click="handleMobileTabChange('tags')"
      >
        <span class="material-symbols-outlined">label</span>
        <span class="nav-label">{{ t('knowledge.tags') }}</span>
      </button>
      <button
        class="nav-item"
        @click="handleNoteCreate"
      >
        <span class="material-symbols-outlined">add_circle</span>
        <span class="nav-label">{{ t('knowledge.new') }}</span>
      </button>
    </nav>

    <!-- 加载遮罩 -->
    <div v-if="initialLoading" class="loading-overlay">
      <div class="loading-spinner">
        <div class="spinner"></div>
        <p>{{ t('knowledge.loading') }}</p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import SearchBar from '../components/knowledge/SearchBar.vue'
import NotebookTree from '../components/knowledge/NotebookTree.vue'
import TagCloud from '../components/knowledge/TagCloud.vue'
import NoteList from '../components/knowledge/NoteList.vue'
import { useNotebooksStore } from '../stores/notebooks'
import { useKnowledgeSearchStore } from '../stores/knowledgeSearch'
import { useNoteLinksStore } from '../stores/noteLinks'
import type { Note, SearchFilters, Notebook, Tag } from '../types/knowledge'

const router = useRouter()
const { t } = useI18n()

// Stores
const notebooksStore = useNotebooksStore()
const searchStore = useKnowledgeSearchStore()
const noteLinksStore = useNoteLinksStore()

// 响应式状态
const searchQuery = ref('')
const searchFilters = ref<SearchFilters>({
  source: 'all',
  tags: []
})

const selectedNotebookId = ref<string>()
const selectedNoteId = ref<string>()
const selectedTags = ref<string[]>([])
const sidebarVisible = ref(false)
const showRightPanel = ref(false)
const activeTab = ref('notes')
const initialLoading = ref(true)
const notesLoading = ref(false)
const tagsLoading = ref(false)

// Mock notes data (replace with actual API calls)
const notes = ref<Note[]>([])

// 计算属性
const isMobile = computed(() => {
  if (typeof window === 'undefined') return false
  return window.innerWidth < 768
})

const isTablet = computed(() => {
  if (typeof window === 'undefined') return false
  return window.innerWidth >= 768 && window.innerWidth < 1024
})

const isDesktop = computed(() => {
  if (typeof window === 'undefined') return false
  return window.innerWidth >= 1024
})

// 显示的笔记列表（根据搜索和过滤）
const displayNotes = computed(() => {
  let filteredNotes = [...notes.value]

  // 按笔记本过滤
  if (selectedNotebookId.value) {
    filteredNotes = filteredNotes.filter(
      note => note.notebookId === selectedNotebookId.value
    )
  }

  // 按标签过滤
  if (selectedTags.value.length > 0) {
    filteredNotes = filteredNotes.filter(note =>
      note.tags?.some(tag => selectedTags.value.includes(tag))
    )
  }

  // 搜索结果
  if (searchQuery.value.trim()) {
    const query = searchQuery.value.toLowerCase()
    filteredNotes = filteredNotes.filter(note =>
      note.title?.toLowerCase().includes(query) ||
      note.content?.toLowerCase().includes(query) ||
      note.summary?.toLowerCase().includes(query)
    )
  }

  return filteredNotes
})

// 列表标题
const listTitle = computed(() => {
  if (searchQuery.value.trim()) {
    return t('knowledge.searchResults', { count: displayNotes.value.length })
  }
  
  if (selectedNotebookId.value) {
    const notebook = notebooksStore.notebooks.find(
      nb => nb.id === selectedNotebookId.value
    )
    return notebook?.name || t('knowledge.notes')
  }

  if (selectedTags.value.length > 0) {
    return t('knowledge.taggedNotes', { tags: selectedTags.value.join(', ') })
  }

  return t('knowledge.allNotes')
})

// 可用标签（从笔记中提取）
const availableTags = computed(() => {
  const tagMap = new Map<string, number>()

  notes.value.forEach(note => {
    note.tags?.forEach(tag => {
      tagMap.set(tag, (tagMap.get(tag) || 0) + 1)
    })
  })

  const tags: Tag[] = Array.from(tagMap.entries()).map(([name, count]) => ({
    name,
    count
  }))

  return tags.sort((a, b) => b.count - a.count)
})

// 事件处理
function handleSearch() {
  if (!searchQuery.value.trim()) {
    // 清空搜索，显示所有笔记
    return
  }

  // 执行搜索
  searchStore.search(searchQuery.value, {
    ...searchFilters.value,
    tags: searchFilters.value.tags
  })
}

function handleFiltersUpdate(filters: SearchFilters) {
  searchFilters.value = filters
}

function handleNotebookSelect(notebookId: string) {
  selectedNotebookId.value = notebookId
  selectedNoteId.value = undefined
  
  // 加载该笔记本的笔记
  loadNotebookNotes(notebookId)
  
  // 移动端自动切换到笔记列表
  if (isMobile.value) {
    activeTab.value = 'notes'
    sidebarVisible.value = false
  }
}

function handleNotebookCreate(parentId: string | null) {
  const name = prompt(t('knowledge.enterNotebookName'))
  if (name && name.trim()) {
    notebooksStore.createNotebook({
      name: name.trim(),
      parentId: parentId || undefined
    })
  }
}

function handleNotebookUpdate(notebookId: string, data: Partial<Notebook>) {
  notebooksStore.updateNotebook(notebookId, data)
}

function handleNotebookDelete(notebookId: string) {
  notebooksStore.deleteNotebook(notebookId)
  
  // 如果删除的是当前选中的笔记本，清空选择
  if (selectedNotebookId.value === notebookId) {
    selectedNotebookId.value = undefined
  }
}

function handleTagsSelect(tags: string[]) {
  selectedTags.value = tags
}

function handleNoteSelect(noteId: string) {
  selectedNoteId.value = noteId
  
  // 加载笔记链接
  noteLinksStore.fetchRelatedNotes(noteId)
  
  // 桌面端显示右侧面板
  if (isDesktop.value) {
    showRightPanel.value = true
  }
  
  // 导航到笔记详情页
  router.push(`/notes/${noteId}`)
}

function handleNoteCreate() {
  router.push('/notes/new')
}

function handleMobileTabChange(tab: string) {
  activeTab.value = tab
  
  if (tab === 'notebooks' || tab === 'tags') {
    sidebarVisible.value = true
  } else {
    sidebarVisible.value = false
  }
}

// 加载笔记本的笔记
async function loadNotebookNotes(notebookId: string) {
  notesLoading.value = true
  try {
    // TODO: Replace with actual API call
    // const response = await notesApi.getNotebookNotes(notebookId)
    // notes.value = response.notes
    
    // Mock data for now
    await new Promise(resolve => setTimeout(resolve, 500))
    console.log('Loading notes for notebook:', notebookId)
  } catch (error) {
    console.error('Failed to load notebook notes:', error)
  } finally {
    notesLoading.value = false
  }
}

// 加载所有笔记
async function loadAllNotes() {
  notesLoading.value = true
  try {
    // TODO: Replace with actual API call
    // const response = await notesApi.getAllNotes()
    // notes.value = response.notes
    
    // Mock data for now
    await new Promise(resolve => setTimeout(resolve, 800))
    notes.value = generateMockNotes()
  } catch (error) {
    console.error('Failed to load notes:', error)
  } finally {
    notesLoading.value = false
  }
}

// 生成模拟数据
function generateMockNotes(): Note[] {
  return [
    {
      id: 'note-1',
      userId: 'local',
      notebookId: notebooksStore.notebooks[0]?.id,
      title: 'Q3 预算讨论',
      content: '今天和客户讨论了 Q3 预算，需要在周五前确认...',
      summary: '今天和客户讨论了 Q3 预算，需要在周五前确认分配方案。',
      contentType: 'mixed',
      domain: 'work',
      tags: ['预算', '客户', 'Q3'],
      source: 'voice',
      isLatest: true,
      isPinned: true,
      createdAt: Date.now() - 3600000,
      updatedAt: Date.now() - 1800000
    },
    {
      id: 'note-2',
      userId: 'local',
      title: '会议纪要：技术方案评审',
      content: '讨论了新功能的技术实现方案...',
      summary: '讨论了新功能的技术实现方案，决定采用微服务架构。',
      contentType: 'text',
      domain: 'work',
      tags: ['技术', '评审', '会议'],
      source: 'meeting',
      isLatest: true,
      createdAt: Date.now() - 86400000,
      updatedAt: Date.now() - 86400000
    },
    {
      id: 'note-3',
      userId: 'local',
      title: '读书笔记：原子习惯',
      content: '习惯是自我提升的复利...',
      summary: '习惯的力量在于复利效应，每天进步1%。',
      contentType: 'text',
      domain: 'study',
      tags: ['读书', '习惯', '成长'],
      source: 'manual',
      isLatest: true,
      createdAt: Date.now() - 172800000,
      updatedAt: Date.now() - 172800000
    }
  ]
}

// 初始化
onMounted(async () => {
  initialLoading.value = true
  
  try {
    // 加载笔记本列表
    await notebooksStore.fetchNotebooks()
    
    // 加载所有笔记
    await loadAllNotes()
    
    // 如果有查询参数，执行搜索
    const query = router.currentRoute.value.query.q as string
    if (query) {
      searchQuery.value = query
      handleSearch()
    }
  } catch (error) {
    console.error('Failed to initialize knowledge hub:', error)
  } finally {
    initialLoading.value = false
  }
})

// 监听窗口大小变化
if (typeof window !== 'undefined') {
  window.addEventListener('resize', () => {
    // 桌面端自动显示侧边栏
    if (isDesktop.value) {
      sidebarVisible.value = false
    }
  })
}
</script>

<script lang="ts">
export default {
  name: 'KnowledgeHubPage'
}
</script>

<style scoped>
.knowledge-hub-page {
  display: flex;
  flex-direction: column;
  height: 100vh;
  height: 100dvh;
  background: var(--bg-base);
  overflow: hidden;
}

/* 错误出口横幅 */
.hub-errors {
  flex-shrink: 0;
  padding: var(--space-2) var(--space-4);
  background: var(--danger-bg);
  border-bottom: 1px solid var(--danger);
}

.hub-errors p {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--danger);
}

/* 顶部搜索栏 */
.hub-header {
  flex-shrink: 0;
  padding: var(--space-3) var(--space-4);
  background: var(--bg-base);
  border-bottom: 1px solid var(--border);
  z-index: var(--z-hub-float);
}

/* 主内容区域 */
.hub-content {
  flex: 1;
  display: flex;
  overflow: hidden;
  position: relative;
}

/* 左侧栏 */
.hub-sidebar {
  width: 280px;
  flex-shrink: 0;
  background: var(--bg-base);
  border-right: 1px solid var(--border);
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

.sidebar-content {
  flex: 1;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding: var(--space-3);
}

.sidebar-section {
  flex-shrink: 0;
}

.notebooks-section {
  flex: 1;
  min-height: 300px;
}

.tags-section {
  flex: 0 0 auto;
  max-height: 400px;
}

/* 中间主内容 */
.hub-main {
  flex: 1;
  overflow: hidden;
  background: var(--bg-secondary);
}

/* 右侧栏 */
.hub-sidebar-right {
  width: 320px;
  flex-shrink: 0;
  background: var(--bg-base);
  border-left: 1px solid var(--border);
  overflow: hidden;
}

.sidebar-right-content {
  height: 100%;
  display: flex;
  flex-direction: column;
}

.panel-header {
  padding: var(--space-4);
  border-bottom: 1px solid var(--border);
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.panel-title {
  font-size: var(--text-base);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
  margin: 0;
}

.btn-close {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border: none;
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.btn-close:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.panel-content {
  flex: 1;
  overflow-y: auto;
  padding: var(--space-4);
}

.panel-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: var(--space-8) var(--space-4);
  text-align: center;
  color: var(--text-muted);
}

.empty-icon {
  font-size: 48px;
  color: var(--text-tertiary);
  margin-bottom: var(--space-3);
}

.panel-empty p {
  margin: 0;
  font-size: var(--text-sm);
}

/* 移动端底部导航 */
.hub-bottom-nav {
  display: none;
  position: fixed;
  bottom: var(--bottom-chrome-height);
  left: 0;
  right: 0;
  height: 64px;
  background: var(--bg-card);
  border-top: 1px solid var(--border);
  padding: var(--space-2) var(--space-4);
  z-index: var(--z-hub-float);
}

.nav-item {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 2px;
  border: none;
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  transition: color var(--duration-fast) var(--ease-out);
}

.nav-item:hover,
.nav-item.active {
  color: var(--brand-primary);
}

.nav-item .material-symbols-outlined {
  font-size: 24px;
}

.nav-label {
  font-size: var(--text-2xs);
  font-weight: var(--font-weight-medium);
}

/* 加载遮罩 */
.loading-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: var(--z-menu);
}

.loading-spinner {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-6);
  background: var(--bg-card);
  border-radius: var(--radius-lg);
}

.spinner {
  width: 40px;
  height: 40px;
  border: 3px solid var(--border);
  border-top-color: var(--brand-primary);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

.loading-spinner p {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--text-secondary);
}

/* 响应式：移动端 */
@media (max-width: 767px) {
  .hub-content {
    padding-bottom: 64px;
  }

  .hub-sidebar {
    position: fixed;
    top: 0;
    left: 0;
    bottom: 0;
    width: 80%;
    max-width: 320px;
    transform: translateX(-100%);
    transition: transform var(--duration-normal) var(--ease-out);
    z-index: var(--z-hub-drawer);
    box-shadow: var(--shadow-lg);
  }

  .hub-sidebar.sidebar-visible {
    transform: translateX(0);
  }

  .sidebar-overlay {
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    background: rgba(0, 0, 0, 0.5);
    z-index: var(--z-hub-drawer-overlay);
  }

  .hub-sidebar-right {
    display: none;
  }

  .hub-bottom-nav {
    display: flex;
  }
}

/* 响应式：平板 */
@media (min-width: 768px) and (max-width: 1023px) {
  .hub-sidebar {
    width: 240px;
  }

  .hub-sidebar-right {
    display: none;
  }
}

/* 响应式：桌面 */
@media (min-width: 1024px) {
  .hub-sidebar {
    width: 280px;
  }

  .hub-sidebar-right {
    width: 320px;
  }
}

/* 超大屏幕 */
@media (min-width: 1440px) {
  .hub-sidebar {
    width: 320px;
  }

  .hub-sidebar-right {
    width: 360px;
  }
}
</style>
