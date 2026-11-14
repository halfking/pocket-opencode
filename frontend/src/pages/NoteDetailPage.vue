<!--
  NoteDetailPage — 笔记详情页面
  
  功能：
  - 显示笔记完整内容（Markdown 渲染）
  - 显示笔记元信息（标签、来源、时间）
  - 显示笔记链接关系（右侧面板或底部）
  - 支持编辑笔记
  - 支持删除笔记
  - 支持固定/取消固定
  
  路由：/notes/:id
-->
<template>
  <ion-page>
    <ion-header>
      <ion-toolbar>
        <ion-buttons slot="start">
          <ion-back-button default-href="/knowledge"></ion-back-button>
        </ion-buttons>
        <ion-title>{{ note?.title || t('knowledge.noteDetail') }}</ion-title>
        <ion-buttons slot="end">
          <ion-button @click="handleEdit">
            <span class="material-symbols-outlined">edit</span>
          </ion-button>
          <ion-button @click="handleMore">
            <span class="material-symbols-outlined">more_vert</span>
          </ion-button>
        </ion-buttons>
      </ion-toolbar>
    </ion-header>

    <ion-content :fullscreen="true">
      <div class="note-detail-page">
        <template v-if="loading">
          <div class="loading-container">
            <ion-spinner></ion-spinner>
            <p>{{ t('knowledge.loading') }}</p>
          </div>
        </template>

        <template v-else-if="!note">
          <div class="error-container">
            <span class="material-symbols-outlined error-icon">note_off</span>
            <h3>{{ t('knowledge.noteNotFound') }}</h3>
            <ion-button @click="router.push('/knowledge')">
              {{ t('knowledge.backToHub') }}
            </ion-button>
          </div>
        </template>

        <template v-else>
          <div class="note-content-wrapper">
            <!-- 笔记主内容 -->
            <article class="note-article">
              <!-- 笔记头部 -->
              <header class="note-header">
                <h1 class="note-title">
                  {{ note.title || t('knowledge.untitled') }}
                </h1>
                
                <div class="note-meta">
                  <div class="meta-row">
                    <span v-if="note.source" class="meta-item source">
                      <span class="material-symbols-outlined">{{ getSourceIcon(note.source) }}</span>
                      <span>{{ note.source }}</span>
                    </span>
                    <span v-if="note.domain" class="meta-item domain">
                      <span class="material-symbols-outlined">category</span>
                      <span>{{ note.domain }}</span>
                    </span>
                    <span v-if="note.isPinned" class="meta-item pinned">
                      <span class="material-symbols-outlined">push_pin</span>
                      <span>{{ t('knowledge.pinned') }}</span>
                    </span>
                  </div>
                  
                  <div class="meta-row">
                    <time class="meta-item time">
                      <span class="material-symbols-outlined">schedule</span>
                      <span>{{ t('knowledge.createdAt') }}: {{ formatDate(note.createdAt) }}</span>
                    </time>
                    <time v-if="note.updatedAt !== note.createdAt" class="meta-item time">
                      <span class="material-symbols-outlined">update</span>
                      <span>{{ t('knowledge.updatedAt') }}: {{ formatDate(note.updatedAt) }}</span>
                    </time>
                  </div>

                  <div v-if="note.tags && note.tags.length > 0" class="note-tags">
                    <span v-for="tag in note.tags" :key="tag" class="tag">
                      #{{ tag }}
                    </span>
                  </div>
                </div>
              </header>

              <!-- 笔记摘要 -->
              <div v-if="note.summary" class="note-summary">
                <p>{{ note.summary }}</p>
              </div>

              <!-- 笔记内容 -->
              <div class="note-body">
                <div v-if="note.contentType === 'markdown'" class="markdown-content" v-html="renderedContent"></div>
                <div v-else class="text-content">{{ note.content }}</div>
              </div>
            </article>

            <!-- 笔记链接面板 -->
            <aside class="note-links-panel">
              <h3 class="panel-title">
                <span class="material-symbols-outlined">link</span>
                {{ t('knowledge.relatedNotes') }}
              </h3>

              <template v-if="noteLinksStore.loading">
                <div class="panel-loading">
                  <ion-spinner name="crescent"></ion-spinner>
                </div>
              </template>

              <template v-else-if="relatedNotes">
                <!-- 手动链接 -->
                <div v-if="relatedNotes.manualLinks.length > 0" class="links-section">
                  <h4 class="section-subtitle">{{ t('knowledge.manualLinks') }}</h4>
                  <div class="link-list">
                    <button
                      v-for="link in relatedNotes.manualLinks"
                      :key="link.id"
                      class="link-item"
                      @click="handleLinkClick(link.targetNoteId)"
                    >
                      <span class="material-symbols-outlined">{{ getLinkIcon(link.linkType) }}</span>
                      <span class="link-title">{{ link.targetNote?.title || 'Untitled' }}</span>
                    </button>
                  </div>
                </div>

                <!-- AI 智能链接 -->
                <div v-if="relatedNotes.smartLinks.length > 0" class="links-section">
                  <h4 class="section-subtitle">{{ t('knowledge.smartLinks') }}</h4>
                  <div class="link-list">
                    <button
                      v-for="link in relatedNotes.smartLinks"
                      :key="link.id"
                      class="link-item smart"
                      @click="handleLinkClick(link.targetNoteId)"
                    >
                      <span class="material-symbols-outlined">psychology</span>
                      <div class="link-info">
                        <span class="link-title">{{ link.targetNote?.title || 'Untitled' }}</span>
                        <span class="link-confidence">{{ Math.round(link.confidence * 100) }}%</span>
                      </div>
                    </button>
                  </div>
                </div>

                <!-- 反向链接 -->
                <div v-if="relatedNotes.backlinks.length > 0" class="links-section">
                  <h4 class="section-subtitle">{{ t('knowledge.backlinks') }}</h4>
                  <div class="link-list">
                    <button
                      v-for="backlink in relatedNotes.backlinks"
                      :key="backlink.id"
                      class="link-item"
                      @click="handleLinkClick(backlink.id)"
                    >
                      <span class="material-symbols-outlined">arrow_back</span>
                      <span class="link-title">{{ backlink.title || 'Untitled' }}</span>
                    </button>
                  </div>
                </div>

                <!-- 空状态 -->
                <div v-if="!hasAnyLinks" class="panel-empty">
                  <span class="material-symbols-outlined">link_off</span>
                  <p>{{ t('knowledge.noLinksYet') }}</p>
                </div>
              </template>
            </aside>
          </div>
        </template>
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
  IonSpinner,
  actionSheetController,
  alertController
} from '@ionic/vue'
import { useNoteLinksStore } from '../stores/noteLinks'
import type { Note, RelatedNotesResponse } from '../types/knowledge'

const router = useRouter()
const route = useRoute()
const { t } = useI18n()

const noteLinksStore = useNoteLinksStore()

const loading = ref(true)
const note = ref<Note | null>(null)

const noteId = computed(() => route.params.id as string)

const relatedNotes = computed(() => noteLinksStore.relatedNotes)

const hasAnyLinks = computed(() => {
  if (!relatedNotes.value) return false
  return (
    relatedNotes.value.manualLinks.length > 0 ||
    relatedNotes.value.smartLinks.length > 0 ||
    relatedNotes.value.backlinks.length > 0
  )
})

const renderedContent = computed(() => {
  if (!note.value?.content) return ''
  
  // 简单的 Markdown 渲染（实际项目中应使用 marked 或 markdown-it）
  let html = note.value.content
  
  // 标题
  html = html.replace(/^### (.*$)/gim, '<h3>$1</h3>')
  html = html.replace(/^## (.*$)/gim, '<h2>$1</h2>')
  html = html.replace(/^# (.*$)/gim, '<h1>$1</h1>')
  
  // 粗体
  html = html.replace(/\*\*(.*?)\*\*/gim, '<strong>$1</strong>')
  
  // 斜体
  html = html.replace(/\*(.*?)\*/gim, '<em>$1</em>')
  
  // 链接
  html = html.replace(/\[([^\]]+)\]\(([^)]+)\)/gim, '<a href="$2" target="_blank">$1</a>')
  
  // 换行
  html = html.replace(/\n/gim, '<br>')
  
  return html
})

async function loadNote() {
  loading.value = true
  try {
    const response = await fetch(`/api/notes/${noteId.value}`)
    if (!response.ok) {
      throw new Error('Failed to load note')
    }
    note.value = await response.json()
    
    // 加载相关笔记
    await noteLinksStore.fetchRelatedNotes(noteId.value)
  } catch (error) {
    console.error('Failed to load note:', error)
    note.value = null
  } finally {
    loading.value = false
  }
}

function handleEdit() {
  router.push(`/notes/${noteId.value}/edit`)
}

async function handleMore() {
  const actionSheet = await actionSheetController.create({
    header: t('knowledge.actions'),
    buttons: [
      {
        text: note.value?.isPinned ? t('knowledge.unpin') : t('knowledge.pin'),
        icon: 'push_pin',
        handler: () => handleTogglePin()
      },
      {
        text: t('knowledge.delete'),
        icon: 'trash',
        role: 'destructive',
        handler: () => handleDelete()
      },
      {
        text: t('common.cancel'),
        icon: 'close',
        role: 'cancel'
      }
    ]
  })
  
  await actionSheet.present()
}

async function handleTogglePin() {
  if (!note.value) return
  
  try {
    const response = await fetch(`/api/notes/${noteId.value}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ isPinned: !note.value.isPinned })
    })
    
    if (response.ok) {
      note.value.isPinned = !note.value.isPinned
    }
  } catch (error) {
    console.error('Failed to toggle pin:', error)
  }
}

async function handleDelete() {
  const alert = await alertController.create({
    header: t('knowledge.deleteNote'),
    message: t('knowledge.deleteNoteConfirm'),
    buttons: [
      {
        text: t('common.cancel'),
        role: 'cancel'
      },
      {
        text: t('common.delete'),
        role: 'destructive',
        handler: async () => {
          try {
            const response = await fetch(`/api/notes/${noteId.value}`, {
              method: 'DELETE'
            })
            
            if (response.ok) {
              router.push('/knowledge')
            }
          } catch (error) {
            console.error('Failed to delete note:', error)
          }
        }
      }
    ]
  })
  
  await alert.present()
}

function handleLinkClick(targetNoteId: string) {
  router.push(`/notes/${targetNoteId}`)
}

function getSourceIcon(source: string): string {
  const iconMap: Record<string, string> = {
    email: 'mail',
    meeting: 'event',
    voice: 'mic',
    manual: 'edit_note',
    import: 'upload_file',
    memora: 'psychology'
  }
  return iconMap[source] || 'description'
}

function getLinkIcon(linkType: string): string {
  const iconMap: Record<string, string> = {
    related: 'link',
    reference: 'bookmark',
    parent: 'arrow_upward',
    child: 'arrow_downward',
    bidirectional: 'sync_alt'
  }
  return iconMap[linkType] || 'link'
}

function formatDate(timestamp: number): string {
  const date = new Date(timestamp)
  return date.toLocaleString()
}

onMounted(async () => {
  await loadNote()
})
</script>

<script lang="ts">
export default {
  name: 'NoteDetailPage'
}
</script>

<style scoped>
.note-detail-page {
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

.note-content-wrapper {
  display: flex;
  gap: var(--space-4);
  padding: var(--space-4);
}

.note-article {
  flex: 1;
  background: var(--bg-card);
  border-radius: var(--radius-lg);
  padding: var(--space-6);
  overflow: hidden;
}

.note-header {
  margin-bottom: var(--space-6);
  padding-bottom: var(--space-4);
  border-bottom: 1px solid var(--border);
}

.note-title {
  margin: 0 0 var(--space-4);
  font-size: var(--text-3xl);
  font-weight: var(--font-weight-bold);
  color: var(--text-primary);
  line-height: 1.3;
}

.note-meta {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.meta-row {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-3);
}

.meta-item {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  font-size: var(--text-sm);
  color: var(--text-secondary);
}

.meta-item .material-symbols-outlined {
  font-size: var(--text-lg);
}

.meta-item.source,
.meta-item.domain {
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-sm);
  background: var(--bg-secondary);
}

.meta-item.pinned {
  color: var(--brand-primary);
  font-weight: var(--font-weight-medium);
}

.note-tags {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin-top: var(--space-2);
}

.tag {
  padding: var(--space-1) var(--space-3);
  border-radius: var(--radius-full);
  background: var(--brand-bg);
  color: var(--brand-primary);
  font-size: var(--text-sm);
  font-weight: var(--font-weight-medium);
}

.note-summary {
  margin-bottom: var(--space-6);
  padding: var(--space-4);
  border-left: 3px solid var(--brand-primary);
  background: var(--brand-bg);
  border-radius: var(--radius-md);
}

.note-summary p {
  margin: 0;
  font-size: var(--text-base);
  color: var(--text-secondary);
  line-height: 1.6;
}

.note-body {
  font-size: var(--text-base);
  line-height: 1.8;
  color: var(--text-primary);
}

.markdown-content :deep(h1),
.markdown-content :deep(h2),
.markdown-content :deep(h3) {
  margin: var(--space-6) 0 var(--space-3);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
}

.markdown-content :deep(h1) {
  font-size: var(--text-2xl);
}

.markdown-content :deep(h2) {
  font-size: var(--text-xl);
}

.markdown-content :deep(h3) {
  font-size: var(--text-lg);
}

.markdown-content :deep(p) {
  margin: var(--space-3) 0;
}

.markdown-content :deep(a) {
  color: var(--brand-primary);
  text-decoration: underline;
}

.text-content {
  white-space: pre-wrap;
}

.note-links-panel {
  width: 320px;
  flex-shrink: 0;
  background: var(--bg-card);
  border-radius: var(--radius-lg);
  padding: var(--space-4);
  max-height: calc(100vh - 200px);
  overflow-y: auto;
}

.panel-title {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin: 0 0 var(--space-4);
  font-size: var(--text-base);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
}

.panel-title .material-symbols-outlined {
  font-size: 20px;
  color: var(--brand-primary);
}

.panel-loading {
  display: flex;
  justify-content: center;
  padding: var(--space-6);
}

.links-section {
  margin-bottom: var(--space-4);
}

.section-subtitle {
  margin: 0 0 var(--space-2);
  font-size: var(--text-sm);
  font-weight: var(--font-weight-medium);
  color: var(--text-secondary);
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.link-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.link-item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  padding: var(--space-2) var(--space-3);
  border: none;
  border-radius: var(--radius-md);
  background: var(--bg-secondary);
  color: var(--text-primary);
  font-size: var(--text-sm);
  text-align: left;
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.link-item:hover {
  background: var(--bg-hover);
  transform: translateX(4px);
}

.link-item .material-symbols-outlined {
  font-size: var(--text-xl);
  color: var(--text-secondary);
  flex-shrink: 0;
}

.link-item.smart .material-symbols-outlined {
  color: var(--brand-primary);
}

.link-info {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex: 1;
  gap: var(--space-2);
}

.link-title {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.link-confidence {
  flex-shrink: 0;
  padding: 2px 6px;
  border-radius: var(--radius-sm);
  background: var(--brand-bg);
  color: var(--brand-primary);
  font-size: var(--text-xs);
  font-weight: var(--font-weight-medium);
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

.panel-empty .material-symbols-outlined {
  font-size: 48px;
  color: var(--text-tertiary);
  margin-bottom: var(--space-2);
}

.panel-empty p {
  margin: 0;
  font-size: var(--text-sm);
}

@media (max-width: 1023px) {
  .note-content-wrapper {
    flex-direction: column;
  }

  .note-links-panel {
    width: 100%;
    max-height: none;
  }
}
</style>
