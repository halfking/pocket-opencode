<!--
  NoteEditPage — 笔记编辑页面
  
  功能：
  - Markdown 编辑器（纯文本输入）
  - 实时预览（可切换）
  - 笔记本选择
  - 标签编辑
  - 领域选择
  - 保存和取消
  - 自动保存草稿
  
  路由：
  - /notes/new - 新建笔记
  - /notes/:id/edit - 编辑现有笔记
-->
<template>
  <ion-page>
    <ion-header>
      <ion-toolbar>
        <ion-buttons slot="start">
          <ion-button @click="handleCancel">
            <span class="material-symbols-outlined">close</span>
          </ion-button>
        </ion-buttons>
        <ion-title>{{ isNewNote ? t('knowledge.newNote') : t('knowledge.editNote') }}</ion-title>
        <ion-buttons slot="end">
          <ion-button @click="togglePreview" v-if="!isPreviewMode">
            <span class="material-symbols-outlined">visibility</span>
          </ion-button>
          <ion-button @click="togglePreview" v-else>
            <span class="material-symbols-outlined">edit</span>
          </ion-button>
          <ion-button @click="handleSave" :disabled="saving">
            <ion-spinner v-if="saving" name="crescent"></ion-spinner>
            <span v-else class="material-symbols-outlined">save</span>
          </ion-button>
        </ion-buttons>
      </ion-toolbar>
    </ion-header>

    <ion-content :fullscreen="true">
      <div class="note-edit-page">
        <div class="edit-container">
          <!-- 笔记元信息编辑区 -->
          <div class="note-meta-editor">
            <!-- 标题 -->
            <ion-input
              v-model="formData.title"
              :placeholder="t('knowledge.enterTitle')"
              class="title-input"
            ></ion-input>

            <!-- 元数据行 -->
            <div class="meta-controls">
              <!-- 笔记本选择 -->
              <ion-select
                v-model="formData.notebookId"
                :placeholder="t('knowledge.selectNotebook')"
                interface="action-sheet"
                class="meta-select"
              >
                <ion-select-option :value="undefined">{{ t('knowledge.noNotebook') }}</ion-select-option>
                <ion-select-option
                  v-for="notebook in notebooksStore.notebooks"
                  :key="notebook.id"
                  :value="notebook.id"
                >
                  {{ notebook.name }}
                </ion-select-option>
              </ion-select>

              <!-- 领域选择 -->
              <ion-select
                v-model="formData.domain"
                :placeholder="t('knowledge.selectDomain')"
                interface="action-sheet"
                class="meta-select"
              >
                <ion-select-option value="work">{{ t('knowledge.work') }}</ion-select-option>
                <ion-select-option value="study">{{ t('knowledge.study') }}</ion-select-option>
                <ion-select-option value="life">{{ t('knowledge.life') }}</ion-select-option>
                <ion-select-option value="idea">{{ t('knowledge.idea') }}</ion-select-option>
              </ion-select>
            </div>

            <!-- 标签输入 -->
            <div class="tags-editor">
              <div class="tags-input-wrapper">
                <span class="material-symbols-outlined tags-icon">label</span>
                <input
                  v-model="tagInput"
                  type="text"
                  :placeholder="t('knowledge.addTags')"
                  class="tags-input"
                  @keydown.enter.prevent="handleAddTag"
                  @keydown.comma.prevent="handleAddTag"
                />
              </div>
              <div v-if="formData.tags.length > 0" class="tags-list">
                <span
                  v-for="(tag, index) in formData.tags"
                  :key="index"
                  class="tag"
                >
                  #{{ tag }}
                  <button
                    class="tag-remove"
                    @click="handleRemoveTag(index)"
                  >
                    <span class="material-symbols-outlined">close</span>
                  </button>
                </span>
              </div>
            </div>
          </div>

          <!-- 内容编辑/预览区 -->
          <div class="content-editor">
            <template v-if="!isPreviewMode">
              <!-- 编辑模式 -->
              <ion-textarea
                v-model="formData.content"
                :placeholder="t('knowledge.enterContent')"
                :auto-grow="true"
                class="content-textarea"
                rows="20"
              ></ion-textarea>

              <!-- Markdown 快捷工具栏 -->
              <div class="markdown-toolbar">
                <button
                  v-for="tool in markdownTools"
                  :key="tool.name"
                  class="toolbar-btn"
                  :title="tool.title"
                  @click="insertMarkdown(tool)"
                >
                  <span class="material-symbols-outlined">{{ tool.icon }}</span>
                </button>
              </div>
            </template>

            <template v-else>
              <!-- 预览模式 -->
              <div class="preview-container">
                <h2 class="preview-title">{{ formData.title || t('knowledge.untitled') }}</h2>
                <div class="preview-content" v-html="renderedContent"></div>
              </div>
            </template>
          </div>
        </div>
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
  IonButton,
  IonInput,
  IonTextarea,
  IonSelect,
  IonSelectOption,
  IonSpinner,
  alertController
} from '@ionic/vue'
import { useNotebooksStore } from '../stores/notebooks'
import type { Note } from '../types/knowledge'

const router = useRouter()
const route = useRoute()
const { t } = useI18n()

const notebooksStore = useNotebooksStore()

const saving = ref(false)
const isPreviewMode = ref(false)
const tagInput = ref('')

const formData = ref({
  title: '',
  content: '',
  notebookId: undefined as string | undefined,
  domain: undefined as string | undefined,
  tags: [] as string[]
})

const noteId = computed(() => {
  const id = route.params.id as string
  return id === 'new' ? null : id
})

const isNewNote = computed(() => noteId.value === null)

const markdownTools = [
  { name: 'bold', icon: 'format_bold', title: t('knowledge.bold'), syntax: '**text**' },
  { name: 'italic', icon: 'format_italic', title: t('knowledge.italic'), syntax: '*text*' },
  { name: 'heading', icon: 'title', title: t('knowledge.heading'), syntax: '## ' },
  { name: 'list', icon: 'format_list_bulleted', title: t('knowledge.list'), syntax: '- ' },
  { name: 'link', icon: 'link', title: t('knowledge.link'), syntax: '[text](url)' },
  { name: 'code', icon: 'code', title: t('knowledge.code'), syntax: '`code`' }
]

const renderedContent = computed(() => {
  if (!formData.value.content) return '<p class="empty-preview">' + t('knowledge.noContent') + '</p>'
  
  // 简单的 Markdown 渲染
  let html = formData.value.content
  
  // 标题
  html = html.replace(/^### (.*$)/gim, '<h3>$1</h3>')
  html = html.replace(/^## (.*$)/gim, '<h2>$1</h2>')
  html = html.replace(/^# (.*$)/gim, '<h1>$1</h1>')
  
  // 粗体
  html = html.replace(/\*\*(.*?)\*\*/gim, '<strong>$1</strong>')
  
  // 斜体
  html = html.replace(/\*(.*?)\*/gim, '<em>$1</em>')
  
  // 代码
  html = html.replace(/`(.*?)`/gim, '<code>$1</code>')
  
  // 链接
  html = html.replace(/\[([^\]]+)\]\(([^)]+)\)/gim, '<a href="$2" target="_blank">$1</a>')
  
  // 列表
  html = html.replace(/^- (.*$)/gim, '<li>$1</li>')
  html = html.replace(/(<li>.*<\/li>)/s, '<ul>$1</ul>')
  
  // 段落
  html = html.split('\n\n').map(para => {
    if (para.match(/^<[h|u|o]/)) return para
    return '<p>' + para.replace(/\n/g, '<br>') + '</p>'
  }).join('\n')
  
  return html
})

async function loadNote() {
  if (isNewNote.value) {
    // 从 URL 参数获取笔记本 ID
    const notebookId = route.query.notebookId as string
    if (notebookId) {
      formData.value.notebookId = notebookId
    }
    return
  }
  
  try {
    const response = await fetch(`/api/notes/${noteId.value}`)
    if (!response.ok) {
      throw new Error('Failed to load note')
    }
    const note: Note = await response.json()
    
    formData.value = {
      title: note.title || '',
      content: note.content,
      notebookId: note.notebookId,
      domain: note.domain,
      tags: note.tags || []
    }
  } catch (error) {
    console.error('Failed to load note:', error)
  }
}

async function handleSave() {
  if (!formData.value.content.trim()) {
    const alert = await alertController.create({
      header: t('knowledge.error'),
      message: t('knowledge.contentRequired'),
      buttons: [t('common.ok')]
    })
    await alert.present()
    return
  }
  
  saving.value = true
  try {
    const url = isNewNote.value ? '/api/notes' : `/api/notes/${noteId.value}`
    const method = isNewNote.value ? 'POST' : 'PUT'
    
    const payload = {
      ...formData.value,
      contentType: 'markdown'
    }
    
    const response = await fetch(url, {
      method,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    })
    
    if (!response.ok) {
      throw new Error('Failed to save note')
    }
    
    const savedNote: Note = await response.json()
    router.push(`/notes/${savedNote.id}`)
  } catch (error) {
    console.error('Failed to save note:', error)
    const alert = await alertController.create({
      header: t('knowledge.error'),
      message: t('knowledge.saveFailed'),
      buttons: [t('common.ok')]
    })
    await alert.present()
  } finally {
    saving.value = false
  }
}

async function handleCancel() {
  // 检查是否有未保存的更改
  const hasChanges = formData.value.title || formData.value.content || formData.value.tags.length > 0
  
  if (hasChanges) {
    const alert = await alertController.create({
      header: t('knowledge.discardChanges'),
      message: t('knowledge.discardChangesConfirm'),
      buttons: [
        {
          text: t('common.cancel'),
          role: 'cancel'
        },
        {
          text: t('knowledge.discard'),
          role: 'destructive',
          handler: () => router.back()
        }
      ]
    })
    await alert.present()
  } else {
    router.back()
  }
}

function togglePreview() {
  isPreviewMode.value = !isPreviewMode.value
}

function handleAddTag() {
  const tag = tagInput.value.trim().replace(/^#+/, '')
  if (tag && !formData.value.tags.includes(tag)) {
    formData.value.tags.push(tag)
    tagInput.value = ''
  }
}

function handleRemoveTag(index: number) {
  formData.value.tags.splice(index, 1)
}

function insertMarkdown(tool: typeof markdownTools[0]) {
  const textarea = document.querySelector('.content-textarea textarea') as HTMLTextAreaElement
  if (!textarea) return
  
  const start = textarea.selectionStart
  const end = textarea.selectionEnd
  const selectedText = formData.value.content.substring(start, end)
  
  let insertion = tool.syntax
  
  if (tool.name === 'bold' || tool.name === 'italic') {
    insertion = tool.syntax.replace('text', selectedText || 'text')
  } else if (tool.name === 'heading' || tool.name === 'list') {
    insertion = tool.syntax + (selectedText || '')
  } else if (tool.name === 'link') {
    insertion = tool.syntax.replace('text', selectedText || 'text')
  } else if (tool.name === 'code') {
    insertion = tool.syntax.replace('code', selectedText || 'code')
  }
  
  const newContent = 
    formData.value.content.substring(0, start) +
    insertion +
    formData.value.content.substring(end)
  
  formData.value.content = newContent
  
  // 重新聚焦并设置光标位置
  setTimeout(() => {
    textarea.focus()
    const newCursorPos = start + insertion.length
    textarea.setSelectionRange(newCursorPos, newCursorPos)
  }, 0)
}

onMounted(async () => {
  // 加载笔记本列表
  if (notebooksStore.notebooks.length === 0) {
    await notebooksStore.fetchNotebooks()
  }
  
  // 加载笔记内容
  await loadNote()
})
</script>

<script lang="ts">
export default {
  name: 'NoteEditPage'
}
</script>

<style scoped>
.note-edit-page {
  height: 100%;
  background: var(--bg-base);
}

.edit-container {
  height: 100%;
  display: flex;
  flex-direction: column;
  padding: var(--space-4);
  gap: var(--space-4);
}

.note-meta-editor {
  flex-shrink: 0;
  background: var(--bg-card);
  border-radius: var(--radius-lg);
  padding: var(--space-4);
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.title-input {
  --padding-start: 0;
  --padding-end: 0;
  font-size: var(--text-2xl);
  font-weight: var(--font-weight-bold);
  --placeholder-color: var(--text-tertiary);
  --placeholder-opacity: 1;
}

.meta-controls {
  display: flex;
  gap: var(--space-3);
  flex-wrap: wrap;
}

.meta-select {
  flex: 1;
  min-width: 150px;
  --padding-start: var(--space-3);
  --padding-end: var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-secondary);
}

.tags-editor {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.tags-input-wrapper {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--border);
  border-radius: var(--radius-md);
  background: var(--bg-secondary);
}

.tags-icon {
  font-size: 20px;
  color: var(--text-secondary);
}

.tags-input {
  flex: 1;
  border: none;
  background: transparent;
  font-size: var(--text-base);
  color: var(--text-primary);
  outline: none;
}

.tags-input::placeholder {
  color: var(--text-tertiary);
}

.tags-list {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.tag {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-full);
  background: var(--brand-bg);
  color: var(--brand-primary);
  font-size: var(--text-sm);
  font-weight: var(--font-weight-medium);
}

.tag-remove {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  border: none;
  border-radius: 50%;
  background: transparent;
  color: inherit;
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-out);
}

.tag-remove:hover {
  background: rgba(0, 0, 0, 0.1);
}

.tag-remove .material-symbols-outlined {
  font-size: 14px;
}

.content-editor {
  flex: 1;
  display: flex;
  flex-direction: column;
  background: var(--bg-card);
  border-radius: var(--radius-lg);
  overflow: hidden;
  position: relative;
}

.content-textarea {
  flex: 1;
  --padding-start: var(--space-4);
  --padding-end: var(--space-4);
  --padding-top: var(--space-4);
  --padding-bottom: 60px;
  font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', monospace;
  font-size: var(--text-base);
  line-height: 1.8;
  --placeholder-color: var(--text-tertiary);
  --placeholder-opacity: 1;
}

.markdown-toolbar {
  position: absolute;
  bottom: 0;
  left: 0;
  right: 0;
  display: flex;
  gap: var(--space-1);
  padding: var(--space-2);
  background: var(--bg-secondary);
  border-top: 1px solid var(--border);
}

.toolbar-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 40px;
  height: 40px;
  border: none;
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--text-secondary);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-out);
}

.toolbar-btn:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.toolbar-btn .material-symbols-outlined {
  font-size: 20px;
}

.preview-container {
  flex: 1;
  padding: var(--space-6);
  overflow-y: auto;
}

.preview-title {
  margin: 0 0 var(--space-6);
  font-size: var(--text-3xl);
  font-weight: var(--font-weight-bold);
  color: var(--text-primary);
}

.preview-content {
  font-size: var(--text-base);
  line-height: 1.8;
  color: var(--text-primary);
}

.preview-content :deep(h1),
.preview-content :deep(h2),
.preview-content :deep(h3) {
  margin: var(--space-6) 0 var(--space-3);
  font-weight: var(--font-weight-semibold);
  color: var(--text-primary);
}

.preview-content :deep(h1) {
  font-size: var(--text-2xl);
}

.preview-content :deep(h2) {
  font-size: var(--text-xl);
}

.preview-content :deep(h3) {
  font-size: var(--text-lg);
}

.preview-content :deep(p) {
  margin: var(--space-3) 0;
}

.preview-content :deep(ul) {
  margin: var(--space-3) 0;
  padding-left: var(--space-6);
}

.preview-content :deep(li) {
  margin: var(--space-1) 0;
}

.preview-content :deep(code) {
  padding: 2px 6px;
  border-radius: var(--radius-sm);
  background: var(--bg-secondary);
  color: var(--brand-primary);
  font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', monospace;
  font-size: 0.9em;
}

.preview-content :deep(a) {
  color: var(--brand-primary);
  text-decoration: underline;
}

.preview-content :deep(.empty-preview) {
  color: var(--text-tertiary);
  font-style: italic;
}
</style>
