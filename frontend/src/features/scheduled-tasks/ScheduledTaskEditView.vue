<template>
  <div class="page">
    <HeaderActionsPortal>
      <button type="button" class="back-btn" @click="router.back()" aria-label="返回">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>
      <button type="button" class="save-link" :disabled="saving" @click="save">{{ saving ? '保存中…' : '保存' }}</button>
    </HeaderActionsPortal>
    <form class="form" @submit.prevent="save">
      <label>名称 *<input v-model="form.name" required maxlength="120" placeholder="例如：工作日晨报" /></label>
      <label>描述<textarea v-model="form.description" rows="2" placeholder="可选说明" /></label>
      <label>自动化类型<select v-model="form.kind"><option v-for="item in TASK_KINDS" :key="item.value" :value="item.value">{{ item.label }}</option></select></label>
      <fieldset><legend>执行计划</legend>
        <div class="chips"><button v-for="item in SCHEDULE_KINDS" :key="item.value" type="button" :class="{ active: form.scheduleKind === item.value }" @click="form.scheduleKind = item.value">{{ item.label }}</button></div>
        <input v-model="form.scheduleExpr" required :placeholder="scheduleHint" />
        <small>{{ scheduleHint }}</small>
        <input v-model="form.timezone" placeholder="时区，例如 Asia/Shanghai" />
      </fieldset>
      <label>Payload（JSON）<textarea v-model="form.payloadText" rows="8" spellcheck="false" placeholder='{"message":"Hello"}' /></label>
      <div class="grid"><label>最大运行次数<input v-model.number="form.maxRuns" type="number" min="0" /></label><label>冷却（秒）<input v-model.number="form.cooldownSec" type="number" min="0" /></label></div>
      <label>超时（秒）<input v-model.number="form.timeoutSec" type="number" min="1" max="86400" /></label>
      <label class="checkbox"><input v-model="form.enabled" type="checkbox" /> 创建后启用</label>
      <p v-if="error" class="error" role="alert">{{ error }}</p>
      <div class="actions"><button type="button" @click="router.back()">取消</button><button class="primary" type="submit" :disabled="saving">{{ isEdit ? '保存修改' : '创建自动化' }}</button></div>
    </form>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'
import { useScheduledTasksStore } from './store'
import { SCHEDULE_KINDS, TASK_KINDS, formatPayload, type ScheduleKind, type ScheduledTask } from './types'

const route = useRoute(); const router = useRouter(); const store = useScheduledTasksStore()
const taskId = computed(() => route.params.id as string | undefined)
const isEdit = computed(() => Boolean(taskId.value))
const saving = ref(false); const error = ref('')
const form = reactive({ name: '', description: '', kind: TASK_KINDS[0].value, scheduleKind: 'cron' as ScheduleKind, scheduleExpr: '0 9 * * 1-5', timezone: 'Asia/Shanghai', payloadText: '{}', enabled: true, maxRuns: 0, cooldownSec: 0, timeoutSec: 120 })
const scheduleHint = computed(() => SCHEDULE_KINDS.find((item) => item.value === form.scheduleKind)?.hint || '')

function hydrate(task: ScheduledTask) { Object.assign(form, { name: task.name, description: task.description || '', kind: task.kind, scheduleKind: task.scheduleKind, scheduleExpr: task.scheduleExpr, timezone: task.timezone || 'Asia/Shanghai', payloadText: formatPayload(task.payload), enabled: task.enabled, maxRuns: task.maxRuns || 0, cooldownSec: task.cooldownSec || 0, timeoutSec: task.timeoutSec || 120 }) }
async function load() { if (!taskId.value) return; try { hydrate(await store.loadOne(taskId.value)) } catch (e: any) { error.value = e?.message || '加载失败' } }
async function save() {
  error.value = ''
  let payload: unknown
  try { payload = JSON.parse(form.payloadText || '{}') } catch { error.value = 'Payload 必须是合法 JSON'; return }
  saving.value = true
  try {
    const input = { name: form.name.trim(), description: form.description.trim(), kind: form.kind, scheduleKind: form.scheduleKind, scheduleExpr: form.scheduleExpr.trim(), timezone: form.timezone.trim() || 'Asia/Shanghai', payload, enabled: form.enabled, maxRuns: form.maxRuns || 0, cooldownSec: form.cooldownSec || 0, timeoutSec: form.timeoutSec || 120 }
    const saved = isEdit.value ? await store.update(taskId.value!, input) : await store.create(input)
    router.replace(`/settings/scheduled-tasks/${saved.id}`)
  } catch (e: any) { error.value = e?.message || '保存失败' } finally { saving.value = false }
}
onMounted(load)
watch(taskId, (id, previous) => { if (id && id !== previous) void load() })
</script>

<style scoped>
.page { min-height: 100%; background: var(--bg-base); }
.back-btn { background: none; border: none; color: var(--text-primary); display: flex; align-items: center; padding: 4px; cursor: pointer; }
.save-link { color: var(--brand-primary); font-weight: 600; }
.form { display: flex; flex-direction: column; gap: var(--space-4); padding: var(--space-4) var(--space-3) 100px; } label, legend { font-size: 13px; font-weight: 600; color: var(--text-secondary); } label { display: flex; flex-direction: column; gap: 6px; }
input, textarea, select { width: 100%; box-sizing: border-box; padding: 10px 12px; border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--bg-card); color: var(--text-primary); font: inherit; font-size: 14px; } textarea { resize: vertical; } fieldset { border: 1px solid var(--border); border-radius: var(--radius-md); padding: var(--space-3); display: flex; flex-direction: column; gap: var(--space-2); } small { color: var(--text-muted); font-weight: 400; }
.chips { display: flex; gap: 7px; } .chips button, .actions button { padding: 9px 12px; border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--bg-card); color: var(--text-primary); cursor: pointer; } .chips button { flex: 1; } .chips button.active { background: var(--brand-primary); color: var(--text-inverse); border-color: var(--brand-primary); }
.grid { display: grid; grid-template-columns: 1fr 1fr; gap: var(--space-3); } .checkbox { flex-direction: row; align-items: center; } .checkbox input { width: auto; } .actions { display: flex; gap: var(--space-3); } .actions button { flex: 1; } .actions .primary { color: var(--text-inverse); background: var(--brand-gradient); border: 0; } .error { margin: 0; padding: var(--space-3); color: var(--danger); background: var(--danger-bg); border-radius: var(--radius-sm); }
</style>
