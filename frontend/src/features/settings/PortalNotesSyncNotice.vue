<template>
  <div v-if="status.failed || status.blocked || status.resetPending" role="alert" data-testid="portal-notes-sync-error">
    <p>{{ t(status.resetPending ? 'settings.storageNotesResetPending' : status.resetBlocked ? 'settings.storageNotesResetBlocked' : status.epochUnknown ? 'settings.storageNotesInitPending' : 'settings.storageNotesSyncPending') }}</p>
    <button v-if="!status.resetPending" type="button" :disabled="status.running" data-testid="portal-notes-sync-retry" @click="syncPortalNotesNow()">{{ t('common.retry') }}</button>
    <button v-if="status.clockPending && !status.resetPending" type="button" :disabled="status.running" data-testid="portal-notes-clock-retry" @click="reaffirmClock">{{ t('settings.storageClockRetry') }}</button>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useConfirm } from '../../composables/useConfirm'
import { portalNotesSyncStatus as status, syncPortalNotesNow } from '../../native/portal-sync/runtime'
const { t } = useI18n()
const { confirm } = useConfirm()
function reaffirmClock() {
  const message = t('settings.storageClockConfirm', { count: status.clockPending })
  void syncPortalNotesNow(() => confirm({ message }))
}
</script>
