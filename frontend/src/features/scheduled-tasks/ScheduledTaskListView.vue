<template>
  <div class="page">
    <HeaderActionsPortal>
      <button type="button" class="header-action" aria-label="创建自动化" @click="router.push('/settings/scheduled-tasks/new')">
        <span class="material-symbols-outlined">add</span>
      </button>
    </HeaderActionsPortal>

    <div class="toolbar">
      <label class="filter"><input v-model="enabledOnly" type="checkbox" @change="load" /> 仅显示启用</label>
      <button type="button" class="refresh" :disabled="store.loading" @click="load">刷新</button>
    </div>
    <div v-if="store.error" class="error" role="alert">{{ store.error }} <button @click="load">重试</button></div>
    <div v-if="store.loading" class="state">加载中…</div>
    <div v-else-if="store.tasks.length === 0" class="state"><p>还没有自动化任务</p><button class="primary" @click="router.push('/settings/scheduled-tasks/new')">创建自动化</button></div>
    <main v-else class="list">
      <article v-for="task in store.tasks" :key="task.id" class="card" @click="open(task.id)">
        <div class="card-head"><h2>{{ task.name }}</h2><span :class="['status', task.enabled ? 'on' : 'off']">{{ task.enabled ? '启用' : '停用' }}</span></div>
        <p v-if="task.description" class="description">{{ task.description }}</p>
        <div class="meta"><span>{{ taskKindLabel(task.kind) }}</span><span>{{ scheduleKindLabel(task.scheduleKind) }} · {{ task.scheduleExpr }}</span></div>
        <div class="meta secondary"><span>下次 {{ formatTimestamp(task.nextRunAt) }}</span><span v-if="task.lastStatus">上次 {{ task.lastStatus }}</span></div>
        <div class="card-actions" @click.stop>
          <button type="button" @click="toggle(task)">{{ task.enabled ? '停用' : '启用' }}</button>
          <button type="button" @click="router.push(`/settings/scheduled-tasks/${task.id}/edit`)">编辑</button>
          <button type="button" class="danger" @click="remove(task)">删除</button>
        </div>
      </article>
    </main>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'
import { useScheduledTasksStore } from './store'
import { formatTimestamp, scheduleKindLabel, taskKindLabel, type ScheduledTask } from './types'

const router = useRouter()
const store = useScheduledTasksStore()
const enabledOnly = ref(false)

function load() { return store.load(enabledOnly.value).catch(() => {}) }
async function toggle(task: ScheduledTask) { await store.update(task.id, { enabled: !task.enabled }).catch(() => {}) }
async function remove(task: ScheduledTask) {
  if (!window.confirm(`删除自动化「${task.name}」？`)) return
  await store.remove(task.id).catch(() => {})
}
function open(id: string) { router.push(`/settings/scheduled-tasks/${id}`) }
onMounted(load)
</script>

<style scoped>
.page { min-height: 100%; background: var(--bg-base); }
.header-action { color: var(--brand-primary); }
.toolbar { display: flex; justify-content: space-between; align-items: center; padding: var(--space-3); border-bottom: 1px solid var(--border); }
.filter { color: var(--text-secondary); font-size: 13px; display: flex; gap: 8px; align-items: center; }
.refresh, .card-actions button, .primary, .error button { border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--bg-card); color: var(--text-primary); padding: 7px 12px; cursor: pointer; }
.list { display: flex; flex-direction: column; gap: var(--space-2); padding: var(--space-3); }
.card { padding: var(--space-3); border: 1px solid var(--border); border-radius: var(--radius-md); background: var(--bg-card); cursor: pointer; }
.card-head { display: flex; align-items: center; gap: 8px; } h2 { flex: 1; margin: 0; font-size: 15px; color: var(--text-primary); }
.status { font-size: 11px; padding: 3px 8px; border-radius: 999px; } .status.on { color: var(--success); background: var(--success-bg); } .status.off { color: var(--text-secondary); background: var(--bg-subtle); }
.description { margin: 7px 0; font-size: 13px; color: var(--text-secondary); }
.meta { display: flex; flex-wrap: wrap; gap: 10px; font-size: 12px; color: var(--text-primary); margin-top: 7px; } .meta.secondary { color: var(--text-muted); }
.card-actions { display: flex; gap: 7px; margin-top: 11px; } .card-actions button { flex: 1; font-size: 12px; } .card-actions .danger { color: var(--danger); }
.state { padding: 48px 20px; text-align: center; color: var(--text-secondary); } .primary { color: var(--text-inverse); background: var(--brand-gradient); border: 0; }
.error { margin: var(--space-3); padding: var(--space-3); color: var(--danger); background: var(--danger-bg); border-radius: var(--radius-sm); font-size: 13px; } .error button { margin-left: 8px; }
</style>
