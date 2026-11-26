<template>
  <section aria-label="派发与取消回执" class="commands">
    <div class="command-actions">
      <h3>派发与取消回执</h3>
      <button type="button" :disabled="busy" @click="refresh">刷新回执</button>
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
        <div v-if="row.can_cancel || row.can_retry" class="command-actions">
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
import { cancelRunCommand, readRunCommands, retryRunCommand, type RunCommandReceipt } from '../../api/accUnifiedTasks'
import { AccApiError, type AccRequestAuthority } from '../../api/accApi'

const props = defineProps<{ runId: string; authority: AccRequestAuthority }>()
const commands = ref<RunCommandReceipt[] | null>(null)
type CommandAction = 'cancel' | 'retry'
const confirming = ref<{ id: string; action: CommandAction } | null>(null)
const busy = ref(false)
const error = ref('')
const controller = new AbortController()
const labels = { requested: '取消已请求', delivered: '取消已送达', effective: '取消已生效' }
const activeStatuses = new Set(['created', 'launching', 'leased', 'running', 'settling'])
let pollUntil = 0
let timer: ReturnType<typeof setTimeout> | undefined

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
    const rows = await readRunCommands(props.authority, props.runId, controller.signal)
    if (controller.signal.aborted) return
    commands.value = rows
    error.value = ''
    if (rows.some(row => row.cancel_state === 'requested' || row.cancel_state === 'delivered'
      || row.retry_of_dispatch_id && activeStatuses.has(row.status))) schedule()
  } catch (cause) {
    if (controller.signal.aborted) return
    if (cause instanceof AccApiError && [401, 403, 404].includes(cause.status)) {
      commands.value = null
      confirming.value = null
    } else {
      schedule()
    }
    error.value = '派发回执暂不可读取。'
  } finally {
    busy.value = false
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
