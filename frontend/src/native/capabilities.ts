/**
 * Native capability detection + TS bridge contract (PR14 of optimization v4).
 *
 * Implements the capability-gated plugin contract from
 *   docs/优化v4/04-目标架构与领域拆分.md §3
 *   docs/优化v4/06-隐私安全与凭据边界.md
 *
 * Goals (PR14 boundary, see 14 §2 row 14):
 *   - Capability detection (recording, biometric, secure-storage, push)
 *     without requiring any specific Android plugin.
 *   - Strict TS contract that future native plugins can satisfy.
 *   - Capability flags respect the feature flag system from PR1 §7 so
 *     they can be turned off without code changes.
 *   - Web fallback always remains available; the caller is responsible
 *     for picking the right path.
 *
 * PR14 does NOT:
 *   - Implement the actual native plugin. The repo's Capacitor Android
 *     plugin set is owned by the Android/Capacitor track (see ADR-009).
 *   - Remove any existing Web fallback path.
 *   - Make hard claims about iOS support (per docs/优化v4/13 §3
 *     'no evidence → write 未确认').
 *   - Commit keystore files or audio samples.
 */

import { useFeatureFlag } from '../config/featureFlags'

/** Capability flags surfaced to UI and feature stores. */
export interface NativeCapabilities {
  /** Audio recording at the OS layer (Capacitor plugin available + permission grantable). */
  audioRecording: boolean
  /** Biometric authentication (fingerprint / face / device credential). */
  biometricAuth: boolean
  /** Hardware-backed secure storage (Keystore on Android). */
  secureStorage: boolean
  /** Background tasks / foreground services. */
  backgroundTask: boolean
  /** Push notifications (FCM / hms / apns). */
  push: boolean
  /** Active network reachability (best-effort, not authoritative). */
  networkReachable: boolean
}

const DEFAULT_CAPS: NativeCapabilities = {
  audioRecording: false,
  biometricAuth: false,
  secureStorage: false,
  backgroundTask: false,
  push: false,
  networkReachable: true,
}

/**
 * Probe the runtime for native capabilities. The probe is intentionally
 * cheap and synchronous; the actual native bridges can be injected by
 * the host (see withProbes below).
 *
 * Capability flags respect the PR1 §7 feature flag rules:
 *   realtime.ws_envelope_v1, approval.bottom_sheet_v1,
 *   approval.server_confirm_required, realtime.idempotent_ws_bus
 * are required for the related flows to be enabled.
 */
export function detectCapabilities(env: CapabilityEnv = {}): NativeCapabilities {
  const out: NativeCapabilities = { ...DEFAULT_CAPS, ...env.static }

  // Honour feature flags (PR1 §7). When a flag is false, the related
  // capability is reported as unavailable so callers can fall back.
  if (!useFeatureFlag('audio.voice_input_v1')) out.audioRecording = false
  if (!useFeatureFlag('security.keystore_v1')) out.secureStorage = false
  if (!useFeatureFlag('notifications.push_v1')) out.push = false
  if (!useFeatureFlag('background.task_v1')) out.backgroundTask = false

  return out
}

/** Environment passed to detectCapabilities. */
export interface CapabilityEnv {
  /** Static results from optional native probes. */
  static?: Partial<NativeCapabilities>
}

/** Helper: convenience for host code that wires actual native probes. */
export function withProbes(probes: Partial<NativeCapabilities>): () => NativeCapabilities {
  return () => detectCapabilities({ static: probes })
}

/** Decide whether the runtime can record audio. */
export function canRecordAudio(caps: NativeCapabilities): boolean {
  return caps.audioRecording && caps.networkReachable !== false
}

/** Decide whether the runtime can store secrets in hardware-backed storage. */
export function canUseSecureStorage(caps: NativeCapabilities): boolean {
  return caps.secureStorage
}

/** Decide whether biometric authentication is available. */
export function canUseBiometric(caps: NativeCapabilities): boolean {
  return caps.biometricAuth
}
