<!--
  NotebookDetailPage — 笔记本详情页面
  
  功能：
  - 显示笔记本信息（名称、图标、描述）
  - 列出该笔记本的所有笔记
  - 支持编辑笔记本属性
  - 支持创建新笔记
  - 支持笔记排序和过滤
  
  路由：/notebooks/:id
-->
<template>
  <ion-page>
    <ion-header>
      <ion-toolbar>
        <ion-buttons slot="start">
          <ion-back-button default-href="/knowledge"></ion-back-button>
        </ion-buttons>
        <ion-title>{{ notebook?.name || t('knowledge.notebook') }}</ion-title>
        <ion-buttons slot="end">
          <ion-button @click="handleEdit">
            <span class="material-symbols-outlined">edit</span>
          </ion-button>
        </ion-buttons>
      </ion-toolbar>
    </ion-header>

    <ion-content :fullscreen="true">
      <div class="notebook-detail-page">
        <template v-if="loading">
          <div class="loading-container">
            <ion-spinner></ion-spinner>
            <p>{{ t('knowledge.loading') }}</p>
          </div>
        </template>

        <template v-else-if="!notebook">
          <div class="error-container">
            <span class="material-symbols-outlined error-icon">folder_off</span>
            <h3>{{ t('knowledge.notebookNotFound') }}</h3>
            <ion-button @click="router.push('/knowledge')">
              {{ t('knowledge.backToHub') }}
            </ion-button>
          </div>
        </template>

        <template v-else>
          <!-- 笔记本信息卡片 -->
          <div class="notebook-info-card">
            <div class="notebook-header">
              <div class="notebook-icon" :style="{ backgroundColor: notebook.color || '#6366f1' }">
                <span class="material-symbols-outlined">
                  {{ notebook.icon || 'folder' }}
                </span>
              </div>
              <div class="notebook-meta">
                <h1 class="notebook-name">{{ notebook.name }}</h1>
                <p class="notebook-stats">
                  {{ t('knowledge.noteCount', { count: notes.length }) }} · 
                  {{ t('knowledge.updatedAt', { time: formatDate(notebook.updatedAt) }) }}
                </p>
              </div>
            </div>

            <!-- 面包屑导航（如果有父笔记本） -->
            <div v-if="breadcrumbs.length > 0" class="breadcrumbs">
              <button
                v-for="(crumb, index) in breadcrumbs"
                :key="crumb.id"
                class="breadcrumb-item"
                @click="router.push(`/notebooks/${crumb.id}`)"
              >
                {{ crumb.name }}
                <span v-if="index < breadcrumbs.length - 1" class="breadcrumb-separator">/</span>
              </button>
            </div>

            <!-- 子笔记本列表 -->
            <div v-if="childNotebooks.length > 0" class="child-notebooks">
              <h3 class="section-title">{{ t('knowledge.subNotebooks') }}</h3>
              <div class="notebook-grid">
                <button
                  v-for="child in childNotebooks"
                  :key="child.id"
                  class="notebook-card"
                  @click="router.push(`/notebooks/${child.id}`)"
                >
                  <div class="card-icon" :style="{ backgroundColor: child.color || '#6366f1' }">
                    <span class="material-symbols-outlined">{{ child.icon || 'folder' }}</span>
                  </div>
                  <div class="card-info">
                    <p class="card-name">{{ child.name }}</p>
                    <p class="card-count">{{ child.noteCount || 0 }} {{ t('knowledge.notes') }}</p>
                  </div>
                </button>
              </div>
            </div>
          </div>

          <!-- 笔记列表 -->
          <div class="notes-section">
            <NoteList
              :notes="notes"
              :loading="notesLoading"
              :selected-id="selectedNoteId"
              :title="t('knowledge.notesInNotebook')"
              @select="handleNoteSelect"
              @create="handleNoteCreate"
            />
          </div>
        </template>
      </div>
    </ion-content>
  </ion-page>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
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
  IonSpinner,
  alertController
} from '@ionic/vue'
import NoteList from '../components/knowledge/NoteList.vue'
import { useNotebooksStore } from '../stores/notebooks'
import type { Note, Notebook } from '../types/knowledge'

const router = useRouter()
const route = useRoute()
const { t } = useI18n()

const notebooksStore = useNotebooksStore()

const loading = ref(true)
const notesLoading = ref(false)
const notes = ref<Note[]>([])
const selectedNoteId = ref<string>()

const notebookId = computed(() => route.params.id as string)

const notebook = computed(() => {
  return notebooksStore.notebookById(notebookId.value)
})

// 子笔记本
const childNotebooks = computed(() => {
  return notebooksStore.notebooks.filter(nb => nb.parentId === notebookId.value)
})

// 面包屑导航
const breadcrumbs = computed(() => {
  const crumbs: Notebook[] = []
  let current = notebook.value
  
  while (current?.parentId) {
    const parent = notebooksStore.notebookById(current.parentId)
    if (parent) {
      crumbs.unshift(parent)
      current = parent
    } else {
      break
    }
  }
  
  return crumbs
})

async function loadNotebookNotes() {
  notesLoading.value = true
  try {
    const response = await fetch(`/api/notebooks/${notebookId.value}/notes`)
    if (!response.ok) {
      throw new Error('Failed to load notes')
    }
    const data = await response.json()
    notes.value = Array.isArray(data) ? data : data.notes || []
  } catch (error) {
    console.error('Failed to load notebook notes:', error)
    notes.value = []
  } finally {
    notesLoading.value = false
  }
}

async function handleEdit() {
  const alert = await alertController.create({
    header: t('knowledge.editNotebook'),
    inputs: [
      {
        name: 'name',
        type: 'text',
        placeholder: t('knowledge.notebookName'),
        value: notebook.value?.name
      }
    ],
    buttons: [
      {
        text: t('common.cancel'),
        role: 'cancel'
      },
      {
        text: t('common.save'),
        handler: async (data) => {
          if (data.name && data.name.trim()) {
            try {
              await notebooksStore.updateNotebook(notebookId.value, {
                name: data.name.trim()
              })
            } catch (error) {
              console.error('Failed to update notebook:', error)
            }
          }
        }
      }
    ]
  })
  
  await alert.present()
}

function handleNoteSelect(noteId: string) {
  selectedNoteId.value = noteId
  router.push(`/notes/${noteId}`)
}

function handleNoteCreate() {
  router.push({
    path: '/notes/new',
    query: { notebookId: notebookId.value }
  })
}

function formatDate(timestamp: number): string {
  const date = new Date(timestamp)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffDays = Math.floor(diffMs / 86400000)
  
  if (diffDays === 0) return t('knowledge.today')
  if (diffDays === 1) return t('knowledge.yesterday')
  if (diffDays < 7) return t('knowledge.daysAgo', { n: diffDays })
  
  return date.toLocaleDateString()
}

onMounted(async () => {
  loading.value = true
  try {
    // 加载笔记本列表（如果还没加载）
    if (notebooksStore.notebooks.length === 0) {
      await notebooksStore.fetchNotebooks()
    }
    
    // 加载笔记本的笔记
    await loadNotebookNotes()
  } catch (error) {
    console.error('Failed to load notebook:', error)
  } finally {
    loading.value = false
  }
})

// 监听路由变化，重新加载笔记
watch(() => route.params.id, async (newId) => {
  if (newId) {
    await loadNotebookNotes()
  }
})
</script>

<script lang="ts">
export default {
  name: 'NotebookDetailPage'
}
</script>

<style scoped>
.notebook-detail-page {
  min-height: 100%;
  background: var(--bg-base);
}

.loading-container,
.error-container {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: var(--space-12);
  gap: var(--space-3);
  text-align: center;
}

.error-icon {
  font-size: 64px;
  color: var(--text-tertiary);
}

.error-container h3 {
  margin: 0;
  font-size: var(--text-lg);
  color: var(--text-primary);
}

.notebook-info-card {
  padding: var(--space-6) var(--space-4);
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
}

.notebook-header {
  display: flex;
  align-items: flex-start;
  gap: var(--space-4);
  margin-bottom: var(--space-4);
}

.notebook-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 64px;
  height: 64px;
  border-radius: var(--radius-lg);
  flex-shrink: 0;
}

.notebook-icon .material-symbols-outlined {
  font-size: 32px;
  color: white;
}

.notebook-meta {
  flex: 1;
}

.notebook-name {
  margin: 0 0 var(--space-1);
  font-size: var(--text-2xl);
  font-weight: var(--font-weight-bold);
  color: var(--text-primary);
}

.notebook-stats {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--text-secondary);
}

.breadcrumbs {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  margin-bottom: var(--space-4);
  padding: var(--space-2) 0;
  overflow-x: auto;
}

.breadcrumb-item {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-1) var(--space-2);
  border: none;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text-secondary);
  font-size: var(--text-sm);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
  white-space: nowrap;
}

.breadcrumb-item:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.breadcrumb-separator {
  margin-left: var(--space-1);
  color: var(--text-tertiary);
}

.child-notebooks {
  margin-top: var(--space-4);
}

.section-title {
  margin: 0 0 var(--space-3);
  font-size: var(--text-base);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
}

.notebook-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  gap: var(--space-3);
}

.notebook-card {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-4);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-base);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
  text-align: center;
}

.notebook-card:hover {
  border-color: var(--brand-primary);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.08);
  transform: translateY(-2px);
}

.card-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 48px;
  height: 48px;
  border-radius: var(--radius-md);
}

.card-icon .material-symbols-outlined {
  font-size: 24px;
  color: white;
}

.card-info {
  width: 100%;
}

.card-name {
  margin: 0 0 var(--space-1);
  font-size: var(--text-sm);
  font-weight: var(--font-weight-medium);
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.card-count {
  margin: 0;
  font-size: var(--text-xs);
  color: var(--text-secondary);
}

.notes-section {
  padding: var(--space-4);
}
</style>
