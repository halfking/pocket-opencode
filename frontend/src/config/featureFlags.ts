/**
 * Feature flag registry (PR1 §7).
 *
 * Default values must match the legacy behavior so that flipping the
 * flag off restores the pre-flag flow. Flags are read from a single
 * source (`flags` constant) so REL/PO can audit the inventory in one
 * place.
 *
 * Per PR1 §7:
 *   - Names follow `<scope>.<feature>.v<n>` (e.g.
 *     `realtime.ws_envelope_v1`).
 *   - Default value MUST equal the legacy behavior; introducing a new
 *     behavior with `default=true` is forbidden.
 *   - Every flag must be removed or promoted to a constant within 90
 *     days; the `createdAt` field powers a future lint.
 */

export interface FeatureFlagEntry {
  key: string
  description: string
  defaultValue: boolean
  /** Server can override; this is informational. */
  serverOverrideable: boolean
  /** Creation date (ISO). Used by the lint that flags >90d old flags. */
  createdAt: string
}

export const flags: Record<string, FeatureFlagEntry> = {
  'realtime.ws_envelope_v1': {
    key: 'realtime.ws_envelope_v1',
    description: 'Use the v1 envelope normaliser from PR5 (idempotentWsBus).',
    defaultValue: false,
    serverOverrideable: true,
    createdAt: '2026-08-14',
  },
  'realtime.idempotent_ws_bus': {
    key: 'realtime.idempotent_ws_bus',
    description: 'Wrap wsClient with the dedupe dispatcher from PR5.',
    defaultValue: false,
    serverOverrideable: true,
    createdAt: '2026-08-14',
  },
  'approval.bottom_sheet_v1': {
    key: 'approval.bottom_sheet_v1',
    description: 'Render the new ApprovalBottomSheet from PR8.',
    defaultValue: false,
    serverOverrideable: false,
    createdAt: '2026-08-14',
  },
  'approval.server_confirm_required': {
    key: 'approval.server_confirm_required',
    description: 'Refuse to flip a decision to approved until the server confirms.',
    defaultValue: true,
    serverOverrideable: false,
    createdAt: '2026-08-14',
  },
  'audio.voice_input_v1': {
    key: 'audio.voice_input_v1',
    description: 'Enable the native voice recording path; keep Web fallback.',
    defaultValue: false,
    serverOverrideable: false,
    createdAt: '2026-08-14',
  },
  'security.keystore_v1': {
    key: 'security.keystore_v1',
    description: 'Use hardware-backed secure storage (Android Keystore).',
    defaultValue: false,
    serverOverrideable: false,
    createdAt: '2026-08-14',
  },
  'notifications.push_v1': {
    key: 'notifications.push_v1',
    description: 'Enable push notifications.',
    defaultValue: false,
    serverOverrideable: false,
    createdAt: '2026-08-14',
  },
  'background.task_v1': {
    key: 'background.task_v1',
    description: 'Enable background task / foreground service.',
    defaultValue: false,
    serverOverrideable: false,
    createdAt: '2026-08-14',
  },
}

export function useFeatureFlag(key: string): boolean {
  const entry = flags[key]
  if (!entry) {
    // Unknown flag → default to legacy behaviour (false). This is
    // safer than throwing because consumers can call this in hot paths
    // without try/catch.
    return false
  }
  return entry.defaultValue
}

export function listFlags(): FeatureFlagEntry[] {
  return Object.values(flags)
}
