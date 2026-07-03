<template>
  <Card
    class="email-card"
    :hoverable="true"
    :clickable="true"
    @click="handleClick"
  >
    <div class="email-header">
      <div class="email-from">
        <span class="from-name">{{ email.from_name || email.from_address }}</span>
        <span v-if="email.is_starred" class="star-icon">⭐</span>
      </div>
      <span class="email-time">{{ formattedTime }}</span>
    </div>

    <h4 class="email-subject">{{ email.subject }}</h4>

    <p class="email-snippet">{{ email.snippet }}</p>

    <div class="email-footer">
      <div class="email-tags">
        <span
          v-if="email.category"
          class="email-tag email-tag--category"
        >
          {{ categoryLabel }}
        </span>
        <span
          v-if="email.importance === 'high'"
          class="email-tag email-tag--important"
        >
          重要
        </span>
        <span
          v-if="email.has_attachments"
          class="email-tag email-tag--attachment"
        >
          📎 附件
        </span>
      </div>
      
      <div
        v-if="!email.is_read"
        class="unread-indicator"
        title="未读"
      ></div>
    </div>
  </Card>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import Card from '../base/Card.vue'

export interface Email {
  id: string
  subject: string
  snippet: string
  from_name?: string
  from_address: string
  date: number
  is_read: boolean
  is_starred: boolean
  category?: string
  importance?: 'normal' | 'high' | 'low'
  has_attachments: boolean
}

export interface EmailCardProps {
  email: Email
  onClick?: (email: Email) => void
}

const props = defineProps<EmailCardProps>()

const categoryLabel = computed(() => {
  const labels: Record<string, string> = {
    primary: '主要',
    social: '社交',
    promotions: '推广',
    updates: '更新',
    forums: '论坛',
  }
  return props.email.category ? labels[props.email.category] || props.email.category : ''
})

const formattedTime = computed(() => {
  const now = Date.now()
  const diff = now - props.email.date

  const minutes = Math.floor(diff / 60000)
  const hours = Math.floor(diff / 3600000)
  const days = Math.floor(diff / 86400000)

  if (minutes < 1) return '刚刚'
  if (minutes < 60) return `${minutes} 分钟前`
  if (hours < 24) return `${hours} 小时前`
  if (days < 7) return `${days} 天前`

  const date = new Date(props.email.date)
  return `${date.getMonth() + 1}/${date.getDate()}`
})

const handleClick = () => {
  props.onClick?.(props.email)
}
</script>

<style scoped>
.email-card {
  padding: var(--space-4);
}

.email-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  margin-bottom: var(--space-2);
}

.email-from {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex: 1;
  min-width: 0;
}

.from-name {
  font-size: 14px;
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.star-icon {
  flex-shrink: 0;
  font-size: 14px;
}

.email-time {
  flex-shrink: 0;
  font-size: 12px;
  color: var(--color-text-tertiary);
}

.email-subject {
  font-size: 15px;
  font-weight: var(--font-weight-semibold);
  color: var(--color-text-primary);
  margin: 0 0 var(--space-2) 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.email-snippet {
  font-size: 14px;
  color: var(--color-text-secondary);
  line-height: 1.5;
  margin: 0 0 var(--space-3) 0;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.email-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.email-tags {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex-wrap: wrap;
}

.email-tag {
  font-size: 11px;
  padding: 2px 8px;
  border-radius: var(--radius-full);
  font-weight: var(--font-weight-medium);
  white-space: nowrap;
}

.email-tag--category {
  background: var(--color-primary);
  color: white;
}

.email-tag--important {
  background: var(--color-error);
  color: white;
}

.email-tag--attachment {
  background: var(--color-bg-base);
  color: var(--color-text-secondary);
}

.unread-indicator {
  flex-shrink: 0;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--color-primary);
}
</style>
