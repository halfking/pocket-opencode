<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '../../stores/auth'
import { rssSyncRuntime } from './rss-sync-runtime'
import { listQueuedRssSources, removeRejectedRssSource, rssSourceQueueChanged } from './rss-source-outbox'

const auth = useAuthStore()
const { t } = useI18n()
const entries = ref<Awaited<ReturnType<typeof listQueuedRssSources>>>([])
const failed = ref(false)
const pending = computed(() => entries.value.some(entry => !entry.rejection))
let version = 0

async function load() {
  const current = ++version
  if (auth.activationStatus !== 'ready') return
  try {
    const next = await listQueuedRssSources()
    if (current === version) {
      entries.value = next
      failed.value = false
    }
  } catch (error) {
    if (current === version && !(error instanceof Error && error.name === 'AbortError')) failed.value = true
  }
}

async function remove(id: string) {
  const current = version
  try {
    await removeRejectedRssSource(id)
  } catch (error) {
    if (current === version && !(error instanceof Error && error.name === 'AbortError')) failed.value = true
  }
}

watch(() => [auth.activationVersion, auth.activationStatus], () => {
  entries.value = []
  failed.value = false
  void load()
}, { flush: 'sync' })
onMounted(() => {
  window.addEventListener(rssSourceQueueChanged, load)
  void load()
})
onBeforeUnmount(() => {
  version++
  window.removeEventListener(rssSourceQueueChanged, load)
})
</script>

<template>
  <section v-if="entries.length || failed" class="pending-sources" aria-labelledby="pending-source-heading">
    <h3 id="pending-source-heading">{{ t('rssPortal.queueTitle') }}</h3>
    <p v-if="failed" role="alert">{{ t('rssPortal.queueReadFailed') }}</p>
    <p v-if="pending">{{ t('rssPortal.queueWaiting') }}</p>
    <ul>
      <li v-for="entry in entries" :key="entry.id">
        <span>{{ entry.label || t('rssPortal.queueLocked') }}</span>
        <template v-if="entry.rejection">
          <small>{{ t(entry.rejection === 'conflict' ? 'rssPortal.queueConflict' : 'rssPortal.queueInvalid') }}</small>
          <button type="button" @click="remove(entry.id)">{{ t('rssPortal.queueRemove') }}</button>
        </template>
      </li>
    </ul>
    <button v-if="pending" type="button" :disabled="rssSyncRuntime.status.running" @click="rssSyncRuntime.syncNow()">{{ t('common.retry') }}</button>
  </section>
</template>

<style scoped>
.pending-sources { margin: 12px 0; padding: 12px; border: 1px solid var(--border); border-radius: 6px; }
.pending-sources ul { padding-inline-start: 20px; }
.pending-sources li > * { margin-inline-end: 8px; overflow-wrap: anywhere; }
</style>
