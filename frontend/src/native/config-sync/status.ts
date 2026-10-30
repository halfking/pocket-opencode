import { reactive } from 'vue'

/** UI reports only the current activation; old accounts cannot leave a warning
 * over a replacement session. No queued payload or credentials live here. */
export const userConfigSyncStatus = reactive({ capabilityUnavailable: false, activationVersion: -1 })
