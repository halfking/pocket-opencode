import { Capacitor } from '@capacitor/core'

export type RuntimePlatform = 'android' | 'ios' | 'harmony' | 'web'

export type HarmonyCapability =
  | 'camera'
  | 'notificationPermission'
  | 'localNotifications'
  | 'statusBar'
  | 'biometric'
  | 'encryptedLocalDb'
  | 'appLifecycle'
  | 'backButton'
  | 'appSettings'

interface HarmonyBridge {
  version: 1
  host: 'arkts-webview'
  capabilities: Partial<Record<HarmonyCapability, boolean>>
  invoke?: (capability: HarmonyCapability, method: string, args?: unknown) => Promise<unknown>
}

declare global {
  interface Window {
    __OPENCODE_POCKET_HARMONY__?: unknown
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  try {
    return typeof value === 'object' && value !== null && Object.getPrototypeOf(value) === Object.prototype
  } catch {
    return false
  }
}

function harmonyBridge(): HarmonyBridge | null {
  if (typeof window === 'undefined') return null

  try {
    const candidate = window.__OPENCODE_POCKET_HARMONY__
    if (!isRecord(candidate) || candidate.version !== 1 || candidate.host !== 'arkts-webview') return null
    if (!isRecord(candidate.capabilities)) return null
    return candidate as unknown as HarmonyBridge
  } catch {
    return null
  }
}

function capacitorPlatform(): 'android' | 'ios' | 'web' {
  const platform = Capacitor.getPlatform()
  return platform === 'android' || platform === 'ios' ? platform : 'web'
}

/**
 * Runtime identity is separate from capability detection. HarmonyOS must never
 * impersonate Capacitor, otherwise Android/iOS-only plugins become callable.
 */
export function runtimePlatform(): RuntimePlatform {
  const platform = capacitorPlatform()
  if (platform !== 'web') return platform
  return harmonyBridge() !== null ? 'harmony' : 'web'
}

export function isHarmonyWebView(): boolean {
  return runtimePlatform() === 'harmony'
}

export function isWebFallbackRuntime(): boolean {
  const platform = runtimePlatform()
  return platform === 'web' || platform === 'harmony'
}

export interface NativeCapabilityFallback {
  audioRecording: boolean
  biometricAuth: boolean
  secureStorage: boolean
  backgroundTask: boolean
  push: boolean
}

/** Keeps Phase A on audited Web fallbacks until each ArkTS bridge is verified. */
export function clampHarmonyNativeCapabilities<T extends NativeCapabilityFallback>(caps: T): T {
  if (!isHarmonyWebView()) return caps
  return {
    ...caps,
    audioRecording: false,
    biometricAuth: false,
    secureStorage: false,
    backgroundTask: false,
    push: false,
  }
}

export function hasHarmonyCapability(capability: HarmonyCapability): boolean {
  const bridge = harmonyBridge()
  return runtimePlatform() === 'harmony'
    && bridge?.capabilities[capability] === true
    && typeof bridge.invoke === 'function'
}

/**
 * Calls are fail-closed: a malformed or unavailable bridge is indistinguishable
 * from a missing native capability, so callers can retain their Web fallback.
 */
export async function invokeHarmony<T>(
  capability: HarmonyCapability,
  method: string,
  args?: unknown,
): Promise<T | null> {
  const bridge = harmonyBridge()
  if (!hasHarmonyCapability(capability) || !bridge?.invoke) return null

  try {
    return await bridge.invoke(capability, method, args) as T
  } catch {
    return null
  }
}
