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
        </div>
        <div v-if="row.can_cancel" class="command-actions">
          <template v-if="confirming === row.dispatch_id">
            <button type="button" :disabled="busy" @click="cancel(row.dispatch_id)">确认取消</button>
            <button type="button" @click="confirming = null">返回</button>
          </template>
          <button v-else type="button" :disabled="busy" @click="confirming = row.dispatch_id">取消派发</button>
        </div>
      </li>
    </ul>
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { cancelRunCommand, readRunCommands, type RunCommandReceipt } from '../../api/accUnifiedTasks'
import { AccApiError, type AccRequestAuthority } from '../../api/accApi'

const props = defineProps<{ runId: string; authority: AccRequestAuthority }>()
const commands = ref<RunCommandReceipt[] | null>(null)
const confirming = ref<string | null>(null)
const busy = ref(false)
const error = ref('')
const controller = new AbortController()
const labels = { requested: '取消已请求', delivered: '取消已送达', effective: '取消已生效' }
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
    if (rows.some(row => row.cancel_state === 'requested' || row.cancel_state === 'delivered')) schedule()
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

async function cancel(dispatchId: string) {
  if (controller.signal.aborted || busy.value) return
  clearTimeout(timer)
  busy.value = true
  confirming.value = null
  error.value = ''
  try {
    const row = await cancelRunCommand(props.authority, props.runId, dispatchId, controller.signal)
    if (controller.signal.aborted) return
    commands.value = commands.value?.map(command => command.dispatch_id === dispatchId ? row : command) ?? null
  } catch {
    if (!controller.signal.aborted) {
      commands.value = null
      error.value = '未能确认取消请求。请刷新回执。'
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
