<!--
  BacklinksPanel.vue — 反向链接面板。

  显示引用了 targetTitle 的所有笔记（pkm-store.getBacklinks）。
  被 PkmNoteView / PkmTodayView 复用，放在编辑器下方。
  点击某条 → emit('open', id)，父视图跳转。
-->
<template>
  <section class="backlinks" v-if="ready">
    <h3 class="bl-title">
      反向链接 <span class="bl-count">{{ links.length }}</span>
    </h3>
    <div v-if="failed" class="bl-empty bl-failed">
      反向链接加载失败
      <button type="button" class="bl-retry" @click="reload">重试</button>
    </div>
    <div v-else-if="links.length === 0" class="bl-empty">
      还没有其它笔记引用「{{ targetTitle }}」
    </div>
    <ul v-else class="bl-list">
      <li v-for="n in links" :key="n.id" class="bl-item" @click="emit('open', n.id)">
        <span class="bl-item-title">{{ n.title || '无标题' }}</span>
        <span class="bl-snippet">{{ snippet(n.html) }}</span>
        <span class="bl-date">{{ formatTime(n.updatedAt) }}</span>
      </li>
    </ul>
  </section>
</template>

<script setup lang="ts">
import { ref, watch, onMounted } from 'vue'
import { getBacklinks, type PkmNote } from './pkm-store'

const props = defineProps<{
  targetTitle: string
  workspaceId?: string
  /** 任意值变化即重查。父组件笔记保存后递增它（覆盖 self-link 等标题不变的场景）。 */
  refreshKey?: number
}>()
const emit = defineEmits<{ (e: 'open', id: string): void }>()

const links = ref<PkmNote[]>([])
const ready = ref(false)
/**
 * §854：`ready` 同时充当「加载中」和「要不要渲染」的开关（模板 `v-if="ready"`），
 * 而 `getBacklinks` → `localDB.query` 不吞错 ⇒ **一次查询失败就让 `ready` 永远停在 false**，
 * 整个反向链接面板永久不渲染，且与「加载中」完全不可区分。
 * 失败还不止于此：未处理的 rejection 会在控制台留痕，用户侧零反馈。
 * ⇒ 必须复位 `ready`，并给一条**说清是失败、且可重试**的提示（而不是伪装成空态）。
 */
const failed = ref(false)
async function reload() {
  ready.value = false
  failed.value = false
  try {
    links.value = await getBacklinks(props.targetTitle, props.workspaceId)
  } catch (e: any) {
    console.error('[pkm] 反向链接查询失败:', e)
    links.value = []
    failed.value = true
  } finally {
    // ⚠ 无论成败都要复位：`ready` 是渲染开关，卡在 false 就等于面板永久消失。
    ready.value = true
  }
}

onMounted(reload)
// 标题/工作区变化、或父组件显式 bump refreshKey（保存后）→ 重查
watch(() => [props.targetTitle, props.workspaceId, props.refreshKey], reload)

function snippet(html: string): string {
  const text = html.replace(/<[^>]+>/g, ' ').replace(/\s+/g, ' ').trim()
  return text.length > 80 ? text.slice(0, 80) + '…' : text
}

function formatTime(ts: number): string {
  const d = new Date(ts)
  return `${d.getMonth() + 1}/${d.getDate()}`
}
</script>

<style scoped>
.backlinks {
  border-top: 1px solid var(--border, #eee);
  padding: 16px;
  margin-top: 8px;
}
.bl-title {
  font-size: var(--text-smd);
  font-weight: 600;
  color: var(--text-secondary, #666);
  margin: 0 0 8px;
}
.bl-count {
  background: var(--bg-muted, #f0f0f0);
  border-radius: 10px;
  padding: 1px 7px;
  font-size: var(--text-2xs);
}
.bl-empty {
  font-size: var(--text-smd);
  color: var(--text-secondary, #999);
  padding: 8px 0;
}
/* §854：失败态要与空态视觉可区分 —— 谎报成「没有反向链接」会让用户以为笔记真的没被引用。 */
.bl-failed {
  color: var(--text-danger, #c0392b);
  display: flex;
  align-items: center;
  gap: 8px;
}
.bl-retry {
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--bg-card);
  color: var(--text-primary);
  padding: 3px 10px;
  cursor: pointer;
}
.bl-list {
  list-style: none;
  margin: 0;
  padding: 0;
}
.bl-item {
  padding: 10px 12px;
  border-radius: 8px;
  cursor: pointer;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.bl-item:hover {
  background: var(--bg-hover, #f7f7f9);
}
.bl-item-title {
  font-size: var(--text-base);
  font-weight: 600;
  color: var(--accent, #2563eb);
}
.bl-snippet {
  font-size: var(--text-sm);
  color: var(--text-secondary, #888);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.bl-date {
  font-size: var(--text-2xs);
  color: var(--text-tertiary, #aaa);
}
</style>
