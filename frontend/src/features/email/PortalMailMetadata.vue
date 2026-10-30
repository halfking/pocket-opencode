<template>
  <details class="portal-mail" @toggle="onToggle">
    <summary>{{ t('email.portalMetadata') }} <span aria-hidden="true">({{ rows.length }})</span></summary>
    <p>{{ t('email.portalMetadataHint') }}</p>
    <div v-if="runtime.status.failed || runtime.status.blocked || runtime.status.resetPending" role="alert">
      {{ t('email.portalMetadataPending') }}
    </div>
    <p v-if="error" role="alert">{{ error }}</p>
    <button type="button" :disabled="runtime.status.running || busy" @click="refresh">{{ t('common.retry') }}</button>
    <ul v-if="rows.length">
      <li v-for="row in rows.slice(0, visible)" :key="row.id">
        <strong>{{ row.item.title }}</strong>
        <p v-if="row.item.kind === 'account'">{{ row.item.payload.address }}</p>
        <template v-else><p>{{ row.item.payload.from }}</p><p class="snippet">{{ row.item.payload.snippet }}</p></template>
        <button type="button" :disabled="busy" @click="openEditor(row)">{{ t('common.edit') }}</button>
        <button type="button" :disabled="busy" @click="remove(row)">{{ t('common.delete') }}</button>
      </li>
    </ul>
    <p v-else>{{ t('email.portalMetadataEmpty') }}</p>
    <button v-if="rows.length > visible" type="button" @click="visible += 20">{{ t('email.portalMetadataMore') }}</button>
    <form v-if="editing" @submit.prevent="save">
      <label>{{ t('email.portalMetadataTitle') }}<input v-model="draft.title" required /></label>
      <template v-for="field in fields" :key="field">
        <label>{{ t(`email.portalMetadataField.${field}`) }}<textarea v-if="field === 'snippet'" v-model="draft.payload[field]" rows="5" /><input v-else v-model="draft.payload[field]" /></label>
      </template>
      <button type="submit" :disabled="busy">{{ t('common.save') }}</button>
      <button type="button" :disabled="busy" @click="editing = null">{{ t('common.cancel') }}</button>
    </form>
  </details>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useConfirm } from '../../composables/useConfirm'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '../../stores/auth'
import { captureEmailOwnership } from './email-ownership'
import { deletePortalMailMetadata, editPortalMailMetadata, listPortalMailMetadata } from './portal-mail-store'
import { portalEmailRuntime as runtime, syncPortalMailNow } from './portal-mail-runtime'

const { t } = useI18n()
const { confirm } = useConfirm(), auth = useAuthStore()
type Metadata = Awaited<ReturnType<typeof listPortalMailMetadata>>[number]
const rows = ref<Metadata[]>([]), editing = ref<Metadata | null>(null), busy = ref(false), error = ref(''), visible = ref(20)
const draft = reactive({ title: '', payload: {} as Record<string, string> })
let ownerGeneration = 0, loadGeneration = 0, editorDb: ReturnType<typeof captureEmailOwnership>['db'] | null = null
const fields = computed(() => editing.value?.item.kind === 'account' ? ['address', 'display_name', 'last_sync_at'] : ['from', 'subject', 'snippet', 'received_at'])
async function load() {
  const current = ++loadGeneration, owner = ownerGeneration
  try {
    const { db } = captureEmailOwnership()
    const next = await listPortalMailMetadata(db)
    db.assertCurrent()
    if (owner === ownerGeneration && current === loadGeneration) rows.value = next
  } catch (cause) {
    if (owner === ownerGeneration && current === loadGeneration && !(cause instanceof Error && cause.name === 'AbortError')) error.value = t('email.portalMetadataPending')
  }
}
async function refresh() { await syncPortalMailNow(); await load() }
function onToggle(event: Event) { if ((event.target as HTMLDetailsElement).open) void load() }
function openEditor(row: Metadata) {
  try {
    const { db } = captureEmailOwnership()
    editorDb = db
    editing.value = row
    draft.title = row.item.title
    draft.payload = Object.fromEntries(Object.entries(row.item.payload).map(([key, value]) => [key, String(value)]))
    error.value = ''
  } catch { error.value = t('email.portalMetadataPending') }
}
async function save() {
  const row = editing.value, db = editorDb
  if (!row || !db || busy.value) return
  const owner = ownerGeneration, patch = { title: draft.title, payload: { ...draft.payload } }
  busy.value = true
  try {
    await editPortalMailMetadata(row.id, patch, db)
    db.assertCurrent()
    if (owner === ownerGeneration) { editing.value = null; editorDb = null; await load() }
  } catch (cause) {
    if (owner === ownerGeneration && !(cause instanceof Error && cause.name === 'AbortError')) error.value = t('email.portalMetadataPending')
  } finally { if (owner === ownerGeneration) busy.value = false }
}
async function remove(row: Metadata) {
  if (busy.value) return
  // §854/BUG-AQ：裸 window.confirm 在 Android WebView 里同步阻塞渲染进程，改走全局 ConfirmDialog。
  if (!await confirm({ message: t('email.portalMetadataDelete'), danger: true })) return
  const owner = ownerGeneration
  try {
    const { db } = captureEmailOwnership()
    busy.value = true
    await deletePortalMailMetadata(row.id, db)
    db.assertCurrent()
    if (owner === ownerGeneration) await load()
  }
  catch (cause) { if (owner === ownerGeneration && !(cause instanceof Error && cause.name === 'AbortError')) error.value = t('email.portalMetadataPending') }
  finally { if (owner === ownerGeneration) busy.value = false }
}
const refreshList = () => { void load() }
watch(() => [auth.activationVersion, auth.activationStatus, auth.userId, auth.workspaceId], () => {
  ownerGeneration++; loadGeneration++; rows.value = []; editing.value = null; editorDb = null; draft.title = ''; draft.payload = {}; busy.value = false; error.value = ''; visible.value = 20
  if (auth.activationStatus === 'ready') void load()
}, { flush: 'sync' })
onMounted(() => { void load(); window.addEventListener('openpocket:portal-email-applied', refreshList); window.addEventListener('openpocket:portal-email-outbox-changed', refreshList) })
onBeforeUnmount(() => { ownerGeneration++; loadGeneration++; window.removeEventListener('openpocket:portal-email-applied', refreshList); window.removeEventListener('openpocket:portal-email-outbox-changed', refreshList) })
</script>

<style scoped>
.portal-mail { margin: 12px; padding: 12px; border: 1px solid var(--line, #d4d4d4); border-radius: 8px; }
summary { cursor: pointer; font-weight: 600; }
ul { list-style: none; padding: 0; }
li { padding: 12px 0; border-top: 1px solid var(--line, #d4d4d4); }
.snippet { white-space: pre-wrap; overflow-wrap: anywhere; }
button { margin: 4px 8px 4px 0; }
form, label { display: grid; gap: 6px; margin-top: 12px; }
input, textarea { width: 100%; box-sizing: border-box; }
</style>
