<template>
  <div v-if="status.failed || status.blocked || status.resetPending" role="alert" data-testid="portal-notes-sync-error">
    <p>{{ t(status.resetPending ? 'settings.storageNotesResetPending' : status.resetBlocked ? 'settings.storageNotesResetBlocked' : status.epochUnknown ? 'settings.storageNotesInitPending' : 'settings.storageNotesSyncPending') }}</p>
    <button v-if="!status.resetPending" type="button" :disabled="status.running" data-testid="portal-notes-sync-retry" @click="syncPortalNotesNow()">{{ t('common.retry') }}</button>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { portalNotesSyncStatus as status, syncPortalNotesNow } from '../../native/portal-sync/runtime'
const { t } = useI18n()
</script>
