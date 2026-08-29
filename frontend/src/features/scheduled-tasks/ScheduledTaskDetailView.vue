<template>
  <div class="page">
    <HeaderActionsPortal>
      <button type="button" class="header-action" @click="router.push(`/settings/scheduled-tasks/${taskId}/edit`)"><span class="material-symbols-outlined">edit</span></button>
    </HeaderActionsPortal>
    <div v-if="store.detailLoading" class="state">加载中…</div>
    <div v-else-if="store.error" class="state error">{{ store.error }}<button @click="load">重试</button></div>
    <main v-else-if="task" class="content">
      <section class="hero"><div class="title-row"><h1>{{ task.name }}</h1><span :class="['status', task.enabled ? 'on' : 'off']">{{ task.enabled ? '启用' : '停用' }}</span></div><p v-if="task.description">{{ task.description }}</p><div class="chips"><span>{{ taskKindLabel(task.kind) }}</span><span>{{ scheduleKindLabel(task.scheduleKind) }}</span></div></section>
      <section class="section"><h2>执行计划</h2><dl><dt>表达式</dt><dd><code>{{ task.scheduleExpr }}</code></dd><dt>时区</dt><dd>{{ task.timezone }}</dd><dt>下次运行</dt><dd>{{ formatTimestamp(task.nextRunAt) }}</dd><dt>运行次数</dt><dd>{{ task.runCount }}{{ task.maxRuns ? ` / ${task.maxRuns}` : '' }}</dd></dl></section>
      <section class="section"><h2>Payload</h2><pre>{{ formatPayload(task.payload) }}</pre></section>
      <section class="section"><h2>最近运行</h2><div v-if="store.runs.length === 0" class="muted">尚无运行记录</div><div v-else class="runs"><div v-for="run in store.runs" :key="run.id" class="run"><span :class="['run-status', run.status]">{{ run.status }}</span><span>{{ formatTimestamp(run.startedAt) }}</span><span v-if="run.durationMs">{{ run.durationMs }}ms</span><span v-if="run.error" class="run-error">{{ run.error }}</span></div></div></section>
      <div class="actions"><button @click="runNow" :disabled="running">{{ running ? '执行中…' : '立即执行' }}</button><button @click="toggle">{{ task.enabled ? '停用自动化' : '启用自动化' }}</button><button class="danger" @click="remove">删除自动化</button></div>
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'
import { useScheduledTasksStore } from './store'
import { formatPayload, formatTimestamp, scheduleKindLabel, taskKindLabel } from './types'

const route = useRoute(); const router = useRouter(); const store = useScheduledTasksStore()
const taskId = computed(() => route.params.id as string)
const task = computed(() => store.selected)
const running = ref(false)
function load() { return store.loadOne(taskId.value).catch(() => {}) }
async function runNow() {
  if (!task.value || running.value) return
  running.value = true
  try { await store.run(task.value.id); await load() } catch (e: any) { store.error = e?.message || '执行失败' } finally { running.value = false }
}
async function toggle() { if (task.value) await store.update(task.value.id, { enabled: !task.value.enabled }) }
async function remove() { if (!task.value || !window.confirm(`删除自动化「${task.value.name}」？`)) return; await store.remove(task.value.id); router.replace('/settings/scheduled-tasks') }
onMounted(load)
watch(taskId, (id, previous) => { if (id && id !== previous) void load() })
</script>

<style scoped>
.page { min-height: 100%; background: var(--bg-base); } .header-action { color: var(--brand-primary); } .content { display: flex; flex-direction: column; gap: var(--space-3); padding: var(--space-3); padding-bottom: 80px; } .hero, .section { background: var(--bg-card); border: 1px solid var(--border); border-radius: var(--radius-md); padding: var(--space-4); } .title-row { display: flex; align-items: center; gap: 8px; } h1 { flex: 1; margin: 0; font-size: 20px; color: var(--text-primary); } h2 { margin: 0 0 var(--space-3); font-size: 14px; color: var(--text-secondary); } p { color: var(--text-secondary); font-size: 13px; line-height: 1.5; } .status { font-size: 11px; padding: 3px 8px; border-radius: 999px; } .status.on { color: var(--success); background: var(--success-bg); } .status.off { color: var(--text-secondary); background: var(--bg-subtle); } .chips { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 10px; } .chips span { padding: 4px 8px; border-radius: 999px; background: var(--bg-subtle); color: var(--text-secondary); font-size: 11px; } dl { display: grid; grid-template-columns: 90px 1fr; gap: 10px; margin: 0; font-size: 13px; } dt { color: var(--text-muted); } dd { margin: 0; color: var(--text-primary); word-break: break-word; } pre { margin: 0; padding: var(--space-3); overflow: auto; border-radius: var(--radius-sm); background: var(--bg-subtle); color: var(--text-primary); font: 12px/1.5 ui-monospace, monospace; } .muted, .state { color: var(--text-secondary); text-align: center; padding: 30px 12px; } .run { display: flex; gap: 10px; align-items: center; padding: 9px 0; border-bottom: 1px solid var(--border); font-size: 12px; color: var(--text-secondary); } .run:last-child { border-bottom: 0; } .run-status { font-weight: 600; } .run-status.success { color: var(--success); } .run-status.failed { color: var(--danger); } .run-status.running { color: var(--warning); } .run-error { color: var(--danger); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; } .actions { display: flex; gap: 8px; } .actions button, .state button { flex: 1; padding: 10px; border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--bg-card); color: var(--text-primary); } .actions .danger { color: var(--danger); }
</style>
