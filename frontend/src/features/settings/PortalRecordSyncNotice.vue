<template>
  <div
    v-if="runtime.status.failed || runtime.status.blocked || runtime.status.resetPending"
    role="alert"
    :data-testid="`portal-${module}-sync-error`"
  >
    <p>
      {{
        t(
          runtime.status.resetPending
            ? 'settings.storageModuleResetPending'
            : runtime.status.resetBlocked
              ? 'settings.storageModuleResetBlocked'
              : runtime.status.epochUnknown
                ? 'settings.storageModuleInitPending'
                : 'settings.storageModuleSyncPending',
          { module: t(nameKey) },
        )
      }}
    </p>
    <button
      v-if="!runtime.status.resetPending"
      type="button"
      :disabled="runtime.status.running"
      :data-testid="`portal-${module}-sync-retry`"
      @click="runtime.syncNow()"
    >
      {{ t('common.retry') }}
    </button>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { RecordRuntime } from '../../native/portal-sync/record-runtime.ts'
import type { RecordModule } from '../../native/portal-sync/epochs.ts'
defineProps<{ runtime: RecordRuntime; module: RecordModule; nameKey: string }>()
const { t } = useI18n()
</script>
