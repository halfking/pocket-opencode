/**
 * Device tier + STT engine selection.
 *
 * Tiers map to the strategy in docs/2026-07-02-android-stt-evaluation.md
 * section 3. In production the native plugin probes device memory / CPU;
 * here we default to 'unknown' (auto) and let the user override in settings.
 */
import { defineStore } from 'pinia'

export type DeviceTier = 'high' | 'mid' | 'low' | 'unknown'

export const useDeviceStore = defineStore('device', {
  state: () => ({
    tier: (localStorage.getItem('pocket_device_tier') as DeviceTier) || 'unknown',
    /** User override: 'local' = always try on-device first; 'cloud' = prefer cloud. */
    sttPreference: (localStorage.getItem('pocket_stt_pref') as 'local' | 'cloud' | 'auto') || 'auto',
  }),
  getters: {
    shouldPreferLocal: (s) =>
      s.sttPreference === 'local' || (s.sttPreference === 'auto' && s.tier !== 'low'),
    recommendedModel: (s): 'paraformer' | 'sensevoice' | 'whisper-base' | 'vosk-small' => {
      if (s.tier === 'low') return 'vosk-small'
      return 'paraformer' // best Chinese CER on mid/high devices
    },
  },
  actions: {
    setTier(t: DeviceTier) {
      this.tier = t
      localStorage.setItem('pocket_device_tier', t)
    },
    setSttPreference(p: 'local' | 'cloud' | 'auto') {
      this.sttPreference = p
      localStorage.setItem('pocket_stt_pref', p)
    },
  },
})
