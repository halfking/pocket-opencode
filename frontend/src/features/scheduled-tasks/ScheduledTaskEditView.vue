<template>
  <div class="page">
    <HeaderActionsPortal>
      <button type="button" class="back-btn" @click="goBack" aria-label="返回">
        <span class="material-symbols-outlined">arrow_back</span>
      </button>
      <button type="button" class="save-link" :disabled="saving" @click="save">{{ saving ? '保存中…' : '保存' }}</button>
    </HeaderActionsPortal>
    <form class="form" @submit.prevent="save">
      <label>名称 *<input v-model="form.name" required maxlength="120" placeholder="例如：工作日晨报" /></label>
      <PromptOptimizeField v-model="form.description" label="说明" single-line placeholder="可选说明，写完可点优化" />
      <label>任务类型
        <select v-model="form.kind">
          <option v-for="item in TASK_KINDS" :key="item.value" :value="item.value">{{ item.label }}</option>
        </select>
      </label>
      <SchedulePlanFields v-model="plan">
        <div v-if="previewTimes.length" class="preview" aria-live="polite">
          <span class="preview-label">接下来会在</span>
          <span v-for="(t, i) in previewTimes.slice(0, 3)" :key="i" class="preview-time">{{ formatTimestamp(t) }}</span>
        </div>
        <small v-else-if="previewError" class="preview-err" role="status">{{ previewError }}</small>
        <small v-else-if="previewEmpty" class="preview-empty">没有未来的执行时间。一次性计划如果已过期，保存后不会自动运行。</small>
      </SchedulePlanFields>
      <PromptOptimizeField
        v-if="showPrompt"
        v-model="form.prompt"
        label="任务提示词"
        placeholder="到点时希望助手做的事，可语音输入或点优化"
      />
      <details class="advanced">
        <summary>高级选项</summary>
        <label v-if="!showPrompt">任务参数（JSON）
          <textarea v-model="form.payloadText" rows="6" spellcheck="false" placeholder='{"url":"https://example.com"}' />
        </label>
        <div class="grid">
          <label>最多执行次数<input v-model.number="form.maxRuns" type="number" min="0" /></label>
          <label>两次间隔（秒）<input v-model.number="form.cooldownSec" type="number" min="0" /></label>
        </div>
        <label>单次超时（秒）<input v-model.number="form.timeoutSec" type="number" min="1" max="86400" /></label>
      </details>
      <label class="checkbox"><input v-model="form.enabled" type="checkbox" /> 创建后启用</label>
      <p v-if="error" class="error" role="alert">{{ error }}</p>
      <div class="actions">
        <button type="button" @click="goBack">取消</button>
        <button class="primary" type="submit" :disabled="saving">{{ isEdit ? '保存修改' : '创建任务' }}</button>
      </div>
    </form>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import HeaderActionsPortal from '../../components/layout/HeaderActionsPortal.vue'
import { useScheduledTasksStore } from './store'
import { scheduledTasksApi } from './api'
import { TASK_KINDS, formatPayload, formatTimestamp, type ScheduledTask } from './types'
import { defaultSchedulePlan, encodeSchedule, parseSchedule } from './schedule-plan'
import { applyPrompt, extractPrompt, promptFieldForKind } from './task-prompt'
import PromptOptimizeField from './PromptOptimizeField.vue'
import SchedulePlanFields from './SchedulePlanFields.vue'
import { markListDirty } from '../../composables/list-scene-store'

const route = useRoute()
const router = useRouter()
const store = useScheduledTasksStore()
const taskId = computed(() => route.params.id as string | undefined)
const isEdit = computed(() => Boolean(taskId.value))
const saving = ref(false)
const error = ref('')
const form = reactive({
  name: '',
  description: '',
  kind: TASK_KINDS[0].value,
  prompt: '',
  payloadText: '{}',
  enabled: true,
  maxRuns: 0,
  cooldownSec: 0,
  timeoutSec: 120,
})
const plan = reactive(defaultSchedulePlan())
const showPrompt = computed(() => promptFieldForKind(form.kind) !== 'none')

function goBack() {
  if (window.history.length > 1 && window.history.state?.back) router.back()
  else router.push('/settings/scheduled-tasks')
}

function hydrate(task: ScheduledTask) {
  Object.assign(form, {
    name: task.name,
    description: task.description || '',
    kind: task.kind,
    prompt: extractPrompt(task.kind, task.payload),
    payloadText: formatPayload(task.payload),
    enabled: task.enabled,
    maxRuns: task.maxRuns || 0,
    cooldownSec: task.cooldownSec || 0,
    timeoutSec: task.timeoutSec || 120,
  })
  Object.assign(plan, parseSchedule(task.scheduleKind, task.scheduleExpr))
  plan.timezone = task.timezone || plan.timezone
}

async function load() {
  if (!taskId.value) return
  try { hydrate(await store.loadOne(taskId.value)) }
  catch (e: any) { error.value = e?.message || '加载失败' }
}

function parsePayloadText(): unknown {
  try { return JSON.parse(form.payloadText || '{}') }
  catch { return null }
}

async function save() {
  error.value = ''
  const encoded = encodeSchedule(plan)
  let payload: unknown
  if (showPrompt.value) {
    payload = applyPrompt(form.kind, parsePayloadText() ?? {}, form.prompt)
  } else {
    payload = parsePayloadText()
    if (payload === null) { error.value = '任务参数必须是合法 JSON'; return }
  }
  if (showPrompt.value && !form.prompt.trim()) {
    error.value = '请填写任务提示词'
    return
  }
  saving.value = true
  try {
    const input = {
      name: form.name.trim(),
      description: form.description.trim(),
      kind: form.kind,
      scheduleKind: encoded.scheduleKind,
      scheduleExpr: encoded.scheduleExpr,
      timezone: plan.timezone || 'Asia/Shanghai',
      payload,
      enabled: form.enabled,
      maxRuns: form.maxRuns || 0,
      cooldownSec: form.cooldownSec || 0,
      timeoutSec: form.timeoutSec || 120,
    }
    const saved = isEdit.value ? await store.update(taskId.value!, input) : await store.create(input)
    markListDirty('scheduled-tasks')
    router.replace(`/settings/scheduled-tasks/${saved.id}`)
  } catch (e: any) { error.value = e?.message || '保存失败' }
  finally { saving.value = false }
}

onMounted(load)
watch(taskId, (id, previous) => { if (id && id !== previous) void load() })

const previewTimes = ref<number[]>([])
const previewError = ref('')
const previewEmpty = ref(false)
let previewTimer: ReturnType<typeof setTimeout> | null = null

function requestPreview() {
  previewError.value = ''
  previewEmpty.value = false
  if (previewTimer) clearTimeout(previewTimer)
  const encoded = encodeSchedule(plan)
  const timezone = plan.timezone || 'Asia/Shanghai'
  if (!encoded.scheduleExpr) { previewTimes.value = []; return }
  previewTimer = setTimeout(async () => {
    const latest = encodeSchedule(plan)
    if (latest.scheduleKind !== encoded.scheduleKind || latest.scheduleExpr !== encoded.scheduleExpr) return
    try {
      const res = await scheduledTasksApi.preview({ scheduleKind: encoded.scheduleKind, scheduleExpr: encoded.scheduleExpr, timezone })
      previewTimes.value = res.next ?? []
      previewEmpty.value = previewTimes.value.length === 0
    } catch (e: any) {
      previewTimes.value = []
      previewError.value = e?.status === 400 ? (e?.body?.error || '计划无法识别，请检查日期和时间') : '预览失败，请稍后重试'
    }
  }, 500)
}

watch(() => ({ ...plan }), requestPreview, { deep: true })
onBeforeUnmount(() => { if (previewTimer) clearTimeout(previewTimer) })
</script>

<style scoped>
.page { min-height: 100%; background: var(--bg-base); }
.back-btn { background: none; border: none; color: var(--text-primary); display: flex; align-items: center; padding: 4px; cursor: pointer; }
.save-link { color: var(--brand-primary); font-weight: 600; }
.form { display: flex; flex-direction: column; gap: var(--space-4); padding: var(--space-4) var(--space-3) 100px; }
label { font-size: 13px; font-weight: 600; color: var(--text-secondary); display: flex; flex-direction: column; gap: 6px; }
input, textarea, select { width: 100%; box-sizing: border-box; padding: 10px 12px; border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--bg-card); color: var(--text-primary); font: inherit; font-size: 14px; }
textarea { resize: vertical; }
small { color: var(--text-muted); font-weight: 400; }
.advanced { border: 1px solid var(--border); border-radius: var(--radius-md); padding: var(--space-3); display: flex; flex-direction: column; gap: var(--space-3); }
.advanced summary { font-size: 13px; font-weight: 600; color: var(--text-secondary); cursor: pointer; }
.grid { display: grid; grid-template-columns: 1fr 1fr; gap: var(--space-3); }
.checkbox { flex-direction: row; align-items: center; }
.checkbox input { width: auto; }
.actions { display: flex; gap: var(--space-3); }
.actions button { flex: 1; padding: 9px 12px; border: 1px solid var(--border); border-radius: var(--radius-sm); background: var(--bg-card); color: var(--text-primary); cursor: pointer; }
.actions .primary { color: var(--text-inverse); background: var(--brand-gradient); border: 0; }
.error { margin: 0; padding: var(--space-3); color: var(--danger); background: var(--danger-bg); border-radius: var(--radius-sm); }
.preview { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; font-size: 11px; color: var(--text-muted); }
.preview-time { padding: 3px 8px; background: var(--bg-subtle); border: 1px solid var(--border); border-radius: 999px; color: var(--text-secondary); font-size: 11px; }
.preview-err { color: var(--warning, #f59e0b); }
.preview-empty { color: var(--text-muted); }
</style>
