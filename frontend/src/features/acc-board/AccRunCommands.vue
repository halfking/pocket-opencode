<template>
  <section aria-label="派发与取消回执" class="commands">
    <div class="command-actions">
      <h3>派发与取消回执</h3>
      <button type="button" :disabled="busy" @click="refresh">刷新回执</button>
    </div>
    <template v-if="runCancellation?.can_cancel">
      <div v-if="confirmingRun" class="command-actions">
        <button type="button" :disabled="busy" @click="cancelRun">确认取消整个 Run</button>
        <button type="button" @click="confirmingRun = false">返回</button>
      </div>
      <button v-else type="button" :disabled="busy" @click="confirming = null; confirmingRun = true">取消整个 Run</button>
    </template>
    <div v-if="runCancellation?.event_id" role="status">
      <p>Run 已取消 · 已关闭 {{ runCancellation.cancelled_tasks }} 个节点</p>
      <p>{{ runCancellation.dispatches }} 条派发 · 未领取 {{ runCancellation.never_claimed }} · 等待执行器 {{ runCancellation.awaiting_executor }} · 已送达 {{ runCancellation.delivered }} · 已生效 {{ runCancellation.effective }}</p>
    </div>
    <p v-if="error" role="alert">{{ error }}</p>
    <p v-if="commands?.length === 0">暂无派发。</p>
    <ul v-if="commands?.length">
      <li v-for="row in commands" :key="row.dispatch_id">
        <div :title="row.dispatch_id">
          {{ row.task_id }} #{{ row.attempt }}<template v-if="!row.cancel_state"> · {{ row.status }}</template>
          <span v-if="row.cancel_state" class="command-stage" role="status">
            {{ labels[row.cancel_state] }}<template v-if="row.cancel_state !== 'requested' && !row.verified_executor"> · 旧协议回执</template>
          </span>
          <span v-if="row.retry_of_dispatch_id" class="command-stage" role="status">从派发重跑 {{ row.retry_of_dispatch_id }}</span>
        </div>
        <div v-if="!confirmingRun && !runCancellation?.event_id && (row.can_cancel || row.can_retry)" class="command-actions">
          <template v-if="confirming?.id === row.dispatch_id && (confirming.action === 'cancel' ? row.can_cancel : row.can_retry)">
            <button type="button" :disabled="busy" @click="change(row, confirming.action)">{{ confirming.action === 'cancel' ? '确认取消' : '确认重跑' }}</button>
            <button type="button" @click="confirming = null">返回</button>
          </template>
          <template v-else>
            <button v-if="row.can_cancel" type="button" :disabled="busy" @click="confirming = { id: row.dispatch_id, action: 'cancel' }">取消派发</button>
            <button v-if="row.can_retry" type="button" :disabled="busy" @click="confirming = { id: row.dispatch_id, action: 'retry' }">重跑派发</button>
          </template>
        </div>
      </li>
    </ul>
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { cancelRunCommand, cancelWholeRun, readRunCommands, retryRunCommand,
  type RunCancellation, type RunCommandReceipt } from '../../api/accUnifiedTasks'
import { AccApiError, type AccRequestAuthority } from '../../api/accApi'

const props = defineProps<{ runId: string; authority: AccRequestAuthority }>()
const emit = defineEmits<{ cancelled: [] }>()
const commands = ref<RunCommandReceipt[] | null>(null)
const runCancellation = ref<RunCancellation>()
const confirmingRun = ref(false)
type CommandAction = 'cancel' | 'retry'
const confirming = ref<{ id: string; action: CommandAction } | null>(null)
const busy = ref(false)
const error = ref('')
const controller = new AbortController()
const labels = { requested: '取消已请求', delivered: '取消已送达', effective: '取消已生效' }
const activeStatuses = new Set(['created', 'launching', 'leased', 'running', 'settling'])
let pollUntil = 0
let timer: ReturnType<typeof setTimeout> | undefined
let notifiedRun = false

function showRunCancellation(receipt?: RunCancellation) {
  runCancellation.value = receipt
  if (receipt?.event_id && !notifiedRun) {
    notifiedRun = true
    emit('cancelled')
  }
}

function schedule() {
  if (!controller.signal.aborted && Date.now() < pollUntil) {
    timer = setTimeout(() => void read(), 2000)
  }
}

async function read() {
  if (controller.signal.aborted || busy.value) return
  clearTimeout(timer)
  busy.value = true
  try {
    const { commands: rows, run_cancellation: cancellation } = await readRunCommands(props.authority, props.runId, controller.signal)
    if (controller.signal.aborted) return
    commands.value = rows
    showRunCancellation(cancellation)
    error.value = ''
    if (rows.some(row => row.cancel_state === 'requested' || row.cancel_state === 'delivered'
      || row.retry_of_dispatch_id && activeStatuses.has(row.status))
      || cancellation && cancellation.awaiting_executor + cancellation.delivered > 0) schedule()
  } catch (cause) {
    if (controller.signal.aborted) return
    if (cause instanceof AccApiError && [401, 403, 404].includes(cause.status)) {
      commands.value = null
      confirming.value = null
      runCancellation.value = undefined
      confirmingRun.value = false
    } else {
      schedule()
    }
    error.value = '派发回执暂不可读取。'
  } finally {
    busy.value = false
  }
}

async function cancelRun() {
  if (controller.signal.aborted || busy.value) return
  clearTimeout(timer)
  busy.value = true
  confirmingRun.value = false
  error.value = ''
  try {
    const receipt = await cancelWholeRun(props.authority, props.runId, controller.signal)
    if (!controller.signal.aborted) showRunCancellation(receipt)
  } catch {
    if (!controller.signal.aborted) {
      commands.value = null
      runCancellation.value = undefined
      error.value = '未能确认整 Run 的取消。请刷新回执。'
    }
  } finally {
    busy.value = false
    pollUntil = Date.now() + 30_000
    schedule()
  }
}

async function change(previous: RunCommandReceipt, action: CommandAction) {
  if (controller.signal.aborted || busy.value) return
  clearTimeout(timer)
  busy.value = true
  confirming.value = null
  error.value = ''
  try {
    const row = action === 'cancel'
      ? await cancelRunCommand(props.authority, props.runId, previous.dispatch_id, controller.signal)
      : await retryRunCommand(props.authority, props.runId, previous, controller.signal)
    if (controller.signal.aborted) return
    if (!commands.value) return
    if (action === 'cancel') {
      commands.value = commands.value.map(command => command.dispatch_id === previous.dispatch_id ? row : command)
    } else {
      commands.value = commands.value.filter(command => command.dispatch_id !== row.dispatch_id).map(command =>
        command.dispatch_id === previous.dispatch_id ? { ...command, can_retry: false } : command)
      commands.value.push(row)
    }
  } catch {
    if (!controller.signal.aborted) {
      commands.value = null
      error.value = action === 'cancel' ? '未能确认取消请求。请刷新回执。' : '未能确认重跑请求。请刷新回执。'
    }
  } finally {
    busy.value = false
    pollUntil = Date.now() + 30_000
    schedule()
  }
}

function refresh() {
  pollUntil = Date.now() + 30_000
  void read()
}

onMounted(refresh)
onBeforeUnmount(() => {
  controller.abort()
  clearTimeout(timer)
})
</script>

<style scoped>
.commands { border: 1px solid var(--border-color, #aab0ba); border-radius: .75rem; padding: .75rem; }
.command-actions { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: .5rem; }
.commands li { margin-block: .5rem; }
.command-stage { display: block; font-weight: 600; }
</style>
