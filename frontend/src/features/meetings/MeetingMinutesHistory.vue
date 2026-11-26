<template>
  <details v-if="available" ref="panel" class="minutes-history" @toggle="onToggle">
    <summary>服务端会议纪要</summary>
    <p v-if="loading" role="status">正在读取纪要…</p>
    <p v-if="failed" role="alert">暂时无法读取纪要。</p>
    <p v-if="loaded && !minutes.length && !failed">没有服务端纪要</p>
    <ul v-if="minutes.length">
      <li v-for="row in minutes" :key="row.id">
        <details>
          <summary>{{ row.title || '未命名会议' }}</summary>
          <p>更新时间 <time :datetime="row.updated_at">{{ row.updated_at }}</time></p>
          <p v-if="row.participants.length">{{ row.participants.join('、') }}</p>
          <p v-if="row.minutes" class="minutes-body">{{ row.minutes }}</p>
          <p v-else>此会议暂无纪要</p>
        </details>
      </li>
    </ul>
    <button v-if="failed || (loaded && nextOffset !== null)" type="button" :disabled="loading" @click="load">
      {{ failed ? '重试' : '加载更多纪要' }}
    </button>
  </details>
</template>

<script setup lang="ts">
import { onActivated, onBeforeUnmount, onDeactivated, onMounted, ref, watch } from 'vue'
import { useAuthStore } from '../../stores/auth'
import { readCachedModuleMode } from '../../stores/syncPolicyStore'
import { readMeetingMinutesHistory, type MeetingMinutesSnapshot } from './meeting-minutes-history'
import { captureRecordingOperation } from './recording-operation'

const auth = useAuthStore()
const panel = ref<HTMLDetailsElement | null>(null)
const available = ref(false)
const minutes = ref<MeetingMinutesSnapshot[]>([])
const nextOffset = ref<number | null>(0)
const loading = ref(false)
const loaded = ref(false)
const failed = ref(false)
let request: AbortController | null = null

function clear() {
  request?.abort()
  request = null
  minutes.value = []
  nextOffset.value = 0
  loading.value = loaded.value = failed.value = false
}
function updateAccess() {
  const allowed = auth.activationStatus === 'ready' && Boolean(auth.userId && auth.workspaceId)
    && readCachedModuleMode('meetings') === 'cloud'
  if (!allowed) clear()
  available.value = allowed
}
async function load() {
  const offset = nextOffset.value
  if (!available.value || request || offset === null) return
  const controller = new AbortController()
  request = controller
  loading.value = true
  failed.value = false
  try {
    const operation = captureRecordingOperation('meetings')
    const page = await readMeetingMinutesHistory(operation, offset, controller.signal)
    operation.assertCloud()
    if (request !== controller) return
    const byId = new Map(minutes.value.map(row => [row.id, row]))
    for (const row of page.minutes) byId.set(row.id, row)
    minutes.value = [...byId.values()]
    nextOffset.value = page.nextOffset
    loaded.value = true
  } catch (error) {
    if (request === controller && !(error instanceof Error && error.name === 'AbortError')) failed.value = true
  } finally {
    if (request === controller) {
      request = null
      loading.value = false
    }
  }
}
function onToggle(event: Event) {
  if ((event.currentTarget as HTMLDetailsElement).open) void load()
  else clear()
}
watch(() => [auth.activationVersion, auth.userId, auth.workspaceId, auth.activationStatus], () => {
  clear()
  if (panel.value) panel.value.open = false
  updateAccess()
}, { flush: 'sync' })
onMounted(() => {
  updateAccess()
  window.addEventListener('openpocket:sync-policy-changed', updateAccess)
})
onActivated(updateAccess)
onDeactivated(() => {
  clear()
  if (panel.value) panel.value.open = false
})
onBeforeUnmount(() => {
  clear()
  window.removeEventListener('openpocket:sync-policy-changed', updateAccess)
})
</script>

<style scoped>
.minutes-history { margin: 0.75rem 0; }
.minutes-history summary { cursor: pointer; }
.minutes-history li { padding-block: 0.35rem; }
.minutes-body { white-space: pre-wrap; overflow-wrap: anywhere; }
</style>
