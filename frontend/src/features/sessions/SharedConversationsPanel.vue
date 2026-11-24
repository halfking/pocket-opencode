<script setup lang="ts">
/**
 * SharedConversationsPanel — 「共享给我的」会话 + 「申请接手」按钮。
 *
 * 这是移动端的接手入口：同事在 RedClaw 上把一个做到一半的会话共享给部门或
 * 项目后，这里能看见它，并一键申请接手。
 *
 * 两个纪律：
 *   ① **申请 ≠ 接手**。按钮发的是一次申请，会话在有人决定之前仍归原主人；
 *      按钮按下后变成「已申请」，不再自我重复。
 *   ② **门户不可达不是错误**。RedClaw 未部署 / 未配置时，整个面板不渲染，
 *      不阻塞会话列表 —— openpocket 仍然是一个可用的移动端工作台。
 */
import { onMounted, onBeforeUnmount, ref, computed } from 'vue'
import {
  fetchSharedConversations,
  requestSessionTakeover,
  SessionTakeoverApiError,
  type SharedConversation,
} from '../../api/sessionTakeover'
import { useToast } from '../../composables/useToast'

const emit = defineEmits<{ (e: 'requested', sessionId: string): void }>()

const toast = useToast()
const conversations = ref<SharedConversation[]>([])
const loading = ref(false)
const failed = ref(false)
const busyId = ref<string>('')
/** 已发起过申请的会话 id —— 按钮按过就不再自我重复。 */
const requested = ref<Record<string, boolean>>({})

const scopeLabel: Record<string, string> = {
  private: '仅自己',
  department: '部门共享',
  project: '项目共享',
  tenant: '全租户共享',
}

const hasContent = computed(() => conversations.value.length > 0)

let timer: ReturnType<typeof setInterval> | undefined

async function load(): Promise<void> {
  loading.value = true
  try {
    conversations.value = await fetchSharedConversations()
    failed.value = false
  } catch (err) {
    // 门户不可达（未配置 / 401 / 网络）⇒ 面板不渲染。不打扰用户。
    failed.value = true
    conversations.value = []
    if (err instanceof SessionTakeoverApiError && err.status === 401) {
      console.warn('[shared-conversations] portal rejected the request', err.message)
    }
  } finally {
    loading.value = false
  }
}

async function ask(sessionId: string): Promise<void> {
  busyId.value = sessionId
  try {
    await requestSessionTakeover(sessionId)
    requested.value = { ...requested.value, [sessionId]: true }
    toast.success('接手申请已提交，等待对方确认')
    emit('requested', sessionId)
  } catch (err) {
    const message = err instanceof Error ? err.message : '接手申请失败'
    toast.error(message)
  } finally {
    busyId.value = ''
  }
}

onMounted(() => {
  void load()
  timer = setInterval(() => { void load() }, 30_000)
})

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
})
</script>

<template>
  <section v-if="hasContent" class="shared-panel" aria-label="共享给我的会话">
    <header class="shared-header">
      <span class="material-symbols-outlined shared-icon" aria-hidden="true">group</span>
      <h2 class="shared-title">共享给我的</h2>
      <span class="shared-count">{{ conversations.length }}</span>
    </header>

    <ul class="shared-list">
      <li v-for="item in conversations" :key="item.id" class="shared-item">
        <div class="shared-info">
          <p class="shared-item-title">{{ item.title || item.id }}</p>
          <p class="shared-item-meta">
            <span>{{ scopeLabel[item.visibility] || item.visibility }}</span>
            <span v-if="item.ownerId"> · 归属 {{ item.ownerId }}</span>
          </p>
        </div>
        <button
          type="button"
          class="shared-action"
          :disabled="requested[item.id] || busyId === item.id"
          @click="ask(item.id)"
        >
          <span
            v-if="busyId === item.id"
            class="material-symbols-outlined shared-action-icon"
            aria-hidden="true"
          >progress_activity</span>
          <template v-else-if="requested[item.id]">已申请</template>
          <template v-else>申请接手</template>
        </button>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.shared-panel {
  margin: 0 var(--space-4) var(--space-3);
  padding: var(--space-3);
  border: 1px solid var(--border-subtle, var(--border-color, rgba(0, 0, 0, 0.08)));
  border-radius: var(--radius-lg, 12px);
  background: var(--surface-dim, rgba(0, 0, 0, 0.02));
}

.shared-header {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  margin-bottom: var(--space-2);
}

.shared-icon {
  font-size: 18px;
  color: var(--text-secondary);
}

.shared-title {
  margin: 0;
  font-size: var(--text-sm, 13px);
  font-weight: 600;
  color: var(--text-primary);
}

.shared-count {
  margin-left: auto;
  font-size: var(--text-xs, 11px);
  color: var(--text-muted);
}

.shared-list {
  margin: 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.shared-item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.shared-info {
  min-width: 0;
  flex: 1;
}

.shared-item-title {
  margin: 0;
  font-size: var(--text-sm, 13px);
  color: var(--text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.shared-item-meta {
  margin: 0;
  font-size: var(--text-xs, 11px);
  color: var(--text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.shared-action {
  flex: none;
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-1) var(--space-2);
  border: 1px solid var(--accent-color, #4f6bed);
  border-radius: var(--radius-md, 8px);
  background: transparent;
  color: var(--accent-color, #4f6bed);
  font-size: var(--text-xs, 11px);
  cursor: pointer;
}

.shared-action:disabled {
  opacity: 0.6;
  cursor: default;
}

.shared-action-icon {
  font-size: 14px;
  animation: shared-spin 1s linear infinite;
}

@keyframes shared-spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
