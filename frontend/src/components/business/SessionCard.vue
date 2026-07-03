<template>
  <Card
    class="session-card"
    :hoverable="true"
    :clickable="true"
    @click="handleClick"
  >
    <div class="session-header">
      <div class="session-avatar">
        {{ session.avatar || '💬' }}
      </div>
      <div class="session-info">
        <h4 class="session-title">{{ session.title }}</h4>
        <p class="session-preview">{{ session.lastMessage }}</p>
      </div>
      <div class="session-meta">
        <span class="session-time">{{ formattedTime }}</span>
        <span
          v-if="session.unreadCount > 0"
          class="unread-badge"
        >
          {{ displayUnreadCount }}
        </span>
      </div>
    </div>
  </Card>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import Card from '../base/Card.vue'

export interface Session {
  id: string
  title: string
  lastMessage: string
  lastMessageTime: number
  unreadCount: number
  avatar?: string
  type?: 'ai' | 'meeting' | 'note'
}

export interface SessionCardProps {
  session: Session
  onClick?: (session: Session) => void
}

const props = defineProps<SessionCardProps>()

const formattedTime = computed(() => {
  const now = Date.now()
  const diff = now - props.session.lastMessageTime

  const minutes = Math.floor(diff / 60000)
  const hours = Math.floor(diff / 3600000)
  const days = Math.floor(diff / 86400000)

  if (minutes < 1) return '刚刚'
  if (minutes < 60) return `${minutes}分钟前`
  if (hours < 24) return `${hours}小时前`
  if (days < 7) return `${days}天前`

  const date = new Date(props.session.lastMessageTime)
  return `${date.getMonth() + 1}/${date.getDate()}`
})

const displayUnreadCount = computed(() => {
  return props.session.unreadCount > 99 ? '99+' : props.session.unreadCount
})

const handleClick = () => {
  props.onClick?.(props.session)
}
</script>

<style scoped>
.session-card {
  padding: var(--space-4);
}

.session-header {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
}

.session-avatar {
  flex-shrink: 0;
  width: 48px;
  height: 48px;
  border-radius: 50%;
  background: var(--gradient-primary);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 24px;
}

.session-info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.session-title {
  font-size: 16px;
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-primary);
  margin: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.session-preview {
  font-size: 14px;
  color: var(--color-text-secondary);
  margin: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.session-meta {
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 6px;
}

.session-time {
  font-size: 12px;
  color: var(--color-text-tertiary);
  white-space: nowrap;
}

.unread-badge {
  min-width: 20px;
  height: 20px;
  padding: 0 6px;
  background: var(--color-error);
  color: white;
  border-radius: var(--radius-full);
  font-size: 11px;
  font-weight: var(--font-weight-bold);
  display: flex;
  align-items: center;
  justify-content: center;
}
</style>
