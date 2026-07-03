<template>
  <Card
    class="note-card"
    :hoverable="true"
    :clickable="true"
    @click="handleClick"
  >
    <div class="note-header">
      <h3 class="note-title">{{ note.title }}</h3>
      <span v-if="note.domain" class="note-domain">{{ domainLabel }}</span>
    </div>
    
    <p v-if="note.content" class="note-content">
      {{ truncatedContent }}
    </p>
    
    <div class="note-footer">
      <div class="note-meta">
        <span class="note-time">{{ formattedTime }}</span>
        <span v-if="note.tags && note.tags.length > 0" class="note-tags">
          {{ note.tags.slice(0, 2).join(' · ') }}
        </span>
      </div>
      
      <div v-if="note.audio_path" class="note-badge">
        🎤
      </div>
    </div>
  </Card>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import Card from '../base/Card.vue'

export interface Note {
  id: string
  title: string
  content?: string
  domain?: string
  tags?: string[]
  audio_path?: string
  created_at: number
  updated_at: number
}

export interface NoteCardProps {
  note: Note
  onClick?: (note: Note) => void
}

const props = defineProps<NoteCardProps>()

const domainLabel = computed(() => {
  const labels: Record<string, string> = {
    work: '工作',
    study: '学习',
    life: '生活',
    idea: '想法',
  }
  return props.note.domain ? labels[props.note.domain] || props.note.domain : ''
})

const truncatedContent = computed(() => {
  if (!props.note.content) return ''
  const maxLength = 120
  return props.note.content.length > maxLength
    ? props.note.content.substring(0, maxLength) + '...'
    : props.note.content
})

const formattedTime = computed(() => {
  const now = Date.now()
  const diff = now - props.note.updated_at
  
  const minutes = Math.floor(diff / 60000)
  const hours = Math.floor(diff / 3600000)
  const days = Math.floor(diff / 86400000)
  
  if (minutes < 1) return '刚刚'
  if (minutes < 60) return `${minutes} 分钟前`
  if (hours < 24) return `${hours} 小时前`
  if (days < 7) return `${days} 天前`
  
  const date = new Date(props.note.updated_at)
  return `${date.getMonth() + 1}/${date.getDate()}`
})

const handleClick = () => {
  props.onClick?.(props.note)
}
</script>

<style scoped>
.note-card {
  padding: var(--space-4);
}

.note-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  margin-bottom: var(--space-2);
}

.note-title {
  font-size: 16px;
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-primary);
  margin: 0;
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.note-domain {
  flex-shrink: 0;
  font-size: 12px;
  padding: 2px 8px;
  background: var(--gradient-primary);
  color: white;
  border-radius: var(--radius-full);
  font-weight: var(--font-weight-medium);
}

.note-content {
  font-size: 14px;
  color: var(--color-text-secondary);
  line-height: 1.6;
  margin: 0 0 var(--space-3) 0;
  display: -webkit-box;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.note-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.note-meta {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: 12px;
  color: var(--color-text-tertiary);
}

.note-time {
  font-weight: var(--font-weight-medium);
}

.note-tags {
  opacity: 0.8;
}

.note-badge {
  font-size: 18px;
}
</style>
