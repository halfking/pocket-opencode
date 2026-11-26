<template>
  <section v-if="available" class="device-backup" :aria-busy="saving">
    <h3>{{ t('settings.deviceBackupTitle') }}</h3>
    <p>{{ t('settings.deviceBackupScope') }}</p>
    <button v-if="!open" type="button" @click="open = true">{{ t('settings.deviceBackupExport') }}</button>
    <form v-else @submit.prevent="save">
      <label for="backup-master-password">{{ t('settings.deviceBackupPassword') }}</label>
      <input id="backup-master-password" v-model="password" type="password" autocomplete="current-password" required :disabled="saving">
      <div class="backup-actions">
        <button type="button" :disabled="saving" @click="close">{{ t('common.cancel') }}</button>
        <button type="submit" :disabled="saving || !password">{{ saving ? t('settings.deviceBackupWorking') : t('settings.deviceBackupSave') }}</button>
      </div>
    </form>
    <p v-if="notice" role="status">{{ notice }}</p>
    <p v-if="failure" role="alert">{{ failure }}</p>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { canUseDeviceBackup, describeDeviceBackupError, exportDeviceBackup } from '../../native/device-backup/service'
import { lobsterReady } from '../../native/lobster-init'

const { t } = useI18n()
const available = computed(() => lobsterReady.value && canUseDeviceBackup())
const open = ref(false)
const saving = ref(false)
const password = ref('')
const notice = ref('')
const failure = ref('')

function close() {
  open.value = false
  password.value = ''
  failure.value = ''
}

watch(available, enabled => { if (!enabled) close() })

async function save() {
  const master = password.value
  if (saving.value || !master) return
  notice.value = ''
  failure.value = ''
  saving.value = true
  password.value = ''
  try {
    if (await exportDeviceBackup(master)) {
      close()
      notice.value = t('settings.deviceBackupSaved')
    }
  } catch (error) {
    failure.value = t(describeDeviceBackupError(error, false))
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
.device-backup { display: grid; gap: 8px; padding: 12px; border: 1px solid var(--border); border-radius: var(--radius-md); }
.device-backup h3, .device-backup p { margin: 0; }
.device-backup h3 { font-size: var(--text-sm); }
.device-backup p, .device-backup label { color: var(--text-secondary); font-size: var(--text-smd); }
.device-backup form { display: grid; gap: 6px; }
.device-backup input { width: 100%; color: inherit; background: var(--bg-base); border: 1px solid var(--border); border-radius: var(--radius-md); padding: 8px; }
.device-backup button { justify-self: start; padding: 6px 12px; color: var(--brand-primary); background: var(--bg-base); border: 1px solid var(--border); border-radius: var(--radius-md); }
.device-backup [role="alert"] { color: var(--danger); }
.backup-actions { display: flex; gap: 8px; }
</style>
