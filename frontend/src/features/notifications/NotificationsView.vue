<script setup lang="ts">
/**
 * NotificationsView — 通知中心 inbox(2026-09-20 通知体系 P1)。
 *
 * 数据源:notification store(启动/WS 连接时增量拉取 + WS 推送实时入账)。
 * 操作:单条已读(点击)、全部已读;按 source/kind 映射点击跳转。
 */
import { computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useNotificationStore } from '../../stores/notification'
import type { Notification } from '../../api/notifications'

const router = useRouter()
const store = useNotificationStore()

const items = computed(() => store.inbox)
const loading = computed(() => store.loading)
const unreadCount = computed(() => store.unreadCount)

onMounted(() => {
  if (!items.value.length) void store.loadInbox()
})

function timeLabel(sec: number): string {
  if (!sec) return ''
  const diff = Date.now() / 1000 - sec
  if (diff < 60) return '刚刚'
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`
  if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`
  if (diff < 7 * 86400) return `${Math.floor(diff / 86400)} 天前`
  const d = new Date(sec * 1000)
  return `${d.getMonth() + 1}/${d.getDate()}`
}

/** source/kind → 点击跳转;无映射则留在通知中心。 */
function targetFor(n: Notification): string {
  if (n.source === 'scheduledtask') return '/settings/scheduled-tasks'
  if (n.source === 'email') return '/email'
  if (n.source === 'flashcards') return '/flashcards/review'
  return ''
}

async function open(n: Notification) {
  if (!n.read_at) {
    void store.markRead(n.id).catch(() => { /* 已读失败不挡跳转 */ })
    n.read_at = Math.floor(Date.now() / 1000)
  }
  const target = targetFor(n)
  if (target) router.push(target)
}

async function markAllRead() {
  if (!unreadCount.value) return
  await store.markRead().catch(() => {})
}
</script>

<template>
  <div class="ntf-view">
    <div class="ntf-toolbar">
      <span class="ntf-count">{{ unreadCount ? `${unreadCount} 条未读` : '没有未读' }}</span>
      <button
        class="ntf-markall"
        type="button"
        :disabled="!unreadCount"
        @click="markAllRead"
      >全部已读</button>
    </div>

    <div v-if="loading && !items.length" class="ntf-state">加载中…</div>
    <div v-else-if="!items.length" class="ntf-state">
      <p>暂无通知</p>
      <p class="ntf-hint">定时任务失败、重要邮件、闪卡到期等会出现在这里</p>
    </div>

    <ul v-else class="ntf-list">
      <li v-for="n in items" :key="n.id" :class="{ unread: !n.read_at }">
        <button class="ntf-item" type="button" @click="open(n)">
          <span class="ntf-dot" :class="n.priority" aria-hidden="true"></span>
          <span class="ntf-main">
            <span class="ntf-title">{{ n.title }}</span>
            <span v-if="n.body" class="ntf-body">{{ n.body }}</span>
            <span class="ntf-meta">
              {{ n.source }}<template v-if="n.kind"> · {{ n.kind }}</template>
              · {{ timeLabel(n.created_at) }}
            </span>
          </span>
        </button>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.ntf-view { display: flex; flex-direction: column; min-height: 100%; }
.ntf-toolbar {
  display: flex; align-items: center; justify-content: space-between;
  padding: 10px var(--space-3);
}
.ntf-count { font-size: 12px; color: var(--text-secondary); }
.ntf-markall {
  border: 1px solid var(--border); background: var(--bg-card); color: var(--text-primary);
  border-radius: 999px; padding: 6px 14px; font-size: 13px;
}
.ntf-markall:disabled { opacity: 0.5; }
.ntf-state { text-align: center; color: var(--text-secondary); padding: var(--space-6); font-size: 14px; }
.ntf-hint { margin-top: 6px; font-size: 12px; color: var(--text-muted); }
.ntf-list { list-style: none; margin: 0; padding: 0 var(--space-3) 96px; display: flex; flex-direction: column; gap: 8px; }
.ntf-item {
  display: flex; gap: 10px; width: 100%; text-align: left;
  background: var(--bg-card); border: 1px solid var(--border); border-radius: 8px;
  padding: 10px 12px;
}
li.unread .ntf-item { border-left: 3px solid var(--brand-primary, #2f6fed); }
.ntf-dot { width: 8px; height: 8px; border-radius: 999px; background: var(--text-muted); margin-top: 6px; flex-shrink: 0; }
.ntf-dot.high, .ntf-dot.urgent, .ntf-dot.high_quiet { background: var(--danger, #e5484d); }
.ntf-main { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
.ntf-title { font-size: 14px; font-weight: 600; color: var(--text-primary); }
.ntf-body { font-size: 13px; color: var(--text-secondary); line-height: 1.4; }
.ntf-meta { font-size: 11px; color: var(--text-muted); }
</style>
