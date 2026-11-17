<!--
  KnowledgeSearchPage — 知识搜索结果页面
  
  功能：
  - 显示统一搜索结果（笔记、记忆、邮件、会议）
  - 支持高级过滤（来源、笔记本、标签、日期范围）
  - 按来源分组显示结果
  - 加载更多（分页）
  
  使用场景：
  - 从 KnowledgeHubPage 搜索后跳转
  - 全局搜索入口
-->
<template>
  <ion-page>
    <ion-header>
      <ion-toolbar>
        <ion-buttons slot="start">
          <ion-back-button default-href="/knowledge"></ion-back-button>
        </ion-buttons>
        <ion-title>{{ t('knowledge.searchResults') }}</ion-title>
      </ion-toolbar>
    </ion-header>

    <ion-content :fullscreen="true">
      <div class="search-page">
        <!-- 搜索栏 -->
        <div class="search-header">
          <SearchBar
            v-model="searchQuery"
            :filters="searchFilters"
            :notebooks="notebooksStore.notebooks"
            :loading="searchStore.loading"
            @update:filters="handleFiltersUpdate"
            @search="handleSearch"
          />
        </div>

        <!-- 搜索统计 -->
        <div v-if="searchStore.query && !searchStore.loading" class="search-stats">
          <p class="stats-text">
            {{ t('knowledge.searchResultsCount', { 
              count: searchStore.total,
              time: searchStore.searchTime 
            }) }}
          </p>
          <button
            v-if="searchStore.hasActiveFilters"
            class="btn-clear-filters"
            @click="handleClearFilters"
          >
            <span class="material-symbols-outlined">filter_alt_off</span>
            {{ t('knowledge.clearFilters') }}
          </button>
        </div>

        <!-- 搜索结果 -->
        <div class="search-results">
          <template v-if="searchStore.loading && searchStore.results.length === 0">
            <div class="loading-container">
              <ion-spinner></ion-spinner>
              <p>{{ t('knowledge.searching') }}</p>
            </div>
          </template>

          <template v-else-if="searchStore.results.length === 0 && searchStore.query">
            <div class="empty-results">
              <span class="material-symbols-outlined empty-icon">search_off</span>
              <h3>{{ t('knowledge.noResults') }}</h3>
              <p>{{ t('knowledge.tryDifferentKeywords') }}</p>
            </div>
          </template>

          <template v-else-if="searchStore.results.length > 0">
            <!-- 按来源分组显示 -->
            <div class="results-by-source">
              <div
                v-for="(results, source) in groupedResults"
                :key="source"
                v-show="results.length > 0"
                class="source-group"
              >
                <h3 class="source-title">
                  <span class="material-symbols-outlined">{{ getSourceIcon(source) }}</span>
                  {{ getSourceLabel(source) }}
                  <span class="source-count">{{ results.length }}</span>
                </h3>

                <div class="result-list">
                  <article
                    v-for="result in results"
                    :key="result.id"
                    class="result-item"
                    @click="handleResultClick(result)"
                  >
                    <div class="result-header">
                      <h4 class="result-title">{{ result.title }}</h4>
                      <span class="result-score">{{ Math.round(result.score * 100) }}%</span>
                    </div>

                    <p class="result-snippet" v-html="highlightText(result.snippet)"></p>

                    <div class="result-meta">
                      <div v-if="result.tags && result.tags.length > 0" class="result-tags">
                        <span v-for="tag in result.tags.slice(0, 3)" :key="tag" class="tag">
                          #{{ tag }}
                        </span>
                      </div>
                      <time class="result-time">{{ formatDate(result.createdAt) }}</time>
                    </div>
                  </article>
                </div>
              </div>
            </div>

            <!-- 加载更多 -->
            <div v-if="hasMore" class="load-more-container">
              <ion-button
                expand="block"
                fill="outline"
                :disabled="searchStore.loading"
                @click="handleLoadMore"
              >
                <span v-if="!searchStore.loading">{{ t('knowledge.loadMore') }}</span>
                <ion-spinner v-else name="crescent"></ion-spinner>
              </ion-button>
            </div>
          </template>
        </div>
      </div>
    </ion-content>
  </ion-page>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  IonPage,
  IonHeader,
  IonToolbar,
  IonTitle,
  IonContent,
  IonButtons,
  IonBackButton,
  IonButton,
  IonSpinner
} from '@ionic/vue'
import SearchBar from '../components/knowledge/SearchBar.vue'
import { useNotebooksStore } from '../stores/notebooks'
import { useKnowledgeSearchStore } from '../stores/knowledgeSearch'
import type { SearchFilters, KnowledgeSearchResult } from '../types/knowledge'

const router = useRouter()
const route = useRoute()
const { t } = useI18n()

const notebooksStore = useNotebooksStore()
const searchStore = useKnowledgeSearchStore()

const searchQuery = ref('')
const searchFilters = ref<SearchFilters>({
  source: 'all',
  tags: []
})

const groupedResults = computed(() => {
  const grouped: Record<string, KnowledgeSearchResult[]> = {
    local: [],
    memora: [],
    email: [],
    meeting: []
  }

  searchStore.results.forEach(result => {
    if (grouped[result.source]) {
      grouped[result.source].push(result)
    }
  })

  return grouped
})

const hasMore = computed(() => {
  return searchStore.results.length < searchStore.total
})

function handleSearch() {
  if (searchQuery.value.trim()) {
    searchStore.search(searchQuery.value, searchFilters.value)
  }
}

function handleFiltersUpdate(filters: SearchFilters) {
  searchFilters.value = filters
  if (searchQuery.value.trim()) {
    handleSearch()
  }
}

function handleClearFilters() {
  searchFilters.value = {
    source: 'all',
    tags: []
  }
  handleSearch()
}

async function handleLoadMore() {
  await searchStore.loadMore()
}

function handleResultClick(result: KnowledgeSearchResult) {
  if (result.type === 'note') {
    router.push(`/notes/${result.id}`)
  }
}

function getSourceIcon(source: string): string {
  const iconMap: Record<string, string> = {
    local: 'description',
    memora: 'psychology',
    email: 'mail',
    meeting: 'event'
  }
  return iconMap[source] || 'description'
}

function getSourceLabel(source: string): string {
  const labelMap: Record<string, string> = {
    local: t('knowledge.localNotes'),
    memora: t('knowledge.memoraMemories'),
    email: t('knowledge.emails'),
    meeting: t('knowledge.meetings')
  }
  return labelMap[source] || source
}

function highlightText(text: string): string {
  const escapedText = text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
  const query = searchQuery.value.trim()
  if (!query) return escapedText
  const escapedQuery = query.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const regex = new RegExp(`(${escapedQuery})`, 'gi')
  return escapedText.replace(regex, '<mark>$1</mark>')
}

function formatDate(dateStr: string): string {
  const date = new Date(dateStr)
  return date.toLocaleDateString()
}

onMounted(async () => {
  // 加载笔记本列表（用于过滤）
  if (notebooksStore.notebooks.length === 0) {
    await notebooksStore.fetchNotebooks()
  }

  // 从 URL 参数获取搜索关键词
  const query = route.query.q as string
  if (query) {
    searchQuery.value = query
    handleSearch()
  }
})
</script>

<script lang="ts">
export default {
  name: 'KnowledgeSearchPage'
}
</script>

<style scoped>
.search-page {
  min-height: 100%;
  background: var(--bg-base);
}

.search-header {
  padding: var(--space-4);
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
  position: sticky;
  top: 0;
  z-index: var(--z-base);
}

.search-stats {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-3) var(--space-4);
  background: var(--bg-secondary);
  border-bottom: 1px solid var(--border);
}

.stats-text {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--text-secondary);
}

.btn-clear-filters {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-card);
  color: var(--text-primary);
  font-size: var(--text-sm);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.btn-clear-filters:hover {
  background: var(--bg-hover);
  border-color: var(--brand-primary);
}

.btn-clear-filters .material-symbols-outlined {
  font-size: var(--text-xl);
}

.search-results {
  padding: var(--space-4);
}

.loading-container {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: var(--space-12);
  gap: var(--space-3);
}

.loading-container p {
  margin: 0;
  color: var(--text-secondary);
}

.empty-results {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: var(--space-12) var(--space-4);
  text-align: center;
}

.empty-icon {
  font-size: 64px;
  color: var(--text-tertiary);
  margin-bottom: var(--space-4);
}

.empty-results h3 {
  margin: 0 0 var(--space-2);
  font-size: var(--text-lg);
  color: var(--text-primary);
}

.empty-results p {
  margin: 0;
  font-size: var(--text-base);
  color: var(--text-secondary);
}

.results-by-source {
  display: flex;
  flex-direction: column;
  gap: var(--space-6);
}

.source-group {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.source-title {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin: 0;
  font-size: var(--text-base);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
}

.source-title .material-symbols-outlined {
  font-size: 20px;
  color: var(--brand-primary);
}

.source-count {
  margin-left: auto;
  padding: 2px 8px;
  border-radius: var(--radius-full);
  background: var(--bg-secondary);
  font-size: var(--text-xs);
  font-weight: var(--font-weight-medium);
  color: var(--text-secondary);
}

.result-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.result-item {
  padding: var(--space-4);
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.result-item:hover {
  border-color: var(--brand-primary);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.08);
  transform: translateY(-1px);
}

.result-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
  margin-bottom: var(--space-2);
}

.result-title {
  flex: 1;
  margin: 0;
  font-size: var(--text-base);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
}

.result-score {
  flex-shrink: 0;
  padding: 2px 8px;
  border-radius: var(--radius-sm);
  background: var(--brand-bg);
  color: var(--brand-primary);
  font-size: var(--text-xs);
  font-weight: var(--font-weight-medium);
}

.result-snippet {
  margin: 0 0 var(--space-3);
  font-size: var(--text-sm);
  color: var(--text-secondary);
  line-height: 1.6;
}

.result-snippet :deep(mark) {
  background: var(--brand-bg);
  color: var(--brand-primary);
  font-weight: var(--font-weight-medium);
  padding: 0 2px;
  border-radius: 2px;
}

.result-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.result-tags {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.tag {
  padding: 2px 8px;
  border-radius: var(--radius-full);
  background: var(--bg-secondary);
  color: var(--brand-primary);
  font-size: var(--text-xs);
  font-weight: var(--font-weight-medium);
}

.result-time {
  font-size: var(--text-xs);
  color: var(--text-tertiary);
}

.load-more-container {
  margin-top: var(--space-4);
}
</style>
