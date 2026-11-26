<template>
  <Button v-if="eligible && !claimed" variant="secondary" size="small" :disabled="busy"
    data-testid="legacy-note-claim" @click="claim">{{ t('settings.legacyNoteClaim') }}</Button>
  <p v-if="claimed" role="status">{{ t('settings.legacyNoteClaimed') }}</p>
  <p v-if="failed" role="alert">{{ t('settings.legacyNoteClaimFailed') }}</p>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from '../../components/base/Button.vue'
import { useConfirm } from '../../composables/useConfirm'
import { useAuthStore } from '../../stores/auth'
import { canClaimLegacyNote, prepareLegacyNoteClaim, type LegacyNoteSource } from './legacy-note-claim'

const props = defineProps<{ source: LegacyNoteSource; noteId: string }>()
const auth = useAuthStore()
const { t } = useI18n()
const { confirm } = useConfirm()
const eligible = ref(false)
const busy = ref(false)
const claimed = ref(false)
const failed = ref(false)
let generation = 0

watch(() => [props.source, props.noteId, auth.activationVersion, auth.activationStatus], async () => {
  const current = ++generation
  eligible.value = busy.value = claimed.value = failed.value = false
  if (auth.activationStatus !== 'ready') return
  try {
    const available = await canClaimLegacyNote(props.source, props.noteId)
    if (current === generation) eligible.value = available
  } catch {
    // The existing detail page remains usable if the optional claim is unavailable.
  }
}, { immediate: true, flush: 'sync' })

onBeforeUnmount(() => { generation++ })

async function claim() {
  if (busy.value || !eligible.value) return
  const current = generation
  busy.value = true
  failed.value = false
  try {
    const original = await prepareLegacyNoteClaim(props.source, props.noteId)
    if (current !== generation) return
    const allowed = await confirm({ message: t('settings.legacyNoteClaimConfirm', { workspace: original.workspaceId }) })
    if (!allowed || current !== generation) return
    await original.claim()
    if (current === generation) claimed.value = true
  } catch {
    if (current === generation) failed.value = true
  } finally {
    if (current === generation) busy.value = false
  }
}
</script>
